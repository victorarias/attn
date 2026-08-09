package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/transcript"
)

// installActivityRunner wires the activity executor onto a fast in-test queue and
// turns the feature on with a Claude config the fake executable satisfies.
func installActivityRunner(t *testing.T, d *Daemon) {
	t.Helper()
	d.store.SetSetting(SettingActivityEnabled, "true")
	d.store.SetSetting(SettingActivityConfig, `{"agent":"claude","model":"claude-haiku-4-5"}`)
	d.store.SetSetting(canonicalExecutableSettingKey("claude"), writeFakeAgentExecutable(t))

	runner := jobs.New(jobs.Options{
		Store:        newTestJobStore(t, d),
		Log:          func(string, ...interface{}) {},
		PollInterval: 2 * time.Millisecond,
	})
	if err := runner.RegisterWith(sessionActivityKind, d.sessionActivityHandler,
		jobs.HandlerConfig{Timeout: sessionActivityTimeout}); err != nil {
		t.Fatalf("register session_activity: %v", err)
	}
	if err := runner.Start(); err != nil {
		t.Fatalf("start runner: %v", err)
	}
	t.Cleanup(runner.Stop)
	d.jobQueue = runner
}

// watchingClient puts the daemon in the watching tier, which is what every test
// that expects a run needs — away is a hard stop.
func watchingClient(d *Daemon) {
	d.wsHub.clients[&wsClient{presence: clientPresence{
		Visible:          true,
		DashboardVisible: true,
		ReportedAt:       time.Now(),
	}}] = true
}

func addActivitySession(t *testing.T, d *Daemon, id string, state protocol.SessionState) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentClaude,
		Directory: t.TempDir(), State: state,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

// writeActivityTranscript writes a Claude JSONL transcript and returns its path.
func writeActivityTranscript(t *testing.T, texts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendActivityTranscript(t, path, texts...)
	return path
}

func appendActivityTranscript(t *testing.T, path string, texts ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	for _, text := range texts {
		record, err := json.Marshal(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
			},
		})
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if _, err := f.Write(append(record, '\n')); err != nil {
			t.Fatalf("write record: %v", err)
		}
	}
}

func TestActivityExecutorStoresTheGeneratedLine(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "I am going to run the frontend tests now")
	// Seed a cursor so the run reads a delta rather than taking the cold-start
	// path, then append the event the line should be about.
	d.store.UpdateSessionActivity("session-1", "reading the plan", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "the suite is running")

	var seen agentdriver.HeadlessTaskRequest
	d.sessionActivityExecution = func(_ context.Context, _ agentdriver.HeadlessTaskProvider, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		seen = req
		return agentdriver.HeadlessTaskResult{Text: "running the frontend test suite."}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	stored := d.store.GetSessionActivity("session-1")
	// The trailing period is sanitized away: a dashboard row is a fragment, not
	// a sentence.
	if stored.Line != "running the frontend test suite" {
		t.Errorf("line = %q", stored.Line)
	}
	if stored.Cursor == "" {
		t.Error("cursor was not advanced, so the next run would re-read this window")
	}

	// The run must be tool-less and bounded, and must split its prompt so the
	// invariant half replaces the CLI's own system prompt.
	if !seen.DisableTools {
		t.Error("the run was allowed tools; an activity line has no business touching the filesystem")
	}
	if len(seen.OutputSchema) != 0 {
		t.Error("the run asked for a schema; the answer is the final text")
	}
	if strings.TrimSpace(seen.SystemPrompt) == "" {
		t.Error("SystemPrompt is empty, so the run pays the CLI's full system prefix")
	}
	if !strings.Contains(seen.Prompt, "the suite is running") {
		t.Errorf("prompt does not carry the new events:\n%s", seen.Prompt)
	}
	// The previous line is the anchor that makes a near-empty window useful.
	if !strings.Contains(seen.Prompt, "reading the plan") {
		t.Errorf("prompt does not carry the previous line:\n%s", seen.Prompt)
	}
	if !strings.Contains(seen.Prompt, string(protocol.SessionStateWorking)) {
		t.Errorf("prompt does not carry the session state:\n%s", seen.Prompt)
	}
}

// A transcript that has not moved has nothing new to say, and the line already
// there is still true. Spending a run on it is the waste the whole design exists
// to avoid.
func TestActivityExecutorSkipsAnUnmovedTranscript(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "already summarized")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), seedActivityCursor(t, transcriptPath))

	ran := false
	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		ran = true
		return agentdriver.HeadlessTaskResult{Text: "something else"}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if ran {
		t.Error("an agent ran for a transcript that had not moved")
	}
	if got := d.store.GetSessionActivity("session-1").Line; got != "running the test suite" {
		t.Errorf("line = %q, want the previous line kept", got)
	}
}

