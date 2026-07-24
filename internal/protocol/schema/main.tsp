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
  needs_review_after_long_run?: boolean; // True when a 5m+ run finished and awaits user visualization
  chief_of_staff?: boolean;     // True when this session holds the profile-wide coordinator role
  delegated_from_chief?: boolean; // True when this session was delegated from the chief of staff (assignee of a chief-authored ticket)
  ticket_unread?: boolean;      // True when this session has unread ticket events (drives the incoming-nudge indicator)
  nudge_fires_at?: string;      // ISO timestamp a pending ticket-nudge countdown will fire; absent when paused (active session) or no countdown
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

// Ticket is the wire shape of a durable tracked-work record. `activity` and
// `artifacts` are the full history thread and current attach files — populated on the
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
  runtime_state: string; // "stopped" | "starting" | "connected" | "degraded"
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
  request_id: string; // Stable caller-supplied idempotency key for one logical delegation
  source_session_id: string;
  brief: string;
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
// Rows carry the description (the brief) but not the activity thread (bare rows, like
// the broadcast).
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

// Task status surface: list the durable task runner's records, and retry a
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

// TriggerNudgeMessage is the user clicking the incoming-nudge indicator to deliver
// the pending ticket doorbell immediately, bypassing the countdown. Fire-and-forget.
model TriggerNudgeMessage {
  cmd: "trigger_nudge";
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
  initial_prompt?: string;
  executable?: string;         // Generic executable override for selected agent
  claude_executable?: string;  // Backward compatibility
  codex_executable?: string;   // Backward compatibility
  copilot_executable?: string; // Backward compatibility
  `model`?: string;            // Pin the launched agent's model; empty = agent default (chief launches fall back to chief_model_<agent>)
  effort?: string;             // Pin the launched agent's reasoning effort; empty = agent default
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

// Pushes the app's resolved terminal theme colors to the daemon. Fire-and-forget:
// no result event.
model SetTerminalThemeMessage {
  cmd: "set_terminal_theme";
  foreground: string; // "#rrggbb"
  background: string; // "#rrggbb"
  cursor: string; // "#rrggbb"
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
// (bare — no activity/artifacts), re-pushed whenever any ticket mutates. It is
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

// runtime_respawned: the daemon killed and re-spawned a session's agent process
// in place (e.g. chief-of-staff assign/demote reload). The session id is
// unchanged and the transcript is resumed; the client must tear down its dead PTY
// stream and reattach to the new runtime. Distinct from session_exited (which
// drops the session to a dead pane) — the runtime was replaced, not closed.
model RuntimeRespawnedMessage {
  event: "runtime_respawned";
  id: string;
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
  kind: string; // "user" | "assistant" | "tool_call" | "tool_result" | "error"
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
model Notification {
  id: string;
  kind: string;
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
  | SessionExitedMessage
  | PtyDesyncMessage
  | RuntimeRespawnedMessage
  | PtyResizedMessage
  | BrowserControlRequestMessage
  | CommandErrorMessage
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
  | MarkdownAnnotationsSubmitResultMessage;
