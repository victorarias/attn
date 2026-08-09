package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

// Orphaned-ticket reconciliation (docs/plans/2026-07-01-orphaned-ticket-
// reconciliation.md). Invariant: no non-terminal ticket without a live owning
// session. The classifier annotates; it never moves the column.

const (
	// ticketReconcileCommentPrefix marks every reconciliation comment; the
	// sweep's claim-crash repair keys on it.
	ticketReconcileCommentPrefix = "🩺 Reconciliation"

	defaultTicketReconcileModel = "haiku"
	// Runaway backstop, not the primary control. Measured: ~$0.07 over ~2 haiku
	// turns, so 4 turns / $0.20 leaves comfortable margin.
	defaultTicketReconcileMaxTurns     = 4
	defaultTicketReconcileMaxBudgetUSD = "0.20"
	defaultTicketReconcileTimeout      = 5 * time.Minute

	// Both ends of the echoed classifier output matter: head holds stderr, tail
	// holds the trailing `--output-format json` result event carrying the error.
	ticketReconcileFailureDetailHead = 300
	ticketReconcileFailureDetailTail = 700

	// reconcileKind is the durable-queue job kind; unique key is the ticket id,
	// so every trigger for one ticket coalesces onto a single record.
	reconcileKind = "reconcile"

	// ticketReconcileConcurrency is the handler's per-kind MaxConcurrent: a
	// workspace teardown can kill several delegated sessions at once.
	ticketReconcileConcurrency = 2

	// The grace period must exceed the classifier timeout — repair must never
	// fire on a run still in flight.
	defaultTicketReconcileSweepInterval = 5 * time.Minute
	defaultTicketReconcileGrace         = 15 * time.Minute
	ticketReconcileSweepClaimCap        = 3
)

// ticketReconcileVerdictSchema is enforced via --json-schema. "could not
// determine" is deliberately NOT an assessment: machinery failure is the
// rule-7 failure comment, kept distinguishable from a model verdict.
const ticketReconcileVerdictSchema = `{
	"type": "object",
	"properties": {
		"assessment": {
			"type": "string",
			"enum": ["done", "partial", "interrupted", "blocked_unreported"]
		},
		"confidence": { "type": "string", "enum": ["high", "medium", "low"] },
		"whats_left": {
			"type": "string",
			"description": "One line. Empty string when assessment is done."
		},
		"evidence": {
			"type": "string",
			"description": "Pointer to the supporting turn(s): position/timestamp plus a short quote."
		}
	},
	"required": ["assessment", "confidence", "whats_left", "evidence"],
	"additionalProperties": false
}`

// ticketReconcileInputs is captured synchronously at the seam: the session row
// may be deleted moments later, so the async runner must never re-read it.
type ticketReconcileInputs struct {
	TicketID       string
	Title          string
	Brief          string
	StatusAtClaim  store.TicketStatus // drop rule: a status change during the run drops the verdict
	SessionID      string
	Agent          string
	TranscriptPath string
	CloseContext   string // human framing: how the session ended, for prompt + comment
}

// reconcileInputsFromJob decodes the enqueue-time inputs; a missing or garbled
// payload (including an empty ticket id) is terminal — retrying never fixes it.
func reconcileInputsFromJob(job *jobs.Job) (ticketReconcileInputs, error) {
	var in ticketReconcileInputs
	if err := job.DecodePayload(&in); err != nil {
		return in, fmt.Errorf("decode reconcile inputs: %w", err)
	}
	if strings.TrimSpace(in.TicketID) == "" {
		return in, errors.New("reconcile job carries no inputs")
	}
	return in, nil
}

// ticketReconcileVerdict is the classifier's structured output.
type ticketReconcileVerdict struct {
	Assessment string `json:"assessment"`
	Confidence string `json:"confidence"`
	WhatsLeft  string `json:"whats_left"`
	Evidence   string `json:"evidence"`
}

