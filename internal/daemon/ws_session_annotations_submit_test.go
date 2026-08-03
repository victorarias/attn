package daemon

import (
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const submitAnnotationText = "Feedback on your last message.\n\n## 1. 🔍 Verify this\n\n> the parser already handles this"

func sendAnnotationSubmit(t *testing.T, d *Daemon, sessionID, text string) protocol.SessionAnnotationsSubmitResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleSessionAnnotationsSubmit(client, &protocol.SessionAnnotationsSubmitMessage{
		Cmd:       protocol.CmdSessionAnnotationsSubmit,
		SessionID: sessionID,
		Text:      text,
		RequestID: "req-1",
	})
	var res protocol.SessionAnnotationsSubmitResultMessage
	readNotebookWSEvent(t, client.send, &res)
	if res.Event != protocol.EventSessionAnnotationsSubmitResult || res.RequestID != "req-1" {
		t.Fatalf("unexpected result envelope: %+v", res)
	}
	return res
}

// The whole point of routing the send through the daemon: the payload is typed
// as one bracketed-paste block and the Enter that submits it arrives as a
// SEPARATE PTY write. Folded into the first write it would be pasted text, and
// the feedback would sit in the composer forever.
func TestSessionAnnotationsSubmitDelivers(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "session-1", protocol.SessionStateIdle)

	res := sendAnnotationSubmit(t, d, "session-1", submitAnnotationText)

	if !res.Success || res.Status != markdownSubmitStatusDelivered || res.Error != nil {
		t.Fatalf("result = %+v, want delivered", res)
	}
	wantPaste := bracketedPasteStart + submitAnnotationText + bracketedPasteEnd
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 2 || inputs[0] != wantPaste || inputs[1] != "\r" {
		t.Fatalf("PTY inputs = %q, want [%q, %q]", inputs, wantPaste, "\r")
	}
}

// pending_approval is an annotatable state — an approval prompt is exactly when
// a user wants to push back — but it is also where the submitting Enter would
// ANSWER that prompt. Nothing is typed, and the client is told why so it can
// keep the marks for a retry.
func TestSessionAnnotationsSubmitSkipsPendingApproval(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "session-1", protocol.SessionStatePendingApproval)

	res := sendAnnotationSubmit(t, d, "session-1", submitAnnotationText)

	if res.Success || res.Status != markdownSubmitStatusSkipped || res.Error != nil {
		t.Fatalf("result = %+v, want skipped_pending_approval", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("nothing may be typed into a session on an approval prompt, got %q", inputs)
	}
}

func TestSessionAnnotationsSubmitUnknownSession(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)

	res := sendAnnotationSubmit(t, d, "nope", submitAnnotationText)

	if res.Success || res.Status != markdownSubmitStatusError ||
		res.Error == nil || !strings.Contains(*res.Error, "unknown session nope") {
		t.Fatalf("result = %+v, want unknown-session error", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("nothing should be typed for an unknown session, got %q", inputs)
	}
}

// An empty payload would submit a bare Enter into the session — a turn made of
// nothing. Refused with a named reason rather than delivered.
func TestSessionAnnotationsSubmitRejectsEmptyText(t *testing.T) {
	d := newSubmitDaemon(t)
	var mu sync.Mutex
	var inputs []string
	d.ptyBackend = recordingBackend(&inputs, &mu)
	addIdleNotebookSession(d, "session-1", protocol.SessionStateIdle)

	res := sendAnnotationSubmit(t, d, "session-1", "   \n ")

	if res.Success || res.Status != markdownSubmitStatusError ||
		res.Error == nil || !strings.Contains(*res.Error, "text is required") {
		t.Fatalf("result = %+v, want text-required error", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("nothing should be typed for an empty payload, got %q", inputs)
	}
}

// A PTY write that fails is reported as an error, never as a delivery: the
// client clears the user's marks on `delivered` and on nothing else.
func TestSessionAnnotationsSubmitDeliveryFailure(t *testing.T) {
	d := newSubmitDaemon(t)
	d.ptyBackend = &failingInputBackend{fakeSpawnBackend: &fakeSpawnBackend{}}
	addIdleNotebookSession(d, "session-1", protocol.SessionStateIdle)

	res := sendAnnotationSubmit(t, d, "session-1", submitAnnotationText)

	if res.Success || res.Status != markdownSubmitStatusError || res.Error == nil {
		t.Fatalf("result = %+v, want delivery error", res)
	}
}
