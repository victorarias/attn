package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workflow"
)

// fakeWorkflowClient is a hermetic in-memory stand-in for the daemon socket
// client. It records every upsert and serves get/list from its own state, so the
// CLI orchestration can be exercised with no daemon, no socket, and no real agent.
type fakeWorkflowClient struct {
	mu sync.Mutex

	runs      map[string]*protocol.WorkflowRun
	callsByID map[string][]protocol.WorkflowAgentCall

	runUpserts  []protocol.WorkflowRun
	callUpserts []protocol.WorkflowAgentCall

	// getStatusOverride, when set for a runID, forces WorkflowRunGet to report that
	// status (used to drive the cancel watcher deterministically).
	getStatusOverride map[string]protocol.WorkflowRunStatus
}

func newFakeWorkflowClient() *fakeWorkflowClient {
	return &fakeWorkflowClient{
		runs:              map[string]*protocol.WorkflowRun{},
		callsByID:         map[string][]protocol.WorkflowAgentCall{},
		getStatusOverride: map[string]protocol.WorkflowRunStatus{},
	}
}

var _ workflowClient = (*fakeWorkflowClient)(nil)

func (f *fakeWorkflowClient) WorkflowRunUpsert(run *protocol.WorkflowRun) (*protocol.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := *run
	f.runUpserts = append(f.runUpserts, stored)
	saved := stored
	f.runs[run.RunID] = &saved
	return f.hydrateLocked(run.RunID), nil
}

func (f *fakeWorkflowClient) WorkflowCallUpsert(runID string, call *protocol.WorkflowAgentCall) (*protocol.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := *call
	if stored.RunID == "" {
		stored.RunID = runID
	}
	f.callUpserts = append(f.callUpserts, stored)

	calls := f.callsByID[runID]
	replaced := false
	for i := range calls {
		if calls[i].Ordinal == stored.Ordinal {
			calls[i] = stored
			replaced = true
			break
		}
	}
	if !replaced {
		calls = append(calls, stored)
	}
	f.callsByID[runID] = calls
	return f.hydrateLocked(runID), nil
}

func (f *fakeWorkflowClient) WorkflowRunGet(runID string) (*protocol.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hydrateLocked(runID), nil
}

func (f *fakeWorkflowClient) WorkflowRunList(sessionID string) ([]protocol.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []protocol.WorkflowRun{}
	for id := range f.runs {
		run := f.hydrateLocked(id)
		if sessionID != "" && protocol.Deref(run.SessionID) != sessionID {
			continue
		}
		out = append(out, *run)
	}
	return out, nil
}

func (f *fakeWorkflowClient) WorkflowRunCancel(runID string) (*protocol.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getStatusOverride[runID] = protocol.WorkflowRunStatusCanceled
	if run, ok := f.runs[runID]; ok {
		run.Status = protocol.WorkflowRunStatusCanceled
	}
	return f.hydrateLocked(runID), nil
}

// hydrateLocked returns a copy of the run with its agent calls and any status
// override applied. Returns nil when the run is absent. Caller holds f.mu.
func (f *fakeWorkflowClient) hydrateLocked(runID string) *protocol.WorkflowRun {
	run, ok := f.runs[runID]
	if !ok {
		return nil
	}
	copied := *run
	if status, ok := f.getStatusOverride[runID]; ok {
		copied.Status = status
	}
	calls := f.callsByID[runID]
	if len(calls) > 0 {
		copied.AgentCalls = append([]protocol.WorkflowAgentCall(nil), calls...)
	}
	return &copied
}

func (f *fakeWorkflowClient) seedRun(run protocol.WorkflowRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := run.AgentCalls
	run.AgentCalls = nil
	saved := run
	f.runs[run.RunID] = &saved
	if len(calls) > 0 {
		f.callsByID[run.RunID] = append([]protocol.WorkflowAgentCall(nil), calls...)
	}
}