func (v *ticketReconcileVerdict) valid() bool {
	switch v.Assessment {
	case "done", "partial", "interrupted", "blocked_unreported":
	default:
		return false
	}
	switch v.Confidence {
	case "high", "medium", "low":
	default:
		return false
	}
	return true
}

func ticketReconcileModel() string {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RECONCILE_MODEL")); v != "" {
		return v
	}
	return defaultTicketReconcileModel
}

func ticketReconcileMaxTurns() int {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RECONCILE_MAX_TURNS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultTicketReconcileMaxTurns
}

func ticketReconcileMaxBudgetUSD() string {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RECONCILE_MAX_BUDGET_USD")); v != "" {
		return v
	}
	return defaultTicketReconcileMaxBudgetUSD
}

func ticketReconcileTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RECONCILE_TIMEOUT")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultTicketReconcileTimeout
}

func ticketReconcileSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RECONCILE_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultTicketReconcileSweepInterval
}

func ticketReconcileGrace() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RECONCILE_GRACE")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultTicketReconcileGrace
}

// reconcileTicketsOnSessionEnd is the single ticket seam for a dying session,
// fed the pre-clobber runtime state. Called from both handlePTYExit and
// dropSessionRecord — a user close fires both, and the set-if-unset claim dedupes.
func (d *Daemon) reconcileTicketsOnSessionEnd(sessionID, state string) {
	if d.store == nil {
		return
	}
	tickets, err := d.store.ActiveTicketsForSession(sessionID)
	if err != nil {
		d.logf("ticket reconcile: list active tickets for %s: %v", sessionID, err)
		return
	}
	if len(tickets) == 0 {
		return
	}
	// Read before dropSessionRecord deletes the row.
	session := d.store.Get(sessionID)
	runner := d.jobQueueRef()

	// Only a spontaneous mid-flight death earns the Crashed stamp, and the
	// durable half of that signal lives on the still-present session row.
	intentionalClose := d.sessionCloseWasIntentional(sessionID)

	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		statusAtClaim := ticket.Status
		if isMidFlightCrashState(state) && !intentionalClose {
			if !d.crashTicket(ticket.ID, sessionID, state) {
				continue
			}
			statusAtClaim = store.TicketStatusCrashed
		}
		if runner == nil || runner.Disabled() {
			continue // crash stamp applied above; the sweep backstops the reconcile
		}
		claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, time.Now())
		if err != nil {
			d.logf("ticket reconcile: claim %s: %v", ticket.ID, err)
			continue
		}
		if !claimed {
			continue // the other seam-fire or the sweep owns this death's verdict
		}

		agentID := ticket.LastAgentID
		cwd := ticket.Cwd
		anchor := time.Now() // death-time: the transcript was written moments ago
		if session != nil {
			agentID = string(session.Agent)
			cwd = session.Directory
		}
		in := ticketReconcileInputs{
			TicketID:       ticket.ID,
			Title:          ticket.Title,
			Brief:          ticket.Description,
			StatusAtClaim:  statusAtClaim,
			SessionID:      sessionID,
			Agent:          agentID,
			TranscriptPath: d.resolveReconcileTranscript(agentID, sessionID, cwd, anchor, ticket.Assignee),
			CloseContext:   d.reconcileCloseContext(sessionID, state, ticket.Status),
		}
		if _, err := runner.Enqueue(reconcileKind, jobs.EnqueueOptions{
			UniqueKey: ticket.ID,
			RunNow:    true,
			Payload:   in,
		}); err != nil {
			d.logf("ticket reconcile: enqueue %s: %v", ticket.ID, err)
		}
	}
}

