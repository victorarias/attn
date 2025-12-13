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
  id: string;
  repo: string;
  number: int32;
  title: string;
  url: string;
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

model RecentLocation {
  path: string;
  label: string;
  last_seen: string;    // ISO timestamp
  use_count: int32;
}

model Branch {
  name: string;
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

model CollapseRepoMessage {
  cmd: "collapse_repo";
  repo: string;
  collapsed: boolean;
}

model QueryReposMessage {
  cmd: "query_repos";
  filter?: string;
}

model FetchPRDetailsMessage {
  cmd: "fetch_pr_details";
  repo: string;
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
  repo: string;
  number: int32;
}

model MergePRMessage {
  cmd: "merge_pr";
  repo: string;
  number: int32;
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

// ============================================================================
// Result/Event Messages (Daemon → Client)
// ============================================================================

model PRActionResultMessage {
  event: "pr_action_result";
  action: string; // "approve" or "merge"
  repo: string;
  number: int32;
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

// ============================================================================
// Response Types
// ============================================================================

model Response {
  ok: boolean;
  error?: string;
  sessions?: Session[];
  prs?: PR[];
  repos?: RepoState[];
}

model WebSocketEvent {
  event: string;
  protocol_version?: string;
  session?: Session;
  sessions?: Session[];
  prs?: PR[];
  repos?: RepoState[];
  worktrees?: Worktree[];
  branches?: Branch[];
  recent_locations?: RecentLocation[];
  settings?: Record<string>;
  // Rate limiting
  rate_limit_resource?: string;
  rate_limit_reset_at?: string; // ISO timestamp
  // Result feedback (for settings validation, etc.)
  success?: boolean;
  error?: string;
  // Branch operations
  branch?: string;
}
