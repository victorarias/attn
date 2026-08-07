package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func newSessionAnnotationTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

const testAnnotations = `[{"id":"a1","message_key":"turn-1","start":4,"end":10,"quote":"parser","emoji":"❓","comment":"why this?"}]`

func TestSessionAnnotationDraftSaveGetRoundtrip(t *testing.T) {
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 1, now); err != nil {
		t.Fatalf("save gen 1: %v", err)
	}

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != testAnnotations {
		t.Errorf("annotations = %q, want %q", draft.Annotations, testAnnotations)
	}
	if draft.Generation != 1 {
		t.Errorf("generation = %d, want 1", draft.Generation)
	}
	if draft.UpdatedAt != "2026-08-02T12:00:00Z" {
		t.Errorf("updated_at = %q, want RFC3339 of save time", draft.UpdatedAt)
	}
}

func TestSessionAnnotationDraftMissingRowIsEmptyGenZero(t *testing.T) {
	s := newSessionAnnotationTestStore(t)

	draft, err := s.GetSessionAnnotationDraft("never-annotated")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if draft.Annotations != "[]" || draft.Generation != 0 {
		t.Errorf("missing row draft = %+v, want empty list gen 0", draft)
	}
}

func TestSessionAnnotationDraftRejectsAnOlderGeneration(t *testing.T) {
	// Two panes on the same session, or one pane whose writes arrive out of
	// order. The older list must not overwrite the newer one.
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 4, now); err != nil {
		t.Fatalf("save gen 4: %v", err)
	}
	err := s.SaveSessionAnnotationDraft("session-1", `[]`, "", 3, now)
	if !errors.Is(err, ErrStaleAnnotationSave) {
		t.Fatalf("save gen 3 after gen 4 = %v, want ErrStaleAnnotationSave", err)
	}

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != testAnnotations {
		t.Errorf("annotations = %q, want the newer list untouched", draft.Annotations)
	}
}

func TestSessionAnnotationDraftTombstoneRefusesAnInFlightSave(t *testing.T) {
	// Sending the set to the agent clears it. A save already in flight when
	// that happened must not put the sent marks back on the message.
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 1, now); err != nil {
		t.Fatalf("save gen 1: %v", err)
	}
	if err := s.ClearSessionAnnotationDraft("session-1", 2, now); err != nil {
		t.Fatalf("clear gen 2: %v", err)
	}

	err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 2, now)
	if !errors.Is(err, ErrStaleAnnotationSave) {
		t.Fatalf("save at the tombstone's generation = %v, want ErrStaleAnnotationSave", err)
	}

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != "[]" {
		t.Errorf("annotations = %q, want the cleared list", draft.Annotations)
	}
	// The floor a re-mounting client seeds from has to include the tombstone,
	// or its first save is refused for reasons it cannot see.
	if draft.Generation != 2 {
		t.Errorf("generation = %d, want the tombstone's 2", draft.Generation)
	}
}

func TestSessionAnnotationDraftClearWorksOnASessionNeverAnnotated(t *testing.T) {
	// The tombstone IS the row: a client that sends before any save landed
	// still has to be able to refuse that save.
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.ClearSessionAnnotationDraft("session-1", 3, now); err != nil {
		t.Fatalf("clear on a missing row: %v", err)
	}
	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 3, now); !errors.Is(err, ErrStaleAnnotationSave) {
		t.Fatalf("save at the tombstone = %v, want ErrStaleAnnotationSave", err)
	}
}

func TestSessionAnnotationDraftIsKeyedPerSession(t *testing.T) {
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 1, now); err != nil {
		t.Fatalf("save session-1: %v", err)
	}

	draft, err := s.GetSessionAnnotationDraft("session-2")
	if err != nil {
		t.Fatalf("get session-2: %v", err)
	}
	if draft.Annotations != "[]" {
		t.Errorf("session-2 annotations = %q, want its own empty draft", draft.Annotations)
	}
	// Sharing the implementation with markdown drafts must not mean sharing
	// the table: a session id is not a file path.
	md, err := s.GetMarkdownAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get markdown draft: %v", err)
	}
	if md.Annotations != "[]" {
		t.Errorf("markdown draft = %q, want the session draft to be in its own table", md.Annotations)
	}
}

