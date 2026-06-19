package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// newWorkflowTestDaemon builds a hermetic daemon: an in-memory store and no
// wsHub. Workflow methods lazy-init their maps and guard the nil wsHub, so this
// bare construction exercises the production code paths without a live socket.
func newWorkflowTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	return &Daemon{store: store.New()}
}

func sampleWorkflowRun(runID string) *protocol.WorkflowRun {
	return &protocol.WorkflowRun{
		RunID:       runID,
		ScriptPath:  "/repo/.attn/workflows/ship.ts",
		ScriptHash:  "hash-script",
		ArgsJson:    protocol.Ptr(`{"target":"main"}`),
		SessionID:   protocol.Ptr("sess-1"),
		WorkspaceID: protocol.Ptr("ws-1"),
		Status:      protocol.WorkflowRunStatusRunning,
		Phase:       protocol.Ptr("plan"),
		Harness:     protocol.Ptr("claude"),
		Resumable:   true,
		CreatedAt:   "2026-06-14T10:00:00Z",
		UpdatedAt:   "2026-06-14T10:00:00Z",
	}
}

func sampleWorkflowCall(runID, ordinal string, status protocol.WorkflowAgentCallStatus) protocol.WorkflowAgentCall {
	return protocol.WorkflowAgentCall{
		RunID:         runID,
		Ordinal:       ordinal,
		Label:         protocol.Ptr("plan-step"),
		Phase:         protocol.Ptr("plan"),
		ResolvedModel: protocol.Ptr("claude-opus-4-8"),
		AgentType:     protocol.Ptr("planner"),
		ResultJson:    protocol.Ptr(`{"ok":true}`),
		Status:        status,
		StartedAt:     protocol.Ptr("2026-06-14T10:00:01Z"),
	}
}

// upsertOverPipe drives handleWorkflowRunUpsert across an in-memory pipe and
// returns the decoded action result, mirroring the daemon's socket dispatch so the
// test exercises the real handler (guard + persist + reply), not just the core.
func upsertOverPipe(t *testing.T, d *Daemon, run *protocol.WorkflowRun) *protocol.WorkflowActionResultMessage {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleWorkflowRunUpsert(serverConn, &protocol.WorkflowRunUpsertMessage{Run: *run})
		_ = serverConn.Close()
	}()
	var resp protocol.WorkflowActionResultMessage
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode workflow action result: %v", err)
	}
	_ = clientConn.Close()
	<-done
	return &resp
}

func TestGuardWorkflowRunStartEnforcesWorkflowsEnabled(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	// Default (unset) is disabled: a running-status start is refused.
	if err := d.guardWorkflowRunStart(sampleWorkflowRun("run-1")); err == nil {
		t.Fatal("expected start to be refused when workflows are disabled")
	}

	// A terminal-status upsert is never a start, so it passes even while disabled —
	// an in-flight run must record its result after the switch flips off.
	finished := sampleWorkflowRun("run-1")
	finished.Status = protocol.WorkflowRunStatusCompleted
	if err := d.guardWorkflowRunStart(finished); err != nil {
		t.Fatalf("terminal upsert should never be gated: %v", err)
	}

	// Enabling the switch lets a start through.
	d.store.SetSetting(SettingWorkflowsEnabled, "true")
	if err := d.guardWorkflowRunStart(sampleWorkflowRun("run-1")); err != nil {
		t.Fatalf("expected start to be allowed when workflows are enabled: %v", err)
	}
}

