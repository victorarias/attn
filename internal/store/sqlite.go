package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// baseSchema creates the core tables. Column additions are handled by migrations.
// This schema represents the initial state (version 0).
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

// migration represents a database schema migration
type migration struct {
	version int
	desc    string
	sql     string
}

// migrations defines all schema migrations in order.
// Each migration is applied exactly once, tracked in schema_migrations table.
// To add a new migration: append to this slice with the next version number.
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
	// Versions 49 and 50 are BURNED on real prod/dev DBs (pre-release builds recorded
	// them with no matching source migration), so the keeper rename lands at 51 — the
	// first version above every observed MAX(version). See applyMigration51.
	{51, "rename workspace context janitor backups to keeper compact backups", ""},
}

// OpenDB opens a SQLite database at the given path, creating it if necessary.
// It also creates the schema if the database is new.
func OpenDB(dbPath string) (*sql.DB, error) {
	// Create parent directories if they don't exist
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// For in-memory databases, ensure we use a single connection to avoid
	// connection pooling issues (each :memory: connection is a separate DB)
	if dbPath == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	// Create base schema (includes schema_migrations table)
	if _, err := db.Exec(baseSchema); err != nil {
		db.Close()
		return nil, err
	}

	// Run versioned migrations
	if err := migrateDB(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// migrateDB runs all pending migrations in order.
// It tracks applied migrations in the schema_migrations table.
func migrateDB(db *sql.DB) error {
	// Detect and handle legacy databases (created before migration system existed)
	if err := seedLegacyDB(db); err != nil {
		return fmt.Errorf("seeding legacy db: %w", err)
	}

	// Get current schema version
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return fmt.Errorf("getting schema version: %w", err)
	}

	// Run pending migrations
	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		// Execute migration in a transaction
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
		} else if m.version == 51 {
			if err := applyMigration51(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		} else {
			if _, err := tx.Exec(m.sql); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
			}
		}

		// Record migration
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

// applyMigration48 drops the label column from recent_locations. Labels are
// derived from the path now. Skips databases (including partial test
// fixtures) where the table or column never existed.
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

// applyMigration51 retires the last "janitor" identifiers from the live schema:
// it renames the keeper's compaction-backup table and realigns the persisted
// context-updater sentinel. Guarded so it is idempotent and safe on every DB
// shape, including a migration rewind that re-runs migration 47's
// CREATE-IF-NOT-EXISTS after the rename already happened:
//   - only the legacy table exists (normal upgrade): rename it, preserving data.
//   - both exist (47 recreated an empty legacy table beside the authoritative
//     keeper-named one): drop the spurious legacy duplicate.
//   - only the keeper-named table exists (already migrated): no-op.
//
// The UPDATE rewrites historical rows the keeper stamped before this rename.
func applyMigration51(tx *sql.Tx) error {
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
	if _, err := tx.Exec(
		`UPDATE workspace_contexts SET updated_by_session_id = 'attn-keeper' WHERE updated_by_session_id = 'attn-janitor'`,
	); err != nil {
		return err
	}
	return nil
}

// tableExists reports whether a table of the given name exists in the schema.
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

// seedLegacyDB detects databases created before the migration system existed
// and seeds schema_migrations to prevent duplicate column errors.
// Legacy DBs have all columns (up to migration 10) but no migration history.
func seedLegacyDB(db *sql.DB) error {
	// Check if schema_migrations is empty
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return err
	}
	if currentVersion > 0 {
		return nil // Already has migration history
	}

	// Check if this is a legacy DB by looking for a column that only exists
	// in legacy DBs (head_sha was in the original schema before migrations)
	var colCount int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('prs') WHERE name = 'head_sha'
	`).Scan(&colCount)
	if err != nil {
		return err
	}
	if colCount == 0 {
		return nil // Fresh DB, no legacy columns
	}

	// Legacy DB detected - seed migrations 1-10 (all columns that existed before migration system)
	// Migration 11+ were added after the migration system, so they need to run normally
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

// getCurrentVersion returns the highest applied migration version, or 0 if none.
func getCurrentVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// GetSchemaVersion returns the current schema version for the database.
// Exported for testing and diagnostics.
func GetSchemaVersion(db *sql.DB) (int, error) {
	return getCurrentVersion(db)
}
