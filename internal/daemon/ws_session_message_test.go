package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// writeCodexRollout lays down a codex rollout under CODEX_HOME for `cwd`, so
// the daemon's own transcript resolution finds it the way it would in
// production. `source` is "cli" for an interactive pane; `codex exec` runs write
// "exec" and must not be picked up.
func writeCodexRollout(t *testing.T, codexHome, id, cwd, source string, messages ...string) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "08", "02")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+id+".jsonl")
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	content := fmt.Sprintf(
		`{"type":"session_meta","payload":{"id":%q,"cwd":%q,"source":%q,"timestamp":%q}}`+"\n",
		id, cwd, source, stamp,
	)
	for _, message := range messages {
		content += fmt.Sprintf(`{"type":"event_msg","payload":{"type":"agent_message","message":%q}}`+"\n", message)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	return path
}

func sessionMessagesGet(t *testing.T, d *Daemon, sessionID string) protocol.SessionMessagesGetResultMessage {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleSessionMessagesGet(client, &protocol.SessionMessagesGetMessage{
		Cmd:       protocol.CmdSessionMessagesGet,
		SessionID: sessionID,
		RequestID: "req-1",
	})
	var msg protocol.SessionMessagesGetResultMessage
	readNotebookWSEvent(t, client.send, &msg)
	return msg
}

func markdowns(messages []protocol.SessionMessage) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Markdown)
	}
	return out
}

func codexSessionDaemon(t *testing.T, messages ...string) (*Daemon, string) {
	t.Helper()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	path := writeCodexRollout(t, codexHome, "pane-rollout", cwd, "cli", messages...)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.Add(&protocol.Session{
		ID:        "session-1",
		Label:     "session-1",
		Agent:     protocol.SessionAgentCodex,
		Directory: cwd,
		State:     protocol.StateIdle,
	})
	seedAssistantWindow(t, d, "session-1", protocol.SessionAgentCodex, cwd, path)
	return d, "session-1"
}

func seedAssistantWindow(t *testing.T, d *Daemon, sessionID string, agent protocol.SessionAgent, cwd, path string) {
	t.Helper()
	watcher := newTranscriptWatcher(sessionID, agent, cwd, time.Now(), nil)
	follower, err := transcript.NewFollower(path, string(agent), 0)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := follower.Read()
	if err != nil {
		t.Fatal(err)
	}
	watcher.resetSource(protocol.SessionMessageWindowStatusReady, path, "", false)
	watcher.applyEvents(batch.Events)
	d.transcriptWatch[sessionID] = watcher
}

func TestSessionMessagesGet_ReturnsPastTurnsOldestFirst(t *testing.T) {
	// The whole point of the window: a turn scrolling past is still annotatable,
	// so the earlier message has to come back alongside the newest one.
	d, sessionID := codexSessionDaemon(t, "An earlier answer.", "The answer under annotation.")

	result := sessionMessagesGet(t, d, sessionID)

	if !result.Success {
		t.Fatalf("success = false, error = %v", protocol.Deref(result.Error))
	}
	if result.Status != protocol.SessionMessageWindowStatusReady {
		t.Fatalf("status = %q, want ready", result.Status)
	}
	want := []string{"An earlier answer.", "The answer under annotation."}
	if got := markdowns(result.Messages); !sameMarkdowns(got, want) {
		t.Errorf("messages = %q, want %q", got, want)
	}
	if result.Truncated {
		t.Error("truncated = true for a two-message transcript")
	}
	if result.SessionID != sessionID || result.RequestID != "req-1" {
		t.Errorf("correlation fields = %q/%q", result.SessionID, result.RequestID)
	}
}

func TestSessionMessagesGet_KeysAreStableAndPerMessage(t *testing.T) {
	// Keys are what persisted annotations address. Re-reading the transcript
	// must not rename a message, and two messages must not share a key.
	d, sessionID := codexSessionDaemon(t, "An earlier answer.", "The answer under annotation.")

	first := sessionMessagesGet(t, d, sessionID)
	second := sessionMessagesGet(t, d, sessionID)

	if len(first.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(first.Messages))
	}
	for i := range first.Messages {
		if first.Messages[i].Key == "" {
			t.Fatalf("message %d has an empty key", i)
		}
		if first.Messages[i].Key != second.Messages[i].Key {
			t.Errorf("message %d key changed across re-read: %q then %q",
				i, first.Messages[i].Key, second.Messages[i].Key)
		}
	}
	if first.Messages[0].Key == first.Messages[1].Key {
		t.Error("two different messages share a key")
	}
}

func TestSessionMessagesGet_EmptyTranscriptIsNotAnError(t *testing.T) {
	// A transcript with no assistant prose — pure tool activity — is a success
	// with nothing to annotate, which the client reports rather than treating as
	// a failure.
	d, sessionID := codexSessionDaemon(t)

	result := sessionMessagesGet(t, d, sessionID)

	if !result.Success {
		t.Fatalf("success = false, error = %v", protocol.Deref(result.Error))
	}
	if len(result.Messages) != 0 {
		t.Errorf("messages = %q, want none", markdowns(result.Messages))
	}
}

