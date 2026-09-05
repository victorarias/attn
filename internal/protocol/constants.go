package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const ProtocolVersion = "288"

const (
	ErrorCodeConflict             = "conflict"
	ErrorCodeUndeclaredCollection = "undeclared_collection"
	ErrorCodeInvalidQuery         = "invalid_query"
	ErrorCodeCollectionUndefined  = "collection_undefined"
	ErrorCodeCollectionRedeclared = "collection_redeclared"
	ErrorCodeSubscriptionLimit    = "subscription_limit"
	ErrorCodeReconcileOwed        = "reconcile_owed"
	ErrorCodeUnauthorizedClient   = "unauthorized_client"
)

// Tripwire: measured 2026-08-13 against production, where no workspace held
// more than one docked tile.
const DocSubscriptionsPerClient = 64

// Measured 2026-08-10: 32KiB is 1.75x the largest assistant prose block across 120
// transcripts (18,713 chars), and fits the daemon's 64KiB socket frame after escaping.
const AgentMessageMaxChars = 32 * 1024

const CapabilityWorkspaceSessions = "workspace_sessions"

const CapabilityBrowserHost = "browser_host"

const CapabilityBinaryPtyOutput = "binary_pty_output"

// Says nothing about transport, which is CapabilityBinaryPtyOutput's call. The hub relays
// kitty descriptions as JSON over a text pipe and cannot take a binary frame.
const CapabilityKittyImages = "kitty_images"

type SessionAgent = string

const (
	SessionAgentClaude  SessionAgent = "claude"
	SessionAgentCodex   SessionAgent = "codex"
	SessionAgentCopilot SessionAgent = "copilot"
	SessionAgentShell   SessionAgent = "shell"
)

