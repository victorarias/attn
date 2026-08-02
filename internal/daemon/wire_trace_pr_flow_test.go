package daemon

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// TestWireTracePRFlowGolden is the flow golden for the PR surface: the handlers
// a user's clicks land on, in the order the app sends them.
//
// The producer golden proves prs_updated still looks the same; it cannot prove
// that muting a PR still causes one. These handlers only ever reached clients
// through a whole-list re-push, so a migration that dropped the publish
// entirely would leave every per-producer assertion green.
func TestWireTracePRFlowGolden(t *testing.T) {
	dir := t.TempDir()
	d := NewForTesting(filepath.Join(dir, "test.sock"))
	trace := wireRecorder(d)

	client := newWorkspaceProtocolTestClient()
	prID := "github.com/owner/repo#1"
	injected := protocol.PR{
		ID: prID, Repo: "owner/repo", Host: "github.com", Number: 1,
		Title: "Add the thing", Author: "octocat", State: protocol.PRStateWaiting,
		URL: "https://github.com/owner/repo/pull/1",
	}

	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleInjectTestPR(conn, &protocol.InjectTestPRMessage{PR: injected})
	})
	// Injecting the same PR again is an update, not an appearance.
	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleInjectTestPR(conn, &protocol.InjectTestPRMessage{PR: injected})
	})
	d.handleMutePRWS(&protocol.MutePRMessage{ID: prID})
	// Unmuting also warms the PR, so it is two facts and still one push.
	d.handleMutePRWS(&protocol.MutePRMessage{ID: prID})
	d.handlePRVisitedWS(&protocol.PRVisitedMessage{ID: prID})
	d.handleMuteRepoWS(&protocol.MuteRepoMessage{Repo: "owner/repo"})
	d.handleMuteAuthorWS(&protocol.MuteAuthorMessage{Author: "octocat"})
	_ = client

	assertWireGolden(t, "pr_flow", renderWireTrace(trace, map[string]string{
		dir: "<tmp>",
	}))
}
