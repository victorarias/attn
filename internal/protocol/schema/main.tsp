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
  recoverable,
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
  revive,
}

enum WorkspaceContextMaintenanceAction {
  compact,
  rollback,
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
  state_reason?: string;        // Why the resolver reached this state (e.g. "stuck", "classifier_verdict"); absent for states it does not own
  chief_of_staff?: boolean;     // True when this session holds the profile-wide coordinator role
  delegated_from_chief?: boolean; // True when this session was delegated from the chief of staff (assignee of a chief-authored ticket)
  ticket_unread?: boolean;      // True when this session has unread ticket events (drives the incoming-nudge indicator)
  nudge_fires_at?: string;      // ISO timestamp a pending ticket-nudge countdown will fire; absent when paused (active session) or no countdown
  turn_owed?: boolean;          // True when the user owes this session a turn. Derived at broadcast from the turn stamps plus the queue's exclusions (shell, chief, pinned, muted); never stored, and never recomputed by a client.
  turn_opened_at?: string;      // ISO timestamp the outstanding turn opened; the queue's sort key. Absent when nothing is owed.
  turn_snoozed_until?: string;  // ISO timestamp a live snooze runs to. Present only while the deferral holds; its presence is what puts the row in the sidebar's snoozed section, and while it is set no state can open a turn for this session except one that breaks through.
  auto_settle_fires_at?: string; // ISO timestamp the auto-settle countdown will close this session's turn. Present only while the countdown is running, which is the whole contract: it is what the terminal tile animates against, and its absence is what says nothing is pending. Absent during the arm delay, when auto-settle is off, and after a cancel.
  auto_settle_held?: boolean;   // True while the auto-settle countdown is frozen because the user is typing or moving the pointer in this session. Mutually exclusive with auto_settle_fires_at by construction: a frozen countdown has no deadline to animate against, so the tile draws a full paused bar and waits. It clears on its own five quiet seconds after the last interaction, when a fresh deadline replaces it.
  auto_settle_dismiss_armed?: boolean; // True while a standing dismissal covers this session's next auto-settle, whether the user armed it during a countdown or ahead of one. Excludes the other two by construction: arming is what stops a pending timer. It is spent at the end of the `working` stretch it covers, and pressing the cancel-countdown shortcut again while nothing is counting down disarms it.
  pinned_at?: string;           // ISO timestamp this session was individually pinned out of the queue. Presence is the pin, and the instant is the pinned band's sort key. Like a pinned workspace it filters at read: the turn stamps keep accruing underneath, so unpinning surfaces whatever was outstanding at its true age.
  context_window_cap?: int32;   // Per-session context-window cap in tokens, set by the user on this one session. Absent means no pin — the launch falls back to the chief or per-agent default settings. A pin outranks both, and takes effect at the next launch of the agent (the daemon reloads a live session in place when the pin changes).
  parent_session_id?: string;   // The agent session a shell was split from. Spawn-time provenance resolved by the daemon, always an agent id and never another shell's, so there is no chain to walk. Whether the link is live is judged at read (parent present in the same workspace), so the field itself is never cleared.
  activity?: string;            // One short present-tense line saying what the agent is doing right now, generated from its transcript by a headless agent. Display only — it never feeds attention, the queue, or state classification. Absent when the feature is off or nothing has been generated for this session yet.
  activity_at?: string;         // ISO timestamp the activity line was generated. Present whenever activity is, so a client can age out a line the generator has stopped refreshing rather than showing it as current.
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
  endpoint_id?: string;         // Set by a hub daemon on workspaces mirrored from a remote endpoint; absent/empty means the workspace is local to this daemon. Remote daemons never set this themselves — the hub stamps it at ingest.
}

model WorkspaceContext {
  workspace_id: string;
  content: string;
  revision: int32;
  updated_by_session_id: string;
  updated_at: string;
}

// PresentAnnotation is an author-pinned note or thread, resolved at query
// time by the daemon from the manifest's annotation entries against the
// pinned head SHA. Comments are ordered; author is always the presenting
// agent.
model PresentAnnotation {
  line_start: int32; // resolved head-side line range
  line_end: int32;
  comments: string[];
}

// PresentFile is one file entry in a present manifest, as authored by the agent.
model PresentFile {
  path: string;
  note?: string;
  // Line stats for this file across the round's pinned base..head diff,
  // from git numstat. Absent when unknown (binary file, or stats lookup failed).
  additions?: int32;
  deletions?: int32;
  // Author-pinned notes/threads, resolved against the pinned head SHA.
  // Omitted when the file has none, or when resolution failed.
  annotations?: PresentAnnotation[];
}

// PresentManifestView is the parsed, daemon-pinned view of a present manifest —
// the title/summary/file list an agent wrote, plus the skip list it declined to
// walk through.
model PresentManifestView {
  title: string;
  summary?: string;
  files: PresentFile[];
  skip: string[];
}

// Presentation is a review session an agent opened over a set of changes.
// kind is "changes" for now (reserved for future presentation kinds).
model Presentation {
  id: string;
  session_id: string;
  ticket_id?: string;
  title: string;
  kind: string;
  repo_path: string;
  status: string;
  created_at: string;
  latest_round_seq: int32;
  latest_round_submitted: boolean;
}

// PresentationRound is one iteration of a presentation, pinned to a base/head
// SHA pair at the moment it was opened.
model PresentationRound {
  id: string;
  presentation_id: string;
  seq: int32;
  base_sha: string;
  head_sha: string;
  created_at: string;
  submitted_at?: string;
  // "approved" | "feedback", set once the round is submitted. Plain string,
  // not an enum, to avoid the quicktype identical-enum merge trap (see side
  // on PresentationComment below).
  verdict?: string;
  manifest: PresentManifestView;
  // All files changed between base_sha and head_sha, computed at query time
  // by the daemon from `git diff` (destination path for renames);
  // additions/deletions filled from numstat when available (binary/rename
  // entries have them absent). Not persisted with the round — omitted
  // entirely when the diff cannot be computed.
  changed_files?: PresentFile[];
}

// PresentationComment is a reviewer note anchored to a line range within one
// round's diff. side is "new" | "old" — plain string, not an enum, to avoid the
// quicktype identical-enum merge trap.
model PresentationComment {
  id: string;
  round_id: string;
  filepath: string;
  line_start: int32;
  line_end: int32;
  side: string;
  content: string;
  author: string;
  created_at: string;
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
  attach_submitted,
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
  // last_user_activity_at is the RFC3339 timestamp of the most recent UI-origin
  // websocket command the daemon observed (session/workspace selection, PR/file
  // views, terminal input/resize) — a proxy for "the user is at the app right
  // now". A watching agent can use it to decide whether to push a notification
  // immediately or hold it until the user is away. Absent when the daemon has
  // seen no user activity since it started.
  last_user_activity_at?: string;
}

// TicketInboxMode distinguishes a deliberate catch-up from a long-lived polling
// watch. Explicit reads drain immediately; watches share the buffered delivery
// eligibility used by nudges.
enum TicketInboxMode {
  explicit,
  watch,
}

// TicketActivityKind is the type of a human-facing history entry in a ticket's
// detail view: a status change (column move, optional note) or a freeform comment.
// It is the display subset of the richer event log (TicketEventKind).
enum TicketActivityKind {
  status_change,
  comment,
  attach,
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

// TicketArtifact is a current file in the ticket's visible Notebook
// directory. The filesystem is authoritative, so the shape carries paths rather
// than a database identity or lifecycle metadata.
model TicketArtifact {
  filename: string;
  notebook_path: string;
  path: string;
}

// Ticket is the wire shape of a durable tracked-work record — a read by id
// (get_ticket, ticket_show) or the agent's board read (ticket_list), which
// carries the brief. `activity` and `artifacts` are the full history thread and
// current attach files: populated on a full record, left empty on the
// ticket_list rows. The app's board feed carries TicketRow instead.
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
  // Machine-reconciliation provenance: set when a dead owning session's outcome
  // was judged by the reconciliation classifier (the verdict lands as an
  // attn-authored comment). Non-terminal + reconciled_at set = the orphan badge.
  reconciled_at?: string;
  // Highest event sequence for this ticket at detail-read time. App mutations
  // echo it as expected_event_seq and must refresh when it is stale.
  latest_event_seq?: int32;
  activity: TicketActivity[];
  artifacts: TicketArtifact[];
}

