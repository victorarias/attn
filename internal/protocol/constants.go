package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ProtocolVersion is the version of the daemon-client protocol.
// Increment this when making breaking changes to the protocol.
// Client and daemon must have matching versions.
const ProtocolVersion = "158"

// CapabilityWorkspaceSessions is required for websocket clients that use the
// interactive daemon API. Clients without it are not workspace-first clients.
const CapabilityWorkspaceSessions = "workspace_sessions"

// CapabilityBrowserHost identifies the local Tauri client that owns the
// visible child webview used by docked browser tiles.
const CapabilityBrowserHost = "browser_host"

// CapabilityBinaryPtyOutput opts a client into receiving live PTY output as
// binary websocket frames (see binaryframe.go) instead of base64-in-JSON
// pty_output events. Clients without it keep the JSON event, which is what
// keeps daemon-to-daemon relays and older automation clients working.
const CapabilityBinaryPtyOutput = "binary_pty_output"

// SessionAgent labels in-tree and externally registered agent identifiers.
type SessionAgent = string

// Built-in session-agent identifiers. SessionAgent is intentionally open so
// external plugin drivers can publish their own identifiers at runtime.
const (
	SessionAgentClaude  SessionAgent = "claude"
	SessionAgentCodex   SessionAgent = "codex"
	SessionAgentCopilot SessionAgent = "copilot"
	SessionAgentShell   SessionAgent = "shell"
)

