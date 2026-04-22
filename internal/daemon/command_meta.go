package daemon

import "github.com/victorarias/attn/internal/protocol"

type CommandScope int

const (
	ScopeSession CommandScope = iota
	ScopeEndpoint
	ScopeHubLocal
	ScopeHubMerge
)

type CommandMetadata struct {
	Scope                CommandScope
	BlocksDuringRecovery bool
	Log                  bool
}

func commandMetadata(scope CommandScope, blocksDuringRecovery bool, log bool) CommandMetadata {
	return CommandMetadata{
		Scope:                scope,
		BlocksDuringRecovery: blocksDuringRecovery,
		Log:                  log,
	}
}

// CommandMeta centralizes websocket command scope and related routing flags.
// The scope values are preparatory metadata for future endpoint-aware routing.
var CommandMeta = map[string]CommandMetadata{
	protocol.CmdRegister:                 commandMetadata(ScopeSession, false, true),
	protocol.CmdUnregister:               commandMetadata(ScopeSession, true, true),
	protocol.CmdState:                    commandMetadata(ScopeSession, false, true),
	protocol.CmdSetSessionResumeID:       commandMetadata(ScopeSession, false, true),
	protocol.CmdStop:                     commandMetadata(ScopeSession, false, true),
	protocol.CmdTodos:                    commandMetadata(ScopeSession, false, true),
	protocol.CmdQuery:                    commandMetadata(ScopeHubMerge, false, true),
	protocol.CmdHeartbeat:                commandMetadata(ScopeSession, false, true),
	protocol.CmdSessionVisualized:        commandMetadata(ScopeSession, false, true),
	protocol.CmdMute:                     commandMetadata(ScopeSession, false, true),
	protocol.CmdQueryPRs:                 commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMutePR:                   commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMuteRepo:                 commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMuteAuthor:               commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdCollapseRepo:             commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdQueryRepos:               commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdQueryAuthors:             commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdFetchPRDetails:           commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdRefreshPRs:               commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdClearSessions:            commandMetadata(ScopeHubLocal, true, true),
	protocol.CmdClearWarnings:            commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdPRVisited:                commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdListWorktrees:            commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdCreateWorktree:           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdDeleteWorktree:           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetSettings:              commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdSetSetting:               commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdAddEndpoint:              commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdRemoveEndpoint:           commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdUpdateEndpoint:           commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdListEndpoints:            commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdApprovePR:                commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMergePR:                  commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdInjectTestPR:             commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdInjectTestSession:        commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdGetRecentLocations:       commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdBrowseDirectory:          commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdInspectPath:              commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdListBranches:             commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdCreateWorktreeFromBranch: commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetDefaultBranch:         commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdFetchRemotes:             commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdListRemoteBranches:       commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdEnsureRepo:               commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdSubscribeGitStatus:       commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdUnsubscribeGitStatus:     commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetFileDiff:              commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetBranchDiffFiles:       commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetRepoInfo:              commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetReviewState:           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdStartReviewLoop:          commandMetadata(ScopeSession, false, true),
	protocol.CmdStopReviewLoop:           commandMetadata(ScopeSession, false, true),
	protocol.CmdGetReviewLoopState:       commandMetadata(ScopeSession, false, true),
	protocol.CmdGetReviewLoopRun:         commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdSetReviewLoopIterations:  commandMetadata(ScopeSession, false, true),
	protocol.CmdAnswerReviewLoop:         commandMetadata(ScopeSession, false, true),
	protocol.CmdMarkFileViewed:           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdAddComment:               commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdUpdateComment:            commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdResolveComment:           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdDeleteComment:            commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetComments:              commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdSpawnSession:             commandMetadata(ScopeEndpoint, true, true),
	protocol.CmdAttachSession:            commandMetadata(ScopeSession, true, true),
	protocol.CmdDetachSession:            commandMetadata(ScopeSession, true, true),
	protocol.CmdPtyInput:                 commandMetadata(ScopeSession, true, false),
	protocol.CmdPtyResize:                commandMetadata(ScopeSession, true, true),
	protocol.CmdKillSession:              commandMetadata(ScopeSession, true, true),
	protocol.CmdSessionLayoutGet:         commandMetadata(ScopeSession, true, true),
	protocol.CmdSessionLayoutSplitPane:   commandMetadata(ScopeSession, true, true),
	protocol.CmdSessionLayoutClosePane:   commandMetadata(ScopeSession, true, true),
	protocol.CmdSessionLayoutFocusPane:   commandMetadata(ScopeSession, true, true),
	protocol.CmdSessionLayoutRenamePane:  commandMetadata(ScopeSession, true, true),
}

func shouldLogWSCommand(cmd string) bool {
	meta, ok := CommandMeta[cmd]
	if !ok {
		return true
	}
	return meta.Log
}

func blocksDuringRecovery(cmd string) bool {
	meta, ok := CommandMeta[cmd]
	if !ok {
		return false
	}
	return meta.BlocksDuringRecovery
}
