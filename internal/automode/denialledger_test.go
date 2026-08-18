package automode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLedger(t *testing.T, path string, lines ...string) {
	t.Helper()
	contents := ""
	for _, line := range lines {
		contents += line + "\n"
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func record(at, action string) string {
	return `{"session_id":"pi-1","tool_call_id":"c1","tool":"bash","action":"` + action +
		`","reason":"not asked for","rule":"classifier-2a","at":"` + at + `"}`
}

// A machine where auto mode never refused anything has no file, and reading it
// is not an error — it is the ordinary case.
func TestReadDenialLedgerTakesAMissingFileAsEmpty(t *testing.T) {
	reading, err := ReadDenialLedger(filepath.Join(t.TempDir(), DenialLedgerFileName))
	if err != nil {
		t.Fatalf("reading a ledger that is not there: %v", err)
	}
	if len(reading.Records) != 0 || reading.Dropped != 0 || reading.Malformed != 0 {
		t.Errorf("an absent ledger read as %+v", reading)
	}
}

func TestReadDenialLedgerReadsBothGenerationsOldestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), DenialLedgerFileName)
	writeLedger(t, path+".1", record("2026-08-18T10:00:00.000Z", "bash: one"))
	writeLedger(t, path,
		`{"type":"rotated","dropped":2,"at":"2026-08-18T10:00:01.000Z"}`,
		record("2026-08-18T10:00:02.000Z", "bash: two"),
		record("2026-08-18T10:00:03.000Z", "bash: three"),
	)

	reading, err := ReadDenialLedger(path)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	var actions []string
	for _, r := range reading.Records {
		actions = append(actions, r.Action)
	}
	want := []string{"bash: one", "bash: two", "bash: three"}
	for i, action := range want {
		if i >= len(actions) || actions[i] != action {
			t.Fatalf("records read as %v, want %v", actions, want)
		}
	}
	if reading.Dropped != 2 {
		t.Errorf("Dropped = %d, want the 2 the marker claims", reading.Dropped)
	}
	if reading.Records[0].At.UTC() != time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC) {
		t.Errorf("At read as %s", reading.Records[0].At)
	}
}

// A line nobody can read is one denial lost. It is counted, so the reader is
// told, and it never takes the rest of the file with it.
func TestReadDenialLedgerCountsWhatItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), DenialLedgerFileName)
	writeLedger(t, path,
		"{ not json",
		`{"session_id":"pi-1","action":"bash: one","at":"not a timestamp"}`,
		record("2026-08-18T10:00:02.000Z", "bash: two"),
	)

	reading, err := ReadDenialLedger(path)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if len(reading.Records) != 1 || reading.Records[0].Action != "bash: two" {
		t.Fatalf("records read as %+v", reading.Records)
	}
	if reading.Malformed != 2 {
		t.Errorf("Malformed = %d, want 2", reading.Malformed)
	}
}

func TestDenialLedgerPathSitsInTheDataDir(t *testing.T) {
	if got := DenialLedgerPath("/data/attn-dev"); got != "/data/attn-dev/"+DenialLedgerFileName {
		t.Errorf("DenialLedgerPath = %q", got)
	}
}
