package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestStoreEndpointCRUD(t *testing.T) {
	s := New()

	record, err := s.AddEndpoint("gpu-box", "user@example", "")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}
	if record.ID == "" {
		t.Fatal("AddEndpoint() returned empty ID")
	}
	if !record.Enabled {
		t.Fatal("AddEndpoint() should default enabled=true")
	}
	if record.Profile != "" {
		t.Fatalf("AddEndpoint() Profile = %q, want empty", record.Profile)
	}

	got := s.GetEndpoint(record.ID)
	if got == nil {
		t.Fatal("GetEndpoint() returned nil")
	}
	if got.Name != "gpu-box" {
		t.Fatalf("GetEndpoint().Name = %q, want gpu-box", got.Name)
	}
	if got.SSHTarget != "user@example" {
		t.Fatalf("GetEndpoint().SSHTarget = %q, want user@example", got.SSHTarget)
	}

	name := "gpu-box-2"
	target := "dev@example"
	enabled := false
	profile := "dev"
	updated, err := s.UpdateEndpoint(record.ID, EndpointUpdate{
		Name:      &name,
		SSHTarget: &target,
		Enabled:   &enabled,
		Profile:   &profile,
	})
	if err != nil {
		t.Fatalf("UpdateEndpoint() error = %v", err)
	}
	if updated.Name != name {
		t.Fatalf("UpdateEndpoint().Name = %q, want %q", updated.Name, name)
	}
	if updated.SSHTarget != target {
		t.Fatalf("UpdateEndpoint().SSHTarget = %q, want %q", updated.SSHTarget, target)
	}
	if updated.Enabled {
		t.Fatal("UpdateEndpoint().Enabled = true, want false")
	}
	if updated.Profile != profile {
		t.Fatalf("UpdateEndpoint().Profile = %q, want %q", updated.Profile, profile)
	}

	list := s.ListEndpoints()
	if len(list) != 1 {
		t.Fatalf("ListEndpoints() len = %d, want 1", len(list))
	}
	if list[0].ID != record.ID {
		t.Fatalf("ListEndpoints()[0].ID = %q, want %q", list[0].ID, record.ID)
	}
	if list[0].Profile != profile {
		t.Fatalf("ListEndpoints()[0].Profile = %q, want %q", list[0].Profile, profile)
	}

	if err := s.RemoveEndpoint(record.ID); err != nil {
		t.Fatalf("RemoveEndpoint() error = %v", err)
	}
	if got := s.GetEndpoint(record.ID); got != nil {
		t.Fatalf("GetEndpoint() after remove = %+v, want nil", got)
	}
}

func TestAddEndpointWithProfile(t *testing.T) {
	s := New()

	record, err := s.AddEndpoint("gpu-box", "user@example", "dev")
	if err != nil {
		t.Fatalf("AddEndpoint() error = %v", err)
	}
	if record.Profile != "dev" {
		t.Fatalf("AddEndpoint() Profile = %q, want dev", record.Profile)
	}

	got := s.GetEndpoint(record.ID)
	if got == nil || got.Profile != "dev" {
		t.Fatalf("GetEndpoint() Profile = %q, want dev", got.Profile)
	}
}

func TestAddEndpointNormalizesProfileCase(t *testing.T) {
	s := New()

	record, err := s.AddEndpoint("gpu-box", "user@example", "DEV")
	if err != nil {
		t.Fatalf("AddEndpoint(\"DEV\") error = %v", err)
	}
	if record.Profile != "dev" {
		t.Fatalf("AddEndpoint(\"DEV\") Profile = %q, want %q (must be lowercased so $ATTN_PROFILE on the remote — which is already lowercased by config.Profile() — produces the same data dir as the install path the hub builds locally)", record.Profile, "dev")
	}
}

func TestUpdateEndpointClearsProfileWithEmptyString(t *testing.T) {
	s := New()
	record, err := s.AddEndpoint("gpu-box", "user@example", "dev")
	if err != nil {
		t.Fatalf("AddEndpoint(): %v", err)
	}
	if record.Profile != "dev" {
		t.Fatalf("setup: profile = %q, want dev", record.Profile)
	}

	empty := ""
	updated, err := s.UpdateEndpoint(record.ID, EndpointUpdate{Profile: &empty})
	if err != nil {
		t.Fatalf("UpdateEndpoint(profile=\"\") error = %v", err)
	}
	if updated.Profile != "" {
		t.Fatalf("UpdateEndpoint(profile=\"\") Profile = %q, want empty (a non-nil empty pointer must clear the profile back to default)", updated.Profile)
	}

	got := s.GetEndpoint(record.ID)
	if got == nil || got.Profile != "" {
		t.Fatalf("GetEndpoint() Profile = %q, want empty", got.Profile)
	}
}

func TestUpdateEndpointNormalizesProfileCase(t *testing.T) {
	s := New()
	record, err := s.AddEndpoint("gpu-box", "user@example", "")
	if err != nil {
		t.Fatalf("AddEndpoint(): %v", err)
	}

	upper := "DEV"
	updated, err := s.UpdateEndpoint(record.ID, EndpointUpdate{Profile: &upper})
	if err != nil {
		t.Fatalf("UpdateEndpoint(profile=\"DEV\") error = %v", err)
	}
	if updated.Profile != "dev" {
		t.Fatalf("UpdateEndpoint(profile=\"DEV\") Profile = %q, want \"dev\"", updated.Profile)
	}
}

func TestAddEndpointRejectsInvalidProfile(t *testing.T) {
	s := New()

	cases := []string{
		"with space",
		"a-very-long-profile-name-over-limit",
		"-leading-dash",
	}
	for _, profile := range cases {
		t.Run(profile, func(t *testing.T) {
			if _, err := s.AddEndpoint("gpu-box", "user@example", profile); err == nil {
				t.Fatalf("AddEndpoint(%q) succeeded, want validation error", profile)
			}
		})
	}
}

func TestEndpointMigration34BackfillsBlankProfile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Simulate a legacy DB created before migration 34 by opening, dropping the
	// profile column from baseSchema is not possible in SQLite; instead, reset
	// the schema_migrations row for 34 and the profile column, mimicking an
	// upgrade from a pre-34 release.
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE endpoints (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			ssh_target TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		INSERT INTO endpoints (id, name, ssh_target, enabled, created_at, updated_at)
		VALUES ('endpoint-1', 'gpu', 'user@host', 1, '2026-01-01', '2026-01-01');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Mark all migrations up to 33 as applied so only 34 runs.
	for v := 1; v <= 33; v++ {
		if _, err := db.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`,
			v,
		); err != nil {
			t.Fatalf("seed migrations: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen via OpenDB which runs migrations.
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db2.Close()

	var profile string
	if err := db2.QueryRow(`SELECT profile FROM endpoints WHERE id = 'endpoint-1'`).Scan(&profile); err != nil {
		t.Fatalf("scan profile: %v", err)
	}
	if profile != "" {
		t.Fatalf("legacy endpoint profile = %q, want empty", profile)
	}

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if version < 34 {
		t.Fatalf("schema version = %d, want >=34", version)
	}
}