// reconcileCloseContext frames how the session ended, for the prompt and the
// verdict comment; it gates nothing.
func (d *Daemon) reconcileCloseContext(sessionID, state string, column store.TicketStatus) string {
	how := "ended at rest"
	if isMidFlightCrashState(state) {
		how = "was cut off mid-run"
	}
	source := "the agent process exited on its own"
	if d.sessionCloseWasIntentional(sessionID) {
		source = "the session was closed (user close or teardown)"
	}
	if state == "" {
		return fmt.Sprintf("%s while the ticket was %s", source, column)
	}
	return fmt.Sprintf("%s (%s, last runtime state %s) while the ticket was %s", source, how, state, column)
}

// sessionCloseWasIntentional reports an attn/user-initiated close vs a
// spontaneous exit. Either source suffices: the in-memory forced-stop mark, or
// the durable mark on the session row (survives the mark's 30s TTL and a
// daemon restart, so the startup reap still sees the close for what it was).
func (d *Daemon) sessionCloseWasIntentional(sessionID string) bool {
	if d.hasForcedStopMark(sessionID) {
		return true
	}
	return d.store != nil && d.store.SessionCloseIntentional(sessionID)
}

// hasForcedStopMark peeks (never consumes — stop-time classification
// suppression owns the consume) at the mark terminateSession sets before Kill.
func (d *Daemon) hasForcedStopMark(sessionID string) bool {
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	markedAt, ok := d.forcedStop[sessionID]
	return ok && time.Since(markedAt) <= forcedStopSuppressTTL
}

// resolveReconcileTranscript locates the dead session's transcript, preferring
// the ticket's mirrored resume id (the session row's copy dies with the row),
// falling back to the finder's cwd + time-anchor guess. "" means rule 7:
// comment, don't vanish.
func (d *Daemon) resolveReconcileTranscript(agentID, sessionID, cwd string, anchor time.Time, assignee string) string {
	driver := agentdriver.Get(agentID)
	if driver == nil {
		return ""
	}
	tf, ok := agentdriver.GetTranscriptFinder(driver)
	if !ok {
		return ""
	}
	if resumeID := d.store.GetTicketResumeSessionID(assignee); resumeID != "" {
		if path := strings.TrimSpace(tf.FindTranscriptForResume(resumeID)); path != "" {
			return path
		}
	}
	return strings.TrimSpace(tf.FindTranscript(sessionID, cwd, anchor))
}