func (f *fakeWorkflowClient) callUpsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.callUpserts)
}

func (f *fakeWorkflowClient) lastRunUpsert() (protocol.WorkflowRun, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.runUpserts) == 0 {
		return protocol.WorkflowRun{}, false
	}
	return f.runUpserts[len(f.runUpserts)-1], true
}

// --- arg parsing -----------------------------------------------------------

func TestWorkflowParseRunArgs(t *testing.T) {
	t.Run("args-file exclusive with args", func(t *testing.T) {
		_, err := parseWorkflowRunArgs([]string{"s.js", "--args", "{}", "--args-file", "f.json"}, "")
		if err == nil {
			t.Fatal("expected mutual-exclusion error")
		}
	})

	t.Run("session defaults to ATTN_SESSION_ID", func(t *testing.T) {
		got, err := parseWorkflowRunArgs([]string{"s.js"}, "sess-env")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.session != "sess-env" {
			t.Fatalf("session = %q, want sess-env", got.session)
		}
	})

	t.Run("explicit session overrides env", func(t *testing.T) {
		got, err := parseWorkflowRunArgs([]string{"s.js", "--session", "sess-flag"}, "sess-env")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.session != "sess-flag" {
			t.Fatalf("session = %q, want sess-flag", got.session)
		}
	})

	t.Run("resume and harness default", func(t *testing.T) {
		got, err := parseWorkflowRunArgs([]string{"s.js", "--resume", "wf-1"}, "")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.resume != "wf-1" {
			t.Fatalf("resume = %q, want wf-1", got.resume)
		}
		if got.harness != "codex" {
			t.Fatalf("harness = %q, want codex (default)", got.harness)
		}
	})

	t.Run("missing script", func(t *testing.T) {
		if _, err := parseWorkflowRunArgs(nil, ""); err == nil {
			t.Fatal("expected missing-script error")
		}
	})
}

