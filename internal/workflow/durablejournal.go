package workflow

import (
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/store"
)

// WorkflowJournalStore is the narrow store surface the DurableJournal depends on.
// It is satisfied by *store.Store but keeps the workflow package decoupled from
// the concrete store type (and the workflow package's existing tests store-free).
type WorkflowJournalStore interface {
	UpsertWorkflowAgentCall(call *store.WorkflowAgentCallRow) error
	ListWorkflowAgentCalls(runID string) ([]*store.WorkflowAgentCallRow, error)
}

// DurableJournal is a SQLite-backed Journal. It keeps an in-memory write-through
// mirror (a MemJournal) for the hot Lookup/Entries read paths, seeded from the DB
// at construction so a fresh process can resume a prior run by reading persisted
// rows. Every Append/Upsert is mirrored AND written through to the store.
type DurableJournal struct {
	store   WorkflowJournalStore
	runID   string
	mirror  *MemJournal
	lastErr error
}

// NewDurableJournal builds a DurableJournal for runID, seeding its in-memory mirror
// from persisted rows in durable append order (id ASC). Reconstructing the mirror
// from SQLite is what makes durable resume work across a fresh process: a new
// adapter for a prior runID rebuilds the prior journal.
func NewDurableJournal(s WorkflowJournalStore, runID string) *DurableJournal {
	dj := &DurableJournal{
		store:  s,
		runID:  runID,
		mirror: NewMemJournal(),
	}
	rows, err := s.ListWorkflowAgentCalls(runID)
	if err != nil {
		dj.lastErr = err
		return dj
	}
	for _, row := range rows {
		// Seed in id-ASC order; Upsert preserves order and is duplicate-safe.
		dj.mirror.Upsert(entryFromRow(row))
	}
	return dj
}

// Lookup returns the mirrored entry at ordinal (no DB hit). Matches MemJournal.
func (d *DurableJournal) Lookup(ordinal string) (JournalEntry, bool) {
	return d.mirror.Lookup(ordinal)
}

// Append records a freshly-executed live call, enforcing the one-entry-per-ordinal
// invariant against the mirror first (identical error to MemJournal.Append), then
// writing through to the store. A store write failure is returned (Append is the
// only Journal method that returns an error) and the mirror is left unchanged.
func (d *DurableJournal) Append(e JournalEntry) error {
	if _, exists := d.mirror.Lookup(e.Ordinal); exists {
		return fmt.Errorf("journal: duplicate ordinal %q", e.Ordinal)
	}
	if err := d.store.UpsertWorkflowAgentCall(rowFromEntry(d.runID, e)); err != nil {
		return err
	}
	// Mirror invariant already checked above, so Append cannot fail here.
	_ = d.mirror.Append(e)
	return nil
}

// Upsert records a live call, overwriting any stale entry at the same ordinal (the
// divergence-overwrite path). The Journal interface gives Upsert no error return,
// so a store write failure is captured in lastErr (readable via Err) rather than
// dropped silently; the mirror stays authoritative for in-run reads regardless.
func (d *DurableJournal) Upsert(e JournalEntry) {
	d.mirror.Upsert(e)
	if err := d.store.UpsertWorkflowAgentCall(rowFromEntry(d.runID, e)); err != nil {
		d.lastErr = err
	}
}

// Entries returns all mirrored entries in append order (no DB hit). Matches
// MemJournal.Entries.
func (d *DurableJournal) Entries() []JournalEntry {
	return d.mirror.Entries()
}

// Err returns the first store write error observed during a silent (Upsert) write,
// or a seed/list error from construction. Append surfaces its own error directly.
func (d *DurableJournal) Err() error {
	return d.lastErr
}

// rowFromEntry maps a JournalEntry to a store row for the fixed runID. Only the six
// JournalEntry fields are round-tripped; the richer columns (label, phase, model,
// harness, etc.) are owned by other (out-of-scope) write paths and left nil here.
func rowFromEntry(runID string, e JournalEntry) *store.WorkflowAgentCallRow {
	return &store.WorkflowAgentCallRow{
		RunID:      runID,
		Ordinal:    e.Ordinal,
		PromptHash: ptrIfNonEmpty(e.PromptHash),
		SchemaHash: ptrIfNonEmpty(e.SchemaHash),
		ResultJSON: rawMessageToPtr(e.Result),
		Status:     e.Status,
		Error:      ptrIfNonEmpty(e.Err),
	}
}

// entryFromRow maps a store row back to a JournalEntry. The round-trip is lossless
// for all six JournalEntry fields, which is the only correctness requirement:
// IsCacheHit depends on Ordinal+PromptHash+SchemaHash, and replay uses Result/Status.
func entryFromRow(row *store.WorkflowAgentCallRow) JournalEntry {
	return JournalEntry{
		Ordinal:    row.Ordinal,
		PromptHash: derefOr(row.PromptHash),
		SchemaHash: derefOr(row.SchemaHash),
		Result:     ptrToRawMessage(row.ResultJSON),
		Status:     row.Status,
		Err:        derefOr(row.Error),
	}
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func rawMessageToPtr(r json.RawMessage) *string {
	if len(r) == 0 {
		return nil
	}
	s := string(r)
	return &s
}

func ptrToRawMessage(s *string) json.RawMessage {
	if s == nil {
		return nil
	}
	return json.RawMessage(*s)
}