func TestSessionMessagesGet_LiveTranscriptStartupIsDiscovering(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)
	d.store.Add(&protocol.Session{
		ID: "session-1", Agent: protocol.SessionAgentCodex, Directory: t.TempDir(),
		State: protocol.SessionStateWorking,
	})
	d.transcriptWatch["session-1"] = newTranscriptWatcher("session-1", protocol.SessionAgentCodex, t.TempDir(), time.Now(), nil)

	result := sessionMessagesGet(t, d, "session-1")

	if !result.Success || result.Status != protocol.SessionMessageWindowStatusDiscovering || result.Error != nil {
		t.Fatalf("result = %+v, want successful discovering window", result)
	}
	if len(result.Messages) != 0 || result.Truncated {
		t.Fatalf("discovering result carried a window: %+v", result)
	}
}

func TestSessionMessagesGet_NoLiveTranscriptAuthorityIsUnavailable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.Add(&protocol.Session{ID: "session-1", Agent: protocol.SessionAgentCodex, Directory: t.TempDir(), State: protocol.StateIdle})

	result := sessionMessagesGet(t, d, "session-1")

	if !result.Success || result.Status != protocol.SessionMessageWindowStatusUnavailable || protocol.Deref(result.Detail) == "" {
		t.Fatalf("result = %+v, want successful unavailable status with detail", result)
	}
}

func TestSessionMessagesGet_ReportsAnOversizeMessageInsteadOfHalvingIt(t *testing.T) {
	// Handing back half a message would silently re-point every offset past the
	// cut, so an oversize message is left out and the window says so.
	huge := strings.Repeat("x", annotatableMessageMaxChars+1)
	d, sessionID := codexSessionDaemon(t, "A normal answer.", huge)

	result := sessionMessagesGet(t, d, sessionID)

	if !result.Success {
		t.Fatalf("success = false, error = %v", protocol.Deref(result.Error))
	}
	if got := markdowns(result.Messages); !sameMarkdowns(got, []string{"A normal answer."}) {
		t.Errorf("messages = %d entries, want only the in-budget one", len(got))
	}
	if !result.Truncated {
		t.Error("truncated = false after dropping an oversize message")
	}
}

func TestSessionMessagesGet_WindowKeepsTheNewestAndSaysItTruncated(t *testing.T) {
	messages := make([]string, annotatableWindowMessages+3)
	for i := range messages {
		messages[i] = fmt.Sprintf("answer %d", i)
	}
	d, sessionID := codexSessionDaemon(t, messages...)

	result := sessionMessagesGet(t, d, sessionID)

	if len(result.Messages) != annotatableWindowMessages {
		t.Fatalf("messages = %d, want the %d-message cap", len(result.Messages), annotatableWindowMessages)
	}
	last := result.Messages[len(result.Messages)-1].Markdown
	if last != messages[len(messages)-1] {
		t.Errorf("newest message = %q, want %q", last, messages[len(messages)-1])
	}
	if !result.Truncated {
		t.Error("truncated = false after dropping older messages")
	}
}

func TestSessionMessagesGet_RejectsUnknownSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	for _, id := range []string{"", "nope"} {
		result := sessionMessagesGet(t, d, id)
		if result.Success {
			t.Errorf("session_id %q: success = true, want an error", id)
		}
		if protocol.Deref(result.Error) == "" {
			t.Errorf("session_id %q: no error message", id)
		}
	}
}

func TestSessionMessagesGet_IgnoresHeadlessExecRollouts(t *testing.T) {
	// attn's own stop-time classifier runs `codex exec` in the session's own
	// directory, so a second rollout lands seconds after the pane's. Reading it
	// would hand the annotator the classifier's bookkeeping instead of what the
	// agent said to the user.
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	panePath := writeCodexRollout(t, codexHome, "pane", cwd, "cli", "What the agent said to the user.")
	writeCodexRollout(t, codexHome, "classifier", cwd, "exec", `{"verdict":"DONE"}`)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.Add(&protocol.Session{
		ID:        "session-1",
		Agent:     protocol.SessionAgentCodex,
		Directory: cwd,
		State:     protocol.StateIdle,
	})
	seedAssistantWindow(t, d, "session-1", protocol.SessionAgentCodex, cwd, panePath)

	result := sessionMessagesGet(t, d, "session-1")

	if got := markdowns(result.Messages); !sameMarkdowns(got, []string{"What the agent said to the user."}) {
		t.Errorf("messages = %q, want the interactive pane's message", got)
	}
}

func TestSessionMessagesGet_SameDirectorySessionsKeepExactTranscriptIdentity(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	firstPath := writeCodexRollout(t, codexHome, "first", cwd, "cli", "first session")
	secondPath := writeCodexRollout(t, codexHome, "second", cwd, "cli", "second session")
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	for _, id := range []string{"first", "second"} {
		d.store.Add(&protocol.Session{ID: id, Agent: protocol.SessionAgentCodex, Directory: cwd, State: protocol.StateIdle})
	}
	seedAssistantWindow(t, d, "first", protocol.SessionAgentCodex, cwd, firstPath)
	seedAssistantWindow(t, d, "second", protocol.SessionAgentCodex, cwd, secondPath)

	if got := markdowns(sessionMessagesGet(t, d, "first").Messages); !sameMarkdowns(got, []string{"first session"}) {
		t.Fatalf("first session messages = %q", got)
	}
	if got := markdowns(sessionMessagesGet(t, d, "second").Messages); !sameMarkdowns(got, []string{"second session"}) {
		t.Fatalf("second session messages = %q", got)
	}
}

func sameMarkdowns(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
