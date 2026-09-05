package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/rankkey"
)

// A 14-watcher restore exposed sql.DB's two-idle default as schema-reparse churn.
// Keep that measured burst resident and cap excess SQLite connection memory.
const sqliteFileConnectionPoolSize = 16

const baseSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	directory TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT 'idle',
	state_since TEXT NOT NULL,
	state_updated_at TEXT NOT NULL,
	todos TEXT,
	last_seen TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS prs (
	id TEXT PRIMARY KEY,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	title TEXT NOT NULL,
	url TEXT NOT NULL,
	role TEXT NOT NULL,
	state TEXT NOT NULL,
	reason TEXT,
	last_updated TEXT NOT NULL,
	last_polled TEXT NOT NULL,
	muted INTEGER NOT NULL DEFAULT 0,
	details_fetched INTEGER NOT NULL DEFAULT 0,
	details_fetched_at TEXT,
	mergeable INTEGER,
	mergeable_state TEXT,
	ci_status TEXT,
	review_status TEXT
);

CREATE TABLE IF NOT EXISTS repos (
	repo TEXT PRIMARY KEY,
	muted INTEGER NOT NULL DEFAULT 0,
	collapsed INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS pr_interactions (
	pr_id TEXT PRIMARY KEY,
	last_visited_at TEXT,
	last_approved_at TEXT,
	last_seen_sha TEXT,
	last_seen_comment_count INTEGER
);

CREATE TABLE IF NOT EXISTS worktrees (
	path TEXT PRIMARY KEY,
	branch TEXT NOT NULL,
	main_repo TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT
);

CREATE TABLE IF NOT EXISTS endpoints (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	ssh_target TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	profile TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);
`

type migration struct {
	version int
	desc    string
	sql     string
}

const delegationOperationsSchema = `CREATE TABLE IF NOT EXISTS delegation_operations (
	request_id TEXT PRIMARY KEY,
	operation_id TEXT NOT NULL UNIQUE,
	request_json TEXT NOT NULL,
	state TEXT NOT NULL,
	progress TEXT NOT NULL,
	session_id TEXT NOT NULL,
	workspace_id TEXT NOT NULL DEFAULT '',
	ticket_id TEXT NOT NULL DEFAULT '',
	worktree_path TEXT NOT NULL DEFAULT '',
	worktree_owned INTEGER NOT NULL DEFAULT 0,
	result_json TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_delegation_operations_operation_id
	ON delegation_operations(operation_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_delegation_operations_active_ticket
	ON delegation_operations(ticket_id)
	WHERE ticket_id != '' AND state IN ('accepted', 'preparing');`

// Never reuse a version number, even one only claimed on another branch: migrateDB skips
// `version <= max(applied)`. Burned: 50, 98, 108, 111, 112, 113.
var migrations = []migration{
	{1, "add head_sha to prs", "ALTER TABLE prs ADD COLUMN head_sha TEXT"},
	{2, "add head_branch to prs", "ALTER TABLE prs ADD COLUMN head_branch TEXT"},
	{3, "add comment_count to prs", "ALTER TABLE prs ADD COLUMN comment_count INTEGER NOT NULL DEFAULT 0"},
	{4, "add approved_by_me to prs", "ALTER TABLE prs ADD COLUMN approved_by_me INTEGER NOT NULL DEFAULT 0"},
	{5, "add heat_state to prs", "ALTER TABLE prs ADD COLUMN heat_state TEXT NOT NULL DEFAULT 'cold'"},
	{6, "add last_heat_activity_at to prs", "ALTER TABLE prs ADD COLUMN last_heat_activity_at TEXT"},
	{7, "add last_seen_ci_status to pr_interactions", "ALTER TABLE pr_interactions ADD COLUMN last_seen_ci_status TEXT"},
	{8, "add branch to sessions", "ALTER TABLE sessions ADD COLUMN branch TEXT"},
	{9, "add is_worktree to sessions", "ALTER TABLE sessions ADD COLUMN is_worktree INTEGER NOT NULL DEFAULT 0"},
	{10, "add main_repo to sessions", "ALTER TABLE sessions ADD COLUMN main_repo TEXT"},
	{11, "create recent_locations table", `CREATE TABLE IF NOT EXISTS recent_locations (
		path TEXT PRIMARY KEY,
		label TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		use_count INTEGER NOT NULL DEFAULT 1
	)`},
	{12, "create reviews table", `CREATE TABLE IF NOT EXISTS reviews (
		id TEXT PRIMARY KEY,
		branch TEXT NOT NULL,
		pr_number INTEGER,
		repo_path TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(repo_path, branch)
	)`},
	{13, "create review_viewed_files table", `CREATE TABLE IF NOT EXISTS review_viewed_files (
		review_id TEXT NOT NULL,
		filepath TEXT NOT NULL,
		viewed_at TEXT NOT NULL,
		PRIMARY KEY (review_id, filepath),
		FOREIGN KEY (review_id) REFERENCES reviews(id) ON DELETE CASCADE
	)`},
	{14, "create review_comments table", `CREATE TABLE IF NOT EXISTS review_comments (
		id TEXT PRIMARY KEY,
		review_id TEXT NOT NULL,
		filepath TEXT NOT NULL,
		line_start INTEGER NOT NULL,
		line_end INTEGER NOT NULL,
		content TEXT NOT NULL,
		author TEXT NOT NULL,
		resolved INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		FOREIGN KEY (review_id) REFERENCES reviews(id) ON DELETE CASCADE
	)`},
	{15, "add resolution tracking to review_comments", `
		ALTER TABLE review_comments ADD COLUMN resolved_by TEXT NOT NULL DEFAULT '';
		ALTER TABLE review_comments ADD COLUMN resolved_at TEXT NOT NULL DEFAULT '';
	`},
	{16, "create reviewer_sessions table", `CREATE TABLE IF NOT EXISTS reviewer_sessions (
		id TEXT PRIMARY KEY,
		review_id TEXT NOT NULL,
		commit_sha TEXT NOT NULL,
		transcript TEXT NOT NULL,
		started_at TEXT NOT NULL,
		completed_at TEXT,
		FOREIGN KEY (review_id) REFERENCES reviews(id) ON DELETE CASCADE
	)`},
	{17, "add wont_fix tracking to review_comments", `
		ALTER TABLE review_comments ADD COLUMN wont_fix INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE review_comments ADD COLUMN wont_fix_by TEXT NOT NULL DEFAULT '';
		ALTER TABLE review_comments ADD COLUMN wont_fix_at TEXT NOT NULL DEFAULT '';
	`},
	{18, "create authors table", `CREATE TABLE IF NOT EXISTS authors (
		author TEXT PRIMARY KEY,
		muted INTEGER NOT NULL DEFAULT 0
	)`},
	{19, "add author to prs", "ALTER TABLE prs ADD COLUMN author TEXT NOT NULL DEFAULT ''"},
	{20, "add host to prs and migrate ids", `
		ALTER TABLE prs ADD COLUMN host TEXT NOT NULL DEFAULT 'github.com';
		UPDATE prs SET id = 'github.com:' || id WHERE id NOT LIKE '%:%';
		UPDATE pr_interactions SET pr_id = 'github.com:' || pr_id WHERE pr_id NOT LIKE '%:%';
		CREATE UNIQUE INDEX IF NOT EXISTS idx_prs_host_repo_number ON prs(host, repo, number);
	`},
	{21, "add agent to sessions", "ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT 'codex'"},
	{22, "add recoverable to sessions", "ALTER TABLE sessions ADD COLUMN recoverable INTEGER NOT NULL DEFAULT 0"},
	{23, "add resume_session_id to sessions", "ALTER TABLE sessions ADD COLUMN resume_session_id TEXT NOT NULL DEFAULT ''"},
	{24, "create session_review_loops table", `CREATE TABLE IF NOT EXISTS session_review_loops (
		session_id TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		preset_id TEXT,
		custom_prompt TEXT,
		resolved_prompt TEXT NOT NULL,
		iteration_count INTEGER NOT NULL DEFAULT 0,
		iteration_limit INTEGER NOT NULL,
		stop_requested INTEGER NOT NULL DEFAULT 0,
		advance_token TEXT NOT NULL,
		stop_reason TEXT,
		last_prompt_at TEXT,
		last_advance_at TEXT,
		last_user_input_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
	)`},
	{25, "create review_loop_runs table", `CREATE TABLE IF NOT EXISTS review_loop_runs (
		id TEXT PRIMARY KEY,
		source_session_id TEXT NOT NULL,
		repo_path TEXT NOT NULL,
		status TEXT NOT NULL,
		preset_id TEXT,
		custom_prompt TEXT,
		resolved_prompt TEXT NOT NULL,
		handoff_payload_json TEXT,
		iteration_count INTEGER NOT NULL DEFAULT 0,
		iteration_limit INTEGER NOT NULL,
		pending_interaction_id TEXT,
		last_decision TEXT,
		last_result_summary TEXT,
		last_error TEXT,
		stop_reason TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		completed_at TEXT,
		FOREIGN KEY (source_session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_review_loop_runs_source_session_created_at
		ON review_loop_runs(source_session_id, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_review_loop_runs_status
		ON review_loop_runs(status);`},
	{26, "create review_loop_iterations table", `CREATE TABLE IF NOT EXISTS review_loop_iterations (
		id TEXT PRIMARY KEY,
		loop_id TEXT NOT NULL,
		iteration_number INTEGER NOT NULL,
		status TEXT NOT NULL,
		decision TEXT,
		summary TEXT,
		result_text TEXT,
		changes_made INTEGER,
		files_touched_json TEXT,
		blocking_reason TEXT,
		suggested_next_focus TEXT,
		structured_output_json TEXT,
		assistant_trace_json TEXT,
		error TEXT,
		started_at TEXT NOT NULL,
		completed_at TEXT,
		UNIQUE(loop_id, iteration_number),
		FOREIGN KEY (loop_id) REFERENCES review_loop_runs(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_review_loop_iterations_loop_id_iteration_number
		ON review_loop_iterations(loop_id, iteration_number ASC);`},
	{27, "create review_loop_interactions table", `CREATE TABLE IF NOT EXISTS review_loop_interactions (
		id TEXT PRIMARY KEY,
		loop_id TEXT NOT NULL,
		iteration_id TEXT,
		kind TEXT NOT NULL,
		question TEXT NOT NULL,
		answer TEXT,
		status TEXT NOT NULL,
		created_at TEXT NOT NULL,
		answered_at TEXT,
		consumed_at TEXT,
		FOREIGN KEY (loop_id) REFERENCES review_loop_runs(id) ON DELETE CASCADE,
		FOREIGN KEY (iteration_id) REFERENCES review_loop_iterations(id) ON DELETE SET NULL
	);
	CREATE INDEX IF NOT EXISTS idx_review_loop_interactions_loop_id_created_at
		ON review_loop_interactions(loop_id, created_at ASC);
	CREATE INDEX IF NOT EXISTS idx_review_loop_interactions_status
		ON review_loop_interactions(status);`},
	{28, "add result_text to review_loop_iterations", "ALTER TABLE review_loop_iterations ADD COLUMN result_text TEXT"},
	{29, "add change_stats_json to review_loop_iterations", "ALTER TABLE review_loop_iterations ADD COLUMN change_stats_json TEXT"},
	{30, "create workspace persistence tables", `CREATE TABLE IF NOT EXISTS session_workspaces (
		session_id TEXT PRIMARY KEY,
		active_pane_id TEXT NOT NULL,
		layout_json TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS workspace_panes (
		session_id TEXT NOT NULL,
		pane_id TEXT NOT NULL,
		runtime_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		title TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (session_id, pane_id)
	);
	CREATE INDEX IF NOT EXISTS idx_workspace_panes_runtime_id
		ON workspace_panes(runtime_id);`},
	{31, "add endpoint_id to sessions", "ALTER TABLE sessions ADD COLUMN endpoint_id TEXT"},
	{32, "create endpoints table", `CREATE TABLE IF NOT EXISTS endpoints (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		ssh_target TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`},
	{33, "drop unused wont_fix columns from review_comments", ""},
	{34, "add profile to endpoints", "ALTER TABLE endpoints ADD COLUMN profile TEXT NOT NULL DEFAULT ''"},
	{35, "create canvas-workspaces table and add workspace_id to sessions", `
	CREATE TABLE IF NOT EXISTS workspaces (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		directory TEXT NOT NULL,
		muted INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);
		ALTER TABLE sessions ADD COLUMN workspace_id TEXT;
		CREATE INDEX IF NOT EXISTS idx_sessions_workspace_id ON sessions(workspace_id);
	`},
	{36, "create canvas workspace panels table", `
		CREATE TABLE IF NOT EXISTS canvas_workspace_panels (
			workspace_id TEXT NOT NULL,
			panel_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			world_x REAL NOT NULL,
			world_y REAL NOT NULL,
			width REAL NOT NULL,
			height REAL NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (workspace_id, panel_id)
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_canvas_workspace_panels_session_id
			ON canvas_workspace_panels(session_id);
		CREATE INDEX IF NOT EXISTS idx_canvas_workspace_panels_workspace_id
			ON canvas_workspace_panels(workspace_id);
	`},
	{37, "migrate session layouts to workspace layouts", ""},
	{38, "add opaque agent metadata to sessions", "ALTER TABLE sessions ADD COLUMN agent_metadata TEXT NOT NULL DEFAULT ''"},
	{39, "add agent driver report cursor to sessions", `
		ALTER TABLE sessions ADD COLUMN agent_driver_plugin_name TEXT NOT NULL DEFAULT '';
		ALTER TABLE sessions ADD COLUMN agent_driver_run_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE sessions ADD COLUMN agent_driver_report_seq INTEGER NOT NULL DEFAULT 0;
	`},
	{40, "add workspace pane lifecycle status", ""},
	{41, "move session mute state to workspaces", `
		ALTER TABLE workspaces ADD COLUMN muted INTEGER NOT NULL DEFAULT 0;
	`},
	{42, "create workspace contexts table", `
		CREATE TABLE IF NOT EXISTS workspace_contexts (
			workspace_id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			revision INTEGER NOT NULL,
			updated_by_session_id TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
	`},
	{43, "create profile roles table", `
		CREATE TABLE IF NOT EXISTS profile_roles (
			role TEXT PRIMARY KEY,
			session_id TEXT NOT NULL
		);
	`},
	{44, "create chief of staff dispatches table", `
		CREATE TABLE IF NOT EXISTS chief_of_staff_dispatches (
			id TEXT PRIMARY KEY,
			chief_session_id TEXT NOT NULL,
			session_id TEXT NOT NULL UNIQUE,
			workspace_id TEXT NOT NULL,
			brief TEXT NOT NULL,
			label TEXT NOT NULL,
			agent TEXT NOT NULL,
			directory TEXT NOT NULL,
			branch TEXT NOT NULL DEFAULT '',
			latest_report TEXT NOT NULL DEFAULT '',
			reported_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_chief_dispatches_chief_created
			ON chief_of_staff_dispatches(chief_session_id, created_at DESC);
	`},
	{45, "add structured coordination report to chief dispatches", `
		ALTER TABLE chief_of_staff_dispatches
			ADD COLUMN structured_report_json TEXT NOT NULL DEFAULT '';
	`},
	{46, "create chief of staff dispatch messages table", `
		CREATE TABLE IF NOT EXISTS chief_of_staff_dispatch_messages (
			id TEXT PRIMARY KEY,
			dispatch_id TEXT NOT NULL,
			sender_session_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			read_at TEXT NOT NULL DEFAULT '',
			acknowledged_at TEXT NOT NULL DEFAULT '',
			acknowledgement TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(dispatch_id) REFERENCES chief_of_staff_dispatches(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_chief_dispatch_messages_dispatch_created
			ON chief_of_staff_dispatch_messages(dispatch_id, created_at, id);
		CREATE INDEX IF NOT EXISTS idx_chief_dispatch_messages_target_unread
			ON chief_of_staff_dispatch_messages(target_session_id, read_at, created_at);
	`},
	{47, "create workspace context janitor backups table", `
		CREATE TABLE IF NOT EXISTS workspace_context_janitor_backups (
			workspace_id TEXT PRIMARY KEY,
			source_revision INTEGER NOT NULL,
			source_content TEXT NOT NULL,
			result_revision INTEGER NOT NULL,
			agent TEXT NOT NULL,
			model TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
	`},
	{48, "drop label from recent_locations", "ALTER TABLE recent_locations DROP COLUMN label"},
	{49, "add rank to workspaces", `ALTER TABLE workspaces ADD COLUMN rank TEXT NOT NULL DEFAULT ''`},
	// Dispatched to applyMigration49; this SQL never runs. Real DDL here would
	// be a duplicate-column landmine.
	{50, "repair missing workspace rank", `SELECT 1`},
	{51, "create workflow engine journal tables", `CREATE TABLE IF NOT EXISTS workflow_runs (
    run_id TEXT PRIMARY KEY,
    script_path TEXT NOT NULL,
    script_hash TEXT NOT NULL,
    args_json TEXT,
    session_id TEXT,
    workspace_id TEXT,
    status TEXT NOT NULL,
    phase TEXT,
    harness TEXT,
    result_json TEXT,
    last_error TEXT,
    resumable INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_status
    ON workflow_runs(status);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_created_at
    ON workflow_runs(created_at DESC);
CREATE TABLE IF NOT EXISTS workflow_agent_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL,
    ordinal TEXT NOT NULL,
    label TEXT,
    phase TEXT,
    prompt_hash TEXT,
    schema_hash TEXT,
    resolved_model TEXT,
    resolved_harness TEXT,
    agent_type TEXT,
    result_json TEXT,
    status TEXT NOT NULL,
    error TEXT,
    result_path TEXT,
    started_at TEXT,
    completed_at TEXT,
    UNIQUE(run_id, ordinal),
    FOREIGN KEY (run_id) REFERENCES workflow_runs(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_workflow_agent_calls_run_id
    ON workflow_agent_calls(run_id, id ASC);`},
	{52, "rename workspace context janitor backups to keeper compact backups", ""},
	{53, "add closed_state to chief of staff dispatches", ""},
	{54, "add pinned to workspaces", ""},
	{55, "create ticket tables", `CREATE TABLE IF NOT EXISTS tickets (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL,
    assignee      TEXT NOT NULL DEFAULT '',
    cwd           TEXT NOT NULL DEFAULT '',
    last_agent_id TEXT NOT NULL DEFAULT '',
    project_id    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    closed_at     TEXT NOT NULL DEFAULT '',
    archived_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status);
CREATE INDEX IF NOT EXISTS idx_tickets_archived_closed
    ON tickets(archived_at, closed_at);
CREATE TABLE IF NOT EXISTS ticket_activity (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_id   TEXT NOT NULL,
    kind        TEXT NOT NULL,
    author      TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    comment     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ticket_activity_ticket
    ON ticket_activity(ticket_id, id ASC);
CREATE TABLE IF NOT EXISTS ticket_attachments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_id   TEXT NOT NULL,
    filename    TEXT NOT NULL,
    path        TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ticket_attachments_ticket
    ON ticket_attachments(ticket_id, id ASC);`},
	{56, "create ticket event log", `CREATE TABLE IF NOT EXISTS ticket_events (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_id   TEXT NOT NULL,
    kind        TEXT NOT NULL,
    author      TEXT NOT NULL DEFAULT '',
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    comment     TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ticket_events_ticket
    ON ticket_events(ticket_id, seq);
CREATE TABLE IF NOT EXISTS ticket_event_cursors (
    identity   TEXT NOT NULL,
    ticket_id  TEXT NOT NULL,
    cursor     INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (identity, ticket_id),
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);`},
	{57, "add resume_session_id to tickets", "ALTER TABLE tickets ADD COLUMN resume_session_id TEXT NOT NULL DEFAULT ''"},
	{58, "create ticket subscriptions", `CREATE TABLE IF NOT EXISTS ticket_subscriptions (
    identity   TEXT NOT NULL,
    ticket_id  TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (identity, ticket_id),
    FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
);`},
	{59, "drop review_loop tables and settings", `
		DROP TABLE IF EXISTS review_loop_interactions;
		DROP TABLE IF EXISTS review_loop_iterations;
		DROP TABLE IF EXISTS review_loop_runs;
		DROP TABLE IF EXISTS session_review_loops;
		DELETE FROM settings WHERE key IN ('review_loop_prompt_presets','review_loop_last_preset','review_loop_last_prompt','review_loop_last_iterations','review_loop_model');
	`},
	{60, "add reconciled_at to tickets", "ALTER TABLE tickets ADD COLUMN reconciled_at TEXT NOT NULL DEFAULT ''"},
	{61, "create tasks table", `CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		subject TEXT NOT NULL,
		state TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt_at TEXT NOT NULL,
		last_error TEXT NOT NULL DEFAULT '',
		meta_json TEXT NOT NULL DEFAULT '',
		requeued INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`},
	{62, "create notifications table", `CREATE TABLE IF NOT EXISTS notifications (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		body TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT '',
		source_kind TEXT NOT NULL DEFAULT '',
		source_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		read_at TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at)`},
	{63, "create presentation tables", `
		CREATE TABLE IF NOT EXISTS presentations (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			ticket_id TEXT,
			title TEXT NOT NULL,
			kind TEXT NOT NULL,
			repo_path TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'open',
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_presentations_session ON presentations(session_id);
		CREATE TABLE IF NOT EXISTS presentation_rounds (
			id TEXT PRIMARY KEY,
			presentation_id TEXT NOT NULL REFERENCES presentations(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			manifest_yaml TEXT NOT NULL,
			base_sha TEXT NOT NULL,
			head_sha TEXT NOT NULL,
			created_at TEXT NOT NULL,
			submitted_at TEXT,
			UNIQUE(presentation_id, seq)
		);
		CREATE TABLE IF NOT EXISTS presentation_comments (
			id TEXT PRIMARY KEY,
			round_id TEXT NOT NULL REFERENCES presentation_rounds(id) ON DELETE CASCADE,
			filepath TEXT NOT NULL,
			line_start INTEGER NOT NULL,
			line_end INTEGER NOT NULL,
			side TEXT NOT NULL,
			content TEXT NOT NULL,
			author TEXT NOT NULL DEFAULT 'user',
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_presentation_comments_round ON presentation_comments(round_id);
	`},
	{64, "add closed_intentionally_at to sessions", "ALTER TABLE sessions ADD COLUMN closed_intentionally_at TEXT NOT NULL DEFAULT ''"},
	{65, "add verdict to presentation_rounds", ""},
	{66, "add durable ticket role ownership", `
		CREATE TABLE IF NOT EXISTS ticket_role_owners (
			role TEXT NOT NULL,
			ticket_id TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (role, ticket_id),
			FOREIGN KEY (ticket_id) REFERENCES tickets(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_ticket_role_owners_ticket
			ON ticket_role_owners(ticket_id, role);
		INSERT OR IGNORE INTO ticket_role_owners (role, ticket_id, created_at)
		SELECT 'chief_of_staff', t.id, t.created_at
		FROM tickets t
		JOIN ticket_events e ON e.ticket_id = t.id AND e.kind = 'created'
		WHERE t.assignee != ''
			AND t.archived_at = ''
			AND t.status NOT IN ('done', 'failed', 'crashed')
			AND NOT EXISTS (
				SELECT 1 FROM ticket_events assigned
				WHERE assigned.ticket_id = t.id AND assigned.kind = 'assigned'
			);
	`},
	{67, "rename ticket artifact handover records to attachments", `
		UPDATE ticket_activity SET kind = 'attach' WHERE kind = 'handover';
		UPDATE ticket_events SET kind = 'attach_submitted' WHERE kind = 'handover_submitted';
	`},
	{68, "create markdown annotation drafts table", `CREATE TABLE IF NOT EXISTS markdown_annotation_drafts (
		path TEXT PRIMARY KEY,
		annotations_json TEXT NOT NULL,
		generation INTEGER NOT NULL,
		tombstone_generation INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL
	)`},
	{69, "create ticket delivery attention table", `CREATE TABLE IF NOT EXISTS ticket_delivery_attention (
		observer_key TEXT PRIMARY KEY,
		last_attention_at TEXT NOT NULL
	)`},
	{70, "create delegation operations table", delegationOperationsSchema},
	{71, "add delegation worktree ownership token", ""},
	{72, "add delegation initiating chief identity", ""},
	{73, "create automation foundation tables", `
		CREATE TABLE IF NOT EXISTS automation_definitions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			revision INTEGER NOT NULL,
			spec_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS automation_occurrences (
			id TEXT PRIMARY KEY,
			definition_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			occurrence_key TEXT NOT NULL,
			subject_key TEXT NOT NULL DEFAULT '',
			observed_at TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(definition_id, provider, occurrence_key),
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id)
		);
		CREATE TABLE IF NOT EXISTS automation_runs (
			id TEXT PRIMARY KEY,
			definition_id TEXT NOT NULL,
			occurrence_id TEXT NOT NULL UNIQUE,
			definition_revision INTEGER NOT NULL,
			snapshot_json TEXT NOT NULL,
			state TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			ticket_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			resolved_location_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			delivered_at TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id),
			FOREIGN KEY(occurrence_id) REFERENCES automation_occurrences(id)
		);
		CREATE INDEX IF NOT EXISTS idx_automation_runs_definition_created
			ON automation_runs(definition_id, created_at DESC);
	`},
	{74, "add GitHub automation observation and continuity", `
		CREATE TABLE IF NOT EXISTS automation_provider_cursors (
			definition_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			scope TEXT NOT NULL,
			observed_at TEXT NOT NULL,
			PRIMARY KEY(definition_id, provider, scope),
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id)
		);
		CREATE TABLE IF NOT EXISTS automation_review_request_edges (
			definition_id TEXT NOT NULL,
			subject_key TEXT NOT NULL,
			host TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 0,
			cycle INTEGER NOT NULL DEFAULT 0,
			accepted_cycle INTEGER NOT NULL DEFAULT 0,
			last_observed_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(definition_id, subject_key),
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id)
		);
		CREATE INDEX IF NOT EXISTS idx_automation_review_edges_host
			ON automation_review_request_edges(definition_id, host, active);
		CREATE TABLE IF NOT EXISTS automation_continuity_bindings (
			definition_id TEXT NOT NULL,
			continuity_key TEXT NOT NULL,
			ticket_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(definition_id, continuity_key),
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id)
		);
		CREATE TABLE IF NOT EXISTS automation_ticket_occurrence_events (
			run_id TEXT PRIMARY KEY,
			ticket_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES automation_runs(id),
			FOREIGN KEY(ticket_id) REFERENCES tickets(id)
		);
	`},
	{75, "persist the applied automation definition YAML", ""},
	{76, "automations v2: definitions clean slate", ""},
	{77, "automations v2: explicit run and binding state", ""},
	{78, "add launch_intent to sessions", ""},
	{79, "convert recoverable flag to session state", ""},
	{80, "create file_activity table", `CREATE TABLE IF NOT EXISTS file_activity (
		path TEXT NOT NULL,
		source TEXT NOT NULL,
		session_id TEXT,
		last_at TEXT NOT NULL,
		count INTEGER NOT NULL DEFAULT 1,
		PRIMARY KEY(path, source)
	);
	CREATE INDEX IF NOT EXISTS idx_file_activity_last_at ON file_activity(last_at DESC);`},
	{81, "add turn stamps to sessions", ""},
	{82, "define the ticket participant rule once as a view", `
		DROP VIEW IF EXISTS ticket_participants;
		CREATE VIEW ticket_participants (ticket_id, identity) AS
			SELECT id, assignee FROM tickets WHERE assignee != ''
			UNION
			SELECT e.ticket_id, e.author FROM ticket_events e
			WHERE e.author != '' AND e.kind != 'commented'
				AND NOT (
					e.kind = 'created' AND EXISTS (
						SELECT 1 FROM ticket_role_owners ro WHERE ro.ticket_id = e.ticket_id
					)
				)
			UNION
			SELECT ticket_id, identity FROM ticket_subscriptions WHERE identity != ''
			UNION
			SELECT ticket_id, ('role:' || role) FROM ticket_role_owners WHERE role != '';
	`},
	{83, "reserve tickets during delegation preparation", `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_delegation_operations_active_ticket
			ON delegation_operations(ticket_id)
			WHERE ticket_id != '' AND state IN ('accepted', 'preparing');
	`},
	{84, "create event bus log and consumer cursors", `CREATE TABLE IF NOT EXISTS bus_events (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    subject    TEXT NOT NULL DEFAULT '',
    payload    TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bus_events_name ON bus_events(name, seq);
CREATE INDEX IF NOT EXISTS idx_bus_events_subject ON bus_events(subject, seq);
CREATE TABLE IF NOT EXISTS bus_consumers (
    name       TEXT PRIMARY KEY,
    cursor     INTEGER NOT NULL DEFAULT 0,
    filter     TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL DEFAULT ''
);`},
	{85, "add the snooze deadline to sessions", ""},
	{86, "create session annotation drafts table", `CREATE TABLE IF NOT EXISTS session_annotation_drafts (
		session_id TEXT PRIMARY KEY,
		annotations_json TEXT NOT NULL,
		generation INTEGER NOT NULL,
		tombstone_generation INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL
	)`},
	{87, "create the durable job queue", `CREATE TABLE IF NOT EXISTS jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    unique_key   TEXT NOT NULL DEFAULT '',
    priority     INTEGER NOT NULL DEFAULT 0,
    payload      TEXT NOT NULL DEFAULT '',
    result       TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 0,
    scheduled_at TEXT NOT NULL,
    last_error   TEXT NOT NULL DEFAULT '',
    requeued     INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
-- Coalescing identity. Partial, because a job without a unique key is
-- deliberately distinct and any number of them may coexist.
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_unique_key ON jobs(kind, unique_key) WHERE unique_key <> '';
-- The dispatch selection: claimable rows in the order they are claimed.
CREATE INDEX IF NOT EXISTS idx_jobs_eligible ON jobs(state, scheduled_at, priority DESC);`},
	{88, "create the document store", `CREATE TABLE IF NOT EXISTS documents (
    namespace  TEXT NOT NULL,
    collection TEXT NOT NULL,
    id         TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (namespace, collection, id)
);
-- Every query is scoped to one collection, so the primary key is also the
-- access path: a query scans the (namespace, collection) prefix and no further.
-- Declared fields carry no index of their own in v1 (see the A3 plan) — the
-- declaration is the contract, and the physical index waits for a measurement.
CREATE TABLE IF NOT EXISTS document_collections (
    namespace   TEXT NOT NULL,
    collection  TEXT NOT NULL,
    fields_json TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (namespace, collection)
);`},
	{89, "rebuild the document store as a table per collection", ``},
	{90, "give every document a revision", ``},
	{91, "store document timestamps in an encoding that sorts", ``},
	{92, "add the session pin and the satellite parent to sessions", ``},
	{93, "add the note to session annotation drafts", ``},
	{94, "store job and notification timestamps in an encoding that sorts", ``},
	{95, "store turn, cursor and listing timestamps in an encoding that sorts", ``},
	{96, "add the context-window cap pin to sessions", ``},
	{97, "add the activity line and its transcript cursor to sessions", ``},
	{99, "attribute role-acted ticket events to the role", ``},
	{100, "add the severity level to notifications", ``},
	{101, "create agent messages and drop the dispatch message table", `
		CREATE TABLE IF NOT EXISTS agent_messages (
			id TEXT PRIMARY KEY,
			sender_session_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			delivered_at TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_agent_messages_target_queued
			ON agent_messages(target_session_id, delivered_at, created_at, id);
		CREATE INDEX IF NOT EXISTS idx_agent_messages_sender_created
			ON agent_messages(sender_session_id, target_session_id, created_at);
		DROP TABLE IF EXISTS chief_of_staff_dispatch_messages;
	`},
	{102, "create the app registry", `CREATE TABLE IF NOT EXISTS apps (
    name               TEXT PRIMARY KEY,
    current_version_id INTEGER,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS app_versions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    app_name      TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    declaration   TEXT NOT NULL,
    artifact_path TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    UNIQUE(app_name, content_hash)
);
-- History for one app, newest first: the rollback picker's access path.
CREATE INDEX IF NOT EXISTS idx_app_versions_app ON app_versions(app_name, id DESC);
CREATE TABLE IF NOT EXISTS app_invocations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    app_name      TEXT NOT NULL,
    version_id    INTEGER NOT NULL,
    event_seq     INTEGER NOT NULL,
    event_name    TEXT NOT NULL DEFAULT '',
    event_subject TEXT NOT NULL DEFAULT '',
    handler       TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL,
    error         TEXT NOT NULL DEFAULT '',
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    started_at    TEXT NOT NULL
);
-- One app's recent invocations, and the age window retention trims by. Both
-- read this index; started_at is written fixed-width so text order is time
-- order.
CREATE INDEX IF NOT EXISTS idx_app_invocations_app ON app_invocations(app_name, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_app_invocations_started ON app_invocations(started_at);`},
	{103, "record the previously-serving version of each app", ``},
	{104, "remember a parked supervised child across daemon restarts", `CREATE TABLE IF NOT EXISTS supervised_parks (
    child           TEXT PRIMARY KEY,
    parked_at       TEXT NOT NULL,
    restart_attempt INTEGER NOT NULL DEFAULT 0,
    exit_at         TEXT NOT NULL DEFAULT '',
    exit_code       INTEGER,
    exit_signal     TEXT NOT NULL DEFAULT '',
    exit_error      TEXT NOT NULL DEFAULT ''
);`},
	{105, "walk an app's serving history as a chain", ``},
	{106, "add durable per-session token cost state", ``},
	{107, "record which ticket event a delivery covered", ``},
	{109, "auto mode config, proposals and denials", `CREATE TABLE IF NOT EXISTS automode_config (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    enabled_default  INTEGER NOT NULL DEFAULT 1,
    environment      TEXT NOT NULL DEFAULT '[]',
    allow_patterns   TEXT NOT NULL DEFAULT '[]',
    hard_deny        TEXT NOT NULL DEFAULT '[]',
    classifier_model TEXT NOT NULL DEFAULT '',
    escalation_model TEXT NOT NULL DEFAULT '',
    updated_at       TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS automode_proposals (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT NOT NULL,
    target      TEXT NOT NULL DEFAULT '',
    value       TEXT NOT NULL,
    proposed_by TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'pending',
    created_at  TEXT NOT NULL,
    resolved_at TEXT NOT NULL DEFAULT ''
);
-- The review list: everything still pending, oldest first.
CREATE INDEX IF NOT EXISTS idx_automode_proposals_state ON automode_proposals(state, id);
CREATE TABLE IF NOT EXISTS automode_denials (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL DEFAULT '',
    tool       TEXT NOT NULL DEFAULT '',
    signature  TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_automode_denials_recent ON automode_denials(id DESC);`},
	{110, "record which rule denied an auto mode call", ``},
	{111, "one pending auto mode proposal per asker", `CREATE UNIQUE INDEX IF NOT EXISTS
    idx_automode_proposals_pending_ask
    ON automode_proposals(kind, target, value, proposed_by)
    WHERE state = 'pending';`},
	{114, "auto mode judges from an ordered model list per layer", ``},
	{115, "record app reconciliation owed across cursor fences", `CREATE TABLE IF NOT EXISTS app_reconcile_requests (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    app_name            TEXT NOT NULL,
    reason              TEXT NOT NULL,
    version_id          INTEGER NOT NULL,
    through_seq         INTEGER NOT NULL,
    previous_version_id INTEGER,
    cursor              INTEGER,
    earliest            INTEGER,
    missed              INTEGER,
    created_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_app_reconcile_requests_pending
    ON app_reconcile_requests(app_name, id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_app_reconcile_requests_gap
    ON app_reconcile_requests(app_name, cursor, earliest, through_seq)
    WHERE reason = 'gap';
CREATE TABLE IF NOT EXISTS app_reconcile_progress (
    app_name             TEXT PRIMARY KEY,
    completed_request_id INTEGER NOT NULL,
    updated_at           TEXT NOT NULL
);`},
	{116, "record app reconcile invocation lifecycles", ``},
	{117, "index automation provenance lookups", `
		CREATE INDEX IF NOT EXISTS idx_automation_runs_session_created
			ON automation_runs(session_id, created_at DESC, id DESC);
		CREATE INDEX IF NOT EXISTS idx_automation_runs_ticket_created
			ON automation_runs(ticket_id, created_at DESC, id DESC);
	`},
	{118, "the ticket board's font scale becomes the garden's", `
		INSERT OR IGNORE INTO settings (key, value)
			SELECT 'gardenScale', value FROM settings WHERE key = 'ticketBoardScale';
		DELETE FROM settings WHERE key = 'ticketBoardScale';
	`},
	{119, "record review automation activation baselines", ``},
	{120, "watch seeds and coalesce their unread bells", `
		CREATE TABLE IF NOT EXISTS garden_seed_watches (
			watcher_session_id TEXT NOT NULL,
			seed_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(watcher_session_id, seed_id)
		);
		CREATE INDEX IF NOT EXISTS idx_garden_seed_watches_seed
			ON garden_seed_watches(seed_id, watcher_session_id);
		CREATE TABLE IF NOT EXISTS garden_seed_bells (
			watcher_session_id TEXT NOT NULL,
			seed_id TEXT NOT NULL,
			event_kind TEXT NOT NULL,
			message_id TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			PRIMARY KEY(watcher_session_id, seed_id)
		);
	`},
	{121, "record the last observed model request per session", `
		ALTER TABLE sessions ADD COLUMN last_model_request_at TEXT;
		UPDATE sessions SET last_model_request_at = state_updated_at
			WHERE last_model_request_at IS NULL OR last_model_request_at = '';
	`},
	{122, "journal the one-time legacy ticket recovery", `
		CREATE TABLE IF NOT EXISTS legacy_ticket_recovery_runs (
			version                 INTEGER PRIMARY KEY,
			state                   TEXT NOT NULL,
			inventory_json          TEXT NOT NULL,
			counts_json             TEXT NOT NULL DEFAULT '{}',
			warning_notification_id TEXT NOT NULL DEFAULT '',
			started_at              TEXT NOT NULL,
			recovery_at             TEXT NOT NULL,
			finished_at             TEXT NOT NULL DEFAULT '',
			terminal_error          TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS legacy_ticket_recovery_sources (
			run_version INTEGER NOT NULL,
			path        TEXT NOT NULL,
			family      TEXT NOT NULL,
			size        INTEGER NOT NULL,
			mod_time_ns INTEGER NOT NULL,
			sha256      TEXT NOT NULL,
			state       TEXT NOT NULL DEFAULT 'pending',
			detail      TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (run_version, path)
		);
		CREATE INDEX IF NOT EXISTS idx_legacy_ticket_recovery_sources_state
			ON legacy_ticket_recovery_sources(run_version, state);
		CREATE TABLE IF NOT EXISTS legacy_ticket_recovery_items (
			fingerprint              TEXT PRIMARY KEY,
			run_version              INTEGER NOT NULL,
			source_kind              TEXT NOT NULL,
			source_key               TEXT NOT NULL,
			ticket_id                TEXT NOT NULL DEFAULT '',
			recovered_local_identity TEXT NOT NULL DEFAULT '',
			result                   TEXT NOT NULL,
			detail                   TEXT NOT NULL DEFAULT '',
			created_at               TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_legacy_ticket_recovery_items_ticket
			ON legacy_ticket_recovery_items(ticket_id, fingerprint);
		CREATE TABLE IF NOT EXISTS legacy_ticket_seed_links (
			ticket_id               TEXT PRIMARY KEY,
			seed_id                 TEXT NOT NULL UNIQUE,
			source_kind             TEXT NOT NULL,
			evidence_fingerprint    TEXT NOT NULL,
			original_terminal_state TEXT NOT NULL,
			created_at              TEXT NOT NULL
		);
	`},
	{123, "persist session transcript bindings", ``},
	{124, "one model list for both classifier passes", ``},
	{125, "the environment becomes slots the rules can look up", ``},
	{126, "seed slugs drop their stop words", ``},
	{127, "pull requests a session's agent opened", `
		CREATE TABLE IF NOT EXISTS session_pull_requests (
			session_id        TEXT NOT NULL,
			pr_id             TEXT NOT NULL,
			repository        TEXT NOT NULL,
			number            INTEGER NOT NULL,
			url               TEXT NOT NULL,
			created_at        TEXT NOT NULL,
			title             TEXT NOT NULL DEFAULT '',
			draft             INTEGER NOT NULL DEFAULT 0,
			state             TEXT NOT NULL DEFAULT 'open',
			ci_status         TEXT NOT NULL DEFAULT '',
			review_status     TEXT NOT NULL DEFAULT '',
			mergeable_state   TEXT NOT NULL DEFAULT '',
			head_sha          TEXT NOT NULL DEFAULT '',
			head_branch       TEXT NOT NULL DEFAULT '',
			status_fetched_at TEXT NOT NULL DEFAULT '',
			last_activity_at  TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (session_id, pr_id)
		);
		CREATE INDEX IF NOT EXISTS idx_session_pull_requests_session
			ON session_pull_requests(session_id, created_at DESC);
	`},
	{128, "session pull request refresh keeps its own pacing cursor", ``},
	{129, "the screen a session showed when its process exited", `
		CREATE TABLE IF NOT EXISTS session_exit_screens (
			session_id  TEXT PRIMARY KEY,
			text        TEXT NOT NULL DEFAULT '',
			cols        INTEGER NOT NULL DEFAULT 0,
			rows        INTEGER NOT NULL DEFAULT 0,
			exit_code   INTEGER NOT NULL DEFAULT 0,
			exit_signal TEXT NOT NULL DEFAULT '',
			exited_at   TEXT NOT NULL
		);
	`},
	{130, "intentional session teardown survives session removal", `
		CREATE TABLE IF NOT EXISTS session_teardown_tombstones (
			session_id        TEXT PRIMARY KEY,
			requested_at      TEXT NOT NULL,
			driver_plugin_name TEXT NOT NULL DEFAULT '',
			driver_run_id      TEXT NOT NULL DEFAULT '',
			driver_report_seq  INTEGER NOT NULL DEFAULT 0
		);
		INSERT OR IGNORE INTO session_teardown_tombstones (session_id, requested_at)
			SELECT id, closed_intentionally_at FROM sessions WHERE closed_intentionally_at <> '';
	`},
	{131, "repair partial agent driver cursor schemas", ``},
	{132, "separate agent mailbox receipts from message content", `
		CREATE TABLE IF NOT EXISTS peer_messages (
			id                TEXT PRIMARY KEY,
			sender_session_id TEXT NOT NULL,
			body              TEXT NOT NULL,
			created_at        TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS agent_mailbox_items (
			id                   TEXT PRIMARY KEY,
			recipient_session_id TEXT NOT NULL,
			kind                 TEXT NOT NULL,
			source_id            TEXT NOT NULL DEFAULT '',
			coalesce_key         TEXT NOT NULL DEFAULT '',
			hint                 TEXT NOT NULL DEFAULT '',
			prompt               TEXT NOT NULL DEFAULT '',
			created_at           TEXT NOT NULL,
			notified_at          TEXT NOT NULL DEFAULT '',
			read_at              TEXT NOT NULL DEFAULT '',
			CHECK (read_at = '' OR notified_at != '')
		);

		INSERT INTO peer_messages (id, sender_session_id, body, created_at)
		SELECT m.id, m.sender_session_id, m.content, m.created_at
		FROM agent_messages m
		LEFT JOIN garden_seed_bells b ON b.message_id = m.id
		WHERE b.message_id IS NULL AND m.sender_session_id != '';

		INSERT INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt,
			 created_at, notified_at, read_at)
		SELECT m.id, m.target_session_id, 'garden_seed', b.seed_id, b.seed_id,
		       b.event_kind, '', m.created_at, m.delivered_at, ''
		FROM garden_seed_bells b
		JOIN agent_messages m ON m.id = b.message_id;

		INSERT INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt,
			 created_at, notified_at, read_at)
		SELECT m.id, m.target_session_id, 'peer_message', m.id, '', '', '',
		       m.created_at, m.delivered_at, m.delivered_at
		FROM agent_messages m
		LEFT JOIN garden_seed_bells b ON b.message_id = m.id
		WHERE b.message_id IS NULL AND m.sender_session_id != '';

		INSERT INTO agent_mailbox_items
			(id, recipient_session_id, kind, source_id, coalesce_key, hint, prompt,
			 created_at, notified_at, read_at)
		SELECT m.id, m.target_session_id, 'maintenance_prompt', '', '', '', m.content,
		       m.created_at, m.delivered_at, m.delivered_at
		FROM agent_messages m
		LEFT JOIN garden_seed_bells b ON b.message_id = m.id
		WHERE b.message_id IS NULL AND m.sender_session_id = '';

		CREATE INDEX IF NOT EXISTS idx_agent_mailbox_recipient_queued
			ON agent_mailbox_items(recipient_session_id, notified_at, created_at, id);
		CREATE INDEX IF NOT EXISTS idx_agent_mailbox_source
			ON agent_mailbox_items(kind, source_id, recipient_session_id);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_mailbox_unread_coalesce
			ON agent_mailbox_items(recipient_session_id, kind, coalesce_key)
			WHERE coalesce_key != '' AND read_at = '';
		CREATE INDEX IF NOT EXISTS idx_peer_messages_sender_created
			ON peer_messages(sender_session_id, created_at);

		DROP TABLE garden_seed_bells;
		DROP TABLE agent_messages;
	`},
	{133, "index unread agent mailbox delivery", `
		DROP INDEX IF EXISTS idx_agent_mailbox_recipient_queued;
		CREATE INDEX IF NOT EXISTS idx_agent_mailbox_recipient_unread
			ON agent_mailbox_items(recipient_session_id, created_at, id)
			WHERE read_at = '';
	`},
	{134, "persist delegation preferences", `CREATE TABLE IF NOT EXISTS delegation_preferences (id INTEGER PRIMARY KEY CHECK (id = 1), config TEXT NOT NULL);`},
}

const migration99SQL = `
		UPDATE ticket_events SET author_role = (
			SELECT ro.role FROM ticket_role_owners ro
			WHERE ro.ticket_id = ticket_events.ticket_id
				AND ro.created_at = ticket_events.created_at
			LIMIT 1
		)
		WHERE kind IN ('created', 'assigned', 'status_changed') AND EXISTS (
			SELECT 1 FROM ticket_role_owners ro
			WHERE ro.ticket_id = ticket_events.ticket_id
				AND ro.created_at = ticket_events.created_at
		);

		DELETE FROM ticket_subscriptions
		WHERE EXISTS (
			SELECT 1 FROM ticket_events e
			WHERE e.ticket_id = ticket_subscriptions.ticket_id
				AND e.author_role != ''
				AND e.author = ticket_subscriptions.identity
				AND e.created_at = ticket_subscriptions.created_at
		);

		DROP VIEW IF EXISTS ticket_participants;
		CREATE VIEW ticket_participants (ticket_id, identity) AS
			SELECT id, assignee FROM tickets WHERE assignee != ''
			UNION
			SELECT e.ticket_id, e.author FROM ticket_events e
			WHERE e.author != '' AND e.kind != 'commented' AND e.author_role = ''
			UNION
			SELECT e.ticket_id, ('role:' || e.author_role) FROM ticket_events e
			WHERE e.kind != 'commented' AND e.author_role != ''
			UNION
			SELECT ticket_id, identity FROM ticket_subscriptions WHERE identity != ''
			UNION
			SELECT ticket_id, ('role:' || role) FROM ticket_role_owners WHERE role != '';
`

func applyMigration99(tx *sql.Tx) error {
	has, err := columnExists(tx, "ticket_events", "author_role")
	if err != nil {
		return err
	}
	if !has {
		if _, err := tx.Exec("ALTER TABLE ticket_events ADD COLUMN author_role TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	_, err = tx.Exec(migration99SQL)
	return err
}

// A deferred transaction that reads before it writes cannot upgrade while
// another connection holds the write lock: SQLite fails it instantly, no wait.
func sqliteDSN(dbPath string) string {
	if dbPath == ":memory:" {
		return dbPath
	}
	u := &url.URL{Scheme: "file", Path: dbPath}
	query := u.Query()
	query.Set("_txlock", "immediate")
	u.RawQuery = query.Encode()
	return u.String()
}

func OpenDB(dbPath string) (*sql.DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", sqliteDSN(dbPath))
	if err != nil {
		return nil, err
	}

	if dbPath == ":memory:" {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(sqliteFileConnectionPoolSize)
		db.SetMaxIdleConns(sqliteFileConnectionPoolSize)
	}

	if _, err := db.Exec(baseSchema); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrateDB(db, dbPath); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func migrateDB(db *sql.DB, dbPath string) error {
	if err := seedLegacyDB(db); err != nil {
		return fmt.Errorf("seeding legacy db: %w", err)
	}

	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return fmt.Errorf("getting schema version: %w", err)
	}

	if currentVersion > 0 && dbPath != "" && dbPath != ":memory:" && len(migrations) > 0 {
		latest := migrations[len(migrations)-1].version
		if currentVersion < latest {
			if path, err := backupPreMigration(db, dbPath, currentVersion); err != nil {
				if path != "" {
					log.Printf("[store] pre-migration backup written to %s (schema v%d -> v%d), but pruning old pre-migration snapshots failed: %v", path, currentVersion, latest, err)
				} else {
					log.Printf("[store] pre-migration backup failed (schema v%d -> v%d): %v; proceeding with migrations", currentVersion, latest, err)
				}
			} else {
				log.Printf("[store] pre-migration backup written to %s (schema v%d -> v%d)", path, currentVersion, latest)
			}
		}
	}

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("starting transaction for migration %d: %w", m.version, err)
		}

		if m.version == 20 {
			if err := applyMigration20(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 21 {
			if err := applyMigration21(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 22 {
			if err := applyMigration22(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 23 {
			if err := applyMigration23(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 28 {
			if err := applyMigration28(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 29 {
			if err := applyMigration29(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 31 {
			if err := applyMigration31(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 33 {
			if err := applyMigration33(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 34 {
			if err := applyMigration34(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 35 {
			if err := applyMigration35(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 37 {
			if err := applyMigration37(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 38 {
			if err := applyMigration38(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 39 {
			if err := applyMigration39(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 40 {
			if err := applyMigration40(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 41 {
			if err := applyMigration41(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 45 {
			if err := applyMigration45(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 48 {
			if err := applyMigration48(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 49 || m.version == 50 {
			if err := applyMigration49(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 52 {
			if err := applyMigration52(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 53 {
			if err := applyMigration53(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 54 {
			if err := applyMigration54(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 57 {
			if err := applyMigration57(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 60 {
			if err := applyMigration60(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 64 {
			if err := applyMigration64(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 65 {
			if err := applyMigration65(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 71 {
			if err := applyMigration71(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 72 {
			if err := applyMigration72(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 73 {
			if _, err := tx.Exec(m.sql); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
			if err := applyMigration73(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 75 {
			if err := applyMigration75(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 76 {
			if err := applyMigration76(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 77 {
			if err := applyMigration77(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 78 {
			if err := applyMigration78(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 79 {
			if err := applyMigration79(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 81 {
			if err := applyMigration81(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 85 {
			if err := applyMigration85(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 89 {
			if err := applyMigration89(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 90 {
			if err := applyMigration90(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 91 {
			if err := applyMigration91(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 92 {
			if err := applyMigration92(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 93 {
			if err := applyMigration93(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 94 {
			if err := applyMigration94(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 95 {
			if err := applyMigration95(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 96 {
			if err := applyMigration96(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 97 {
			if err := applyMigration97(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 99 {
			if err := applyMigration99(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 100 {
			if err := applyMigration100(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 103 {
			if err := applyMigration103(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 105 {
			if err := applyMigration105(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 106 {
			if err := applyMigration106(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 107 {
			if err := applyMigration107(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 110 {
			if err := applyMigration110(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 116 {
			if err := applyMigration116(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 119 {
			if err := applyMigration119(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 121 {
			if err := applyMigration121(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 123 {
			if err := applyMigration123(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 114 {
			if err := applyMigration114(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 124 {
			if err := applyMigration124(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 125 {
			if err := applyMigration125(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 126 {
			if err := applyMigration126(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 128 {
			if err := applyMigration128(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 131 {
			if err := applyMigration131(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 134 {
			if _, err := tx.Exec(m.sql); err != nil {
				tx.Rollback()
				return err
			}
			has, err := columnExists(tx, "delegation_operations", "resolved_preferences")
			if err == nil && !has {
				_, err = tx.Exec("ALTER TABLE delegation_operations ADD COLUMN resolved_preferences TEXT NOT NULL DEFAULT ''")
			}
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else if m.version == 132 {
			if err := applyMigration132(tx, m.sql); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else {
			if _, err := tx.Exec(m.sql); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		}

		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
			m.version,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", m.version, err)
		}
	}

	return nil
}

func applyMigration121(tx *sql.Tx) error {
	has, err := columnExists(tx, "sessions", "last_model_request_at")
	if err != nil {
		return err
	}
	if !has {
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN last_model_request_at TEXT"); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE sessions SET last_model_request_at = state_updated_at
		WHERE last_model_request_at IS NULL OR last_model_request_at = ''`)
	return err
}

func applyMigration123(tx *sql.Tx) error {
	has, err := columnExists(tx, "sessions", "transcript_path")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = tx.Exec("ALTER TABLE sessions ADD COLUMN transcript_path TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration128(tx *sql.Tx) error {
	has, err := columnExists(tx, "session_pull_requests", "status_checked_at")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE session_pull_requests ADD COLUMN status_checked_at TEXT NOT NULL DEFAULT ''`)
	return err
}

func applyMigration131(tx *sql.Tx) error {
	columns := []struct {
		name string
		sql  string
	}{
		{"agent_driver_plugin_name", `ALTER TABLE sessions ADD COLUMN agent_driver_plugin_name TEXT NOT NULL DEFAULT ''`},
		{"agent_driver_run_id", `ALTER TABLE sessions ADD COLUMN agent_driver_run_id TEXT NOT NULL DEFAULT ''`},
		{"agent_driver_report_seq", `ALTER TABLE sessions ADD COLUMN agent_driver_report_seq INTEGER NOT NULL DEFAULT 0`},
	}
	for _, column := range columns {
		has, err := columnExists(tx, "sessions", column.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := tx.Exec(column.sql); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyMigration132(tx *sql.Tx, migrationSQL string) error {
	mailboxExists, err := tableExists(tx, "agent_mailbox_items")
	if err != nil {
		return err
	}
	legacyExists, err := tableExists(tx, "agent_messages")
	if err != nil {
		return err
	}
	if mailboxExists && !legacyExists {
		return nil
	}
	if !legacyExists {
		return fmt.Errorf("neither agent_messages nor the migrated agent_mailbox_items table exists")
	}
	_, err = tx.Exec(migrationSQL)
	return err
}

func applyMigration107(tx *sql.Tx) error {
	has, err := columnExists(tx, "ticket_delivery_attention", "delivered_through_seq")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE ticket_delivery_attention ADD COLUMN delivered_through_seq INTEGER NOT NULL DEFAULT 0`)
	return err
}

func applyMigration116(tx *sql.Tx) error {
	hadKind, err := columnExists(tx, "app_invocations", "kind")
	if err != nil {
		return err
	}
	columns := []struct {
		name string
		sql  string
	}{
		{"kind", `ALTER TABLE app_invocations ADD COLUMN kind TEXT NOT NULL DEFAULT 'subscription'`},
		{"reconcile_reason", `ALTER TABLE app_invocations ADD COLUMN reconcile_reason TEXT NOT NULL DEFAULT ''`},
		{"through_request_id", `ALTER TABLE app_invocations ADD COLUMN through_request_id INTEGER`},
		{"through_seq", `ALTER TABLE app_invocations ADD COLUMN through_seq INTEGER`},
		{"finished_at", `ALTER TABLE app_invocations ADD COLUMN finished_at TEXT`},
	}
	for _, column := range columns {
		exists, err := columnExists(tx, "app_invocations", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := tx.Exec(column.sql); err != nil {
			return err
		}
	}
	if hadKind {
		return nil
	}
	_, err = tx.Exec(`UPDATE app_invocations
		SET kind = CASE event_name
			WHEN 'app.command' THEN 'command'
			WHEN 'app.view.crashed' THEN 'view'
			ELSE 'subscription'
		END`)
	return err
}

func applyMigration119(tx *sql.Tx) error {
	has, err := columnExists(tx, "automation_review_request_edges", "baseline_cycle")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE automation_review_request_edges ADD COLUMN baseline_cycle INTEGER NOT NULL DEFAULT 0`)
	return err
}

func applyMigration73(tx *sql.Tx) error {
	if _, err := tx.Exec(delegationOperationsSchema); err != nil {
		return err
	}
	if err := applyMigration71(tx); err != nil {
		return err
	}
	if err := applyMigration72(tx); err != nil {
		return err
	}

	exists, err := columnExists(tx, "tickets", "automation_run_id")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(`ALTER TABLE tickets ADD COLUMN automation_run_id TEXT`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_automation_run ON tickets(automation_run_id) WHERE automation_run_id IS NOT NULL`)
	return err
}

func applyMigration20(tx *sql.Tx) error {
	hasHost, err := columnExists(tx, "prs", "host")
	if err != nil {
		return err
	}
	if !hasHost {
		if _, err := tx.Exec("ALTER TABLE prs ADD COLUMN host TEXT NOT NULL DEFAULT 'github.com'"); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("UPDATE prs SET id = 'github.com:' || id WHERE id NOT LIKE '%:%'"); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE pr_interactions SET pr_id = 'github.com:' || pr_id WHERE pr_id NOT LIKE '%:%'"); err != nil {
		return err
	}
	if _, err := tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_prs_host_repo_number ON prs(host, repo, number)"); err != nil {
		return err
	}
	return nil
}

func applyMigration21(tx *sql.Tx) error {
	hasAgent, err := columnExists(tx, "sessions", "agent")
	if err != nil {
		return err
	}
	if hasAgent {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT 'codex'"); err != nil {
		return err
	}
	return nil
}

func applyMigration22(tx *sql.Tx) error {
	hasRecoverable, err := columnExists(tx, "sessions", "recoverable")
	if err != nil {
		return err
	}
	if hasRecoverable {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN recoverable INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func applyMigration23(tx *sql.Tx) error {
	hasResumeSessionID, err := columnExists(tx, "sessions", "resume_session_id")
	if err != nil {
		return err
	}
	if hasResumeSessionID {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN resume_session_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func applyMigration60(tx *sql.Tx) error {
	hasReconciledAt, err := columnExists(tx, "tickets", "reconciled_at")
	if err != nil {
		return err
	}
	if hasReconciledAt {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE tickets ADD COLUMN reconciled_at TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func applyMigration64(tx *sql.Tx) error {
	hasClosedIntentionallyAt, err := columnExists(tx, "sessions", "closed_intentionally_at")
	if err != nil {
		return err
	}
	if hasClosedIntentionallyAt {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN closed_intentionally_at TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func applyMigration57(tx *sql.Tx) error {
	hasResumeSessionID, err := columnExists(tx, "tickets", "resume_session_id")
	if err != nil {
		return err
	}
	if hasResumeSessionID {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE tickets ADD COLUMN resume_session_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func applyMigration45(tx *sql.Tx) error {
	hasStructuredReport, err := columnExists(tx, "chief_of_staff_dispatches", "structured_report_json")
	if err != nil {
		return err
	}
	if hasStructuredReport {
		return nil
	}
	_, err = tx.Exec(`
		ALTER TABLE chief_of_staff_dispatches
			ADD COLUMN structured_report_json TEXT NOT NULL DEFAULT ''
	`)
	return err
}

func applyMigration48(tx *sql.Tx) error {
	hasLabel, err := columnExists(tx, "recent_locations", "label")
	if err != nil {
		return err
	}
	if !hasLabel {
		return nil
	}
	_, err = tx.Exec("ALTER TABLE recent_locations DROP COLUMN label")
	return err
}

func applyMigration49(tx *sql.Tx) error {
	hasRank, err := columnExists(tx, "workspaces", "rank")
	if err != nil {
		return err
	}
	if !hasRank {
		if _, err := tx.Exec(`ALTER TABLE workspaces ADD COLUMN rank TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}

	rows, err := tx.Query(`SELECT id FROM workspaces WHERE rank = '' ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(ids) == 0 {
		return nil
	}

	seeds := rankkey.Seed(len(ids))
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE workspaces SET rank = ? WHERE id = ?`, seeds[i], id); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration52(tx *sql.Tx) error {
	oldExists, err := tableExists(tx, "workspace_context_janitor_backups")
	if err != nil {
		return err
	}
	newExists, err := tableExists(tx, "workspace_keeper_compact_backups")
	if err != nil {
		return err
	}
	switch {
	case oldExists && !newExists:
		if _, err := tx.Exec(`ALTER TABLE workspace_context_janitor_backups RENAME TO workspace_keeper_compact_backups`); err != nil {
			return err
		}
	case oldExists && newExists:
		if _, err := tx.Exec(`DROP TABLE workspace_context_janitor_backups`); err != nil {
			return err
		}
	}
	contextsExist, err := tableExists(tx, "workspace_contexts")
	if err != nil {
		return err
	}
	if contextsExist {
		if _, err := tx.Exec(
			`UPDATE workspace_contexts SET updated_by_session_id = 'attn-keeper' WHERE updated_by_session_id = 'attn-janitor'`,
		); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration54(tx *sql.Tx) error {
	exists, err := tableExists(tx, "workspaces")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasColumn, err := columnExists(tx, "workspaces", "pinned")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE workspaces ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`)
	return err
}

func applyMigration65(tx *sql.Tx) error {
	exists, err := tableExists(tx, "presentation_rounds")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasColumn, err := columnExists(tx, "presentation_rounds", "verdict")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE presentation_rounds ADD COLUMN verdict TEXT`)
	return err
}

func applyMigration71(tx *sql.Tx) error {
	exists, err := tableExists(tx, "delegation_operations")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasColumn, err := columnExists(tx, "delegation_operations", "worktree_token")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE delegation_operations ADD COLUMN worktree_token TEXT NOT NULL DEFAULT ''`)
	return err
}

func applyMigration72(tx *sql.Tx) error {
	exists, err := tableExists(tx, "delegation_operations")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasColumn, err := columnExists(tx, "delegation_operations", "chief_session_id")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE delegation_operations ADD COLUMN chief_session_id TEXT NOT NULL DEFAULT ''`)
	return err
}

func applyMigration75(tx *sql.Tx) error {
	exists, err := tableExists(tx, "automation_definitions")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasColumn, err := columnExists(tx, "automation_definitions", "spec_yaml")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE automation_definitions ADD COLUMN spec_yaml TEXT NOT NULL DEFAULT ''`)
	return err
}

func applyMigration76(tx *sql.Tx) error {
	exists, err := tableExists(tx, "automation_definitions")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, stmt := range []string{
		`DELETE FROM automation_ticket_occurrence_events`,
		`DELETE FROM automation_runs`,
		`DELETE FROM automation_occurrences`,
		`DELETE FROM automation_continuity_bindings`,
		`DELETE FROM automation_review_request_edges`,
		`DELETE FROM automation_provider_cursors`,
		`DROP TABLE automation_definitions`,
		`CREATE TABLE automation_definitions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			revision INTEGER NOT NULL,
			spec_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT NOT NULL DEFAULT ''
		)`,
		`UPDATE tickets SET automation_run_id = NULL WHERE automation_run_id IS NOT NULL`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration77(tx *sql.Tx) error {
	exists, err := tableExists(tx, "automation_runs")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, stmt := range []string{
		`DELETE FROM automation_ticket_occurrence_events`,
		`DELETE FROM automation_runs`,
		`DELETE FROM automation_occurrences`,
		`DELETE FROM automation_continuity_bindings`,
		`DELETE FROM automation_review_request_edges`,
		`DELETE FROM automation_provider_cursors`,
		`UPDATE tickets SET automation_run_id = NULL WHERE automation_run_id IS NOT NULL`,
		`DROP TABLE automation_runs`,
		`CREATE TABLE automation_runs (
			id TEXT PRIMARY KEY,
			definition_id TEXT NOT NULL,
			occurrence_id TEXT NOT NULL UNIQUE,
			definition_revision INTEGER NOT NULL,
			snapshot_json TEXT NOT NULL,
			state TEXT NOT NULL,
			cancel_reason TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			ticket_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			resolved_location_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			delivered_at TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id),
			FOREIGN KEY(occurrence_id) REFERENCES automation_occurrences(id)
		)`,
		`CREATE INDEX idx_automation_runs_definition_created
			ON automation_runs(definition_id, created_at DESC)`,
		`DROP TABLE automation_continuity_bindings`,
		`CREATE TABLE automation_continuity_bindings (
			id TEXT PRIMARY KEY,
			definition_id TEXT NOT NULL,
			continuity_key TEXT NOT NULL,
			ticket_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			released_reason TEXT NOT NULL DEFAULT '',
			released_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id)
		)`,
		`CREATE UNIQUE INDEX idx_automation_bindings_active
			ON automation_continuity_bindings(definition_id, continuity_key) WHERE status='active'`,
		`DROP TABLE automation_review_request_edges`,
		`CREATE TABLE automation_review_request_edges (
			definition_id TEXT NOT NULL,
			subject_key TEXT NOT NULL,
			host TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 0,
			cycle INTEGER NOT NULL DEFAULT 0,
			last_observed_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(definition_id, subject_key),
			FOREIGN KEY(definition_id) REFERENCES automation_definitions(id)
		)`,
		`CREATE INDEX idx_automation_review_edges_host
			ON automation_review_request_edges(definition_id, host, active)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration78(tx *sql.Tx) error {
	hasLaunchIntent, err := columnExists(tx, "sessions", "launch_intent")
	if err != nil {
		return err
	}
	if hasLaunchIntent {
		return nil
	}
	_, err = tx.Exec("ALTER TABLE sessions ADD COLUMN launch_intent TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration79(tx *sql.Tx) error {
	hasRecoverable, err := columnExists(tx, "sessions", "recoverable")
	if err != nil {
		return err
	}
	if !hasRecoverable {
		return nil
	}
	if _, err := tx.Exec("UPDATE sessions SET state = 'recoverable' WHERE recoverable = 1"); err != nil {
		return err
	}
	_, err = tx.Exec("ALTER TABLE sessions DROP COLUMN recoverable")
	return err
}

func applyMigration53(tx *sql.Tx) error {
	exists, err := tableExists(tx, "chief_of_staff_dispatches")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasColumn, err := columnExists(tx, "chief_of_staff_dispatches", "closed_state")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE chief_of_staff_dispatches ADD COLUMN closed_state TEXT NOT NULL DEFAULT ''`)
	return err
}

func tableExists(tx *sql.Tx, name string) (bool, error) {
	var got string
	err := tx.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func applyMigration28(tx *sql.Tx) error {
	hasResultText, err := columnExists(tx, "review_loop_iterations", "result_text")
	if err != nil {
		return err
	}
	if hasResultText {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE review_loop_iterations ADD COLUMN result_text TEXT"); err != nil {
		return err
	}
	return nil
}

func applyMigration29(tx *sql.Tx) error {
	hasChangeStats, err := columnExists(tx, "review_loop_iterations", "change_stats_json")
	if err != nil {
		return err
	}
	if hasChangeStats {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE review_loop_iterations ADD COLUMN change_stats_json TEXT"); err != nil {
		return err
	}
	return nil
}

func applyMigration31(tx *sql.Tx) error {
	hasEndpointID, err := columnExists(tx, "sessions", "endpoint_id")
	if err != nil {
		return err
	}
	if hasEndpointID {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN endpoint_id TEXT"); err != nil {
		return err
	}
	return nil
}

func applyMigration33(tx *sql.Tx) error {
	for _, col := range []string{"wont_fix", "wont_fix_by", "wont_fix_at"} {
		exists, err := columnExists(tx, "review_comments", col)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec("ALTER TABLE review_comments DROP COLUMN " + col); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration34(tx *sql.Tx) error {
	hasProfile, err := columnExists(tx, "endpoints", "profile")
	if err != nil {
		return err
	}
	if hasProfile {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE endpoints ADD COLUMN profile TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func applyMigration35(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS workspaces (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		directory TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	hasWorkspaceID, err := columnExists(tx, "sessions", "workspace_id")
	if err != nil {
		return err
	}
	if !hasWorkspaceID {
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN workspace_id TEXT"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_sessions_workspace_id ON sessions(workspace_id)"); err != nil {
		return err
	}
	return nil
}

func applyMigration37(tx *sql.Tx) error {
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS workspace_layouts (
			workspace_id TEXT PRIMARY KEY,
			active_pane_id TEXT NOT NULL,
			layout_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS workspace_layout_panes (
			workspace_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			runtime_id TEXT NOT NULL DEFAULT '',
			session_id TEXT,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (workspace_id, pane_id)
		);
		CREATE INDEX IF NOT EXISTS idx_workspace_layout_panes_runtime_id
			ON workspace_layout_panes(runtime_id);
		CREATE TABLE IF NOT EXISTS session_workspaces (
			session_id TEXT PRIMARY KEY,
			active_pane_id TEXT NOT NULL,
			layout_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS workspace_panes (
			session_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			runtime_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (session_id, pane_id)
		);

		INSERT OR IGNORE INTO workspaces (id, title, directory, created_at)
		SELECT 'workspace-' || id, label, directory, datetime('now')
		FROM sessions
		WHERE workspace_id IS NULL OR workspace_id = '';
		UPDATE sessions
		SET workspace_id = 'workspace-' || id
		WHERE workspace_id IS NULL OR workspace_id = '';
		INSERT OR IGNORE INTO workspaces (id, title, directory, created_at)
		SELECT workspace_id, label, directory, datetime('now')
		FROM sessions
		WHERE workspace_id IS NOT NULL AND workspace_id != '';

		INSERT OR IGNORE INTO workspace_layout_panes (
			workspace_id, pane_id, runtime_id, session_id, kind, title, created_at, updated_at
		)
		SELECT s.workspace_id, 'pane-' || p.session_id, p.session_id, p.session_id,
			'agent',
			CASE WHEN p.title = 'Session' THEN 'Agent' ELSE p.title END,
			p.created_at, p.updated_at
		FROM workspace_panes p
		JOIN sessions s ON s.id = p.session_id
		WHERE p.kind = 'main';

		INSERT OR IGNORE INTO workspace_layouts (workspace_id, active_pane_id, layout_json, updated_at)
		SELECT workspace_id, 'pane-' || id, '{"type":"pane","pane_id":"pane-' || id || '"}', datetime('now')
		FROM sessions WHERE workspace_id IS NOT NULL AND workspace_id != '';
		INSERT OR IGNORE INTO workspace_layout_panes (
			workspace_id, pane_id, runtime_id, session_id, kind, title, created_at, updated_at
		)
		SELECT workspace_id, 'pane-' || id, id, id, 'agent', 'Agent', datetime('now'), datetime('now')
		FROM sessions WHERE workspace_id IS NOT NULL AND workspace_id != '';

		DROP TABLE IF EXISTS canvas_workspace_panels;
		DROP TABLE IF EXISTS workspace_panes;
		DROP TABLE IF EXISTS session_workspaces;
	`); err != nil {
		return err
	}
	return nil
}

func applyMigration38(tx *sql.Tx) error {
	hasAgentMetadata, err := columnExists(tx, "sessions", "agent_metadata")
	if err != nil {
		return err
	}
	if hasAgentMetadata {
		return nil
	}
	_, err = tx.Exec("ALTER TABLE sessions ADD COLUMN agent_metadata TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration39(tx *sql.Tx) error {
	hasPluginName, err := columnExists(tx, "sessions", "agent_driver_plugin_name")
	if err != nil {
		return err
	}
	if !hasPluginName {
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN agent_driver_plugin_name TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	hasRunID, err := columnExists(tx, "sessions", "agent_driver_run_id")
	if err != nil {
		return err
	}
	if !hasRunID {
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN agent_driver_run_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	hasReportSeq, err := columnExists(tx, "sessions", "agent_driver_report_seq")
	if err != nil {
		return err
	}
	if !hasReportSeq {
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN agent_driver_report_seq INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration40(tx *sql.Tx) error {
	hasStatus, err := columnExists(tx, "workspace_layout_panes", "status")
	if err != nil {
		return err
	}
	if !hasStatus {
		if _, err := tx.Exec("ALTER TABLE workspace_layout_panes ADD COLUMN status TEXT NOT NULL DEFAULT 'ready'"); err != nil {
			return err
		}
	}
	hasError, err := columnExists(tx, "workspace_layout_panes", "error")
	if err != nil {
		return err
	}
	if !hasError {
		if _, err := tx.Exec("ALTER TABLE workspace_layout_panes ADD COLUMN error TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration41(tx *sql.Tx) error {
	hasWorkspaceMuted, err := columnExists(tx, "workspaces", "muted")
	if err != nil {
		return err
	}
	if !hasWorkspaceMuted {
		if _, err := tx.Exec("ALTER TABLE workspaces ADD COLUMN muted INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	hasSessionMuted, err := columnExists(tx, "sessions", "muted")
	if err != nil {
		return err
	}
	if hasSessionMuted {
		if _, err := tx.Exec(`
			UPDATE workspaces
			SET muted = 1
			WHERE id IN (
				SELECT DISTINCT workspace_id
				FROM sessions
				WHERE muted = 1
					AND workspace_id IS NOT NULL
					AND workspace_id != ''
			)
		`); err != nil {
			return err
		}
		if _, err := tx.Exec("ALTER TABLE sessions DROP COLUMN muted"); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration81(tx *sql.Tx) error {
	hasOpened, err := columnExists(tx, "sessions", "turn_opened_at")
	if err != nil {
		return err
	}
	hasSettled, err := columnExists(tx, "sessions", "turn_settled_at")
	if err != nil {
		return err
	}
	if !hasSettled {
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN turn_settled_at TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if hasOpened {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN turn_opened_at TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	_, err = tx.Exec(`
		UPDATE sessions SET turn_opened_at = state_since
		 WHERE state IN ('waiting_input', 'pending_approval', 'unknown')`)
	return err
}

func applyMigration85(tx *sql.Tx) error {
	has, err := columnExists(tx, "sessions", "turn_snoozed_until")
	if err != nil || has {
		return err
	}
	_, err = tx.Exec("ALTER TABLE sessions ADD COLUMN turn_snoozed_until TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration89(tx *sql.Tx) error {
	migrated, err := columnExists(tx, "document_collections", "id")
	if err != nil || migrated {
		return err
	}

	if _, err := tx.Exec(`ALTER TABLE document_collections RENAME TO document_collections_v88`); err != nil {
		return err
	}
	// AUTOINCREMENT, not rowid: a collection's table is doc_<id>, so a reused id
	// would point a name still held by an in-flight query at another collection.
	if _, err := tx.Exec(`CREATE TABLE document_collections (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace   TEXT NOT NULL,
    collection  TEXT NOT NULL,
    fields_json TEXT NOT NULL,
    updated_at  TEXT NOT NULL
)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX idx_document_collections_address
    ON document_collections(namespace, collection)`); err != nil {
		return err
	}

	collections, err := readV88Collections(tx)
	if err != nil {
		return err
	}
	carried := 0
	for _, c := range collections {
		n, err := carryV88Collection(tx, c)
		if err != nil {
			return fmt.Errorf("carrying %s/%s across: %w", c.namespace, c.collection, err)
		}
		carried += n
	}

	var stored int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&stored); err != nil {
		return err
	}
	if carried != stored {
		return fmt.Errorf("carried %d of %d stored documents; refusing to drop the rest", carried, stored)
	}

	if _, err := tx.Exec(`DROP TABLE documents`); err != nil {
		return err
	}
	_, err = tx.Exec(`DROP TABLE document_collections_v88`)
	return err
}

func applyMigration90(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id FROM document_collections`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		table := docstore.TableName(id)
		has, err := columnExists(tx, table, "rev")
		if err != nil {
			return fmt.Errorf("checking %s for a revision column: %w", table, err)
		}
		if has {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf(
			`ALTER TABLE %s ADD COLUMN rev INTEGER NOT NULL DEFAULT %d`, table, docstore.FirstRev)); err != nil {
			return fmt.Errorf("adding a revision column to %s: %w", table, err)
		}
	}
	return nil
}

func applyMigration92(tx *sql.Tx) error {
	hasPinned, err := columnExists(tx, "sessions", "pinned_at")
	if err != nil {
		return err
	}
	if !hasPinned {
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN pinned_at TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	hasParent, err := columnExists(tx, "sessions", "parent_session_id")
	if err != nil || hasParent {
		return err
	}
	_, err = tx.Exec("ALTER TABLE sessions ADD COLUMN parent_session_id TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration96(tx *sql.Tx) error {
	has, err := columnExists(tx, "sessions", "context_window_cap")
	if err != nil || has {
		return err
	}
	_, err = tx.Exec("ALTER TABLE sessions ADD COLUMN context_window_cap INTEGER NOT NULL DEFAULT 0")
	return err
}

func applyMigration97(tx *sql.Tx) error {
	for _, column := range []string{"activity", "activity_at", "activity_cursor"} {
		has, err := columnExists(tx, "sessions", column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := tx.Exec("ALTER TABLE sessions ADD COLUMN " + column + " TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration100(tx *sql.Tx) error {
	has, err := columnExists(tx, "notifications", "severity")
	if err != nil || has {
		return err
	}
	_, err = tx.Exec("ALTER TABLE notifications ADD COLUMN severity TEXT NOT NULL DEFAULT 'info'")
	return err
}

func applyMigration103(tx *sql.Tx) error {
	has, err := columnExists(tx, "apps", "previous_version_id")
	if err != nil || has {
		return err
	}
	_, err = tx.Exec("ALTER TABLE apps ADD COLUMN previous_version_id INTEGER")
	return err
}

func applyMigration110(tx *sql.Tx) error {
	has, err := columnExists(tx, "automode_denials", "rule")
	if err != nil || has {
		return err
	}
	_, err = tx.Exec("ALTER TABLE automode_denials ADD COLUMN rule TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration114(tx *sql.Tx) error {
	for _, layer := range []struct{ single, list string }{
		{"classifier_model", "classifier_models"},
		{"escalation_model", "escalation_models"},
	} {
		hasList, err := columnExists(tx, "automode_config", layer.list)
		if err != nil {
			return err
		}
		if !hasList {
			if _, err := tx.Exec(fmt.Sprintf(
				"ALTER TABLE automode_config ADD COLUMN %s TEXT NOT NULL DEFAULT '[]'", layer.list)); err != nil {
				return err
			}
		}
		hasSingle, err := columnExists(tx, "automode_config", layer.single)
		if err != nil {
			return err
		}
		if !hasSingle {
			continue
		}
		if _, err := tx.Exec(fmt.Sprintf(
			"UPDATE automode_config SET %s = json_array(%s) WHERE %s != '' AND (%s = '' OR %s = '[]')",
			layer.list, layer.single, layer.single, layer.list, layer.list)); err != nil {
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE automode_config DROP COLUMN %s", layer.single)); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration124(tx *sql.Tx) error {
	hasModels, err := columnExists(tx, "automode_config", "models")
	if err != nil {
		return err
	}
	if !hasModels {
		if _, err := tx.Exec(
			"ALTER TABLE automode_config ADD COLUMN models TEXT NOT NULL DEFAULT '[]'"); err != nil {
			return err
		}
	}
	hasClassifier, err := columnExists(tx, "automode_config", "classifier_models")
	if err != nil || !hasClassifier {
		return err
	}
	var classifier, escalation string
	err = tx.QueryRow(
		"SELECT classifier_models, escalation_models FROM automode_config WHERE id = 1").
		Scan(&classifier, &escalation)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	chosen, cerr := aModelWasEverPromoted(tx)
	if cerr != nil {
		return cerr
	}
	if err == nil && chosen {
		folded, ferr := foldModelLists(classifier, escalation)
		if ferr != nil {
			return ferr
		}
		if len(folded) > 0 {
			encoded, merr := json.Marshal(folded)
			if merr != nil {
				return merr
			}
			if _, err := tx.Exec(
				"UPDATE automode_config SET models = ? WHERE id = 1 AND (models = '' OR models = '[]')",
				string(encoded)); err != nil {
				return err
			}
		}
	}
	for _, column := range []string{"classifier_models", "escalation_models"} {
		if _, err := tx.Exec(fmt.Sprintf(
			"ALTER TABLE automode_config DROP COLUMN %s", column)); err != nil {
			return err
		}
	}
	return nil
}

// A promotion row is the only witness that a human picked these models.
func aModelWasEverPromoted(tx *sql.Tx) (bool, error) {
	has, err := tableExists(tx, "automode_proposals")
	if err != nil || !has {
		return false, err
	}
	var count int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM automode_proposals WHERE kind = ? AND state = ?",
		automode.KindModel, automode.StatePromoted).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// Prose lines become the slot document; the prose lands in notes, not discarded.
func applyMigration125(tx *sql.Tx) error {
	has, err := columnExists(tx, "automode_config", "environment")
	if err != nil || !has {
		return err
	}
	var raw string
	err = tx.QueryRow("SELECT environment FROM automode_config WHERE id = 1").Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(raw) == "" {
		raw = "[]"
	}
	var lines []string
	if err := json.Unmarshal([]byte(raw), &lines); err != nil {
		// No prose to carry; the reader falls back to an empty environment.
		return nil
	}
	env := automode.NewEnvironment()
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			env.Notes = append(env.Notes, line)
		}
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE automode_config SET environment = ? WHERE id = 1", string(encoded))
	return err
}

// Slugs are stored at planting, so every seed planted under the old rule keeps a
// title-length slug until it is recomputed here. Nothing references a slug as a key.
func applyMigration126(tx *sql.Tx) error {
	var collection int64
	err := tx.QueryRow(
		`SELECT id FROM document_collections WHERE namespace = ? AND collection = ?`,
		garden.Namespace, garden.CollectionSeeds).Scan(&collection)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	table := docstore.TableName(collection)
	rows, err := tx.Query(fmt.Sprintf(`SELECT id, body FROM %s`, table))
	if err != nil {
		return err
	}
	type reslug struct{ id, body string }
	var updates []reslug
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			continue
		}
		var title, slug string
		json.Unmarshal(body["title"], &title)
		json.Unmarshal(body["step_slug"], &slug)
		if want := garden.StepSlug(title); want != slug {
			encoded, _ := json.Marshal(want)
			body["step_slug"] = encoded
			next, err := json.Marshal(body)
			if err != nil {
				rows.Close()
				return err
			}
			updates = append(updates, reslug{id, string(next)})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	// A new body is a new revision: stale conditional writes and resuming
	// subscriptions decide by rev, so a silent rewrite would leave them holding the old slug.
	stamp := time.Now().UTC().Format(docstore.TimeFormat)
	for _, u := range updates {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET body = ?, rev = rev + 1, updated_at = ? WHERE id = ?`, table), u.body, stamp, u.id); err != nil {
			return err
		}
	}
	return nil
}

func foldModelLists(lists ...string) ([]string, error) {
	folded := []string{}
	seen := map[string]bool{}
	for _, raw := range lists {
		if raw == "" {
			continue
		}
		var entries []string
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return nil, fmt.Errorf("automode_config model list %q: %w", raw, err)
		}
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry == "" || seen[entry] {
				continue
			}
			seen[entry] = true
			folded = append(folded, entry)
		}
	}
	return folded, nil
}

func applyMigration106(tx *sql.Tx) error {
	has, err := columnExists(tx, "sessions", "session_cost_json")
	if err != nil || has {
		return err
	}
	_, err = tx.Exec("ALTER TABLE sessions ADD COLUMN session_cost_json TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration105(tx *sql.Tx) error {
	if _, err := tx.Exec(`
-- No index beyond the primary key: every reader arrives holding a step id —
-- the registry's cursor, or the parent of the step it is standing on — so the
-- chain is walked by rowid. app_name is carried to keep a step readable on its
-- own and for the one-time carry below.
CREATE TABLE IF NOT EXISTS app_serving_steps (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    app_name   TEXT NOT NULL,
    version_id INTEGER NOT NULL,
    parent_id  INTEGER,
    created_at TEXT NOT NULL
);`); err != nil {
		return err
	}
	has, err := columnExists(tx, "apps", "serving_step_id")
	if err != nil || has {
		return err
	}
	if _, err := tx.Exec("ALTER TABLE apps ADD COLUMN serving_step_id INTEGER"); err != nil {
		return err
	}

	carried, err := columnExists(tx, "apps", "previous_version_id")
	if err != nil {
		return err
	}
	if carried {
		if _, err := tx.Exec(`
			INSERT INTO app_serving_steps (app_name, version_id, parent_id, created_at)
			SELECT name, previous_version_id, NULL, updated_at FROM apps
			WHERE current_version_id IS NOT NULL
				AND previous_version_id IS NOT NULL
				AND previous_version_id <> current_version_id`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO app_serving_steps (app_name, version_id, parent_id, created_at)
		SELECT a.name, a.current_version_id,
		       (SELECT MIN(p.id) FROM app_serving_steps p WHERE p.app_name = a.name),
		       a.updated_at
		FROM apps a WHERE a.current_version_id IS NOT NULL`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		UPDATE apps SET serving_step_id = (
			SELECT MAX(s.id) FROM app_serving_steps s WHERE s.app_name = apps.name
		) WHERE current_version_id IS NOT NULL`); err != nil {
		return err
	}
	if !carried {
		return nil
	}
	_, err = tx.Exec("ALTER TABLE apps DROP COLUMN previous_version_id")
	return err
}

func applyMigration93(tx *sql.Tx) error {
	has, err := columnExists(tx, "session_annotation_drafts", "note")
	if err != nil || has {
		return err
	}
	_, err = tx.Exec("ALTER TABLE session_annotation_drafts ADD COLUMN note TEXT NOT NULL DEFAULT ''")
	return err
}

func applyMigration91(tx *sql.Tx) error {
	ids, err := collectionTableIDs(tx)
	if err != nil {
		return err
	}
	unreadable := 0
	for _, id := range ids {
		table := docstore.TableName(id)
		n, err := restampTable(tx, table, "id", []string{"created_at", "updated_at"})
		if err != nil {
			return fmt.Errorf("restamping %s: %w", table, err)
		}
		unreadable += n
	}
	n, err := restampTable(tx, "document_collections", "id", []string{"updated_at"})
	if err != nil {
		return fmt.Errorf("restamping document_collections: %w", err)
	}
	unreadable += n
	if unreadable > 0 {
		log.Printf("[store] migration 91: left %d document timestamp(s) as they were; they are not RFC3339 and cannot be re-encoded", unreadable)
	}
	return nil
}

func collectionTableIDs(tx *sql.Tx) ([]int64, error) {
	rows, err := tx.Query(`SELECT id FROM document_collections`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func applyMigration94(tx *sql.Tx) error {
	unreadable, err := restampTable(tx, "jobs", "id",
		[]string{"scheduled_at", "created_at", "updated_at"})
	if err != nil {
		return fmt.Errorf("restamping jobs: %w", err)
	}
	n, err := restampTable(tx, "notifications", "id", []string{"created_at", "read_at"})
	if err != nil {
		return fmt.Errorf("restamping notifications: %w", err)
	}
	unreadable += n
	if unreadable > 0 {
		log.Printf("[store] migration 94: left %d job/notification timestamp(s) as they were; they are not RFC3339 and cannot be re-encoded", unreadable)
	}
	return nil
}

func applyMigration95(tx *sql.Tx) error {
	restamps := []struct {
		table   string
		key     string
		columns []string
	}{
		{"sessions", "id", []string{"turn_opened_at", "turn_settled_at", "turn_snoozed_until"}},
		{"automation_provider_cursors", "rowid", []string{"observed_at"}},
		{"automation_review_request_edges", "rowid", []string{"last_observed_at"}},
		{"delegation_operations", "request_id", []string{"created_at", "updated_at"}},
		{"endpoints", "id", []string{"created_at", "updated_at"}},
		{"workspaces", "id", []string{"created_at"}},
		{"workspace_layout_panes", "rowid", []string{"created_at", "updated_at"}},
		{"workflow_runs", "run_id", []string{"created_at"}},
	}
	unreadable := 0
	for _, r := range restamps {
		present, err := tableExists(tx, r.table)
		if err != nil {
			return fmt.Errorf("checking for %s: %w", r.table, err)
		}
		if !present {
			continue
		}
		n, err := restampTable(tx, r.table, r.key, r.columns)
		if err != nil {
			return fmt.Errorf("restamping %s: %w", r.table, err)
		}
		unreadable += n
	}
	if unreadable > 0 {
		log.Printf("[store] migration 95: left %d turn/cursor/listing timestamp(s) as they were; they are not RFC3339 and cannot be re-encoded", unreadable)
	}
	return nil
}

// Rows are read out fully before any is written back: the driver holds one
// connection, and a write issued while a read is still streaming deadlocks.
func restampTable(tx *sql.Tx, table, key string, columns []string) (int, error) {
	rows, err := tx.Query(fmt.Sprintf(`SELECT %s, %s FROM %s`, key, strings.Join(columns, ", "), table))
	if err != nil {
		return 0, err
	}
	type row struct {
		key    any
		stamps []string
	}
	var pending []row
	for rows.Next() {
		r := row{stamps: make([]string, len(columns))}
		dest := make([]any, 0, len(columns)+1)
		dest = append(dest, &r.key)
		for i := range r.stamps {
			dest = append(dest, &r.stamps[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	unreadable := 0
	for _, r := range pending {
		sets := make([]string, 0, len(columns))
		args := make([]any, 0, len(columns)+1)
		for i, c := range columns {
			if r.stamps[i] == "" {
				continue
			}
			t, err := docstore.ParseTime(r.stamps[i])
			if err != nil {
				unreadable++
				continue
			}
			encoded := t.Format(docstore.TimeFormat)
			if encoded == r.stamps[i] {
				continue
			}
			sets = append(sets, c+" = ?")
			args = append(args, encoded)
		}
		if len(sets) == 0 {
			continue
		}
		args = append(args, r.key)
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET %s WHERE %s = ?`,
			table, strings.Join(sets, ", "), key), args...); err != nil {
			return 0, err
		}
	}
	return unreadable, nil
}

type v88Collection struct {
	namespace  string
	collection string
	fields     []docstore.FieldSpec
	fieldsJSON string
	updatedAt  string
}

func readV88Collections(tx *sql.Tx) ([]v88Collection, error) {
	rows, err := tx.Query(`SELECT namespace, collection, fields_json, updated_at
        FROM document_collections_v88 ORDER BY namespace, collection`)
	if err != nil {
		return nil, err
	}
	var out []v88Collection
	for rows.Next() {
		var c v88Collection
		if err := rows.Scan(&c.namespace, &c.collection, &c.fieldsJSON, &c.updatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(c.fieldsJSON), &c.fields); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reading the declaration of %s/%s: %w", c.namespace, c.collection, err)
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	orphans, err := tx.Query(`SELECT d.namespace, d.collection, MAX(d.updated_at)
        FROM documents d
       WHERE NOT EXISTS (SELECT 1 FROM document_collections_v88 c
                          WHERE c.namespace = d.namespace AND c.collection = d.collection)
       GROUP BY d.namespace, d.collection
       ORDER BY d.namespace, d.collection`)
	if err != nil {
		return nil, err
	}
	defer orphans.Close()
	for orphans.Next() {
		c := v88Collection{fieldsJSON: "[]"}
		if err := orphans.Scan(&c.namespace, &c.collection, &c.updatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, orphans.Err()
}

func carryV88Collection(tx *sql.Tx, c v88Collection) (int, error) {
	schema := docstore.CollectionSchema{Namespace: c.namespace, Collection: c.collection, Fields: c.fields}
	// Validate before any field name reaches the CREATE TABLE below.
	if err := schema.Validate(); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`INSERT INTO document_collections (namespace, collection, fields_json, updated_at) VALUES (?, ?, ?, ?)`,
		c.namespace, c.collection, c.fieldsJSON, c.updatedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	table := docstore.TableName(id)
	if err := createCollectionTable(tx, table, c.fields); err != nil {
		return 0, err
	}
	moved, err := tx.Exec(`INSERT INTO `+table+` (id, body, created_at, updated_at)
        SELECT id, body, created_at, updated_at FROM documents WHERE namespace = ? AND collection = ?`,
		c.namespace, c.collection)
	if err != nil {
		return 0, err
	}
	n, err := moved.RowsAffected()
	return int(n), err
}

func columnExists(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func seedLegacyDB(db *sql.DB) error {
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return err
	}
	if currentVersion > 0 {
		return nil
	}

	var colCount int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('prs') WHERE name = 'head_sha'
	`).Scan(&colCount)
	if err != nil {
		return err
	}
	if colCount == 0 {
		return nil
	}

	const legacyMaxVersion = 10
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	for v := 1; v <= legacyMaxVersion; v++ {
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
			v,
		); err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func getCurrentVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

func GetSchemaVersion(db *sql.DB) (int, error) {
	return getCurrentVersion(db)
}
