package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/activity"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

// sessionActivityTimeout bounds one generation. A tripwire: measured ~5s on
// Codex, ~12s on Claude, worst observed ~19s.
const sessionActivityTimeout = time.Minute

// sessionActivityBudgetUSD caps one run. Set two orders of magnitude above the
// measured cost ($0.0027 on Codex, $0.011–0.017 on Claude) so only a runaway
// touches it.
const sessionActivityBudgetUSD = "0.50"

// sessionActivityConcurrency bounds concurrent generations. Three is roughly
// how many sessions move at once in a working day, so the common case never
// queues and a pathological one degrades into a queue.
const sessionActivityConcurrency = 3

// activityMaxTurns is 2, not 1: a headless run that exhausts its turn budget
// exits non-zero even when it already produced the text.
const activityMaxTurns = 2

// The scan tick, a cron entry on the shared job queue. It fires faster than any
// tier interval on purpose: it decides nothing, it only asks each session
// whether ITS interval has elapsed.
const (
	sessionActivityScanKind     = "session_activity_scan"
	sessionActivityScanInterval = 30 * time.Second
	sessionActivityScanTimeout  = 30 * time.Second
)

// sessionActivityRun is what one session's last pass left behind. Two stamps,
// two gates: ObservedAt is written by every pass ("have we looked since the
// transcript moved"), SpentAt only by a pass that called the agent ("may we
// spend again").
type sessionActivityRun struct {
	ObservedAt time.Time
	SpentAt    time.Time
	// Err is why the last spending run produced nothing; cleared by a run that works.
	Err string
	// Transcript is the last resolved path, ResumeID the resume id it was
	// resolved under. See sessionActivityTranscript.
	Transcript string
	ResumeID   string
}

// sessionActivityRunRecord reads one session's last pass.
func (d *Daemon) sessionActivityRunRecord(sessionID string) sessionActivityRun {
	d.sessionActivityRunsMu.Lock()
	defer d.sessionActivityRunsMu.Unlock()
	return d.sessionActivityRuns[sessionID]
}

// noteSessionActivityRun records a pass. mutate receives the current record so a
// caller can move one stamp without clobbering the other.
func (d *Daemon) noteSessionActivityRun(sessionID string, mutate func(*sessionActivityRun)) {
	d.sessionActivityRunsMu.Lock()
	defer d.sessionActivityRunsMu.Unlock()
	if d.sessionActivityRuns == nil {
		d.sessionActivityRuns = make(map[string]sessionActivityRun)
	}
	record := d.sessionActivityRuns[sessionID]
	mutate(&record)
	d.sessionActivityRuns[sessionID] = record
}

// forgetSessionActivityRuns drops records for sessions that no longer exist, so
// the map cannot outgrow the session list on a long-running daemon.
func (d *Daemon) forgetSessionActivityRuns(live map[string]struct{}) {
	d.sessionActivityRunsMu.Lock()
	defer d.sessionActivityRunsMu.Unlock()
	for id := range d.sessionActivityRuns {
		if _, ok := live[id]; !ok {
			delete(d.sessionActivityRuns, id)
		}
	}
}

// latest returns whichever stamp is later, treating the zero time as "never".
func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// sessionActivityScanHandler enqueues a refresh for every session whose interval
// has elapsed and whose transcript has moved. Both are measured against the last
// PASS, not the last stored line: measuring against the line makes a failing or
// empty generation invisible and re-runs it every tick.
func (d *Daemon) sessionActivityScanHandler(context.Context, *jobs.Job) (any, error) {
	if !d.activityEnabled() {
		return nil, nil
	}
	tier := d.PresenceTier()
	interval := d.activityInterval(tier)
	if interval <= 0 {
		return nil, nil
	}
	// A misconfiguration is worth one log line per tick rather than silence.
	if _, err := d.activityConfigured(); err != nil {
		d.logf("session_activity: enabled but not runnable: %v", err)
		return nil, nil
	}

	now := time.Now()
	live := make(map[string]struct{})
	for _, session := range d.store.List("") {
		live[session.ID] = struct{}{}
		if !sessionGeneratesActivity(session) {
			continue
		}
		stored := d.store.GetSessionActivity(session.ID)
		run := d.sessionActivityRunRecord(session.ID)
		spent := latest(stored.At, run.SpentAt)
		if !spent.IsZero() && now.Sub(spent) < interval {
			continue
		}
		if !d.transcriptMovedSince(session, latest(stored.At, run.ObservedAt)) {
			continue
		}
		d.enqueueSessionActivity(session.ID)
	}
	d.forgetSessionActivityRuns(live)
	return nil, nil
}

