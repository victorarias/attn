package daemon

import (
	"path/filepath"
	"time"
)

// backupDir returns the directory rotating SQLite backups are written to:
// a "backups" subdirectory of the daemon's runtime data root. In production
// this is identical to config.DataDir() (ValidateDaemonIsolation enforces
// that the socket, and therefore dataRoot, cannot be split away from the
// active profile's data directory) — but reading it off the daemon instance
// rather than the global config keeps this test-safe: daemon tests point
// dataRoot at a throwaway temp dir instead of the real ~/.attn.
func (d *Daemon) backupDir() string {
	return filepath.Join(d.dataRoot, "backups")
}

// runDatabaseBackupLoop takes an immediate backup on daemon start, then one
// every backupInterval until the daemon shuts down. A backup failure is
// logged and never propagated — it must not crash or wedge the daemon.
func (d *Daemon) runDatabaseBackupLoop() {
	d.performDatabaseBackup()

	ticker := time.NewTicker(backupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.performDatabaseBackup()
		}
	}
}

// performDatabaseBackup runs a single BackupNow call and logs the outcome.
// It recovers from a panic in the store's backup path so a corrupt or
// wedged database can never take the daemon down with it.
func (d *Daemon) performDatabaseBackup() {
	defer func() {
		if r := recover(); r != nil {
			d.logf("database backup panicked: %v", r)
		}
	}()

	if d.store == nil {
		return
	}

	path, err := d.store.BackupNow(d.backupDir(), backupKeep)
	if err != nil {
		d.logf("database backup failed: %v", err)
		return
	}
	d.logf("database backup written to %s", path)

	d.lastBackupMu.Lock()
	d.lastBackupAt = time.Now().UTC()
	d.lastBackupMu.Unlock()

	// Fan out via the same settings-updated broadcast the settings code uses
	// elsewhere, so live clients see db.last_backup_at move without a
	// dedicated protocol message.
	d.broadcastSettings("")
}
