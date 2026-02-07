import "@typespec/json-schema";

using TypeSpec.JsonSchema;

@jsonSchema
namespace Protocol;

// ============================================================================
// Enums
// ============================================================================

enum SessionState {
  working,
  waiting_input,
  idle,
}

enum PRRole {
  author,
  reviewer,
}

enum HeatState {
  cold,
  warm,
  hot,
}

// ============================================================================
// Core Entities
// ============================================================================

model Session {
  id: string;
  label: string;
  directory: string;
  branch?: string;              // Optional: might not be in git repo
  is_worktree?: boolean;        // Optional: false if not worktree
  main_repo?: string;           // Optional: only set if worktree
  state: SessionState;
  state_since: string;          // ISO timestamp, always set
  state_updated_at: string;     // ISO timestamp, always set
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

// ============================================================================
// Command Messages (Client → Daemon)
// ============================================================================

model RegisterMessage {
  cmd: "register";
  id: string;
  label?: string;
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
  starting_from?: string;  // Branch to create worktree from (default: current HEAD)
}

model DeleteWorktreeMessage {
  cmd: "delete_worktree";
  path: string;
}

model GetSettingsMessage {
  cmd: "get_settings";
}

model SetSettingMessage {
  cmd: "set_setting";
  key: string;
  value: string;
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
}

model GetReviewStateMessage {
  cmd: "get_review_state";
  repo_path: string;
  branch: string;
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

model StartReviewMessage {
  cmd: "start_review";
  review_id: string;
  repo_path: string;
  branch: string;
  base_branch: string;
}

model CancelReviewMessage {
  cmd: "cancel_review";
  review_id: string;
}

model SpawnSessionMessage {
  cmd: "spawn_session";
  id: string;
  cwd: string;
  agent: string;            // "claude", "codex", "shell"
  cols: int32;
  rows: int32;
  label?: string;
  resume_session_id?: string;
  resume_picker?: boolean;
  fork_session?: boolean;
  claude_executable?: string;
  codex_executable?: string;
}

model AttachSessionMessage {
  cmd: "attach_session";
  id: string;
}

model DetachSessionMessage {
  cmd: "detach_session";
  id: string;
}

model PtyInputMessage {
  cmd: "pty_input";
  id: string;
  data: string;
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

// ============================================================================
// Result/Event Messages (Daemon → Client)
// ============================================================================

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

model RateLimitedMessage {
  event: "rate_limited";
  resource: string; // "core" or "search"
  reset_at: string; // ISO timestamp
}

model WorktreeCreatedEvent {
  event: "worktree_created";
  path: string;
  branch: string;
  main_repo: string;
}

model CreateWorktreeResultMessage {
  event: "create_worktree_result";
  path?: string;
  success: boolean;
  error?: string;
}

model DeleteWorktreeResultMessage {
  event: "delete_worktree_result";
  path: string;
  success: boolean;
  error?: string;
}

model RecentLocationsResultMessage {
  event: "recent_locations_result";
  locations: RecentLocation[];
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
  success: boolean;
  error?: string;
}

model ReviewState {
  review_id: string;
  repo_path: string;
  branch: string;
  viewed_files: string[];  // List of filepath strings
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
model ReviewFinding {
  filepath: string;
  line_start: int32;
  line_end: int32;
  content: string;
  severity?: string;  // "error", "warning", "info"
}

model ReviewStartedMessage {
  event: "review_started";
  review_id: string;
}

model ReviewChunkMessage {
  event: "review_chunk";
  review_id: string;
  content: string;  // Markdown chunk of the review output
}

model ReviewFindingMessage {
  event: "review_finding";
  review_id: string;
  finding: ReviewFinding;
}

model ReviewCompleteMessage {
  event: "review_complete";
  review_id: string;
  success: boolean;
  error?: string;
}

model ReviewCancelledMessage {
  event: "review_cancelled";
  review_id: string;
}

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
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
}

// Warning for non-critical issues (e.g., gh version too old)
model DaemonWarning {
  code: string;    // e.g., "gh_version_too_old"
  message: string; // Human-readable message
}

model WebSocketEvent {
  event: string;
  cmd?: string;
  protocol_version?: string;
  id?: string;
  session?: Session;
  sessions?: Session[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  worktrees?: Worktree[];
  branches?: Branch[];
  recent_locations?: RecentLocation[];
  settings?: Record<string>;
  warnings?: DaemonWarning[]; // Non-critical warnings to display in UI
  // Rate limiting
  rate_limit_resource?: string;
  rate_limit_reset_at?: string; // ISO timestamp
  // Result feedback (for settings validation, etc.)
  success?: boolean;
  error?: string;
  // Branch operations
  branch?: string;
  // Stash operations
  dirty?: boolean;
  conflict?: boolean;
  found?: boolean;
  stash_ref?: string;
  // Ensure repo operations
  target_path?: string;
  cloned?: boolean;
  // PTY lifecycle
  data?: string;
  seq?: int32;
  scrollback?: string;
  scrollback_truncated?: boolean;
  last_seq?: int32;
  cols?: int32;
  rows?: int32;
  pid?: int32;
  running?: boolean;
  exit_code?: int32;
  reason?: string;
  signal?: string;
}
