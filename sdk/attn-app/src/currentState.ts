// attn's current state, as an app view reads it.
//
// These shapes are hand-copied from the wire's generated types: this package is
// published as declarations an app typechecks against with no npm, so it cannot
// import the frontend's generated.ts. app/src/appSdk.currentStateDrift.test.mjs
// is what keeps the copy honest — it fails by name when a field here and the
// same field on the wire stop agreeing.

export interface PullRequestProvenance {
  readonly repository: string
  readonly number: number
  readonly url: string
  readonly title?: string
  readonly head_sha: string
}

export interface AutomationProvenance {
  readonly run_id: string
  readonly definition_id: string
  readonly definition_name: string
  readonly trigger_type: string
  readonly pull_request?: PullRequestProvenance
}

/** One agent session in attn's current state. */
export interface Session {
  readonly activity?: string
  readonly activity_at?: string
  readonly agent: string
  readonly auto_settle_dismiss_armed?: boolean
  readonly auto_settle_fires_at?: string
  readonly auto_settle_held?: boolean
  readonly automation?: AutomationProvenance
  readonly branch?: string
  readonly chief_of_staff?: boolean
  readonly context_window_cap?: number
  readonly cost_unknown?: boolean
  readonly cost_usd?: number
  readonly crew_member?: string
  readonly delegated_from_chief?: boolean
  readonly directory: string
  readonly endpoint_id?: string
  readonly id: string
  readonly is_worktree?: boolean
  readonly label: string
  readonly last_seen: string
  readonly main_repo?: string
  readonly nudge_fires_at?: string
  readonly parent_session_id?: string
  readonly pinned_at?: string
  /** The seed this session reports to, when it was dispatched onto one. */
  readonly seed_id?: string
  readonly state: "idle" | "launching" | "pending_approval" | "recoverable" | "scheduled" | "unknown" | "waiting_input" | "working"
  readonly state_reason?: string
  readonly state_since: string
  readonly state_updated_at: string
  /**
   * True when this session's pty-worker was built against a different
   * libghostty-vt than the daemon, which happens when the app updates under a
   * running session. The app offers a reload; nothing else needs to act on it.
   */
  readonly terminal_build_stale?: boolean
  readonly ticket_unread?: boolean
  readonly todos?: readonly string[]
  readonly turn_opened_at?: string
  readonly turn_owed?: boolean
  readonly turn_snoozed_until?: string
  readonly workspace_id: string
  readonly workspace_muted?: boolean
}

export interface EndpointCapabilities {
  readonly agents_available: readonly string[]
  readonly daemon_instance_id?: string
  readonly projects_directory?: string
  readonly protocol_version: string
  readonly pty_backend_mode?: string
  readonly tailscale_auth_url?: string
  readonly tailscale_domain?: string
  readonly tailscale_enabled?: boolean
  readonly tailscale_error?: string
  readonly tailscale_status?: string
  readonly tailscale_url?: string
}

export interface EndpointInfo {
  readonly capabilities?: EndpointCapabilities
  readonly enabled?: boolean
  readonly id: string
  readonly name: string
  readonly profile?: string
  readonly session_count?: number
  readonly ssh_target: string
  readonly status: string
  readonly status_message?: string
}

export interface WorkspacePane {
  readonly error?: string
  readonly kind: "agent"
  readonly pane_id: string
  readonly runtime_id?: string
  readonly session_id?: string
  readonly status: "failed" | "ready" | "spawning"
  readonly title: string
  readonly workspace_id: string
}

export interface WorkspaceLayout {
  readonly active_pane_id: string
  readonly layout_json: string
  readonly panes: readonly WorkspacePane[]
  readonly updated_at?: string
  readonly workspace_id: string
}

export interface Workspace {
  readonly directory: string
  readonly endpoint_id?: string
  readonly id: string
  readonly layout?: WorkspaceLayout
  readonly muted: boolean
  readonly pinned: boolean
  readonly rank: string
  readonly status: "idle" | "launching" | "pending_approval" | "scheduled" | "unknown" | "waiting_input" | "working"
  readonly title: string
}

