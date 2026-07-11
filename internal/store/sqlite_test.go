package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// latestSchemaVersion is the highest version in the migrations slice. It is NOT
// the same as len(migrations): versions 49 and 50 are burned (see sqlite.go), so
// there is a gap and the max version exceeds the migration count.
func latestSchemaVersion() int {
	max := 0
	for _, m := range migrations {
		if m.version > max {
			max = m.version
		}
	}
	return max
}

func TestOpenDB_CreatesSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	// Verify tables exist by querying them
	tables := []string{"sessions", "prs", "repos", "workspace_contexts", "workspace_keeper_compact_backups", "profile_roles", "chief_of_staff_dispatches", "chief_of_staff_dispatch_messages"}
	for _, table := range tables {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		if err != nil {
			t.Errorf("Table %q does not exist: %v", table, err)
		}
	}
}

func TestOpenDB_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "nested", "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() should create parent directories, got error = %v", err)
	}
	defer db.Close()
}

func TestOpenDB_ReopensExistingDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create and insert data
	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	_, err = db1.Exec("INSERT INTO repos (repo, muted, collapsed) VALUES ('test/repo', 1, 0)")
	if err != nil {
		t.Fatalf("INSERT error = %v", err)
	}
	db1.Close()

	// Reopen and verify data persists
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen error = %v", err)
	}
	defer db2.Close()

	var muted int
	err = db2.QueryRow("SELECT muted FROM repos WHERE repo = 'test/repo'").Scan(&muted)
	if err != nil {
		t.Fatalf("SELECT error = %v", err)
	}
	if muted != 1 {
		t.Errorf("muted = %d, want 1", muted)
	}
}

func TestMigrations_AppliedOnNewDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	// Check schema version matches total migrations
	version, err := GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != latestSchemaVersion() {
		t.Errorf("schema version = %d, want %d", version, latestSchemaVersion())
	}

	// Verify all migrations recorded
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("counting migrations error = %v", err)
	}
	if count != len(migrations) {
		t.Errorf("migration count = %d, want %d", count, len(migrations))
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Open DB twice
	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() first error = %v", err)
	}
	db1.Close()

	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() second error = %v", err)
	}
	defer db2.Close()

	// Should still have same number of migrations
	var count int
	err = db2.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("counting migrations error = %v", err)
	}
	if count != len(migrations) {
		t.Errorf("migration count after reopen = %d, want %d", count, len(migrations))
	}
}

