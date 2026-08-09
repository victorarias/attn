package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The picker's rows come from a shallow read of files pi wrote. Everything here
// is about that read degrading rather than failing: the file is what resumes,
// and a row with a poor label still resumes the right conversation.

func writeSession(t *testing.T, root, sessionID, name string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

const sessionHeader = `{"type":"session","version":3,"id":"pi-1","timestamp":"2026-08-06T19:08:09.077Z","cwd":"/Users/v/projects/attn"}`

func TestPastConversationsAreLabelledFromTheFile(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "sess-a", "a.jsonl",
		sessionHeader,
		`{"type":"model_change","id":"m1","provider":"openai","modelId":"gpt-5.6-luna"}`,
		`{"type":"message","id":"e1","message":{"role":"assistant","content":[{"type":"text","text":"how can I help"}]}}`,
		`{"type":"message","id":"e2","message":{"role":"user","content":[{"type":"text","text":"  fix the   paging bug  "}]}}`,
		`{"type":"message","id":"e3","message":{"role":"user","content":[{"type":"text","text":"and the other one"}]}}`,
	)

	files := collectPastConversationFiles(root)
	if len(files) != 1 {
		t.Fatalf("want 1 conversation, got %d", len(files))
	}
	cwd, preview := readPastConversationHead(files[0].path)
	if cwd != "/Users/v/projects/attn" {
		t.Errorf("cwd = %q", cwd)
	}
	// The FIRST thing the user said, whitespace flattened — an assistant line
	// before it is not what names a conversation.
	if preview != "fix the paging bug" {
		t.Errorf("preview = %q", preview)
	}
}

func TestPastConversationsSurviveWhatTheyCannotRead(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "sess-header-only", "a.jsonl", sessionHeader)
	writeSession(t, root, "sess-unparseable", "a.jsonl", `{"type":"session"`, `not json at all`)
	writeSession(t, root, "sess-future", "a.jsonl",
		`{"type":"session","version":9,"cwd":"/tmp/x"}`,
		`{"type":"turn","id":"e1","parts":[{"kind":"user_text","text":"hello"}]}`,
	)

	files := collectPastConversationFiles(root)
	if len(files) != 3 {
		t.Fatalf("want 3 conversations, got %d", len(files))
	}
	for _, file := range files {
		cwd, preview := readPastConversationHead(file.path)
		// No panics, no errors — a blank label at worst. What matters is that
		// the row exists at all, because the row is what resumes.
		if file.path == "" {
			t.Error("a listed conversation has no file")
		}
		if strings.Contains(file.sessionID, "future") && cwd != "/tmp/x" {
			t.Errorf("a header this build understands was still not read: cwd=%q preview=%q", cwd, preview)
		}
	}
}

