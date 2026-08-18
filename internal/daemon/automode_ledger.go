package daemon

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
)

// Reconciling auto mode's denial ledger into the denials log.
//
// A pi session writes every refused call to a local file before it reports the
// denial over the relay (plugins/attn-pi/automode/ledger.ts). The report is
// what makes a denial visible live — a notification, a fact, a row — and it can
// be lost: a bare pi has no relay at all, and a relay whose socket died (a
// plugin reload is the ordinary way) drops what it is handed. The file cannot
// be lost, so it is what the log is reconciled against before anyone reads it.
//
// This runs on the read path because that is the moment the truth is wanted,
// and because it is the only moment both readers — `attn automode denials` and
// the app's settings section — share. A machine where nothing was ever refused
// has no file, so it does nothing at all.
//
// Design: docs/plans/2026-08-18-automode-denial-ledger.md.

// settingAutoModeDenialCursor remembers the newest ledger record already
// imported. Without it, a record the store's own row cap has since trimmed
// would be re-imported on the next read, and trimmed again on the one after —
// forever.
const settingAutoModeDenialCursor = "automode_denial_ledger_cursor"

// autoModeDenialLedgerPath is the file this daemon's sessions write to. Also
// what the plugin runtime is told, so the pi driver can forward it.
func autoModeDenialLedgerPath() string {
	if override := strings.TrimSpace(os.Getenv(automode.DenialLedgerEnvVar)); override != "" {
		return override
	}
	return automode.DenialLedgerPath(config.DataDir())
}

// autoModeLedgerReconcile is one read's worth of what the ledger admits it lost,
// for the reader to be told about.
type autoModeLedgerReconcile struct {
	Imported  int
	Dropped   int
	Malformed int
}

var autoModeLedgerMu sync.Mutex

// reconcileAutoModeDenialLedger imports every denial the ledger holds that the
// log does not, and returns what the ledger says it clipped. Errors are logged
// and swallowed: a ledger that cannot be read must not take the denials the
// store already holds down with it.
func (d *Daemon) reconcileAutoModeDenialLedger() autoModeLedgerReconcile {
	if d.store == nil {
		return autoModeLedgerReconcile{}
	}
	// Two clients asking at once would otherwise both import the same records:
	// the cursor is read, compared and written across the whole pass.
	autoModeLedgerMu.Lock()
	defer autoModeLedgerMu.Unlock()

	path := autoModeDenialLedgerPath()
	reading, err := automode.ReadDenialLedger(path)
	if err != nil {
		d.logf("automode: reading denial ledger %s: %v", path, err)
		return autoModeLedgerReconcile{}
	}
	if len(reading.Records) == 0 && reading.Dropped == 0 && reading.Malformed == 0 {
		return autoModeLedgerReconcile{}
	}

	cursor := parseAutoModeDenialCursor(d.store.GetSetting(settingAutoModeDenialCursor))
	stored, err := d.store.ListAutoModeDenials(store.AutoModeDenialRows)
	if err != nil {
		d.logf("automode: listing denials to reconcile the ledger: %v", err)
		return autoModeLedgerReconcile{}
	}
	known := make(map[string]struct{}, len(stored))
	for _, denial := range stored {
		known[autoModeDenialKey(denial.SessionID, denial.Signature, denial.CreatedAt)] = struct{}{}
	}

	out := autoModeLedgerReconcile{Dropped: reading.Dropped, Malformed: reading.Malformed}
	newest := cursor
	for _, record := range reading.Records {
		if record.At.After(newest) {
			newest = record.At
		}
		// Already imported once, whether or not the row survives today's cap.
		if !record.At.After(cursor) {
			continue
		}
		if _, ok := known[autoModeDenialKey(record.SessionID, record.Action, record.At)]; ok {
			continue
		}
		if _, _, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
			SessionID: record.SessionID,
			Tool:      record.Tool,
			Signature: record.Action,
			Reason:    record.Reason,
			Rule:      record.Rule,
		}, record.At); err != nil {
			d.logf("automode: importing a denial from the ledger: %v", err)
			// Leave the cursor where it was so this record is tried again.
			return out
		}
		out.Imported++
		d.logf("automode: recovered a denial the relay never delivered session=%s rule=%s action=%q at=%s",
			record.SessionID, record.Rule, record.Action, record.At.Format(time.RFC3339))
	}
	if newest.After(cursor) {
		d.store.SetSetting(settingAutoModeDenialCursor, newest.UTC().Format(time.RFC3339Nano))
	}
	if out.Dropped > 0 {
		d.logf("automode: the denial ledger has dropped %d records to rotation", out.Dropped)
	}
	if out.Malformed > 0 {
		d.logf("automode: the denial ledger holds %d unreadable lines", out.Malformed)
	}
	return out
}

// autoModeDenialKey identifies one denial across the two ways it can arrive.
// The relay stores the session's own timestamp verbatim, so the same denial
// carries the same instant whichever path delivered it.
func autoModeDenialKey(sessionID, signature string, at time.Time) string {
	return fmt.Sprintf("%s|%s|%s", sessionID, at.UTC().Format(time.RFC3339Nano), signature)
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func parseAutoModeDenialCursor(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

// autoModeLedgerNote is the one line a reader is shown when the ledger lost
// something. Empty when it lost nothing, which is the ordinary case.
func autoModeLedgerNote(reconcile autoModeLedgerReconcile) string {
	parts := []string{}
	if reconcile.Dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d older %s dropped when the local ledger rotated",
			reconcile.Dropped, plural(reconcile.Dropped, "denial was", "denials were")))
	}
	if reconcile.Malformed > 0 {
		parts = append(parts, fmt.Sprintf("%d ledger %s not be read",
			reconcile.Malformed, plural(reconcile.Malformed, "line could", "lines could")))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}
