package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func legacyRow(kind, subject, meta string) store.LegacyTaskRecord {
	at := time.Now().UTC()
	return store.LegacyTaskRecord{
		ID:            kind + ":" + subject,
		Kind:          kind,
		Subject:       subject,
		State:         "queued",
		Attempts:      1,
		NextAttemptAt: at,
		MetaJSON:      meta,
		CreatedAt:     at,
		UpdatedAt:     at,
	}
}

// The upgrade must not lose owed background work, and it must not lose the
// inputs that work depends on — a summarize whose transcript path is dropped
// runs against a session row a teardown already deleted.
func TestImportCarriesEachKindsInputsOntoItsPayload(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	d.importDrainedTasks([]store.LegacyTaskRecord{
		legacyRow(notebookSummarizeSessionKind, "s-1", `{"transcript":"/tmp/turn.jsonl","workspace":"ws-1"}`),
		legacyRow(notebookNarrateWorkspaceKind, "ws-2", `{"daily_pass":"1"}`),
		legacyRow(reconcileKind, "t-3", `{"reconcile_inputs":"{\"TicketID\":\"t-3\",\"Title\":\"ship it\"}"}`),
		legacyRow(compactContextKind, "ws-4", ""),
	})

	d.startJobQueue()
	runner := d.jobQueueRef()
	t.Cleanup(runner.Stop)

	summarize, err := runner.GetByKey(notebookSummarizeSessionKind, "s-1")
	if err != nil || summarize == nil {
		t.Fatalf("imported summarize job: %v (%+v)", err, summarize)
	}
	// The legacy id is preserved: a pre-upgrade failure notification records it as
	// its SourceID and the panel's Retry deep-links through it.
	if summarize.ID != "summarize_session:s-1" {
		t.Fatalf("import minted a new id (%s), breaking existing notification links", summarize.ID)
	}
	var carried summarizeSessionPayload
	if err := summarize.DecodePayload(&carried); err != nil {
		t.Fatalf("decode imported summarize payload: %v", err)
	}
	if carried.Transcript != "/tmp/turn.jsonl" {
		t.Fatalf("imported summarize lost its transcript: %+v", carried)
	}
	if carried.WorkspaceID == nil || *carried.WorkspaceID != "ws-1" {
		t.Fatalf("imported summarize lost its workspace: %v", carried.WorkspaceID)
	}

	narrate, err := runner.GetByKey(notebookNarrateWorkspaceKind, "ws-2")
	if err != nil || narrate == nil {
		t.Fatalf("imported narrate job: %v (%+v)", err, narrate)
	}
	var daily narrateWorkspacePayload
	if err := narrate.DecodePayload(&daily); err != nil {
		t.Fatalf("decode imported narrate payload: %v", err)
	}
	if !daily.DailyPass {
		t.Fatal("imported narrate lost its daily-pass flag, which would turn a no-op refresh into a retried failure")
	}

	reconcile, err := runner.GetByKey(reconcileKind, "t-3")
	if err != nil || reconcile == nil {
		t.Fatalf("imported reconcile job: %v (%+v)", err, reconcile)
	}
	in, err := reconcileInputsFromJob(reconcile)
	if err != nil {
		t.Fatalf("imported reconcile inputs: %v", err)
	}
	if in.TicketID != "t-3" || in.Title != "ship it" {
		t.Fatalf("imported reconcile lost its inputs: %+v", in)
	}

	if compact, err := runner.GetByKey(compactContextKind, "ws-4"); err != nil || compact == nil {
		t.Fatalf("imported compact job: %v (%+v)", err, compact)
	}
}

// A row whose meta cannot be read is still imported. Dropping it would silently
// discard owed work; importing it leaves a record the panel shows and the handler
// reports as missing its inputs.
func TestImportKeepsARowWithUnreadableMeta(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.importDrainedTasks([]store.LegacyTaskRecord{
		legacyRow(notebookSummarizeSessionKind, "s-broken", `{not json`),
	})

	job, ok, err := d.store.GetJob("summarize_session:s-broken")
	if err != nil {
		t.Fatalf("get imported job: %v", err)
	}
	if !ok {
		t.Fatal("a row with unreadable meta was dropped instead of imported")
	}
	if job.Payload != "" {
		t.Fatalf("unreadable meta produced a payload: %q", job.Payload)
	}
}