// Commands
const (
	CmdClientHello                           = "client_hello"
	CmdRegister                              = "register"
	CmdDelegate                              = "delegate"
	CmdSetTicketStatus                       = "set_ticket_status"
	CmdTicketInbox                           = "ticket_inbox"
	CmdTicketList                            = "ticket_list"
	CmdTicketShow                            = "ticket_show"
	CmdTicketSubscribe                       = "ticket_subscribe"
	CmdTicketUnsubscribe                     = "ticket_unsubscribe"
	CmdTicketTake                            = "ticket_take"
	CmdTicketAttach                          = "ticket_attach"
	CmdTicketCreate                          = "ticket_create"
	CmdTicketComment                         = "ticket_comment"
	CmdGetTicket                             = "get_ticket"
	CmdTicketChangeStatus                    = "ticket_change_status"
	CmdTicketAddComment                      = "ticket_add_comment"
	CmdTicketEditDescription                 = "ticket_edit_description"
	CmdTicketResume                          = "ticket_resume"
	CmdPresentOpen                           = "present_open"
	CmdPresentFeedback                       = "present_feedback"
	CmdGetPresentations                      = "get_presentations"
	CmdGetPresentationRound                  = "get_presentation_round"
	CmdPresentSubmitRound                    = "present_submit_round"
	CmdPresentClose                          = "present_close"
	CmdWorkspaceContextCheckout              = "workspace_context_checkout"
	CmdWorkspaceContextUpdate                = "workspace_context_update"
	CmdWorkspaceContextStatus                = "workspace_context_status"
	CmdWorkspaceContextList                  = "workspace_context_list"
	CmdWorkspaceContextCompact               = "workspace_context_compact"
	CmdWorkspaceContextRollback              = "workspace_context_rollback"
	CmdNotebookList                          = "notebook_list"
	CmdNotebookRead                          = "notebook_read"
	CmdNotebookWrite                         = "notebook_write"
	CmdNotebookGuide                         = "notebook_guide"
	CmdJournalAppend                         = "journal_append"
	CmdNotebookBacklinks                     = "notebook_backlinks"
	CmdNotebookSendToChief                   = "notebook_send_to_chief"
	CmdTaskList                              = "task_list"
	CmdTaskRetry                             = "task_retry"
	CmdNotificationList                      = "notification_list"
	CmdNotificationMarkRead                  = "notification_mark_read"
	CmdFsList                                = "fs_list"
	CmdFsRead                                = "fs_read"
	CmdFsWrite                               = "fs_write"
	CmdFsRename                              = "fs_rename"
	CmdFsDelete                              = "fs_delete"
	CmdFsExists                              = "fs_exists"
	CmdUnregister                            = "unregister"
	CmdState                                 = "state"
	CmdSetSessionResumeID                    = "set_session_resume_id"
	CmdStop                                  = "stop"
	CmdTodos                                 = "todos"
	CmdQuery                                 = "query"
	CmdHeartbeat                             = "heartbeat"
	CmdSessionVisualized                     = "session_visualized"
	CmdSessionSelected                       = "session_selected"
	CmdWorkspaceSelected                     = "workspace_selected"
	CmdTriggerNudge                          = "trigger_nudge"
	CmdMuteWorkspace                         = "mute_workspace"
	CmdPinWorkspace                          = "pin_workspace"
	CmdQueryPRs                              = "query_prs"
	CmdMutePR                                = "mute_pr"
	CmdMuteRepo                              = "mute_repo"
	CmdMuteAuthor                            = "mute_author"
	CmdCollapseRepo                          = "collapse_repo"
	CmdQueryRepos                            = "query_repos"
	CmdQueryAuthors                          = "query_authors"
	CmdFetchPRDetails                        = "fetch_pr_details"
	CmdRefreshPRs                            = "refresh_prs"
	CmdClearSessions                         = "clear_sessions"
	CmdClearWarnings                         = "clear_warnings"
	CmdPRVisited                             = "pr_visited"
	CmdListWorktrees                         = "list_worktrees"
	CmdCreateWorktree                        = "create_worktree"
	CmdDeleteWorktree                        = "delete_worktree"
	CmdGetSettings                           = "get_settings"
	CmdSetSetting                            = "set_setting"
	CmdListPlugins                           = "list_plugins"
	CmdInstallPlugin                         = "install_plugin"
	CmdRemovePlugin                          = "remove_plugin"
	CmdSetPluginPriority                     = "set_plugin_priority"
	CmdAddEndpoint                           = "add_endpoint"
	CmdRemoveEndpoint                        = "remove_endpoint"
	CmdUpdateEndpoint                        = "update_endpoint"
	CmdListEndpoints                         = "list_endpoints"
	CmdSetEndpointRemoteWeb                  = "set_endpoint_remote_web"
	CmdBootstrapEndpoint                     = "bootstrap_endpoint"
	CmdApprovePR                             = "approve_pr"
	CmdMergePR                               = "merge_pr"
	CmdInjectTestPR                          = "inject_test_pr"
	CmdInjectTestSession                     = "inject_test_session"
	CmdGetRecentLocations                    = "get_recent_locations"
	CmdBrowseDirectory                       = "browse_directory"
	CmdInspectPath                           = "inspect_path"
	CmdListBranches                          = "list_branches"
	CmdCreateWorktreeFromBranch              = "create_worktree_from_branch"
	CmdGetDefaultBranch                      = "get_default_branch"
	CmdFetchRemotes                          = "fetch_remotes"
	CmdListRemoteBranches                    = "list_remote_branches"
	CmdEnsureRepo                            = "ensure_repo"
	CmdSubscribeGitStatus                    = "subscribe_git_status"
	CmdUnsubscribeGitStatus                  = "unsubscribe_git_status"
	CmdGetFileDiff                           = "get_file_diff"
	CmdGetRepoInfo                           = "get_repo_info"
	CmdWorkflowRunUpsert                     = "workflow_run_upsert"
	CmdWorkflowCallUpsert                    = "workflow_call_upsert"
	CmdWorkflowRunGet                        = "workflow_run_get"
	CmdWorkflowRunList                       = "workflow_run_list"
	CmdWorkflowRunCancel                     = "workflow_run_cancel"
	CmdSpawnSession                          = "spawn_session"
	CmdAttachSession                         = "attach_session"
	CmdDetachSession                         = "detach_session"
	CmdGetScreenSnapshot                     = "get_screen_snapshot"
	CmdPtyInput                              = "pty_input"
	CmdPtyResize                             = "pty_resize"
	CmdKillSession                           = "kill_session"
	CmdWorkspaceLayoutGet                    = "workspace_layout_get"
	CmdWorkspaceLayoutAddSessionPane         = "workspace_layout_add_session_pane"
	CmdWorkspaceLayoutClosePane              = "workspace_layout_close_pane"
	CmdWorkspaceLayoutFocusPane              = "workspace_layout_focus_pane"
	CmdWorkspaceLayoutRenamePane             = "workspace_layout_rename_pane"
	CmdWorkspaceLayoutSetSplitRatio          = "workspace_layout_set_split_ratio"
	CmdWorkspaceLayoutDockTile               = "workspace_layout_dock_tile"
	CmdWorkspaceLayoutUndockTile             = "workspace_layout_undock_tile"
	CmdWorkspaceLayoutUpdateTile             = "workspace_layout_update_tile"
	CmdWorkspaceLayoutMoveLeaf               = "workspace_layout_move_leaf"
	CmdWorkspaceLayoutMoveLeafToWorkspace    = "workspace_layout_move_leaf_to_workspace"
	CmdWorkspaceLayoutMoveLeafToNewWorkspace = "workspace_layout_move_leaf_to_new_workspace"
	CmdWorkspaceTileContentGet               = "workspace_tile_content_get"
	CmdOpenMarkdown                          = "open_markdown"
	CmdOpenBrowser                           = "open_browser"
	CmdBrowserControl                        = "browser_control"
	CmdBrowserControlResult                  = "browser_control_result"
	CmdRegisterWorkspace                     = "register_workspace"
	CmdUnregisterWorkspace                   = "unregister_workspace"
	CmdRenameSession                         = "rename_session"
	CmdRenameWorkspace                       = "rename_workspace"
	CmdSetWorkspaceRank                      = "set_workspace_rank"
	CmdSetChiefOfStaff                       = "set_chief_of_staff"
)

