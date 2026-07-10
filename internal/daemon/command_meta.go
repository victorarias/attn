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
	protocol.CmdRegister:                              commandMetadata(ScopeSession, false, true),
	protocol.CmdSetTicketStatus:                       commandMetadata(ScopeSession, false, true),
	protocol.CmdTicketInbox:                           commandMetadata(ScopeSession, false, true),
	protocol.CmdTicketHandover:                        commandMetadata(ScopeSession, false, true),
	protocol.CmdTicketCreate:                          commandMetadata(ScopeSession, false, true),
	protocol.CmdUnregister:                            commandMetadata(ScopeSession, true, true),
	protocol.CmdState:                                 commandMetadata(ScopeSession, false, true),
	protocol.CmdSetSessionResumeID:                    commandMetadata(ScopeSession, false, true),
	protocol.CmdStop:                                  commandMetadata(ScopeSession, false, true),
	protocol.CmdTodos:                                 commandMetadata(ScopeSession, false, true),
	protocol.CmdQuery:                                 commandMetadata(ScopeHubMerge, false, true),
	protocol.CmdHeartbeat:                             commandMetadata(ScopeSession, false, true),
	protocol.CmdSessionVisualized:                     commandMetadata(ScopeSession, false, true),
	protocol.CmdSessionSelected:                       commandMetadata(ScopeSession, false, true),
	protocol.CmdTriggerNudge:                          commandMetadata(ScopeSession, false, true),
	protocol.CmdWorkspaceSelected:                     commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMuteWorkspace:                         commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdQueryPRs:                              commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMutePR:                                commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMuteRepo:                              commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMuteAuthor:                            commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdCollapseRepo:                          commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdQueryRepos:                            commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdQueryAuthors:                          commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdFetchPRDetails:                        commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdRefreshPRs:                            commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdClearSessions:                         commandMetadata(ScopeHubLocal, true, true),
	protocol.CmdClearWarnings:                         commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdPRVisited:                             commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdListWorktrees:                         commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdCreateWorktree:                        commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdDeleteWorktree:                        commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetSettings:                           commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdSetSetting:                            commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdListPlugins:                           commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdInstallPlugin:                         commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdRemovePlugin:                          commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdSetPluginPriority:                     commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdAddEndpoint:                           commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdRemoveEndpoint:                        commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdUpdateEndpoint:                        commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdListEndpoints:                         commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdApprovePR:                             commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdMergePR:                               commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdInjectTestPR:                          commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdInjectTestSession:                     commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdGetRecentLocations:                    commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdBrowseDirectory:                       commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdInspectPath:                           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdListBranches:                          commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdCreateWorktreeFromBranch:              commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetDefaultBranch:                      commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdFetchRemotes:                          commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdListRemoteBranches:                    commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdEnsureRepo:                            commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdSubscribeGitStatus:                    commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdUnsubscribeGitStatus:                  commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetFileDiff:                           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetRepoInfo:                           commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetPresentations:                      commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdGetPresentationRound:                  commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdPresentSubmitRound:                    commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdSpawnSession:                          commandMetadata(ScopeEndpoint, true, true),
	protocol.CmdAttachSession:                         commandMetadata(ScopeSession, true, true),
	protocol.CmdDetachSession:                         commandMetadata(ScopeSession, true, true),
	protocol.CmdPtyInput:                              commandMetadata(ScopeSession, true, false),
	protocol.CmdPtyResize:                             commandMetadata(ScopeSession, true, true),
	protocol.CmdKillSession:                           commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutGet:                    commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutAddSessionPane:         commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutClosePane:              commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutFocusPane:              commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutRenamePane:             commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutSetSplitRatio:          commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutDockTile:               commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutUndockTile:             commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutUpdateTile:             commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutMoveLeaf:               commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutMoveLeafToWorkspace:    commandMetadata(ScopeSession, true, true),
	protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace: commandMetadata(ScopeSession, true, true),
	protocol.CmdSetWorkspaceRank:                      commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdWorkspaceTileContentGet:               commandMetadata(ScopeSession, true, true),
	protocol.CmdBrowserControl:                        commandMetadata(ScopeSession, true, true),
	protocol.CmdBrowserControlResult:                  commandMetadata(ScopeHubLocal, false, true),
	protocol.CmdRenameSession:                         commandMetadata(ScopeSession, false, true),
	protocol.CmdRenameWorkspace:                       commandMetadata(ScopeEndpoint, false, true),
	protocol.CmdSetChiefOfStaff:                       commandMetadata(ScopeHubLocal, false, true),
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