func TestMigration67RenamesTicketArtifactRecords(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-67.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO tickets (id, title, status, created_at, updated_at)
		VALUES ('artifact-ticket', 'Artifact ticket', 'working', '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z');
		INSERT INTO ticket_activity (ticket_id, kind, author, created_at)
		VALUES ('artifact-ticket', 'handover', 'agent', '2026-07-10T00:00:00Z');
		INSERT INTO ticket_events (ticket_id, kind, author, detail, created_at)
		VALUES ('artifact-ticket', 'handover_submitted', 'agent', 'fingerprint', '2026-07-10T00:00:00Z');
		DELETE FROM schema_migrations WHERE version = 67;
	`); err != nil {
		db.Close()
		t.Fatalf("seed legacy artifact records: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded db: %v", err)
	}

	migrated, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() migrate error = %v", err)
	}
	defer migrated.Close()

	var activityKind, eventKind string
	if err := migrated.QueryRow(`SELECT kind FROM ticket_activity WHERE ticket_id = 'artifact-ticket'`).Scan(&activityKind); err != nil {
		t.Fatalf("read migrated activity: %v", err)
	}
	if err := migrated.QueryRow(`SELECT kind FROM ticket_events WHERE ticket_id = 'artifact-ticket'`).Scan(&eventKind); err != nil {
		t.Fatalf("read migrated event: %v", err)
	}
	if activityKind != "attach" || eventKind != "attach_submitted" {
		t.Fatalf("migrated kinds = (%q, %q), want (attach, attach_submitted)", activityKind, eventKind)
	}
}

func TestMigration66BackfillsActiveBornAssignedTicketsWithoutCursor(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-66.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("open seeded store: %v", err)
	}
	now := time.Now().UTC()
	if _, err := s.CreateTicket(Ticket{
		ID: "legacy-delegation", Title: "delegated work", Status: TicketStatusWorking, Assignee: "agent-a",
	}, "chief-a", now); err != nil {
		t.Fatalf("create legacy delegation: %v", err)
	}
	if _, err := s.CreateTicket(Ticket{
		ID: "ordinary-ticket", Title: "ordinary work", Status: TicketStatusTodo,
	}, "author", now); err != nil {
		t.Fatalf("create ordinary ticket: %v", err)
	}
	if err := s.AssignTicket("ordinary-ticket", "agent-b", "author", now.Add(time.Minute)); err != nil {
		t.Fatalf("assign ordinary ticket: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close seeded store: %v", err)
	}

	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`
		DROP TABLE ticket_role_owners;
		DELETE FROM schema_migrations WHERE version >= 66;
	`); err != nil {
		t.Fatalf("rewind migration 66: %v", err)
	}
	_ = raw.Close()

	migrated, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	owned, err := migrated.IsTicketRoleOwner(TicketRoleChiefOfStaff, "legacy-delegation")
	if err != nil || !owned {
		t.Fatalf("legacy delegation role ownership = %v, err %v; want true", owned, err)
	}
	ordinaryOwned, err := migrated.IsTicketRoleOwner(TicketRoleChiefOfStaff, "ordinary-ticket")
	if err != nil || ordinaryOwned {
		t.Fatalf("ordinary assigned ticket role ownership = %v, err %v; want false", ordinaryOwned, err)
	}
	cursor, err := migrated.GetTicketCursor(TicketRoleIdentity(TicketRoleChiefOfStaff), "legacy-delegation")
	if err != nil || cursor != 0 {
		t.Fatalf("backfilled role cursor = %d, err %v; want untouched zero", cursor, err)
	}
}

func TestMigrations_MigratedColumnsExist(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() error = %v", err)
	}
	defer db.Close()

	// Verify migrated columns exist by querying them
	migratedColumns := []struct {
		table  string
		column string
	}{
		{"prs", "host"},
		{"prs", "head_sha"},
		{"prs", "head_branch"},
		{"prs", "comment_count"},
		{"prs", "approved_by_me"},
		{"prs", "heat_state"},
		{"prs", "last_heat_activity_at"},
		{"pr_interactions", "last_seen_ci_status"},
		{"sessions", "branch"},
		{"sessions", "is_worktree"},
		{"sessions", "main_repo"},
		{"sessions", "agent"},
		{"sessions", "recoverable"},
		{"sessions", "resume_session_id"},
		{"sessions", "endpoint_id"},
		{"sessions", "agent_metadata"},
		{"sessions", "agent_driver_plugin_name"},
		{"sessions", "agent_driver_run_id"},
		{"sessions", "agent_driver_report_seq"},
		{"chief_of_staff_dispatches", "structured_report_json"},
		{"tickets", "reconciled_at"},
		{"sessions", "closed_intentionally_at"},
	}

	for _, tc := range migratedColumns {
		query := "SELECT " + tc.column + " FROM " + tc.table + " LIMIT 1"
		_, err := db.Exec(query)
		if err != nil {
			t.Errorf("Column %s.%s should exist after migrations: %v", tc.table, tc.column, err)
		}
	}
}

func TestMigration37_ConvertsSessionLayoutToWorkspaceLayout(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-37.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}

	if _, err := db.Exec(`
		DROP TABLE workspace_layout_panes;
		DROP TABLE workspace_layouts;
		CREATE TABLE session_workspaces (
			session_id TEXT PRIMARY KEY,
			active_pane_id TEXT NOT NULL,
			layout_json TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE workspace_panes (
			session_id TEXT NOT NULL,
			pane_id TEXT NOT NULL,
			runtime_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (session_id, pane_id)
		);
		INSERT INTO sessions (
			id, label, directory, state, state_since, state_updated_at, last_seen, workspace_id
		) VALUES (
			'sess-legacy', 'Legacy', '/tmp/legacy', 'idle', '2026-05-01T00:00:00Z',
			'2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z', NULL
		);
		INSERT INTO session_workspaces (session_id, active_pane_id, layout_json, updated_at)
		VALUES ('sess-legacy', 'pane-shell', '{"type":"split"}', '2026-05-01T00:00:00Z');
		INSERT INTO workspace_panes (session_id, pane_id, runtime_id, kind, title, created_at, updated_at)
		VALUES
			('sess-legacy', 'main', 'sess-legacy', 'main', 'Session', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z'),
			('sess-legacy', 'pane-shell', 'runtime-shell', 'shell', 'Shell 1', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z');
		DELETE FROM schema_migrations WHERE version >= 37;
	`); err != nil {
		db.Close()
		t.Fatalf("seed legacy layout error = %v", err)
	}
	db.Close()

	migrated, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() migrate error = %v", err)
	}
	defer migrated.Close()

	var workspaceID string
	if err := migrated.QueryRow("SELECT workspace_id FROM sessions WHERE id = 'sess-legacy'").Scan(&workspaceID); err != nil {
		t.Fatalf("select migrated session workspace id error = %v", err)
	}
	if workspaceID != "workspace-sess-legacy" {
		t.Fatalf("workspace_id = %q, want workspace-sess-legacy", workspaceID)
	}

	var activePaneID, layoutJSON string
	if err := migrated.QueryRow(
		"SELECT active_pane_id, layout_json FROM workspace_layouts WHERE workspace_id = ?",
		workspaceID,
	).Scan(&activePaneID, &layoutJSON); err != nil {
		t.Fatalf("select migrated layout error = %v", err)
	}
	if activePaneID != "pane-sess-legacy" || layoutJSON != `{"type":"pane","pane_id":"pane-sess-legacy"}` {
		t.Fatalf("migrated layout = (%q, %q), want session-backed pane layout", activePaneID, layoutJSON)
	}

	rows, err := migrated.Query("SELECT pane_id, kind, session_id FROM workspace_layout_panes WHERE workspace_id = ? ORDER BY pane_id", workspaceID)
	if err != nil {
		t.Fatalf("select migrated panes error = %v", err)
	}
	defer rows.Close()
	type migratedPane struct {
		paneID    string
		kind      string
		sessionID *string
	}
	var panes []migratedPane
	for rows.Next() {
		var pane migratedPane
		var sessionID *string
		if err := rows.Scan(&pane.paneID, &pane.kind, &sessionID); err != nil {
			t.Fatalf("scan migrated pane error = %v", err)
		}
		pane.sessionID = sessionID
		panes = append(panes, pane)
	}
	if len(panes) != 1 || panes[0].paneID != "pane-sess-legacy" || panes[0].kind != "agent" || panes[0].sessionID == nil || *panes[0].sessionID != "sess-legacy" {
		t.Fatalf("migrated panes = %+v, want one session-owned agent pane", panes)
	}

	for _, legacyTable := range []string{"session_workspaces", "workspace_panes"} {
		var count int
		if err := migrated.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", legacyTable).Scan(&count); err != nil {
			t.Fatalf("check removed table %s error = %v", legacyTable, err)
		}
		if count != 0 {
			t.Fatalf("legacy table %s still exists", legacyTable)
		}
	}
}

func TestMigration41_PreservesMutedSessionsAsMutedWorkspaces(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-41.db")
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite db: %v", err)
	}
	if _, err := rawDB.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			label TEXT NOT NULL,
			directory TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'idle',
			state_since TEXT NOT NULL,
			state_updated_at TEXT NOT NULL,
			todos TEXT,
			last_seen TEXT NOT NULL,
			workspace_id TEXT,
			muted INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			directory TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		INSERT INTO workspaces (id, title, directory, created_at) VALUES
			('ws-muted', 'Muted workspace', '/repo/muted', '2026-05-31T00:00:00Z'),
			('ws-active', 'Active workspace', '/repo/active', '2026-05-31T00:00:00Z');
		INSERT INTO sessions (
			id, label, directory, state, state_since, state_updated_at, last_seen, workspace_id, muted
		) VALUES
			('s-muted', 'Muted session', '/repo/muted', 'idle', '2026-05-31T00:00:00Z', '2026-05-31T00:00:00Z', '2026-05-31T00:00:00Z', 'ws-muted', 1),
			('s-active', 'Active session', '/repo/active', 'idle', '2026-05-31T00:00:00Z', '2026-05-31T00:00:00Z', '2026-05-31T00:00:00Z', 'ws-active', 0);
	`); err != nil {
		rawDB.Close()
		t.Fatalf("seed migration 41 legacy db: %v", err)
	}
	for version := 1; version <= 40; version++ {
		if _, err := rawDB.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
			version,
		); err != nil {
			rawDB.Close()
			t.Fatalf("seed migration version %d: %v", version, err)
		}
	}
	rawDB.Close()

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() migrating version 41 error = %v", err)
	}
	defer db.Close()

	for _, tc := range []struct {
		workspaceID string
		wantMuted   int
	}{
		{"ws-muted", 1},
		{"ws-active", 0},
	} {
		var got int
		if err := db.QueryRow("SELECT muted FROM workspaces WHERE id = ?", tc.workspaceID).Scan(&got); err != nil {
			t.Fatalf("query workspace %s muted: %v", tc.workspaceID, err)
		}
		if got != tc.wantMuted {
			t.Fatalf("workspace %s muted = %d, want %d", tc.workspaceID, got, tc.wantMuted)
		}
	}

	var sessionMutedColumns int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'muted'").Scan(&sessionMutedColumns); err != nil {
		t.Fatalf("query sessions.muted column: %v", err)
	}
	if sessionMutedColumns != 0 {
		t.Fatalf("sessions.muted column still exists after migration")
	}
}

