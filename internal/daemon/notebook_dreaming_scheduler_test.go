package daemon

import (
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

func TestDreamingLocation(t *testing.T) {
	d := newNotebookDaemon(t)
	if got := d.dreamingLocation(); got != time.Local {
		t.Fatalf("default location = %v, want local", got)
	}
	d.store.SetSetting(SettingNotebookDreamingTimezone, "America/New_York")
	if got := d.dreamingLocation(); got.String() != "America/New_York" {
		t.Fatalf("configured location = %v, want America/New_York", got)
	}
	d.store.SetSetting(SettingNotebookDreamingTimezone, "Not/ARealZone")
	if got := d.dreamingLocation(); got != time.Local {
		t.Fatalf("invalid location should fall back to local, got %v", got)
	}
}

func TestValidateDreamingSettings(t *testing.T) {
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
			if err := validateNotebookDreamingFrequency(tc.value); (err != nil) != tc.wantErr {
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
			if err := validateNotebookDreamingTimezone(tc.value); (err != nil) != tc.wantErr {
				t.Fatalf("validate %q err=%v wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
}
