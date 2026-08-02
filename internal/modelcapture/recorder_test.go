package modelcapture

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecorderPersistsPrivateDeduplicatedObservations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model-captures")
	recorder := New(dir)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	reason, due := recorder.Due("session-secret", "working", now, 10*time.Second)
	if !due || reason != "initial" {
		t.Fatalf("initial Due = (%q, %v), want (initial, true)", reason, due)
	}
	saved, err := recorder.Record(Observation{
		CapturedAt:    now,
		CaptureReason: reason,
		SessionID:     "session-secret",
		Agent:         "Codex",
		DaemonState:   "working",
		StateReason:   "heartbeat_busy",
		Running:       true,
		Cols:          80,
		Rows:          24,
		LastSeq:       42,
		ViewportText:  "• Working\n\n› Explain this codebase",
	}, 1<<30)
	if err != nil {
		t.Fatalf("Record() error: %v", err)
	}
	if !saved {
		t.Fatal("initial observation should be saved")
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat capture dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("capture dir permissions = %o, want 700", got)
	}
	path := recorder.hourlyPath(now)
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat capture file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("capture file permissions = %o, want 600", got)
	}

	entry := readOneRecord(t, path)
	if entry.ViewportText != "• Working\n\n› Explain this codebase" {
		t.Fatalf("viewport text = %q", entry.ViewportText)
	}
	if entry.Agent != "codex" || entry.DaemonState != "working" || entry.StateReason != "heartbeat_busy" {
		t.Fatalf("record provenance = %+v", entry)
	}
	if entry.SessionKey == "" || strings.Contains(entry.SessionKey, "session-secret") {
		t.Fatalf("session key should be a one-way identifier, got %q", entry.SessionKey)
	}

	if _, due := recorder.Due("session-secret", "working", now.Add(9*time.Second), 10*time.Second); due {
		t.Fatal("session should not be due before the interval")
	}
	if _, due := recorder.Due("session-secret", "working", now.Add(10*time.Second), 10*time.Second); !due {
		t.Fatal("session should be due at the interval")
	}
	saved, err = recorder.Record(Observation{
		CapturedAt:    now.Add(10 * time.Second),
		CaptureReason: "interval",
		SessionID:     "session-secret",
		Agent:         "codex",
		DaemonState:   "working",
		ViewportText:  "• Working\n\n› Explain this codebase",
	}, 1<<30)
	if err != nil {
		t.Fatalf("deduplicated Record() error: %v", err)
	}
	if saved {
		t.Fatal("identical viewport and state should be deduplicated")
	}
	if _, due := recorder.Due("session-secret", "working", now.Add(11*time.Second), 10*time.Second); due {
		t.Fatal("deduplicated sample should advance the interval clock")
	}
	if reason, due := recorder.Due("session-secret", "waiting_input", now.Add(11*time.Second), 10*time.Second); !due || reason != "state_change" {
		t.Fatalf("state-change Due = (%q, %v), want (state_change, true)", reason, due)
	}
}

func TestRecorderPrunesOldHourlyFilesButKeepsActiveFile(t *testing.T) {
	recorder := New(filepath.Join(t.TempDir(), "model-captures"))
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for i, sessionID := range []string{"old", "current"} {
		at := start.Add(time.Duration(i) * time.Hour)
		saved, err := recorder.Record(Observation{
			CapturedAt:    at,
			CaptureReason: "initial",
			SessionID:     sessionID,
			Agent:         "claude",
			DaemonState:   "waiting_input",
			ViewportText:  strings.Repeat(sessionID, 100),
		}, 1)
		if err != nil {
			t.Fatalf("Record(%s) error: %v", sessionID, err)
		}
		if !saved {
			t.Fatalf("Record(%s) was unexpectedly deduplicated", sessionID)
		}
	}

	if _, err := os.Stat(recorder.hourlyPath(start)); !os.IsNotExist(err) {
		t.Fatalf("old hourly file should be pruned, stat err=%v", err)
	}
	if _, err := os.Stat(recorder.hourlyPath(start.Add(time.Hour))); err != nil {
		t.Fatalf("active hourly file should remain: %v", err)
	}
}

func readOneRecord(t *testing.T, path string) record {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open capture record: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatalf("capture file is empty: %v", scanner.Err())
	}
	var entry record
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatalf("decode capture record: %v", err)
	}
	if scanner.Scan() {
		t.Fatal("capture file has more than one record")
	}
	return entry
}