const (
	CmdClientHello                           = "client_hello"
	CmdRegister                              = "register"
	CmdDelegate                              = "delegate"
	CmdDelegateStatus                        = "delegate_status"
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
	CmdDocDefine                             = "doc_define"
	CmdDocUndefine                           = "doc_undefine"
	CmdDocCollections                        = "doc_collections"
	CmdDocPut                                = "doc_put"
	CmdDocGet                                = "doc_get"
	CmdDocDelete                             = "doc_delete"
	CmdDocQuery                              = "doc_query"
	CmdDocCount                              = "doc_count"
	CmdDocSubscribe                          = "doc_subscribe"
	CmdDocUnsubscribe                        = "doc_unsubscribe"
	CmdAppList                               = "app_list"
	CmdAppStatus                             = "app_status"
	CmdAppSetEnabled                         = "app_set_enabled"
	CmdAppRemove                             = "app_remove"
	CmdAppApply                              = "app_apply"
	CmdAppRollback                           = "app_rollback"
	CmdAppLogs                               = "app_logs"
	CmdAppRuntimeStatus                      = "app_runtime_status"
	CmdAppRuntimeRestart                     = "app_runtime_restart"
	CmdAppWatch                              = "app_watch"
	CmdAppViewCrash                          = "app_view_crash"
	CmdAppCommand                            = "app_command"
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
	CmdFsReadAsset                           = "fs_read_asset"
	CmdFsWrite                               = "fs_write"
	CmdFsRename                              = "fs_rename"
	CmdFsDelete                              = "fs_delete"
	CmdFsExists                              = "fs_exists"
	CmdFsWatch                               = "fs_watch"
	CmdFsUnwatch                             = "fs_unwatch"
	CmdFsIndex                               = "fs_index"
	CmdUnregister                            = "unregister"
	CmdState                                 = "state"
	CmdHookNotification                      = "hook_notification"
	CmdHookStopFailure                       = "hook_stop_failure"
	CmdHookCompaction                        = "hook_compaction"
	CmdSetSessionResumeID                    = "set_session_resume_id"
	CmdSessionInstructions                   = "session_instructions"
	CmdSessionTranscript                     = "session_transcript"
	CmdSessionList                           = "session_list"
	CmdSessionShow                           = "session_show"
	CmdStateExplain                          = "state_explain"
	CmdAgentPeek                             = "agent_peek"
	CmdAgentMsg                              = "agent_msg"
	CmdAgentClose                            = "agent_close"
	CmdAgentInbox                            = "agent_inbox"
	CmdAgentMsgStatus                        = "agent_msg_status"
	CmdSeedPlant                             = "seed_plant"
	CmdSeedPlot                              = "seed_plot"
	CmdSeedList                              = "seed_list"
	CmdSeedShow                              = "seed_show"
	CmdSeedDocumentGet                       = "seed_document_get"
	CmdSeedArtifactTransfer                  = "seed_artifact_transfer"
	CmdSeedArtifactTarget                    = "seed_artifact_target"
	CmdSeedEdit                              = "seed_edit"
	CmdSeedSetResume                         = "seed_set_resume"
	CmdSeedTransition                        = "seed_transition"
	CmdSeedNote                              = "seed_note"
	CmdSeedNotes                             = "seed_notes"
	CmdSeedWatch                             = "seed_watch"
	CmdSeedLink                              = "seed_link"
	CmdSeedReady                             = "seed_ready"
	CmdSeedResume                            = "seed_resume"
	CmdSeedSendToChief                       = "seed_send_to_chief"
	CmdSeedReviewStart                       = "seed_review_start"
	CmdSeedReviewShow                        = "seed_review_show"
	CmdSeedReviewCancel                      = "seed_review_cancel"
	CmdSeedReviewRetry                       = "seed_review_retry"
	CmdSeedReviewKeep                        = "seed_review_keep"
	CmdSeedReviewDraft                       = "seed_review_draft"
	CmdCrewList                              = "crew_list"
	CmdCrewWake                              = "crew_wake"
	CmdCrewSleep                             = "crew_sleep"
	CmdCrewSet                               = "crew_set"
	CmdCrewPrime                             = "crew_prime"
	CmdCrewHandoff                           = "crew_handoff"
	CmdStop                                  = "stop"
	CmdTodos                                 = "todos"
	CmdFilesEdited                           = "files_edited"
	CmdPullRequestCreated                    = "pull_request_created"
	CmdPullRequestForget                     = "pull_request_forget"
	CmdQuery                                 = "query"
	CmdHeartbeat                             = "heartbeat"
	CmdSessionSelected                       = "session_selected"
	CmdWorkspaceSelected                     = "workspace_selected"
	CmdTriggerNudge                          = "trigger_nudge"
	CmdSettleTurn                            = "settle_turn"
	CmdSnoozeTurn                            = "snooze_turn"
	CmdWakeTurn                              = "wake_turn"
	CmdCancelCountdown                       = "cancel_countdown"
	CmdMuteWorkspace                         = "mute_workspace"
	CmdPinWorkspace                          = "pin_workspace"
	CmdPinSession                            = "pin_session"
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
	CmdInstallBundledPlugin                  = "install_bundled_plugin"
	CmdUninstallPlugin                       = "uninstall_plugin"
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
	CmdRecentFiles                           = "recent_files"
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
	CmdAutomationApply                       = "automation_apply"
	CmdAutomationRun                         = "automation_run"
	CmdAutomationDefinitionsGet              = "automation_definitions_get"
	CmdAutomationDefinitionGet               = "automation_definition_get"
	CmdAutomationRunsGet                     = "automation_runs_get"
	CmdAutomationSetEnabled                  = "automation_set_enabled"
	CmdAutomationDelete                      = "automation_delete"
	CmdAutomationCleanup                     = "automation_cleanup"
	CmdAutomationValidate                    = "automation_validate"
	CmdSpawnSession                          = "spawn_session"
	CmdAttachSession                         = "attach_session"
	CmdDetachSession                         = "detach_session"
	CmdGetScreenSnapshot                     = "get_screen_snapshot"
	CmdGetKittyImage                         = "get_kitty_image"
	CmdPtyInput                              = "pty_input"
	CmdTerminalPointerActivity               = "terminal_pointer_activity"
	CmdAgentPrompt                           = "agent_prompt"
	CmdAgentToolDetail                       = "agent_tool_detail"
	CmdAgentClearQueue                       = "agent_clear_queue"
	CmdAgentAttach                           = "agent_attach"
	CmdAgentHistory                          = "agent_history"
	CmdAgentSetModel                         = "agent_set_model"
	CmdListPastConversations                 = "list_past_conversations"
	CmdBusStatusGet                          = "bus_status_get"
	CmdBusSetConsumerEnabled                 = "bus_set_consumer_enabled"
	CmdPtyResize                             = "pty_resize"
	CmdKillSession                           = "kill_session"
	CmdReloadSession                         = "reload_session"
	CmdSetClientPresence                     = "set_client_presence"
	CmdActivityStatus                        = "activity_status"
	CmdClearSessionActivity                  = "clear_session_activity"
	CmdSetTerminalTheme                      = "set_terminal_theme"
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
	CmdOpenSeed                              = "open_seed"
	CmdOpenSentFiles                         = "open_sent_files"
	CmdSessionMessagesGet                    = "session_messages_get"
	CmdSessionAnnotationsGet                 = "session_annotations_get"
	CmdSessionAnnotationsSave                = "session_annotations_save"
	CmdSessionAnnotationsClear               = "session_annotations_clear"
	CmdSessionAnnotationsSubmit              = "session_annotations_submit"
	CmdMarkdownAnnotationsGet                = "markdown_annotations_get"
	CmdMarkdownAnnotationsSave               = "markdown_annotations_save"
	CmdMarkdownAnnotationsClear              = "markdown_annotations_clear"
	CmdMarkdownAnnotationsSubmit             = "markdown_annotations_submit"
	CmdOpenBrowser                           = "open_browser"
	CmdBrowserControl                        = "browser_control"
	CmdBrowserControlResult                  = "browser_control_result"
	CmdRegisterWorkspace                     = "register_workspace"
	CmdUnregisterWorkspace                   = "unregister_workspace"
	CmdRenameSession                         = "rename_session"
	CmdRenameWorkspace                       = "rename_workspace"
	CmdSetWorkspaceRank                      = "set_workspace_rank"
	CmdSetChiefOfStaff                       = "set_chief_of_staff"
	CmdSetSessionContextWindowCap            = "set_session_context_window_cap"
)