// Cold start must not scan a transcript that can reach tens of megabytes to
// produce a first line, and must not summarize a session's whole history as if
// it were the present.
func TestActivityExecutorSeedsRatherThanScanningOnColdStart(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "ancient history", "more ancient history")

	ran := false
	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		ran = true
		return agentdriver.HeadlessTaskResult{Text: "reading ancient history"}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if ran {
		t.Error("cold start ran an agent over the session's whole history")
	}
	stored := d.store.GetSessionActivity("session-1")
	if stored.Cursor == "" {
		t.Fatal("cold start did not seed a cursor, so the next run repeats it")
	}
	if stored.Line != "" {
		t.Errorf("cold start invented a line: %q", stored.Line)
	}
}

// Claude compaction rewrites the transcript, which invalidates the stored
// cursor. That is normal, not exceptional: re-seed and skip one generation
// rather than failing the job forever against a cursor that will never validate.
func TestActivityExecutorReseedsAfterTheTranscriptIsRewritten(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "before compaction", "still before")
	stale := seedActivityCursor(t, transcriptPath)
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), stale)

	// Compaction: same path, entirely new content, so the cursor's fingerprint
	// no longer matches.
	if err := os.Remove(transcriptPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	appendActivityTranscript(t, transcriptPath, "after compaction")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		t.Error("an agent ran against a cursor the transcript no longer matches")
		return agentdriver.HeadlessTaskResult{}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	stored := d.store.GetSessionActivity("session-1")
	if stored.Cursor == stale || stored.Cursor == "" {
		t.Errorf("cursor = %q, want a fresh seed against the rewritten transcript", stored.Cursor)
	}
	if stored.Line != "running the test suite" {
		t.Errorf("line = %q, want the previous line kept through a re-seed", stored.Line)
	}
}

// A blank answer must never overwrite a line that is still true.
func TestActivityExecutorKeepsThePreviousLineWhenNothingUsableCameBack(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := writeActivityTranscript(t, "first")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "second")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Text: "   \n  "}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)

	if got := d.store.GetSessionActivity("session-1").Line; got != "running the test suite" {
		t.Errorf("line = %q, want the previous line kept", got)
	}
}

// away multiplies the bill by zero, including for a job that was already queued
// when the user walked away.
func TestActivityExecutorGeneratesNothingWhenAway(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	// No client reports presence: the tier is away.

	transcriptPath := writeActivityTranscript(t, "first")
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), seedActivityCursor(t, transcriptPath))
	appendActivityTranscript(t, transcriptPath, "second")

	d.sessionActivityExecution = func(context.Context, agentdriver.HeadlessTaskProvider, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		t.Error("an agent ran while nobody was looking")
		return agentdriver.HeadlessTaskResult{}, nil
	}

	enqueueActivity(t, d, "session-1", transcriptPath)
	waitForTaskState(t, d, sessionActivityKind, "session-1", jobs.StateDone)
}

// Enqueue is where the cheap refusals live: nothing should reach the queue when
// the feature is off or the user is away.
func TestEnqueueSessionActivityRefusesBeforeQueueing(t *testing.T) {
	newDaemon := func(t *testing.T) (*Daemon, string) {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
		installActivityRunner(t, d)
		return d, "session-1"
	}

	t.Run("off", func(t *testing.T) {
		d, id := newDaemon(t)
		watchingClient(d)
		d.store.SetSetting(SettingActivityEnabled, "false")
		d.enqueueSessionActivity(id)
		assertNoActivityJob(t, d, id)
	})

	t.Run("away", func(t *testing.T) {
		d, id := newDaemon(t)
		d.enqueueSessionActivity(id)
		assertNoActivityJob(t, d, id)
	})

	t.Run("a shell split from an agent", func(t *testing.T) {
		d, _ := newDaemon(t)
		watchingClient(d)
		now := string(protocol.TimestampNow())
		d.store.Add(&protocol.Session{
			ID: "shell-1", Label: "shell-1", Agent: protocol.SessionAgentClaude,
			Directory: t.TempDir(), State: protocol.SessionStateWorking,
			ParentSessionID: protocol.Ptr("session-1"),
			StateSince:      now, StateUpdatedAt: now, LastSeen: now,
		})
		d.enqueueSessionActivity("shell-1")
		assertNoActivityJob(t, d, "shell-1")
	})
}