func TestPastConversationsIgnoreWhatIsNotASessionFile(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "sess-a", "a.jsonl", sessionHeader)
	writeSession(t, root, "sess-a", "notes.txt", "not a session")
	if err := os.WriteFile(filepath.Join(root, "stray.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	// An empty file is a host that died before pi wrote anything: there is no
	// conversation to pick up, so offering one would be a lie.
	writeSession(t, root, "sess-empty", "a.jsonl")
	if err := os.Truncate(filepath.Join(root, "sess-empty", "a.jsonl"), 0); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	files := collectPastConversationFiles(root)
	if len(files) != 1 || !strings.HasSuffix(files[0].path, "sess-a/a.jsonl") {
		t.Fatalf("want only sess-a/a.jsonl, got %+v", files)
	}
}

func TestPastConversationsListNewestFirstAndSayWhenClipped(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < pastConversationsLimit+2; i++ {
		path := writeSession(t, root, "sess-"+strings.Repeat("x", i%3)+itoa(i), "a.jsonl", sessionHeader)
		stamp := time.Unix(1_700_000_000+int64(i), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	d := &Daemon{}
	conversations, truncated := d.listPastConversationsIn(root)
	if !truncated {
		t.Error("a listing past the cap must say so, or the picker silently hides conversations")
	}
	if len(conversations) != pastConversationsLimit {
		t.Fatalf("want %d rows, got %d", pastConversationsLimit, len(conversations))
	}
	for i := 1; i < len(conversations); i++ {
		if conversations[i-1].Modified < conversations[i].Modified {
			t.Fatalf("row %d is older than row %d: the clip must keep the newest", i-1, i)
		}
	}
}

// daemonWithConversation is a daemon holding one conversation session and its
// launch intent — the state a model switch has to survive into.
func daemonWithConversation(t *testing.T, intent store.LaunchIntent) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "sess-1",
		Agent:          protocol.SessionAgent("nisse"),
		Directory:      cwd,
		WorkspaceID:    "workspace",
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.store.SetLaunchIntent("sess-1", intent)
	return d
}

// hostEvent is one envelope off a host's stream, on the spine (seq > 0).
func hostEvent(sessionID string, body map[string]interface{}) hostsession.Event {
	return hostsession.Event{SessionID: sessionID, Seq: 1, Kind: "model_changed", Body: body}
}

// assertCommandError reads the one message a refused command puts on the
// asking client's socket.
func assertCommandError(t *testing.T, client *wsClient, want string) {
	t.Helper()
	select {
	case msg := <-client.send:
		var event protocol.WebSocketEvent
		if err := json.Unmarshal(msg.payload, &event); err != nil {
			t.Fatalf("unmarshal client message: %v", err)
		}
		if event.Event != protocol.EventCommandError || protocol.Deref(event.Error) != want {
			t.Fatalf("client got %s %q, want command_error %q", event.Event, protocol.Deref(event.Error), want)
		}
	default:
		t.Fatal("a refused command must say so on the socket; nothing was sent")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// A model switched mid-session has to survive the host that saw it: a revive
// reads the launch intent, and without this the conversation quietly goes back
// to whatever the spawn pinned.
func TestModelChangedRewritesTheLaunchIntent(t *testing.T) {
	d := daemonWithConversation(t, store.LaunchIntent{Model: "openai/luna", Effort: "high"})

	d.handleHostModelChanged(hostEvent("sess-1", map[string]interface{}{"model": "anthropic/claude"}))

	intent, ok := d.store.LaunchIntent("sess-1")
	if !ok || intent.Model != "anthropic/claude" {
		t.Fatalf("launch intent model = %q (found=%v)", intent.Model, ok)
	}
	if intent.Effort != "high" {
		t.Errorf("the rest of the intent must be left alone; effort = %q", intent.Effort)
	}
}

// pi refused the switch and reported the model still in force as nothing. There
// is nothing to record, and blanking the pinned model would make the next
// revive launch on a default nobody chose.
func TestModelChangedWithoutAModelLeavesTheIntentAlone(t *testing.T) {
	d := daemonWithConversation(t, store.LaunchIntent{Model: "openai/luna"})

	d.handleHostModelChanged(hostEvent("sess-1", map[string]interface{}{"error": "no credentials"}))

	intent, _ := d.store.LaunchIntent("sess-1")
	if intent.Model != "openai/luna" {
		t.Fatalf("launch intent model = %q", intent.Model)
	}
}

// The declaration list is what decides whether an envelope restamps
// `state_since`. A model switch does not move the session, and routing it
// through applyState would reset "working for 4m" for a picker change.
func TestModelChangedIsNotAStateDeclaration(t *testing.T) {
	if hostStateDeclarationKinds["model_changed"] {
		t.Fatal("model_changed must not be a state declaration")
	}
}

func TestAgentHistoryWithoutAnAnchorIsRefused(t *testing.T) {
	d := &Daemon{}
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAgentHistory(client, &protocol.AgentHistoryMessage{Cmd: protocol.CmdAgentHistory, ID: "sess-1"})
	assertCommandError(t, client, "agent_history needs a session id and a before cursor")
}

func TestAgentSetModelWithoutAModelIsRefused(t *testing.T) {
	d := &Daemon{}
	client := &wsClient{send: make(chan outboundMessage, 4)}
	d.handleAgentSetModel(client, &protocol.AgentSetModelMessage{Cmd: protocol.CmdAgentSetModel, ID: "sess-1"})
	assertCommandError(t, client, "agent_set_model needs a session id and a model")
}