// First group is agent-reachable over the unix socket and records proposals
// only; the second is the app's alone, because promotion needs a human.
const (
	CmdAutoModeShow     = "automode_show"
	CmdAutoModeEnvSlot  = "automode_env_slot"
	CmdAutoModeEnvNotes = "automode_env_notes"
	CmdAutoModePropose  = "automode_propose"
	CmdAutoModeDenials  = "automode_denials"

	CmdAutoModeGet           = "automode_get"
	CmdAutoModePromote       = "automode_promote"
	CmdAutoModeDiscard       = "automode_discard"
	CmdAutoModePatternAdd    = "automode_pattern_add"
	CmdAutoModePatternRemove = "automode_pattern_remove"
	CmdAutoModeModelSet      = "automode_model_set"
	CmdAutoModeModels        = "automode_models"
)

const EventAutoModeEnvSetResult = "automode_env_set_result"

const EventAutoModeStateChanged = "automode_state_changed"

// Per-action automations result events (socket + WS share one command set;
const (
	EventAutomationApplyResult       = "automation_apply_result"
	EventAutomationValidateResult    = "automation_validate_result"
	EventAutomationDefinitionsResult = "automation_definitions_result"
	EventAutomationDefinitionResult  = "automation_definition_result"
	EventAutomationRunsResult        = "automation_runs_result"
	EventAutomationRunResult         = "automation_run_result"
	EventAutomationSetEnabledResult  = "automation_set_enabled_result"
	EventAutomationDeleteResult      = "automation_delete_result"
	EventAutomationCleanupResult     = "automation_cleanup_result"
)