// The scan tick fires far more often than any tier interval, so it is the
// interval — not the tick — that has to hold a session back.
func TestActivityScanRespectsTheTierInterval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)
	d.store.SetSetting(SettingActivityIntervals, `{"watching":120,"present":300}`)

	transcriptPath := discoverableTranscript(t, "session-1", "first", "second")
	// Inside the window, and the transcript HAS moved since the line was
	// generated — so only the interval can hold this session back.
	generatedInsideWindow := time.Now().Add(-10 * time.Second)
	d.store.UpdateSessionActivity("session-1", "running the test suite", generatedInsideWindow, "v1:abc:1:0")
	touchFile(t, transcriptPath, time.Now())

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")

	// Past the interval, with the transcript written since, the same scan
	// enqueues.
	generatedAt := time.Now().Add(-5 * time.Minute)
	d.store.UpdateSessionActivity("session-1", "running the test suite", generatedAt, "v1:abc:1:0")
	touchFile(t, transcriptPath, generatedAt.Add(time.Minute))
	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if job, err := d.jobQueue.GetByKey(sessionActivityKind, "session-1"); err != nil || job == nil {
		t.Fatalf("nothing was queued past the interval (err=%v)", err)
	}
}

// A transcript that has not moved costs nothing however long the interval has
// been elapsed: the existing line is still true. This is what keeps blocked and
// finished sessions free with the dashboard open all day.
func TestActivityScanSkipsASessionThatHasNotWritten(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	transcriptPath := discoverableTranscript(t, "session-1", "first")
	generatedAt := time.Now().Add(-time.Hour)
	d.store.UpdateSessionActivity("session-1", "running the test suite", generatedAt, "v1:abc:1:0")
	// Written before the line was generated: nothing new to say.
	touchFile(t, transcriptPath, generatedAt.Add(-time.Minute))

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")
}

// away is a hard stop for the tick too, not just for a job already queued.
func TestActivityScanGeneratesNothingWhenAway(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)

	discoverableTranscript(t, "session-1", "first")

	if _, err := d.sessionActivityScanHandler(context.Background(), nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertNoActivityJob(t, d, "session-1")
}

// Reaching a state that wants the user is the moment the line matters most, so
// it does not wait out the interval it would otherwise sit inside.
func TestOpeningATurnRefreshesTheActivityLineImmediately(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addActivitySession(t, d, "session-1", protocol.SessionStateWorking)
	installActivityRunner(t, d)
	watchingClient(d)

	discoverableTranscript(t, "session-1", "first")
	// Well inside the watching interval: the scan would refuse this session.
	d.store.UpdateSessionActivity("session-1", "running the test suite", time.Now(), "v1:abc:1:0")

	if !d.applyState(sessionStateChange{
		sessionID: "session-1",
		state:     string(protocol.SessionStateWaitingInput),
		cause:     liveSignal{},
	}) {
		t.Fatal("state was not applied")
	}

	if job, err := d.jobQueue.GetByKey(sessionActivityKind, "session-1"); err != nil || job == nil {
		t.Fatalf("opening a turn did not queue a refresh (err=%v)", err)
	}
}

func TestSessionGeneratesActivity(t *testing.T) {
	agentSession := &protocol.Session{ID: "s1", Agent: protocol.SessionAgentClaude}
	if !sessionGeneratesActivity(agentSession) {
		t.Error("a plain agent session does not generate activity")
	}
	satellite := &protocol.Session{ID: "s2", Agent: protocol.SessionAgentClaude, ParentSessionID: protocol.Ptr("s1")}
	if sessionGeneratesActivity(satellite) {
		t.Error("a satellite shell generates activity; it has no transcript of its own")
	}
	remote := &protocol.Session{ID: "s3", Agent: protocol.SessionAgentClaude, EndpointID: protocol.Ptr("remote-1")}
	if sessionGeneratesActivity(remote) {
		t.Error("a remote session generates activity; its transcript lives on another daemon")
	}
	if sessionGeneratesActivity(nil) {
		t.Error("a nil session generates activity")
	}
}

func enqueueActivity(t *testing.T, d *Daemon, sessionID, transcriptPath string) {
	t.Helper()
	if _, err := d.jobQueue.Enqueue(sessionActivityKind, jobs.EnqueueOptions{
		UniqueKey: sessionID,
		Payload:   sessionActivityPayload{Transcript: transcriptPath},
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func assertNoActivityJob(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	job, err := d.jobQueue.GetByKey(sessionActivityKind, sessionID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job != nil {
		t.Fatalf("a job was queued anyway: %+v", job)
	}
}

// discoverableTranscript writes a transcript where the Claude finder looks, so
// the scan and enqueue paths resolve it exactly as they do in production.
func discoverableTranscript(t *testing.T, sessionID string, texts ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	dir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	appendActivityTranscript(t, path, texts...)
	return path
}

func touchFile(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func seedActivityCursor(t *testing.T, path string) string {
	t.Helper()
	cursor, err := transcript.HeadCursor(path)
	if err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
	return cursor
}
