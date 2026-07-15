package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const submitTestPath = "/tmp/annotated-doc.md"

func newSubmitDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func seedSubmitDraft(t *testing.T, d *Daemon, generation int, anns []protocol.MarkdownAnnotation) {
	t.Helper()
	blob, err := json.Marshal(anns)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.SaveMarkdownAnnotationDraft(submitTestPath, string(blob), generation, time.Now()); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
}

func submitTestAnnotations() []protocol.MarkdownAnnotation {
	return []protocol.MarkdownAnnotation{
		{ID: "c1", Type: "comment", Anchor: mdAnchor(3, 3, 0, "hello"), Text: protocol.Ptr("hi"), CreatedAt: 1},
	}
}

func sendSubmit(t *testing.T, d *Daemon, target string, orphaned []string) protocol.MarkdownAnnotationsSubmitResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleMarkdownAnnotationsSubmit(client, &protocol.MarkdownAnnotationsSubmitMessage{
		Cmd:             protocol.CmdMarkdownAnnotationsSubmit,
		Path:            submitTestPath,
		TargetSessionID: target,
		OrphanedIds:     orphaned,
		RequestID:       "req-1",
	})
	var res protocol.MarkdownAnnotationsSubmitResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Event != protocol.EventMarkdownAnnotationsSubmitResult || res.RequestID != "req-1" {
		t.Fatalf("unexpected result envelope: %+v", res)
	}
	return res
}

func storedSubmitDraftCount(t *testing.T, d *Daemon) int {
	t.Helper()
	draft, err := d.store.GetMarkdownAnnotationDraft(submitTestPath)
	if err != nil {
		t.Fatal(err)
	}
	anns, err := decodeMarkdownAnnotations(draft.Annotations)
	if err != nil {
		t.Fatal(err)
	}
	return len(anns)
}

// Delivered: exactly one PTY write of paste-start + payload + paste-end + \r,
// the draft is tombstone-cleared, and the result carries the new generation
// floor so the client can seed its counter without a round-trip.
func TestMarkdownAnnotationsSubmitDelivered(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "target", protocol.SessionStateIdle)
	anns := submitTestAnnotations()
	seedSubmitDraft(t, d, 5, anns)

	res := sendSubmit(t, d, "target", nil)

	if !res.Success || res.Status != markdownSubmitStatusDelivered || res.Error != nil {
		t.Fatalf("result = %+v, want delivered", res)
	}
	if res.Generation == nil || *res.Generation != 5 {
		t.Fatalf("generation = %v, want floor 5", res.Generation)
	}
	wantPayload := formatMarkdownAnnotationPayload(submitTestPath, anns, map[string]bool{})
	wantInput := bracketedPasteStart + wantPayload + bracketedPasteEnd + "\r"
	mu.Lock()
	got := append([]string(nil), inputs...)
	mu.Unlock()
	if len(got) != 1 || got[0] != wantInput {
		t.Fatalf("PTY inputs = %q, want exactly [%q]", got, wantInput)
	}
	if n := storedSubmitDraftCount(t, d); n != 0 {
		t.Fatalf("draft not cleared after delivery: %d annotations remain", n)
	}
	// The tombstone must reject a straggling debounced save at the old
	// generation (the resurrection guard).
	if err := d.store.SaveMarkdownAnnotationDraft(submitTestPath, "[]", 5, time.Now()); err == nil {
		t.Fatal("stale save at cleared generation should be rejected")
	}
}

// Orphaned ids travel from the message into the payload label.
func TestMarkdownAnnotationsSubmitCarriesOrphanedIds(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "target", protocol.SessionStateIdle)
	seedSubmitDraft(t, d, 1, submitTestAnnotations())

	res := sendSubmit(t, d, "target", []string{"c1"})
	if !res.Success {
		t.Fatalf("result = %+v, want delivered", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 1 || !strings.Contains(inputs[0], "(~line 3, moved)") {
		t.Fatalf("payload should label orphaned c1, got %q", inputs)
	}
}

// pending_approval target: nothing is typed, drafts stay intact, and the
// result reports skipped_pending_approval (success=false, no error).
func TestMarkdownAnnotationsSubmitSkippedPendingApproval(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "target", protocol.SessionStatePendingApproval)
	seedSubmitDraft(t, d, 2, submitTestAnnotations())

	res := sendSubmit(t, d, "target", nil)

	if res.Success || res.Status != markdownSubmitStatusSkipped || res.Error != nil {
		t.Fatalf("result = %+v, want skipped_pending_approval", res)
	}
	mu.Lock()
	if len(inputs) != 0 {
		t.Fatalf("nothing should be typed into a pending_approval session, got %q", inputs)
	}
	mu.Unlock()
	if n := storedSubmitDraftCount(t, d); n != 1 {
		t.Fatalf("draft must stay intact on skip, got %d annotations", n)
	}
}

