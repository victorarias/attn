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

// sessionActivityTimeout bounds one generation.
//
// A tripwire, not a schedule: measured end to end the seam answers in ~5s on
// Codex and ~12s on Claude, worst observed ~19s. A run that reaches a minute has
// stopped being a dashboard refresh and become a hung subprocess, and killing it
// costs nothing — the next trigger produces a fresher line anyway.
const sessionActivityTimeout = time.Minute

// sessionActivityBudgetUSD caps one run. Set two orders of magnitude above the
// measured cost ($0.0027 on Codex, $0.011–0.017 on Claude) so only a runaway
// touches it.
const sessionActivityBudgetUSD = "0.50"

// sessionActivityConcurrency bounds how many generations run at once.
//
// Three because that is roughly how many sessions are moving at any moment in a
// working day (the plan's cost model uses the same number, from the same capture
// data), so the common case never queues, and a pathological one — twenty
// sessions all writing — degrades into a queue rather than twenty subprocesses.
const sessionActivityConcurrency = 3

// activityMaxTurns is 2, not 1. One turn is enough to answer, but a headless run
// that exhausts its turn budget exits non-zero even when it already produced the
// text, so a 1-turn cap turns a correct answer into a failed job.
const activityMaxTurns = 2

// The scan tick. It is a cron entry on the same queue as everything else the
// daemon does periodically, so there is one mechanism and one place to look when
// a recurring thing stops happening.
//
// The tick fires faster than any tier interval on purpose: the tick decides
// nothing, it only asks each session whether ITS interval has elapsed. Making
// the tick itself the interval would tie every session to the same phase and
// would have to be rescheduled whenever the tier changed.
const (
	sessionActivityScanKind     = "session_activity_scan"
	sessionActivityScanInterval = 30 * time.Second
	sessionActivityScanTimeout  = 30 * time.Second
)

