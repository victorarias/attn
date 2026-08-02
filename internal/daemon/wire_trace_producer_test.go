package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// TestWireTraceProducerGolden drives every state-change broadcaster the daemon
// has and pins the exact bytes each one puts on the wire.
//
// It exists because the event-bus migration rewrites all of them, and half of
// them are exercised by no other test in this package: before this golden,
// author states, settings, endpoints, notebook changes, notifications, tasks,
// workflow runs, workspace context and workspace-layout-updated could have been
// migrated to emit nothing at all and the suite would have stayed green.
//
// The broadcasters are invoked directly, one per step, with fixed inputs. That
// is deliberate: this golden answers "does this producer still emit the same
// message?", and the flow golden answers "does the call site still reach the
// producer?".
func TestWireTraceProducerGolden(t *testing.T) {
	dir := t.TempDir()
	d := NewForTesting(filepath.Join(dir, "test.sock"))
	trace := wireRecorder(d)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("create workspace dir: %v", err)
	}
	// Registered and paned through the handlers rather than seeded: a workspace
	// only has a layout once it has a pane, and the layout broadcasters bail out
	// without one.
	client := newWorkspaceProtocolTestClient()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-1", Title: "One", Directory: workspaceDir,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "workspace-1",
		PaneID: protocol.Ptr("pane-1"), SessionID: "sess-1", Title: protocol.Ptr("one"),
	})
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "sess-1", Label: "one", Agent: protocol.SessionAgentClaude,
		Directory: workspaceDir, WorkspaceID: "workspace-1",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.associateSession("sess-1", "workspace-1", "one")
	if _, err := d.ensureWorkspaceLayout("workspace-1"); err != nil {
		t.Fatalf("ensureWorkspaceLayout: %v", err)
	}

	// Everything published before this point is fixture noise.
	trace.Clear()

	steps := []struct {
		name string
		run  func()
	}{
		{"session_state_changed", func() { d.broadcastSessionStateChanged("sess-1") }},
		{"sessions_updated", func() { d.broadcastSessionsUpdated() }},
		{"rate_limited", func() {
			d.broadcastRateLimited("github", time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
		}},
		{"workspace_layout", func() { d.broadcastWorkspaceLayout("workspace-1") }},
		{"workspace_layout_updated", func() { d.broadcastWorkspaceLayoutUpdated("workspace-1") }},
		{"workspace_state_changed via mute", func() { d.setWorkspaceMuted("workspace-1", true) }},
		{"workspace_state_changed via pin", func() { d.setWorkspacePinned("workspace-1", true) }},
		{"workspace_state_changed via mute toggle", func() { d.toggleWorkspaceMute("workspace-1") }},
		{"workspace_context_changed", func() {
			d.broadcastWorkspaceContextChanged(&protocol.WorkspaceContext{
				WorkspaceID: "workspace-1", Content: "# Context\n", Revision: 3,
				UpdatedAt: now, UpdatedBySessionID: "sess-1",
			})
		}},
		{"workflow_run_updated", func() {
			d.broadcastWorkflowRunUpdated(&protocol.WorkflowRun{
				RunID: "run-1", Status: protocol.WorkflowRunStatusRunning,
				ScriptPath: filepath.Join(workspaceDir, "flow.js"), ScriptHash: "hash-1",
				CreatedAt: now, UpdatedAt: now,
			})
		}},
		{"prs_updated", func() { d.broadcastPRs() }},
		{"repos_updated", func() { d.broadcastRepoStates() }},
		{"authors_updated", func() { d.broadcastAuthorStates() }},
		{"github_hosts_updated", func() { d.broadcastGitHubHosts() }},
		{"automations_changed", func() { d.broadcastAutomationsChanged("definition-1") }},
		{"plugins_updated", func() { d.broadcastPluginsUpdated() }},
		{"settings_updated", func() { d.broadcastCurrentSettings("claude_executable") }},
		{"tasks_changed", func() { d.broadcastTasksChanged() }},
		{"notifications_updated", func() { d.broadcastNotificationsUpdated() }},
		{"notebook_changed", func() {
			d.broadcastNotebookChanged("agent", filepath.Join(workspaceDir, "journal.md"))
		}},
		{"endpoints_updated", func() { d.broadcastEndpointsUpdated() }},
		{"endpoint_status_changed", func() {
			d.broadcastEndpointStatusChanged(protocol.EndpointInfo{ID: "endpoint-1", Name: "remote"})
		}},
		{"git_operation", func() {
			finish := d.beginGitOperation(protocol.GitOperationKindDeleteWorktree, workspaceDir, nil)
			finish(nil)
		}},
	}
	for _, step := range steps {
		step.run()
	}

	assertWireGolden(t, "producers", renderWireTrace(trace, map[string]string{
		dir: "<tmp>",
	}))
}
