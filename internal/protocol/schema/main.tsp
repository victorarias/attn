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
  branch?: string;
  is_worktree?: boolean;
  main_repo?: string;
  state: SessionState;
  state_since?: string; // ISO timestamp
  state_updated_at?: string; // ISO timestamp
  todos?: string[];
  last_seen?: string; // ISO timestamp
  muted?: boolean;
}

model PR {
  id: string;
  repo: string;
  number: int32;
  title: string;
  url: string;
  role: PRRole;
  state: string;
  reason?: string;
  last_updated?: string; // ISO timestamp
  last_polled?: string; // ISO timestamp
  muted?: boolean;
  // Detailed status
  details_fetched?: boolean;
  details_fetched_at?: string; // ISO timestamp
  mergeable?: boolean;
  mergeable_state?: string;
  ci_status?: string;
  review_status?: string;
  // Interaction tracking
  head_sha?: string;
  head_branch?: string;
  comment_count?: int32;
  approved_by_me?: boolean;
  has_new_changes?: boolean;
  // Heat state
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
  muted?: boolean;
  collapsed?: boolean;
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
  settings?: Record<string>;
  // Rate limiting
  rate_limit_resource?: string;
  rate_limit_reset_at?: string; // ISO timestamp
}
