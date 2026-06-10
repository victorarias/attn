import "@typespec/json-schema";

using TypeSpec.JsonSchema;

@jsonSchema
namespace Protocol;

// ============================================================================
// Enums
// ============================================================================

enum SessionState {
  launching,
  working,
  waiting_input,
  idle,
  pending_approval,
  state_unknown: "unknown",
}

enum PRRole {
  author,
  reviewer,
}

// WorkspaceStatus is the rolled-up status of all sessions in a workspace.
// Priority (highest first): working > waiting_input > pending_approval > idle > launching.
// `launching` sits below `idle` because it carries less information — any
// session in a settled state out-prioritizes a peer that's still booting.
// No `unknown` value: a workspace always has a directory and a registry
// entry, so the rollup always has a meaningful answer (empty/all-unknown
// member sessions roll up to `idle`).
enum WorkspaceStatus {
  launching,
  working,
  waiting_input,
  pending_approval,
  idle,
}

enum WorkspaceLayoutPaneKind {
  agent,
}

enum WorkspaceLayoutPaneStatus {
  spawning,
  ready,
  failed,
}

enum WorkspaceLayoutSplitDirection {
  vertical,
  horizontal,
}

// Which side of the anchor pane a docked tile lands on.
enum WorkspaceLayoutDockEdge {
  left,
  right,
  top,
  bottom,
}

enum HeatState {
  cold,
  warm,
  hot,
}

enum AttachPolicy {
  fresh_spawn,
  relaunch_restore,
  same_app_remount,
}

enum WorkspaceContextMaintenanceAction {
  compact,
  rollback,
}

enum ReviewLoopStatus {
  running,
  waiting_for_agent_advance,
  advance_received_waiting_prompt,
  stopped,
  completed,
  error,
}

enum ReviewLoopRunStatus {
  running,
  awaiting_user,
  stopped,
  completed,
  error,
}

enum ReviewLoopIterationStatus {
  running,
  awaiting_user,
  cancelled,
  completed,
  error,
}

enum ReviewLoopDecision {
  continue_loop: "continue",
  converged,
  needs_user_input,
  error,
}

enum ReviewLoopInteractionStatus {
  pending,
  answered,
  consumed,
}

enum TourStatus {
  active,
  ended,
}

enum TourConnectionState {
  connected,
  disconnected,
}

enum GitOperationKind {
  delete_worktree,
}

enum GitOperationStatus {
  running,
  succeeded,
  failed,
}

// ============================================================================
// Core Entities
// ============================================================================

model Session {
  id: string;
  label: string;
  agent: string;                // Built-in ID or registered plugin driver ID
  directory: string;
  endpoint_id?: string;         // Optional: owning endpoint for remote/session-aware routing
  workspace_id: string;         // Owning workspace
  branch?: string;              // Optional: might not be in git repo
  is_worktree?: boolean;        // Optional: false if not worktree
  main_repo?: string;           // Optional: only set if worktree
  state: SessionState;
  state_since: string;          // ISO timestamp, always set
  state_updated_at: string;     // ISO timestamp, always set
  needs_review_after_long_run?: boolean; // True when a 5m+ run finished and awaits user visualization
  recoverable?: boolean;        // True when session can be recovered after daemon restart (agent has resumable conversation)
  chief_of_staff?: boolean;     // True when this session holds the profile-wide coordinator role
  todos?: string[];             // Optional: can be empty/absent
  last_seen: string;            // ISO timestamp, always set
}

// Workspace is a directory plus one or more agent sessions and its split layout.
model Workspace {
  id: string;
  title: string;
  directory: string;
  status: WorkspaceStatus;
  muted: boolean;               // Muting parks the whole workspace out of attention surfaces.
  layout?: WorkspaceLayout;
}

model WorkspaceContext {
  workspace_id: string;
  content: string;
  revision: int32;
  updated_by_session_id: string;
  updated_at: string;
}

enum DispatchReportType {
  progress,
  blocker,
  handoff,
  completion,
  failure,
}

enum DispatchWorkState {
  in_progress,
  needs_input,
  ready_for_review,
  completed,
  failed,
}

enum DispatchRequestStatus {
  pending,
  resolved,
}

model DispatchDecisionRequest {
  question: string;
  recommendation?: string;
  consequence?: string;
  expected_responder: string;
  status: DispatchRequestStatus;
  response?: string;
  resolution_link?: string;
  responded_by?: string;
  responded_at?: string;
}

model DispatchArtifact {
  identity: string;
  workspace_id?: string;
  branch?: string;
  revision?: string;
  dirty?: boolean;
  description?: string;
}

model DispatchVerification {
  actor: string;
  target: string;
  result: string;
  timestamp: string;
  artifact_identity: string;
  current?: boolean;
}

model DispatchReport {
  report_type: DispatchReportType;
  summary: string;
  work_state: DispatchWorkState;
  next_actor?: string;
  next_action?: string;
  remaining_scope?: string[];
  constraints?: string[];
  request?: DispatchDecisionRequest;
  artifact?: DispatchArtifact;
  verification?: DispatchVerification[];
  reported_at: string;
}

model DispatchMessage {
  id: string;
  dispatch_id: string;
  sender_session_id: string;
  target_session_id: string;
  content: string;
  created_at: string;
  read_at?: string;
  acknowledged_at?: string;
  acknowledgement?: string;
}

model ChiefOfStaffDispatch {
  id: string;
  chief_session_id: string;
  session_id: string;
  workspace_id: string;
  brief: string;
  label: string;
  agent: string;
  directory: string;
  branch?: string;
  status: string;
  status_since: string;
  latest_report?: string;
  structured_report?: DispatchReport;
  concise_summary?: string;
  actionable?: boolean;
  unread_message_count?: int32;
  reported_at?: string;
  created_at: string;
  updated_at: string;
}

model PR {
  // Core identity - always present
  // ID format: "host:owner/repo#number" (e.g. github.com:acme/widget#42)
  id: string;
  host: string;
  repo: string;
  number: int32;
  title: string;
  url: string;
  author: string;               // GitHub username of PR author
  role: PRRole;
  state: string;
  reason: string;               // Why it needs attention
  last_updated: string;         // ISO timestamp
  last_polled: string;          // ISO timestamp
  muted: boolean;

  // Detailed status (fetched on-demand via gh api)
  details_fetched: boolean;     // Always has value
  details_fetched_at?: string;  // ISO timestamp, only set if fetched
  mergeable?: boolean;          // nil=unknown, true/false
  mergeable_state?: string;     // clean, blocked, dirty, unstable
  ci_status?: string;           // success, failure, pending, none
  review_status?: string;       // approved, changes_requested, pending, none

  // Interaction tracking
  head_sha?: string;
  head_branch?: string;
  comment_count?: int32;
  approved_by_me: boolean;      // Always has value
  has_new_changes: boolean;     // Always has value

  // Heat state for detail refresh scheduling
  heat_state?: HeatState;
  last_heat_activity_at?: string; // ISO timestamp
}

