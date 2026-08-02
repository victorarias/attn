package daemon

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// runDaemonSocketCommand drives a unix-socket handler, draining the reply so the
// handler's write does not block.
func runDaemonSocketCommand(t *testing.T, fn func(conn net.Conn)) {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(server)
		_ = server.Close()
	}()
	_, _ = io.Copy(io.Discard, client)
	_ = client.Close()
	<-done
}

// TestWireTraceFlowGolden drives the daemon the way a client does and pins every
// byte that comes back.
//
// The producer golden proves a broadcaster still builds the same message. This
// one proves the handlers still reach it — and, just as importantly, that they
// reach it the same number of times and in the same order. A migration that
// publishes a fact from the wrong place, twice, or not at all is invisible to a
// per-producer test and obvious here.
func TestWireTraceFlowGolden(t *testing.T) {
	dir := t.TempDir()
	d := NewForTesting(filepath.Join(dir, "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	trace := wireRecorder(d)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("create workspace dir: %v", err)
	}
	client := newWorkspaceProtocolTestClient()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-1", Title: "One", Directory: workspaceDir,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "workspace-1",
		PaneID: protocol.Ptr("pane-1"), SessionID: "sess-1", Title: protocol.Ptr("one"),
	})
	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleRegister(conn, &protocol.RegisterMessage{
			ID: "sess-1", Label: protocol.Ptr("one"), Dir: workspaceDir,
			Agent: protocol.Ptr(protocol.SessionAgentClaude), WorkspaceID: "workspace-1",
		})
	})
	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleTodos(conn, &protocol.TodosMessage{
			ID: "sess-1", Todos: []string{"write the migration"},
		})
	})
	d.handleRenameSession(client, &protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "sess-1", Label: "renamed",
	})
	d.handleRenameWorkspace(client, &protocol.RenameWorkspaceMessage{
		Cmd: protocol.CmdRenameWorkspace, WorkspaceID: "workspace-1", Title: "Renamed",
	})
	d.handleMuteWorkspaceWS(client, &protocol.MuteWorkspaceMessage{
		Cmd: protocol.CmdMuteWorkspace, WorkspaceID: "workspace-1",
	})
	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: "sess-1"})
	d.handleUnregisterWorkspace(client, &protocol.UnregisterWorkspaceMessage{
		Cmd: protocol.CmdUnregisterWorkspace, ID: "workspace-1",
	})

	assertWireGolden(t, "flow", renderWireTrace(trace, map[string]string{
		dir: "<tmp>",
	}))
}