const EventAutomationsChanged = "automations_changed"

const (
	EventSessionRegistered               = "session_registered"
	EventSessionUnregistered             = "session_unregistered"
	EventSessionStateChanged             = "session_state_changed"
	EventWorkspaceRegistered             = "workspace_registered"
	EventWorkspaceUnregistered           = "workspace_unregistered"
	EventWorkspaceStateChanged           = "workspace_state_changed"
	EventWorkspaceContextChanged         = "workspace_context_changed"
	EventNotebookChanged                 = "notebook_changed"
	EventSessionTodosUpdated             = "session_todos_updated"
	EventSessionsUpdated                 = "sessions_updated"
	EventRenameResult                    = "rename_result"
	EventChiefOfStaffResult              = "chief_of_staff_result"
	EventSessionContextWindowCapResult   = "session_context_window_cap_result"
	EventGardenSeedsUpdated              = "garden_seeds_updated"
	EventGardenReviewUpdated             = "garden_review_updated"
	EventAppsUpdated                     = "apps_updated"
	EventAppCommandResult                = "app_command_result"
	EventDocSubscriptionDelivery         = "doc_subscription_delivery"
	EventDocSubscriptionEnded            = "doc_subscription_ended"
	EventCrewUpdated                     = "crew_updated"
	EventCrewWakeResult                  = "crew_wake_result"
	EventCrewSleepResult                 = "crew_sleep_result"
	EventTicketAttachResult              = "ticket_attach_result"
	EventGetPresentationsResult          = "get_presentations_result"
	EventGetPresentationRoundResult      = "get_presentation_round_result"
	EventPresentSubmitRoundResult        = "present_submit_round_result"
	EventPresentCloseResult              = "present_close_result"
	EventPresentationAdded               = "presentation_added"
	EventPresentationUpdated             = "presentation_updated"
	EventDelegateResult                  = "delegate_result"
	EventDelegationOperation             = "delegation_operation"
	EventWorkspaceContextResult          = "workspace_context_result"
	EventWorkspaceContextListResult      = "workspace_context_list_result"
	EventNotebookListResult              = "notebook_list_result"
	EventNotebookReadResult              = "notebook_read_result"
	EventNotebookBacklinksResult         = "notebook_backlinks_result"
	EventNotebookWriteResult             = "notebook_write_result"
	EventNotebookSendToChiefResult       = "notebook_send_to_chief_result"
	EventTaskListResult                  = "task_list_result"
	EventTaskRetryResult                 = "task_retry_result"
	EventTasksChanged                    = "tasks_changed"
	EventNotificationListResult          = "notification_list_result"
	EventNotificationMarkReadResult      = "notification_mark_read_result"
	EventNotificationsUpdated            = "notifications_updated"
	EventFsListResult                    = "fs_list_result"
	EventFsReadResult                    = "fs_read_result"
	EventFsReadAssetResult               = "fs_read_asset_result"
	EventFsWriteResult                   = "fs_write_result"
	EventFsRenameResult                  = "fs_rename_result"
	EventFsDeleteResult                  = "fs_delete_result"
	EventFsExistsResult                  = "fs_exists_result"
	EventFsWatchResult                   = "fs_watch_result"
	EventFsUnwatchResult                 = "fs_unwatch_result"
	EventFsIndexResult                   = "fs_index_result"
	EventFsChanged                       = "fs_changed"
	EventPRsUpdated                      = "prs_updated"
	EventReposUpdated                    = "repos_updated"
	EventAuthorsUpdated                  = "authors_updated"
	EventInitialState                    = "initial_state"
	EventEndpointStatusChanged           = "endpoint_status_changed"
	EventEndpointsUpdated                = "endpoints_updated"
	EventEndpointActionResult            = "endpoint_action_result"
	EventPRActionResult                  = "pr_action_result"
	EventRefreshPRsResult                = "refresh_prs_result"
	EventFetchPRDetailsResult            = "fetch_pr_details_result"
	EventBranchChanged                   = "branch_changed"
	EventWorktreeCreated                 = "worktree_created"
	EventWorktreeDeleted                 = "worktree_deleted"
	EventWorktreesUpdated                = "worktrees_updated"
	EventCreateWorktreeResult            = "create_worktree_result"
	EventDeleteWorktreeResult            = "delete_worktree_result"
	EventGitOperationStarted             = "git_operation_started"
	EventGitOperationFinished            = "git_operation_finished"
	EventSettingsUpdated                 = "settings_updated"
	EventGitHubHostsUpdated              = "github_hosts_updated"
	EventPluginsUpdated                  = "plugins_updated"
	EventPluginActionResult              = "plugin_action_result"
	EventRateLimited                     = "rate_limited"
	EventRecentLocationsResult           = "recent_locations_result"
	EventRecentFilesResult               = "recent_files_result"
	EventBrowseDirectoryResult           = "browse_directory_result"
	EventInspectPathResult               = "inspect_path_result"
	EventBranchesResult                  = "branches_result"
	EventGetDefaultBranchResult          = "get_default_branch_result"
	EventFetchRemotesResult              = "fetch_remotes_result"
	EventListRemoteBranchesResult        = "list_remote_branches_result"
	EventEnsureRepoResult                = "ensure_repo_result"
	EventGitStatusUpdate                 = "git_status_update"
	EventFileDiffResult                  = "file_diff_result"
	EventGetRepoInfoResult               = "get_repo_info_result"
	EventWorkflowRunUpdated              = "workflow_run_updated"
	EventWorkflowActionResult            = "workflow_action_result"
	EventPtyOutput                       = "pty_output"
	EventPtyInputProbeResult             = "pty_input_probe_result"
	EventAgentEvent                      = "agent_event"
	EventSpawnResult                     = "spawn_result"
	EventReloadSessionResult             = "reload_session_result"
	EventAttachResult                    = "attach_result"
	EventGetScreenSnapshotResult         = "get_screen_snapshot_result"
	EventSessionExited                   = "session_exited"
	EventPtyDesync                       = "pty_desync"
	EventKittyPlacements                 = "kitty_placements"
	EventKittyImageResult                = "kitty_image_result"
	EventRuntimeRespawned                = "runtime_respawned"
	EventPtyResized                      = "pty_resized"
	EventWorkspaceLayout                 = "workspace_layout"
	EventWorkspaceLayoutUpdated          = "workspace_layout_updated"
	EventWorkspaceLayoutActionResult     = "workspace_layout_action_result"
	EventWorkspaceTileContent            = "workspace_tile_content"
	EventOpenMarkdownResult              = "open_markdown_result"
	EventOpenSeedResult                  = "open_seed_result"
	EventSeedDocumentGetResult           = "seed_document_get_result"
	EventSeedArtifactTransferResult      = "seed_artifact_transfer_result"
	EventSeedArtifactTargetResult        = "seed_artifact_target_result"
	EventSeedResumeResult                = "seed_resume_result"
	EventSeedSendToChiefResult           = "seed_send_to_chief_result"
	EventSeedReviewResult                = "seed_review_result"
	EventSeedReviewDraftResult           = "seed_review_draft_result"
	EventSeedTransitionResult            = "seed_transition_result"
	EventSeedNoteResult                  = "seed_note_result"
	EventSessionMessagesGetResult        = "session_messages_get_result"
	EventSessionMessagesChanged          = "session_messages_changed"
	EventPastConversationsResult         = "past_conversations_result"
	EventBusStatusResult                 = "bus_status_result"
	EventBusSetConsumerEnabledResult     = "bus_set_consumer_enabled_result"
	EventAutoModeStateResult             = "automode_state_result"
	EventAutoModePromoteResult           = "automode_promote_result"
	EventAutoModeDiscardResult           = "automode_discard_result"
	EventAutoModePatternResult           = "automode_pattern_result"
	EventAutoModeModelSetResult          = "automode_model_set_result"
	EventAutoModeModelsResult            = "automode_models_result"
	EventSessionAnnotationsGetResult     = "session_annotations_get_result"
	EventSessionAnnotationsSaveResult    = "session_annotations_save_result"
	EventSessionAnnotationsClearResult   = "session_annotations_clear_result"
	EventSessionAnnotationsSubmitResult  = "session_annotations_submit_result"
	EventMarkdownAnnotationsGetResult    = "markdown_annotations_get_result"
	EventMarkdownAnnotationsSaveResult   = "markdown_annotations_save_result"
	EventMarkdownAnnotationsClearResult  = "markdown_annotations_clear_result"
	EventMarkdownAnnotationsSubmitResult = "markdown_annotations_submit_result"
	EventBrowserControlResponse          = "browser_control_response"
	EventBrowserControlRequest           = "browser_control_request"
	EventCommandError                    = "command_error"
	EventClientEvictionNotice            = "client_eviction_notice"
)

