package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

func callAgentPeek(t *testing.T, d *Daemon, target string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentPeek(conn, &protocol.AgentPeekMessage{Cmd: protocol.CmdAgentPeek, TargetSessionID: target})
	})
}

// Peek assembles everything from what the daemon already holds: the store
// snapshot (state, todos, workspace), the transcript file, and nothing else
// when no screen snapshot is available — a backend that cannot serve one
// degrades to an absent screen, never an error.
func TestHandleAgentPeekReturnsStateTodosWorkspaceAndLastMessage(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	transcriptDir := filepath.Join(codexHome, "sessions", "2026", "08", "10")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		`{"timestamp":"2026-08-10T10:00:00Z","type":"session_meta","payload":{"id":"native-peek"}}`,
		`{"timestamp":"2026-08-10T10:00:01Z","type":"event_msg","payload":{"type":"agent_message","message":"first answer"}}`,
		`{"timestamp":"2026-08-10T10:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"latest answer"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(transcriptDir, "rollout.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	workspaceID := addCharacterizationSession(t, d, "peek-target", protocol.SessionAgentCodex, protocol.SessionStateWorking)
	d.store.UpdateTodos("peek-target", []string{"[✓] read the plan", "[→] build peek"})
	d.store.SetResumeSessionID("peek-target", "native-peek")

	resp := callAgentPeek(t, d, "peek-target")
	if !resp.Ok || resp.AgentPeekResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	result := resp.AgentPeekResult
	if result.SessionID != "peek-target" || result.State != string(protocol.SessionStateWorking) {
		t.Fatalf("result identity/state = %+v", result)
	}
	if len(result.Todos) != 2 || result.Todos[1] != "[→] build peek" {
		t.Fatalf("todos = %v", result.Todos)
	}
	if result.WorkspaceID != workspaceID {
		t.Fatalf("workspace id = %q, want %q", result.WorkspaceID, workspaceID)
	}
	if protocol.Deref(result.LastAssistantMessage) != "latest answer" {
		t.Fatalf("last assistant message = %q", protocol.Deref(result.LastAssistantMessage))
	}
	if result.Screen != nil {
		t.Fatalf("screen = %+v, want absent when the backend has no snapshot", result.Screen)
	}
}

// The address book prints 8-char short ids, so peek must resolve a unique
// prefix — and refuse an ambiguous one by name rather than guessing.
func TestHandleAgentPeekResolvesPrefixesAndNamesFailures(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addCharacterizationSession(t, d, "aaa-first", protocol.SessionAgentClaude, protocol.SessionStateIdle)
	addCharacterizationSession(t, d, "aab-second", protocol.SessionAgentClaude, protocol.SessionStateIdle)

	resp := callAgentPeek(t, d, "aaa")
	if !resp.Ok || resp.AgentPeekResult == nil || resp.AgentPeekResult.SessionID != "aaa-first" {
		t.Fatalf("unique prefix response = %+v", resp)
	}

	ambiguous := callAgentPeek(t, d, "aa")
	if ambiguous.Ok || protocol.Deref(ambiguous.Error) != "ambiguous_session" {
		t.Fatalf("ambiguous response = %+v", ambiguous)
	}

	missing := callAgentPeek(t, d, "zzz")
	if missing.Ok || protocol.Deref(missing.Error) != "session_not_found" {
		t.Fatalf("missing response = %+v", missing)
	}
}

type peekSnapshotBackend struct {
	*fakeSpawnBackend
	snapshot pty.SnapshotInfo
}

func (b *peekSnapshotBackend) Snapshot(context.Context, string) (pty.SnapshotInfo, error) {
	return b.snapshot, nil
}

func TestHandleAgentPeekServesTheRenderedScreen(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addCharacterizationSession(t, d, "peek-screen", protocol.SessionAgentClaude, protocol.SessionStateWorking)
	d.ptyBackend = &peekSnapshotBackend{
		fakeSpawnBackend: &fakeSpawnBackend{},
		snapshot: pty.SnapshotInfo{
			Screen: &pty.ViewportSnapshot{Text: "$ make test\nok\n", HasText: true, Cols: 80, Rows: 24},
		},
	}

	resp := callAgentPeek(t, d, "peek-screen")
	if !resp.Ok || resp.AgentPeekResult == nil || resp.AgentPeekResult.Screen == nil {
		t.Fatalf("response = %+v", resp)
	}
	screen := resp.AgentPeekResult.Screen
	if screen.Text != "$ make test\nok\n" || screen.Cols != 80 || screen.Rows != 24 {
		t.Fatalf("screen = %+v", screen)
	}
}