model Worktree {
  path: string;
  branch: string;
  main_repo: string;
  created_at?: string; // ISO timestamp
}

model PluginInfo {
  name: string;
  version: string;
  description?: string;
  dir: string;
  priority: int32;
  connected: boolean;
  running: boolean;
  health_status?: string;
  health_message?: string;
  last_health_at?: string;
}

model PluginIssue {
  path: string;
  error: string;
}

model GitOperation {
  id: string;
  kind: GitOperationKind;
  status: GitOperationStatus;
  path?: string;
  endpoint_id?: string;
  started_at: string;   // ISO timestamp
  finished_at?: string; // ISO timestamp
  duration_ms?: int32;
  error?: string;
}

model RepoState {
  repo: string;
  muted: boolean;
  collapsed: boolean;
}

model AuthorState {
  author: string;                // GitHub username
  muted: boolean;
}

model RecentLocation {
  path: string;
  label: string;
  last_seen: string;    // ISO timestamp
  use_count: int32;
}

model DirectoryEntry {
  name: string;
  path: string;
}

model PathInspection {
  input_path: string;
  resolved_path: string;
  home_path?: string;
  exists: boolean;
  is_directory: boolean;
  repo_root?: string;
}

model Branch {
  name: string;
  commit_hash?: string;    // Short SHA (7 chars)
  commit_time?: string;    // ISO timestamp
  is_current?: boolean;    // True if this is the checked-out branch
}

model GitFileChange {
  path: string;
  status: string;        // "modified", "added", "deleted", "renamed", "untracked"
  additions?: int32;     // Line additions (not available for untracked)
  deletions?: int32;     // Line deletions (not available for untracked)
  old_path?: string;     // For renames
}

model WorkspaceLayoutPane {
  pane_id: string;
  runtime_id?: string;
  workspace_id: string;
  session_id?: string;
  kind: WorkspaceLayoutPaneKind;
  title: string;
  status: WorkspaceLayoutPaneStatus;
  error?: string;
}

model WorkspaceLayout {
  workspace_id: string;
  active_pane_id: string;
  layout_json: string;
  panes: WorkspaceLayoutPane[];
  updated_at?: string;
}

model ReplaySegment {
  cols: int32;
  rows: int32;
  data: string; // base64 bytes
}

model EndpointCapabilities {
  protocol_version: string;
  agents_available: string[];
  projects_directory?: string;
  pty_backend_mode?: string;
  daemon_instance_id?: string;
  tailscale_enabled?: boolean;
  tailscale_status?: string;
  tailscale_url?: string;
  tailscale_domain?: string;
  tailscale_auth_url?: string;
  tailscale_error?: string;
}

model EndpointInfo {
  id: string;
  name: string;
  ssh_target: string;
  status: string;
  enabled?: boolean;
  profile?: string;
  status_message?: string;
  session_count?: int32;
  capabilities?: EndpointCapabilities;
}

// ============================================================================
// Command Messages (Client → Daemon)
// ============================================================================

// Sent first thing after a WebSocket connect. Interactive clients must
// advertise workspace_sessions so the daemon only accepts workspace-first
// clients.
//
model ClientHelloMessage {
  cmd: "client_hello";
  client_kind: string;          // e.g. "tauri-app", "remote-web"
  version: string;              // free-form, used for diagnostics
  capabilities: string[];       // see capability docstrings above
  browser_host_token?: string;  // secret available only to the trusted Tauri main webview
}

model RegisterMessage {
  cmd: "register";
  id: string;
  label?: string;
  agent?: string;               // Optional for registered plugin IDs
  dir: string;
  workspace_id: string;
}

model UnregisterMessage {
  cmd: "unregister";
  id: string;
}

model StateMessage {
  cmd: "state";
  id: string;
  state: string;
}

model SetSessionResumeIDMessage {
  cmd: "set_session_resume_id";
  id: string;
  resume_session_id: string;
}

model StopMessage {
  cmd: "stop";
  id: string;
  transcript_path: string;
}

model TodosMessage {
  cmd: "todos";
  id: string;
  todos: string[];
}

model DelegateMessage {
  cmd: "delegate";
  source_session_id: string;
  brief: string;
  agent?: string;
  label?: string;
  yolo_mode?: boolean;
  placement?: string;
  workspace_id?: string;
  cwd?: string;
  worktree?: DelegateWorktreeRequest;
}

model ListDispatchesMessage {
  cmd: "list_dispatches";
  source_session_id: string;
}

model ReportDispatchMessage {
  cmd: "report_dispatch";
  source_session_id: string;
  report: string;
  structured_report?: DispatchReport;
}

model GetDispatchMessage {
  cmd: "get_dispatch";
  source_session_id: string;
}

model ResolveDispatchRequestMessage {
  cmd: "resolve_dispatch_request";
  source_session_id: string;
  dispatch_id: string;
  response: string;
  resolution_link?: string;
}

model SendDispatchMessage {
  cmd: "send_dispatch_message";
  source_session_id: string;
  dispatch_id: string;
  content: string;
}

model ListDispatchMessagesMessage {
  cmd: "list_dispatch_messages";
  source_session_id: string;
  dispatch_id?: string;
  unread_only?: boolean;
}

model ReadDispatchMessage {
  cmd: "read_dispatch_message";
  source_session_id: string;
  message_id: string;
}

model AcknowledgeDispatchMessage {
  cmd: "acknowledge_dispatch_message";
  source_session_id: string;
  message_id: string;
  acknowledgement?: string;
}

model WakeDispatchAgentMessage {
  cmd: "wake_dispatch_agent";
  source_session_id: string;
  dispatch_id: string;
  request_id: string;
}

model WorkspaceContextCheckoutMessage {
  cmd: "workspace_context_checkout";
  source_session_id: string;
  force?: boolean;
}

model WorkspaceContextUpdateMessage {
  cmd: "workspace_context_update";
  source_session_id: string;
}

model WorkspaceContextStatusMessage {
  cmd: "workspace_context_status";
  source_session_id: string;
}

model WorkspaceContextListMessage {
  cmd: "workspace_context_list";
  request_id: string;
}

model WorkspaceContextCompactMessage {
  cmd: "workspace_context_compact";
  source_session_id: string;
}

model WorkspaceContextRollbackMessage {
  cmd: "workspace_context_rollback";
  source_session_id: string;
}

model DelegateWorktreeRequest {
  repo?: string;
  branch: string;
  path?: string;
  starting_from?: string;
}

model QueryMessage {
  cmd: "query";
  filter?: string;
}