func TestHandleWorkflowRunUpsertRefusesStartWhenWorkflowsDisabled(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	// Workflows disabled (default): the socket handler rejects the start, returns an
	// error to the client, and persists nothing.
	resp := upsertOverPipe(t, d, sampleWorkflowRun("run-1"))
	if resp.Success {
		t.Fatal("expected upsert to fail when workflows are disabled")
	}
	if resp.Error == nil || !strings.Contains(*resp.Error, "disabled") {
		t.Fatalf("expected a 'disabled' error, got %v", resp.Error)
	}
	if got, err := d.getWorkflowRunHydrated("run-1"); err != nil {
		t.Fatalf("getWorkflowRunHydrated: %v", err)
	} else if got != nil {
		t.Fatal("refused run must not be persisted")
	}

	// Enable workflows: the same start now succeeds and persists.
	d.store.SetSetting(SettingWorkflowsEnabled, "true")
	resp = upsertOverPipe(t, d, sampleWorkflowRun("run-1"))
	if !resp.Success {
		t.Fatalf("expected upsert to succeed when enabled, error=%v", protocol.Deref(resp.Error))
	}
	if got, err := d.getWorkflowRunHydrated("run-1"); err != nil {
		t.Fatalf("getWorkflowRunHydrated: %v", err)
	} else if got == nil {
		t.Fatal("expected run to be persisted when enabled")
	}
}

func TestWorkflowRunUpsertHydratesRoundTrip(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	run := sampleWorkflowRun("run-1")
	run.AgentCalls = []protocol.WorkflowAgentCall{
		sampleWorkflowCall("run-1", "0", protocol.WorkflowAgentCallStatusOk),
		sampleWorkflowCall("run-1", "1", protocol.WorkflowAgentCallStatusRunning),
	}

	hydrated, err := d.applyWorkflowRunUpsert(run)
	if err != nil {
		t.Fatalf("applyWorkflowRunUpsert: %v", err)
	}
	if hydrated == nil {
		t.Fatal("expected hydrated run, got nil")
	}

	// Re-fetch independently to prove persistence (not just the returned value).
	got, err := d.getWorkflowRunHydrated("run-1")
	if err != nil {
		t.Fatalf("getWorkflowRunHydrated: %v", err)
	}
	if got == nil {
		t.Fatal("expected run, got nil")
	}

	if got.Status != protocol.WorkflowRunStatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if protocol.Deref(got.ArgsJson) != `{"target":"main"}` {
		t.Errorf("ArgsJson not preserved: %q", protocol.Deref(got.ArgsJson))
	}
	if protocol.Deref(got.SessionID) != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", protocol.Deref(got.SessionID))
	}
	if protocol.Deref(got.Phase) != "plan" {
		t.Errorf("Phase = %q, want plan", protocol.Deref(got.Phase))
	}
	if !got.Resumable {
		t.Error("Resumable not preserved")
	}
	if len(got.AgentCalls) != 2 {
		t.Fatalf("AgentCalls = %d, want 2", len(got.AgentCalls))
	}
	if got.AgentCalls[0].Ordinal != "0" || got.AgentCalls[1].Ordinal != "1" {
		t.Errorf("calls out of append order: %q, %q", got.AgentCalls[0].Ordinal, got.AgentCalls[1].Ordinal)
	}
	if got.AgentCalls[0].Status != protocol.WorkflowAgentCallStatusOk {
		t.Errorf("call[0].Status = %q, want ok", got.AgentCalls[0].Status)
	}
	if protocol.Deref(got.AgentCalls[1].StartedAt) != "2026-06-14T10:00:01Z" {
		t.Errorf("call[1].StartedAt not preserved: %q", protocol.Deref(got.AgentCalls[1].StartedAt))
	}
}

func TestWorkflowCallUpsertConflictUpdatesInPlace(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	if _, err := d.applyWorkflowRunUpsert(sampleWorkflowRun("run-1")); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// Append a call.
	call := sampleWorkflowCall("run-1", "0", protocol.WorkflowAgentCallStatusRunning)
	if _, err := d.applyWorkflowCallUpsert("run-1", &call); err != nil {
		t.Fatalf("first call upsert: %v", err)
	}

	// Upsert the SAME (run_id, ordinal): must update in place, not append.
	call.Status = protocol.WorkflowAgentCallStatusOk
	call.CompletedAt = protocol.Ptr("2026-06-14T10:05:00Z")
	run, err := d.applyWorkflowCallUpsert("run-1", &call)
	if err != nil {
		t.Fatalf("second call upsert: %v", err)
	}

	if len(run.AgentCalls) != 1 {
		t.Fatalf("AgentCalls = %d, want 1 (ON CONFLICT updates in place)", len(run.AgentCalls))
	}
	if run.AgentCalls[0].Status != protocol.WorkflowAgentCallStatusOk {
		t.Errorf("call status = %q, want ok after update", run.AgentCalls[0].Status)
	}
	if protocol.Deref(run.AgentCalls[0].CompletedAt) != "2026-06-14T10:05:00Z" {
		t.Errorf("CompletedAt not updated: %q", protocol.Deref(run.AgentCalls[0].CompletedAt))
	}

	// A fresh ordinal appends.
	call2 := sampleWorkflowCall("run-1", "1", protocol.WorkflowAgentCallStatusRunning)
	run, err = d.applyWorkflowCallUpsert("run-1", &call2)
	if err != nil {
		t.Fatalf("third call upsert: %v", err)
	}
	if len(run.AgentCalls) != 2 {
		t.Fatalf("AgentCalls = %d, want 2 after fresh ordinal", len(run.AgentCalls))
	}
}

