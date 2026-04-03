package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCommandMetaCoversAllCommands(t *testing.T) {
	commands := []string{
		protocol.CmdRegister,
		protocol.CmdUnregister,
		protocol.CmdState,
		protocol.CmdSetSessionResumeID,
		protocol.CmdStop,
		protocol.CmdTodos,
		protocol.CmdQuery,
		protocol.CmdHeartbeat,
		protocol.CmdSessionVisualized,
		protocol.CmdMute,
		protocol.CmdQueryPRs,
		protocol.CmdMutePR,
		protocol.CmdMuteRepo,
		protocol.CmdMuteAuthor,
		protocol.CmdCollapseRepo,
		protocol.CmdQueryRepos,
		protocol.CmdQueryAuthors,
		protocol.CmdFetchPRDetails,
		protocol.CmdRefreshPRs,
		protocol.CmdClearSessions,
		protocol.CmdClearWarnings,
		protocol.CmdPRVisited,
		protocol.CmdListWorktrees,
		protocol.CmdCreateWorktree,
		protocol.CmdDeleteWorktree,
		protocol.CmdGetSettings,
		protocol.CmdSetSetting,
		protocol.CmdApprovePR,
		protocol.CmdMergePR,
		protocol.CmdInjectTestPR,
		protocol.CmdInjectTestSession,
		protocol.CmdGetRecentLocations,
		protocol.CmdBrowseDirectory,
		protocol.CmdInspectPath,
		protocol.CmdListBranches,
		protocol.CmdDeleteBranch,
		protocol.CmdSwitchBranch,
		protocol.CmdCreateWorktreeFromBranch,
		protocol.CmdCreateBranch,
		protocol.CmdCheckDirty,
		protocol.CmdStash,
		protocol.CmdStashPop,
		protocol.CmdCheckAttnStash,
		protocol.CmdCommitWIP,
		protocol.CmdGetDefaultBranch,
		protocol.CmdFetchRemotes,
		protocol.CmdListRemoteBranches,
		protocol.CmdEnsureRepo,
		protocol.CmdSubscribeGitStatus,
		protocol.CmdUnsubscribeGitStatus,
		protocol.CmdGetFileDiff,
		protocol.CmdGetBranchDiffFiles,
		protocol.CmdGetRepoInfo,
		protocol.CmdGetReviewState,
		protocol.CmdStartReviewLoop,
		protocol.CmdStopReviewLoop,
		protocol.CmdGetReviewLoopState,
		protocol.CmdGetReviewLoopRun,
		protocol.CmdSetReviewLoopIterations,
		protocol.CmdAnswerReviewLoop,
		protocol.CmdMarkFileViewed,
		protocol.CmdAddComment,
		protocol.CmdUpdateComment,
		protocol.CmdResolveComment,
		protocol.CmdWontFixComment,
		protocol.CmdDeleteComment,
		protocol.CmdGetComments,
		protocol.CmdSpawnSession,
		protocol.CmdAttachSession,
		protocol.CmdDetachSession,
		protocol.CmdPtyInput,
		protocol.CmdPtyResize,
		protocol.CmdKillSession,
		protocol.CmdWorkspaceGet,
		protocol.CmdWorkspaceSplitPane,
		protocol.CmdWorkspaceClosePane,
		protocol.CmdWorkspaceFocusPane,
		protocol.CmdWorkspaceRenamePane,
	}

	for _, cmd := range commands {
		if _, ok := CommandMeta[cmd]; !ok {
			t.Fatalf("missing command metadata for %s", cmd)
		}
	}
}

func TestCommandMetaExamples(t *testing.T) {
	if meta := CommandMeta[protocol.CmdPtyInput]; meta.Scope != ScopeSession {
		t.Fatalf("pty_input scope = %v, want %v", meta.Scope, ScopeSession)
	}
	if meta := CommandMeta[protocol.CmdSpawnSession]; meta.Scope != ScopeEndpoint {
		t.Fatalf("spawn_session scope = %v, want %v", meta.Scope, ScopeEndpoint)
	}
	if meta := CommandMeta[protocol.CmdClearSessions]; meta.Scope != ScopeHubLocal {
		t.Fatalf("clear_sessions scope = %v, want %v", meta.Scope, ScopeHubLocal)
	}
	if meta := CommandMeta[protocol.CmdQueryPRs]; meta.Scope != ScopeHubLocal {
		t.Fatalf("query_prs scope = %v, want %v", meta.Scope, ScopeHubLocal)
	}
	if !blocksDuringRecovery(protocol.CmdPtyInput) {
		t.Fatal("pty_input should block during recovery")
	}
	if shouldLogWSCommand(protocol.CmdPtyInput) {
		t.Fatal("pty_input should be excluded from normal websocket command logging")
	}
}