model HeartbeatMessage {
  cmd: "heartbeat";
  id: string;
}

model SessionVisualizedMessage {
  cmd: "session_visualized";
  id: string;
}

model SessionSelectedMessage {
  cmd: "session_selected";
  id: string;
}

model WorkspaceSelectedMessage {
  cmd: "workspace_selected";
  workspace_id: string;
}

model MuteWorkspaceMessage {
  cmd: "mute_workspace";
  workspace_id: string;
  endpoint_id?: string;
}

model QueryPRsMessage {
  cmd: "query_prs";
  filter?: string;
}

model MutePRMessage {
  cmd: "mute_pr";
  id: string;
}

model MuteRepoMessage {
  cmd: "mute_repo";
  repo: string;
}

model MuteAuthorMessage {
  cmd: "mute_author";
  author: string;
}

model CollapseRepoMessage {
  cmd: "collapse_repo";
  repo: string;
  collapsed: boolean;
}

model QueryReposMessage {
  cmd: "query_repos";
  filter?: string;
}

model QueryAuthorsMessage {
  cmd: "query_authors";
}

model FetchPRDetailsMessage {
  cmd: "fetch_pr_details";
  id: string;
}

model RefreshPRsMessage {
  cmd: "refresh_prs";
}

model ClearSessionsMessage {
  cmd: "clear_sessions";
}

model ClearWarningsMessage {
  cmd: "clear_warnings";
}

model PRVisitedMessage {
  cmd: "pr_visited";
  id: string;
}

model ListWorktreesMessage {
  cmd: "list_worktrees";
  main_repo: string;
}

model CreateWorktreeMessage {
  cmd: "create_worktree";
  main_repo: string;
  branch: string;
  path?: string;
  endpoint_id?: string;
  starting_from?: string;  // Branch to create worktree from (default: current HEAD)
}

model DeleteWorktreeMessage {
  cmd: "delete_worktree";
  path: string;
  endpoint_id?: string;
  force?: boolean;
}

model GetSettingsMessage {
  cmd: "get_settings";
}

model SetSettingMessage {
  cmd: "set_setting";
  key: string;
  value: string;
}

model ListPluginsMessage {
  cmd: "list_plugins";
}

model InstallPluginMessage {
  cmd: "install_plugin";
  source: string;
}

model RemovePluginMessage {
  cmd: "remove_plugin";
  name: string;
}

model SetPluginPriorityMessage {
  cmd: "set_plugin_priority";
  name: string;
  priority: int32;
}

model AddEndpointMessage {
  cmd: "add_endpoint";
  name: string;
  ssh_target: string;
  profile?: string;
}

model RemoveEndpointMessage {
  cmd: "remove_endpoint";
  endpoint_id: string;
}

model UpdateEndpointMessage {
  cmd: "update_endpoint";
  endpoint_id: string;
  name?: string;
  ssh_target?: string;
  enabled?: boolean;
  profile?: string;
}

model ListEndpointsMessage {
  cmd: "list_endpoints";
}

model SetEndpointRemoteWebMessage {
  cmd: "set_endpoint_remote_web";
  endpoint_id: string;
  enabled: boolean;
}

model BootstrapEndpointMessage {
  cmd: "bootstrap_endpoint";
  endpoint_id: string;
}

model ApprovePRMessage {
  cmd: "approve_pr";
  id: string;
}

model MergePRMessage {
  cmd: "merge_pr";
  id: string;
  method: string; // "squash", "merge", "rebase"
}

model InjectTestPRMessage {
  cmd: "inject_test_pr";
  pr: PR;
}

model InjectTestSessionMessage {
  cmd: "inject_test_session";
  session: Session;
}

model GetRecentLocationsMessage {
  cmd: "get_recent_locations";
  limit?: int32;  // Default 20
  endpoint_id?: string;
  request_id?: string;
}

model BrowseDirectoryMessage {
  cmd: "browse_directory";
  input_path: string;
  endpoint_id?: string;
  request_id?: string;
}

model InspectPathMessage {
  cmd: "inspect_path";
  path: string;
  endpoint_id?: string;
  request_id?: string;
}

model ListBranchesMessage {
  cmd: "list_branches";
  main_repo: string;
}

model CreateWorktreeFromBranchMessage {
  cmd: "create_worktree_from_branch";
  main_repo: string;
  branch: string;
  path?: string;
}

model GetDefaultBranchMessage {
  cmd: "get_default_branch";
  repo: string;
}

model FetchRemotesMessage {
  cmd: "fetch_remotes";
  repo: string;
}

model ListRemoteBranchesMessage {
  cmd: "list_remote_branches";
  repo: string;
}

model EnsureRepoMessage {
  cmd: "ensure_repo";
  target_path: string;   // Where to clone (e.g., /Users/victor/projects/dashboards-go)
  clone_url: string;     // Git clone URL (e.g., https://github.com/owner/repo.git)
}

model SubscribeGitStatusMessage {
  cmd: "subscribe_git_status";
  directory: string;
}

model UnsubscribeGitStatusMessage {
  cmd: "unsubscribe_git_status";
}

model GetFileDiffMessage {
  cmd: "get_file_diff";
  directory: string;
  path: string;
  staged?: boolean;  // DEPRECATED: If true, show staged diff instead of working tree
  base_ref?: string; // If provided, compare against this ref instead of HEAD (for PR-like diffs)
}

// Branch diff file info for PR-like review
model BranchDiffFile {
  path: string;
  status: string;           // "added", "modified", "deleted", "renamed"
  old_path?: string;        // For renames
  additions?: int32;
  deletions?: int32;
  has_uncommitted?: boolean; // True if file also has uncommitted changes
}

model GetBranchDiffFilesMessage {
  cmd: "get_branch_diff_files";
  directory: string;
  base_ref?: string;  // Defaults to origin/<default-branch>
}

model GetRepoInfoMessage {
  cmd: "get_repo_info";
  repo: string;
  endpoint_id?: string;
}

model GetReviewStateMessage {
  cmd: "get_review_state";
  repo_path: string;
  branch: string;
}

model OpenTourMessage {
  cmd: "open_tour";
  session_id: string;
  guide_path: string;
  base_ref?: string;
  name?: string;
}

model GetTourStateMessage {
  cmd: "get_tour_state";
  session_id: string;
}

model RefreshTourMessage {
  cmd: "refresh_tour";
  tour_id: string;
}

model SaveTourDraftMessage {
  cmd: "save_tour_draft";
  tour_id: string;
  draft: TourFileDraft;
  current_file?: string;
}

model AskTourMessage {
  cmd: "ask_tour";
  tour_id: string;
  body: string;
  context: TourQuestionContext;
}

model ReplyTourMessage {
  cmd: "reply_tour";
  tour_id: string;
  event_id: string;
  body: string;
}