func TestWorkflowRunListFiltersBySessionNewestFirst(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	older := sampleWorkflowRun("run-old")
	older.SessionID = protocol.Ptr("sess-a")
	older.CreatedAt = "2026-06-14T09:00:00Z"
	newer := sampleWorkflowRun("run-new")
	newer.SessionID = protocol.Ptr("sess-a")
	newer.CreatedAt = "2026-06-14T11:00:00Z"
	other := sampleWorkflowRun("run-other")
	other.SessionID = protocol.Ptr("sess-b")
	other.CreatedAt = "2026-06-14T10:00:00Z"

	for _, r := range []*protocol.WorkflowRun{older, newer, other} {
		if _, err := d.applyWorkflowRunUpsert(r); err != nil {
			t.Fatalf("seed %s: %v", r.RunID, err)
		}
	}

	runs, err := d.listWorkflowRunsHydrated("sess-a")
	if err != nil {
		t.Fatalf("list sess-a: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("len = %d, want 2 for sess-a", len(runs))
	}
	if runs[0].RunID != "run-new" || runs[1].RunID != "run-old" {
		t.Errorf("not newest-first: %q, %q", runs[0].RunID, runs[1].RunID)
	}

	all, err := d.listWorkflowRunsHydrated("")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len all = %d, want 3", len(all))
	}
}

// fakeWorkflowEngineSink records control frames relayed to the engine.
type fakeWorkflowEngineSink struct {
	mu      sync.Mutex
	frames  []interface{}
	sendErr error
}

func (f *fakeWorkflowEngineSink) sendWorkflowControl(msg interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, msg)
	return f.sendErr
}

func (f *fakeWorkflowEngineSink) received() []interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]interface{}(nil), f.frames...)
}

func TestWorkflowRunCancelRelaysAndPersists(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	if _, err := d.applyWorkflowRunUpsert(sampleWorkflowRun("run-1")); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	sink := &fakeWorkflowEngineSink{}
	d.registerWorkflowEngine("run-1", sink)

	run, relayed, err := d.cancelWorkflowRun("run-1")
	if err != nil {
		t.Fatalf("cancelWorkflowRun: %v", err)
	}
	if !relayed {
		t.Error("expected relayed=true, a sink was registered")
	}
	if run == nil || run.Status != protocol.WorkflowRunStatusCanceled {
		t.Fatalf("run not canceled: %+v", run)
	}
	if run.CompletedAt == nil {
		t.Error("CompletedAt not set on cancel")
	}

	// Persistence: re-fetch shows canceled.
	got, err := d.getWorkflowRunHydrated("run-1")
	if err != nil {
		t.Fatalf("refetch: %v", err)
	}
	if got.Status != protocol.WorkflowRunStatusCanceled {
		t.Errorf("persisted status = %q, want canceled", got.Status)
	}

	// The fake received exactly one cancel control frame for this run.
	frames := sink.received()
	if len(frames) != 1 {
		t.Fatalf("relayed frames = %d, want 1", len(frames))
	}
	control, ok := frames[0].(protocol.WorkflowRunCancelMessage)
	if !ok {
		t.Fatalf("control frame type = %T, want WorkflowRunCancelMessage", frames[0])
	}
	if control.Cmd != protocol.CmdWorkflowRunCancel || control.RunID != "run-1" {
		t.Errorf("control frame = %+v, want cancel for run-1", control)
	}
}