// TicketRow is a ticket as the board feed carries it — the whole non-archived
// board is re-pushed on every ticket mutation and again on connect, so it holds
// only what a board renders: identity, the column, who owns it, and the stamps
// the app reads (updated_at drives the open detail view's live refresh,
// closed_at the recently-closed filter, reconciled_at the orphan badge). The
// brief, the history thread and the artifacts are detail-sized and belong to a
// read by id (get_ticket over the socket, ticket_show over the CLI), never to a
// list push.
//
// In generated.ts quicktype names an array's element type after the property
// that holds it, not after the model: TicketRow arrives as `TicketElement` and
// `Ticket` as `TicketObject`. Import the aliases at the top of
// useDaemonSocket.ts (`TicketRow`, `Ticket`) rather than either generated name.
model TicketRow {
  id: string;
  title: string;
  status: TicketStatus;
  assignee: string;
  cwd: string;
  last_agent_id: string;
  updated_at: string;
  closed_at?: string;
  reconciled_at?: string;
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
  availability: string; // "bundled" | "user"
  installation_state: string; // "available" | "installed"
  runtime_state: string; // "stopped" | "starting" | "connected" | "degraded" | "parked"
  can_install: boolean;
  can_uninstall: boolean;
  // "starting" | "connected" | "backoff" | "stopped". Plain string keeps
  // generated Go/TypeScript consumers aligned with other runtime status fields.
  runtime_phase?: string;
  restart_attempt?: int32;
  next_restart_at?: string;
  last_exit?: string;
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

// One file the user has touched, as recorded in the daemon's file-activity log.
// `source` says how it was touched: "opened" (any route into a reader tile) or
// "edited" (an agent wrote it, reported by the tool-use hook). The log stores a
// row per (path, source); a reply merges them into one row per path, so
// `last_at` is the most recent activity of any source, `count` is the total,
// and `source` names whichever source was most recent.
model FileActivity {
  path: string;         // absolute
  source: string;       // "opened" | "edited"
  last_at: string;      // ISO timestamp
  count: int32;
  session_id?: string;  // the session in play at the time, when known
}

model DirectoryEntry {
  name: string;
  path: string;
  is_dir: boolean;
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
  // Stable for as long as the client process lives, new on every restart. It is
  // what lets the daemon tell a reconnecting client why it was hung up on
  // (client_eviction_notice); a client that omits it simply gets no notice.
  client_id?: string;
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
  // permission_mode is the agent's *resolved* approval mode at the moment the
  // hook fired ("default", "auto", "acceptEdits", …). attn's own launch flags
  // cannot be trusted for this: a user's global agent settings can put a
  // guardian in the loop for a session attn launched without asking for one,
  // and Shift+Tab changes it mid-session. Absent from agents that do not
  // report it.
  permission_mode?: string;
}

// HookNotificationMessage carries Claude's `Notification` hook. The hook is
// harness-owned and typed — notification_type is "permission_prompt" when the
// agent is blocked on approval and "idle_prompt" when it has been waiting on
// the user for 60s — so it needs no message-string parsing. It arrives ~6s
// after the underlying event, which makes it a corroborator rather than a
// low-latency trigger.
model HookNotificationMessage {
  cmd: "hook_notification";
  id: string;
  notification_type: string;
  message?: string;
}

// HookStopFailureMessage carries Claude's `StopFailure` hook, which replaces
// `Stop` when a turn ends on an API error rather than on an answer. Without it
// an agent that died on a rate limit or an unpaid bill is indistinguishable
// from one that finished quietly, which is the opposite of the truth: the turn
// produced nothing and cannot resume until a person does something.
//
// error_type is the harness's own classification ("rate_limit", "overloaded",
// "billing_error", "authentication_failed", …) and error_message is its
// human-readable form.
model HookStopFailureMessage {
  cmd: "hook_stop_failure";
  id: string;
  error_type: string;
  error_message?: string;
}

// HookCompactionMessage carries Claude's `PreCompact`/`PostCompact` pair, which
// bracket the agent rewriting its own context. Compaction is work — measured at
// 26s — but it paints no spinner frames and opens no turn, so between turns it
// reads as a session that went quiet and settles. active is true on PreCompact
// and false on PostCompact; trigger is "manual" or "auto".
model HookCompactionMessage {
  cmd: "hook_compaction";
  id: string;
  active: boolean;
  trigger?: string;
}

// A SessionStart hook observed the provider-owned conversation currently hosted
// by one stable attn session. transcript_path is the provider's exact live path;
// resume_session_id remains the durable identity used to reconstruct it after a
// daemon restart.
model SetSessionResumeIDMessage {
  cmd: "set_session_resume_id";
  id: string;
  resume_session_id: string;
  transcript_path?: string;
}

model SessionInstructionsMessage {
  cmd: "session_instructions";
  target_session_id: string;
  question: string;
}

// Reads provider-neutral, redacted transcript events strictly after the opaque
// cursor. The daemon owns native transcript discovery and cursor validation.
model SessionTranscriptMessage {
  cmd: "session_transcript";
  target_session_id: string;
  after_cursor?: string;
}

// Replays the daemon's per-session ring of state observations so a wrong color
// can be explained: which sources spoke, what each claimed, and what happened to
// each claim. Diagnostic only — reading it changes nothing.
model StateExplainMessage {
  cmd: "state_explain";
  target_session_id: string;
}

// One agent observes another without interrupting it: state, todos, the last
// assistant message, and the rendered screen, all served from what the daemon
// already holds. Passive by construction — nothing here touches the target's
// PTY or generates input, so watching costs the observed agent nothing.
model AgentPeekMessage {
  cmd: "agent_peek";
  target_session_id: string;    // full id or unique prefix (the short id from `attn agent list`)
}

// One agent sends another a message. The daemon persists it, composes the
// attributed prompt, and delivers it into the target's conversation — the
// sender never touches the target's PTY, so attribution cannot be forged
// beyond what the sender puts in content.
model AgentMsgMessage {
  cmd: "agent_msg";
  target_session_id: string;    // full id or unique prefix
  source_session_id: string;    // who is speaking; the daemon composes from this
  content: string;
}

// A Stop is not always terminal: the turn may yield with background work still
// in flight (it auto-resumes on completion) or park on a pending scheduled
// wakeup. These carry that as FACTS observed by the hook — the statuses of the
// agent's background tasks, and how many scheduled wakeups are pending — and the
// daemon decides what they mean. The hook used to decide and report a state
// instead, which hid the facts from the daemon and forced the hook to query the
// daemon back for the chief-of-staff role it needed to decide.
//
// Both are optional: a hook binary predating them reports neither, and the
// daemon then classifies the stop as terminal, which is what such a hook already
// caused by omitting the state update.
model StopMessage {
  cmd: "stop";
  id: string;
  transcript_path: string;
  background_task_statuses?: string[];
  pending_session_crons?: int32;
}

model TodosMessage {
  cmd: "todos";
  id: string;
  todos: string[];
}

// Files an agent just wrote, reported by the tool-use hook after a completed
// edit. Paths are absolute; the daemon keeps only the ones the ⌘P opener can
// show, so a session reporting source files costs a no-op rather than log rows.
model FilesEditedMessage {
  cmd: "files_edited";
  id: string;
  paths: string[];
}

model DelegateMessage {
  cmd: "delegate";
  request_id: string; // Stable caller-supplied idempotency key for one logical delegation
  source_session_id: string;
  brief: string; // Empty only when ticket_id supplies the task source
  ticket_id?: string; // Existing ticket to adopt; mutually exclusive with brief
  confirm?: boolean; // Allow takeover when the ticket has a non-orphan assignee
  agent?: string;
  label?: string;
  yolo_mode?: boolean;
  placement?: string;
  workspace_id?: string;
  cwd?: string;
  worktree?: DelegateWorktreeRequest;
  `model`?: string; // Pin the delegated agent's model (alias or full id); empty = agent default
  effort?: string; // Pin the delegated agent's reasoning effort; empty = agent default
  allow_worktree_reuse?: boolean; // Exceptional opt-in to share an existing worktree
}

model DelegateStatusMessage {
  cmd: "delegate_status";
  id: string; // Request id or daemon-issued operation id
}

// SetTicketStatusMessage moves a ticket to the column for a reported work
// state. Without ticket_id, the daemon resolves the ticket from the calling
// session (the assignee) — an agent self-reporting its own bound ticket. With
// ticket_id, it moves that ticket directly instead, regardless of who is bound
// to it. `comment` is an optional note recorded on the change.
model SetTicketStatusMessage {
  cmd: "set_ticket_status";
  source_session_id: string;
  work_state: DispatchWorkState;
  comment?: string;
  ticket_id?: string; // moves this ticket by id instead of the session's bound ticket — any ticket, no ownership gate
}

// TicketStatusResult echoes the resolved ticket id and the column it now sits in
// after a status change, so the caller can confirm what moved.
model TicketStatusResult {
  ticket_id: string;
  status: TicketStatus;
  catch_up?: TicketEventBundle;
  applied: boolean; // false when catch_up had to be read first and the move did not run
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

// TicketCommentMessage posts a one-shot comment from the calling agent onto ANY
// ticket by id — not just the one bound to its session. The daemon records the
// comment authored by the session, which informs that ticket's participants but
// does NOT enroll the commenter in the ticket's future notifications (comment
// authorship confers no participation; standing interest is opt-in via take or
// subscribe). This is the agent equivalent of the app's ticket_add_comment, which
// authors as "you".
model TicketCommentMessage {
  cmd: "ticket_comment";
  source_session_id: string;
  ticket_id: string;
  comment: string;
}

// TicketCommentResult echoes the ticket the comment landed on, so the caller can
// confirm which ticket it posted to.
model TicketCommentResult {
  ticket_id: string;
  catch_up?: TicketEventBundle;
  applied: boolean; // false when catch_up had to be read first and the comment did not post
}

// PresentOpenMessage opens a new presentation (or a new round on an existing
// one, when presentation_id is set) from a raw manifest YAML the agent wrote.
// The daemon parses and pins the manifest and the base/head SHAs at open time.
model PresentOpenMessage {
  cmd: "present_open";
  source_session_id: string;
  manifest_yaml: string;
  presentation_id?: string;
  ticket_id?: string;
}

// PresentOpenResult echoes the round the daemon created, so the caller can
// report where the presentation now lives.
model PresentOpenResult {
  presentation_id: string;
  round_id: string;
  seq: int32;
  base_sha: string;
  head_sha: string;
  title: string;
  // Anchor warnings from resolving the manifest's annotations (e.g. an
  // ambiguous anchor), formatted "path[index]: message". Absent when there
  // were none. An error-severity issue fails the open instead of appearing here.
  warnings?: string[];
}

// PresentFeedbackMessage reads reviewer feedback for a round back to the
// authoring agent as markdown. seq omitted/0 means the latest round.
model PresentFeedbackMessage {
  cmd: "present_feedback";
  presentation_id: string;
  seq?: int32;
}

// PresentFeedbackResult carries the rendered feedback markdown for the
// resolved round, plus whether that round has been submitted yet. verdict is
// the submitted round's verdict ("approved" | "feedback"), absent until
// submitted. presentation_status is the presentation's current status
// ("open" | "approved" | "closed") — a poller uses it to detect a reviewer
// closing the presentation instead of submitting a round.
model PresentFeedbackResult {
  markdown: string;
  seq: int32;
  submitted: boolean;
  verdict?: string;
  presentation_status: string;
}

// TicketInboxMessage reads (and consumes) the calling session's unread ticket
// events. The daemon resolves the observer identity from the session, so the agent
// names nothing — it just asks for what it has not yet seen.
model TicketInboxMessage {
  cmd: "ticket_inbox";
  source_session_id: string;
  mode?: TicketInboxMode;
  // Watch polls report their cadence so the daemon can distinguish a live slow
  // watcher from an abandoned one without narrowing the CLI's interval support.
  watch_interval_ms?: int64;
}

// TicketListMessage reads the board — every ticket, optionally filtered by status —
// the way the app sees it, but pulled over the agent socket instead of pushed as a
// tickets_updated broadcast. This is the read foundation the write verbs depend on:
// an agent finds a ticket-id here before it can comment on (or take/subscribe to) it.
// Unlike the write verbs it is NOT identity-scoped — it is a global board read, so
// source_session_id is optional and unused, present only for command-shape uniformity.
// Rows carry the description (the brief) but not the activity thread — unlike the
// app's board feed, whose TicketRow drops the brief too.
model TicketListMessage {
  cmd: "ticket_list";
  source_session_id?: string;
  status?: string;
  include_archived?: boolean;
}

// TicketListResult is the board snapshot — bare ticket rows, newest first.
model TicketListResult {
  tickets: Ticket[];
}

// TicketShowMessage reads one ticket's full record — metadata, description, and the
// complete activity thread (with full bodies) plus current artifacts — pulled over the
// agent socket instead of pushed as a tickets_updated broadcast. Unlike ticket_inbox
// this is non-consuming: it never advances the calling session's unread cursor, so
// it can be re-read at will. Like ticket_list it is NOT identity-scoped — a global
// read of a ticket by id, so source_session_id is optional and unused, present only
// for command-shape uniformity.
model TicketShowMessage {
  cmd: "ticket_show";
  source_session_id?: string;
  ticket_id: string;
}

// TicketShowResult is the full ticket record — the same shape the app's get_ticket
// serves, with Activity and Artifacts populated.
model TicketShowResult {
  ticket: Ticket;
}

// TicketSubscribeMessage opts the calling session into a ticket's notifications —
// an explicit, opt-in standing interest in a ticket it is not assigned to. The
// daemon records the subscription authored by the session (identity = session), so
// the agent names only the ticket. Subscribing makes the session a participant
// (nudged about activity, the ticket delivered in its inbox) without advancing its
// cursor, so the first inbox after subscribing delivers the ticket's history.
model TicketSubscribeMessage {
  cmd: "ticket_subscribe";
  source_session_id: string;
  ticket_id: string;
}

// TicketSubscribeResult echoes the ticket the subscription landed on.
model TicketSubscribeResult {
  ticket_id: string;
  unread_count?: int32;
}

// TicketUnsubscribeMessage opts the calling session back out of a ticket's
// notifications. It is idempotent — opting out when not subscribed succeeds — so it
// does not require the ticket to still exist.
model TicketUnsubscribeMessage {
  cmd: "ticket_unsubscribe";
  source_session_id: string;
  ticket_id: string;
}

// TicketUnsubscribeResult echoes the ticket the session unsubscribed from.
model TicketUnsubscribeResult {
  ticket_id: string;
}

// TicketTakeMessage claims a ticket for the calling session — it becomes the
// assignee, the way a delegated agent is the assignee of its bound ticket, so the
// ticket's notifications now route to it. Taking a ticket already assigned to
// someone else requires confirm=true, a guard against silently stealing another
// agent's active work; taking an unassigned ticket (a backlog todo) or one already
// yours does not. Taking does NOT advance the taker's cursor, so its first inbox
// after this delivers the ticket's history (it is picking up unseen work).
model TicketTakeMessage {
  cmd: "ticket_take";
  source_session_id: string;
  ticket_id: string;
  confirm?: boolean;
}

// TicketTakeResult echoes the ticket and the assignee it was taken FROM (empty when
// it was unassigned), so the caller can report what changed.
model TicketTakeResult {
  ticket_id: string;
  previous_assignee: string;
  unread_count?: int32;
}

model TicketAttachFile {
  source_path: string;
  filename: string;
}

// TicketAttachMessage copies one or more artifact files into a ticket's
// visible Notebook directory and records one durable attach receipt. Without a
// ticket_id the daemon resolves the caller's bound ticket; with one it follows the
// same permissive by-id authorization as ticket status and comment.
model TicketAttachMessage {
  cmd: "ticket_attach";
  source_session_id: string;
  ticket_id?: string;
  files: TicketAttachFile[];
  state?: DispatchWorkState;
  comment?: string;
  request_id?: string;
  expected_event_seq?: int32;
}

// TicketAttachResult is the durable receipt returned for a new submission and
// for an idempotent retry of the same fingerprint.
model TicketAttachResult {
  ticket_id: string;
  artifacts: TicketArtifact[];
  fingerprint: string;
  event_seq: int32;
  state: TicketStatus;
  deduplicated: boolean;
  catch_up?: TicketEventBundle;
  applied: boolean; // false when catch_up had to be read first and nothing was attached
}

model TicketAttachResultMessage {
  event: "ticket_attach_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: TicketAttachResult;
}

// GetTicketMessage fetches one ticket's full record — the row plus its activity
// thread and current artifacts — for the detail view. A by-id read needs no caller
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
  expected_event_seq?: int32;
}

// ticket_add_comment appends a freeform note to a ticket's thread — the chief's
// steer to the agent, carrying real content.
model TicketAddCommentMessage {
  cmd: "ticket_add_comment";
  request_id?: string;
  ticket_id: string;
  comment: string;
  expected_event_seq?: int32;
}

// ticket_edit_description replaces a ticket's brief (a re-brief / re-scope).
model TicketEditDescriptionMessage {
  cmd: "ticket_edit_description";
  request_id?: string;
  ticket_id: string;
  description: string;
  expected_event_seq?: int32;
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

// ticket_resume reopens the agent session bound to a ticket: same session id
// (the assignee), the ticket's cwd, resuming the mirrored conversation when
// resumable. The daemon owns the whole composite (register workspace + add pane +
// spawn, with rollback) so no racy frontend seeding is involved; the frontend just
// sends this command and focuses the result. The session/pane reach the UI through
// the normal session_registered / workspace_layout_updated broadcasts.
model TicketResumeMessage {
  cmd: "ticket_resume";
  request_id?: string;
  ticket_id: string;
}

// TicketResumeResultMessage replies to ticket_resume, correlated by request_id.
// session_id is the (re)opened session the frontend focuses; already_running is
// true when the assignee session was still registered, so the daemon spawned
// nothing and the frontend just focuses the existing pane.
model TicketResumeResultMessage {
  event: "ticket_resume_result";
  request_id: string;
  success: boolean;
  error?: string;
  session_id?: string;
  workspace_id?: string;
  already_running?: boolean;
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
// base).
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

// JournalAppendMessage appends one markdown block to the notebook's dated daily
// journal (journal/<date>.md) through the daemon's serialized notebook.Store
// writer, instead of an agent editing the file directly with its own file-edit
// tools. The chief of staff and the daemon's own keeper both write the same
// journal file; a direct file edit races the keeper's writes and hits
// "file modified since read" on nearly every append. Routing through the daemon's
// single cached Store per root serializes all writers through one append, which
// never conflicts (unlike a hash-CAS note edit). Not identity-scoped (like
// notebook_guide); source_session_id is optional and unused, present only for
// command-shape uniformity with the ticket verbs.
model JournalAppendMessage {
  cmd: "journal_append";
  source_session_id?: string;
  entry: string;
  date?: string; // YYYY-MM-DD; defaults to today in the daemon's local timezone
}

// JournalAppendResult echoes where the entry landed: rel_path is the
// notebook-relative path (journal/<date>.md) and hash is the file's content hash
// after the append.
model JournalAppendResult {
  rel_path: string;
  hash: string;
}

// Task status surface: list the durable job queue's records, and retry a
// failed/dead one. state and kind are plain strings (not enums) so the on-disk
// record stays self-describing and a new TypeSpec enum cannot trip the quicktype
// identical-enum-merge that drops a generated export.
model TaskListMessage {
  cmd: "task_list";
  request_id?: string; // set by the WS frontend to correlate the result event
}

model TaskRetryMessage {
  cmd: "task_retry";
  request_id?: string; // set by the WS frontend for result correlation
  task_id: string;
}

// Notifications surface: a global, daemon-level feed of durable notifications
// (see docs/plans/2026-07-02-bg-task-notifications.md). The producer is the task
// runner, which adds one when a background task reaches terminal `dead`. The
// frontend lists them, marks them read, and re-lists on the notifications_updated
// broadcast; the unread count drives a sidebar badge.
model NotificationListMessage {
  cmd: "notification_list";
  request_id?: string; // set by the WS frontend to correlate the result event
}

// Mark notifications read. notification_id marks that one; when absent, ALL
// unread notifications are marked read ("mark all read").
model NotificationMarkReadMessage {
  cmd: "notification_mark_read";
  request_id?: string; // set by the WS frontend for result correlation
  notification_id?: string;
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
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string; // set by the WS frontend to correlate the result event
}

model FsReadMessage {
  cmd: "fs_read";
  path: string;
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string; // set by the WS frontend to correlate the result event
}

// Read one image file's bytes as base64, for the notebook editor to render
// ![alt](path) images without widening Tauri's fs permissions. Only a fixed
// allowlist of image extensions is served; anything else is an error.
model FsReadAssetMessage {
  cmd: "fs_read_asset";
  path: string;
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string; // set by the WS frontend to correlate the result event
}

model FsWriteMessage {
  cmd: "fs_write";
  path: string;
  content: string;
  base_hash?: string; // omitted = create-only; present = hash-CAS edit
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string; // set by the WS frontend to correlate the result event
}

model FsRenameMessage {
  cmd: "fs_rename";
  path: string;
  new_path: string;
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string;
}

model FsDeleteMessage {
  cmd: "fs_delete";
  path: string;
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string;
}

// Check whether a path exists under the root, without reading it. Used to flag
// in-notebook markdown links that point at a missing note.
model FsExistsMessage {
  cmd: "fs_exists";
  path: string;
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string; // set by the WS frontend to correlate the result event
}

// Subscribe this client to fs_changed broadcasts for a root: starts a live
// watcher on first subscriber, refcounted per client so multiple subscribers (or
// repeat calls) share one watcher. The notebook root is always watched already,
// so fs_watch on it is a success no-op. Pair with fs_unwatch when the client no
// longer needs live updates for that root.
model FsWatchMessage {
  cmd: "fs_watch";
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string; // set by the WS frontend to correlate the result event
}

// Drop this client's subscription from fs_watch. The watcher for a root closes
// once no client holds it open (including on client disconnect). Unwatching a
// root this client never watched is a success no-op.
model FsUnwatchMessage {
  cmd: "fs_unwatch";
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  request_id?: string; // set by the WS frontend to correlate the result event
}

// Bounded recursive file index of a root, for the ⌘P finder. No
// client-controlled limit — the entry cap is a server-side constant
// (maxFsIndexEntries). Root resolution goes through the same resolveFsRoot
// chokepoint as every other fs_* command, so an explicit root is gated on the
// authenticated app client.
model FsIndexMessage {
  cmd: "fs_index";
  root?: string; // Absolute directory to operate under; omitted/empty = the notebook root.
  // Restrict the index to these file extensions, without leading dots and
  // case-insensitive (e.g. ["md", "markdown"]). Omitted/empty indexes every
  // file. The entry cap applies AFTER filtering, so a markdown-only index
  // cannot be truncated by unrelated files.
  extensions?: string[];
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

model SessionSelectedMessage {
  cmd: "session_selected";
  id: string;
}

model WorkspaceSelectedMessage {
  cmd: "workspace_selected";
  workspace_id: string;
}

// TriggerNudgeMessage is the user clicking the incoming-nudge indicator to deliver
// the pending ticket doorbell immediately, bypassing the countdown. Fire-and-forget.
model TriggerNudgeMessage {
  cmd: "trigger_nudge";
  session_id: string;
}

// SettleTurnMessage closes the turn a session opened. It is the only way a turn
// ever ends: no state transition removes a session from the queue, so an agent
// stays there — running or not — until the user says they are done with it.
// Fire-and-forget; the daemon broadcasts the session afterwards.
model SettleTurnMessage {
  cmd: "settle_turn";
  session_id: string;
}

// SnoozeTurnMessage defers a session until an instant: it closes any open turn
// and stops the next one from opening before then. Snooze is available on any
// agent, not only one that currently owes a turn, so a run you already know you
// do not want handed back can be deferred before it finishes.
//
// `until` is an absolute ISO instant computed by the client, because "tomorrow"
// and "Monday" are calendar questions that need the user's timezone — which a
// remote endpoint's daemon does not share. Fire-and-forget.
model SnoozeTurnMessage {
  cmd: "snooze_turn";
  session_id: string;
  until: string;
}

// WakeTurnMessage ends a snooze early. It clears the deadline; whether a turn
// then opens depends on the state the agent is in at that instant, which is the
// daemon's call. Fire-and-forget.
model WakeTurnMessage {
  cmd: "wake_turn";
  session_id: string;
}

// CancelCountdownMessage is the user calling off whatever attn is counting down
// to on this session. attn runs two such countdowns and both are things about to
// happen *to* an agent without being asked for, so one verb calls off either:
//
//   - auto-settle, about to close a turn the user still wants;
//   - the ticket nudge, about to doorbell the agent with pending ticket activity.
//
// The daemon cancels every countdown the session has, because the sender is
// acting on what it can see and both indicators live in the same pane header. A
// session with neither is a no-op.
//
// Neither cancel re-arms on the state the session is already in — re-arming
// seconds later would make the cancel meaningless — and each stays cancelled
// until something genuinely new happens: for auto-settle, the session leaving
// `working` and being steered again; for the nudge, ticket activity newer than
// what was pending when the user said no. Fire-and-forget.
model CancelCountdownMessage {
  cmd: "cancel_countdown";
  session_id: string;
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

// Pins one session out of the queue, leaving its workspace and its siblings
// where they are. The chief is refused: it already has an anchored slot.
model PinSessionMessage {
  cmd: "pin_session";
  session_id: string;
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

model InstallBundledPluginMessage {
  cmd: "install_bundled_plugin";
  name: string;
}

model UninstallPluginMessage {
  cmd: "uninstall_plugin";
  name: string;
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

// Recently touched files, frecency-ranked (frequency weighted by recency, the
// same model as get_recent_locations). Feeds the ⌘P markdown opener's list on
// an empty query. Entries whose file no longer exists are pruned rather than
// returned.
model RecentFilesMessage {
  cmd: "recent_files";
  limit?: int32;  // Default 20
  // The directory the caller is working in (the opener's fuzzy root). Files
  // under it rank above equally-scored files from elsewhere, so the workspace
  // in front of you wins without hiding the rest.
  root?: string;
  request_id?: string;
}

// Lists one directory. Directories are always returned; extensions adds regular
// files with those extensions (no dots, e.g. ["md"]) to the listing. Asking for
// files exposes document names rather than just tree shape, so that variant is
// restricted to the authenticated app client.
model BrowseDirectoryMessage {
  cmd: "browse_directory";
  input_path: string;
  extensions?: string[];
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
  head_ref?: string; // If set, the modified side is pinned to `git show <head_ref>:<path>` instead of the working tree (used by the presentation reader for base_sha->head_sha diffs)
  request_id?: string; // Client-chosen correlation id, echoed verbatim in the result
}

model GetRepoInfoMessage {
  cmd: "get_repo_info";
  repo: string;
  endpoint_id?: string;
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

// Dual-surface (unix-socket/CLI `attn automation apply` + WS, the app
// editor's Save): the socket/CLI path applies unconditionally — "last writer
// wins," unchanged by this field addition. The WS path additionally supplies
// expected_id and expected_revision so the daemon can refuse a save that
// would silently fork the definition (an edited id — apply is keyed on the
// id *inside* the YAML) or silently clobber a concurrent apply (a stale
// revision) instead of persisting it; see Daemon.automationApplyWithGuards.
// Both are absent/zero when creating. request_id is optional, used only by
// WS for correlation, like automation_delete's below.
model AutomationApplyMessage {
  cmd: "automation_apply";
  definition_yaml: string;
  request_id?: string;
  expected_id?: string;
  expected_revision?: int32;
}
model AutomationRunMessage { cmd: "automation_run"; definition_id: string; request_id: string; input_json?: string; pr_url?: string; }

// Dual-surface (unix-socket `attn automation validate` + WS "Validate"
// button): runs exactly the checks automation_apply runs before persisting
// (Daemon.validateAutomationSpec) without writing anything. Deliberately not
// just a thinner call to ParseDefinitionYAML — see AutomationValidateResultMessage
// below for why a validate that skipped one of apply's checks would actively
// lie to the user.
model AutomationValidateMessage {
  cmd: "automation_validate";
  definition_yaml: string;
  request_id?: string;
}

// Dual-surface (unix-socket/CLI + WS), unlike automation_set_enabled below:
// mirrors automation_apply/automation_run's shape. request_id is optional
// because the WS path uses it for correlation but the socket/CLI path
// doesn't need one.
model AutomationDeleteMessage {
  cmd: "automation_delete";
  definition_id: string;
  request_id?: string;
}

// Same dual-surface shape as automation_delete above. Unlike delete, cleanup
// is disk-only (worktrees only; never rows or artifacts) and repeatable —
// see AutomationCleanupResultMessage's cleaned/kept_dirty/kept_active fields.
model AutomationCleanupMessage {
  cmd: "automation_cleanup";
  definition_id: string;
  request_id?: string;
}

// WS-only automations surface: list definitions, list one definition's runs, and
// toggle enabled. automation_run (above) is reused for WS run-now; it already
// carries request_id for correlation.
model AutomationDefinitionsGetMessage {
  cmd: "automation_definitions_get";
  request_id?: string;
}

// WS-only: the editor's load-for-edit and load-for-create-template call, in
// one request. definition_id "" returns the starter template
// (automation.StarterTemplateYAML) at revision 0, so create and edit share
// one frontend code path and the template can never structurally drift from
// what ParseDefinitionYAML accepts. A non-empty, unknown definition_id
// surfaces as success=false with an error, like the other WS lookups above.
model AutomationDefinitionGetMessage {
  cmd: "automation_definition_get";
  definition_id: string;
  request_id?: string;
}
model AutomationRunsGetMessage {
  cmd: "automation_runs_get";
  definition_id: string;
  request_id?: string;
}
model AutomationSetEnabledMessage {
  cmd: "automation_set_enabled";
  definition_id: string;
  enabled: boolean;
  request_id?: string;
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
  chief_of_staff?: boolean;    // Launch this session already holding the chief role (guidance injected at first spawn)
  spawned_from?: string;       // The session this one was split from. Consulted only for agent "shell": the daemon resolves it to the owning agent session and stores that as the satellite's parent.
  initial_prompt?: string;
  executable?: string;         // Generic executable override for selected agent
  claude_executable?: string;  // Backward compatibility
  codex_executable?: string;   // Backward compatibility
  copilot_executable?: string; // Backward compatibility
  `model`?: string;            // Pin the launched agent's model; empty = agent default (chief launches fall back to chief_model_<agent>)
  effort?: string;             // Pin the launched agent's reasoning effort; empty = agent default
  // A recorded conversation to pick up (see list_past_conversations). Only
  // meaningful for a conversation agent: the host copies that history into this
  // session's own storage on its first launch and continues from it, so the
  // conversation being resumed is never written to by two sessions. Ignored
  // once this session has a history of its own, which is what keeps a revive
  // from rewinding a resumed conversation to the day it was picked up.
  resume_conversation_file?: string;
}

model AttachSessionMessage {
  cmd: "attach_session";
  id: string;
  attach_policy?: AttachPolicy;
  // PTY geometry for revive; required when attach_policy is "revive"
  cols?: int32;
  rows?: int32;
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

// Pulls the pixels behind a placement. Images are pulled on miss, never pushed:
// a stored image is megabytes of raw pixels and a client usually already has
// it, so kitty_placements describes and this fetches what the cache lacks.
//
// image_id alone identifies the image — a terminal holds one generation per id,
// and the answer states which. Asking is enough: this is answered whether or
// not the client advertised "kitty_images". What decides the shape of the
// answer is "binary_pty_output" — with it the pixels come back as binary frame
// 0x02, without it as base64 in kitty_image_result.
model GetKittyImageMessage {
  cmd: "get_kitty_image";
  id: string;
  image_id: safeint;
}

model PtyInputMessage {
  cmd: "pty_input";
  id: string;
  data: string;
  source?: string;
}

// Reports genuine pointer movement inside a terminal pane. No bytes are written
// to the PTY: this is user engagement for auto-settle only, and deliberately
// does not participate in the keyboard-only ticket-nudge splice guard.
model TerminalPointerActivityMessage {
  cmd: "terminal_pointer_activity";
  id: string;
}

// Sends a message to a conversation session — one whose agent runs in a headless
// host process rather than a PTY, so there is no keystroke to type. `id` is the
// attn session id, exactly as pty_input uses it.
//
// `mode` is WHEN the agent reads it, and it exists because a running agent has
// two queues:
//
//   prompt     (default, and what an absent mode means) the first word of a
//              run. Refused while a run is open.
//   steer      read at the agent's next turn boundary, interrupting the run in
//              progress.
//   follow_up  read only when the run would otherwise settle, so it queues
//              behind the work rather than cutting into it.
//
// steer and follow_up are valid on an idle session too: the host opens a run
// for the text rather than queueing it against a run that will never come.
model AgentPromptMessage {
  cmd: "agent_prompt";
  id: string;
  text: string;
  mode?: string;
}

// Asks a conversation session's host for what an expanded tool card shows.
//
// A tool declaration carries a name, a one-line summary and the files it named
// — never the output, which is how a transcript balloons. The output is fetched
// when a card is opened, and the answer arrives as an `agent_event` of kind
// `tool_detail` addressed by the same `call_id`, so every client with that card
// open draws it.
//
// `full` asks for pi's untruncated output file instead of the clipped result it
// gave the model. It only means anything for a call whose declaration said
// `full_output`.
model AgentToolDetailMessage {
  cmd: "agent_tool_detail";
  id: string;
  call_id: string;
  full?: boolean;
}

// Drops everything a conversation session's agent has been sent and not yet
// read. The way out for the queue strip: a steer that has not landed yet, or a
// follow-up queued behind work the user no longer wants done.
//
// It clears both queues at once because that is the only shape pi offers — no
// per-entry removal exists — and the host answers with pi's own queue state, so
// the strip empties on the agent's word rather than optimistically.
model AgentClearQueueMessage {
  cmd: "agent_clear_queue";
  id: string;
}

// Asks a conversation session's host for the whole conversation as it stands.
//
// This is the conversation's half of the terminal restore contract. A terminal
// client attaches and gets the worker's VT dump, then dedups the live byte
// stream against `last_seq`; a conversation client sends this and gets a
// `conversation_snapshot` agent_event, then dedups the live envelope stream
// against the same `seq` spine. There is no result event and nothing to time
// out: the answer travels the ordinary envelope path, so every client watching
// the session lands on the same transcript.
//
// Sent by a client that has no picture of the session — a second window, a
// reloaded app, a pane mounted long after the host started. A client that is
// already following the stream has nothing to ask for.
model AgentAttachMessage {
  cmd: "agent_attach";
  id: string;
}

// Asks a conversation session's host for the page of transcript items older
// than the one the client is showing at the top — scroll-back.
//
// `before` is that item's key, in the host's own addressing ("message:<id>",
// "tool:<call_id>", "notice:<id>"), and the answer is a `conversation_page`
// agent_event echoing it. Like the snapshot and the tool detail, it is
// broadcast and addressed by the anchor rather than by a request id: a second
// window sitting at the same place in the conversation fills in for free, a
// window sitting somewhere else ignores it, and nothing can time out.
//
// A page the host cannot serve — an anchor it has never held, or history past
// its retention — answers empty with `has_more` false, which is how a client
// learns it has reached the start rather than being left waiting.
model AgentHistoryMessage {
  cmd: "agent_history";
  id: string;
  before: string;
}

// Switches the model a conversation session's agent runs on, from its next run.
//
// `model` is the "provider/model-id" pair attn pins launches with. The host
// applies it to the live agent and answers with a `model_changed` agent_event
// carrying the model actually in force — the one asked for, or the one still
// running plus the reason the switch was refused. The daemon reads that answer
// and rewrites the session's stored launch intent, so a later revive does not
// put the conversation back on the model the user switched away from.
model AgentSetModelMessage {
  cmd: "agent_set_model";
  id: string;
  `model`: string;
}

// Lists the conversations this machine has recorded, newest first, so one can be
// picked up in a new session.
//
// A conversation session's history is a pi session file under attn's data dir,
// and it outlives the attn session that wrote it — closing a session does not
// destroy what was said in it. This is how the user finds one again.
model ListPastConversationsMessage {
  cmd: "list_past_conversations";
  request_id: string;
}

// One recorded conversation, in the shape a picker draws.
//
// `preview` and `cwd` are read from the session file itself and are display
// only: resuming goes through pi's own reader, so a file this daemon could not
// summarize is still perfectly resumable and is listed with whatever it could
// read.
model PastConversation {
  // The attn session that recorded it. Still a live session when `live` is set.
  session_id: string;
  // pi's session file — what a spawn's `resume_conversation_file` names.
  file: string;
  // The directory the conversation ran in, from the session file's own header.
  cwd: string;
  // The first thing the user said in it, clipped. Empty when unreadable.
  preview: string;
  // Last write, epoch ms (int64 would codegen as string).
  modified: safeint;
  bytes: safeint;
  // An attn session is running on this conversation right now. Resuming is
  // still allowed — it copies the history rather than writing to this file —
  // but it means the copy is taken from a conversation still in motion.
  live: boolean;
}

// Result of list_past_conversations.
model PastConversationsResultMessage {
  event: "past_conversations_result";
  request_id: string;
  success: boolean;
  error?: string;
  conversations: PastConversation[];
  // More conversations exist than are listed; the newest ones are here.
  truncated: boolean;
}

// Asks for the durable event bus's operator picture: what the log holds, which
// fact classes write to it and how fast, which consumers read it and how far
// behind they are, and what is wrong with any of it.
//
// Answering costs one aggregate pass over the whole log, so this is asked on
// demand — when the page is opened and on a slow refresh while it is — never on
// a tick that runs whether or not anyone is looking.
model BusStatusGetMessage {
  cmd: "bus_status_get";
  request_id: string;
}

// One fact class's contribution to the log. The rates are means over the
// windows named in the result, already divided out.
model BusProducerStatus {
  // The dotted fact name, e.g. `session.state.changed`.
  name: string;
  events: safeint;
  // Event text these rows hold, not database pages.
  bytes: safeint;
  // Distinct subjects these events describe. Far fewer subjects than events
  // means a producer republishing the same entity rather than describing change.
  subjects: safeint;
  // Fraction of the log's rows, 0..1.
  share: float64;
  recent_per_hour: float64;
  baseline_per_hour: float64;
  sustained_per_hour: float64;
  // The rate crossed the tripwire on one of the sustained windows.
  surging: boolean;
  // Which window tripped, and its rate. Zero when not surging.
  surge_window_seconds: float64;
  surge_per_hour: float64;
}

// One durable consumer's registration, position, and lag.
model BusConsumerStatus {
  name: string;
  cursor: safeint;
  // Events between this cursor and the log head.
  lag: safeint;
  filter: string;
  enabled: boolean;
  // RFC3339; empty when the registration never recorded a write.
  updated_at: string;
  // This daemon owns a delivery loop for it. Meaningful only when the result's
  // `delivering` is set.
  live: boolean;
  // The current failure message when its handler is failing.
  stalled: string;
  // RFC3339 stamp of the oldest event it has not read; empty when caught up.
  // Lag counts events, this says how long they have waited.
  oldest_unread_at: string;
  // Its cursor is the floor retention stops at — it is pinning the log.
  holds_retention_floor: boolean;
  // That pin is past `pin_alarm_seconds`: no longer the system working, but an
  // outage holding the log open. The matching `retention_pinned` health entry
  // carries the sentence to render.
  pin_alarm: boolean;
  // What the backlog above its cursor weighs. Read only for a consumer whose
  // pin is alarming, so `pin_alarm` is what separates "holding nothing" from
  // "not measured".
  pinned_bytes: safeint;
}

// One thing wrong, said in a whole sentence. A client renders `message`; it does
// not re-derive the finding from the numbers. `kind` is for styling and
// filtering only.
model BusHealthEntry {
  // "warn" or "error".
  level: string;
  kind: string;
  // The consumer or fact name the finding is about.
  subject: string;
  message: string;
}

// Result of bus_status_get.
model BusStatusResultMessage {
  event: "bus_status_result";
  request_id: string;
  success: boolean;
  error?: string;
  earliest: safeint;
  head: safeint;
  rows: safeint;
  bytes: safeint;
  // RFC3339 bounds of the log in time; empty on an empty log.
  oldest_at: string;
  newest_at: string;
  // The snapshot came from the process that owns the delivery loops, which is
  // what makes each consumer's `live` field mean anything.
  delivering: boolean;
  retention_seconds: float64;
  recent_window_seconds: float64;
  baseline_window_seconds: float64;
  surge_rate_per_hour: float64;
  // How long an enabled consumer may pin the retention floor before the pin is
  // reported. Carried so a client shows the limit beside the observed value.
  pin_alarm_seconds: float64;
  producers: BusProducerStatus[];
  consumers: BusConsumerStatus[];
  health: BusHealthEntry[];
}

// Flips a durable consumer's kill switch, the same bit `attn bus enable|disable`
// writes. Disabling stops delivery and stops the consumer pinning retention; its
// cursor is preserved, and re-enabling resumes from wherever trimming left it.
model BusSetConsumerEnabledMessage {
  cmd: "bus_set_consumer_enabled";
  consumer: string;
  enabled: boolean;
  request_id: string;
}

// Result of bus_set_consumer_enabled.
model BusSetConsumerEnabledResultMessage {
  event: "bus_set_consumer_enabled_result";
  request_id: string;
  consumer: string;
  success: boolean;
  error?: string;
}

// xpixel/ypixel are the pane's TOTAL size in device pixels, the units
// TIOCGWINSZ's ws_xpixel/ws_ypixel and XTWINOPS speak, so image emitters can
// size an image to the space they were given. Absent or 0 means the client has
// no pixel geometry to report; the session then keeps the cell size it already
// derived rather than reporting a lie.
model PtyResizeMessage {
  cmd: "pty_resize";
  id: string;
  cols: int32;
  rows: int32;
  xpixel?: int32;
  ypixel?: int32;
}

// Reports whether the user can currently see this client, and whether what they
// are seeing is the dashboard. Fire-and-forget: no result event.
//
// Only the client knows window visibility, which view is rendered, and when
// input last happened; only the daemon decides what to do about it. So the
// client reports facts and never a rate or a tier — the policy that turns these
// into an activity-generation interval is the daemon's alone.
//
// It is a HEARTBEAT, not a latch: send it on every change AND periodically, or
// the daemon expires this client to "away". That asymmetry is deliberate. A
// client that crashes or is force-quit while the dashboard is up would otherwise
// pin generation on forever with nobody looking; expiry fails toward off, which
// is the safe direction, and the cost of a missed heartbeat is one interval of
// latency when the user comes back.
model SetClientPresenceMessage {
  cmd: "set_client_presence";
  visible: boolean; // The app window is on screen and not fully occluded.
  dashboard_visible: boolean; // ...and the view it is rendering is the dashboard.
  idle_seconds?: float64; // Seconds since the last keyboard or pointer input anywhere in this client. Absent means "no input observed yet this connection".
}

// ActivityStatusMessage asks what attn believes about session activity right
// now: the presence tier it has reduced its clients to, and every session's
// current line with the time it was generated. It is the CLI's way to see a
// feature whose whole output otherwise lives on the dashboard — including why
// nothing is being generated, which is almost always the tier.
model ActivityStatusMessage {
  cmd: "activity_status";
}

model ActivityStatusResult {
  presence_tier: string; // "watching" | "present" | "away"
  enabled: boolean;
  error?: string; // Why the feature cannot run — most often that no agent has been chosen.
  sessions: ActivityStatusSession[];
}

model ActivityStatusSession {
  id: string;
  label: string;
  activity?: string;
  activity_at?: string;
  // Why this session's last generation produced nothing. A run that keeps
  // failing is rate limited like a successful one, so without this a feature the
  // user paid to switch on goes quiet and looks identical to one with nothing to
  // say.
  error?: string;
}

// ClearSessionActivityMessage forgets one session's line. The way out for a line
// that is wrong on a session that will never write again — a session that is
// still moving replaces its own line on the next refresh, but a finished one
// would keep the wrong line forever. Clearing also drops the read cursor, so the
// next line is generated from what happens next rather than from a window that
// was already summarized.
model ClearSessionActivityMessage {
  cmd: "clear_session_activity";
  id: string;
}

// Pushes the app's resolved terminal theme colors to the daemon. Fire-and-forget:
// no result event.
model SetTerminalThemeMessage {
  cmd: "set_terminal_theme";
  foreground: string; // "#rrggbb"
  background: string; // "#rrggbb"
  cursor: string; // "#rrggbb"
  ansi_palette: string[]; // Exactly 16 "#rrggbb" entries, ANSI 0-15.
}

model KillSessionMessage {
  cmd: "kill_session";
  id: string;
  signal?: string; // "SIGTERM", "SIGINT", ...
}

// reload_session: kill and re-spawn a session's agent in place, daemon-owned.
// The daemon rebuilds launch flags from the worker registry or the stored
// launch intent; the client contributes only geometry (used when no live
// worker exists to read geometry from).
model ReloadSessionMessage {
  cmd: "reload_session";
  id: string;
  cols: int32;
  rows: int32;
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

  // Optional initial tile params for a freshly docked tile. When non-empty it
  // becomes the tile's params; when omitted/empty, a re-dock (move) preserves
  // the tile's existing params.
  tile_params?: string;
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
  // When present, rebind the tile's session binding (markdown tiles' Send
  // target). tile_params keeps its existing meaning; markdown tiles accept a
  // retarget-only update (their params stay immutable).
  tile_session_id?: string;
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
// `attn open` CLI over the unix socket and by the frontend over the websocket
// when the user cmd+clicks a markdown path in a session terminal. The tile is
// bound to session_id (the opener); when empty the daemon falls back to the
// currently selected session. request_id correlates the websocket reply
// (OpenMarkdownResultMessage) and is unused on the unix-socket path.
model OpenMarkdownMessage {
  cmd: "open_markdown";
  path: string;
  session_id?: string;
  request_id?: string;
}

// Result of a websocket open_markdown command. On success workspace_id and
// tile_id identify the docked (or reused) markdown tile.
model OpenMarkdownResultMessage {
  event: "open_markdown_result";
  request_id?: string;
  success: boolean;
  error?: string;
  path: string;
  workspace_id?: string;
  tile_id?: string;
}

// A resilient anchor into a markdown document: block-scoped offsets plus
// enough context (exact/prefix/suffix, content hash) to re-locate the text
// after the file changes. Mirrors the frontend anchoring core's AnchorRecord.
model MarkdownAnnotationAnchor {
  block_id: string;
  start_line: int32;
  end_line: int32;
  exact: string;
  prefix: string;
  suffix: string;
  start: int32;
  end: int32;
  content_hash: string;
}

// One markdown annotation draft entry. `anchor` is absent only for
// type "global" (document-wide comments). A quick-label annotation is a
// "comment" carrying quick_label_id (+ tip snapshotted at creation).
model MarkdownAnnotation {
  id: string;
  // "comment" | "deletion" | "global". Plain string, like PresentationRound's
  // verdict, so codegen yields string on both sides (a literal union becomes
  // anyOf -> Go interface{}).
  `type`: string;
  text?: string;
  anchor?: MarkdownAnnotationAnchor;
  quick_label_id?: string;
  quick_label_tip?: string;
  // Display text of the quick label snapshotted at creation, e.g.
  // "👍 Looks good" (emoji + label text). Optional: older drafts lack it;
  // the payload formatter falls back to quick_label_id.
  quick_label_text?: string;
  created_at: safeint; // epoch ms (int64 would codegen as string)
}

// One assistant message from a session's transcript, as the markdown it was
// written in — which is what annotations anchor their offsets to.
model SessionMessage {
  // Stable identity of this message. The transcript entry's own id when the
  // agent's format carries one, otherwise a content hash. Annotations are
  // persisted against it, so it must survive a re-read of the transcript.
  key: string;
  markdown: string;
}

// Fetch the recent assistant messages of a session, so any of them can be
// annotated in place in the terminal — not just the newest, because a turn
// scrolling past is not a reason to lose what the user marked on it. Read-only:
// it resolves the session's transcript and reads it, touching no PTY and no
// session state.
model SessionMessagesGetMessage {
  cmd: "session_messages_get";
  session_id: string;
  request_id: string;
}

// Result of session_messages_get.
model SessionMessagesGetResultMessage {
  event: "session_messages_get_result";
  request_id: string;
  session_id: string;
  success: boolean;
  error?: string;
  // Oldest first, newest last. Empty when the transcript holds no annotatable
  // prose (structured verdicts, or pure tool activity); that is a success, and
  // the client is expected to say so rather than offer an empty surface.
  messages: SessionMessage[];
  // Messages were left out of the window: older ones beyond the count/byte
  // budget, or one too large to hand back with offsets that still mean
  // anything. The daemon logs which limit was hit and by how much.
  truncated: boolean;
}

// One persisted terminal annotation. Offsets address `message_key`'s markdown,
// so an annotation outlives the turn scrolling away, the pane unmounting, and
// the app restarting.
model SessionAnnotation {
  // Client-generated and stable for the life of the annotation: it is what a
  // later save addresses when the comment or label changes.
  id: string;
  message_key: string;
  start: int32;
  end: int32;
  // The markdown the offsets covered when the annotation was made. Kept so the
  // panel can list it, and the draft can be submitted, without the message.
  quote: string;
  emoji: string;
  comment: string;
}

// Fetch a session's persisted annotations. The reply's generation is the
// current floor — max(stored generation, tombstone) — so a re-mounting client
// seeds its counter past any tombstone even when the list is empty.
model SessionAnnotationsGetMessage {
  cmd: "session_annotations_get";
  session_id: string;
  request_id: string;
}

// Result of session_annotations_get.
model SessionAnnotationsGetResultMessage {
  event: "session_annotations_get_result";
  request_id: string;
  session_id: string;
  success: boolean;
  error?: string;
  annotations: SessionAnnotation[];
  // The draft's note: what the user wants to say about the turn as a whole,
  // alongside the marks on its parts. Empty when there is none.
  note?: string;
  generation: int32;
}

// Persist the full annotation list for a session, and the note that goes with
// it. Whole-draft rather than per-annotation edits: the list is small, one
// writer owns it, and a full save cannot leave the store holding half of an
// edit.
model SessionAnnotationsSaveMessage {
  cmd: "session_annotations_save";
  session_id: string;
  annotations: SessionAnnotation[];
  // Saved with the marks and cleared with them, because it is one thing the
  // user is composing and sends in one keystroke.
  note?: string;
  generation: int32;
  request_id: string;
}

// Result of session_annotations_save.
model SessionAnnotationsSaveResultMessage {
  event: "session_annotations_save_result";
  request_id: string;
  session_id: string;
  success: boolean;
  error?: string;
  // The save carried a generation at or below the stored one or the tombstone.
  // Benign: the client drops what it was holding and re-hydrates.
  stale?: boolean;
  generation: int32;
}

// Tombstone a session's annotations — what sending them to the agent does.
// Idempotent, and works on a session that never had a row.
model SessionAnnotationsClearMessage {
  cmd: "session_annotations_clear";
  session_id: string;
  generation: int32;
  request_id: string;
}

// Result of session_annotations_clear.
model SessionAnnotationsClearResultMessage {
  event: "session_annotations_clear_result";
  request_id: string;
  session_id: string;
  success: boolean;
  error?: string;
  generation: int32;
}

// Deliver composed annotation feedback into a session — the same doorbell
// primitive markdown annotations submit through: bracketed paste, a measured
// gap, then Enter.
//
// The text is composed client-side rather than from the persisted draft: a
// quick label's instruction is UI vocabulary that belongs with the chip that
// offers it, and the client can already write these exact bytes with
// pty_input. What the daemon owns here is the delivery — the approval guard,
// the PTY write fence that keeps a racing keystroke out of the gap, and the
// gap itself, without which the agent folds Enter into the pasted text and
// nothing is ever submitted.
model SessionAnnotationsSubmitMessage {
  cmd: "session_annotations_submit";
  session_id: string;
  // The composed feedback, delivered as one pasted block.
  text: string;
  request_id: string;
}

// Result of session_annotations_submit.
// status: "delivered" — typed into the session and submitted.
//         "skipped_pending_approval" — the session is waiting on an approval
//           prompt, where the submit's Enter would answer it. Nothing was
//           typed; the annotations are kept so the send can be retried once
//           the prompt is resolved.
//         "error" — see error; annotations kept.
model SessionAnnotationsSubmitResultMessage {
  event: "session_annotations_submit_result";
  request_id: string;
  session_id: string;
  success: boolean;
  status: string;
  error?: string;
}

// Fetch the persisted annotation draft for an absolute markdown file path.
// The reply's generation is the current generation floor —
// max(stored generation, tombstone) — so a re-mounting client seeds its
// counter past any tombstone even when the draft is empty.
model MarkdownAnnotationsGetMessage {
  cmd: "markdown_annotations_get";
  path: string;
  // Owning workspace of the requesting markdown tile. On a hub this routes
  // the command to the endpoint that owns the workspace, so drafts live on
  // (and are keyed by) the daemon that owns the document — identical paths
  // on different endpoints never share a draft/generation/tombstone row.
  workspace_id: string;
  request_id: string;
}

// Result of markdown_annotations_get. annotations may be empty; generation is
// the floor the client must exceed on its next save.
model MarkdownAnnotationsGetResultMessage {
  event: "markdown_annotations_get_result";
  request_id: string;
  // Echo of the request's workspace_id (client correlation key).
  workspace_id: string;
  success: boolean;
  error?: string;
  path: string;
  annotations: MarkdownAnnotation[];
  generation: int32;
}

// Persist the full annotation list for a path (debounced full-list save, not
// per-op CRUD). generation must be strictly greater than both the stored
// generation and the tombstone generation or the save is rejected as stale.
model MarkdownAnnotationsSaveMessage {
  cmd: "markdown_annotations_save";
  path: string;
  // See MarkdownAnnotationsGetMessage.workspace_id (hub endpoint routing).
  workspace_id: string;
  annotations: MarkdownAnnotation[];
  generation: int32;
  request_id: string;
}

// Result of markdown_annotations_save. stale=true (with success=false) means
// the save lost to a tombstone or a newer generation — the client should drop
// its pending list and re-hydrate; it is benign, not an error.
model MarkdownAnnotationsSaveResultMessage {
  event: "markdown_annotations_save_result";
  request_id: string;
  // Echo of the request's workspace_id (client correlation key).
  workspace_id: string;
  success: boolean;
  stale?: boolean;
  error?: string;
  path: string;
  generation: int32;
}

// Tombstone the draft for a path at the given generation: empties the list and
// rejects any later save carrying generation <= tombstone (stops a debounced
// save that fires after clear from resurrecting ghost drafts). Idempotent.
model MarkdownAnnotationsClearMessage {
  cmd: "markdown_annotations_clear";
  path: string;
  // See MarkdownAnnotationsGetMessage.workspace_id (hub endpoint routing).
  workspace_id: string;
  generation: int32;
  request_id: string;
}

// Result of markdown_annotations_clear; generation is the new floor.
model MarkdownAnnotationsClearResultMessage {
  event: "markdown_annotations_clear_result";
  request_id: string;
  // Echo of the request's workspace_id (client correlation key).
  workspace_id: string;
  success: boolean;
  error?: string;
  path: string;
  generation: int32;
}

// Format the persisted annotation draft for `path` into the feedback payload
// and deliver it into target_session_id's PTY (bracketed paste + Enter).
// Drafts are tombstone-cleared ONLY on successful delivery.
model MarkdownAnnotationsSubmitMessage {
  cmd: "markdown_annotations_submit";
  path: string;
  target_session_id: string;
  // Annotation ids the client currently shows as orphaned (anchor no longer
  // resolves). Orphanhood is a client-derived, non-persisted property; the
  // formatter labels these "(~line N, moved)".
  orphaned_ids?: string[];
  request_id: string;
}

// Result of markdown_annotations_submit.
// status: "delivered" — payload typed into the session; drafts cleared.
//         "skipped_pending_approval" — target is waiting on an approval
//           prompt; nothing was typed, drafts kept.
//         "error" — see error; drafts kept.
// generation: new generation floor after the clear (delivered only) so the
// client can seed its counter without a round-trip.
model MarkdownAnnotationsSubmitResultMessage {
  event: "markdown_annotations_submit_result";
  request_id: string;
  path: string;
  target_session_id: string;
  success: boolean;
  status: string;
  error?: string;
  generation?: int32;
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

// Pin or clear a per-session context-window cap (tokens). cap > 0 pins; cap 0
// clears the pin so the launch falls back to the chief or per-agent default
// settings. A changed pin on a live claude/codex session reloads the agent in
// place (resume-preserving) so the cap takes effect immediately.
model SetSessionContextWindowCapMessage {
  cmd: "set_session_context_window_cap";
  session_id: string;
  cap: int32;
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
  /**
   * The daemon id that owns this daemon's home-level state. Equal to
   * daemon_instance_id on a home daemon; a different id means this daemon is an
   * outpost of that home.
   */
  home_daemon_id?: string;
  sessions?: Session[];
  endpoints?: EndpointInfo[];
  workspaces?: Workspace[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  github_hosts?: string[];
  settings?: Record<string>;
  warnings?: DaemonWarning[];
  tickets?: TicketRow[];
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

// Reply to set_session_context_window_cap, correlated by session_id. cap
// echoes the pin that is now stored (0 = cleared). The updated session is also
// broadcast separately via sessions_updated.
model SessionContextWindowCapResultMessage {
  event: "session_context_window_cap_result";
  session_id: string;
  cap: int32;
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

// TicketsUpdatedMessage is the live board feed: the non-archived ticket rows,
// re-pushed whenever any ticket mutates. It is the same shape carried in
// InitialStateMessage.tickets, so a client renders the board identically from
// the bootstrap snapshot and from each update.
model TicketsUpdatedMessage {
  event: "tickets_updated";
  tickets: TicketRow[];
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

// GetPresentationsMessage reads the full list of presentations, the way the
// app renders the present surface.
model GetPresentationsMessage {
  cmd: "get_presentations";
}

model GetPresentationsResultMessage {
  event: "get_presentations_result";
  success: boolean;
  error?: string;
  presentations: Presentation[];
}

// GetPresentationRoundMessage fetches one round of a presentation — the round
// record plus its comments — for the detail view. seq omitted/0 means the
// latest round.
model GetPresentationRoundMessage {
  cmd: "get_presentation_round";
  presentation_id: string;
  seq?: int32;
}

model GetPresentationRoundResultMessage {
  event: "get_presentation_round_result";
  success: boolean;
  error?: string;
  presentation?: Presentation;
  round?: PresentationRound;
  comments?: PresentationComment[];
  repo_head_sha?: string; // Current `git rev-parse HEAD` of the presentation's repo_path at query time, for drift detection; omitted if the rev-parse fails (e.g. repo gone)
}

// PresentCommentInput is a reviewer comment as submitted from the app, before
// the daemon assigns it an id and author.
model PresentCommentInput {
  filepath: string;
  line_start: int32;
  line_end: int32;
  side: string;
  content: string;
}

// PresentSubmitRoundMessage hands a round's review back to the authoring
// agent. handback marks the round submitted, closing the loop for that round.
// verdict is "approved" | "feedback" — plain string, see PresentationComment.side.
model PresentSubmitRoundMessage {
  cmd: "present_submit_round";
  round_id: string;
  verdict: string;
  comments: PresentCommentInput[];
  handback: boolean;
}

model PresentSubmitRoundResultMessage {
  event: "present_submit_round_result";
  success: boolean;
  error?: string;
  round_id: string;
}

// PresentCloseMessage dismisses a presentation without a review: the
// presentation's status moves straight to "closed", with no round submission
// and no handback to the authoring agent.
model PresentCloseMessage {
  cmd: "present_close";
  presentation_id: string;
}

model PresentCloseResultMessage {
  event: "present_close_result";
  success: boolean;
  error?: string;
  presentation_id: string;
}

model PresentationAddedMessage {
  event: "presentation_added";
  presentation: Presentation;
}

model PresentationUpdatedMessage {
  event: "presentation_updated";
  presentation: Presentation;
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
  request_id?: string; // Client-chosen correlation id, echoed verbatim from the request
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

// Compact automations summaries for the WS surface. Canonical state (full
// SpecJSON, occurrence payloads, snapshots) stays server-side in SQLite; these
// carry only what the UI renders. AutomationDefinitionSummary's schedule
// fields are extracted daemon-side from the definition's SpecJSON. last_run is
// the definition's single most-recent run (Store.LatestAutomationRunPerDefinition),
// embedded so the panel's list badge never needs a per-definition runs fetch.
model AutomationDefinitionSummary {
  id: string;
  name: string;
  enabled: boolean;
  revision: int32;
  trigger_type: string;
  schedule_cron?: string;
  schedule_time_zone?: string;
  updated_at: string;
  last_run?: AutomationRunSummary;
}

model AutomationRunSummary {
  id: string;
  definition_id: string;
  state: string;
  last_error?: string;
  cancel_reason?: string;
  ticket_id?: string;
  session_id?: string;
  pane_id?: string;
  created_at: string;
  updated_at: string;
  delivered_at?: string;
  occurrence_key?: string;
}

// Per-action result messages for the automations surface (socket + WS share
// one command set — see the commands above). Each carries the base
// request_id/success/error triple plus that action's own typed payload,
// replacing the old shared AutomationActionResultMessage grab-bag and the
// unix-socket's separate generic-`data` automationActionResult struct: both
// transports now emit the same typed shape per action.

model AutomationApplyResultMessage {
  event: "automation_apply_result";
  request_id?: string;
  success: boolean;
  error?: string;
  definition?: AutomationDefinitionSummary;
  // spec_yaml: the canonical rendered YAML of the saved spec
  // (automationDefinitionYAML), not an echo of the input.
  spec_yaml?: string;
  // error_code: machine-readable failure class for the app form. One of
  // "revision_conflict" | "id_collision" | "deleted_elsewhere" |
  // "id_mismatch" | "validation"; absent on success and on failures with no
  // specific class.
  error_code?: string;
}

model AutomationValidateResultMessage {
  event: "automation_validate_result";
  request_id?: string;
  success: boolean;
  error?: string;
}

model AutomationDefinitionsResultMessage {
  event: "automation_definitions_result";
  request_id?: string;
  success: boolean;
  error?: string;
  definitions?: AutomationDefinitionSummary[];
}

// definition_id "" returns the starter template YAML (automation.StarterTemplateYAML)
// with no definition — the client treats a missing definition as revision 0 /
// new, so create and edit share one frontend code path.
model AutomationDefinitionResultMessage {
  event: "automation_definition_result";
  request_id?: string;
  success: boolean;
  error?: string;
  definition?: AutomationDefinitionSummary;
  spec_yaml?: string;
  // spec_json: canonical JSON encoding of the definition spec (same
  // canonical bytes family the store persists), present on success including
  // the definition_id "" starter-template case; the app form edits
  // structured spec and never parses spec_yaml.
  spec_json?: string;
}

model AutomationRunsResultMessage {
  event: "automation_runs_result";
  request_id?: string;
  success: boolean;
  error?: string;
  definition_id: string;
  runs?: AutomationRunSummary[];
  truncated?: boolean;
}

model AutomationRunResultMessage {
  event: "automation_run_result";
  request_id?: string;
  success: boolean;
  error?: string;
  run?: AutomationRunSummary;
}

model AutomationSetEnabledResultMessage {
  event: "automation_set_enabled_result";
  request_id?: string;
  success: boolean;
  error?: string;
  definition?: AutomationDefinitionSummary;
}

model AutomationDeleteResultMessage {
  event: "automation_delete_result";
  request_id?: string;
  success: boolean;
  error?: string;
}

// cleaned/kept_dirty/kept_active: automation_cleanup's three-way per-run-id
// partition of the terminal runs it examined — cleaned had their worktree
// removed; kept_dirty were skipped because the worktree had uncommitted
// changes; kept_active were skipped because the run's thread is still in use
// (its session is live, or its session id is still referenced by a
// continuity binding even though the session row is gone) — a bound
// continuity thread shares its worktree with its whole thread, so an old
// terminal run's worktree may still be the thread's live working directory.
// One bucket for both live-session and bound-thread cases, deliberately: the
// user-facing story is identical, "still in use, not reclaimable" — only the
// daemon log distinguishes them.
model AutomationCleanupResultMessage {
  event: "automation_cleanup_result";
  request_id?: string;
  success: boolean;
  error?: string;
  cleaned?: string[];
  kept_dirty?: string[];
  kept_active?: string[];
}

// Compact change signal: canonical state lives in SQLite, so clients re-read
// (automation_definitions_get / automation_runs_get) on receipt rather than
// trusting a broadcast payload.
model AutomationsChangedMessage {
  event: "automations_changed";
  definition_ids: string[];
}

// Reviewer agent streaming events
model SpawnResultMessage {
  event: "spawn_result";
  id: string;
  success: boolean;
  error?: string;
}

model ReloadSessionResultMessage {
  event: "reload_session_result";
  id: string;
  success: boolean;
  error?: string;
}

// One OSC 133 shell-integration command block resolved by the worker, carried
// beside the VT dump so restore reconstructs clickable prompt/command/output
// regions (the dump has rendered cells, not the marks). Rows are SCREEN-space
// rows of vt_dump_b64, which equal client buffer rows after the dump is written
// into a fresh same-size terminal. See Phase 3a in
// docs/plans/2026-07-22-server-authoritative-terminal.md.
model AttachBlock {
  id: safeint;                   // server-assigned, monotonic per session
  pending: boolean;              // the single open block (no command-end yet)
  prompt_row: int32;
  input_row?: int32;
  input_col?: int32;
  output_start_row?: int32;
  end_row?: int32;               // exclusive; absent while pending
  command?: string;
  exit_code?: int32;
}

// One kitty image placed on the worker's active screen, as ghostty resolved it
// at observation time. The worker is the only kitty parser in the system — APC
// bytes never reach a client — so this description is the only way a client
// learns an image exists.
//
// Geometry is the worker's, and the client's grid equals it, so viewport_row /
// viewport_col are screen-relative and a client maps them by adding its own
// scrollback length at apply time.
//
// grid_cols / grid_rows are ADVISORY and are 0 on a freshly placed image: kitty
// calls that "natural size", and ghostty resolves a cell footprint only when
// something reflows the placement. The wire carries what ghostty resolved and
// never a synthesized count, so a client sizes from pixel_width / pixel_height
// and treats the grid fields as a hint.
model KittyPlacement {
  image_id: safeint;
  placement_id: safeint;
  image_generation: safeint;  // changes on any retransmission of image_id
  virtual: boolean;           // unicode-placeholder placement; no cursor geometry
  z: int32;
  viewport_row: int32;        // top-left, screen-relative; negative = scrolled up
  viewport_col: int32;
  viewport_visible: boolean;  // false: fully off-screen or virtual
  grid_cols: int32;           // advisory; 0 until a reflow resolves it
  grid_rows: int32;
  pixel_width: int32;         // rendered size (source rect + aspect applied)
  pixel_height: int32;
  source_x: int32;            // resolved source rect in image pixels
  source_y: int32;
  source_width: int32;
  source_height: int32;
}

// Server-authoritative restore snapshot: a self-contained VT serialization of
// the worker's parsed terminal (grid + scrollback + cursor + modes, both
// primary and alternate screens) produced by libghostty-vt. Present on every
// restore attach; the client resets its model and replays vt_dump_b64, then
// seeds its command-block store from blocks.
model AttachSnapshot {
  cols: int32;
  rows: int32;
  vt_dump_b64: string;           // base64 self-contained VT stream
  blocks?: AttachBlock[];        // OSC 133 command blocks, empty/absent if none
  // Kitty placements on the screen vt_dump_b64 serializes, captured in the same
  // hold. Absent when the session holds no images. Pixels are not inlined; the
  // client pulls what its cache lacks with get_kitty_image.
  placements?: KittyPlacement[];
  scrollback_truncated: boolean; // ghostty dropped scrollback at its cap
}

model AttachResultMessage {
  event: "attach_result";
  id: string;
  success: boolean;
  // The server-authoritative restore. Present whenever the attach restores prior
  // terminal state; the client resets its model and replays snapshot.vt_dump_b64.
  // Absent on a fresh spawn or when no parsed terminal is available.
  snapshot?: AttachSnapshot;
  last_seq?: int32;
  cols?: int32;
  rows?: int32;
  pid?: int32;
  running?: boolean;
  // session was respawned by this attach (revive policy)
  revived?: boolean;
  error?: string;
}

model GetScreenSnapshotResultMessage {
  event: "get_screen_snapshot_result";
  id: string;
  success: boolean;
  screen_snapshot?: string; // base64 ANSI replay of the visible frame
  screen_rows?: int32;
  screen_cols?: int32;
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

// One event from a conversation session's headless host. Two streams share one
// envelope and one `seq` spine, so a render delta and the semantic event that
// brackets it stay ordered against each other.
//
// `kind` is either SEMANTIC — attn's own vocabulary, which the daemon reads and
// features integrate on (`session_ready`, `run_started`, `run_settled`) — or
// RENDER, which exists only so the app can paint (`message_start`,
// `message_delta`, `message_end`, `queue_update`). The daemon forwards render
// bodies without reading them: their shape is agreed between the host and the
// app and is versioned by the plugin, not by ProtocolVersion. Do not add daemon
// behavior keyed on a render kind; if the daemon needs to know something, it
// needs a semantic kind.
//
// Every semantic body carries a `state` in attn's session vocabulary — the
// state that declaration puts the session in. The host is attn code inside the
// agent's own loop, so a conversation session's state is declared rather than
// classified or scraped, and the envelope's `seq` is the ordering cursor the
// daemon applies it under.
//
// `body` is therefore untyped here on purpose. A client that meets an unknown
// kind ignores it — the host's kind set grows with the plugin.
model AgentEventMessage {
  event: "agent_event";
  id: string;
  seq: int32;
  kind: string;
  body: Record<unknown>;
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

// The session's WHOLE kitty placement set as of the chunk stamped seq, sent
// only to clients that advertised the "kitty_images" capability. Rides the same
// per-session path as pty_output and arrives after the bytes of the same seq,
// because the positions were measured on the grid those bytes produce.
//
// placements is REQUIRED and may be empty. The empty set is the one message
// that tells a client the last image is gone; a client that treats an absent
// array as "nothing to do" keeps drawing a ghost forever.
//
// Sets are replaced wholesale, never merged. A client applies a set whose seq
// is >= the last it applied — a resize re-describes at the replay watermark
// rather than a fresh seq, so equal seqs happen and both are truthful.
model KittyPlacementsMessage {
  event: "kitty_placements";
  id: string;
  seq: int32;
  placements: KittyPlacement[];
}

// Answer to get_kitty_image: one stored image's RAW PIXELS in format's layout
// (ghostty decodes PNG and inflates zlib before storing, so there is no
// compressed form to pass through).
//
// This JSON form serves clients WITHOUT "binary_pty_output" — automation
// clients and the hub's daemon-to-daemon relay, which is a text pipe — so they
// can still assert on an image. Clients advertising it get the same pixels as
// binary frame 0x02 instead, skipping base64. Note this is the transport bit,
// not "kitty_images": whether a client is TOLD about images and whether it can
// decode a frame are separate questions.
//
// A FAILURE has no pixels and so has no frame: success=false always arrives
// here, on both kinds of client. A binary-capable client must handle both
// shapes.
//
// Correlation is by content key (id, image_id, generation), never a request id:
// a cache fill is last-writer-wins per key, and two answers for the same key
// carry the same pixels.
//
// success=false is an ordinary answer, not a broken session: the image was
// evicted at the storage limit, deleted by the program, or never transmitted.
// error names the image id and the client drops that placement's render.
model KittyImageResultMessage {
  event: "kitty_image_result";
  id: string;
  image_id: safeint;
  success: boolean;
  generation?: safeint;
  width?: int32;
  height?: int32;
  format?: string;    // "rgb" | "rgba" | "gray_alpha" | "gray"
  data_b64?: string;  // base64 raw pixels, present on success
  error?: string;
}

// runtime_respawned: the daemon killed and re-spawned a session's agent process
// in place (e.g. chief-of-staff assign/demote reload). The session id is
// unchanged and the transcript is resumed; the client must tear down its dead PTY
// stream and reattach to the new runtime. Distinct from session_exited (which
// drops the session to a dead pane) — the runtime was replaced, not closed.
model RuntimeRespawnedMessage {
  event: "runtime_respawned";
  id: string;
}

// Echoes the geometry the resizing client sent, pixels included, so the other
// attached clients converge on the same frame. Same unit and same "0 means
// unknown" rule as PtyResizeMessage.
model PtyResizedMessage {
  event: "pty_resized";
  id: string;
  cols: integer;
  rows: integer;
  xpixel?: int32;
  ypixel?: int32;
}

model CommandErrorMessage {
  event: "command_error";
  cmd?: string;
  success: boolean;
  error: string;
}

// Sent once to a reconnecting client that the hub had hung up on for falling
// behind, keyed on the client_id it sent with its hello. The close status that
// explains an eviction travels the same TCP stream as the backlog that caused
// it, so on the link where it matters it arrives long after the disconnect —
// this is how the reason reaches the client instead.
model ClientEvictionNoticeMessage {
  event: "client_eviction_notice";
  evicted_at: string;          // RFC3339, when the hub dropped the connection
  reason: string;              // the close reason the client could not read, e.g. "client too slow"
  undelivered_messages: int32; // messages the daemon gave up on handing over
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
// Document store
// ============================================================================
//
// The document store's IPC surface. The query's STRUCTURE is typed here and is
// as rigid as the rest of the protocol; only a filter's bound travels as JSON
// text, because it is the one genuinely polymorphic leaf (a string, a number, or
// a boolean, decided by the field's declared type). Typing it as a union here
// would still not express the rule that matters — a bound must match the type
// the collection declared for its field — which is checked in internal/docstore
// where the declaration lives.

// One queryable field of a collection. `type` is string, number or bool.
model DocumentFieldSpec {
  "name": string;
  "type": string;
}

// A collection's declaration: which fields may be filtered and sorted on. It is
// a query contract, not a storage schema — document bodies are arbitrary JSON
// and an undeclared field is stored and read back untouched.
model DocumentCollectionSchema {
  "namespace": string;
  "collection": string;
  "fields": DocumentFieldSpec[];
}

// One comparison. `op` is eq, lt, lte, gt or gte. `value_json` is the bound
// encoded as JSON ("\"pending\"", "5", "true").
model DocumentFilter {
  "field": string;
  "op": string;
  "value_json": string;
}

model DocumentSort {
  "field": string;
  "desc"?: boolean;
}

// A query. `created_at` and `updated_at` are always available to filter and sort
// on; every other field must be declared. An absent limit means the store's
// default rather than "unbounded".
model DocumentQuery {
  "namespace": string;
  "collection": string;
  "filters"?: DocumentFilter[];
  "sort"?: DocumentSort;
  "limit"?: int32;
  // The id of the last document of the previous page. The visible order is
  // (sort field, id), and a filter can only constrain one of those, so a tie on
  // the sort field makes range-filter paging skip or repeat. This compares
  // against the whole tuple.
  "after"?: string;
}

// One stored document. `body` is the document's JSON as written, byte for byte.
//
// `rev` counts writes to this document, starting at 1. Hand it back as a put's
// or delete's `expected_rev` to make the write conditional on nobody having
// changed the document since this read. safeint rather than int32 because int32
// is only about two billion writes to one document, and a wrapped revision would
// make a stale check pass — the exact failure it guards against.
model StoredDocument {
  "id": string;
  "body": string;
  "rev": safeint;
  "created_at": string;
  "updated_at": string;
}

// Declare a collection, replacing any previous declaration. Redeclaring is how a
// collection gains a queryable field; documents are never rewritten.
model DocDefineMessage {
  "cmd": "doc_define";
  "schema": DocumentCollectionSchema;
}

model DocDefineResult {
  "namespace": string;
  "collection": string;
}

// Remove a collection's declaration and every document under it.
model DocUndefineMessage {
  "cmd": "doc_undefine";
  "namespace": string;
  "collection": string;
}

model DocUndefineResult {
  "namespace": string;
  "collection": string;
  "documents_removed": int32;
}

// Every declared collection — the operator's index of what exists.
model DocCollectionsMessage {
  "cmd": "doc_collections";
}

model DocCollectionsResult {
  "collections": DocumentCollectionSchema[];
}

// Write a document, creating or fully replacing it. `body` is JSON object text.
//
// `expected_rev` is the caller's assertion about what it is overwriting: absent
// writes unconditionally, 0 writes only if the document does not exist yet, and
// any other value writes only if the document is still at that revision. A
// failed assertion writes nothing and answers with an error naming both
// revisions. Revisions start at 1, which is what makes 0 unambiguous as "must
// not exist".
model DocPutMessage {
  "cmd": "doc_put";
  "namespace": string;
  "collection": string;
  "id": string;
  "body": string;
  "expected_rev"?: safeint;
}

// `rev` is the revision the document now has, so a create-then-update sequence
// needs no read in between.
//
// `seq` is the write's POSITION: the document write and the fact describing it
// are one commit, and this is where that fact landed on the durable log. Compare
// it against a later read's `as_of_seq` to know whether that read includes this
// write. It is an opaque monotone position — never a row id, never dense, never
// contiguous — and clients only store and compare it.
model DocPutResult {
  "namespace": string;
  "collection": string;
  "id": string;
  "rev": safeint;
  "seq": safeint;
}

model DocGetMessage {
  "cmd": "doc_get";
  "namespace": string;
  "collection": string;
  "id": string;
}

// `found` separates an absent document from a failed read; `document` is present
// only when found.
//
// `as_of_seq` is the log position this answer was true at, read in the same
// transaction as the document. 0 means "before everything" — a valid position on
// a log that has never been written to, not a missing answer.
model DocGetResult {
  "found": boolean;
  "document"?: StoredDocument;
  "as_of_seq": safeint;
}

// `expected_rev` removes the document only if it is still at that revision —
// deleting on the strength of a body you read is the same lost-update hazard as
// overwriting one. Absent deletes unconditionally. Unlike a put, 0 is refused:
// "delete this if it does not exist" is not an assertion a delete can act on.
model DocDeleteMessage {
  "cmd": "doc_delete";
  "namespace": string;
  "collection": string;
  "id": string;
  "expected_rev"?: safeint;
}

// `existed` is false when the delete removed nothing, so a caller does not
// report a change that did not happen.
//
// `seq` is the removal's position on the durable log, as DocPutResult describes.
// It is 0 when `existed` is false: nothing changed, so nothing was announced and
// there is no position to report.
model DocDeleteResult {
  "namespace": string;
  "collection": string;
  "id": string;
  "existed": boolean;
  "seq": safeint;
}

model DocQueryMessage {
  "cmd": "doc_query";
  "query": DocumentQuery;
}

// `as_of_seq` is the log position these documents were true at, read in the same
// transaction as the rows themselves.
model DocQueryResult {
  "documents": StoredDocument[];
  "as_of_seq": safeint;
}

// Count the documents a query matches, without fetching their bodies. The
// filters (and the after cursor, if any) mean exactly what they mean for
// doc_query; sort and limit are ignored, because they decide which matches come
// back and never how many there are.
model DocCountMessage {
  "cmd": "doc_count";
  "query": DocumentQuery;
}

model DocCountResult {
  "count": int32;
  "as_of_seq": safeint;
}

// The revisions a refused write disagreed about. `expected` is what the caller
// asserted (0 meaning "must not exist"); `found` reports whether a document was
// there at all when the store looked, and `actual` is its revision, meaningless
// when `found` is false. Together they say which write this one lost to.
model DocumentConflict {
  "namespace": string;
  "collection": string;
  "id": string;
  "expected": safeint;
  "found": boolean;
  "actual": safeint;
}

// One document a resuming subscriber already holds, and at which revision.
//
// A pair list rather than a map because the generated Go types erase a record's
// value type to `any`, and a revision that decodes as an untyped number is
// exactly the value this resume must not get wrong.
model DocumentRevision {
  "id": string;
  "rev": safeint;
}

// Subscribe to a live query. Unlike every other command here, this one keeps the
// connection open: the daemon writes a DocSubscribeResult immediately carrying
// the query's current window, then writes a fresh one every time a write to the
// collection could have changed it. The subscription ends when the caller closes
// the connection, or when the collection stops being able to answer the query.
//
// `have` is what the subscriber already holds, and is how a reconnecting client
// resumes without replaying history: the first delivery then carries only the
// window members whose revision differs from what is declared here. Everything
// else about that delivery is unchanged, so a fresh subscribe is simply the case
// where `have` is empty.
//
// A query carrying `after` is REFUSED. A live query is a window and a cursor is
// a walk: the anchor moves under a subscription, so the page it names is not the
// page it named a write ago. Use `limit` and let each delivery carry the current
// window. One-shot doc_query keeps `after` exactly as it is, because a single
// read has one instant and nothing to move under it.
model DocSubscribeMessage {
  "cmd": "doc_subscribe";
  "query": DocumentQuery;
  "have"?: DocumentRevision[];
}

// One delivery of a live query's window, and the whole client contract is one
// rule:
//
//   Render `order`. Take each body from `upsert` if it is there, else from your
//   cache. Forget every cached document not named in `order`.
//
// `order` is the query's current result, in the server's own order — the client
// never re-derives sort position, because document order comes from SQLite's
// comparison under column affinity and a client re-sorting locally would have to
// reimplement that exactly. `upsert` carries only the bodies the subscriber does
// not already hold: the ones it declared at a different revision in `have`, and
// the ones that have changed since a previous delivery on this connection.
// There is no `remove` field because removal is derivable from `order`, and a
// derivable field is a field two implementations can disagree about.
//
// The daemon upholds — and a test pins — that every id in `order` is either in
// `upsert`, or was declared in `have` at its current revision, or arrived in an
// earlier delivery on this connection. A client that finds a body it does not
// hold must treat the subscription as broken and resubscribe with no `have`.
//
// `as_of_seq` is the log position this window was true at, read in the same
// transaction as the rows. `delivery` counts deliveries on this subscription,
// starting at 1; it is named for what it counts because a document carries its
// own `rev` and one word for both would read as though they were related. int32
// rather than safeint because the counter is bounded by how long one connection
// stays open, not by a document's whole history.
model DocSubscribeResult {
  "delivery": int32;
  "as_of_seq": safeint;
  "order": string[];
  "upsert": StoredDocument[];
}

// ============================================================================
// App registry
// ============================================================================
//
// An app is a manifest-declared automation running in attn's shared runtime: it
// consumes facts from the durable bus as its own consumer `app:<name>`, keeps
// state in the document namespace `app/<name>`, and is versioned by the content
// hash of its built artifact. A plugin is the other thing entirely — an
// integration with its own process — and the two never share a surface.
//
// Scaffolding an app is a local act with no daemon in it — `attn app new` writes
// a directory. Building is the caller's too: it runs the developer's bun and the
// TypeScript attn keeps under its data dir, and only the finished result crosses
// this surface. What the daemon owns is the part that must be atomic and
// observed — recording the version, moving the pointer, publishing the fact.

// One app as `attn app list` shows it: the registry row, plus the consumer that
// carries its enabled state. There is no enabled field on the app itself —
// enabled IS the consumer's bit, and a second copy here would be a value that
// can disagree with the one the bus actually reads.
model AppSummary {
  "name": string;
  // The version this app currently runs. Absent when no version has been
  // applied yet: a registry row can exist ahead of its first successful build.
  "current_version"?: AppVersionInfo;
  "created_at": string;
  "updated_at": string;
  // Absent when the app has no `app:<name>` consumer registered. That is a real
  // state, not a gap in the answer: it means nothing is delivering facts to
  // this app.
  "consumer"?: AppConsumerInfo;
}

model AppVersionInfo {
  "id": safeint;
  // The content hash of the built artifact — the version's identity, which is
  // why re-applying byte-identical content reports this same version.
  "content_hash": string;
  "artifact_path": string;
  "created_at": string;
}

// The bus consumer behind an app. `enabled` is the app's enabled state, `lag` is
// head minus cursor: how many facts the app has yet to be delivered.
model AppConsumerInfo {
  "name": string;
  "enabled": boolean;
  "cursor": safeint;
  "lag": safeint;
  "filter": string;
}

model AppListMessage {
  "cmd": "app_list";
}

model AppListResult {
  "apps": AppSummary[];
}

model AppStatusMessage {
  "cmd": "app_status";
  "name": string;
}

// What is known about one app, and only what is known: the registry row, the
// consumer if there is one, the invocations recorded so far, and the shared
// runtime's state. An app's status must not claim a health it cannot observe, so
// every field the runtime owns is absent when there is no runtime.
model AppStatusResult {
  "app": AppSummary;
  "versions": safeint;
  "invocations": safeint;
  // The most recent invocations, newest first, when any have been recorded.
  "recent"?: AppInvocationInfo[];
  // The shared runtime every app's handlers execute in. Absent until it has been
  // started once — a daemon with quiet apps starts no sidecar, and reporting a
  // "stopped" runtime that was never asked to run would read as a fault.
  "runtime"?: AppRuntimeInfo;
  // What this app is currently stuck on, when it is stuck. Present only while a
  // handler is failing on the same event; a successful delivery clears it.
  "stall"?: AppStallInfo;
}

// The shared app runtime as the supervisor sees it. One process for every app —
// isolation between apps is failure attribution, not an OS boundary — so this
// answer is the same whichever app's status asked for it.
model AppRuntimeInfo {
  // "starting" | "connected" | "backoff" | "stopped" | "parked". Parked is the
  // one that needs acting on: it means attn gave up restarting and no app's
  // handlers are running until `attn app runtime restart`.
  "phase": string;
  // "running" | "stopped" — what attn is trying to achieve, as opposed to where
  // it currently is.
  "desired": string;
  "running": boolean;
  "connected": boolean;
  // Increments on every start. The supervisor fences stale processes by it, so a
  // generation that keeps climbing is a crash loop in one number.
  "generation": safeint;
  "restart_attempt": int32;
  // The connected runtime's process id. Absent when nothing is connected.
  "pid"?: safeint;
  "started_at"?: string;
  "connected_at"?: string;
  // When the next restart fires, while backing off.
  "next_restart_at"?: string;
  // When attn gave up restarting. Present only while parked, and it survives a
  // daemon restart: a park has one timestamp for as long as it lasts, so a
  // reader can tell a runtime that has been down since this morning from one
  // that just crash-looped.
  "parked_at"?: string;
  // How the last process ended, rendered. Absent when it has never exited.
  "last_exit"?: string;
}

// An app that has failed on the same event long enough to be on the clock.
//
// `disables_at` is the whole point of reporting this: an app stalled on one
// event holds the durable log's retention floor open for every other consumer,
// so attn disables it, and the reader is entitled to know when.
model AppStallInfo {
  "event_seq": safeint;
  "event_name": string;
  // When the app first failed on this event.
  "since": string;
  "attempts": int32;
  "last_error": string;
  "disables_at": string;
}

// The shared runtime's own surface, addressed as `runtime` rather than as an app
// — which is why no app may be called that.
model AppRuntimeStatusMessage {
  "cmd": "app_runtime_status";
}

model AppRuntimeStatusResult {
  // Absent when the runtime has never been started in this daemon.
  "runtime"?: AppRuntimeInfo;
  // How many apps are registered, and how many of those are enabled — the answer
  // to "should anything be running at all".
  "apps": safeint;
  "apps_enabled": safeint;
  // The binary that gets launched. Absent, with `host_error` set, when it cannot
  // be found: a runtime that will never start must say so before it is asked to.
  "host_path"?: string;
  "host_error"?: string;
  // Where the runtime's captured output lands. `attn app logs` reads it.
  "log_path": string;
}

// Restart the shared runtime: kill the current process if there is one, and
// start a fresh generation. It is also the way out of `parked` — a crash-looped
// runtime is not retried until somebody asks.
//
// It takes no app name, deliberately. There is one runtime for every app, so
// naming one would suggest a per-app restart that does not exist.
model AppRuntimeRestartMessage {
  "cmd": "app_runtime_restart";
}

model AppRuntimeRestartResult {
  // The phase the runtime was in before the restart, so the answer says what
  // actually changed — reviving a parked runtime and bouncing a healthy one are
  // different acts with the same verb.
  "was": string;
  "runtime": AppRuntimeInfo;
}

// The shared runtime's captured stdout and stderr, filtered to one app.
//
// Every app's handlers print to the same process's output, so the runtime tags
// each line with the app that wrote it and this reads the tag back. `runtime` as
// the name means the whole log, untagged lines included — the runtime's own
// output, which is where a startup failure appears.
model AppLogsMessage {
  "cmd": "app_logs";
  "name": string;
  // How many matching lines to return, newest last. 0 means the default.
  "lines"?: int32;
}

model AppLogsResult {
  "name": string;
  "path": string;
  // Matching lines in the order they were written, with the app tag removed.
  "lines": string[];
  // True when older matching lines were dropped to honour the limit.
  "truncated": boolean;
}

// Stream an app's invocations as they are recorded. The connection stays open
// and one result is written per invocation, which is what `attn app dev` renders
// beside its apply results: an app author editing a handler sees the handler run.
model AppWatchMessage {
  "cmd": "app_watch";
  "name": string;
}

model AppWatchResult {
  "invocation": AppInvocationInfo;
}

model AppInvocationInfo {
  "id": safeint;
  // The version that actually ran, which is not necessarily the one the app
  // points at now — a rollback does not rewrite what happened.
  "version_id": safeint;
  "event_seq": safeint;
  "event_name": string;
  "event_subject": string;
  "handler": string;
  "status": string;
  "error": string;
  "duration_ms": safeint;
  "started_at": string;
}

// Flip an app's enabled state, which is its bus consumer's enabled bit and
// nothing else. Refused when the app has no consumer: there is no state to flip,
// and inventing one would leave the app looking enabled while nothing delivers
// to it.
model AppSetEnabledMessage {
  "cmd": "app_set_enabled";
  "name": string;
  "enabled": boolean;
}

model AppSetEnabledResult {
  "name": string;
  "consumer": string;
  "enabled": boolean;
}

// Uninstall an app: stop and delete its bus consumer, then delete its registry
// row. What survives is deliberate — version history, the invocation log, and
// every document under `app/<name>`. The result counts them so the answer says
// what was kept rather than leaving the caller to guess.
model AppRemoveMessage {
  "cmd": "app_remove";
  "name": string;
}

model AppRemoveResult {
  "name": string;
  "consumer_removed": boolean;
  "versions_kept": safeint;
  "invocations_kept": safeint;
  "namespace_kept": string;
}

// Record a built version and point the app at it — the whole of what an apply
// changes, in one transaction.
//
// The caller has already parsed the manifest, regenerated the derived types,
// typechecked, bundled and placed the artifact in the content-addressed store;
// everything that could be refused has been. The daemon does not take that on
// trust: it re-derives the hash from the declaration and the bytes on disk
// before recording anything, so a version row never names content that is not
// what it says it is.
model AppApplyMessage {
  "cmd": "app_apply";
  "name": string;
  // The version's identity: the frozen declaration and the built bundle hashed
  // together. The bundle alone would be wrong — generated types are erased at
  // build time, so a manifest edit can leave the bundle byte-identical while
  // changing what the app declares.
  "content_hash": string;
  // The manifest's snapshot as JSON, frozen onto the version row so what an old
  // version declared survives editing the file.
  "declaration": string;
  // Where it was built from. Logged, never stored: attn does not remember where
  // an app's source lives.
  "source_path"?: string;
}

model AppApplyResult {
  "name": string;
  "version_id": safeint;
  "content_hash": string;
  "artifact_path": string;
  // False when this exact content was already a version of this app. Applying
  // byte-identical content mints no row — the hash is the version's identity —
  // so the pointer simply lands back on the version that already was.
  "version_created": boolean;
  // What the app pointed at before this apply. Absent when it pointed at
  // nothing, which is every app's first apply.
  "previous_version_id"?: safeint;
}

// Move an app onto a version it already has. Rollback builds nothing: a version
// is an immutable row and an artifact that is still on disk, so going back is a
// pointer move and nothing else.
model AppRollbackMessage {
  "cmd": "app_rollback";
  "name": string;
  // Absent means the version applied before the current one.
  "version_id"?: safeint;
}

model AppRollbackResult {
  "name": string;
  "version_id": safeint;
  "content_hash": string;
  "artifact_path": string;
  "previous_version_id"?: safeint;
}

// ============================================================================
// Response Types
// ============================================================================

model Response {
  ok: boolean;
  error?: string;
  // A machine-readable name for what went wrong, beside the human-readable
  // `error` text. Present only where a caller has to branch on the answer:
  // `conflict` (retry after re-reading — `error_conflict` carries which
  // revisions disagreed), `undeclared_collection` (the collection is not
  // there), `invalid_query` (the query itself is wrong), and the two that end a
  // live subscription — `collection_undefined` (the collection was removed;
  // kill the tile) and `collection_redeclared` (it no longer declares a field
  // the query used; the tile's query needs rewriting). Everything else carries
  // no code, and a client must treat an unknown code as "broken, stop" rather
  // than matching the message text.
  error_code?: string;
  error_conflict?: DocumentConflict;
  data?: string;
  delegate_result?: DelegateResult;
  delegation_operation?: DelegationOperation;
  workspace_context_result?: WorkspaceContextResult;
  workspace_context_maintenance_result?: WorkspaceContextMaintenanceResult;
  workspace_contexts?: WorkspaceContext[];
  sessions?: Session[];
  workspaces?: Workspace[];
  prs?: PR[];
  repos?: RepoState[];
  authors?: AuthorState[];
  notebook_entries?: NotebookEntry[];
  notebook_read?: NotebookReadResult;
  notebook_write?: NotebookWriteResult;
  notebook_guide?: NotebookGuideResult;
  journal_append_result?: JournalAppendResult;
  ticket_status_result?: TicketStatusResult;
  ticket_inbox_result?: TicketInboxResult;
  ticket_list_result?: TicketListResult;
  ticket_show_result?: TicketShowResult;
  activity_status_result?: ActivityStatusResult;
  ticket_attach_result?: TicketAttachResult;
  ticket_create_result?: TicketCreateResult;
  ticket_comment_result?: TicketCommentResult;
  ticket_subscribe_result?: TicketSubscribeResult;
  ticket_unsubscribe_result?: TicketUnsubscribeResult;
  ticket_take_result?: TicketTakeResult;
  present_open_result?: PresentOpenResult;
  present_feedback_result?: PresentFeedbackResult;
  session_instructions_result?: SessionInstructionsResult;
  session_transcript_result?: SessionTranscriptResult;
  state_explain_result?: StateExplainResult;
  agent_peek_result?: AgentPeekResult;
  agent_msg_result?: AgentMsgResult;
  doc_define_result?: DocDefineResult;
  doc_undefine_result?: DocUndefineResult;
  doc_collections_result?: DocCollectionsResult;
  doc_put_result?: DocPutResult;
  doc_get_result?: DocGetResult;
  doc_delete_result?: DocDeleteResult;
  doc_query_result?: DocQueryResult;
  doc_count_result?: DocCountResult;
  doc_subscribe_result?: DocSubscribeResult;
  app_list_result?: AppListResult;
  app_status_result?: AppStatusResult;
  app_set_enabled_result?: AppSetEnabledResult;
  app_remove_result?: AppRemoveResult;
  app_apply_result?: AppApplyResult;
  app_rollback_result?: AppRollbackResult;
  app_logs_result?: AppLogsResult;
  app_runtime_status_result?: AppRuntimeStatusResult;
  app_runtime_restart_result?: AppRuntimeRestartResult;
  app_watch_result?: AppWatchResult;
}

// One recorded state observation and its fate. Outcome separates the ways an
// observation can fail to reach the store: "vetoed" is a driver transition
// filter refusing it before the store door, "discarded" is the store's own
// commit rule refusing it, "skipped" is the source deciding it had nothing to
// say. A stuck color is a session whose observations are all non-applied.
model StateExplainEntry {
  source: string;
  claim: string;
  outcome: string; // "applied" | "discarded" | "vetoed" | "skipped"
  detail?: string;
  cause?: string;
  reason?: string;
  observed_at: string;
  recorded_at: string;
  // repeats counts identical consecutive observations collapsed into this one.
  // A level source (the OSC 0 title heartbeat) restates itself continuously;
  // without collapsing, it would evict every other kind of evidence from the
  // bounded ring within a minute.
  repeats?: int32;
}

model StateExplainResult {
  session_id: string;
  agent: string;
  state: string;
  state_since?: string;
  observations: StateExplainEntry[];
  // capacity is the ring size, so a full ring can be reported as truncated
  // rather than read as the whole history.
  capacity: int32;
}

// The target's rendered viewport, plain text from the worker's parsed
// terminal. Viewport only: scrollback is not part of the snapshot RPC.
model AgentPeekScreen {
  text: string;
  cols: int32;
  rows: int32;
}

model AgentPeekResult {
  session_id: string;
  label: string;
  agent: string;
  workspace_id: string;
  workspace_title?: string;
  state: string;
  state_reason?: string;
  state_since: string;
  last_seen: string;
  turn_owed?: boolean;
  // The flat strings the todo hook stores, served as-is; consumers parse the
  // [✓]/[→]/[ ] prefix if they care.
  todos: string[];
  // Absent is not an error: a session with no inspectable transcript, or one
  // that has said nothing yet, simply has no message to show.
  last_assistant_message?: string;
  // Absent when the backend cannot serve a snapshot (old worker, no frame).
  screen?: AgentPeekScreen;
}

// What became of a message. Never a silent drop: a message that could not be
// typed right now is `queued` and the daemon retries it on the target's next
// state change, and one the inbound guard turned away is `refused` with the
// reason. `detail` is the sentence for the sender, presence-specific.
enum AgentMsgStatus {
  delivered,
  queued,
  refused,
}

model AgentMsgResult {
  message_id: string;
  target_session_id: string;
  status: AgentMsgStatus;
  detail: string;
}

model EvidenceExcerpt {
  turn_id: string;
  author: string;
  quote: string;
  timestamp?: string;
}

model SessionInstructionsResult {
  answer: string;
  evidence: EvidenceExcerpt[];
  session_id: string;
  transcript_path: string;
  transcript_fingerprint: string;
  `model`: string;
  reasoning_effort: string;
}

// Stable transcript contract. Provider-specific JSONL records never cross the
// daemon boundary; new runtimes normalize into these existing event kinds.
model SessionTranscriptEvent {
  cursor: string;
  timestamp?: string;
  kind: string; // "user" | "assistant" | "thinking" | "tool_call" | "tool_result" | "error"
                // "thinking" is the agent's own reasoning (Claude thinking blocks,
                // Codex agent_reasoning), not text the user was shown.
  role?: string;
  text?: string;
  tool_name?: string;
  tool_call_id?: string;
  is_error?: boolean;
}

model SessionTranscriptResult {
  session_id: string;
  events: SessionTranscriptEvent[];
  next_cursor: string;
  at_end: boolean;
}

model DelegateResult {
  session_id: string;
  workspace_id: string;
  directory: string;
  branch?: string;
  placement: string;
  worktree_created?: boolean;
}

enum DelegationOperationState {
  accepted,
  preparing,
  completed,
  failed,
}

model DelegationOperation {
  operation_id: string;
  request_id: string;
  state: DelegationOperationState;
  progress: string;
  session_id: string;
  workspace_id?: string;
  ticket_id?: string;
  worktree_path?: string;
  result?: DelegateResult;
  error?: string;
  created_at: string;
  updated_at: string;
}

model DelegateResultMessage {
  event: "delegate_result";
  success: boolean;
  error?: string;
  result?: DelegateResult;
}

model DelegationOperationMessage {
  event: "delegation_operation";
  success: boolean;
  error?: string;
  operation?: DelegationOperation;
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
model Task {
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

// Result of task_list: the durable runner's records, newest-updated
// first. An empty/absent tasks array means the runner is disabled or has no work.
model TaskListResultMessage {
  event: "task_list_result";
  request_id: string;
  success: boolean;
  error?: string;
  tasks?: Task[];
}

// Result of task_retry. task echoes the record after the retry flip
// (failed/dead -> queued); absent when the runner could not act on the id.
model TaskRetryResultMessage {
  event: "task_retry_result";
  request_id: string;
  success: boolean;
  error?: string;
  task?: Task;
}

// Broadcast (no request_id) when any task lifecycle transition occurs, so an open
// task panel re-lists. Fired from the runner's OnChange callback.
model TasksChangedMessage {
  event: "tasks_changed";
}

// One durable notification in the global feed. read_at is empty while unread and
// an RFC3339 timestamp once read. source_kind/source_id point back at the origin
// (e.g. "task" + the task id) so the detail dialog's Retry can act on it. kind and
// source_kind are plain strings (not enums) to avoid the quicktype identical-enum
// merge that drops a generated export.
// How much a notification wants the user. The panel styles the feed by it, and
// critical additionally earns an ambient surface outside the panel that stays up
// while any critical notification is unread.
enum NotificationSeverity {
  info,
  warning,
  critical,
}

model Notification {
  id: string;
  kind: string;
  severity: NotificationSeverity;
  title: string;
  body: string;
  detail: string;
  source_kind: string;
  source_id: string;
  created_at: string;
  read_at: string; // "" while unread
}

// Result of notification_list: the whole feed, newest-created first, plus the
// current unread count so the client can set its badge authoritatively.
model NotificationListResultMessage {
  event: "notification_list_result";
  request_id: string;
  success: boolean;
  error?: string;
  notifications?: Notification[];
  unread_count: int32;
  // The ambient critical surface's whole input, carried here as well as on the
  // broadcast so a client that just connected renders it from its seeding list
  // without a second read. critical_title names the newest unread critical
  // notification and is absent when the count is zero.
  unread_critical_count: int32;
  critical_title?: string;
}

// Result of notification_mark_read. unread_count is the post-mark count so the
// caller can update its badge without a re-list.
model NotificationMarkReadResultMessage {
  event: "notification_mark_read_result";
  request_id: string;
  success: boolean;
  error?: string;
  unread_count: int32;
}

// Broadcast (no request_id) whenever the feed changes — a new notification is
// added or one/all are marked read — so every client updates its badge and any
// open panel re-lists. unread_count is the authoritative post-change count.
model NotificationsUpdatedMessage {
  event: "notifications_updated";
  unread_count: int32;
  // The ambient critical surface is driven entirely by these two, so it appears
  // and clears on the broadcast alone — a panel that was never opened is not a
  // reason for the user to miss something critical.
  unread_critical_count: int32;
  critical_title?: string;
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

model FsReadAssetResult {
  path: string;
  mime_type: string;
  data_base64: string;
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

model FsRenameResult {
  path: string;
  new_path: string;
}

model FsDeleteResult {
  path: string;
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

model FsReadAssetResultMessage {
  event: "fs_read_asset_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: FsReadAssetResult;
}

model FsRenameResultMessage {
  event: "fs_rename_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: FsRenameResult;
}

model FsDeleteResultMessage {
  event: "fs_delete_result";
  request_id: string;
  success: boolean;
  error?: string;
  result?: FsDeleteResult;
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

// Result of fs_watch. root is the resolved absolute root on success, so the
// frontend can key its subscription state on the same value fs_changed.root
// carries.
model FsWatchResultMessage {
  event: "fs_watch_result";
  request_id: string;
  success: boolean;
  error?: string;
  root?: string;
}

// Result of fs_unwatch.
model FsUnwatchResultMessage {
  event: "fs_unwatch_result";
  request_id: string;
  success: boolean;
  error?: string;
  root?: string;
}

// Result of recent_files. files are absolute paths, best-first by frecency.
model RecentFilesResultMessage {
  event: "recent_files_result";
  request_id: string;
  success: boolean;
  error?: string;
  files: FileActivity[];
}

// Result of fs_index. root is the resolved absolute root (echoed for
// correlation with the request). files are root-relative slash paths, files
// only (no directories), sorted lexicographically. truncated is true when the
// walk hit the server-side entry cap before finishing.
model FsIndexResultMessage {
  event: "fs_index_result";
  request_id: string;
  success: boolean;
  error?: string;
  root: string;
  files: string[];
  truncated: boolean;
}

// Broadcast when files under the root change. origin is agent|ui|external, the
// same vocabulary as notebook_changed. Fires for every changed path regardless of
// file type: under the notebook root this rides the always-on notebook watcher;
// under any other root it fires only while at least one client holds that root
// open via fs_watch.
model FsChangedMessage {
  event: "fs_changed";
  paths: string[];
  origin: string;
  root: string; // the resolved absolute root the change happened under
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
  base_ref?: string;
  staged?: GitFileChange[];
  unstaged?: GitFileChange[];
  untracked?: GitFileChange[];

  // PTY lifecycle
  data?: string;
  seq?: int32;
  scrollback_truncated?: boolean;
  screen_snapshot?: string;
  screen_rows?: int32;
  screen_cols?: int32;
  last_seq?: int32;
  cols?: int32;
  rows?: int32;
  // Pane total in device pixels; see PtyResizedMessage.
  xpixel?: int32;
  ypixel?: int32;
  pid?: int32;
  running?: boolean;
  exit_code?: int32;
  reason?: string;
  signal?: string;

  workspace_context_result?: WorkspaceContextResult;
  ticket?: Ticket;
  tickets?: TicketRow[];
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
  | SessionContextWindowCapResultMessage
  | TicketsUpdatedMessage
  | TicketResultMessage
  | TicketActionResultMessage
  | TicketAttachResultMessage
  | TicketResumeResultMessage
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
  | GetRepoInfoResultMessage
  | WorkflowRunUpdatedMessage
  | WorkflowActionResultMessage
  | AutomationApplyResultMessage
  | AutomationValidateResultMessage
  | AutomationDefinitionsResultMessage
  | AutomationDefinitionResultMessage
  | AutomationRunsResultMessage
  | AutomationRunResultMessage
  | AutomationSetEnabledResultMessage
  | AutomationDeleteResultMessage
  | AutomationCleanupResultMessage
  | AutomationsChangedMessage
  | WorkspaceContextResultMessage
  | WorkspaceContextListResultMessage
  | NotebookListResultMessage
  | NotebookReadResultMessage
  | NotebookBacklinksResultMessage
  | NotebookWriteResultMessage
  | NotebookSendToChiefResultMessage
  | FsListResultMessage
  | FsReadResultMessage
  | FsReadAssetResultMessage
  | FsWriteResultMessage
  | FsRenameResultMessage
  | FsDeleteResultMessage
  | FsExistsResultMessage
  | FsWatchResultMessage
  | FsUnwatchResultMessage
  | FsIndexResultMessage
  | RecentFilesResultMessage
  | FsChangedMessage
  | TaskListResultMessage
  | TaskRetryResultMessage
  | TasksChangedMessage
  | NotificationListResultMessage
  | NotificationMarkReadResultMessage
  | NotificationsUpdatedMessage
  | SpawnResultMessage
  | ReloadSessionResultMessage
  | AttachResultMessage
  | GetScreenSnapshotResultMessage
  | PtyOutputMessage
  | AgentEventMessage
  | SessionExitedMessage
  | PtyDesyncMessage
  | KittyPlacementsMessage
  | KittyImageResultMessage
  | RuntimeRespawnedMessage
  | PtyResizedMessage
  | BrowserControlRequestMessage
  | CommandErrorMessage
  | ClientEvictionNoticeMessage
  | GetPresentationsResultMessage
  | GetPresentationRoundResultMessage
  | PresentSubmitRoundResultMessage
  | PresentCloseResultMessage
  | PresentationAddedMessage
  | PresentationUpdatedMessage
  | OpenMarkdownResultMessage
  | MarkdownAnnotationsGetResultMessage
  | MarkdownAnnotationsSaveResultMessage
  | MarkdownAnnotationsClearResultMessage
  | MarkdownAnnotationsSubmitResultMessage
  | PastConversationsResultMessage
  | BusStatusResultMessage
  | BusSetConsumerEnabledResultMessage;