func TestRemoveSessionDeletesItsAnnotationDraft(t *testing.T) {
	// A session id is never reused, so a tombstone left behind is a row nobody
	// can reach. The draft goes with the session.
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	s.Add(&protocol.Session{
		ID:         "session-1",
		Label:      "annotated",
		Directory:  "/tmp/project",
		State:      protocol.SessionStateIdle,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	})
	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 5, now); err != nil {
		t.Fatalf("save: %v", err)
	}

	s.Remove("session-1")

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get after remove: %v", err)
	}
	if draft.Annotations != "[]" || draft.Generation != 0 {
		t.Errorf("draft after remove = %+v, want nothing left behind", draft)
	}
}

func TestClearSessionsDeletesAnnotationDrafts(t *testing.T) {
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	s.Add(&protocol.Session{
		ID:         "session-1",
		Label:      "annotated",
		Directory:  "/tmp/project",
		State:      protocol.SessionStateIdle,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	})
	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "", 5, now); err != nil {
		t.Fatalf("save: %v", err)
	}

	s.ClearSessions()

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if draft.Annotations != "[]" || draft.Generation != 0 {
		t.Errorf("draft after ClearSessions = %+v, want nothing left behind", draft)
	}
}

func TestSessionAnnotationDraftCarriesItsNote(t *testing.T) {
	// The note is the instruction the marks qualify. It is drafted over the
	// same minutes they are, so it has to survive a reopened pane the same way.
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "Split this into two PRs.", 1, now); err != nil {
		t.Fatalf("save: %v", err)
	}

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Note != "Split this into two PRs." {
		t.Errorf("note = %q, want the saved note", draft.Note)
	}
}

func TestSessionAnnotationDraftNoteIsEmptyBeforeAnythingIsSaved(t *testing.T) {
	s := newSessionAnnotationTestStore(t)

	draft, err := s.GetSessionAnnotationDraft("never-annotated")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if draft.Note != "" {
		t.Errorf("note = %q, want empty", draft.Note)
	}
}

func TestClearSessionAnnotationDraftClearsTheNoteToo(t *testing.T) {
	// Clearing is what a send does. Leaving the note behind would put the
	// instruction that was just delivered back on the next turn.
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "Split this into two PRs.", 1, now); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.ClearSessionAnnotationDraft("session-1", 2, now); err != nil {
		t.Fatalf("clear: %v", err)
	}

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Note != "" || draft.Annotations != "[]" {
		t.Errorf("draft after clear = %+v, want the note gone with the marks", draft)
	}
}

func TestSessionAnnotationDraftKeepsTheNoteOfTheGenerationThatWon(t *testing.T) {
	// A refused save must not leave its note behind: the note and the marks
	// are one draft, and a half-applied one is a draft the user never wrote.
	s := newSessionAnnotationTestStore(t)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	if err := s.SaveSessionAnnotationDraft("session-1", testAnnotations, "the newer note", 4, now); err != nil {
		t.Fatalf("save gen 4: %v", err)
	}
	if err := s.SaveSessionAnnotationDraft("session-1", `[]`, "the older note", 3, now); !errors.Is(err, ErrStaleAnnotationSave) {
		t.Fatalf("save gen 3 after gen 4 = %v, want ErrStaleAnnotationSave", err)
	}

	draft, err := s.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Note != "the newer note" {
		t.Errorf("note = %q, want the newer note untouched", draft.Note)
	}
}

func TestMigration93KeepsDraftsWrittenBeforeTheNoteExisted(t *testing.T) {
	// `attn` has shipped session annotation drafts since schema 86, so real
	// installs hold rows written by a build that had no note column. They are
	// carried, not recreated: the marks on them are work the user did.
	dbPath := filepath.Join(t.TempDir(), "pre-93.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}
	if _, err := db.Exec(`
		ALTER TABLE session_annotation_drafts DROP COLUMN note;
		INSERT INTO session_annotation_drafts
			(session_id, annotations_json, generation, tombstone_generation, updated_at)
			VALUES ('session-1', '` + testAnnotations + `', 6, 0, '2026-08-02T12:00:00Z');
		DELETE FROM schema_migrations WHERE version >= 93;
	`); err != nil {
		t.Fatalf("seed a pre-93 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-93 database: %v", err)
	}

	migrated, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer migrated.Close()

	draft, err := migrated.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != testAnnotations || draft.Generation != 6 {
		t.Errorf("draft after migration = %+v, want the marks and generation carried", draft)
	}
	if draft.Note != "" {
		t.Errorf("note = %q, want empty for a draft written before it existed", draft.Note)
	}
}
