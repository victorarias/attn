package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The invariant: a denial that happened is a denial you can read later, even
// when the relay report that normally carries it was lost.

func ledgerLine(session, action, at string) string {
	return fmt.Sprintf(
		`{"session_id":%q,"tool_call_id":"c1","tool":"bash","action":%q,"reason":"outside the envelope","rule":"classifier-2a","at":%q}`,
		session, action, at)
}

// writeSessionLedger puts a ledger where this daemon's sessions would have
// written one, and points the daemon at it.
func writeSessionLedger(t *testing.T, lines ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), automode.DenialLedgerFileName)
	contents := ""
	for _, line := range lines {
		contents += line + "\n"
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing the ledger: %v", err)
	}
	t.Setenv(automode.DenialLedgerEnvVar, path)
}

func listDenials(t *testing.T, d *Daemon) *protocol.AutoModeDenialsResult {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleAutoModeDenials(c, &protocol.AutoModeDenialsMessage{Cmd: protocol.CmdAutoModeDenials})
	})
	if !resp.Ok {
		t.Fatalf("denials: %v", protocol.Deref(resp.Error))
	}
	return resp.AutomodeDenialsResult
}

// The 2026-08-17 episode: the breaker said four calls were refused and the feed
// held none of them, because the relay socket was gone. The record on disk is
// what closes that gap.
func TestDenialsFeedRecoversWhatTheRelayNeverDelivered(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t,
		ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"),
		ledgerLine("pi-1", "write /etc/hosts", "2026-08-18T10:00:01.000Z"),
	)

	result := listDenials(t, d)
	if len(result.Denials) != 2 {
		t.Fatalf("denials = %+v, want both recovered from the ledger", result.Denials)
	}
	if result.Denials[0].Signature != "write /etc/hosts" {
		t.Errorf("newest denial = %q", result.Denials[0].Signature)
	}
	if result.Denials[0].Rule != "classifier-2a" || result.Denials[0].SessionID != "pi-1" {
		t.Errorf("a recovered denial lost its attribution: %+v", result.Denials[0])
	}
}

// The ordinary case is that the relay worked. Folding the ledger in must not
// double what is already there — on this read or on any later one.
func TestReconcileDoesNotDoubleWhatTheRelayDelivered(t *testing.T) {
	d := newDaemonForTest(t)
	at := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	if _, _, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
		SessionID: "pi-1", Tool: "bash", Signature: "bash: curl https://one.example",
		Reason: "outside the envelope", Rule: "classifier-2a",
	}, at); err != nil {
		t.Fatalf("record denial: %v", err)
	}
	writeSessionLedger(t, ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"))

	for round := 1; round <= 3; round++ {
		if listed := listDenials(t, d).Denials; len(listed) != 1 {
			t.Fatalf("round %d listed %d denials, want the one that happened", round, len(listed))
		}
	}
}

// A record the store's own row cap has since trimmed must not come back on the
// next read and be trimmed again on the one after, forever. The cursor is what
// stops that, so it is what is pinned here: a record at or before it is already
// imported, whether or not a row for it is still in the log.
func TestReconcileImportsARecordOnlyOnce(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t, ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"))

	first := d.reconcileAutoModeDenialLedger()
	if first.Imported != 1 {
		t.Fatalf("the first pass imported %d records, want the one the relay lost", first.Imported)
	}
	if cursor := d.store.GetSetting(settingAutoModeDenialCursor); !strings.HasPrefix(cursor, "2026-08-18T10:00:00") {
		t.Fatalf("cursor = %q, want the record it just imported", cursor)
	}
	if again := d.reconcileAutoModeDenialLedger(); again.Imported != 0 {
		t.Errorf("a second pass imported %d records", again.Imported)
	}

}

// The row the import created is gone — trimmed by the log's own cap — and the
// ledger still holds the record. The cursor alone decides, so it does not come
// back.
func TestReconcileLeavesARecordTheLogHasSinceTrimmed(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t, ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"))
	d.store.SetSetting(settingAutoModeDenialCursor, "2026-08-18T10:00:00.000Z")

	result := listDenials(t, d)
	if len(result.Denials) != 0 {
		t.Fatalf("a record already behind the cursor came back: %+v", result.Denials)
	}
}

// A ledger that clipped says so, and the reader is told rather than shown a
// partial episode as if it were whole.
func TestDenialsFeedNamesWhatTheLedgerLost(t *testing.T) {
	d := newDaemonForTest(t)
	writeSessionLedger(t,
		`{"type":"rotated","dropped":3,"at":"2026-08-18T09:00:00.000Z"}`,
		"{ not json",
		ledgerLine("pi-1", "bash: curl https://one.example", "2026-08-18T10:00:00.000Z"),
	)

	result := listDenials(t, d)
	note := protocol.Deref(result.LedgerNote)
	if !strings.Contains(note, "3 older denials") {
		t.Errorf("note does not name the dropped records: %q", note)
	}
	if !strings.Contains(note, "1 ledger line could not be read") {
		t.Errorf("note does not name the unreadable line: %q", note)
	}
	if len(result.Denials) != 1 {
		t.Errorf("the readable record was lost with the unreadable one: %+v", result.Denials)
	}
}

// Nothing to reconcile is the common case, and it must cost nothing and say
// nothing.
func TestDenialsFeedIsSilentWithoutALedger(t *testing.T) {
	d := newDaemonForTest(t)
	t.Setenv(automode.DenialLedgerEnvVar, filepath.Join(t.TempDir(), automode.DenialLedgerFileName))

	result := listDenials(t, d)
	if len(result.Denials) != 0 {
		t.Fatalf("a machine that denied nothing has denials: %+v", result.Denials)
	}
	if note := protocol.Deref(result.LedgerNote); note != "" {
		t.Errorf("an absent ledger reported %q", note)
	}
}