// transcriptMovedSince reports whether a session wrote anything since its line
// was generated, via stat rather than the reader because it runs over every
// session on every tick. Never-generated counts as moved: the executor turns
// that into a cursor seed.
func (d *Daemon) transcriptMovedSince(session *protocol.Session, since time.Time) bool {
	if since.IsZero() {
		return true
	}
	path := d.sessionActivityTranscript(session)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.ModTime().After(since)
}

// sessionActivityTranscript resolves a session's transcript path and caches it
// across ticks; resolving it on Codex measured 235–489ms per session. Two
// things invalidate the cache and both are checked: the file going away, and a
// resume under a new id (which writes a new file and leaves the old one
// stat-able), hence the remembered resume id.
func (d *Daemon) sessionActivityTranscript(session *protocol.Session) string {
	if session == nil {
		return ""
	}
	resumeID := strings.TrimSpace(d.store.GetResumeSessionID(session.ID))
	if cached := d.sessionActivityRunRecord(session.ID); cached.Transcript != "" && cached.ResumeID == resumeID {
		if _, err := os.Stat(cached.Transcript); err == nil {
			return cached.Transcript
		}
	}
	path := strings.TrimSpace(d.resolveTranscriptPathForSession(session, ""))
	d.noteSessionActivityRun(session.ID, func(run *sessionActivityRun) {
		run.Transcript = path
		run.ResumeID = resumeID
	})
	return path
}

// sessionActivityPayload carries the transcript path resolved at enqueue time,
// so a debounced run does not depend on the finder agreeing minutes later.
type sessionActivityPayload struct {
	Transcript string `json:"transcript,omitempty"`
}