func TestMigration49_BackfillsRankInCreatedAtOrder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-49.db")
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite db: %v", err)
	}
	// Pre-49 workspaces table: no rank column. Rows are inserted out of created_at
	// order so the backfill must read created_at, not insertion/rowid order.
	if _, err := rawDB.Exec(`
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			directory TEXT NOT NULL,
			muted INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		INSERT INTO workspaces (id, title, directory, created_at) VALUES
			('ws-second', 'Second', '/repo/second', '2026-05-31T00:00:02Z'),
			('ws-third', 'Third', '/repo/third', '2026-05-31T00:00:03Z'),
			('ws-first', 'First', '/repo/first', '2026-05-31T00:00:01Z');
	`); err != nil {
		rawDB.Close()
		t.Fatalf("seed migration 49 legacy db: %v", err)
	}
	for version := 1; version <= 48; version++ {
		if _, err := rawDB.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
			version,
		); err != nil {
			rawDB.Close()
			t.Fatalf("seed migration version %d: %v", version, err)
		}
	}
	rawDB.Close()

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() migrating version 49 error = %v", err)
	}
	defer db.Close()

	ranks := map[string]string{}
	rows, err := db.Query("SELECT id, rank FROM workspaces")
	if err != nil {
		t.Fatalf("query backfilled ranks: %v", err)
	}
	for rows.Next() {
		var id, rank string
		if err := rows.Scan(&id, &rank); err != nil {
			rows.Close()
			t.Fatalf("scan rank: %v", err)
		}
		if rank == "" {
			t.Fatalf("workspace %s has empty rank after backfill", id)
		}
		ranks[id] = rank
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	rows.Close()

	// created_at order is first < second < third, so ranks must sort the same way.
	if !(ranks["ws-first"] < ranks["ws-second"] && ranks["ws-second"] < ranks["ws-third"]) {
		t.Fatalf("ranks not strictly increasing in created_at order: %#v", ranks)
	}

	// Idempotent: reopening must not re-rank already-ranked rows.
	db.Close()
	reopened, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen error = %v", err)
	}
	defer reopened.Close()

	for id, want := range ranks {
		var got string
		if err := reopened.QueryRow("SELECT rank FROM workspaces WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatalf("query rank after reopen for %s: %v", id, err)
		}
		if got != want {
			t.Fatalf("rank for %s changed on reopen = %q, want %q", id, got, want)
		}
	}
}