// sessionActivityRun is what one session's last pass left behind.
//
// Two stamps because the two gates ask different questions. ObservedAt answers
// "have we looked since the transcript last moved" and is written by every pass,
// including the ones that spend nothing — a cold-start seed, an empty window —
// so a quiet session stops being re-read. SpentAt answers "may we spend again"
// and is written only when a run actually calls the agent, so a first line is
// not delayed by a seed while a failing agent is still held to the interval.
type sessionActivityRun struct {
	ObservedAt time.Time
	SpentAt    time.Time
	// Err is why the last run that spent produced nothing, kept so the feature
	// can say what is wrong instead of going quiet. Cleared by a run that works.
	Err string
	// Transcript is the last resolved transcript path, and ResumeID the resume id
	// it was resolved under. See sessionActivityTranscript.
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
// has elapsed and whose transcript has actually moved.
//
// Both preconditions are what keep the count far below "sessions × rate". A
// session that has not written since its last line has nothing new to say, and
// its existing line is still true — so blocked and finished sessions cost
// nothing even with the dashboard open.
//
// Both are measured against the last PASS rather than the last stored line.
// Measuring against the line makes every outcome except success invisible: a
// generation that fails, times out, or answers with nothing leaves the stamp
// untouched, and the scan re-runs it every tick — at the 30s tick rate, not the
// tier's, and with the queue resetting the job's attempt count each time so
// backoff and the dead-job notification never arrive.
func (d *Daemon) sessionActivityScanHandler(context.Context, *jobs.Job) (any, error) {
	if !d.activityEnabled() {
		return nil, nil
	}
	tier := d.PresenceTier()
	interval := d.activityInterval(tier)
	if interval <= 0 {
		return nil, nil
	}
	// A misconfiguration is worth one log line per tick rather than a silent
	// nothing: the feature is on, the user expects lines, and none are coming.
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

// transcriptMovedSince reports whether a session has written anything since its
// line was generated.
//
// It asks the filesystem rather than the reader: a stat is O(1) where opening,
// seeking and decoding is not, and this runs over every session on every tick.
// The reader still has the last word — a window that comes back empty skips the
// run — so a stat that says "maybe" costs one cheap read, and one that says "no"
// costs nothing at all.
//
// A session that has never had a line generated always counts as moved: that is
// the cold start, and the executor turns it into a cursor seed.
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

// sessionActivityTranscript resolves a session's transcript path and remembers
// it across ticks.
//
// The scan asks every session where its transcript is on every tick, and on
// Codex answering means walking ~/.codex/sessions and opening rollouts until one
// carries the right id — 235–489ms with a working day of rollouts on disk, per
// Codex session, every 30 seconds, whether or not anything is generated. The
// answer barely ever changes: a session writes to one file for as long as it
// runs.
//
// Two things do change it, and both are checked rather than assumed. The file
// can go away (a pruned rollout, a compaction that rewrites elsewhere), which
// the stat catches. And the session can resume under a new id, which starts a
// new file while leaving the old one on disk to stat perfectly well — so the
// resume id the path was found under is remembered beside it, and a different
// one re-resolves.
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

// sessionActivityPayload carries the transcript path resolved at enqueue time.
// The generator runs debounced, and resolving the path here means a run does not
// depend on the finder still guessing the same file minutes later.
type sessionActivityPayload struct {
	Transcript string `json:"transcript,omitempty"`
}

// enqueueSessionActivity queues a refresh for one session, coalesced by session
// id so a burst of transcript movement collapses into a single run.
//
// Everything that decides whether to generate lives here rather than in the
// executor, so a suppressed refresh costs a map lookup instead of a queued job:
// the feature has to be off, the agent unconfigured, or the tier away, and any
// of those means no work at all.
func (d *Daemon) enqueueSessionActivity(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !d.activityEnabled() {
		return
	}
	// away is a hard stop, and it is checked before anything else because it is
	// the case that has to cost nothing.
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

// sessionGeneratesActivity reports whether a session is the kind that has
// something to say. A shell has no transcript and no agent, and a satellite is
// a split of one — neither is an agent doing work, so neither gets a line.
func sessionGeneratesActivity(session *protocol.Session) bool {
	if session == nil {
		return false
	}
	if session.ParentSessionID != nil && strings.TrimSpace(*session.ParentSessionID) != "" {
		return false
	}
	// Remote sessions are deferred: their transcript lives on the remote daemon
	// and the presence signal originates at the hub. See the plan's
	// "Deferred: remote sessions".
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

// sessionActivityHandler generates one session's line.
//
// The shape of a run is: read the delta since the stored cursor, render it with
// the session's state and its previous line, ask a tool-less agent for one
// sentence, and store the answer with the cursor it was generated through.
//
// Two cursor outcomes are normal rather than exceptional and both re-seed at
// head instead of failing: a session with no stored cursor (cold start — a full
// read of a 32MB transcript to produce a first line is not worth 1.4s and would
// summarize the session's history rather than its present), and a cursor the
// transcript no longer matches (Claude compaction rewrites the file).
func (d *Daemon) sessionActivityHandler(ctx context.Context, job *jobs.Job) (any, error) {
	// A run queued before the toggle went off must not spend money after it. The
	// same applies to the tier: the user can walk away between enqueue and claim.
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
	// Every pass past this point counts as having looked, whatever it goes on to
	// do — seed a cursor, find an empty window, spend on the agent, or fail. The
	// scan measures transcript movement from here.
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
		// Cold start, checked before the read rather than after it: reading from
		// byte 0 succeeds, which is exactly the problem — it would spend a full
		// scan of a transcript that reaches tens of megabytes to summarize the
		// session's whole history as if it were the present.
		return nil, d.reseedSessionActivity(sessionID, transcriptPath)
	}
	window, err := activity.Read(transcriptPath, string(session.Agent), stored.Cursor)
	switch {
	case err == nil:
	case errors.Is(err, transcript.ErrCursorMismatch) ||
		errors.Is(err, transcript.ErrCursorPastEnd) ||
		errors.Is(err, transcript.ErrInvalidCursor) ||
		errors.Is(err, activity.ErrDeltaTooLarge):
		// Re-seed and skip this generation. The next real movement produces a
		// line against a cursor that validates, which is cheaper and more honest
		// than failing this job repeatedly against one that never will.
		return nil, d.reseedSessionActivity(sessionID, transcriptPath)
	default:
		return nil, fmt.Errorf("session_activity: read %s: %w", transcriptPath, err)
	}

	if window.Empty() {
		// Nothing was appended: the existing line is still true. Advance the
		// cursor anyway so the next trigger measures movement from here.
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
	// Stamped before the call, not after: a run killed by the timeout has spent
	// the same as one that answered, and stamping on the way out would let a
	// generation that always hangs reschedule itself on every tick.
	d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
		record.SpentAt = time.Now()
	})
	result, err := run(ctx, provider, agentdriver.HeadlessTaskRequest{
		Executable:      executablePath,
		Model:           config.Model,
		ReasoningEffort: config.Effort,
		Prompt:          prompt.User,
		// The invariant half replaces the CLI's own system prompt, which is
		// written for interactive coding and is most of what a run this small
		// pays for. Measured: the billed prefix drops from 46,745 tokens to
		// 33,955, and the volatile sections that were invalidating the cache
		// suffix every run go with it.
		SystemPrompt: prompt.System,
		WorkDir:      workDir,
		// Tool-less and bounded. An activity line has no business touching the
		// filesystem, and a run on a per-refresh schedule needs a ceiling.
		//
		// No OutputSchema on purpose: the answer IS the final text. Asking for it
		// as a schema-validated object bought a second reasoning turn restating
		// what the first already said, and Codex's tool-free path has no schema
		// support at all — dropping it makes both agents answer the same way.
		DisableTools: true,
		// MaxTurns and MaxBudgetUSD are Claude-only — `codex exec` has no flag to
		// translate them into. What actually bounds BOTH agents is the pair above
		// and below: DisableTools leaves the run nothing to loop over, and ctx
		// carries sessionActivityTimeout, so the worst Codex case is one minute of
		// a single tool-less completion. The two caps are the extra belt Claude
		// happens to offer, not the only thing standing between us and a runaway.
		MaxTurns:     activityMaxTurns,
		MaxBudgetUSD: sessionActivityBudgetUSD,
	})
	if err != nil {
		// Kept, not just logged. A generation that fails every time is now rate
		// limited like a successful one, which is right but silent — and a silent
		// feature the user paid to turn on is indistinguishable from a broken
		// one. `attn activity` reads this back.
		d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
			record.Err = err.Error()
		})
		return nil, fmt.Errorf("session_activity: run agent: %w (%s)", err, result.Diagnostics)
	}

	line, ok := activity.Sanitize(result.Text)
	if !ok {
		// The run produced nothing usable. Keep whatever line is there — a stale
		// true line beats a blank row — but advance the cursor so the next
		// trigger works from fresh events rather than re-reading this window.
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
	// Re-checked after the agent call, not only before it. The run takes seconds,
	// and a user who switched the feature off during them has already had every
	// line cleared — writing this one now would strand it on home with nothing
	// left to clear it.
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

// reseedSessionActivity moves the cursor to the transcript's head, keeping the
// line that is already there. Used for a cold start and for a cursor the
// transcript no longer matches.
func (d *Daemon) reseedSessionActivity(sessionID, transcriptPath string) error {
	head, err := activity.SeedCursor(transcriptPath)
	if err != nil {
		return fmt.Errorf("session_activity: seed cursor for %s: %w", transcriptPath, err)
	}
	d.store.SetSessionActivityCursor(sessionID, head)
	return nil
}

// advanceSessionActivityCursor moves the cursor forward without changing the
// line. Kept separate from a re-seed because they mean different things in a
// log, and because passing an empty cursor here would silently clear the line.
func (d *Daemon) advanceSessionActivityCursor(sessionID string, stored store.SessionActivity, next string) error {
	if next == "" || next == stored.Cursor {
		return nil
	}
	d.store.SetSessionActivityCursor(sessionID, next)
	return nil
}

// clearAllSessionActivity forgets every line, one fact per session so each row
// re-renders. Used when the feature is switched off: the lines are the feature's
// only output, and leaving them behind would leave home asserting something attn
// has stopped keeping true.
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
// right now. It exists because the feature's entire output otherwise lives on
// the dashboard: from a terminal, or on a headless daemon, there is no other way
// to see whether lines are being generated or why they are not.
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

// handleClearSessionActivity forgets one session's line, and the cursor with it
// so the next line describes what happens next rather than a window that was
// already summarized.
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
