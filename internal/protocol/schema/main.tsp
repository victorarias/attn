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
  scheduled,
  state_unknown: "unknown",
}

enum PRRole {
  author,
  reviewer,
}

// WorkspaceStatus is the rolled-up status of all sessions in a workspace.
// Priority (highest first): working > waiting_input > pending_approval > unknown > scheduled > idle > launching.
// `unknown` sits just below the explicit attention states: a session whose
// state could not be determined still wants a look, so it out-prioritizes the
// quiet `scheduled`/`idle` peers — but a clear "needs steering" signal wins.
// (At the session level `unknown` is already an attention state, so the
// workspace dot must be able to surface it rather than masquerading as idle.)
// `scheduled` sits above `idle` because a parked-on-schedule session will
// auto-resume and carries more information than a settled idle peer, but below
// the attention states since it needs no steering. `launching` sits below
// `idle` because it carries less information — any session in a settled state
// out-prioritizes a peer that's still booting. A truly empty workspace (no
// member sessions) rolls up to `idle`.
enum WorkspaceStatus {
  launching,
  working,
  waiting_input,
  pending_approval,
  status_unknown: "unknown",
  scheduled,
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

enum GitOperationKind {
  delete_worktree,
}

enum GitOperationStatus {
  running,
  succeeded,
  failed,
}

enum WorkflowRunStatus {
  running,
  completed,
  failed,
  canceled,
}

enum WorkflowAgentCallStatus {
  running,
  ok,
  skipped,
  errored,
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
  workspace_muted?: boolean;    // True when the owning workspace is hidden from normal sidebar/attention surfaces
  branch?: string;              // Optional: might not be in git repo
  is_worktree?: boolean;        // Optional: false if not worktree
  main_repo?: string;           // Optional: only set if worktree
  state: SessionState;
  state_since: string;          // ISO timestamp, always set
  state_updated_at: string;     // ISO timestamp, always set
  needs_review_after_long_run?: boolean; // True when a 5m+ run finished and awaits user visualization
  recoverable?: boolean;        // True when session can be recovered after daemon restart (agent has resumable conversation)
  chief_of_staff?: boolean;     // True when this session holds the profile-wide coordinator role
  delegated_from_chief?: boolean; // True when this session was delegated from the chief of staff (assignee of a chief-authored ticket)
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
  pinned: boolean;              // Pinned workspaces persist in the sidebar even when empty.
  rank: string;                 // Fractional-index sort key; default order is opening (creation) order.
  layout?: WorkspaceLayout;
}

model WorkspaceContext {
  workspace_id: string;
  content: string;
  revision: int32;
  updated_by_session_id: string;
  updated_at: string;
}

// DispatchWorkState is the agent-reported work state carried on a set_ticket_status
// command (and mapped onto a TicketStatus). It is the one Dispatch-named symbol
// retained after the chief-of-staff dispatch namespace was retired.
enum DispatchWorkState {
  in_progress,
  needs_input,
  ready_for_review,
  completed,
  failed,
}

// TicketStatus is the column a ticket lives in on the work-tracker board.
// `crashed` is attn-authored only (an agent that died without reporting); the
// rest are reachable by the chief or the agent's own self-report.
enum TicketStatus {
  todo,
  working,
  blocked,
  in_review,
  done,
  failed,
  crashed,
}

// TicketEventKind is the type of a ticket activity event in the notification log.
enum TicketEventKind {
  created,
  status_changed,
  commented,
  assigned,
  description_edited,
  attachment_added,
}

// TicketEvent is one entry in a ticket's append-only event log, as delivered to an
// observer reading its unread queue. The store's per-ticket sequence is the cursor
// key and stays server-side; the wire event is for display. from_status /
// to_status are present only on a status_changed event; comment / detail are
// present when the kind carries them.
model TicketEvent {
  ticket_id: string;
  kind: TicketEventKind;
  author: string;
  from_status?: TicketStatus;
  to_status?: TicketStatus;
  comment?: string;
  detail?: string;
  created_at: string;
}

// TicketEventBundle groups an observer's unread events for a single ticket,
// oldest-first — the consume's "bundle by ticket".
model TicketEventBundle {
  ticket_id: string;
  events: TicketEvent[];
}

// TicketInboxResult is the calling identity's unread events, bundled by ticket.
// Reading the inbox advances the identity's per-ticket cursors past everything
// returned, so a second read is empty until new events land.
model TicketInboxResult {
  bundles: TicketEventBundle[];
}

// TicketActivityKind is the type of a human-facing history entry in a ticket's
// detail view: a status change (column move, optional note) or a freeform comment.
// It is the display subset of the richer event log (TicketEventKind).
enum TicketActivityKind {
  status_change,
  comment,
}

// TicketActivity is one entry in a ticket's history thread, as shown in the detail
// view. from_status / to_status are present only on a status_change; comment is
// present when the entry carries a note (a comment, or a note on a status change).
model TicketActivity {
  id: int32;
  kind: TicketActivityKind;
  author: string;
  from_status?: TicketStatus;
  to_status?: TicketStatus;
  comment?: string;
  created_at: string;
}

// TicketAttachment is a file handed over with a ticket's work, copied into the
// ticket's store and shown in the detail view.
model TicketAttachment {
  id: int32;
  filename: string;
  path: string;
  note?: string;
  created_at: string;
}

// Ticket is the wire shape of a durable tracked-work record. `activity` and
// `attachments` are the full history thread and handover files — populated on the
// get_ticket full record, and left empty on the list / board feed for cheapness.
model Ticket {
  id: string;
  title: string;
  description: string;
  status: TicketStatus;
  assignee: string;
  cwd: string;
  last_agent_id: string;
  project_id: string;
  created_at: string;
  updated_at: string;
  closed_at?: string;
  archived_at?: string;
  activity: TicketActivity[];
  attachments: TicketAttachment[];
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

// SetTicketStatusMessage is an agent self-reporting the work state of its own
// bound ticket. The daemon resolves the ticket from the calling session (the
// assignee) — the agent does not name an id — and maps the reported work state
// onto a ticket column. `comment` is an optional note recorded on the change.
model SetTicketStatusMessage {
  cmd: "set_ticket_status";
  source_session_id: string;
  work_state: DispatchWorkState;
  comment?: string;
}

// TicketStatusResult echoes the resolved ticket id and the column it now sits in
// after a status change, so the caller can confirm what moved.
model TicketStatusResult {
  ticket_id: string;
  status: TicketStatus;
}

// TicketCreateMessage mints a standalone backlog ticket — unbound (no assignee, no
// session), starting in todo. Unlike delegation, nothing is dispatched: this is the
// chief/user capturing work to do later. source_session_id is the author of record;
// title is the ticket's name (its slug is derived from it); description is the
// optional brief. An explicit id pins the slug instead of deriving it.
model TicketCreateMessage {
  cmd: "ticket_create";
  source_session_id: string;
  title: string;
  description?: string;
  id?: string;
}

// TicketCreateResult echoes the resolved ticket id (the derived-or-pinned slug), the
// column it landed in (todo), and the title, so the caller learns the final slug.
model TicketCreateResult {
  ticket_id: string;
  status: TicketStatus;
  title: string;
}

// TicketInboxMessage reads (and consumes) the calling session's unread ticket
// events. The daemon resolves the observer identity from the session, so the agent
// names nothing — it just asks for what it has not yet seen.
model TicketInboxMessage {
  cmd: "ticket_inbox";
  source_session_id: string;
}

// TicketAttachMessage hands a file to the calling session's bound ticket. The
// daemon resolves the ticket from the session (the assignee), copies the file at
// source_path into the ticket's store directory (.attn/tickets/<id>/), and records
// the attachment authored by the session. source_path is absolute (the CLI resolves
// it against its cwd) and read by the daemon, so this is a same-machine operation.
model TicketAttachMessage {
  cmd: "ticket_attach";
  source_session_id: string;
  source_path: string;
  filename: string;
  note?: string;
}

// TicketAttachResult echoes the ticket the file landed on and the stored filename,
// so the caller can confirm what was attached and where.
model TicketAttachResult {
  ticket_id: string;
  filename: string;
}

// GetTicketMessage fetches one ticket's full record — the row plus its activity
// thread and attachments — for the detail view. A by-id read needs no caller
// identity: the board feed (the initial_state push / tickets_updated broadcast)
// already bounds what the app can see, and detail is the row it clicked. The
// daemon replies with a ticket_result event correlated by request_id.
model GetTicketMessage {
  cmd: "get_ticket";
  request_id?: string;
  ticket_id: string;
}

// The chief/user acting on a ticket from the app. These are by-id mutations
// authored as the human identity ("you"); the daemon notifies the assigned agent
// and re-broadcasts the board. All three reply with the shared ticket_action_result
// event, correlated by request_id; the updated record reaches the panel through the
// tickets_updated broadcast (which the open detail view re-fetches on).

// ticket_change_status moves a ticket to a chosen column (the chief picks the
// status directly, unlike the agent's work-state forward channel). An optional
// comment is recorded on the change.
model TicketChangeStatusMessage {
  cmd: "ticket_change_status";
  request_id?: string;
  ticket_id: string;
  status: TicketStatus;
  comment?: string;
}

// ticket_add_comment appends a freeform note to a ticket's thread — the chief's
// steer to the agent, carrying real content.
model TicketAddCommentMessage {
  cmd: "ticket_add_comment";
  request_id?: string;
  ticket_id: string;
  comment: string;
}

// ticket_edit_description replaces a ticket's brief (a re-brief / re-scope).
model TicketEditDescriptionMessage {
  cmd: "ticket_edit_description";
  request_id?: string;
  ticket_id: string;
  description: string;
}

// TicketActionResultMessage is the shared reply to a chief ticket action,
// correlated by request_id. success/error report whether the mutation landed; the
// resulting data reaches the UI through the tickets_updated broadcast.
model TicketActionResultMessage {
  event: "ticket_action_result";
  request_id: string;
  success: boolean;
  error?: string;
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

// ============================================================================
// Notebook (durable, profile-wide markdown: a dated journal + a PARA knowledge
// base). See docs/plans/2026-06-13-notebook.md and 2026-06-18-knowledge-base.md.
// Filesystem-canonical: edits use hash-CAS on file bytes, not a revision table.
// These messages are the frontend's WebSocket path; the user-facing `attn
// notebook …` CLI was removed (only notebook_guide remains, for the launcher).
// ============================================================================

model NotebookListMessage {
  cmd: "notebook_list";
  prefix?: string;
  request_id?: string; // set by the WS frontend to correlate the result event
}

model NotebookReadMessage {
  cmd: "notebook_read";
  path: string;
  request_id?: string; // set by the WS frontend to correlate the result event
}

// List the notes whose body links to `path` (root-absolute markdown links).
model NotebookBacklinksMessage {
  cmd: "notebook_backlinks";
  path: string;
  request_id?: string; // set by the WS frontend for result correlation
}

model NotebookWriteMessage {
  cmd: "notebook_write";
  path: string;
  content: string;
  base_hash?: string; // omitted = create-only; present = hash-CAS edit
  request_id?: string; // set by the WS frontend to correlate the result event
}

// Send a Notebook selection to the chief of staff. The daemon appends it to the
// chief inbox note and, when a chief session is live and idle/waiting, fires a
// bounded PTY nudge. The UI never messages the chief directly.
model NotebookSendToChiefMessage {
  cmd: "notebook_send_to_chief";
  selection: string;
  source_path?: string; // the note the selection came from (root-absolute or relative)
  request_id?: string; // set by the WS frontend for result correlation
}

// Resolve a session's notebook role and root for the agent-launch wrapper. The
// daemon answers whether the session is the chief of staff and where the notebook
// root is; the launcher injects the canonical operating guidance from that. This
// is the one surviving unix-socket notebook command after the user-facing
// `attn notebook …` subcommands were removed.
model NotebookGuideMessage {
  cmd: "notebook_guide";
  session_id?: string; // when set, session_is_chief reflects this session's role
}

// Task status surface: list the durable task runner's records, and retry a
// failed/dead one. state and kind are plain strings (not enums) so the on-disk
// record stays self-describing and a new TypeSpec enum cannot trip the quicktype
// identical-enum-merge that drops a generated export.
model NotebookTaskListMessage {
  cmd: "notebook_task_list";
  request_id?: string; // set by the WS frontend to correlate the result event
}

model NotebookTaskRetryMessage {
  cmd: "notebook_task_retry";
  request_id?: string; // set by the WS frontend for result correlation
  task_id: string;
}

// ============================================================================
// Generic filesystem surface (fs_*). A plain view over the configurable root
// (the notebook.root setting): list a directory, read a file, write a file with
// the same hash-CAS contract as notebook_write. Unlike the notebook_* commands
// this is NOT .md-aware or PARA-aware — it surfaces every file and directory as
// it is on disk, so the UI can render a real folder tree over arbitrary content.
// The notebook_* surface remains the curated, markdown-only view of the same
// root; fs_* is the raw filesystem underneath it.
// ============================================================================

// List the immediate children (files and subdirectories) of one directory under
// the root. SHALLOW by design: a tree UI expands lazily, one readdir per node.
model FsListMessage {
  cmd: "fs_list";
  path?: string; // omitted/"" = the root directory itself
  request_id?: string; // set by the WS frontend to correlate the result event
}

model FsReadMessage {
  cmd: "fs_read";
  path: string;
  request_id?: string; // set by the WS frontend to correlate the result event
}

model FsWriteMessage {
  cmd: "fs_write";
  path: string;
  content: string;
  base_hash?: string; // omitted = create-only; present = hash-CAS edit
  request_id?: string; // set by the WS frontend to correlate the result event
}

// Check whether a path exists under the root, without reading it. Used to flag
// in-notebook markdown links that point at a missing note.
model FsExistsMessage {
  cmd: "fs_exists";
  path: string;
  request_id?: string; // set by the WS frontend to correlate the result event
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

model PinWorkspaceMessage {
  cmd: "pin_workspace";
  workspace_id: string;
  pinned: boolean;
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

model WorkflowRunUpsertMessage {
  cmd: "workflow_run_upsert";
  run: WorkflowRun;
}

model WorkflowCallUpsertMessage {
  cmd: "workflow_call_upsert";
  run_id: string;
  call: WorkflowAgentCall;
}

model WorkflowRunGetMessage {
  cmd: "workflow_run_get";
  run_id: string;
}

model WorkflowRunListMessage {
  cmd: "workflow_run_list";
  session_id?: string;
  workspace_id?: string;
}

model WorkflowRunCancelMessage {
  cmd: "workflow_run_cancel";
  run_id: string;
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

// Move an existing leaf out of its current workspace into a brand-new workspace
// the daemon creates on the fly. Like WorkspaceLayoutMoveLeafToWorkspaceMessage
// but with no target_workspace_id: the daemon registers a fresh workspace (seeded
// after the current max rank so it lands at the end of the order) and moves the
// leaf into it. The optional anchor_id/edge/ratio position the leaf within the
// new layout; an empty anchor docks it against the new workspace root.
model WorkspaceLayoutMoveLeafToNewWorkspaceMessage {
  cmd: "workspace_layout_move_leaf_to_new_workspace";
  source_workspace_id: string;
  leaf_id: string;
  anchor_id?: string;
  edge?: WorkspaceLayoutDockEdge;
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

// Reorder a workspace by placing it between two neighbours. The frontend sends
// neighbour ids only; the daemon computes the fractional-index rank key from
// the neighbours' ranks. Empty prev = move to top; empty next = move to bottom.
model SetWorkspaceRankMessage {
  cmd: "set_workspace_rank";
  workspace_id: string;
  prev_workspace_id?: string;
  next_workspace_id?: string;
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
  tickets?: Ticket[];
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

// Broadcast when notebook content changes. origin is agent|ui|external.
model NotebookChangedMessage {
  event: "notebook_changed";
  paths: string[];
  origin: string;
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

// TicketsUpdatedMessage is the live board feed: the non-archived ticket rows
// (bare — no activity/attachments), re-pushed whenever any ticket mutates. It is
// the same shape carried in InitialStateMessage.tickets, so a client renders the
// board identically from the bootstrap snapshot and from each update.
model TicketsUpdatedMessage {
  event: "tickets_updated";
  tickets: Ticket[];
}

// TicketResultMessage is the reply to a get_ticket request, correlated by
// request_id. `ticket` carries the full record on success; `error` is set and
// `ticket` omitted when the id is unknown or the read failed.
model TicketResultMessage {
  event: "ticket_result";
  request_id: string;
  success: boolean;
  ticket?: Ticket;
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

model WorkflowAgentCall {
  run_id: string;
  ordinal: string;
  label?: string;
  phase?: string;
  prompt_hash?: string;
  schema_hash?: string;
  resolved_model?: string;
  resolved_harness?: string;
  agent_type?: string;
  result_json?: string;
  status: WorkflowAgentCallStatus;
  error?: string;
  result_path?: string;
  started_at?: string;
  completed_at?: string;
}

model WorkflowRun {
  run_id: string;
  script_path: string;
  script_hash: string;
  args_json?: string;
  session_id?: string;
  workspace_id?: string;
  status: WorkflowRunStatus;
  phase?: string;
  harness?: string;
  result_json?: string;
  last_error?: string;
  resumable: boolean;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  agent_calls?: WorkflowAgentCall[];
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

model WorkflowRunUpdatedMessage {
  event: "workflow_run_updated";
  run: WorkflowRun;
}

model WorkflowActionResultMessage {
  event: "workflow_action_result";
  action: string;
  run_id?: string;
  run?: WorkflowRun;
  runs?: WorkflowRun[];
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
  sessions?: Session[];
  workspaces?: Workspace[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  review_loop_run?: ReviewLoopRun;
  notebook_entries?: NotebookEntry[];
  notebook_read?: NotebookReadResult;
  notebook_write?: NotebookWriteResult;
  notebook_guide?: NotebookGuideResult;
  ticket_status_result?: TicketStatusResult;
  ticket_inbox_result?: TicketInboxResult;
  ticket_attach_result?: TicketAttachResult;
  ticket_create_result?: TicketCreateResult;
}

model DelegateResult {
  session_id: string;
  workspace_id: string;
  directory: string;
  branch?: string;
  placement: string;
  worktree_created?: boolean;
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

// One notebook note as surfaced by notebook_list (metadata only, no body).
model NotebookEntry {
  path: string;
  type?: string;
  title?: string;
  summary?: string;
  updated?: string;
  size: int32;
}

model NotebookReadResult {
  path: string;
  content: string;
  hash: string;
}

// Result of notebook_write. When conflict is true the write did not apply;
// current_hash is the hash now on disk (empty if gone).
model NotebookWriteResult {
  path: string;
  hash?: string;
  conflict: boolean;
  current_hash?: string;
}

// Result of notebook_guide. guidance is the operating rules text; root is the
// resolved notebook root; session_is_chief is true only when the request's
// session_id currently holds the chief-of-staff role.
model NotebookGuideResult {
  guidance: string;
  root: string;
  session_is_chief: boolean;
}

// One durable task-runner record, surfaced read-only for the task status panel.
// kind and state are plain strings (the runner persists them as self-describing
// strings, never Go/TypeSpec enums). The runner's internal Meta bag (transcript
// filesystem paths and other kind-specific inputs) is deliberately omitted — it
// carries internal inputs that must never reach the user-facing protocol.
model NotebookTask {
  id: string;
  kind: string;
  subject: string;
  state: string;
  attempts: int32;
  next_attempt_at: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

// WS result events for the frontend's notebook reads (request/result pattern).
// The unix CLI uses the synchronous Response fields above; these carry the same
// payloads back to a websocket client correlated by request_id.
model NotebookListResultMessage {
  event: "notebook_list_result";
  request_id: string;
  success: boolean;
  error?: string;
  entries?: NotebookEntry[];
}

model NotebookReadResultMessage {
  event: "notebook_read_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: NotebookReadResult;
}

model NotebookBacklinksResultMessage {
  event: "notebook_backlinks_result";
  request_id: string;
  success: boolean;
  error?: string;
  entries?: NotebookEntry[];
}

// Result of a WS notebook_write (the in-app editor's hash-CAS save). A conflict
// (the file changed on disk since the editor loaded it) is a successful response
// the UI reconciles, carried in result.conflict — not a transport error.
model NotebookWriteResultMessage {
  event: "notebook_write_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: NotebookWriteResult;
}

// Result of notebook_send_to_chief. path is the inbox note the selection was
// appended to; nudged is true when a live chief session was pinged in its PTY
// (false when no chief is set or it was busy — the inbox delivery still happened).
model NotebookSendToChiefResult {
  path: string;
  nudged: boolean;
}

model NotebookSendToChiefResultMessage {
  event: "notebook_send_to_chief_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: NotebookSendToChiefResult;
}

// Result of notebook_task_list: the durable runner's records, newest-updated
// first. An empty/absent tasks array means the runner is disabled or has no work.
model NotebookTaskListResultMessage {
  event: "notebook_task_list_result";
  request_id: string;
  success: boolean;
  error?: string;
  tasks?: NotebookTask[];
}

// Result of notebook_task_retry. task echoes the record after the retry flip
// (failed/dead -> queued); absent when the runner could not act on the id.
model NotebookTaskRetryResultMessage {
  event: "notebook_task_retry_result";
  request_id: string;
  success: boolean;
  error?: string;
  task?: NotebookTask;
}

// Broadcast (no request_id) when any task lifecycle transition occurs, so an open
// task panel re-lists. Fired from the runner's OnChange callback.
model NotebookTasksChangedMessage {
  event: "notebook_tasks_changed";
}

// ---- Generic filesystem surface results (fs_*) ----

// One child of a listed directory. is_dir distinguishes a subdirectory (which the
// UI expands with another fs_list) from a file (which fs_read opens). size is the
// byte size for a file (0 for a directory). modified is the file mtime (RFC3339).
model FsEntry {
  path: string; // root-relative, slash-separated, e.g. "knowledge/areas/foo.md"
  name: string; // the base name, e.g. "foo.md"
  is_dir: boolean;
  size: int32;
  modified?: string;
}

model FsReadResult {
  path: string;
  content: string;
  hash: string;
}

// Result of fs_write. When conflict is true the write did not apply; current_hash
// is the hash now on disk (empty if the target is gone). Same contract as
// NotebookWriteResult.
model FsWriteResult {
  path: string;
  hash?: string;
  conflict: boolean;
  current_hash?: string;
}

model FsListResultMessage {
  event: "fs_list_result";
  request_id: string;
  success: boolean;
  error?: string;
  entries?: FsEntry[];
}

model FsReadResultMessage {
  event: "fs_read_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: FsReadResult;
}

// Result of fs_write (hash-CAS). A conflict is a successful response the UI
// reconciles, carried in result.conflict — not a transport error.
model FsWriteResultMessage {
  event: "fs_write_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: FsWriteResult;
}

model FsExistsResult {
  path: string;
  exists: boolean;
}

model FsExistsResultMessage {
  event: "fs_exists_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: FsExistsResult;
}

// Broadcast when files under the root change. origin is agent|ui|external, the
// same vocabulary as notebook_changed. NOTE: today this rides the notebook
// watcher, which only surfaces .md paths, so a non-.md external edit does not yet
// emit fs_changed; generalizing the watcher to all files lands with the non-text
// editor work.
model FsChangedMessage {
  event: "fs_changed";
  paths: string[];
  origin: string;
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
  workspace_context_result?: WorkspaceContextResult;
  ticket?: Ticket;
  tickets?: Ticket[];
  workspace_contexts?: WorkspaceContext[];
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
  | NotebookChangedMessage
  | SessionTodosUpdatedMessage
  | SessionsUpdatedMessage
  | WorkspaceLayoutMessage
  | WorkspaceLayoutUpdatedMessage
  | WorkspaceLayoutActionResultMessage
  | RenameResultMessage
  | ChiefOfStaffResultMessage
  | TicketsUpdatedMessage
  | TicketResultMessage
  | TicketActionResultMessage
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
  | ReviewLoopResultMessage
  | ReviewLoopUpdatedMessage
  | WorkflowRunUpdatedMessage
  | WorkflowActionResultMessage
  | WorkspaceContextResultMessage
  | WorkspaceContextListResultMessage
  | NotebookListResultMessage
  | NotebookReadResultMessage
  | NotebookBacklinksResultMessage
  | NotebookWriteResultMessage
  | NotebookSendToChiefResultMessage
  | NotebookTaskListResultMessage
  | NotebookTaskRetryResultMessage
  | NotebookTasksChangedMessage
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
