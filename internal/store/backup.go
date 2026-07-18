package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// backupNamePrefix and backupNameLayout define the on-disk naming scheme for
// rotating backups: attn-<UTC timestamp YYYYMMDD-HHMMSS>.db. Pre-migration
// snapshots use a different prefix (see backupNow's premigration variant in
// sqlite.go) so the rotation prune below never counts or removes them.
const (
	backupNamePrefix = "attn-"
	backupNameLayout = "20060102-150405"
	backupNameSuffix = ".db"
)

// backupPremigrationKeep caps how many pre-migration snapshots
// (attn-premigration-<version>-<timestamp>.db) accumulate in the backups
// dir. These fire rarely (once per schema migration), but a long-lived
// install crossing many schema versions would otherwise never prune them.
const backupPremigrationKeep = 5

// premigrationNamePrefix and premigrationTimestampLen let
// pruneBackupPremigration recognize pre-migration snapshot filenames and
// pull out the trailing UTC timestamp for chronological sorting, since the
// embedded schema version varies in digit count and the whole filename does
// not sort chronologically.
const premigrationNamePrefix = "attn-premigration-"

// len("20060102-150405")
const premigrationTimestampLen = len(backupNameLayout)

// BackupNow writes a consistent online snapshot of the store's database to
// dir/attn-<UTC timestamp YYYYMMDD-HHMMSS>.db using SQLite's VACUUM INTO,
// then prunes the oldest files in dir so at most keep backups remain.
// It creates dir if needed. Returns the path of the new backup. It must be
// safe to call while the daemon is serving traffic (VACUUM INTO reads
// through a consistent snapshot without blocking writers).
func (s *Store) BackupNow(dir string, keep int) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("backup: store has no open database")
	}
	if !s.durable {
		return "", fmt.Errorf("backup: store is not durably backed (in-memory fallback), skipping backup")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("backup: create dir %s: %w", dir, err)
	}

	name := backupNamePrefix + time.Now().UTC().Format(backupNameLayout) + backupNameSuffix
	target := filepath.Join(dir, name)

	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("backup: target %s already exists", target)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("backup: stat target %s: %w", target, err)
	}

	if _, err := s.db.Exec("VACUUM INTO ?", target); err != nil {
		return "", fmt.Errorf("backup: vacuum into %s: %w", target, err)
	}

	if err := pruneBackups(dir, keep); err != nil {
		return target, fmt.Errorf("backup: wrote %s but prune failed: %w", target, err)
	}

	return target, nil
}

// pruneBackups removes the oldest rotating backups in dir beyond keep. It
// only considers files matching the canonical attn-<timestamp>.db name — not
// pre-migration snapshots (attn-premigration-*.db), which are exempt from
// rotation. Lexical sort on the fixed-width timestamp format sorts
// chronologically.
func pruneBackups(dir string, keep int) error {
	if keep < 0 {
		keep = 0
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if IsRotatingBackupName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if len(names) <= keep {
		return nil
	}

	toRemove := names[:len(names)-keep]
	var firstErr error
	for _, name := range toRemove {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return firstErr
}

// backupPreMigration writes a one-off snapshot of db to
// dir/attn-premigration-<version>-<timestamp>.db, where dir is the
// "backups" directory alongside dbPath, before pending migrations run.
// version identifies the schema version the database is migrating FROM.
// This name is deliberately excluded from IsRotatingBackupName's pattern so
// BackupNow's rotation prune never counts or removes it. Returns the path of
// the snapshot on success.
func backupPreMigration(db *sql.DB, dbPath string, version int) (string, error) {
	dir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create dir %s: %w", dir, err)
	}

	name := fmt.Sprintf("attn-premigration-%d-%s%s", version, time.Now().UTC().Format(backupNameLayout), backupNameSuffix)
	target := filepath.Join(dir, name)

	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("target %s already exists", target)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat target %s: %w", target, err)
	}

	if _, err := db.Exec("VACUUM INTO ?", target); err != nil {
		return "", fmt.Errorf("vacuum into %s: %w", target, err)
	}

	if err := pruneBackupPremigration(dir, backupPremigrationKeep); err != nil {
		return target, fmt.Errorf("wrote %s but premigration prune failed: %w", target, err)
	}

	return target, nil
}

// pruneBackupPremigration removes the oldest pre-migration snapshots in dir
// beyond keep, leaving the rotating attn-<timestamp>.db backups (and
// anything else) untouched. Snapshots are ordered by the trailing
// YYYYMMDD-HHMMSS timestamp embedded before the .db suffix, not by the
// filename as a whole (the embedded schema version varies in digit count,
// so whole-filename sort does not sort chronologically) and not by mtime.
func pruneBackupPremigration(dir string, keep int) error {
	if keep < 0 {
		keep = 0
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	type snapshot struct {
		name string
		ts   string
	}
	var snapshots []snapshot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, premigrationNamePrefix) || !strings.HasSuffix(name, backupNameSuffix) {
			continue
		}
		stem := strings.TrimSuffix(name, backupNameSuffix)
		if len(stem) < premigrationTimestampLen {
			continue
		}
		ts := stem[len(stem)-premigrationTimestampLen:]
		if _, err := time.Parse(backupNameLayout, ts); err != nil {
			continue
		}
		snapshots = append(snapshots, snapshot{name: name, ts: ts})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ts < snapshots[j].ts
	})

	if len(snapshots) <= keep {
		return nil
	}

	toRemove := snapshots[:len(snapshots)-keep]
	var firstErr error
	for _, s := range toRemove {
		if err := os.Remove(filepath.Join(dir, s.name)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", s.name, err)
		}
	}
	return firstErr
}

// IsRotatingBackupName reports whether name is a canonical rotating backup
// (attn-<timestamp>.db), excluding pre-migration snapshots
// (attn-premigration-<version>-<timestamp>.db) which are exempt from prune.
func IsRotatingBackupName(name string) bool {
	if !strings.HasPrefix(name, backupNamePrefix) || !strings.HasSuffix(name, backupNameSuffix) {
		return false
	}
	stem := strings.TrimSuffix(strings.TrimPrefix(name, backupNamePrefix), backupNameSuffix)
	if _, err := time.Parse(backupNameLayout, stem); err != nil {
		return false
	}
	return true
}