// enqueueSessionActivity queues a refresh for one session, coalesced by session
// id. Every suppression check lives here rather than in the executor, so a
// suppressed refresh costs a map lookup instead of a queued job.
func (d *Daemon) enqueueSessionActivity(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !d.activityEnabled() {
		return
	}
	// away is a hard stop, checked first because it must cost nothing.
	if d.PresenceTier() == PresenceAway {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || !sessionGeneratesActivity(session) {
		return
	}
	transcriptPath := d.sessionActivityTranscript(session)
	if transcriptPath == "" {
		return
	}
	if _, err := runner.Enqueue(sessionActivityKind, jobs.EnqueueOptions{
		UniqueKey: sessionID,
		Payload:   sessionActivityPayload{Transcript: transcriptPath},
	}); err != nil {
		d.logf("session_activity: enqueue %s: %v", sessionID, err)
	}
}

// sessionGeneratesActivity reports whether a session has something to say;
// shells and satellites do not.
func sessionGeneratesActivity(session *protocol.Session) bool {
	if session == nil {
		return false
	}
	if session.ParentSessionID != nil && strings.TrimSpace(*session.ParentSessionID) != "" {
		return false
	}
	// Remote sessions are deferred: their transcript lives on the remote daemon.
	if session.EndpointID != nil && strings.TrimSpace(*session.EndpointID) != "" {
		return false
	}
	driver := agentdriver.Get(string(session.Agent))
	if driver == nil {
		return false
	}
	_, isFinder := agentdriver.GetTranscriptFinder(driver)
	return isFinder
}

// sessionActivityHandler generates one session's line: read the delta since the
// stored cursor, render it, ask a tool-less agent for one sentence, store it
// with the cursor it was generated through. A missing cursor (cold start) and a
// mismatched one (Claude compaction) both re-seed at head instead of failing.
func (d *Daemon) sessionActivityHandler(ctx context.Context, job *jobs.Job) (any, error) {
	// A run queued before the toggle or tier changed must not spend after it.
	if !d.activityEnabled() || d.PresenceTier() == PresenceAway {
		return nil, nil
	}

	sessionID := strings.TrimSpace(jobSubject(job))
	if sessionID == "" {
		return nil, errors.New("session_activity requires a session id")
	}
	config, err := d.activityConfigured()
	if err != nil {
		return nil, err
	}

	session := d.store.Get(sessionID)
	if session == nil {
		// The session is gone; its line went with it. Nothing to retry.
		return nil, nil
	}
	// Every pass past here counts as having looked, whatever it goes on to do;
	// the scan measures transcript movement from this stamp.
	d.noteSessionActivityRun(sessionID, func(run *sessionActivityRun) {
		run.ObservedAt = time.Now()
	})

	var carried sessionActivityPayload
	if err := job.DecodePayload(&carried); err != nil {
		return nil, err
	}
	transcriptPath := strings.TrimSpace(carried.Transcript)
	if transcriptPath == "" {
		transcriptPath = d.sessionActivityTranscript(session)
	}
	if transcriptPath == "" {
		return nil, nil
	}

	stored := d.store.GetSessionActivity(sessionID)
	if stored.Cursor == "" {
		// Cold start, checked before the read: reading from byte 0 succeeds, which
		// is the problem — a full scan summarizing history as if it were now.
		return nil, d.reseedSessionActivity(sessionID, transcriptPath)
	}
	window, err := activity.Read(transcriptPath, string(session.Agent), stored.Cursor)
	switch {
	case err == nil:
	case errors.Is(err, transcript.ErrCursorMismatch) ||
		errors.Is(err, transcript.ErrCursorPastEnd) ||
		errors.Is(err, transcript.ErrInvalidCursor) ||
		errors.Is(err, activity.ErrDeltaTooLarge):
		// Re-seed and skip: the next movement works against a cursor that validates.
		return nil, d.reseedSessionActivity(sessionID, transcriptPath)
	default:
		return nil, fmt.Errorf("session_activity: read %s: %w", transcriptPath, err)
	}

	if window.Empty() {
		// Nothing appended: the line is still true. Advance the cursor anyway so
		// the next trigger measures movement from here.
		return nil, d.advanceSessionActivityCursor(sessionID, stored, window.NextCursor)
	}

	prompt := activity.Baseline().Render(activity.Input{
		State:       string(session.State),
		StateReason: protocol.Deref(session.StateReason),
		Window:      window.Render(),
		Previous:    stored.Line,
	})

	provider, executablePath, err := d.resolveActivityExecutable(config)
	if err != nil {
		return nil, err
	}
	workDir, err := headlessScratchCwd()
	if err != nil {
		return nil, fmt.Errorf("session_activity: resolve scratch cwd: %w", err)
	}

	run := d.sessionActivityExecution
	if run == nil {
		run = func(ctx context.Context, p agentdriver.HeadlessTaskProvider, r agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return p.RunHeadlessTask(ctx, r)
		}
	}
	// Stamped before the call: a run killed by the timeout spent the same as one
	// that answered, and stamping after would let a hanging run retry every tick.
	d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
		record.SpentAt = time.Now()
	})
	result, err := run(ctx, provider, agentdriver.HeadlessTaskRequest{
		Executable:      executablePath,
		Model:           config.Model,
		ReasoningEffort: config.Effort,
		Prompt:          prompt.User,
		// Replaces the CLI's own interactive-coding system prompt. Measured: the
		// billed prefix drops from 46,745 tokens to 33,955.
		SystemPrompt: prompt.System,
		WorkDir:      workDir,
		// No OutputSchema on purpose: the answer IS the final text, and Codex's
		// tool-free path has no schema support at all.
		DisableTools: true,
		// MaxTurns and MaxBudgetUSD are Claude-only; `codex exec` has no flag for
		// them. What bounds both agents is DisableTools plus ctx's
		// sessionActivityTimeout.
		MaxTurns:     activityMaxTurns,
		MaxBudgetUSD: sessionActivityBudgetUSD,
	})
	if err != nil {
		// Kept, not just logged: a run that always fails is rate limited like a
		// successful one, so nothing else would surface it. `attn activity` reads it.
		d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
			record.Err = err.Error()
		})
		return nil, fmt.Errorf("session_activity: run agent: %w (%s)", err, result.Diagnostics)
	}

	line, ok := activity.Sanitize(result.Text)
	if !ok {
		// Keep whatever line is there — a stale true line beats a blank row — but
		// advance the cursor so the next trigger works from fresh events.
		d.logf("session_activity: session=%s produced no usable line (%s)", sessionID, result.Diagnostics)
		d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
			record.Err = "the agent answered with nothing usable"
		})
		return nil, d.advanceSessionActivityCursor(sessionID, stored, window.NextCursor)
	}
	d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) { record.Err = "" })

	if note := window.Report.String(); note != "" {
		d.logf("session_activity: session=%s window truncated: %s", sessionID, note)
	}
	// Re-checked after the agent call: a user who switched the feature off during
	// it has had every line cleared, and this one would be stranded on home.
	if !d.activityEnabled() {
		return nil, nil
	}
	if !d.store.UpdateSessionActivity(sessionID, line, time.Now(), window.NextCursor) {
		// The row vanished mid-run. Nothing to publish and nothing to retry.
		return nil, nil
	}
	d.publishFact(FactSessionActivityChanged, sessionID, nil)
	d.logf("session_activity: session=%s agent=%s model=%s line=%q", sessionID, config.Agent, config.Model, line)
	return nil, nil
}