export interface PR {
  readonly approved_by_me: boolean
  readonly author: string
  readonly ci_status?: string
  readonly comment_count?: number
  readonly details_fetched: boolean
  readonly details_fetched_at?: string
  readonly has_new_changes: boolean
  readonly head_branch?: string
  readonly head_sha?: string
  readonly heat_state?: "cold" | "hot" | "warm"
  readonly host: string
  readonly id: string
  readonly last_heat_activity_at?: string
  readonly last_polled: string
  readonly last_updated: string
  readonly mergeable?: boolean
  readonly mergeable_state?: string
  readonly muted: boolean
  readonly number: number
  readonly reason: string
  readonly repo: string
  readonly review_status?: string
  readonly role: "author" | "reviewer"
  readonly state: string
  readonly title: string
  readonly url: string
}

export interface RepoState {
  readonly collapsed: boolean
  readonly muted: boolean
  readonly repo: string
}

export interface AuthorState {
  readonly author: string
  readonly muted: boolean
}

// The app's own surfaces read the garden now; this row is kept for apps and is
// shaped by internal/daemon/current_state.go, not by the app wire.
export interface TicketRow {
  readonly assignee: string
  readonly automation?: AutomationProvenance
  readonly closed_at?: string
  readonly cwd: string
  readonly id: string
  readonly last_agent_id: string
  readonly reconciled_at?: string
  readonly status: "blocked" | "crashed" | "done" | "failed" | "in_review" | "todo" | "working"
  readonly title: string
  readonly updated_at: string
}

export interface SeedEdge {
  readonly kind: string
  readonly to: string
}

export interface SeedPlotProgress {
  readonly blocked: number
  readonly done: number
  readonly dormant: number
  readonly growing: number
  readonly ready: number
  readonly total: number
  readonly withered: number
}

export interface SeedVar {
  readonly default?: string
  readonly description?: string
  readonly enum?: readonly string[]
  readonly name: string
  readonly pattern?: string
  readonly required?: boolean
}

export interface Seed {
  readonly body: string
  readonly created_at: string
  readonly edges: readonly SeedEdge[]
  readonly gate: boolean
  readonly id: string
  readonly planter_member: string
  readonly planter_session: string
  readonly plot_progress?: SeedPlotProgress
  readonly ready: boolean
  readonly reason?: string
  readonly resume_agent?: string
  readonly resume_cwd?: string
  readonly resume_session_id?: string
  readonly rev: number
  readonly status: string
  readonly step_slug: string
  readonly template: boolean
  readonly tender_member: string
  readonly tender_session: string
  readonly title: string
  readonly updated_at: string
  readonly vars: readonly SeedVar[]
}

export interface CrewMember {
  readonly agent?: string
  readonly awareness_dirs: readonly string[]
  readonly binding_session?: string
  readonly charter_path: string
  readonly cwd?: string
  readonly home_dir: string
  readonly id: string
  readonly model?: string
}

export interface AppViewInfo {
  readonly kind: string
  readonly name: string
  readonly params_label?: string
  readonly params_placeholder?: string
  readonly title: string
}

/** The app projection Initial State uses for mounting views. */
export interface AppRegistryEntry {
  readonly content_hash?: string
  readonly description?: string
  readonly enabled: boolean
  readonly name: string
  readonly version_id?: number
  readonly views: readonly AppViewInfo[]
}

/** The state-bearing domains shared with attn's Initial State projection. */
export interface CurrentStateSnapshot {
  /** Bus head captured before the projection was assembled. */
  readonly asOfSeq: number
  readonly sessions: readonly Session[]
  readonly endpoints: readonly EndpointInfo[]
  readonly workspaces: readonly Workspace[]
  readonly prs: readonly PR[]
  readonly repos: readonly RepoState[]
  readonly authors: readonly AuthorState[]
  readonly githubHosts: readonly string[]
  readonly tickets: readonly TicketRow[]
  readonly seeds: readonly Seed[]
  readonly crew: readonly CrewMember[]
  readonly apps: readonly AppRegistryEntry[]
}