// reconcileJobHandler runs the headless classifier from enqueue-time inputs.
// Returns nil whenever a conclusion was reached — a classifier error becomes a
// posted failure note. The only retryable error is failing to post the comment.
func (d *Daemon) reconcileJobHandler(ctx context.Context, job *jobs.Job) (any, error) {
	if d.ticketReconcileDone != nil {
		// Test hook: fires on any terminal outcome.
		defer d.ticketReconcileDone(jobSubject(job))
	}
	in, err := reconcileInputsFromJob(job)
	if err != nil {
		// Garbled inputs can never run into health; retire (nil) to avoid a hot loop.
		d.logf("ticket reconcile %s: %v", jobSubject(job), err)
		return nil, nil
	}
	execFn := d.ticketReconcileExec
	if execFn == nil {
		// Test daemons without a wired classifier; production always wires it in New().
		d.logf("ticket reconcile %s: classifier not configured; skipping", in.TicketID)
		return nil, nil
	}

	var verdict *ticketReconcileVerdict
	failReason := ""
	if strings.TrimSpace(in.TranscriptPath) == "" {
		failReason = "could not locate the dead session's transcript"
	} else {
		result, runErr := execFn(ctx, in)
		if result.TotalCostUSD > 0 || result.NumTurns > 0 {
			d.logf("ticket reconcile %s: classifier spent $%.4f over %d turns", in.TicketID, result.TotalCostUSD, result.NumTurns)
		}
		switch {
		case runErr != nil:
			failReason = "classifier run failed: " + runErr.Error()
			if raw := strings.TrimSpace(result.FailureOutput); raw != "" {
				failReason += "\nClassifier output:\n" + truncateMiddleString(raw,
					ticketReconcileFailureDetailHead, ticketReconcileFailureDetailTail)
			}
		case len(result.StructuredOutput) == 0:
			failReason = "classifier returned no structured verdict (cap hit or early exit)"
		default:
			parsed := &ticketReconcileVerdict{}
			if jsonErr := json.Unmarshal(result.StructuredOutput, parsed); jsonErr != nil || !parsed.valid() {
				failReason = "classifier verdict did not match the schema"
			} else {
				verdict = parsed
			}
		}
	}
	if failReason != "" {
		// The comment can be dropped or fail to post; the log is the durable copy.
		d.logf("ticket reconcile %s: %s", in.TicketID, failReason)
	}

	// Drop rule: a status change since the claim means someone acted — the
	// verdict describes a state that no longer exists, drop silently.
	ticket, err := d.store.GetTicket(in.TicketID)
	if err != nil || ticket == nil {
		d.logf("ticket reconcile %s: ticket gone before verdict landed", in.TicketID)
		return nil, nil
	}
	if ticket.Status != in.StatusAtClaim {
		d.logf("ticket reconcile %s: dropped verdict — status moved %s -> %s during classification",
			in.TicketID, in.StatusAtClaim, ticket.Status)
		return nil, nil
	}

	comment := renderTicketReconcileComment(in, verdict, failReason)
	// Annotate-only cross-check: never mutates the verdict, never fails the
	// reconcile — any problem degrades to no annotation.
	if lines := d.reconcileGroundTruth(ctx, verdict, ticket.Cwd); len(lines) > 0 {
		comment += "\n" + strings.Join(lines, "\n")
	}
	if _, err := d.store.AddTicketComment(in.TicketID, store.TicketAuthorAttn, comment, time.Now()); err != nil {
		// The only retryable path: the verdict must land.
		return nil, fmt.Errorf("post reconcile verdict comment: %w", err)
	}
	// attn is an authoring identity, never an observer.
	d.notifyTicketObservers(in.TicketID)
	d.publishTicketFact(FactTicketCommented, in.TicketID)
	return nil, nil
}

// truncateMiddleString keeps the first head and last tail bytes of s, marking
// the cut — both ends of a failed run's output matter.
func truncateMiddleString(s string, head, tail int) string {
	if len(s) <= head+tail {
		return s
	}
	return s[:head] + " …(truncated) " + s[len(s)-tail:]
}

// renderTicketReconcileComment renders the durable verdict or rule-7 failure
// note; everything starts with ticketReconcileCommentPrefix.
func renderTicketReconcileComment(in ticketReconcileInputs, verdict *ticketReconcileVerdict, failReason string) string {
	header := fmt.Sprintf("session %s (%s) — %s.", in.SessionID, in.Agent, in.CloseContext)
	if verdict == nil {
		return fmt.Sprintf("%s could not determine the outcome — needs a human look.\n%s\nReason: %s",
			ticketReconcileCommentPrefix, header, failReason)
	}
	lines := []string{
		fmt.Sprintf("%s verdict — %s", ticketReconcileCommentPrefix, header),
		fmt.Sprintf("Assessment: %s (confidence: %s)", verdict.Assessment, verdict.Confidence),
	}
	if strings.TrimSpace(verdict.WhatsLeft) != "" {
		lines = append(lines, "What's left: "+strings.TrimSpace(verdict.WhatsLeft))
	}
	if strings.TrimSpace(verdict.Evidence) != "" {
		lines = append(lines, "Evidence: "+strings.TrimSpace(verdict.Evidence))
	}
	return strings.Join(lines, "\n")
}

