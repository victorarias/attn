package enrollment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	homeID    = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherHome = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func seedDaemonID(t *testing.T, root, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, DaemonIDFileName), []byte(id+"\n"), 0600); err != nil {
		t.Fatalf("seed daemon id: %v", err)
	}
}

func readStoredRecord(t *testing.T, root string) Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, RecordFileName))
	if err != nil {
		t.Fatalf("read enrollment record: %v", err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("parse enrollment record %q: %v", string(data), err)
	}
	return record
}

func TestEnsureDaemonID_PersistsAcrossCalls(t *testing.T) {
	root := t.TempDir()

	first, err := EnsureDaemonID(root)
	if err != nil {
		t.Fatalf("EnsureDaemonID first call: %v", err)
	}
	if !ValidDaemonID(first) {
		t.Fatalf("first daemon id %q is invalid", first)
	}

	second, err := EnsureDaemonID(root)
	if err != nil {
		t.Fatalf("EnsureDaemonID second call: %v", err)
	}
	if first != second {
		t.Fatalf("daemon id changed across calls: first=%q second=%q", first, second)
	}
}

func TestEnsureDaemonID_RewritesCorruptFile(t *testing.T) {
	root := t.TempDir()
	idPath := filepath.Join(root, DaemonIDFileName)
	if err := os.WriteFile(idPath, []byte("corrupt\n"), 0600); err != nil {
		t.Fatalf("seed corrupt daemon id file: %v", err)
	}

	id, err := EnsureDaemonID(root)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	if !ValidDaemonID(id) {
		t.Fatalf("rewritten daemon id %q is invalid", id)
	}

	storedBytes, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("read daemon id file: %v", err)
	}
	if stored := string(storedBytes); stored != id+"\n" {
		t.Fatalf("stored daemon id = %q, want %q", stored, id+"\n")
	}
}

func TestEnsureDaemonID_ConcurrentCallsStable(t *testing.T) {
	root := t.TempDir()
	const workers = 16
	results := make(chan string, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := EnsureDaemonID(root)
			if err != nil {
				errs <- err
				return
			}
			results <- id
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("EnsureDaemonID concurrent call: %v", err)
	}

	var first string
	for id := range results {
		if !ValidDaemonID(id) {
			t.Fatalf("invalid daemon id %q", id)
		}
		if first == "" {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("daemon id mismatch across concurrent calls: first=%q current=%q", first, id)
		}
	}
}

func TestEnsure_FreshInstallIsItsOwnHome(t *testing.T) {
	root := t.TempDir()
	id, err := EnsureDaemonID(root)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}

	status, err := Ensure(root, id)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !status.IsHome() {
		t.Fatalf("fresh install is not a home: %+v", status)
	}
	if got := status.Describe(); got != "home" {
		t.Fatalf("Describe() = %q, want \"home\"", got)
	}
	if record := readStoredRecord(t, root); record.HomeDaemonID != id {
		t.Fatalf("record home = %q, want own id %q", record.HomeDaemonID, id)
	}
	if record := readStoredRecord(t, root); strings.TrimSpace(record.RecordedAt) == "" {
		t.Fatalf("record has no recorded_at: %+v", record)
	}
}

func TestEnsure_KeepsAnExistingEnrollment(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, otherHome)
	if _, err := Enroll(root, homeID); err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	status, err := Ensure(root, otherHome)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if status.IsHome() {
		t.Fatalf("enrolled daemon reported itself a home: %+v", status)
	}
	if status.HomeDaemonID != homeID {
		t.Fatalf("home = %q, want %q", status.HomeDaemonID, homeID)
	}
	if got, want := status.Describe(), "outpost of "+homeID; got != want {
		t.Fatalf("Describe() = %q, want %q", got, want)
	}
}

func TestEnroll_FirstTimeAndIdempotent(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, otherHome)

	first, err := Enroll(root, homeID)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if first.Status != "enrolled" || !first.Changed() {
		t.Fatalf("first enroll = %+v, want status enrolled", first)
	}

	second, err := Enroll(root, homeID)
	if err != nil {
		t.Fatalf("second Enroll: %v", err)
	}
	if second.Status != "unchanged" || second.Changed() {
		t.Fatalf("second enroll = %+v, want status unchanged", second)
	}
}

func TestEnroll_AdoptsAStandaloneHome(t *testing.T) {
	root := t.TempDir()
	id, err := EnsureDaemonID(root)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	if _, err := Ensure(root, id); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	result, err := Enroll(root, homeID)
	if err != nil {
		t.Fatalf("Enroll a standalone home: %v", err)
	}
	if result.Status != "enrolled" {
		t.Fatalf("enroll = %+v, want status enrolled", result)
	}
	if record := readStoredRecord(t, root); record.HomeDaemonID != homeID {
		t.Fatalf("record home = %q, want %q", record.HomeDaemonID, homeID)
	}
}

func TestEnroll_IntoADataDirThatDoesNotExistYet(t *testing.T) {
	// A home enrolls a remote right after installing the binary there, before any
	// daemon has run: the data dir does not exist and there is no daemon id yet.
	root := filepath.Join(t.TempDir(), "never-started")

	result, err := Enroll(root, homeID)
	if err != nil {
		t.Fatalf("Enroll into a fresh data dir: %v", err)
	}
	if result.Status != "enrolled" {
		t.Fatalf("enroll = %+v, want status enrolled", result)
	}
	if record := readStoredRecord(t, root); record.HomeDaemonID != homeID {
		t.Fatalf("record home = %q, want %q", record.HomeDaemonID, homeID)
	}

	// The daemon that starts there next keeps the enrollment rather than
	// declaring itself a home.
	id, err := EnsureDaemonID(root)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	status, err := Ensure(root, id)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if status.IsHome() || status.HomeDaemonID != homeID {
		t.Fatalf("first start after enrollment = %+v, want an outpost of %q", status, homeID)
	}
}