func TestMigration50RepairsRankWhenVersion49WasAlreadyRecorded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "workspace-rank-repair.db")
	rawDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite db: %v", err)
	}
	if _, err := rawDB.Exec(`
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			directory TEXT NOT NULL,
			muted INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		INSERT INTO workspaces (id, title, directory, created_at) VALUES
			('ws-second', 'Second', '/repo/second', '2026-05-31T00:00:02Z'),
			('ws-first', 'First', '/repo/first', '2026-05-31T00:00:01Z');
		INSERT INTO schema_migrations (version, applied_at) VALUES (49, datetime('now'));
	`); err != nil {
		rawDB.Close()
		t.Fatalf("seed workspace rank repair db: %v", err)
	}
	rawDB.Close()

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() repairing version 49 collision: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('workspaces') WHERE name = 'rank'
	`).Scan(&count); err != nil {
		t.Fatalf("query rank column: %v", err)
	}
	if count != 1 {
		t.Fatalf("workspaces.rank columns = %d, want 1", count)
	}

	rows, err := db.Query("SELECT id, rank FROM workspaces ORDER BY rank")
	if err != nil {
		t.Fatalf("query repaired ranks: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id, rank string
		if err := rows.Scan(&id, &rank); err != nil {
			rows.Close()
			t.Fatalf("scan repaired rank: %v", err)
		}
		if rank == "" {
			rows.Close()
			t.Fatalf("workspace %s has empty repaired rank", id)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("repaired rank rows: %v", err)
	}
	rows.Close()
	if want := []string{"ws-first", "ws-second"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("workspace order = %v, want %v", ids, want)
	}
}

func TestMigration20_IdempotentWhenHostColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create DB and schema manually up to migration 19, then pre-add prs.host.
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	// Re-open raw and force migration state back to 19 while keeping host column.
	raw, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	// Delete migration markers for v20+ to simulate partially migrated DBs in the wild.
	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version >= 20"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration >=20 markers error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE prs ADD COLUMN host TEXT NOT NULL DEFAULT 'github.com'"); err != nil {
		// On already-host databases this may fail; that still matches the scenario.
		_ = err
	}
	raw.Close()

	// Should not fail on duplicate host column when applying v20.
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing prs.host in migration 20, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != latestSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", version, latestSchemaVersion())
	}

	// Unique index from migration 20 should exist.
	var idxName string
	err = db2.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_prs_host_repo_number'
	`).Scan(&idxName)
	if err != nil {
		t.Fatalf("expected migration 20 index to exist: %v", err)
	}
	if idxName != "idx_prs_host_repo_number" {
		t.Fatalf("index name = %q, want idx_prs_host_repo_number", idxName)
	}

	// Migration table should contain version 20.
	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 20").Scan(&count); err != nil {
		t.Fatalf("count migration 20 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 20 marker count = %d, want 1", count)
	}

}

