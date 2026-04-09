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

enum SessionAgent {
  claude,
  codex,
  copilot,
  pi,
}

enum WorkspacePaneKind {
  main,
  shell,
}

enum WorkspaceSplitDirection {
  vertical,
  horizontal,
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

// ============================================================================
// Core Entities
// ============================================================================

model Session {
  id: string;
  label: string;
  agent: SessionAgent;
  directory: string;
  endpoint_id?: string;         // Optional: owning endpoint for remote/session-aware routing
  branch?: string;              // Optional: might not be in git repo
  is_worktree?: boolean;        // Optional: false if not worktree
  main_repo?: string;           // Optional: only set if worktree
  state: SessionState;
  state_since: string;          // ISO timestamp, always set
  state_updated_at: string;     // ISO timestamp, always set
  needs_review_after_long_run?: boolean; // True when a 5m+ run finished and awaits user visualization
  recoverable?: boolean;        // True when session can be recovered after daemon restart (agent has resumable conversation)
  todos?: string[];             // Optional: can be empty/absent
  last_seen: string;            // ISO timestamp, always set
  muted: boolean;               // Always has value (defaults false)
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

model WorkspacePane {
  pane_id: string;
  runtime_id?: string;
  kind: WorkspacePaneKind;
  title: string;
}

model WorkspaceSnapshot {
  session_id: string;
  active_pane_id: string;
  layout_json: string;
  panes: WorkspacePane[];
  updated_at?: string;
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
  status_message?: string;
  session_count?: int32;
  capabilities?: EndpointCapabilities;
}

// ============================================================================
// Command Messages (Client → Daemon)
// ============================================================================

model RegisterMessage {
  cmd: "register";
  id: string;
  label?: string;
  agent?: SessionAgent;         // Optional for legacy wrappers
  dir: string;
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

model MuteMessage {
  cmd: "mute";
  id: string;
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
}

model GetSettingsMessage {
  cmd: "get_settings";
}

model SetSettingMessage {
  cmd: "set_setting";
  key: string;
  value: string;
}

model AddEndpointMessage {
  cmd: "add_endpoint";
  name: string;
  ssh_target: string;
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
}

model ListEndpointsMessage {
  cmd: "list_endpoints";
}

model SetEndpointRemoteWebMessage {
  cmd: "set_endpoint_remote_web";
  endpoint_id: string;
  enabled: boolean;
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

model DeleteBranchMessage {
  cmd: "delete_branch";
  main_repo: string;
  branch: string;
  force: boolean;
  endpoint_id?: string;
}

model SwitchBranchMessage {
  cmd: "switch_branch";
  main_repo: string;
  branch: string;
}

model CreateWorktreeFromBranchMessage {
  cmd: "create_worktree_from_branch";
  main_repo: string;
  branch: string;
  path?: string;
}

model CreateBranchMessage {
  cmd: "create_branch";
  main_repo: string;
  branch: string;
}

model CheckDirtyMessage {
  cmd: "check_dirty";
  repo: string;
}

model StashMessage {
  cmd: "stash";
  repo: string;
  message: string;
}

model StashPopMessage {
  cmd: "stash_pop";
  repo: string;
}

model CheckAttnStashMessage {
  cmd: "check_attn_stash";
  repo: string;
  branch: string;
}

model CommitWIPMessage {
  cmd: "commit_wip";
  repo: string;
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

model WontFixCommentMessage {
  cmd: "wont_fix_comment";
  comment_id: string;
  wont_fix: boolean;
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
  agent: string;            // "claude", "codex", "copilot", "shell"
  cols: int32;
  rows: int32;
  label?: string;
  resume_session_id?: string;
  resume_picker?: boolean;
  fork_session?: boolean;
  yolo_mode?: boolean;
  executable?: string;         // Generic executable override for selected agent
  claude_executable?: string;  // Backward compatibility
  codex_executable?: string;   // Backward compatibility
  copilot_executable?: string; // Backward compatibility
  pi_executable?: string;      // Backward compatibility
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

model WorkspaceGetMessage {
  cmd: "workspace_get";
  session_id: string;
}

model WorkspaceSplitPaneMessage {
  cmd: "workspace_split_pane";
  session_id: string;
  target_pane_id: string;
  direction: WorkspaceSplitDirection;
}

model WorkspaceClosePaneMessage {
  cmd: "workspace_close_pane";
  session_id: string;
  pane_id: string;
}

model WorkspaceFocusPaneMessage {
  cmd: "workspace_focus_pane";
  session_id: string;
  pane_id: string;
}

model WorkspaceRenamePaneMessage {
  cmd: "workspace_rename_pane";
  session_id: string;
  pane_id: string;
  title: string;
}

// ============================================================================
// Result/Event Messages (Daemon → Client)
// ============================================================================

model InitialStateMessage {
  event: "initial_state";
  protocol_version?: string;
  daemon_instance_id?: string;
  sessions?: Session[];
  endpoints?: EndpointInfo[];
  workspaces?: WorkspaceSnapshot[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  settings?: Record<string>;
  warnings?: DaemonWarning[];
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

model SessionTodosUpdatedMessage {
  event: "session_todos_updated";
  session: Session;
}

model SessionsUpdatedMessage {
  event: "sessions_updated";
  sessions?: Session[];
}

model WorkspaceSnapshotMessage {
  event: "workspace_snapshot";
  workspace: WorkspaceSnapshot;
}

model WorkspaceUpdatedMessage {
  event: "workspace_updated";
  workspace: WorkspaceSnapshot;
}

model WorkspaceActionResultMessage {
  event: "workspace_action_result";
  action: string;
  session_id: string;
  pane_id?: string;
  success: boolean;
  error?: string;
}

model WorkspaceRuntimeExitedMessage {
  event: "workspace_runtime_exited";
  session_id: string;
  pane_id: string;
  runtime_id: string;
  exit_code: int32;
  signal?: string;
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
}

model SettingsUpdatedMessage {
  event: "settings_updated";
  settings?: Record<string>;
  changed_key?: string;
  success?: boolean;
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

model DeleteBranchResultMessage {
  event: "delete_branch_result";
  branch: string;
  endpoint_id?: string;
  success: boolean;
  error?: string;
}

model SwitchBranchResultMessage {
  event: "switch_branch_result";
  branch: string;
  success: boolean;
  error?: string;
}

model CreateBranchResultMessage {
  event: "create_branch_result";
  branch: string;
  success: boolean;
  error?: string;
}

model CheckDirtyResultMessage {
  event: "check_dirty_result";
  dirty: boolean;
  success: boolean;
  error?: string;
}

model StashResultMessage {
  event: "stash_result";
  success: boolean;
  error?: string;
}

model StashPopResultMessage {
  event: "stash_pop_result";
  success: boolean;
  conflict?: boolean;
  error?: string;
}

model CheckAttnStashResultMessage {
  event: "check_attn_stash_result";
  found: boolean;
  stash_ref?: string;
  success: boolean;
  error?: string;
}

model CommitWIPResultMessage {
  event: "commit_wip_result";
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
  wont_fix: boolean;
  wont_fix_by?: string;  // "user" or "agent" (empty if not wont_fix)
  wont_fix_at?: string;  // ISO timestamp (empty if not wont_fix)
  created_at: string;    // ISO timestamp
}

model GetReviewStateResultMessage {
  event: "get_review_state_result";
  state?: ReviewState;
  success: boolean;
  error?: string;
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

model WontFixCommentResultMessage {
  event: "wont_fix_comment_result";
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

model CommandErrorMessage {
  event: "command_error";
  cmd?: string;
  success: boolean;
  error: string;
}

// ============================================================================
// Response Types
// ============================================================================

model Response {
  ok: boolean;
  error?: string;
  sessions?: Session[];
  workspaces?: WorkspaceSnapshot[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  review_loop_run?: ReviewLoopRun;
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
  runtime_id?: string;
  session_id?: string;
  session?: Session;
  sessions?: Session[];
  workspace?: WorkspaceSnapshot;
  workspaces?: WorkspaceSnapshot[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  worktrees?: Worktree[];
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
}

alias DaemonEvent =
  InitialStateMessage
  | SessionRegisteredMessage
  | SessionUnregisteredMessage
  | SessionStateChangedMessage
  | SessionTodosUpdatedMessage
  | SessionsUpdatedMessage
  | WorkspaceSnapshotMessage
  | WorkspaceUpdatedMessage
  | WorkspaceActionResultMessage
  | WorkspaceRuntimeExitedMessage
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
  | SettingsUpdatedMessage
  | RecentLocationsResultMessage
  | BranchesResultMessage
  | DeleteBranchResultMessage
  | SwitchBranchResultMessage
  | CreateBranchResultMessage
  | CheckDirtyResultMessage
  | StashResultMessage
  | StashPopResultMessage
  | CheckAttnStashResultMessage
  | CommitWIPResultMessage
  | GetDefaultBranchResultMessage
  | FetchRemotesResultMessage
  | ListRemoteBranchesResultMessage
  | EnsureRepoResultMessage
  | GitStatusUpdateMessage
  | FileDiffResultMessage
  | BranchDiffFilesResultMessage
  | GetRepoInfoResultMessage
  | GetReviewStateResultMessage
  | ReviewLoopResultMessage
  | ReviewLoopUpdatedMessage
  | MarkFileViewedResultMessage
  | AddCommentResultMessage
  | UpdateCommentResultMessage
  | ResolveCommentResultMessage
  | WontFixCommentResultMessage
  | DeleteCommentResultMessage
  | GetCommentsResultMessage
  | SpawnResultMessage
  | AttachResultMessage
  | PtyOutputMessage
  | SessionExitedMessage
  | PtyDesyncMessage
  | CommandErrorMessage;