// execTicketReconcileClassifier always spawns Claude Code headless regardless
// of the judged agent's CLI — the one CLI with enforceable turn/dollar caps and
// schema-enforced output.
func (d *Daemon) execTicketReconcileClassifier(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
	slice, err := transcript.ExtractConversationSlice(in.TranscriptPath, transcript.DefaultSliceOptions())
	if err != nil {
		return agentdriver.HeadlessTaskResult{}, fmt.Errorf("read transcript: %w", err)
	}
	if slice.Empty() {
		return agentdriver.HeadlessTaskResult{}, errors.New("transcript had no readable conversation turns")
	}

	driver := agentdriver.Get("claude")
	if driver == nil {
		return agentdriver.HeadlessTaskResult{}, errors.New("claude driver unavailable")
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		return agentdriver.HeadlessTaskResult{}, errors.New("claude driver does not support headless tasks")
	}
	configured := d.store.GetSetting(canonicalExecutableSettingKey("claude"))
	executablePath, err := exec.LookPath(driver.ResolveExecutable(configured))
	if err != nil {
		return agentdriver.HeadlessTaskResult{}, fmt.Errorf("resolve claude executable: %w", err)
	}
	tempDir, err := os.MkdirTemp("", "attn-ticket-reconcile-*")
	if err != nil {
		return agentdriver.HeadlessTaskResult{}, fmt.Errorf("create reconcile scratch dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	request := agentdriver.HeadlessTaskRequest{
		Executable:   executablePath,
		Model:        ticketReconcileModel(),
		Prompt:       buildTicketReconcilePrompt(in, slice),
		WorkDir:      tempDir,
		AllowedTools: nil,
		DisableTools: true,
		MaxTurns:     ticketReconcileMaxTurns(),
		MaxBudgetUSD: ticketReconcileMaxBudgetUSD(),
		OutputSchema: json.RawMessage(ticketReconcileVerdictSchema),
	}
	return provider.RunHeadlessTask(ctx, request)
}

func buildTicketReconcilePrompt(in ticketReconcileInputs, slice transcript.ConversationSlice) string {
	return fmt.Sprintf(`A delegated agent session ended without driving its ticket to a terminal state. Judge the dead session's work against the ticket's brief and render a verdict.

You have no tools; do not attempt to read files -- judge only from the conversation slice below.

Ticket: %s — %s
Ticket brief (the definition of done), as filed:
%s

Ticket column at session end: %s
How the session ended: %s

Stop as soon as you can support a verdict — but judge against the BRIEF above, never the final messages alone: an agent can sound finished while the brief is half-done.

The brief is the starting definition of done, not the final one: the user can re-scope the work mid-session. If the conversation slice shows the user explicitly authorizing, narrowing, or extending the scope, judge against that latest explicit agreement — work the user approved in-session is in scope even where the original brief's wording says otherwise. The slice's first human turn is often the more detailed real instruction (the delegation prompt) — read it alongside the filed brief above; they can differ and both matter.

%s

Report via structured output:
- assessment: done | partial | interrupted | blocked_unreported
- confidence: high | medium | low
- whats_left: one line; empty string when assessment is done
- evidence: which turn(s) support the verdict — position/timestamp plus a short quote`,
		in.TicketID, in.Title, in.Brief, in.StatusAtClaim, in.CloseContext, slice.Render())
}

// runTicketReconcileSweep backstops what the seam cannot cover: pre-feature
// orphans and a daemon death mid-seam (claim landed, enqueue lost). No boot
// pass — startup recovery routes dead-worker sessions through the seam itself.
func (d *Daemon) runTicketReconcileSweep() {
	ticker := time.NewTicker(ticketReconcileSweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.ticketReconcileSweepPass(time.Now())
		}
	}
}