// reseedSessionActivity moves the cursor to the transcript head, keeping the
// existing line. Used for a cold start and for a mismatched cursor.
func (d *Daemon) reseedSessionActivity(sessionID, transcriptPath string) error {
	head, err := activity.SeedCursor(transcriptPath)
	if err != nil {
		return fmt.Errorf("session_activity: seed cursor for %s: %w", transcriptPath, err)
	}
	d.store.SetSessionActivityCursor(sessionID, head)
	return nil
}

// advanceSessionActivityCursor moves the cursor forward without changing the
// line. An empty cursor here would silently clear the line.
func (d *Daemon) advanceSessionActivityCursor(sessionID string, stored store.SessionActivity, next string) error {
	if next == "" || next == stored.Cursor {
		return nil
	}
	d.store.SetSessionActivityCursor(sessionID, next)
	return nil
}

// clearAllSessionActivity forgets every line, one fact per session so each row
// re-renders. Used when the feature is switched off, so home stops asserting
// what attn no longer keeps true.
func (d *Daemon) clearAllSessionActivity() {
	for _, session := range d.store.List("") {
		if d.store.GetSessionActivity(session.ID).Line == "" {
			continue
		}
		d.store.UpdateSessionActivity(session.ID, "", time.Time{}, "")
		d.publishFact(FactSessionActivityChanged, session.ID, nil)
	}
}

// handleActivityStatus answers what the daemon believes about session activity
// right now — the only view of it from a terminal or a headless daemon.
func (d *Daemon) handleActivityStatus(conn net.Conn, _ *protocol.ActivityStatusMessage) {
	result := protocol.ActivityStatusResult{
		PresenceTier: d.PresenceTier().String(),
		Enabled:      d.activityEnabled(),
		Sessions:     []protocol.ActivityStatusSession{},
	}
	if _, err := d.activityConfigured(); err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	for _, session := range d.store.List("") {
		if !sessionGeneratesActivity(session) {
			continue
		}
		entry := protocol.ActivityStatusSession{ID: session.ID, Label: session.Label}
		if run := d.sessionActivityRunRecord(session.ID); run.Err != "" {
			entry.Error = protocol.Ptr(run.Err)
		}
		if stored := d.store.GetSessionActivity(session.ID); stored.Line != "" {
			entry.Activity = protocol.Ptr(stored.Line)
			if !stored.At.IsZero() {
				entry.ActivityAt = protocol.Ptr(string(protocol.NewTimestamp(stored.At)))
			}
		}
		result.Sessions = append(result.Sessions, entry)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, ActivityStatusResult: &result})
}

// handleClearSessionActivity forgets one session's line and its cursor, so the
// next line describes what happens next rather than an already-summarized window.
func (d *Daemon) handleClearSessionActivity(conn net.Conn, msg *protocol.ClearSessionActivityMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	if sessionID == "" {
		d.sendError(conn, "clear session activity: id is required")
		return
	}
	if !d.store.UpdateSessionActivity(sessionID, "", time.Time{}, "") {
		d.sendError(conn, "clear session activity: session not found: "+sessionID)
		return
	}
	d.publishFact(FactSessionActivityChanged, sessionID, nil)
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true})
}
