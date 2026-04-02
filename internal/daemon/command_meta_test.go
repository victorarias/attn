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