func (d *Daemon) ticketReconcileSweepPass(now time.Time) {
	if d.store == nil {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return // no durable runner to enqueue onto; nothing the sweep can do
	}
	tickets, err := d.store.ListTickets(store.TicketListFilter{})
	if err != nil {
		d.logf("ticket reconcile sweep: list tickets: %v", err)
		return
	}
	claims := 0
	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		assignee := strings.TrimSpace(ticket.Assignee)
		// Only session-bound tickets have an owning session to be orphaned by.
		if assignee == "" || assignee == store.TicketAuthorYou {
			continue
		}
		if ticket.Status.IsTerminal() {
			d.clearOrphanFirstSeen(ticket.ID)
			continue
		}
		if d.reconcileSessionLive(assignee) {
			d.clearOrphanFirstSeen(ticket.ID)
			continue
		}
		// The durable job record is the "already triggered" ledger: a job in ANY
		// state means the queue owns this ticket from here.
		if existing, err := runner.GetByKey(reconcileKind, ticket.ID); err != nil {
			d.logf("ticket reconcile sweep: lookup job for %s: %v", ticket.ID, err)
			continue
		} else if existing != nil {
			d.clearOrphanFirstSeen(ticket.ID)
			continue
		}
		firstSeen := d.orphanFirstSeen(ticket.ID, now)
		if now.Sub(firstSeen) < ticketReconcileGrace() {
			continue
		}
		if claims >= ticketReconcileSweepClaimCap {
			continue // next pass picks it up
		}
		claims++
		d.clearOrphanFirstSeen(ticket.ID)
		if _, err := d.store.ClaimTicketReconciliation(ticket.ID, now); err != nil {
			d.logf("ticket reconcile sweep: claim %s: %v", ticket.ID, err)
		}

		// The session row may or may not still exist; use the freshest source.
		agentID := ticket.LastAgentID
		cwd := ticket.Cwd
		anchor := ticket.CreatedAt // delegation ≈ spawn; widens the codex window safely
		if session := d.store.Get(assignee); session != nil {
			agentID = string(session.Agent)
			cwd = session.Directory
		}
		in := ticketReconcileInputs{
			TicketID:       ticket.ID,
			Title:          ticket.Title,
			Brief:          ticket.Description,
			StatusAtClaim:  ticket.Status,
			SessionID:      assignee,
			Agent:          agentID,
			TranscriptPath: d.resolveReconcileTranscript(agentID, assignee, cwd, anchor, assignee),
			CloseContext: fmt.Sprintf(
				"found orphaned by the periodic sweep (owning session dead) while the ticket was %s", ticket.Status),
		}
		if _, err := runner.Enqueue(reconcileKind, jobs.EnqueueOptions{
			UniqueKey: ticket.ID,
			RunNow:    true,
			Payload:   in,
		}); err != nil {
			d.logf("ticket reconcile sweep: enqueue %s: %v", ticket.ID, err)
		}
	}
}

// reconcileSessionLive: no store row => dead; row + backend runtime => the
// liveness probe decides; row + NO backend runtime => LIVE (conservative:
// CLI-registered and remote sessions never have a daemon PTY, and
// exited-in-place already ran the seam at exit).
func (d *Daemon) reconcileSessionLive(sessionID string) bool {
	if d.store == nil || d.store.Get(sessionID) == nil {
		return false
	}
	if d.ptyBackend == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range d.ptyBackend.SessionIDs(ctx) {
		if id != sessionID {
			continue
		}
		if prober, ok := d.ptyBackend.(ptybackend.SessionLivenessProber); ok {
			alive, err := prober.SessionLikelyAlive(ctx, sessionID)
			if err != nil {
				return true // unknown must never read as dead
			}
			return alive
		}
		return true
	}
	return true
}

func (d *Daemon) orphanFirstSeen(ticketID string, now time.Time) time.Time {
	d.ticketReconcileMu.Lock()
	defer d.ticketReconcileMu.Unlock()
	if d.ticketOrphanFirstSeen == nil {
		d.ticketOrphanFirstSeen = make(map[string]time.Time)
	}
	if first, ok := d.ticketOrphanFirstSeen[ticketID]; ok {
		return first
	}
	d.ticketOrphanFirstSeen[ticketID] = now
	return now
}

func (d *Daemon) clearOrphanFirstSeen(ticketID string) {
	d.ticketReconcileMu.Lock()
	defer d.ticketReconcileMu.Unlock()
	delete(d.ticketOrphanFirstSeen, ticketID)
}