func TestWorkflowResolveArgsJSON(t *testing.T) {
	t.Run("from --args", func(t *testing.T) {
		got, err := resolveWorkflowArgsJSON(workflowRunArgs{argsInline: `{"a":1}`})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != `{"a":1}` {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("from --args-file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "args.json")
		if err := os.WriteFile(path, []byte(`{"b":2}`), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := resolveWorkflowArgsJSON(workflowRunArgs{argsFile: path})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != `{"b":2}` {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		if _, err := resolveWorkflowArgsJSON(workflowRunArgs{argsInline: `{not json`}); err == nil {
			t.Fatal("expected invalid-JSON error")
		}
	})
}

// --- ipc journal -----------------------------------------------------------

func TestWorkflowIPCJournalProxiesAndMirrors(t *testing.T) {
	fake := newFakeWorkflowClient()
	fake.seedRun(protocol.WorkflowRun{RunID: "wf-1", Status: protocol.WorkflowRunStatusRunning})

	j := NewIPCJournal(fake, "wf-1")

	entry := workflow.JournalEntry{
		Ordinal:    "ord-1",
		PromptHash: "ph",
		SchemaHash: "none",
		Result:     json.RawMessage(`"hi"`),
		Status:     "ok",
	}
	if err := j.Append(entry); err != nil {
		t.Fatalf("append: %v", err)
	}

	// Mirror read path: no network.
	if got, ok := j.Lookup("ord-1"); !ok || got.PromptHash != "ph" {
		t.Fatalf("lookup miss after append: %+v ok=%v", got, ok)
	}
	if len(j.Entries()) != 1 {
		t.Fatalf("entries = %d, want 1", len(j.Entries()))
	}

	// Proxy: a WorkflowCallUpsert reached the fake client.
	if fake.callUpsertCount() != 1 {
		t.Fatalf("call upserts = %d, want 1", fake.callUpsertCount())
	}
	if got := fake.callUpserts[0]; got.Ordinal != "ord-1" || got.Status != protocol.WorkflowAgentCallStatusOk {
		t.Fatalf("proxied call = %+v", got)
	}

	// Upsert overwrites in the mirror and proxies again.
	j.Upsert(workflow.JournalEntry{Ordinal: "ord-1", PromptHash: "ph2", SchemaHash: "none", Status: "ok"})
	if got, _ := j.Lookup("ord-1"); got.PromptHash != "ph2" {
		t.Fatalf("upsert did not overwrite mirror: %+v", got)
	}
	if fake.callUpsertCount() != 2 {
		t.Fatalf("call upserts after upsert = %d, want 2", fake.callUpsertCount())
	}
}

func TestWorkflowIPCJournalSeedsFromDaemon(t *testing.T) {
	fake := newFakeWorkflowClient()
	fake.seedRun(protocol.WorkflowRun{
		RunID:  "wf-1",
		Status: protocol.WorkflowRunStatusRunning,
		AgentCalls: []protocol.WorkflowAgentCall{
			{
				RunID:      "wf-1",
				Ordinal:    "ord-seed",
				PromptHash: protocol.Ptr("seed-ph"),
				SchemaHash: protocol.Ptr("none"),
				ResultJson: protocol.Ptr(`"seeded"`),
				Status:     protocol.WorkflowAgentCallStatusOk,
			},
		},
	})

	j := NewIPCJournal(fake, "wf-1")

	got, ok := j.Lookup("ord-seed")
	if !ok {
		t.Fatal("seeded entry not found in mirror")
	}
	if got.PromptHash != "seed-ph" || string(got.Result) != `"seeded"` {
		t.Fatalf("seeded entry = %+v", got)
	}
	// Seeding is a read, not a write: no proxy call yet.
	if fake.callUpsertCount() != 0 {
		t.Fatalf("seeding should not proxy; got %d call upserts", fake.callUpsertCount())
	}
}

// --- end-to-end orchestration (stub agent, fake client) --------------------

// fixedStub is a deterministic AgentStub returning the same result for every call.
type fixedStub struct {
	result json.RawMessage
}

func (s fixedStub) Run(_ context.Context, _ workflow.AgentCall) (json.RawMessage, error) {
	return s.result, nil
}

func TestWorkflowExecuteRunCompletes(t *testing.T) {
	fake := newFakeWorkflowClient()

	const script = `export const meta={name:'t',description:'d'};
const a = await agent('hi', {schema:{type:'object'}});
return a;`

	runID := "wf-e2e"
	parsed := workflowRunArgs{
		script:  "inline.js",
		harness: "codex",
		session: "sess-1",
		wait:    true,
	}

	// Seed the initial running row exactly as executeWorkflowRun does, then drive the
	// REAL engine path (runWorkflowEngine) with a fake stub — no driverAgent and no
	// codex spawned, and no reimplementation of the engine/finalize wiring in the test.
	if _, err := fake.WorkflowRunUpsert(buildInitialWorkflowRun(parsed, runID, sha256Hex([]byte(script)), parsed.argsJSON)); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	stub := fixedStub{result: json.RawMessage(`{"ok":true}`)}
	exit := runWorkflowEngine(fake, parsed, runID, script, parsed.argsJSON, stub)

	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}

	// A per-call upsert was proxied for the single agent() call.
	if fake.callUpsertCount() < 1 {
		t.Fatalf("expected at least one call upsert, got %d", fake.callUpsertCount())
	}

	last, ok := fake.lastRunUpsert()
	if !ok {
		t.Fatal("expected a final run upsert")
	}
	if last.Status != protocol.WorkflowRunStatusCompleted {
		t.Fatalf("final status = %q, want completed", last.Status)
	}
	if last.ResultJson == nil || *last.ResultJson != `{"ok":true}` {
		t.Fatalf("final result_json = %v", last.ResultJson)
	}
}

// --- cancel watcher --------------------------------------------------------

// ctxAwareBlockingStub blocks in Run until the run context is canceled. Because the
// engine threads the run ctx into AgentStub.Run, a canceled run tears the in-flight
// subagent down directly: Run wakes on ctx.Done() and returns. `started` lets the
// test observe the dispatch is genuinely in flight before it triggers the cancel.
type ctxAwareBlockingStub struct {
	started chan struct{}
	once    sync.Once
}

func (s *ctxAwareBlockingStub) Run(ctx context.Context, _ workflow.AgentCall) (json.RawMessage, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWorkflowCancelWatcherInterruptsRun(t *testing.T) {
	fake := newFakeWorkflowClient()
	runID := "wf-cancel"
	fake.seedRun(protocol.WorkflowRun{RunID: runID, Status: protocol.WorkflowRunStatusRunning})

	stub := &ctxAwareBlockingStub{started: make(chan struct{})}

	const script = `const a = await agent('hi'); return a;`

	journal := NewIPCJournal(fake, runID)
	engine := workflow.New(workflow.Config{
		Stub:            stub,
		Journal:         journal,
		WatchdogTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Fast poll interval so the watcher trips quickly once the fake reports canceled.
	stopWatcher := startCancelWatcher(ctx, cancel, fake, runID, 5*time.Millisecond)
	defer stopWatcher()

	resultCh := make(chan workflow.RunResult, 1)
	go func() {
		res, _ := engine.Run(ctx, script, nil)
		resultCh <- res
	}()

	// Wait for the subagent to be in flight, then mark the run canceled at the
	// daemon. The watcher polls, observes canceled, and cancels ctx; the canceled
	// ctx both wakes the engine's event loop (parked on the await) and tears down the
	// in-flight stub, which honors ctx.Done() directly.
	select {
	case <-stub.started:
	case <-time.After(2 * time.Second):
		t.Fatal("subagent never started")
	}
	if _, err := fake.WorkflowRunCancel(runID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.Status != workflow.StatusInterrupted {
			t.Fatalf("status = %q, want interrupted", res.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not finish after cancel")
	}
}

func TestWorkflowMapRunStatus(t *testing.T) {
	cases := []struct {
		name string
		res  workflow.RunResult
		want protocol.WorkflowRunStatus
	}{
		{"completed", workflow.RunResult{Status: workflow.StatusCompleted}, protocol.WorkflowRunStatusCompleted},
		{"errored", workflow.RunResult{Status: workflow.StatusErrored}, protocol.WorkflowRunStatusFailed},
		{
			"interrupted by cancel",
			workflow.RunResult{Status: workflow.StatusInterrupted, Err: &workflow.ErrInterrupted{Reason: "workflow cancelled"}},
			protocol.WorkflowRunStatusCanceled,
		},
		{
			"interrupted by watchdog timeout is a failure",
			workflow.RunResult{Status: workflow.StatusInterrupted, Err: &workflow.ErrInterrupted{Reason: "workflow exceeded the watchdog timeout"}},
			protocol.WorkflowRunStatusFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapRunStatus(tc.res); got != tc.want {
				t.Fatalf("mapRunStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkflowObserveCanceled(t *testing.T) {
	fake := newFakeWorkflowClient()
	fake.seedRun(protocol.WorkflowRun{RunID: "wf-1", Status: protocol.WorkflowRunStatusRunning})

	if observeCanceled(fake, "wf-1") {
		t.Fatal("running run reported as canceled")
	}
	if _, err := fake.WorkflowRunCancel("wf-1"); err != nil {
		t.Fatal(err)
	}
	if !observeCanceled(fake, "wf-1") {
		t.Fatal("canceled run not observed")
	}
	if observeCanceled(fake, "missing") {
		t.Fatal("absent run reported as canceled")
	}
}

// --- result / show / list output -------------------------------------------

func TestWorkflowResultOutputAndExitCode(t *testing.T) {
	run := &protocol.WorkflowRun{
		RunID:      "wf-1",
		Status:     protocol.WorkflowRunStatusCompleted,
		Phase:      protocol.Ptr("review"),
		ResultJson: protocol.Ptr(`{"value":42}`),
		AgentCalls: []protocol.WorkflowAgentCall{
			{Ordinal: "a", Status: protocol.WorkflowAgentCallStatusOk},
			{Ordinal: "b", Status: protocol.WorkflowAgentCallStatusErrored},
			{Ordinal: "c", Status: protocol.WorkflowAgentCallStatusRunning},
		},
	}

	out := buildWorkflowResultOutput(run)
	if out.Status != "completed" {
		t.Fatalf("status = %q", out.Status)
	}
	if out.Phase != "review" {
		t.Fatalf("phase = %q", out.Phase)
	}
	if string(out.Result) != `{"value":42}` {
		t.Fatalf("result = %s", out.Result)
	}
	if out.CallsTotal != 3 {
		t.Fatalf("calls_total = %d, want 3", out.CallsTotal)
	}
	if out.CallsDone != 2 {
		t.Fatalf("calls_done = %d, want 2 (ok+errored, not running)", out.CallsDone)
	}
	if out.CallsRunning != 1 {
		t.Fatalf("calls_running = %d, want 1", out.CallsRunning)
	}

	// Exit-code logic is pure and testable.
	if got := workflowResultExitCode(protocol.WorkflowRunStatusCompleted); got != 0 {
		t.Fatalf("completed exit = %d, want 0", got)
	}
	for _, s := range []protocol.WorkflowRunStatus{
		protocol.WorkflowRunStatusFailed,
		protocol.WorkflowRunStatusCanceled,
		protocol.WorkflowRunStatusRunning,
	} {
		if got := workflowResultExitCode(s); got != 1 {
			t.Fatalf("%s exit = %d, want 1", s, got)
		}
	}
}

func TestWorkflowResultOutputJSONShape(t *testing.T) {
	run := &protocol.WorkflowRun{
		RunID:      "wf-1",
		Status:     protocol.WorkflowRunStatusFailed,
		LastError:  protocol.Ptr("boom"),
		AgentCalls: []protocol.WorkflowAgentCall{{Ordinal: "a", Status: protocol.WorkflowAgentCallStatusErrored}},
	}
	out := buildWorkflowResultOutput(run)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"status", "calls_total", "calls_done"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing key %q in %s", key, b)
		}
	}
	if decoded["error"] != "boom" {
		t.Fatalf("error = %v", decoded["error"])
	}
	// No result key when result_json is absent.
	if _, ok := decoded["result"]; ok {
		t.Fatalf("result key should be omitted when absent: %s", b)
	}
}

func TestBuildWorkflowShowOutput(t *testing.T) {
	run := &protocol.WorkflowRun{
		RunID:      "wf-9",
		Status:     protocol.WorkflowRunStatusRunning,
		Phase:      protocol.Ptr("review"),
		ScriptPath: "pipeline.js",
		Resumable:  true,
		CreatedAt:  "2026-06-16T22:00:00Z",
		UpdatedAt:  "2026-06-16T22:05:00Z",
		AgentCalls: []protocol.WorkflowAgentCall{
			{
				Ordinal: "ph1/cs@p.js:1#0", Status: protocol.WorkflowAgentCallStatusOk,
				Label: protocol.Ptr("plan"), Phase: protocol.Ptr("plan"),
				ResolvedModel: protocol.Ptr("gpt-5-codex"),
				StartedAt:     protocol.Ptr("2026-06-16T22:00:00Z"),
				CompletedAt:   protocol.Ptr("2026-06-16T22:00:41Z"),
			},
			{
				Ordinal: "ph2/cs@p.js:9#0", Status: protocol.WorkflowAgentCallStatusRunning,
				Label: protocol.Ptr("review changes"), Phase: protocol.Ptr("review"),
				ResolvedModel: protocol.Ptr("gpt-5-codex"),
				StartedAt:     protocol.Ptr("2026-06-16T22:04:00Z"),
			},
		},
	}

	out := buildWorkflowShowOutput(run)
	if out.Status != "running" || out.Phase != "review" || out.Script != "pipeline.js" {
		t.Fatalf("header = %+v", out)
	}
	if out.Progress.CallsTotal != 2 || out.Progress.CallsDone != 1 || out.Progress.CallsRunning != 1 {
		t.Fatalf("progress = %+v, want total=2 done=1 running=1", out.Progress)
	}
	if !strings.Contains(out.Progress.Summary, "running") || !strings.Contains(out.Progress.Summary, "review") {
		t.Fatalf("summary = %q, want it to mention running + phase", out.Progress.Summary)
	}
	if len(out.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(out.Calls))
	}
	// Finished call: elapsed is the fixed started->completed span (41s), label/model carried.
	done := out.Calls[0]
	if done.Label != "plan" || done.Model != "gpt-5-codex" {
		t.Fatalf("done call lost display fields: %+v", done)
	}
	if done.ElapsedSeconds == nil || *done.ElapsedSeconds != 41 {
		t.Fatalf("done elapsed = %v, want 41", done.ElapsedSeconds)
	}
	// In-flight call: surfaced with label/phase/model and a non-nil elapsed (started->now).
	running := out.Calls[1]
	if running.Status != "running" || running.Label != "review changes" || running.Phase != "review" {
		t.Fatalf("running call = %+v", running)
	}
	if running.ElapsedSeconds == nil {
		t.Fatalf("running elapsed should be non-nil (started->now)")
	}
}

func TestBuildWorkflowShowOutputOmitsElapsedWhenNoStart(t *testing.T) {
	run := &protocol.WorkflowRun{
		RunID:  "wf-10",
		Status: protocol.WorkflowRunStatusRunning,
		AgentCalls: []protocol.WorkflowAgentCall{
			// Running but no started_at (e.g. timestamps not yet flushed): elapsed omitted.
			{Ordinal: "x", Status: protocol.WorkflowAgentCallStatusRunning},
		},
	}
	out := buildWorkflowShowOutput(run)
	if out.Calls[0].ElapsedSeconds != nil {
		t.Fatalf("elapsed should be omitted when started_at is empty, got %v", out.Calls[0].ElapsedSeconds)
	}
}

func TestCountWorkflowCallsRunning(t *testing.T) {
	calls := []protocol.WorkflowAgentCall{
		{Status: protocol.WorkflowAgentCallStatusOk},
		{Status: protocol.WorkflowAgentCallStatusErrored},
		{Status: protocol.WorkflowAgentCallStatusSkipped},
		{Status: protocol.WorkflowAgentCallStatusRunning},
		{Status: protocol.WorkflowAgentCallStatusRunning},
	}
	total, done, running := countWorkflowCalls(calls)
	if total != 5 || done != 3 || running != 2 {
		t.Fatalf("counts = (%d,%d,%d), want (5,3,2)", total, done, running)
	}
}

func TestWorkflowListEntries(t *testing.T) {
	runs := []protocol.WorkflowRun{
		{RunID: "wf-1", Status: protocol.WorkflowRunStatusCompleted, ScriptPath: "a.js", CreatedAt: "t1", Resumable: true, Phase: protocol.Ptr("p1")},
		{RunID: "wf-2", Status: protocol.WorkflowRunStatusRunning, ScriptPath: "b.js", CreatedAt: "t2"},
	}
	entries := buildWorkflowListEntries(runs)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].RunID != "wf-1" || entries[0].Script != "a.js" || entries[0].Phase != "p1" || !entries[0].Resumable {
		t.Fatalf("entry[0] = %+v", entries[0])
	}
	if entries[1].Status != "running" || entries[1].Resumable {
		t.Fatalf("entry[1] = %+v", entries[1])
	}
}