const (
	StateLaunching       = "launching"
	StateWorking         = "working"
	StateWaitingInput    = "waiting_input"
	StateIdle            = "idle"
	StatePendingApproval = "pending_approval"
	StateScheduled       = "scheduled"
	StateUnknown         = "unknown"
)

const (
	AgentShellValue = "shell"
)

const (
	PRStateWaiting = "waiting"
)

const (
	PRReasonReadyToMerge     = "ready_to_merge"
	PRReasonCIFailed         = "ci_failed"
	PRReasonChangesRequested = "changes_requested"
	PRReasonReviewNeeded     = "review_needed"
)

const (
	HeatHotDuration  = 3 * time.Minute
	HeatWarmDuration = 10 * time.Minute
	HeatHotInterval  = 30 * time.Second
	HeatWarmInterval = 2 * time.Minute
	HeatColdInterval = 10 * time.Minute
)

func (pr *PR) NeedsDetailRefresh() bool {
	if !pr.DetailsFetched {
		return true
	}
	lastUpdated := Timestamp(pr.LastUpdated).Time()
	detailsFetchedAt := Timestamp(Deref(pr.DetailsFetchedAt)).Time()

	if lastUpdated.After(detailsFetchedAt) {
		return true
	}
	if time.Since(detailsFetchedAt) > 5*time.Minute {
		return true
	}
	return false
}