func TestMigration21_IdempotentWhenAgentColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	raw, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version >= 21"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration >=21 markers error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT 'codex'"); err != nil {
		_ = err
	}
	raw.Close()

	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing sessions.agent in migration 21, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != latestSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", version, latestSchemaVersion())
	}

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 21").Scan(&count); err != nil {
		t.Fatalf("count migration 21 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 21 marker count = %d, want 1", count)
	}
}

func TestMigration31_IdempotentWhenEndpointIDColumnAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() setup error = %v", err)
	}
	db.Close()

	raw, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen setup error = %v", err)
	}

	if _, err := raw.Exec("DELETE FROM schema_migrations WHERE version >= 31"); err != nil {
		raw.Close()
		t.Fatalf("DELETE migration >=31 markers error = %v", err)
	}
	if _, err := raw.Exec("ALTER TABLE sessions ADD COLUMN endpoint_id TEXT"); err != nil {
		_ = err
	}
	raw.Close()

	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() should handle existing sessions.endpoint_id in migration 31, got error = %v", err)
	}
	defer db2.Close()

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion() error = %v", err)
	}
	if version != latestSchemaVersion() {
		t.Fatalf("schema version = %d, want %d", version, latestSchemaVersion())
	}

	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 31").Scan(&count); err != nil {
		t.Fatalf("count migration 31 row error = %v", err)
	}
	if count != 1 {
		t.Fatalf("migration 31 marker count = %d, want 1", count)
	}
}

