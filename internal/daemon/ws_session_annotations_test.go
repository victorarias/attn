package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func annotationsGet(t *testing.T, d *Daemon, sessionID string) protocol.SessionAnnotationsGetResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleSessionAnnotationsGet(client, &protocol.SessionAnnotationsGetMessage{
		Cmd:       protocol.CmdSessionAnnotationsGet,
		SessionID: sessionID,
		RequestID: "req-get",
	})
	var msg protocol.SessionAnnotationsGetResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	return msg
}

func annotationsSave(t *testing.T, d *Daemon, sessionID string, generation int, annotations []protocol.SessionAnnotation) protocol.SessionAnnotationsSaveResultMessage {
	t.Helper()
	return annotationsSaveWithNote(t, d, sessionID, generation, annotations, "")
}

func annotationsSaveWithNote(t *testing.T, d *Daemon, sessionID string, generation int, annotations []protocol.SessionAnnotation, note string) protocol.SessionAnnotationsSaveResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleSessionAnnotationsSave(client, &protocol.SessionAnnotationsSaveMessage{
		Cmd:         protocol.CmdSessionAnnotationsSave,
		SessionID:   sessionID,
		Annotations: annotations,
		Note:        protocol.Ptr(note),
		Generation:  generation,
		RequestID:   "req-save",
	})
	var msg protocol.SessionAnnotationsSaveResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	return msg
}

func annotationsClear(t *testing.T, d *Daemon, sessionID string, generation int) protocol.SessionAnnotationsClearResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleSessionAnnotationsClear(client, &protocol.SessionAnnotationsClearMessage{
		Cmd:        protocol.CmdSessionAnnotationsClear,
		SessionID:  sessionID,
		Generation: generation,
		RequestID:  "req-clear",
	})
	var msg protocol.SessionAnnotationsClearResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	return msg
}

func annotation(id, messageKey, quote string) protocol.SessionAnnotation {
	return protocol.SessionAnnotation{
		ID:         id,
		MessageKey: messageKey,
		Start:      0,
		End:        len(quote),
		Quote:      quote,
		Emoji:      "🔄",
		Comment:    "",
	}
}

func annotationDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func TestSessionAnnotations_SurviveASaveAndReload(t *testing.T) {
	// The reason this is persisted at all: what the user marked has to still be
	// there after everything that draws it is gone.
	d := annotationDaemon(t)
	saved := []protocol.SessionAnnotation{
		annotation("a1", "msg-old", "an earlier turn"),
		annotation("a2", "msg-new", "the newest turn"),
	}

	if result := annotationsSave(t, d, "session-1", 1, saved); !result.Success {
		t.Fatalf("save failed: %v", protocol.Deref(result.Error))
	}

	result := annotationsGet(t, d, "session-1")
	if !result.Success {
		t.Fatalf("get failed: %v", protocol.Deref(result.Error))
	}
	if len(result.Annotations) != 2 {
		t.Fatalf("annotations = %d, want 2", len(result.Annotations))
	}
	if result.Annotations[0].MessageKey != "msg-old" || result.Annotations[1].MessageKey != "msg-new" {
		t.Errorf("message keys = %q/%q, want the two turns preserved",
			result.Annotations[0].MessageKey, result.Annotations[1].MessageKey)
	}
	if result.Annotations[0].Quote != "an earlier turn" {
		t.Errorf("quote = %q, want the annotated text", result.Annotations[0].Quote)
	}
	if result.Generation != 1 {
		t.Errorf("generation = %d, want 1", result.Generation)
	}
}

func TestSessionAnnotations_UnannotatedSessionIsEmptyNotAnError(t *testing.T) {
	d := annotationDaemon(t)

	result := annotationsGet(t, d, "never-annotated")

	if !result.Success {
		t.Fatalf("get failed: %v", protocol.Deref(result.Error))
	}
	if len(result.Annotations) != 0 {
		t.Errorf("annotations = %d, want none", len(result.Annotations))
	}
	if result.Generation != 0 {
		t.Errorf("generation = %d, want 0", result.Generation)
	}
}

func TestSessionAnnotations_StaleSaveIsRefusedNotAnError(t *testing.T) {
	// A reply that arrives out of order must not overwrite newer marks. The
	// client is told stale rather than failed, and re-hydrates.
	d := annotationDaemon(t)
	annotationsSave(t, d, "session-1", 2, []protocol.SessionAnnotation{annotation("a2", "msg", "newer")})

	result := annotationsSave(t, d, "session-1", 1, []protocol.SessionAnnotation{annotation("a1", "msg", "older")})

	if result.Success {
		t.Error("success = true for a save below the stored generation")
	}
	if !protocol.Deref(result.Stale) {
		t.Error("stale = false; the client cannot tell this apart from a failure")
	}
	if protocol.Deref(result.Error) != "" {
		t.Errorf("error = %q; a stale save is benign", protocol.Deref(result.Error))
	}
	stored := annotationsGet(t, d, "session-1")
	if len(stored.Annotations) != 1 || stored.Annotations[0].Quote != "newer" {
		t.Errorf("stored annotations = %+v, want the newer list intact", stored.Annotations)
	}
}