model SubmitTourMessage {
  cmd: "submit_tour";
  tour_id: string;
  body: string;
  finish: boolean;
}

model WaitTourEventMessage {
  cmd: "wait_tour_event";
  tour_id: string;
  after_seq: int32;
}

model StartReviewLoopMessage {
  cmd: "start_review_loop";
  session_id: string;
  preset_id?: string;
  prompt: string;
  iteration_limit: int32;
  handoff_payload_json?: string;
}

model StopReviewLoopMessage {
  cmd: "stop_review_loop";
  session_id: string;
}

model GetReviewLoopStateMessage {
  cmd: "get_review_loop_state";
  session_id: string;
}

model GetReviewLoopRunMessage {
  cmd: "get_review_loop_run";
  loop_id: string;
}

model SetReviewLoopIterationLimitMessage {
  cmd: "set_review_loop_iteration_limit";
  session_id: string;
  iteration_limit: int32;
}

model AnswerReviewLoopMessage {
  cmd: "answer_review_loop";
  loop_id: string;
  interaction_id?: string;
  answer: string;
}

model MarkFileViewedMessage {
  cmd: "mark_file_viewed";
  review_id: string;
  filepath: string;
  viewed: boolean;  // true to mark viewed, false to unmark
}

model AddCommentMessage {
  cmd: "add_comment";
  review_id: string;
  filepath: string;
  line_start: int32;
  line_end: int32;
  content: string;
}

model UpdateCommentMessage {
  cmd: "update_comment";
  comment_id: string;
  content: string;
}

model ResolveCommentMessage {
  cmd: "resolve_comment";
  comment_id: string;
  resolved: boolean;
}

model DeleteCommentMessage {
  cmd: "delete_comment";
  comment_id: string;
}

model GetCommentsMessage {
  cmd: "get_comments";
  review_id: string;
  filepath?: string;  // Optional: filter by file
}

model SpawnSessionMessage {
  cmd: "spawn_session";
  id: string;
  cwd: string;
  endpoint_id?: string;
  workspace_id: string;     // Required owner for agent sessions
  agent: string;            // Built-in ID, "shell", or registered plugin driver ID
  cols: int32;
  rows: int32;
  label?: string;
  resume_session_id?: string;
  resume_picker?: boolean;
  yolo_mode?: boolean;
  initial_prompt?: string;
  executable?: string;         // Generic executable override for selected agent
  claude_executable?: string;  // Backward compatibility
  codex_executable?: string;   // Backward compatibility
  copilot_executable?: string; // Backward compatibility
}

model AttachSessionMessage {
  cmd: "attach_session";
  id: string;
  attach_policy?: AttachPolicy;
}

model DetachSessionMessage {
  cmd: "detach_session";
  id: string;
}

// Read-only request for a session's current rendered screen. Unlike
// attach_session it registers no subscriber and claims no PTY geometry; used to
// seed observers (e.g. grid tiles) that then dedup live output against last_seq.
model GetScreenSnapshotMessage {
  cmd: "get_screen_snapshot";
  id: string;
}

model PtyInputMessage {
  cmd: "pty_input";
  id: string;
  data: string;
  source?: string;
}

model PtyResizeMessage {
  cmd: "pty_resize";
  id: string;
  cols: int32;
  rows: int32;
}

model KillSessionMessage {
  cmd: "kill_session";
  id: string;
  signal?: string; // "SIGTERM", "SIGINT", ...
}

model WorkspaceLayoutGetMessage {
  cmd: "workspace_layout_get";
  workspace_id: string;
}

model WorkspaceLayoutAddSessionPaneMessage {
  cmd: "workspace_layout_add_session_pane";
  workspace_id: string;
  pane_id?: string;
  session_id: string;
  title?: string;
  target_pane_id?: string;
  direction?: WorkspaceLayoutSplitDirection;
}

model WorkspaceLayoutClosePaneMessage {
  cmd: "workspace_layout_close_pane";
  workspace_id: string;
  pane_id: string;
}

model WorkspaceLayoutFocusPaneMessage {
  cmd: "workspace_layout_focus_pane";
  workspace_id: string;
  pane_id: string;
}

model WorkspaceLayoutRenamePaneMessage {
  cmd: "workspace_layout_rename_pane";
  workspace_id: string;
  pane_id: string;
  title: string;
}

model WorkspaceLayoutSetSplitRatioMessage {
  cmd: "workspace_layout_set_split_ratio";
  workspace_id: string;
  split_id: string;
  ratio: float64;
  request_id?: string;
}

// Dock (or move) a tile beside an anchor leaf. Docking is idempotent: an
// existing instance of tile_id is relocated rather than duplicated. tile_kind
// is opaque to the daemon — it persists where the tile sits, not what it
// renders. ratio is the new tile's fraction of the split (optional; defaults to
// a sensible third).
model WorkspaceLayoutDockTileMessage {
  cmd: "workspace_layout_dock_tile";
  workspace_id: string;
  anchor_pane_id: string;
  edge: WorkspaceLayoutDockEdge;
  tile_id: string;
  tile_kind: string;
  ratio?: float64;
}

model WorkspaceLayoutUndockTileMessage {
  cmd: "workspace_layout_undock_tile";
  workspace_id: string;
  tile_id: string;
}

model WorkspaceLayoutUpdateTileMessage {
  cmd: "workspace_layout_update_tile";
  workspace_id: string;
  tile_id: string;
  tile_params: string;
  request_id: string;
}

// Move an existing leaf (terminal pane or docked tile) so it sits beside an
// anchor leaf on the given edge. An empty anchor_id docks the leaf against the
// whole workspace (the root), spanning that edge. ratio is the moved leaf's
// fraction of the new split (optional; defaults to an equal split). Dropping a
// leaf on itself (leaf_id == anchor_id) is a no-op. The new split is ratio-locked
// so the dropped size survives normalization.
model WorkspaceLayoutMoveLeafMessage {
  cmd: "workspace_layout_move_leaf";
  workspace_id: string;
  leaf_id: string;
  anchor_id: string;
  edge: WorkspaceLayoutDockEdge;
  ratio?: float64;
}

// Move an existing leaf from one workspace layout into another workspace layout.
// source_workspace_id and target_workspace_id must be owned by the same daemon
// endpoint; live terminal runtimes are not migrated between endpoints. If the
// target already contains the moved leaf id, the daemon assigns a fresh suffix
// and reports final_leaf_id in the action result.
model WorkspaceLayoutMoveLeafToWorkspaceMessage {
  cmd: "workspace_layout_move_leaf_to_workspace";
  source_workspace_id: string;
  target_workspace_id: string;
  leaf_id: string;
  anchor_id?: string;
  edge: WorkspaceLayoutDockEdge;
  ratio?: float64;
}