type stubRemoteCommandResolver struct {
	path    map[string]string
	review  map[string]string
	comment map[string]string
	loop    map[string]string
}

func (s stubRemoteCommandResolver) EndpointIDForPath(path string) (string, bool) {
	endpointID, ok := s.path[path]
	return endpointID, ok
}

func (s stubRemoteCommandResolver) EndpointIDForReview(reviewID string) (string, bool) {
	endpointID, ok := s.review[reviewID]
	return endpointID, ok
}

func (s stubRemoteCommandResolver) EndpointIDForComment(commentID string) (string, bool) {
	endpointID, ok := s.comment[commentID]
	return endpointID, ok
}

func (s stubRemoteCommandResolver) EndpointIDForReviewLoop(loopID string) (string, bool) {
	endpointID, ok := s.loop[loopID]
	return endpointID, ok
}

func TestRemoteCommandScopedEndpointID(t *testing.T) {
	resolver := stubRemoteCommandResolver{
		path: map[string]string{
			"/srv/repo": "endpoint-path",
		},
		review: map[string]string{
			"review-1": "endpoint-review",
		},
		comment: map[string]string{
			"comment-1": "endpoint-comment",
		},
		loop: map[string]string{
			"loop-1": "endpoint-loop",
		},
	}

	if endpointID, ok := remoteCommandScopedEndpointID(&protocol.GetFileDiffMessage{Directory: "/srv/repo"}, resolver); !ok || endpointID != "endpoint-path" {
		t.Fatalf("remoteCommandScopedEndpointID(path) = (%q, %v), want (%q, true)", endpointID, ok, "endpoint-path")
	}
	if endpointID, ok := remoteCommandScopedEndpointID(&protocol.AddCommentMessage{ReviewID: "review-1"}, resolver); !ok || endpointID != "endpoint-review" {
		t.Fatalf("remoteCommandScopedEndpointID(review) = (%q, %v), want (%q, true)", endpointID, ok, "endpoint-review")
	}
	if endpointID, ok := remoteCommandScopedEndpointID(&protocol.ResolveCommentMessage{CommentID: "comment-1"}, resolver); !ok || endpointID != "endpoint-comment" {
		t.Fatalf("remoteCommandScopedEndpointID(comment) = (%q, %v), want (%q, true)", endpointID, ok, "endpoint-comment")
	}
	if endpointID, ok := remoteCommandScopedEndpointID(&protocol.GetReviewLoopRunMessage{LoopID: "loop-1"}, resolver); !ok || endpointID != "endpoint-loop" {
		t.Fatalf("remoteCommandScopedEndpointID(loop) = (%q, %v), want (%q, true)", endpointID, ok, "endpoint-loop")
	}
}

func TestRemoteCommandSessionID_IncludesReviewLoopCommands(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		msg  interface{}
		want string
	}{
		{
			name: "start",
			cmd:  protocol.CmdStartReviewLoop,
			msg:  &protocol.StartReviewLoopMessage{SessionID: "sess-start"},
			want: "sess-start",
		},
		{
			name: "stop",
			cmd:  protocol.CmdStopReviewLoop,
			msg:  &protocol.StopReviewLoopMessage{SessionID: "sess-stop"},
			want: "sess-stop",
		},
		{
			name: "get_state",
			cmd:  protocol.CmdGetReviewLoopState,
			msg:  &protocol.GetReviewLoopStateMessage{SessionID: "sess-get"},
			want: "sess-get",
		},
		{
			name: "set_iterations",
			cmd:  protocol.CmdSetReviewLoopIterations,
			msg:  &protocol.SetReviewLoopIterationLimitMessage{SessionID: "sess-set"},
			want: "sess-set",
		},
	}

	for _, tc := range cases {
		if got := remoteCommandSessionID(tc.cmd, tc.msg); got != tc.want {
			t.Fatalf("%s remoteCommandSessionID() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
