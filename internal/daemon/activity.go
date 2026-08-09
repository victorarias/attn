package daemon

import (
	"context"
	"errors"
	"fmt"
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

// sessionActivityScanHandler enqueues a refresh for every session whose interval
// has elapsed and whose transcript has actually moved.
//
// Both preconditions are what keep the count far below "sessions × rate". A
// session that has not written since its last line has nothing new to say, and
// its existing line is still true — so blocked and finished sessions cost
// nothing even with the dashboard open.
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
	for _, session := range d.store.List("") {
		if !sessionGeneratesActivity(session) {
			continue
		}
		stored := d.store.GetSessionActivity(session.ID)
		if !stored.At.IsZero() && now.Sub(stored.At) < interval {
			continue
		}
		if !d.transcriptMovedSince(session, stored.At) {
			continue
		}
		d.enqueueSessionActivity(session.ID)
	}
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
	path := strings.TrimSpace(d.resolveTranscriptPathForSession(session, ""))
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.ModTime().After(since)
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
	transcriptPath := strings.TrimSpace(d.resolveTranscriptPathForSession(session, ""))
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

	var carried sessionActivityPayload
	if err := job.DecodePayload(&carried); err != nil {
		return nil, err
	}
	transcriptPath := strings.TrimSpace(carried.Transcript)
	if transcriptPath == "" {
		transcriptPath = strings.TrimSpace(d.resolveTranscriptPathForSession(session, ""))
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
		errors.Is(err, transcript.ErrInvalidCursor):
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
		MaxTurns:     activityMaxTurns,
		MaxBudgetUSD: sessionActivityBudgetUSD,
	})
	if err != nil {
		return nil, fmt.Errorf("session_activity: run agent: %w (%s)", err, result.Diagnostics)
	}

	line, ok := activity.Sanitize(result.Text)
	if !ok {
		// The run produced nothing usable. Keep whatever line is there — a stale
		// true line beats a blank row — but advance the cursor so the next
		// trigger works from fresh events rather than re-reading this window.
		d.logf("session_activity: session=%s produced no usable line (%s)", sessionID, result.Diagnostics)
		return nil, d.advanceSessionActivityCursor(sessionID, stored, window.NextCursor)
	}

	if note := window.Report.String(); note != "" {
		d.logf("session_activity: session=%s window truncated: %s", sessionID, note)
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