// Request the current rendered content of a docked tile (e.g. the markdown
// file a markdown tile points at). The daemon resolves the file from the
// tile's persisted params, reads it, and replies with WorkspaceTileContentMessage.
model WorkspaceTileContentGetMessage {
  cmd: "workspace_tile_content_get";
  workspace_id: string;
  tile_id: string;
}

// Open a markdown file into a docked, live-reloading tile. Sent by the
// `attn open` CLI over the unix socket. When session_id is empty the daemon
// targets the currently selected session's workspace.
model OpenMarkdownMessage {
  cmd: "open_markdown";
  path: string;
  session_id?: string;
}

// Open or retarget the docked browser tile. Sent by `attn browser open`.
// When session_id is empty the daemon targets the currently selected session.
model OpenBrowserMessage {
  cmd: "open_browser";
  url: string;
  session_id?: string;
}

// Ask the visible in-app browser host to perform an action. Sent by the CLI
// over the unix socket; the daemon brokers it to a capable Tauri client.
model BrowserControlMessage {
  cmd: "browser_control";
  action: string;
  params?: string;
  selector?: string;
  text?: string;
  session_id?: string;
  workspace_id?: string;
  request_id?: string;
}

// Return a browser-host action result from the Tauri client to the daemon.
model BrowserControlResultMessage {
  cmd: "browser_control_result";
  request_id: string;
  success: boolean;
  data?: string;
  error?: string;
}

// Return a remotely executed browser-control result to the hub daemon that
// originated the request.
model BrowserControlResponseMessage {
  event: "browser_control_response";
  request_id: string;
  success: boolean;
  data?: string;
  error?: string;
}

model RegisterWorkspaceMessage {
  cmd: "register_workspace";
  id: string;
  title: string;
  directory: string;
  endpoint_id?: string;
}

model UnregisterWorkspaceMessage {
  cmd: "unregister_workspace";
  id: string;
}

// Rename a session's display label. The stored label is authoritative once set:
// it survives session re-registration and respawn so a rename is durable.
model RenameSessionMessage {
  cmd: "rename_session";
  session_id: string;
  label: string;
}

// Rename a workspace's title. Like the session label, the stored title wins over
// the name re-derived at register time, so a rename sticks across reconnects.
model RenameWorkspaceMessage {
  cmd: "rename_workspace";
  workspace_id: string;
  title: string;
}

// Assign or remove the profile-wide chief-of-staff role. Assigning a new
// session transfers the singleton role from the previous holder.
model SetChiefOfStaffMessage {
  cmd: "set_chief_of_staff";
  session_id: string;
  chief_of_staff: boolean;
}

// ============================================================================
// Result/Event Messages (Daemon → Client)
// ============================================================================