// WebSocket Events (daemon -> client)
const (
	EventSessionRegistered           = "session_registered"
	EventSessionUnregistered         = "session_unregistered"
	EventSessionStateChanged         = "session_state_changed"
	EventWorkspaceRegistered         = "workspace_registered"
	EventWorkspaceUnregistered       = "workspace_unregistered"
	EventWorkspaceStateChanged       = "workspace_state_changed"
	EventWorkspaceContextChanged     = "workspace_context_changed"
	EventNotebookChanged             = "notebook_changed"
	EventSessionTodosUpdated         = "session_todos_updated"
	EventSessionsUpdated             = "sessions_updated"
	EventRenameResult                = "rename_result"
	EventChiefOfStaffResult          = "chief_of_staff_result"
	EventTicketsUpdated              = "tickets_updated"
	EventTicketResult                = "ticket_result"
	EventTicketActionResult          = "ticket_action_result"
	EventTicketAttachResult          = "ticket_attach_result"
	EventTicketResumeResult          = "ticket_resume_result"
	EventGetPresentationsResult      = "get_presentations_result"
	EventGetPresentationRoundResult  = "get_presentation_round_result"
	EventPresentSubmitRoundResult    = "present_submit_round_result"
	EventPresentCloseResult          = "present_close_result"
	EventPresentationAdded           = "presentation_added"
	EventPresentationUpdated         = "presentation_updated"
	EventDelegateResult              = "delegate_result"
	EventWorkspaceContextResult      = "workspace_context_result"
	EventWorkspaceContextListResult  = "workspace_context_list_result"
	EventNotebookListResult          = "notebook_list_result"
	EventNotebookReadResult          = "notebook_read_result"
	EventNotebookBacklinksResult     = "notebook_backlinks_result"
	EventNotebookWriteResult         = "notebook_write_result"
	EventNotebookSendToChiefResult   = "notebook_send_to_chief_result"
	EventTaskListResult              = "task_list_result"
	EventTaskRetryResult             = "task_retry_result"
	EventTasksChanged                = "tasks_changed"
	EventNotificationListResult      = "notification_list_result"
	EventNotificationMarkReadResult  = "notification_mark_read_result"
	EventNotificationsUpdated        = "notifications_updated"
	EventFsListResult                = "fs_list_result"
	EventFsReadResult                = "fs_read_result"
	EventFsWriteResult               = "fs_write_result"
	EventFsRenameResult              = "fs_rename_result"
	EventFsDeleteResult              = "fs_delete_result"
	EventFsExistsResult              = "fs_exists_result"
	EventFsChanged                   = "fs_changed"
	EventPRsUpdated                  = "prs_updated"
	EventReposUpdated                = "repos_updated"
	EventAuthorsUpdated              = "authors_updated"
	EventInitialState                = "initial_state"
	EventEndpointStatusChanged       = "endpoint_status_changed"
	EventEndpointsUpdated            = "endpoints_updated"
	EventEndpointActionResult        = "endpoint_action_result"
	EventPRActionResult              = "pr_action_result"
	EventRefreshPRsResult            = "refresh_prs_result"
	EventFetchPRDetailsResult        = "fetch_pr_details_result"
	EventBranchChanged               = "branch_changed"
	EventWorktreeCreated             = "worktree_created"
	EventWorktreeDeleted             = "worktree_deleted"
	EventWorktreesUpdated            = "worktrees_updated"
	EventCreateWorktreeResult        = "create_worktree_result"
	EventDeleteWorktreeResult        = "delete_worktree_result"
	EventGitOperationStarted         = "git_operation_started"
	EventGitOperationFinished        = "git_operation_finished"
	EventSettingsUpdated             = "settings_updated"
	EventGitHubHostsUpdated          = "github_hosts_updated"
	EventPluginsUpdated              = "plugins_updated"
	EventPluginActionResult          = "plugin_action_result"
	EventRateLimited                 = "rate_limited"
	EventRecentLocationsResult       = "recent_locations_result"
	EventBrowseDirectoryResult       = "browse_directory_result"
	EventInspectPathResult           = "inspect_path_result"
	EventBranchesResult              = "branches_result"
	EventGetDefaultBranchResult      = "get_default_branch_result"
	EventFetchRemotesResult          = "fetch_remotes_result"
	EventListRemoteBranchesResult    = "list_remote_branches_result"
	EventEnsureRepoResult            = "ensure_repo_result"
	EventGitStatusUpdate             = "git_status_update"
	EventFileDiffResult              = "file_diff_result"
	EventGetRepoInfoResult           = "get_repo_info_result"
	EventWorkflowRunUpdated          = "workflow_run_updated"
	EventWorkflowActionResult        = "workflow_action_result"
	EventPtyOutput                   = "pty_output"
	EventSpawnResult                 = "spawn_result"
	EventAttachResult                = "attach_result"
	EventGetScreenSnapshotResult     = "get_screen_snapshot_result"
	EventSessionExited               = "session_exited"
	EventPtyDesync                   = "pty_desync"
	EventRuntimeRespawned            = "runtime_respawned"
	EventPtyResized                  = "pty_resized"
	EventWorkspaceLayout             = "workspace_layout"
	EventWorkspaceLayoutUpdated      = "workspace_layout_updated"
	EventWorkspaceLayoutActionResult = "workspace_layout_action_result"
	EventWorkspaceTileContent        = "workspace_tile_content"
	EventBrowserControlResponse      = "browser_control_response"
	EventBrowserControlRequest       = "browser_control_request"
	EventCommandError                = "command_error"
)

