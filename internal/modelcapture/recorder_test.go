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

func TestRecorderSegmentsActiveHourToKeepTotalUnderCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model-captures")
	recorder := New(dir)
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sessionIDs := []string{"one", "two", "six"}

	recordObservation := func(index int, maxBytes int64) {
		t.Helper()
		sessionID := sessionIDs[index]
		saved, err := recorder.Record(Observation{
			CapturedAt:    start.Add(time.Duration(index) * time.Second),
			CaptureReason: "initial",
			SessionID:     sessionID,
			Agent:         "claude",
			DaemonState:   "waiting_input",
			ViewportText:  strings.Repeat(sessionID, 100),
		}, maxBytes)
		if err != nil {
			t.Fatalf("Record(%s) error: %v", sessionID, err)
		}
		if !saved {
			t.Fatalf("Record(%s) was unexpectedly deduplicated", sessionID)
		}
	}

	recordObservation(0, 1<<20)
	firstInfo, err := os.Stat(recorder.hourlyPath(start))
	if err != nil {
		t.Fatalf("stat first capture segment: %v", err)
	}
	maxBytes := firstInfo.Size() + 1

	for index := 1; index < len(sessionIDs); index++ {
		if index == 2 {
			recorder = New(dir)
		}
		recordObservation(index, maxBytes)
		total, err := SizeBytes(dir)
		if err != nil {
			t.Fatalf("SizeBytes() error: %v", err)
		}
		if total > maxBytes {
			t.Fatalf("capture storage after record %d = %d, exceeds cap %d", index, total, maxBytes)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read capture directory: %v", err)
	}
	var captureNames []string
	for _, entry := range entries {
		if isCaptureFile(entry.Name()) {
			captureNames = append(captureNames, entry.Name())
		}
	}
	wantPath := strings.TrimSuffix(recorder.hourlyPath(start), fileSuffix) + "-0002" + fileSuffix
	if len(captureNames) != 1 || captureNames[0] != filepath.Base(wantPath) {
		t.Fatalf("capture segments = %v, want only %s", captureNames, filepath.Base(wantPath))
	}
	entry := readOneRecord(t, wantPath)
	if entry.ViewportText != strings.Repeat("six", 100) {
		t.Fatalf("retained viewport = %q, want latest record", entry.ViewportText)
	}
}

func TestRecorderRejectsRecordLargerThanStorageCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "model-captures")
	recorder := New(dir)
	saved, err := recorder.Record(Observation{
		CapturedAt:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		CaptureReason: "initial",
		SessionID:     "one",
		Agent:         "codex",
		DaemonState:   "working",
		ViewportText:  "too large",
	}, 1)
	if err == nil {
		t.Fatal("Record() should reject a record larger than the cap")
	}
	if saved {
		t.Fatal("oversized record should not be saved")
	}
	total, sizeErr := SizeBytes(dir)
	if sizeErr != nil {
		t.Fatalf("SizeBytes() error: %v", sizeErr)
	}
	if total != 0 {
		t.Fatalf("capture storage after rejected record = %d, want 0", total)
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