model InitialStateMessage {
  event: "initial_state";
  protocol_version?: string;
  source_fingerprint?: string;
  daemon_instance_id?: string;
  sessions?: Session[];
  endpoints?: EndpointInfo[];
  workspaces?: Workspace[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  github_hosts?: string[];
  settings?: Record<string>;
  warnings?: DaemonWarning[];
  chief_of_staff_dispatches?: ChiefOfStaffDispatch[];
}

model EndpointStatusChangedMessage {
  event: "endpoint_status_changed";
  endpoint: EndpointInfo;
}

model EndpointsUpdatedMessage {
  event: "endpoints_updated";
  endpoints: EndpointInfo[];
}

model EndpointActionResultMessage {
  event: "endpoint_action_result";
  action: string;
  endpoint_id?: string;
  success: boolean;
  error?: string;
}

model SessionRegisteredMessage {
  event: "session_registered";
  session: Session;
}

model SessionUnregisteredMessage {
  event: "session_unregistered";
  session: Session;
}

model SessionStateChangedMessage {
  event: "session_state_changed";
  session: Session;
}

model WorkspaceRegisteredMessage {
  event: "workspace_registered";
  workspace: Workspace;
}

model WorkspaceUnregisteredMessage {
  event: "workspace_unregistered";
  workspace: Workspace;
}

model WorkspaceStateChangedMessage {
  event: "workspace_state_changed";
  workspace: Workspace;
}

model WorkspaceContextChangedMessage {
  event: "workspace_context_changed";
  workspace_id: string;
  revision: int32;
  updated_by_session_id: string;
  updated_at: string;
}

model SessionTodosUpdatedMessage {
  event: "session_todos_updated";
  session: Session;
}

model SessionsUpdatedMessage {
  event: "sessions_updated";
  sessions?: Session[];
}

model WorkspaceLayoutMessage {
  event: "workspace_layout";
  workspace_layout: WorkspaceLayout;
}

model WorkspaceLayoutUpdatedMessage {
  event: "workspace_layout_updated";
  workspace_layout: WorkspaceLayout;
}

// Result of a rename_session / rename_workspace command. cmd echoes the command
// name and id is the renamed session_id or workspace_id, so the client can match
// it to the pending request. The renamed entity is also broadcast separately via
// session_state_changed / workspace_state_changed.
model RenameResultMessage {
  event: "rename_result";
  cmd: string;
  id: string;
  success: boolean;
  error?: string;
}

model ChiefOfStaffResultMessage {
  event: "chief_of_staff_result";
  session_id: string;
  chief_of_staff: boolean;
  previous_session_id?: string;
  success: boolean;
  error?: string;
}

model ChiefOfStaffDispatchesUpdatedMessage {
  event: "chief_of_staff_dispatches_updated";
  dispatches: ChiefOfStaffDispatch[];
}

model WakeDispatchAgentResultMessage {
  event: "wake_dispatch_agent_result";
  dispatch_id: string;
  request_id: string;
  success: boolean;
  error?: string;
}

model WorkspaceLayoutActionResultMessage {
  event: "workspace_layout_action_result";
  action: string;
  workspace_id: string;
  source_workspace_id?: string;
  target_workspace_id?: string;
  leaf_id?: string;
  final_leaf_id?: string;
  pane_id?: string;
  split_id?: string;
  tile_id?: string;
  request_id?: string;
  success: boolean;
  error?: string;
}

// Rendered content of a docked tile. Sent as the reply to
// workspace_tile_content_get and broadcast whenever the underlying file
// changes (live reload). For markdown, content is the raw file text and path is
// its absolute path; error is set when the file can't be read.
model WorkspaceTileContentMessage {
  event: "workspace_tile_content";
  workspace_id: string;
  tile_id: string;
  tile_kind: string;
  path: string;
  content: string;
  error?: string;
}

model PRsUpdatedMessage {
  event: "prs_updated";
  prs?: PR[];
}

model ReposUpdatedMessage {
  event: "repos_updated";
  repos?: RepoState[];
}

model AuthorsUpdatedMessage {
  event: "authors_updated";
  authors?: AuthorState[];
}

model BranchChangedMessage {
  event: "branch_changed";
  session?: Session;
}

model PRActionResultMessage {
  event: "pr_action_result";
  action: string; // "approve" or "merge"
  id: string;
  success: boolean;
  error?: string;
}

model RefreshPRsResultMessage {
  event: "refresh_prs_result";
  success: boolean;
  error?: string;
}

model FetchPRDetailsResultMessage {
  event: "fetch_pr_details_result";
  prs?: PR[];
  success: boolean;
  error?: string;
}

model RateLimitedMessage {
  event: "rate_limited";
  rate_limit_resource: string; // "core" or "search"
  rate_limit_reset_at: string; // ISO timestamp
}

model WorktreeCreatedEvent {
  event: "worktree_created";
  worktrees: Worktree[];
}

model WorktreeDeletedEvent {
  event: "worktree_deleted";
  worktrees: Worktree[];
}

model WorktreesUpdatedMessage {
  event: "worktrees_updated";
  worktrees?: Worktree[];
}

model CreateWorktreeResultMessage {
  event: "create_worktree_result";
  path?: string;
  endpoint_id?: string;
  success: boolean;
  error?: string;
}

model DeleteWorktreeResultMessage {
  event: "delete_worktree_result";
  path: string;
  endpoint_id?: string;
  success: boolean;
  error?: string;
  forceable?: boolean;
  reason_kind?: "dirty_worktree" | "provider_error" | "not_found" | "git_error";
}

model GitOperationStartedMessage {
  event: "git_operation_started";
  operation: GitOperation;
}

model GitOperationFinishedMessage {
  event: "git_operation_finished";
  operation: GitOperation;
}

model SettingsUpdatedMessage {
  event: "settings_updated";
  settings?: Record<string>;
  changed_key?: string;
  success?: boolean;
  error?: string;
}

model GitHubHostsUpdatedMessage {
  event: "github_hosts_updated";
  github_hosts: string[];
}

model PluginsUpdatedMessage {
  event: "plugins_updated";
  plugins: PluginInfo[];
  issues?: PluginIssue[];
}

model PluginActionResultMessage {
  event: "plugin_action_result";
  action: string;
  name?: string;
  success: boolean;
  error?: string;
}

model RecentLocationsResultMessage {
  event: "recent_locations_result";
  recent_locations: RecentLocation[];
  endpoint_id?: string;
  request_id?: string;
  home_path?: string;
  success: boolean;
  error?: string;
}

model BrowseDirectoryResultMessage {
  event: "browse_directory_result";
  input_path: string;
  directory: string;
  entries: DirectoryEntry[];
  endpoint_id?: string;
  request_id?: string;
  home_path?: string;
  success: boolean;
  error?: string;
}

model InspectPathResultMessage {
  event: "inspect_path_result";
  inspection?: PathInspection;
  endpoint_id?: string;
  request_id?: string;
  success: boolean;
  error?: string;
}

model BranchesResultMessage {
  event: "branches_result";
  branches: Branch[];
  success: boolean;
  error?: string;
}

model GetDefaultBranchResultMessage {
  event: "get_default_branch_result";
  branch: string;
  success: boolean;
  error?: string;
}

model FetchRemotesResultMessage {
  event: "fetch_remotes_result";
  success: boolean;
  error?: string;
}

model ListRemoteBranchesResultMessage {
  event: "list_remote_branches_result";
  branches: Branch[];
  success: boolean;
  error?: string;
}

model EnsureRepoResultMessage {
  event: "ensure_repo_result";
  target_path?: string;
  cloned?: boolean;
  success?: boolean;
  error?: string;
}

model GitStatusUpdateMessage {
  event: "git_status_update";
  directory: string;
  staged: GitFileChange[];
  unstaged: GitFileChange[];
  untracked: GitFileChange[];
  error?: string;
  mode?: string;                 // "full" or "tracked_only"
  limited?: boolean;             // true when untracked files were skipped
  limited_reason?: string;       // user-facing reason for the limited result
  duration_ms?: int32;           // daemon-side status command duration
}

model FileDiffResultMessage {
  event: "file_diff_result";
  directory: string;
  path: string;
  original: string;
  modified: string;
  success: boolean;
  error?: string;
}

model BranchDiffFilesResultMessage {
  event: "branch_diff_files_result";
  directory: string;
  base_ref: string;           // Actual base ref used
  files: BranchDiffFile[];
  success: boolean;
  error?: string;
}

model RepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: Worktree[];
  branches: Branch[];          // Available branches with commit info
  fetched_at?: string;         // ISO timestamp, when branches were last fetched
}

model GetRepoInfoResultMessage {
  event: "get_repo_info_result";
  info?: RepoInfo;
  endpoint_id?: string;
  success: boolean;
  error?: string;
}

model ReviewState {
  review_id: string;
  repo_path: string;
  branch: string;
  viewed_files: string[];  // List of filepath strings
}

model ReviewLoopState {
  session_id: string;
  status: ReviewLoopStatus;
  preset_id?: string;
  custom_prompt?: string;
  resolved_prompt: string;
  iteration_count: int32;
  iteration_limit: int32;
  stop_requested: boolean;
  advance_token: string;
  stop_reason?: string;
  last_prompt_at?: string;
  last_advance_at?: string;
  last_user_input_at?: string;
  created_at: string;
  updated_at: string;
}

model ReviewLoopRun {
  loop_id: string;
  source_session_id: string;
  repo_path: string;
  status: ReviewLoopRunStatus;
  preset_id?: string;
  custom_prompt?: string;
  resolved_prompt: string;
  handoff_payload_json?: string;
  iteration_count: int32;
  iteration_limit: int32;
  pending_interaction_id?: string;
  last_decision?: ReviewLoopDecision;
  last_result_summary?: string;
  last_error?: string;
  stop_reason?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  pending_interaction?: ReviewLoopInteraction;
  latest_iteration?: ReviewLoopIteration;
  iterations?: ReviewLoopIteration[];
}

model ReviewLoopIteration {
  id: string;
  loop_id: string;
  iteration_number: int32;
  status: ReviewLoopIterationStatus;
  decision?: ReviewLoopDecision;
  summary?: string;
  result_text?: string;
  changes_made?: boolean;
  files_touched?: string[];
  change_stats?: BranchDiffFile[];
  blocking_reason?: string;
  suggested_next_focus?: string;
  structured_output_json?: string;
  assistant_trace_json?: string;
  error?: string;
  started_at: string;
  completed_at?: string;
}

model ReviewLoopInteraction {
  id: string;
  loop_id: string;
  iteration_id?: string;
  kind: string;
  question: string;
  answer?: string;
  status: ReviewLoopInteractionStatus;
  created_at: string;
  answered_at?: string;
  consumed_at?: string;
}