func TestWorkflowRunCancelUnknownRunNoError(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	run, relayed, err := d.cancelWorkflowRun("missing")
	if err != nil {
		t.Fatalf("cancel unknown: unexpected err %v", err)
	}
	if run != nil {
		t.Errorf("expected nil run for unknown, got %+v", run)
	}
	if relayed {
		t.Error("expected relayed=false for unknown run")
	}
}

func TestWorkflowBroadcastCoalescesPerRun(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	var (
		mu        sync.Mutex
		broadcast []*protocol.WorkflowRunUpdatedMessage
	)
	d.workflowBroadcastHook = func(msg *protocol.WorkflowRunUpdatedMessage) {
		mu.Lock()
		broadcast = append(broadcast, msg)
		mu.Unlock()
	}

	run := sampleWorkflowRun("run-1")
	run.AgentCalls = []protocol.WorkflowAgentCall{
		sampleWorkflowCall("run-1", "0", protocol.WorkflowAgentCallStatusOk),
	}
	if _, err := d.applyWorkflowRunUpsert(run); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// Ten rapid dirties for one run should collapse into ONE broadcast.
	for i := 0; i < 10; i++ {
		d.markWorkflowRunDirty("run-1")
	}
	d.flushWorkflowBroadcasts()

	mu.Lock()
	got := append([]*protocol.WorkflowRunUpdatedMessage(nil), broadcast...)
	mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("broadcasts = %d, want exactly 1 (coalesced)", len(got))
	}
	if got[0].Event != protocol.EventWorkflowRunUpdated {
		t.Errorf("event = %q, want %q", got[0].Event, protocol.EventWorkflowRunUpdated)
	}
	// The broadcast carries the FULL hydrated run, including its calls.
	if got[0].Run.RunID != "run-1" {
		t.Errorf("broadcast run id = %q, want run-1", got[0].Run.RunID)
	}
	if len(got[0].Run.AgentCalls) != 1 {
		t.Errorf("broadcast run AgentCalls = %d, want 1 (full hydrated run)", len(got[0].Run.AgentCalls))
	}
}