// Unknown target session: error result, drafts intact.
func TestMarkdownAnnotationsSubmitUnknownSession(t *testing.T) {
	d := newSubmitDaemon(t)
	seedSubmitDraft(t, d, 2, submitTestAnnotations())

	res := sendSubmit(t, d, "nope", nil)

	if res.Success || res.Status != markdownSubmitStatusError ||
		res.Error == nil || !strings.Contains(*res.Error, "session not found: nope") {
		t.Fatalf("result = %+v, want session-not-found error", res)
	}
	if n := storedSubmitDraftCount(t, d); n != 1 {
		t.Fatalf("draft must stay intact on error, got %d annotations", n)
	}
}

// Empty draft: error result, no PTY write, no clear.
func TestMarkdownAnnotationsSubmitEmptyDraft(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "target", protocol.SessionStateIdle)

	res := sendSubmit(t, d, "target", nil)

	if res.Success || res.Status != markdownSubmitStatusError ||
		res.Error == nil || *res.Error != "no annotations to send" {
		t.Fatalf("result = %+v, want no-annotations error", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("nothing should be typed for an empty draft, got %q", inputs)
	}
}

// PTY delivery failure: error result, drafts intact (retry-safe).
func TestMarkdownAnnotationsSubmitDeliveryFailure(t *testing.T) {
	d := newSubmitDaemon(t)
	d.ptyBackend = &failingInputBackend{fakeSpawnBackend: &fakeSpawnBackend{}}
	addIdleNotebookSession(d, "target", protocol.SessionStateIdle)
	seedSubmitDraft(t, d, 2, submitTestAnnotations())

	res := sendSubmit(t, d, "target", nil)

	if res.Success || res.Status != markdownSubmitStatusError ||
		res.Error == nil || !strings.Contains(*res.Error, "pty write exploded") {
		t.Fatalf("result = %+v, want delivery error", res)
	}
	if n := storedSubmitDraftCount(t, d); n != 1 {
		t.Fatalf("draft must stay intact when delivery fails, got %d annotations", n)
	}
}

// A clear failure after a successful delivery still reports delivered (never
// risk re-delivery) but carries an error so the UI re-hydrates.
func TestMarkdownAnnotationsSubmitClearFailureStillDelivered(t *testing.T) {
	d := newSubmitDaemon(t)
	// Closing the store DB from inside the PTY write makes the delivery
	// succeed and the follow-up ClearMarkdownAnnotationDraft fail.
	d.ptyBackend = &fakeSpawnBackend{onInput: func(string, []byte) {
		if err := d.store.Close(); err != nil {
			t.Errorf("closing store: %v", err)
		}
	}}
	addIdleNotebookSession(d, "target", protocol.SessionStateIdle)
	seedSubmitDraft(t, d, 2, submitTestAnnotations())

	res := sendSubmit(t, d, "target", nil)

	if !res.Success || res.Status != markdownSubmitStatusDelivered {
		t.Fatalf("result = %+v, want delivered despite clear failure", res)
	}
	if res.Error == nil || !strings.Contains(*res.Error, "delivered; failed to clear drafts") {
		t.Fatalf("error = %v, want delivered-but-not-cleared marker", res.Error)
	}
	if res.Generation != nil {
		t.Fatalf("generation should be absent when the clear failed, got %v", res.Generation)
	}
}

// failingInputBackend is a fakeSpawnBackend whose PTY input always errors.
type failingInputBackend struct {
	*fakeSpawnBackend
}

func (b *failingInputBackend) Input(context.Context, string, []byte) error {
	return fmt.Errorf("pty write exploded")
}