model ReviewComment {
  id: string;
  review_id: string;
  filepath: string;
  line_start: int32;
  line_end: int32;
  content: string;
  author: string;  // "user" or "agent"
  resolved: boolean;
  resolved_by?: string;  // "user" or "agent" (empty if not resolved)
  resolved_at?: string;  // ISO timestamp (empty if not resolved)
  created_at: string;    // ISO timestamp
}

model TourComment {
  author: string;
  body: string;
}

model TourAnnotation {
  id: string;
  line_start: int32;
  line_end: int32;
  comments: TourComment[];
}

model TourFile {
  path: string;
  old_path?: string;
  status: string;
  additions: int32;
  deletions: int32;
  group: string;
  chapter_id?: string;
  chapter_title?: string;
  chapter_summary?: string;
  risk_note?: string;
  view: string;
  note: string;
  original: string;
  modified: string;
  annotations: TourAnnotation[];
}

model TourDraftText {
  id: string;
  body: string;
}

model TourLineComment {
  line: int32;
  body: string;
}

model TourFileDraft {
  path: string;
  reviewed: boolean;
  note: string;
  annotation_replies: TourDraftText[];
  line_comments: TourLineComment[];
}

model TourQuestionContext {
  path: string;
  line_start?: int32;
  line_end?: int32;
  code?: string;
  source: string;
}

model TourTranscriptEntry {
  id: string;
  role: string;
  body: string;
  event_id?: string;
  context?: TourQuestionContext;
  created_at: string;
}

model TourEvent {
  id: string;
  tour_id: string;
  seq: int32;
  kind: string;
  markdown: string;
  finish: boolean;
  context?: TourQuestionContext;
  created_at: string;
}

model TourRun {
  tour_id: string;
  session_id: string;
  name: string;
  repo_path: string;
  guide_path: string;
  base_ref: string;
  status: TourStatus;
  connection_state: TourConnectionState;
  summary: string;
  warnings: string[];
  files: TourFile[];
  drafts: TourFileDraft[];
  transcript: TourTranscriptEntry[];
  current_file?: string;
  listener_event_seq: int32;
  created_at: string;
  updated_at: string;
  ended_at?: string;
}

model GetReviewStateResultMessage {
  event: "get_review_state_result";
  state?: ReviewState;
  success: boolean;
  error?: string;
}

model TourResultMessage {
  event: "tour_result";
  action: string;
  session_id?: string;
  tour_id?: string;
  tour?: TourRun;
  tour_event?: TourEvent;
  success: boolean;
  error?: string;
}

model TourUpdatedMessage {
  event: "tour_updated";
  session_id: string;
  tour?: TourRun;
}

model ReviewLoopResultMessage {
  event: "review_loop_result";
  action: string;
  session_id: string;
  loop_id?: string;
  review_loop_run?: ReviewLoopRun;
  success: boolean;
  error?: string;
}

model ReviewLoopUpdatedMessage {
  event: "review_loop_updated";
  session_id: string;
  review_loop_run?: ReviewLoopRun;
}

model MarkFileViewedResultMessage {
  event: "mark_file_viewed_result";
  review_id: string;
  filepath: string;
  viewed: boolean;
  success: boolean;
  error?: string;
}

model AddCommentResultMessage {
  event: "add_comment_result";
  success: boolean;
  comment?: ReviewComment;
  error?: string;
}

model UpdateCommentResultMessage {
  event: "update_comment_result";
  success: boolean;
  error?: string;
}

model ResolveCommentResultMessage {
  event: "resolve_comment_result";
  success: boolean;
  error?: string;
}

model DeleteCommentResultMessage {
  event: "delete_comment_result";
  success: boolean;
  error?: string;
}

model GetCommentsResultMessage {
  event: "get_comments_result";
  success: boolean;
  comments?: ReviewComment[];
  error?: string;
}

// Reviewer agent streaming events
model SpawnResultMessage {
  event: "spawn_result";
  id: string;
  success: boolean;
  error?: string;
}

model AttachResultMessage {
  event: "attach_result";
  id: string;
  success: boolean;
  scrollback?: string;           // base64 bytes
  scrollback_truncated?: boolean;
  replay_segments?: ReplaySegment[];
  screen_snapshot?: string;      // base64 ANSI replay of visible frame
  screen_rows?: int32;
  screen_cols?: int32;
  screen_cursor_x?: int32;       // 0-based
  screen_cursor_y?: int32;       // 0-based
  screen_cursor_visible?: boolean;
  screen_snapshot_fresh?: boolean;
  last_seq?: int32;
  cols?: int32;
  rows?: int32;
  pid?: int32;
  running?: boolean;
  error?: string;
}

model GetScreenSnapshotResultMessage {
  event: "get_screen_snapshot_result";
  id: string;
  success: boolean;
  screen_snapshot?: string; // base64 ANSI replay of the visible frame
  screen_rows?: int32;
  screen_cols?: int32;
  screen_cursor_x?: int32; // 0-based
  screen_cursor_y?: int32; // 0-based
  screen_cursor_visible?: boolean;
  screen_snapshot_fresh?: boolean;
  last_seq?: int32;
  cols?: int32;
  rows?: int32;
  running?: boolean;
  error?: string;
}

model PtyOutputMessage {
  event: "pty_output";
  id: string;
  data: string; // base64 bytes
  seq: int32;
}

model SessionExitedMessage {
  event: "session_exited";
  id: string;
  exit_code: int32;
  signal?: string;
}

model PtyDesyncMessage {
  event: "pty_desync";
  id: string;
  reason: string;
}

model PtyResizedMessage {
  event: "pty_resized";
  id: string;
  cols: integer;
  rows: integer;
}

model CommandErrorMessage {
  event: "command_error";
  cmd?: string;
  success: boolean;
  error: string;
}

model BrowserControlRequestMessage {
  event: "browser_control_request";
  request_id: string;
  workspace_id: string;
  tile_id: string;
  action: string;
  params?: string;
  selector?: string;
  text?: string;
}

// ============================================================================
// Response Types
// ============================================================================

