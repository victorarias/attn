package daemon

import (
	"path/filepath"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return parsed
}

func TestNotebookCronLocation(t *testing.T) {
	d := newNotebookDaemon(t)
	if got := d.notebookCronLocation(); got != time.Local {
		t.Fatalf("default location = %v, want local", got)
	}
	d.store.SetSetting(SettingNotebookCronTimezone, "America/New_York")
	if got := d.notebookCronLocation(); got.String() != "America/New_York" {
		t.Fatalf("configured location = %v, want America/New_York", got)
	}
	d.store.SetSetting(SettingNotebookCronTimezone, "Not/ARealZone")
	if got := d.notebookCronLocation(); got != time.Local {
		t.Fatalf("invalid location should fall back to local, got %v", got)
	}
}

func TestValidateNotebookCronSettings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty frequency ok", "", false},
		{"valid cron", "0 3 * * *", false},
		{"valid every-minute cron", "* * * * *", false},
		{"descriptor ok", "@daily", false},
		{"garbage cron", "not a cron", true},
		{"too few fields", "0 3 * *", true},
		{"impossible date (Feb 30) rejected", "0 0 30 2 *", true},
		{"embedded CRON_TZ rejected", "CRON_TZ=Asia/Tokyo 0 3 * * *", true},
		{"embedded TZ rejected", "TZ=Asia/Tokyo 0 3 * * *", true},
	} {
		t.Run("frequency/"+tc.name, func(t *testing.T) {
			if err := validateNotebookCronFrequency(tc.value); (err != nil) != tc.wantErr {
				t.Fatalf("validate %q err=%v wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty tz ok", "", false},
		{"valid IANA", "America/New_York", false},
		{"utc", "UTC", false},
		{"garbage tz", "Not/ARealZone", true},
	} {
		t.Run("timezone/"+tc.name, func(t *testing.T) {
			if err := validateNotebookCronTimezone(tc.value); (err != nil) != tc.wantErr {
				t.Fatalf("validate %q err=%v wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
}

// TestMigrateNotebookCronSettingKeys covers the one-time rename of the persisted
// notebook.dreaming.{frequency,timezone} settings to notebook.cron.*: configured
// legacy values are carried forward, the legacy rows are dropped, and the migration
// is idempotent — a re-run never clobbers a value set under the new key.
func TestMigrateNotebookCronSettingKeys(t *testing.T) {
	t.Run("copies both legacy values forward and drops the legacy rows", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		d.store.SetSetting(legacyNotebookDreamingFrequencyKey, "0 5 * * *")
		d.store.SetSetting(legacyNotebookDreamingTimezoneKey, "America/New_York")

		d.migrateNotebookCronSettingKeys()

		if got := d.store.GetSetting(SettingNotebookCronFrequency); got != "0 5 * * *" {
			t.Fatalf("frequency new key = %q, want %q", got, "0 5 * * *")
		}
		if got := d.store.GetSetting(SettingNotebookCronTimezone); got != "America/New_York" {
			t.Fatalf("timezone new key = %q, want %q", got, "America/New_York")
		}
		if got := d.store.GetSetting(legacyNotebookDreamingFrequencyKey); got != "" {
			t.Fatalf("legacy frequency key still present: %q", got)
		}
		if got := d.store.GetSetting(legacyNotebookDreamingTimezoneKey); got != "" {
			t.Fatalf("legacy timezone key still present: %q", got)
		}
	})

	t.Run("idempotent: re-run is a no-op and never clobbers a user-set new value", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		d.store.SetSetting(legacyNotebookDreamingFrequencyKey, "0 5 * * *")

		d.migrateNotebookCronSettingKeys()

		// User reconfigures under the new key, and (defensively) a stale legacy row reappears.
		d.store.SetSetting(SettingNotebookCronFrequency, "30 2 * * *")
		d.store.SetSetting(legacyNotebookDreamingFrequencyKey, "0 5 * * *")

		d.migrateNotebookCronSettingKeys()

		if got := d.store.GetSetting(SettingNotebookCronFrequency); got != "30 2 * * *" {
			t.Fatalf("new key was clobbered: got %q, want %q", got, "30 2 * * *")
		}
		if got := d.store.GetSetting(legacyNotebookDreamingFrequencyKey); got != "" {
			t.Fatalf("legacy key still present after re-run: %q", got)
		}
	})

	t.Run("no legacy values: nothing to migrate", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

		d.migrateNotebookCronSettingKeys()

		if got := d.store.GetSetting(SettingNotebookCronFrequency); got != "" {
			t.Fatalf("frequency new key unexpectedly set: %q", got)
		}
		if got := d.store.GetSetting(SettingNotebookCronTimezone); got != "" {
			t.Fatalf("timezone new key unexpectedly set: %q", got)
		}
	})

	t.Run("reaps the orphaned enabled gate (no cron successor)", func(t *testing.T) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		d.store.SetSetting(legacyNotebookDreamingEnabledKey, "true")

		d.migrateNotebookCronSettingKeys()

		if got := d.store.GetSetting(legacyNotebookDreamingEnabledKey); got != "" {
			t.Fatalf("stale enabled gate not reaped: %q", got)
		}
		if _, ok := d.store.GetAllSettings()[legacyNotebookDreamingEnabledKey]; ok {
			t.Fatal("reaped enabled gate still appears in GetAllSettings")
		}
	})
}
