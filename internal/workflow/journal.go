package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// schemaNoneSentinel is the schemaHash for an absent/empty schema. Hashing absence
// to a stable sentinel (rather than the empty string) is what makes a text->schema
// or schema->text transition flip schemaHash (R-spec R5).
const schemaNoneSentinel = "none"

// hashPrompt returns the sha256 hex of the resolved prompt string.
func hashPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// hashSchema returns the sha256 hex of the schema bytes, or the "none" sentinel
// when the schema is absent/empty. In E1 the schema is always empty, but the
// sentinel + hashing path is wired so R5 (schema change) is testable.
func hashSchema(schema json.RawMessage) string {
	if len(schema) == 0 {
		return schemaNoneSentinel
	}
	sum := sha256.Sum256(schema)
	return hex.EncodeToString(sum[:])
}

// JournalEntry is one recorded agent() call.
type JournalEntry struct {
	Ordinal    string          // OrdinalPath.String()
	PromptHash string          // sha256 of the resolved prompt
	SchemaHash string          // sha256 of schema bytes, or "none" sentinel
	Result     json.RawMessage // canned fake result; null on skipped/errored
	Status     string          // "ok" | "skipped" | "errored"
	Err        string          // diagnostics for skipped/errored
}

// Journal is the durable-swappable persistence interface. E1 ships MemJournal; a
// SQLite-backed impl can drop in for E4 without touching the engine.
type Journal interface {
	// Lookup returns the entry recorded at ordinal, if any.
	Lookup(ordinal string) (JournalEntry, bool)
	// Append records a freshly-executed live call at a not-yet-present ordinal.
	// It MUST reject a duplicate ordinal (one entry per ordinal invariant).
	Append(JournalEntry) error
	// Upsert records a live call, overwriting any stale entry at the same ordinal
	// (the divergence-overwrite path on resume).
	Upsert(JournalEntry)
	// Entries returns all entries in append order (for assertions/snapshotting).
	Entries() []JournalEntry
}

// IsCacheHit is the resume match predicate: a journaled entry is a hit iff its
// ordinal, prompt hash, and schema hash all match the live call. The model is
// deliberately OUT of the predicate (R-spec §3).
func IsCacheHit(e JournalEntry, ordinal, promptHash, schemaHash string) bool {
	return e.Ordinal == ordinal && e.PromptHash == promptHash && e.SchemaHash == schemaHash
}

// MemJournal is the in-memory E1 Journal implementation.
type MemJournal struct {
	byOrdinal map[string]int // ordinal -> index into order
	order     []JournalEntry
}

// NewMemJournal returns an empty in-memory journal.
func NewMemJournal() *MemJournal {
	return &MemJournal{byOrdinal: map[string]int{}}
}

// Clone returns a deep copy so a prior run's journal can seed a Resume without the
// resume mutating the original (tests rely on this).
func (m *MemJournal) Clone() *MemJournal {
	out := NewMemJournal()
	out.order = make([]JournalEntry, len(m.order))
	copy(out.order, m.order)
	for k, v := range m.byOrdinal {
		out.byOrdinal[k] = v
	}
	return out
}

func (m *MemJournal) Lookup(ordinal string) (JournalEntry, bool) {
	idx, ok := m.byOrdinal[ordinal]
	if !ok {
		return JournalEntry{}, false
	}
	return m.order[idx], true
}

func (m *MemJournal) Append(e JournalEntry) error {
	if _, exists := m.byOrdinal[e.Ordinal]; exists {
		return fmt.Errorf("journal: duplicate ordinal %q", e.Ordinal)
	}
	m.byOrdinal[e.Ordinal] = len(m.order)
	m.order = append(m.order, e)
	return nil
}

func (m *MemJournal) Upsert(e JournalEntry) {
	if idx, exists := m.byOrdinal[e.Ordinal]; exists {
		m.order[idx] = e
		return
	}
	m.byOrdinal[e.Ordinal] = len(m.order)
	m.order = append(m.order, e)
}

func (m *MemJournal) Entries() []JournalEntry {
	out := make([]JournalEntry, len(m.order))
	copy(out, m.order)
	return out
}