// TestMigration52RenamesKeeperBackupsAndRealignsSentinel proves the keeper rename
// migration actually transforms a LEGACY DB: it renames the janitor-named backup
// table to the keeper-named one and rewrites the persisted 'attn-janitor' sentinel
// to 'attn-keeper' on existing workspace_contexts rows. (The fresh-DB happy path is
// already covered by the table-existence test; this exercises the legacy data path.)
func TestMigration52RenamesKeeperBackupsAndRealignsSentinel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	// Seed a context row, then roll the DB back to a pre-52 shape: plant the legacy
	// 'attn-janitor' sentinel, drop the keeper-named table, recreate the legacy
	// janitor-named one, and unrecord migration 52 (and everything after it) so
	// migrateDB re-applies it. getCurrentVersion is MAX-based, so a higher recorded
	// version (53+) would otherwise gate 52's re-run out; the later migrations are
	// idempotent and simply re-run as no-ops.
	s.AddWorkspace(&protocol.Workspace{ID: "workspace-1", Title: "W", Directory: t.TempDir()})
	if _, _, err := s.UpdateWorkspaceContext("workspace-1", "ctx", "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE workspace_contexts SET updated_by_session_id = 'attn-janitor' WHERE workspace_id = 'workspace-1'`); err != nil {
		t.Fatalf("plant legacy sentinel: %v", err)
	}
	if _, err := s.db.Exec(`DROP TABLE workspace_keeper_compact_backups`); err != nil {
		t.Fatalf("drop keeper table: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TABLE workspace_context_janitor_backups (
		workspace_id TEXT PRIMARY KEY,
		source_revision INTEGER NOT NULL,
		source_content TEXT NOT NULL,
		result_revision INTEGER NOT NULL,
		agent TEXT NOT NULL,
		model TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("recreate legacy table: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 52`); err != nil {
		t.Fatalf("unrecord migration 52: %v", err)
	}

	if err := migrateDB(s.db); err != nil {
		t.Fatalf("migrateDB error: %v", err)
	}

	if !tableExistsForTest(t, s.db, "workspace_keeper_compact_backups") {
		t.Fatal("workspace_keeper_compact_backups missing after migration 52")
	}
	if tableExistsForTest(t, s.db, "workspace_context_janitor_backups") {
		t.Fatal("legacy workspace_context_janitor_backups still present after migration 52")
	}
	var updater string
	if err := s.db.QueryRow(`SELECT updated_by_session_id FROM workspace_contexts WHERE workspace_id = 'workspace-1'`).Scan(&updater); err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if updater != "attn-keeper" {
		t.Fatalf("sentinel = %q, want attn-keeper", updater)
	}
}

func TestMigration53AddsClosedStateColumnIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	hasClosedState := func() bool {
		rows, err := s.db.Query(`PRAGMA table_info(chief_of_staff_dispatches)`)
		if err != nil {
			t.Fatalf("table_info: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				cid     int
				name    string
				colType string
				notNull int
				dflt    sql.NullString
				pk      int
			)
			if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
				t.Fatalf("scan table_info: %v", err)
			}
			if name == "closed_state" {
				return true
			}
		}
		return false
	}

	// A fresh DB has the column from migration 53.
	if !hasClosedState() {
		t.Fatal("closed_state column missing after migrations")
	}

	// Re-applying migration 53 over an existing column must be a no-op (the
	// columnExists guard), not a duplicate-column error. Unrecord 53 and re-migrate.
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 53`); err != nil {
		t.Fatalf("unrecord migration 53: %v", err)
	}
	if err := migrateDB(s.db); err != nil {
		t.Fatalf("re-run migrateDB after unrecording 53: %v", err)
	}
	if !hasClosedState() {
		t.Fatal("closed_state column missing after idempotent re-run")
	}
}

func tableExistsForTest(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return got == name
}