func TestSessionAnnotations_ClearTombstonesAgainstAnInFlightSave(t *testing.T) {
	// Sending the annotations clears them. A save that was already in flight
	// when that happened must not bring them back.
	d := annotationDaemon(t)
	annotationsSave(t, d, "session-1", 1, []protocol.SessionAnnotation{annotation("a1", "msg", "sent")})

	cleared := annotationsClear(t, d, "session-1", 2)
	if !cleared.Success {
		t.Fatalf("clear failed: %v", protocol.Deref(cleared.Error))
	}
	if cleared.Generation < 2 {
		t.Errorf("generation floor = %d, want at least the clear's own generation", cleared.Generation)
	}

	late := annotationsSave(t, d, "session-1", 2, []protocol.SessionAnnotation{annotation("a1", "msg", "ghost")})
	if !protocol.Deref(late.Stale) {
		t.Error("a save at the tombstone generation was accepted; cleared marks can come back")
	}

	stored := annotationsGet(t, d, "session-1")
	if len(stored.Annotations) != 0 {
		t.Errorf("annotations = %+v, want none after the clear", stored.Annotations)
	}
}

func TestSessionAnnotations_RejectsMissingSessionID(t *testing.T) {
	d := annotationDaemon(t)

	if result := annotationsGet(t, d, ""); result.Success || protocol.Deref(result.Error) == "" {
		t.Error("get with an empty session_id did not report an error")
	}
	if result := annotationsSave(t, d, "", 1, nil); result.Success || protocol.Deref(result.Error) == "" {
		t.Error("save with an empty session_id did not report an error")
	}
	if result := annotationsClear(t, d, "", 1); result.Success || protocol.Deref(result.Error) == "" {
		t.Error("clear with an empty session_id did not report an error")
	}
}

func TestSessionAnnotations_DroppedWithTheirSession(t *testing.T) {
	// A session id never comes back, so its draft is deleted rather than
	// tombstoned — otherwise the row outlives everything that could read it.
	d := annotationDaemon(t)
	d.store.Add(&protocol.Session{ID: "session-1", State: protocol.StateIdle})
	annotationsSave(t, d, "session-1", 1, []protocol.SessionAnnotation{annotation("a1", "msg", "marked")})

	d.store.Remove("session-1")

	result := annotationsGet(t, d, "session-1")
	if len(result.Annotations) != 0 {
		t.Errorf("annotations = %+v, want none after the session was removed", result.Annotations)
	}
	if result.Generation != 0 {
		t.Errorf("generation = %d, want a clean slate", result.Generation)
	}
}

func TestSessionAnnotations_NoteSurvivesTheSaveWithTheMarks(t *testing.T) {
	// The note is drafted alongside the marks and belongs to the same draft, so
	// it comes back from the same get the marks do.
	d := annotationDaemon(t)

	result := annotationsSaveWithNote(t, d, "session-1", 1,
		[]protocol.SessionAnnotation{annotation("a1", "msg", "marked")}, "Split this into two PRs.")
	if !result.Success {
		t.Fatalf("save failed: %v", protocol.Deref(result.Error))
	}

	stored := annotationsGet(t, d, "session-1")
	if protocol.Deref(stored.Note) != "Split this into two PRs." {
		t.Errorf("note = %q, want the saved note", protocol.Deref(stored.Note))
	}
}

func TestSessionAnnotations_NoNoteIsAbsentNotEmpty(t *testing.T) {
	// An older client sends no note at all; the field stays off the wire so
	// nothing downstream has to tell "" apart from "not set".
	d := annotationDaemon(t)
	annotationsSave(t, d, "session-1", 1, []protocol.SessionAnnotation{annotation("a1", "msg", "marked")})

	stored := annotationsGet(t, d, "session-1")
	if stored.Note != nil {
		t.Errorf("note = %q, want absent", protocol.Deref(stored.Note))
	}
}

func TestSessionAnnotations_ClearTakesTheNoteWithTheMarks(t *testing.T) {
	// Clear is what a send does. A note left behind is an instruction already
	// delivered, waiting to be delivered again.
	d := annotationDaemon(t)
	annotationsSaveWithNote(t, d, "session-1", 1,
		[]protocol.SessionAnnotation{annotation("a1", "msg", "marked")}, "Split this into two PRs.")

	if cleared := annotationsClear(t, d, "session-1", 2); !cleared.Success {
		t.Fatalf("clear failed: %v", protocol.Deref(cleared.Error))
	}

	stored := annotationsGet(t, d, "session-1")
	if stored.Note != nil {
		t.Errorf("note = %q, want it cleared with the marks", protocol.Deref(stored.Note))
	}
}