func ParseMessage(data []byte) (string, interface{}, error) {
	var peek struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return "", nil, err
	}
	if peek.Cmd == "" {
		return "", nil, errors.New("missing cmd field")
	}

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

	case CmdAutomationApply:
		var msg AutomationApplyMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil
	case CmdAutomationRun:
		var msg AutomationRunMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutomationDefinitionsGet:
		var msg AutomationDefinitionsGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutomationDefinitionGet:
		var msg AutomationDefinitionGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutomationValidate:
		var msg AutomationValidateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutomationRunsGet:
		var msg AutomationRunsGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutomationSetEnabled:
		var msg AutomationSetEnabledMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutomationDelete:
		var msg AutomationDeleteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutomationCleanup:
		var msg AutomationCleanupMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDelegateStatus:
		var msg DelegateStatusMessage
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

	case CmdDocDefine:
		var msg DocDefineMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocUndefine:
		var msg DocUndefineMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocCollections:
		var msg DocCollectionsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocPut:
		var msg DocPutMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocGet:
		var msg DocGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocDelete:
		var msg DocDeleteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocQuery:
		var msg DocQueryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocCount:
		var msg DocCountMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocSubscribe:
		var msg DocSubscribeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdDocUnsubscribe:
		var msg DocUnsubscribeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppList:
		var msg AppListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppStatus:
		var msg AppStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppSetEnabled:
		var msg AppSetEnabledMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppRemove:
		var msg AppRemoveMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppApply:
		var msg AppApplyMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppRollback:
		var msg AppRollbackMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppLogs:
		var msg AppLogsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppRuntimeStatus:
		var msg AppRuntimeStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppRuntimeRestart:
		var msg AppRuntimeRestartMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppViewCrash:
		var msg AppViewCrashMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppCommand:
		var msg AppCommandMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAppWatch:
		var msg AppWatchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeShow:
		var msg AutoModeShowMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeEnvSlot:
		var msg AutoModeEnvSlotMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeEnvNotes:
		var msg AutoModeEnvNotesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModePropose:
		var msg AutoModeProposeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeDenials:
		var msg AutoModeDenialsMessage
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

	case CmdFsReadAsset:
		var msg FsReadAssetMessage
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

	case CmdFsWatch:
		var msg FsWatchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsUnwatch:
		var msg FsUnwatchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdFsIndex:
		var msg FsIndexMessage
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

	case CmdHookNotification:
		var msg HookNotificationMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdHookStopFailure:
		var msg HookStopFailureMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdHookCompaction:
		var msg HookCompactionMessage
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

	case CmdSessionInstructions:
		var msg SessionInstructionsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSessionTranscript:
		var msg SessionTranscriptMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSessionList:
		var msg SessionListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSessionShow:
		var msg SessionShowMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdStateExplain:
		var msg StateExplainMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAgentPeek:
		var msg AgentPeekMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAgentMsg:
		var msg AgentMsgMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAgentClose:
		var msg AgentCloseMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAgentInbox:
		var msg AgentInboxMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdAgentMsgStatus:
		var msg AgentMsgStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCrewList:
		var msg CrewListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCrewWake:
		var msg CrewWakeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCrewSleep:
		var msg CrewSleepMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCrewSet:
		var msg CrewSetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCrewPrime:
		var msg CrewPrimeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCrewHandoff:
		var msg CrewHandoffMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedPlant:
		var msg SeedPlantMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedPlot:
		var msg SeedPlotMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedList:
		var msg SeedListMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedShow:
		var msg SeedShowMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedDocumentGet:
		var msg SeedDocumentGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedArtifactTransfer:
		var msg SeedArtifactTransferMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedArtifactTarget:
		var msg SeedArtifactTargetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedEdit:
		var msg SeedEditMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedSetResume:
		var msg SeedSetResumeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedTransition:
		var msg SeedTransitionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedNote:
		var msg SeedNoteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedNotes:
		var msg SeedNotesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedWatch:
		var msg SeedWatchMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedLink:
		var msg SeedLinkMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedReady:
		var msg SeedReadyMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedResume:
		var msg SeedResumeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedSendToChief:
		var msg SeedSendToChiefMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedReviewStart:
		var msg SeedReviewStartMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedReviewShow:
		var msg SeedReviewShowMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedReviewCancel:
		var msg SeedReviewCancelMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedReviewRetry:
		var msg SeedReviewRetryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedReviewKeep:
		var msg SeedReviewKeepMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSeedReviewDraft:
		var msg SeedReviewDraftMessage
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

	case CmdFilesEdited:
		var msg FilesEditedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPullRequestCreated:
		var msg PullRequestCreatedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdPullRequestForget:
		var msg PullRequestForgetMessage
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

	case CmdSettleTurn:
		var msg SettleTurnMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdSnoozeTurn:
		var msg SnoozeTurnMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdWakeTurn:
		var msg WakeTurnMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdCancelCountdown:
		var msg CancelCountdownMessage
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

	case CmdPinSession:
		var msg PinSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal pin_session: %w", err)
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

	case CmdInstallBundledPlugin:
		var msg InstallBundledPluginMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, err
		}
		return peek.Cmd, &msg, nil

	case CmdUninstallPlugin:
		var msg UninstallPluginMessage
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

	case CmdRecentFiles:
		var msg RecentFilesMessage
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

	case CmdGetKittyImage:
		var msg GetKittyImageMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal get_kitty_image: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdPtyInput:
		var msg PtyInputMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal pty_input: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdTerminalPointerActivity:
		var msg TerminalPointerActivityMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal terminal_pointer_activity: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAgentPrompt:
		var msg AgentPromptMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal agent_prompt: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAgentToolDetail:
		var msg AgentToolDetailMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal agent_tool_detail: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAgentClearQueue:
		var msg AgentClearQueueMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal agent_clear_queue: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAgentAttach:
		var msg AgentAttachMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal agent_attach: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAgentHistory:
		var msg AgentHistoryMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal agent_history: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAgentSetModel:
		var msg AgentSetModelMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal agent_set_model: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdListPastConversations:
		var msg ListPastConversationsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal list_past_conversations: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdBusStatusGet:
		var msg BusStatusGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal bus_status_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeGet:
		var msg AutoModeGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal automode_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModePromote:
		var msg AutoModePromoteMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal automode_promote: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeDiscard:
		var msg AutoModeDiscardMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal automode_discard: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModePatternAdd:
		var msg AutoModePatternAddMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal automode_pattern_add: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModePatternRemove:
		var msg AutoModePatternRemoveMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal automode_pattern_remove: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeModelSet:
		var msg AutoModeModelSetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal automode_model_set: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdAutoModeModels:
		var msg AutoModeModelsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal automode_models: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdBusSetConsumerEnabled:
		var msg BusSetConsumerEnabledMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal bus_set_consumer_enabled: %w", err)
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

	case CmdReloadSession:
		var msg ReloadSessionMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal reload_session: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSetClientPresence:
		var msg SetClientPresenceMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal set_client_presence: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdActivityStatus:
		var msg ActivityStatusMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal activity_status: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdClearSessionActivity:
		var msg ClearSessionActivityMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal clear_session_activity: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSetTerminalTheme:
		var msg SetTerminalThemeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal set_terminal_theme: %w", err)
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

	case CmdOpenSeed:
		var msg OpenSeedMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal open_seed: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdOpenSentFiles:
		var msg OpenSentFilesMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal open_sent_files: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSessionMessagesGet:
		var msg SessionMessagesGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal session_messages_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSessionAnnotationsGet:
		var msg SessionAnnotationsGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal session_annotations_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSessionAnnotationsSave:
		var msg SessionAnnotationsSaveMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal session_annotations_save: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSessionAnnotationsClear:
		var msg SessionAnnotationsClearMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal session_annotations_clear: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdSessionAnnotationsSubmit:
		var msg SessionAnnotationsSubmitMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal session_annotations_submit: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdMarkdownAnnotationsGet:
		var msg MarkdownAnnotationsGetMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal markdown_annotations_get: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdMarkdownAnnotationsSave:
		var msg MarkdownAnnotationsSaveMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal markdown_annotations_save: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdMarkdownAnnotationsClear:
		var msg MarkdownAnnotationsClearMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal markdown_annotations_clear: %w", err)
		}
		return peek.Cmd, &msg, nil

	case CmdMarkdownAnnotationsSubmit:
		var msg MarkdownAnnotationsSubmitMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal markdown_annotations_submit: %w", err)
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

	case CmdSetSessionContextWindowCap:
		var msg SetSessionContextWindowCapMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", nil, fmt.Errorf("unmarshal set_session_context_window_cap: %w", err)
		}
		return peek.Cmd, &msg, nil

	default:
		return "", nil, errors.New("unknown command: " + peek.Cmd)
	}
}