// Session states (values for SessionState enum)
const (
	StateLaunching       = "launching"
	StateWorking         = "working"
	StateWaitingInput    = "waiting_input"
	StateIdle            = "idle"
	StatePendingApproval = "pending_approval"
	StateScheduled       = "scheduled"
	StateUnknown         = "unknown"
)

// Agent values
const (
	AgentShellValue = "shell"
)

// PR states (values for PR.State field, distinct from session states)
const (
	PRStateWaiting = "waiting" // PR needs attention
)

// PR reasons (why it needs attention)
const (
	PRReasonReadyToMerge     = "ready_to_merge"
	PRReasonCIFailed         = "ci_failed"
	PRReasonChangesRequested = "changes_requested"
	PRReasonReviewNeeded     = "review_needed"
)

// Heat state timing constants
const (
	HeatHotDuration  = 3 * time.Minute  // Stay hot for 3 min after activity
	HeatWarmDuration = 10 * time.Minute // Stay warm for 10 min total
	HeatHotInterval  = 30 * time.Second // Refresh hot PRs every 30s
	HeatWarmInterval = 2 * time.Minute  // Refresh warm PRs every 2 min
	HeatColdInterval = 10 * time.Minute // Refresh cold PRs every 10 min
)

// NeedsDetailRefresh returns true if PR details should be re-fetched
func (pr *PR) NeedsDetailRefresh() bool {
	if !pr.DetailsFetched {
		return true
	}
	// Parse timestamps for comparison
	lastUpdated := Timestamp(pr.LastUpdated).Time()
	detailsFetchedAt := Timestamp(Deref(pr.DetailsFetchedAt)).Time()

	// Invalidate if PR was updated after we fetched details
	if lastUpdated.After(detailsFetchedAt) {
		return true
	}
	// Invalidate if details are older than 5 minutes
	if time.Since(detailsFetchedAt) > 5*time.Minute {
		return true
	}
	return false
}