func TestEnroll_RefusesASecondHome(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, "d-cccccccccccccccccccccccccccccccc")
	if _, err := Enroll(root, homeID); err != nil {
		t.Fatalf("first Enroll: %v", err)
	}

	result, err := Enroll(root, otherHome)
	var foreign *ForeignHomeError
	if !errors.As(err, &foreign) {
		t.Fatalf("Enroll from a second home = %v, want ForeignHomeError", err)
	}
	if result.Status != "refused" {
		t.Fatalf("refused enroll = %+v, want status refused", result)
	}
	if record := readStoredRecord(t, root); record.HomeDaemonID != homeID {
		t.Fatalf("refused enroll overwrote the record: home = %q, want %q", record.HomeDaemonID, homeID)
	}
	message := foreign.Error()
	for _, want := range []string{homeID, otherHome, "attn enrollment leave", PlanPath} {
		if !strings.Contains(message, want) {
			t.Fatalf("re-home refusal does not name %q:\n%s", want, message)
		}
	}
}

func TestEnroll_RejectsSelfAndMalformedHomes(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, homeID)

	if _, err := Enroll(root, homeID); err == nil {
		t.Fatal("Enroll to its own id succeeded, want refusal")
	}
	if _, err := Enroll(root, "not-a-daemon-id"); err == nil {
		t.Fatal("Enroll with a malformed home id succeeded, want refusal")
	}
}

func TestLeave_MakesTheDaemonAHomeAgain(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, otherHome)
	if _, err := Enroll(root, homeID); err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	result, err := Leave(root)
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if result.Status != "left" || result.PreviousHome != homeID {
		t.Fatalf("leave = %+v, want status left with previous home %q", result, homeID)
	}

	status, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !status.IsHome() {
		t.Fatalf("daemon is not a home after leaving: %+v", status)
	}

	// The way out is what unblocks the second home; enrolling now succeeds.
	if _, err := Enroll(root, homeID); err != nil {
		t.Fatalf("Enroll after Leave: %v", err)
	}
}

func TestLeave_AlreadyHomeIsUnchanged(t *testing.T) {
	root := t.TempDir()
	id, err := EnsureDaemonID(root)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	if _, err := Ensure(root, id); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	result, err := Leave(root)
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if result.Status != "unchanged" || result.Changed() {
		t.Fatalf("leave on a home = %+v, want status unchanged", result)
	}
}

func TestLeave_WithoutADaemonIDSaysSo(t *testing.T) {
	root := t.TempDir()
	if _, err := Leave(root); !errors.Is(err, ErrNoDaemonID) {
		t.Fatalf("Leave without a daemon id = %v, want ErrNoDaemonID", err)
	}
}

func TestRequireHome_PassesAtHomeAndNamesTheHomeOnAnOutpost(t *testing.T) {
	home := Status{DaemonID: homeID, HomeDaemonID: homeID}
	if err := home.RequireHome("the garden"); err != nil {
		t.Fatalf("RequireHome on a home daemon: %v", err)
	}

	outpost := Status{DaemonID: otherHome, HomeDaemonID: homeID}
	err := outpost.RequireHome("the garden")
	var fenced *FencedError
	if !errors.As(err, &fenced) {
		t.Fatalf("RequireHome on an outpost = %v, want FencedError", err)
	}
	message := err.Error()
	for _, want := range []string{"the garden", otherHome, homeID, "attn enrollment leave", PlanPath} {
		if !strings.Contains(message, want) {
			t.Fatalf("fence error does not name %q:\n%s", want, message)
		}
	}
}

func TestRequireHome_UnknownEnrollmentFailsClosed(t *testing.T) {
	unknown := Status{DaemonID: homeID}
	err := unknown.RequireHome("the crew")
	if err == nil {
		t.Fatal("RequireHome with no home recorded passed, want a refusal")
	}
	if !strings.Contains(err.Error(), "the crew") {
		t.Fatalf("refusal does not name the surface:\n%s", err.Error())
	}
}

func TestLoad_ReportsAMissingOrCorruptRecord(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, homeID)

	if _, err := Load(root); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("Load without a record = %v, want ErrNoRecord", err)
	}

	if err := os.WriteFile(filepath.Join(root, RecordFileName), []byte("{not json"), 0600); err != nil {
		t.Fatalf("seed corrupt record: %v", err)
	}
	status, err := Load(root)
	if err == nil {
		t.Fatal("Load with a corrupt record succeeded, want an error")
	}
	if status.IsHome() {
		t.Fatalf("corrupt record resolved to a home: %+v", status)
	}
	if err := status.RequireHome("the garden"); err == nil {
		t.Fatal("fence passed on a corrupt record, want it to fail closed")
	}
}

func TestEnroll_ConcurrentCallsAgreeOnOneHome(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, otherHome)

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Enroll(root, homeID); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Enroll: %v", err)
	}
	if record := readStoredRecord(t, root); record.HomeDaemonID != homeID {
		t.Fatalf("record home = %q, want %q", record.HomeDaemonID, homeID)
	}
}
