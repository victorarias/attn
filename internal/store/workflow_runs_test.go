package store

import (
	"testing"
)

func strptr(s string) *string { return &s }

// TestWorkflowRunCRUD exercises migration 50 plus the full workflow journal CRUD:
// run upsert/get round-trip, update-on-conflict for runs and calls, the composite
// UNIQUE(run_id, ordinal) upsert, id-ASC list ordering, the optional session
// filter, created_at DESC list ordering, manual child-delete cascade, and nullable
// round-trips. No insertTestSession: session_id/workspace_id are unenforced columns.
func TestWorkflowRunCRUD(t *testing.T) {
	s := New()

	// Migration smoke: tables must exist after New(), and version must be 50.
	var maxVersion int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if maxVersion != 50 {
		t.Fatalf("schema version = %d, want 50", maxVersion)
	}

	// 1. Insert a fully-populated run.
	run := &WorkflowRunRow{
		RunID:       "run-1",
		ScriptPath:  "/scripts/review.js",
		ScriptHash:  "abc123",
		ArgsJSON:    strptr(`{"tag":"v1"}`),
		SessionID:   strptr("sess-A"),
		WorkspaceID: strptr("ws-1"),
		Status:      "running",
		Phase:       strptr("plan"),
		Harness:     strptr("claude"),
		ResultJSON:  strptr(`{"ok":true}`),
		LastError:   nil,
		Resumable:   true,
		CreatedAt:   "2026-06-14T10:00:00Z",
		UpdatedAt:   "2026-06-14T10:01:00Z",
		CompletedAt: nil,
	}
	if err := s.UpsertWorkflowRun(run); err != nil {
		t.Fatalf("UpsertWorkflowRun: %v", err)
	}

	got, err := s.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetWorkflowRun = nil, want run")
	}
	if got.Status != "running" {
		t.Fatalf("Status = %q, want running", got.Status)
	}
	if got.ResultJSON == nil || *got.ResultJSON != `{"ok":true}` {
		t.Fatalf("ResultJSON = %v, want {\"ok\":true}", got.ResultJSON)
	}
	if !got.Resumable {
		t.Fatalf("Resumable = false, want true")
	}
	if got.CompletedAt != nil {
		t.Fatalf("CompletedAt = %v, want nil", got.CompletedAt)
	}

	// 2. Update-on-conflict: same run_id, changed status/phase/completed_at.
	run.Status = "completed"
	run.Phase = strptr("done")
	run.CompletedAt = strptr("2026-06-14T10:05:00Z")
	if err := s.UpsertWorkflowRun(run); err != nil {
		t.Fatalf("UpsertWorkflowRun (update): %v", err)
	}
	all, err := s.ListWorkflowRuns("")
	if err != nil {
		t.Fatalf("ListWorkflowRuns: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("after update, run count = %d, want 1 (update-on-conflict, not insert)", len(all))
	}
	got, _ = s.GetWorkflowRun("run-1")
	if got.Status != "completed" || got.CompletedAt == nil || *got.CompletedAt != "2026-06-14T10:05:00Z" {
		t.Fatalf("update not applied: status=%q completedAt=%v", got.Status, got.CompletedAt)
	}

	// 3. Agent calls: two distinct ordinals, then re-upsert ordinal "0".
	call0 := &WorkflowAgentCallRow{
		RunID:      "run-1",
		Ordinal:    "0",
		PromptHash: strptr("ph0"),
		SchemaHash: strptr("none"),
		ResultJSON: strptr(`"first"`),
		Status:     "ok",
	}
	call1 := &WorkflowAgentCallRow{
		RunID:      "run-1",
		Ordinal:    "1",
		PromptHash: strptr("ph1"),
		SchemaHash: strptr("none"),
		ResultJSON: nil, // nullable round-trip
		Status:     "errored",
		Error:      strptr("boom"),
	}
	if err := s.UpsertWorkflowAgentCall(call0); err != nil {
		t.Fatalf("UpsertWorkflowAgentCall call0: %v", err)
	}
	if err := s.UpsertWorkflowAgentCall(call1); err != nil {
		t.Fatalf("UpsertWorkflowAgentCall call1: %v", err)
	}

	// Re-upsert ordinal "0" with a changed status/result — overwrite, not duplicate.
	call0.Status = "skipped"
	call0.ResultJSON = strptr(`"first-EDITED"`)
	if err := s.UpsertWorkflowAgentCall(call0); err != nil {
		t.Fatalf("UpsertWorkflowAgentCall call0 (overwrite): %v", err)
	}

	calls, err := s.ListWorkflowAgentCalls("run-1")
	if err != nil {
		t.Fatalf("ListWorkflowAgentCalls: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("call count = %d, want 2 (composite-key overwrite, not duplicate)", len(calls))
	}
	// id ASC: ordinal "0" was inserted first, so it stays first even after re-upsert.
	if calls[0].Ordinal != "0" || calls[1].Ordinal != "1" {
		t.Fatalf("ordering = [%q,%q], want [0,1] (id ASC)", calls[0].Ordinal, calls[1].Ordinal)
	}
	if calls[0].Status != "skipped" || calls[0].ResultJSON == nil || *calls[0].ResultJSON != `"first-EDITED"` {
		t.Fatalf("ordinal 0 not overwritten: status=%q result=%v", calls[0].Status, calls[0].ResultJSON)
	}
	// Nullable round-trip: call1's ResultJSON was nil — must read back nil, not &"".
	if calls[1].ResultJSON != nil {
		t.Fatalf("ordinal 1 ResultJSON = %v, want nil", calls[1].ResultJSON)
	}
	if calls[1].Error == nil || *calls[1].Error != "boom" {
		t.Fatalf("ordinal 1 Error = %v, want boom", calls[1].Error)
	}

	// 4. GetLatestWorkflowAgentCall = highest id = ordinal "1" (re-upsert of "0"
	// does not change its id).
	latest, err := s.GetLatestWorkflowAgentCall("run-1")
	if err != nil {
		t.Fatalf("GetLatestWorkflowAgentCall: %v", err)
	}
	if latest == nil || latest.Ordinal != "1" {
		t.Fatalf("latest call ordinal = %v, want 1", latest)
	}

	// 5. Session filter + created_at DESC ordering. Add a second run, newer, with a
	// different session.
	run2 := &WorkflowRunRow{
		RunID:      "run-2",
		ScriptPath: "/scripts/other.js",
		ScriptHash: "def456",
		SessionID:  strptr("sess-B"),
		Status:     "running",
		CreatedAt:  "2026-06-14T11:00:00Z", // newer than run-1
		UpdatedAt:  "2026-06-14T11:00:00Z",
	}
	if err := s.UpsertWorkflowRun(run2); err != nil {
		t.Fatalf("UpsertWorkflowRun run2: %v", err)
	}

	all, _ = s.ListWorkflowRuns("")
	if len(all) != 2 {
		t.Fatalf("list-all count = %d, want 2", len(all))
	}
	if all[0].RunID != "run-2" || all[1].RunID != "run-1" {
		t.Fatalf("ordering = [%q,%q], want [run-2,run-1] (created_at DESC)", all[0].RunID, all[1].RunID)
	}

	filtered, err := s.ListWorkflowRuns("sess-A")
	if err != nil {
		t.Fatalf("ListWorkflowRuns(sess-A): %v", err)
	}
	if len(filtered) != 1 || filtered[0].RunID != "run-1" {
		t.Fatalf("session filter = %v, want only run-1", filtered)
	}

	// 6. Delete cascade (manual child delete).
	if err := s.DeleteWorkflowRun("run-1"); err != nil {
		t.Fatalf("DeleteWorkflowRun: %v", err)
	}
	gone, _ := s.GetWorkflowRun("run-1")
	if gone != nil {
		t.Fatalf("run-1 still present after delete: %v", gone)
	}
	orphans, _ := s.ListWorkflowAgentCalls("run-1")
	if len(orphans) != 0 {
		t.Fatalf("child calls survived delete = %d, want 0 (manual cascade)", len(orphans))
	}
	// run-2 untouched.
	if survivor, _ := s.GetWorkflowRun("run-2"); survivor == nil {
		t.Fatal("run-2 deleted by run-1 delete")
	}
}

// TestWorkflowMigrationIdempotentOnReopen proves migration 50 is idempotent: a
// second OpenDB over the same on-disk DB re-runs migrateDB without error and leaves
// the schema intact.
func TestWorkflowMigrationIdempotentOnReopen(t *testing.T) {
	dbPath := t.TempDir() + "/attn.db"

	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB (first): %v", err)
	}
	s1 := &Store{db: db1}
	if err := s1.UpsertWorkflowRun(&WorkflowRunRow{
		RunID:      "run-x",
		ScriptPath: "/s.js",
		ScriptHash: "h",
		Status:     "running",
		CreatedAt:  "2026-06-14T10:00:00Z",
		UpdatedAt:  "2026-06-14T10:00:00Z",
	}); err != nil {
		t.Fatalf("UpsertWorkflowRun: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	// Reopen: migrateDB runs again; CREATE TABLE/INDEX IF NOT EXISTS must be no-ops.
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB (reopen): %v", err)
	}
	defer db2.Close()
	s2 := &Store{db: db2}

	got, err := s2.GetWorkflowRun("run-x")
	if err != nil {
		t.Fatalf("GetWorkflowRun after reopen: %v", err)
	}
	if got == nil || got.RunID != "run-x" {
		t.Fatalf("run-x not persisted across reopen: %v", got)
	}

	var maxVersion int
	if err := db2.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		t.Fatalf("read schema_migrations after reopen: %v", err)
	}
	if maxVersion != 50 {
		t.Fatalf("schema version after reopen = %d, want 50", maxVersion)
	}
}