func TestWorkflowBroadcastFlushesEachDirtyRunOnce(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	seen := map[string]int{}
	var mu sync.Mutex
	d.workflowBroadcastHook = func(msg *protocol.WorkflowRunUpdatedMessage) {
		mu.Lock()
		seen[msg.Run.RunID]++
		mu.Unlock()
	}

	for _, id := range []string{"run-a", "run-b"} {
		r := sampleWorkflowRun(id)
		if _, err := d.applyWorkflowRunUpsert(r); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	d.markWorkflowRunDirty("run-a")
	d.markWorkflowRunDirty("run-b")
	d.flushWorkflowBroadcasts()

	mu.Lock()
	defer mu.Unlock()
	if seen["run-a"] != 1 || seen["run-b"] != 1 {
		t.Fatalf("expected one broadcast per run, got %v", seen)
	}
}

// TestDaemonAggregateAttentionSurfacesStoreBackedRuns proves the PRODUCTION
// attention path end to end: runs persisted through the real upsert core are
// read back from the store by the daemon, fed into attention.Aggregate, and a
// finished run surfaces while a still-running one does not. This is the live
// production Aggregate caller the read-model relies on — exercised here over the
// store, not a hand-built run slice.
func TestDaemonAggregateAttentionSurfacesStoreBackedRuns(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	done := sampleWorkflowRun("run-done")
	done.Status = protocol.WorkflowRunStatusCompleted
	done.CompletedAt = protocol.Ptr("2026-06-14T12:00:00Z")

	running := sampleWorkflowRun("run-go") // default status is running

	for _, r := range []*protocol.WorkflowRun{done, running} {
		if _, err := d.applyWorkflowRunUpsert(r); err != nil {
			t.Fatalf("seed %s: %v", r.RunID, err)
		}
	}

	result := d.aggregateAttention()
	if result.WorkflowCount != 1 {
		t.Fatalf("WorkflowCount = %d, want 1 (only the completed run)", result.WorkflowCount)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1", result.TotalCount)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "run-done" {
		t.Fatalf("items = %+v, want only run-done", result.Items)
	}
	if result.Items[0].Reason != string(protocol.WorkflowRunStatusCompleted) {
		t.Errorf("reason = %q, want completed", result.Items[0].Reason)
	}
}

// TestWorkflowFlushRecomputesAttention proves the live surface is driven: when a
// finished run is broadcast on flush, the daemon recomputes the unified
// attention view through the aggregator. The hook observes the production result
// without scraping the log.
func TestWorkflowFlushRecomputesAttention(t *testing.T) {
	d := newWorkflowTestDaemon(t)

	var (
		mu      sync.Mutex
		results []attention.Result
	)
	d.workflowAttentionHook = func(r attention.Result) {
		mu.Lock()
		results = append(results, r)
		mu.Unlock()
	}

	done := sampleWorkflowRun("run-done")
	done.Status = protocol.WorkflowRunStatusFailed
	done.CompletedAt = protocol.Ptr("2026-06-14T12:00:00Z")
	if _, err := d.applyWorkflowRunUpsert(done); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	d.markWorkflowRunDirty("run-done")
	d.flushWorkflowBroadcasts()

	mu.Lock()
	defer mu.Unlock()
	if len(results) == 0 {
		t.Fatal("flush did not recompute attention")
	}
	last := results[len(results)-1]
	if last.WorkflowCount != 1 {
		t.Fatalf("recomputed WorkflowCount = %d, want 1 (failed run surfaces)", last.WorkflowCount)
	}
	if len(last.Items) != 1 || last.Items[0].Reason != string(protocol.WorkflowRunStatusFailed) {
		t.Errorf("recomputed item = %+v, want failed run-done", last.Items)
	}
}

func TestWorkflowAttentionSurfacesFinishedRuns(t *testing.T) {
	completed := *sampleWorkflowRun("run-done")
	completed.Status = protocol.WorkflowRunStatusCompleted
	completed.CompletedAt = protocol.Ptr("2026-06-14T12:00:00Z")

	failed := *sampleWorkflowRun("run-fail")
	failed.Status = protocol.WorkflowRunStatusFailed
	failed.CompletedAt = protocol.Ptr("2026-06-14T11:00:00Z")

	running := *sampleWorkflowRun("run-go")
	running.Status = protocol.WorkflowRunStatusRunning

	canceled := *sampleWorkflowRun("run-cancel")
	canceled.Status = protocol.WorkflowRunStatusCanceled
	canceled.CompletedAt = protocol.Ptr("2026-06-14T13:00:00Z")

	agg := attention.NewAggregator(nil, nil)
	result := agg.Aggregate(nil, nil, []protocol.WorkflowRun{completed, failed, running, canceled})

	if result.WorkflowCount != 2 {
		t.Fatalf("WorkflowCount = %d, want 2 (completed + failed)", result.WorkflowCount)
	}
	if result.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", result.TotalCount)
	}

	ids := map[string]string{}
	for _, item := range result.Items {
		if item.Kind != "workflow" {
			t.Errorf("unexpected kind %q", item.Kind)
		}
		ids[item.ID] = item.Reason
	}
	if ids["run-done"] != string(protocol.WorkflowRunStatusCompleted) {
		t.Errorf("run-done reason = %q, want completed", ids["run-done"])
	}
	if ids["run-fail"] != string(protocol.WorkflowRunStatusFailed) {
		t.Errorf("run-fail reason = %q, want failed", ids["run-fail"])
	}
	if _, ok := ids["run-go"]; ok {
		t.Error("running run must not raise attention")
	}
	if _, ok := ids["run-cancel"]; ok {
		t.Error("canceled run must not raise attention")
	}

	// Label uses the script base name.
	for _, item := range result.Items {
		if item.Label != "ship.ts" {
			t.Errorf("label = %q, want ship.ts", item.Label)
		}
	}
}