// ParseMessage parses a JSON message and returns the command type and parsed message
func ParseMessage(data []byte) (string, interface{}, error) {
	// First, extract just the command
	var peek struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return "", nil, err
	}
	if peek.Cmd == "" {
		return "", nil, errors.New("missing cmd field")
	}

	// Parse based on command type
	switch peek.Cmd {
	case CmdClientHello:
		var msg ClientHelloMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdRegister:
		var msg RegisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDelegate:
		var msg DelegateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSetTicketStatus:
		var msg SetTicketStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketInbox:
		var msg TicketInboxMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketList:
		var msg TicketListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketShow:
		var msg TicketShowMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketSubscribe:
		var msg TicketSubscribeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketUnsubscribe:
		var msg TicketUnsubscribeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketTake:
		var msg TicketTakeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketAttach:
		var msg TicketAttachMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketCreate:
		var msg TicketCreateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketComment:
		var msg TicketCommentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetTicket:
		var msg GetTicketMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketChangeStatus:
		var msg TicketChangeStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketAddComment:
		var msg TicketAddCommentMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketResume:
		var msg TicketResumeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPresentOpen:
		var msg PresentOpenMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPresentFeedback:
		var msg PresentFeedbackMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetPresentations:
		var msg GetPresentationsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetPresentationRound:
		var msg GetPresentationRoundMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPresentSubmitRound:
		var msg PresentSubmitRoundMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPresentClose:
		var msg PresentCloseMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTicketEditDescription:
		var msg TicketEditDescriptionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceContextCheckout:
		var msg WorkspaceContextCheckoutMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceContextUpdate:
		var msg WorkspaceContextUpdateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceContextStatus:
		var msg WorkspaceContextStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceContextList:
		var msg WorkspaceContextListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceContextCompact:
		var msg WorkspaceContextCompactMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceContextRollback:
		var msg WorkspaceContextRollbackMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotebookList:
		var msg NotebookListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotebookRead:
		var msg NotebookReadMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotebookWrite:
		var msg NotebookWriteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotebookGuide:
		var msg NotebookGuideMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdJournalAppend:
		var msg JournalAppendMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotebookBacklinks:
		var msg NotebookBacklinksMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotebookSendToChief:
		var msg NotebookSendToChiefMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTaskList:
		var msg TaskListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTaskRetry:
		var msg TaskRetryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotificationList:
		var msg NotificationListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdNotificationMarkRead:
		var msg NotificationMarkReadMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsList:
		var msg FsListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsRead:
		var msg FsReadMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsWrite:
		var msg FsWriteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsRename:
		var msg FsRenameMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsDelete:
		var msg FsDeleteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsExists:
		var msg FsExistsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUnregister:
		var msg UnregisterMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdState:
		var msg StateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSetSessionResumeID:
		var msg SetSessionResumeIDMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdStop:
		var msg StopMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTodos:
		var msg TodosMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQuery:
		var msg QueryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdHeartbeat:
		var msg HeartbeatMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSessionVisualized:
		var msg SessionVisualizedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSessionSelected:
		var msg SessionSelectedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceSelected:
		var msg WorkspaceSelectedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdTriggerNudge:
		var msg TriggerNudgeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMuteWorkspace:
		var msg MuteWorkspaceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPinWorkspace:
		var msg PinWorkspaceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal pin_workspace: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdQueryPRs:
		var msg QueryPRsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMutePR:
		var msg MutePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMuteRepo:
		var msg MuteRepoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMuteAuthor:
		var msg MuteAuthorMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCollapseRepo:
		var msg CollapseRepoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQueryRepos:
		var msg QueryReposMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdQueryAuthors:
		var msg QueryAuthorsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFetchPRDetails:
		var msg FetchPRDetailsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdRefreshPRs:
		var msg RefreshPRsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdClearSessions:
		var msg ClearSessionsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdClearWarnings:
		var msg ClearWarningsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPRVisited:
		var msg PRVisitedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListWorktrees:
		var msg ListWorktreesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCreateWorktree:
		var msg CreateWorktreeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDeleteWorktree:
		var msg DeleteWorktreeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetSettings:
		var msg GetSettingsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSetSetting:
		var msg SetSettingMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListPlugins:
		var msg ListPluginsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdInstallPlugin:
		var msg InstallPluginMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdRemovePlugin:
		var msg RemovePluginMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSetPluginPriority:
		var msg SetPluginPriorityMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAddEndpoint:
		var msg AddEndpointMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdRemoveEndpoint:
		var msg RemoveEndpointMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUpdateEndpoint:
		var msg UpdateEndpointMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListEndpoints:
		var msg ListEndpointsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSetEndpointRemoteWeb:
		var msg SetEndpointRemoteWebMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdBootstrapEndpoint:
		var msg BootstrapEndpointMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdApprovePR:
		var msg ApprovePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdMergePR:
		var msg MergePRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdInjectTestPR:
		var msg InjectTestPRMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdInjectTestSession:
		var msg InjectTestSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetRecentLocations:
		var msg GetRecentLocationsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdBrowseDirectory:
		var msg BrowseDirectoryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdInspectPath:
		var msg InspectPathMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListBranches:
		var msg ListBranchesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCreateWorktreeFromBranch:
		var msg CreateWorktreeFromBranchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetDefaultBranch:
		var msg GetDefaultBranchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFetchRemotes:
		var msg FetchRemotesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdListRemoteBranches:
		var msg ListRemoteBranchesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdEnsureRepo:
		var msg EnsureRepoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSubscribeGitStatus:
		var msg SubscribeGitStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUnsubscribeGitStatus:
		var msg UnsubscribeGitStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetFileDiff:
		var msg GetFileDiffMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdGetRepoInfo:
		var msg GetRepoInfoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWorkflowRunUpsert:
		var msg WorkflowRunUpsertMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workflow_run_upsert: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkflowCallUpsert:
		var msg WorkflowCallUpsertMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workflow_call_upsert: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkflowRunGet:
		var msg WorkflowRunGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workflow_run_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkflowRunList:
		var msg WorkflowRunListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workflow_run_list: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkflowRunCancel:
		var msg WorkflowRunCancelMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workflow_run_cancel: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSpawnSession:
		var msg SpawnSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal spawn_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAttachSession:
		var msg AttachSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal attach_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdDetachSession:
		var msg DetachSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal detach_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdGetScreenSnapshot:
		var msg GetScreenSnapshotMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal get_screen_snapshot: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdPtyInput:
		var msg PtyInputMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal pty_input: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdPtyResize:
		var msg PtyResizeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal pty_resize: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdKillSession:
		var msg KillSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal kill_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutGet:
		var msg WorkspaceLayoutGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutAddSessionPane:
		var msg WorkspaceLayoutAddSessionPaneMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_add_session_pane: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutClosePane:
		var msg WorkspaceLayoutClosePaneMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_close_pane: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutFocusPane:
		var msg WorkspaceLayoutFocusPaneMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_focus_pane: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutRenamePane:
		var msg WorkspaceLayoutRenamePaneMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_rename_pane: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutSetSplitRatio:
		var msg WorkspaceLayoutSetSplitRatioMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_set_split_ratio: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutDockTile:
		var msg WorkspaceLayoutDockTileMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_dock_tile: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutUndockTile:
		var msg WorkspaceLayoutUndockTileMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_undock_tile: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutUpdateTile:
		var msg WorkspaceLayoutUpdateTileMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_update_tile: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutMoveLeaf:
		var msg WorkspaceLayoutMoveLeafMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_move_leaf: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutMoveLeafToWorkspace:
		var msg WorkspaceLayoutMoveLeafToWorkspaceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_move_leaf_to_workspace: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceLayoutMoveLeafToNewWorkspace:
		var msg WorkspaceLayoutMoveLeafToNewWorkspaceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_layout_move_leaf_to_new_workspace: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdWorkspaceTileContentGet:
		var msg WorkspaceTileContentGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal workspace_tile_content_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdOpenMarkdown:
		var msg OpenMarkdownMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal open_markdown: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdOpenBrowser:
		var msg OpenBrowserMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal open_browser: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdBrowserControl:
		var msg BrowserControlMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal browser_control: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdBrowserControlResult:
		var msg BrowserControlResultMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal browser_control_result: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdRegisterWorkspace:
		var msg RegisterWorkspaceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal register_workspace: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdUnregisterWorkspace:
		var msg UnregisterWorkspaceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal unregister_workspace: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdRenameSession:
		var msg RenameSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal rename_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdRenameWorkspace:
		var msg RenameWorkspaceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal rename_workspace: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSetWorkspaceRank:
		var msg SetWorkspaceRankMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal set_workspace_rank: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSetChiefOfStaff:
		var msg SetChiefOfStaffMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal set_chief_of_staff: %w", err)
		}
		return peek.Cmd, &msg, nil

	default:
		return "", nil, errors.New("unknown command: " + peek.Cmd)
	}
}