model Response {
  ok: boolean;
  error?: string;
  data?: string;
  delegate_result?: DelegateResult;
  workspace_context_result?: WorkspaceContextResult;
  workspace_context_maintenance_result?: WorkspaceContextMaintenanceResult;
  workspace_contexts?: WorkspaceContext[];
  chief_of_staff_dispatch?: ChiefOfStaffDispatch;
  chief_of_staff_dispatches?: ChiefOfStaffDispatch[];
  dispatch_message?: DispatchMessage;
  dispatch_messages?: DispatchMessage[];
  sessions?: Session[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  review_loop_run?: ReviewLoopRun;
  tour?: TourRun;
  tour_event?: TourEvent;
}

model DelegateResult {
  session_id: string;
  workspace_id: string;
  directory: string;
  branch?: string;
  placement: string;
  worktree_created?: boolean;
  dispatch_id?: string;
}

model DelegateResultMessage {
  event: "delegate_result";
  success: boolean;
  error?: string;
  result?: DelegateResult;
}

model WorkspaceContextResult {
  workspace_id: string;
  session_id: string;
  path: string;
  revision: int32;
  canonical_revision: int32;
  modified: boolean;
  stale: boolean;
  updated_by_session_id?: string;
  updated_at?: string;
}

model WorkspaceContextMaintenanceResult {
  action: WorkspaceContextMaintenanceAction;
  workspace_id: string;
  source_revision: int32;
  result_revision: int32;
  changed: boolean;
  agent?: string;
  agent_model?: string;
}

model WorkspaceContextResultMessage {
  event: "workspace_context_result";
  action: string;
  success: boolean;
  error?: string;
  result?: WorkspaceContextResult;
}

model WorkspaceContextListResultMessage {
  event: "workspace_context_list_result";
  request_id: string;
  success: boolean;
  error?: string;
  contexts?: WorkspaceContext[];
}

// Warning for non-critical issues (e.g., gh version too old)
model DaemonWarning {
  code: string;    // e.g., "gh_version_too_old"
  message: string; // Human-readable message
}

model WebSocketEvent {
  event: string;
  // Generic metadata
  cmd?: string;
  protocol_version?: string;

  // Session/PR/repo state payloads
  id?: string;
  pane_id?: string;
  split_id?: string;
  tile_id?: string;
  tile_kind?: string;
  content?: string;
  runtime_id?: string;
  session_id?: string;
  chief_of_staff?: boolean;
  previous_session_id?: string;
  workspace_id?: string;
  session?: Session;
  sessions?: Session[];
  workspace_layout?: WorkspaceLayout;
  workspace?: Workspace;
  workspaces?: Workspace[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  worktrees?: Worktree[];
  plugins?: PluginInfo[];
  plugin_issues?: PluginIssue[];
  operation?: GitOperation;
  branches?: Branch[];
  recent_locations?: RecentLocation[];
  settings?: Record<string>;
  warnings?: DaemonWarning[];

  // Common result fields
  success?: boolean;
  error?: string;

  // PR/worktree/branch operations
  action?: string;
  branch?: string;
  path?: string;
  name?: string;
  priority?: int32;

  // Rate limiting
  rate_limit_resource?: string;
  rate_limit_reset_at?: string;

  // Stash operations
  dirty?: boolean;
  conflict?: boolean;
  found?: boolean;
  stash_ref?: string;

  // Ensure repo operations
  target_path?: string;
  cloned?: boolean;

  // Diff/git payloads
  directory?: string;
  original?: string;
  modified?: string;
  files?: BranchDiffFile[];
  base_ref?: string;
  staged?: GitFileChange[];
  unstaged?: GitFileChange[];
  untracked?: GitFileChange[];

  // PTY lifecycle
  data?: string;
  seq?: int32;
  scrollback?: string;
  scrollback_truncated?: boolean;
  screen_snapshot?: string;
  screen_rows?: int32;
  screen_cols?: int32;
  screen_cursor_x?: int32;
  screen_cursor_y?: int32;
  screen_cursor_visible?: boolean;
  screen_snapshot_fresh?: boolean;
  last_seq?: int32;
  cols?: int32;
  rows?: int32;
  pid?: int32;
  running?: boolean;
  exit_code?: int32;
  reason?: string;
  signal?: string;

  review_loop_run?: ReviewLoopRun;
  tour?: TourRun;
  tour_event?: TourEvent;
  workspace_context_result?: WorkspaceContextResult;
  chief_of_staff_dispatch?: ChiefOfStaffDispatch;
  chief_of_staff_dispatches?: ChiefOfStaffDispatch[];
  workspace_contexts?: WorkspaceContext[];
  dispatch_message?: DispatchMessage;
  dispatch_messages?: DispatchMessage[];
}

alias DaemonEvent =
  InitialStateMessage
  | SessionRegisteredMessage
  | SessionUnregisteredMessage
  | SessionStateChangedMessage
  | WorkspaceRegisteredMessage
  | WorkspaceUnregisteredMessage
  | WorkspaceStateChangedMessage
  | WorkspaceContextChangedMessage
  | SessionTodosUpdatedMessage
  | SessionsUpdatedMessage
  | WorkspaceLayoutMessage
  | WorkspaceLayoutUpdatedMessage
  | WorkspaceLayoutActionResultMessage
  | RenameResultMessage
  | ChiefOfStaffResultMessage
  | ChiefOfStaffDispatchesUpdatedMessage
  | WakeDispatchAgentResultMessage
  | WorkspaceTileContentMessage
  | BrowserControlResponseMessage
  | PRsUpdatedMessage
  | ReposUpdatedMessage
  | AuthorsUpdatedMessage
  | BranchChangedMessage
  | PRActionResultMessage
  | RefreshPRsResultMessage
  | FetchPRDetailsResultMessage
  | RateLimitedMessage
  | WorktreeCreatedEvent
  | WorktreeDeletedEvent
  | WorktreesUpdatedMessage
  | CreateWorktreeResultMessage
  | DeleteWorktreeResultMessage
  | GitOperationStartedMessage
  | GitOperationFinishedMessage
  | SettingsUpdatedMessage
  | GitHubHostsUpdatedMessage
  | PluginsUpdatedMessage
  | PluginActionResultMessage
  | RecentLocationsResultMessage
  | BranchesResultMessage
  | GetDefaultBranchResultMessage
  | FetchRemotesResultMessage
  | ListRemoteBranchesResultMessage
  | EnsureRepoResultMessage
  | GitStatusUpdateMessage
  | FileDiffResultMessage
  | BranchDiffFilesResultMessage
  | GetRepoInfoResultMessage
  | GetReviewStateResultMessage
  | TourResultMessage
  | TourUpdatedMessage
  | ReviewLoopResultMessage
  | ReviewLoopUpdatedMessage
  | WorkspaceContextResultMessage
  | WorkspaceContextListResultMessage
  | MarkFileViewedResultMessage
  | AddCommentResultMessage
  | UpdateCommentResultMessage
  | ResolveCommentResultMessage
  | DeleteCommentResultMessage
  | GetCommentsResultMessage
  | SpawnResultMessage
  | AttachResultMessage
  | GetScreenSnapshotResultMessage
  | PtyOutputMessage
  | SessionExitedMessage
  | PtyDesyncMessage
  | PtyResizedMessage
  | BrowserControlRequestMessage
  | CommandErrorMessage;
