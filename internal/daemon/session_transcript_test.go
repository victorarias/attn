package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func callSessionTranscript(t *testing.T, d *Daemon, msg *protocol.SessionTranscriptMessage) protocol.Response {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		d.handleSessionTranscript(server, msg)
		_ = server.Close()
	}()
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	var response protocol.Response
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHandleSessionTranscriptResolvesNativeIDAndReturnsRedactedEvents(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	transcriptDir := filepath.Join(codexHome, "sessions", "2026", "07", "19")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(transcriptDir, "rollout.jsonl")
	content := strings.Join([]string{
		`{"timestamp":"2026-07-19T10:00:00Z","type":"session_meta","payload":{"id":"native-session"}}`,
		`{"timestamp":"2026-07-19T10:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"token=do-not-leak"}}`,
		`{"timestamp":"2026-07-19T10:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"done"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	d.store.Add(&protocol.Session{
		ID: "attn-session", Agent: protocol.SessionAgentCodex, Directory: t.TempDir(),
		WorkspaceID: "workspace", State: protocol.SessionStateWorking,
		StateSince: "2026-07-19T10:00:00Z", StateUpdatedAt: "2026-07-19T10:00:00Z", LastSeen: "2026-07-19T10:00:00Z",
	})
	d.store.SetResumeSessionID("attn-session", "native-session")

	response := callSessionTranscript(t, d, &protocol.SessionTranscriptMessage{Cmd: protocol.CmdSessionTranscript, TargetSessionID: "attn-session"})
	if !response.Ok || response.SessionTranscriptResult == nil {
		t.Fatalf("response = %+v", response)
	}
	result := response.SessionTranscriptResult
	if !result.AtEnd || result.NextCursor == "" || len(result.Events) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if text := protocol.Deref(result.Events[0].Text); strings.Contains(text, "do-not-leak") || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("redacted event text = %q", text)
	}

	resumed := callSessionTranscript(t, d, &protocol.SessionTranscriptMessage{
		Cmd: protocol.CmdSessionTranscript, TargetSessionID: "attn-session", AfterCursor: protocol.Ptr(result.NextCursor),
	})
	if !resumed.Ok || resumed.SessionTranscriptResult == nil || len(resumed.SessionTranscriptResult.Events) != 0 {
		t.Fatalf("resumed response = %+v", resumed)
	}
}

func TestHandleSessionTranscriptReturnsStableErrors(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	response := callSessionTranscript(t, d, &protocol.SessionTranscriptMessage{Cmd: protocol.CmdSessionTranscript, TargetSessionID: "missing"})
	if response.Ok || protocol.Deref(response.Error) != "session_not_found" {
		t.Fatalf("response = %+v", response)
	}
	if got := SessionTranscriptErrorMessage("cursor_mismatch"); got != "The transcript cursor belongs to a different transcript" {
		t.Fatalf("message = %q", got)
	}
}
