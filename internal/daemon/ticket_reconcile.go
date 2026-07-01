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
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

// Orphaned-ticket reconciliation (docs/plans/2026-07-01-orphaned-ticket-
// reconciliation.md). Invariant: no non-terminal ticket without a live owning
// session. When an owning session ends, this seam judges every bound
// non-terminal ticket: mid-flight deaths keep the Crashed stamp
// (ticket_crash.go), and — for all deaths — a capped headless `claude -p`
// classifier reads the dead session's transcript against the ticket's brief and
// posts a structured verdict as an attn-authored comment. The classifier never
// moves the column; the chief and the board badge present, Victor decides.

const (
	// ticketReconcileCommentPrefix marks every reconciliation comment (verdict or
	// failure note). The sweep's claim-crash repair keys on it to detect a claim
	// whose verdict never landed.
	ticketReconcileCommentPrefix = "🩺 Reconciliation"

	defaultTicketReconcileModel        = "sonnet"
	defaultTicketReconcileMaxTurns     = 15
	defaultTicketReconcileMaxBudgetUSD = "0.50"
	defaultTicketReconcileTimeout      = 5 * time.Minute

	// ticketReconcileConcurrency bounds simultaneous classifier processes: a
	// workspace teardown can kill several delegated sessions at once, and without
	// a cap that is N parallel sonnet runs.
	ticketReconcileConcurrency = 2

	// Sweep cadence. The grace period must comfortably exceed the classifier
	// timeout (so the repair pass cannot fire on a run still in flight) and cover
	// daemon-restart churn plus a quick close-then-resume; the claim cap turns a
	// first-deploy backlog of historical orphans into a trickle, not a burst.
	defaultTicketReconcileSweepInterval = 5 * time.Minute
	defaultTicketReconcileGrace         = 15 * time.Minute
	ticketReconcileSweepClaimCap        = 3
)

// ticketReconcileVerdictSchema is the JSON Schema enforced via --json-schema.
// "could not determine" is deliberately NOT an assessment: machinery failure is
// the rule-7 failure comment, kept forever distinguishable from a model verdict.
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

// ticketReconcileInputs is everything the classifier run needs, captured
// synchronously at the seam — the session row may be deleted moments later
// (dropSessionRecord), so the async runner must never re-read the session.
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

// --- tunables (constants with env overrides; Victor 2026-07-01: "yes tunable") ---

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

// --- the session-end seam ---

// reconcileTicketsOnSessionEnd is the single ticket seam for a dying session,
// fed the pre-clobber runtime state. Called from handlePTYExit (process death)
// and dropSessionRecord (user close / reap / teardown backstop) — a user close
// fires both, so everything downstream of the claim is double-fire-safe: the
// claim is a set-if-unset and the loser exits.
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
	// The session row is still present at both call sites; capture what the
	// classifier needs NOW (dropSessionRecord deletes the row right after).
	session := d.store.Get(sessionID)

	for _, ticket := range tickets {
		if ticket == nil {
			continue
		}
		statusAtClaim := ticket.Status
		if isMidFlightCrashState(state) {
			// The blunt terminal stamp ships first (unchanged behavior); the
			// classifier then annotates the crashed ticket with what was left
			// (Victor 2026-07-01: crashes get verdicts too).
			if !d.crashTicket(ticket.ID, sessionID, state) {
				continue
			}
			statusAtClaim = store.TicketStatusCrashed
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
		go d.runTicketReconciliation(in)
	}
}

// reconcileCloseContext frames how the session ended, for the prompt and the
// verdict comment. The user response differs by case (crash → "retry",
// closed-while-In-Review → "output ready, just review"), so the framing
// matters even though it gates nothing.
func (d *Daemon) reconcileCloseContext(sessionID, state string, column store.TicketStatus) string {
	how := "ended at rest"
	if isMidFlightCrashState(state) {
		how = "was cut off mid-run"
	}
	source := "the agent process exited on its own"
	if d.hasForcedStopMark(sessionID) {
		source = "the session was closed (user close or teardown)"
	}
	if state == "" {
		return fmt.Sprintf("%s while the ticket was %s", source, column)
	}
	return fmt.Sprintf("%s (%s, last runtime state %s) while the ticket was %s", source, how, state, column)
}

// hasForcedStopMark peeks (never consumes — stop-time classification suppression
// owns the consume) at the forced-stop mark terminateSession sets before Kill,
// distinguishing an attn-initiated close from a spontaneous process death.
func (d *Daemon) hasForcedStopMark(sessionID string) bool {
	d.forcedStopMu.Lock()
	defer d.forcedStopMu.Unlock()
	markedAt, ok := d.forcedStop[sessionID]
	return ok && time.Since(markedAt) <= forcedStopSuppressTTL
}

// resolveReconcileTranscript locates the dead session's transcript via the
// judged agent's driver. Claude transcripts resolve by id — prefer the ticket's
// mirrored resume id (the latest claude-native id after resumes; the session
// row's copy dies with the row) and fall back to the attn session id. Codex has
// no id-based lookup (ResumeSessionIDFromStopTranscriptPath returns ""), so it
// resolves by cwd + time anchor: time.Now() at the death seam (fresh mod-time
// window), the ticket's CreatedAt from the sweep (delegation ≈ spawn; an early
// anchor only widens the window). "" means rule 7: comment, don't vanish.
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

// --- the classifier run ---

func (d *Daemon) ticketReconcileSemaphore() chan struct{} {
	d.ticketReconcileMu.Lock()
	defer d.ticketReconcileMu.Unlock()
	if d.ticketReconcileSem == nil {
		d.ticketReconcileSem = make(chan struct{}, ticketReconcileConcurrency)
	}
	return d.ticketReconcileSem
}

// runTicketReconciliation runs one claimed reconciliation to its durable end: a
// verdict comment, a rule-7 failure comment, or a logged drop (status moved
// during the run — someone acted, the verdict is stale). It runs in its own
// goroutine and must not touch the session (dead) or block the exit path.
func (d *Daemon) runTicketReconciliation(in ticketReconcileInputs) {
	execFn := d.ticketReconcileExec
	if execFn == nil {
		// Test daemons: the claim stands (provenance) but no classifier runs and
		// no comment lands. Production always wires the real exec in New().
		d.logf("ticket reconcile %s: classifier not configured; skipping", in.TicketID)
		return
	}
	sem := d.ticketReconcileSemaphore()
	sem <- struct{}{}
	defer func() { <-sem }()
	defer func() {
		if d.ticketReconcileDone != nil {
			d.ticketReconcileDone(in.TicketID)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), ticketReconcileTimeout())
	defer cancel()

	var verdict *ticketReconcileVerdict
	failReason := ""
	if strings.TrimSpace(in.TranscriptPath) == "" {
		failReason = "could not locate the dead session's transcript"
	} else {
		result, err := execFn(ctx, in)
		if result.TotalCostUSD > 0 || result.NumTurns > 0 {
			d.logf("ticket reconcile %s: classifier spent $%.4f over %d turns", in.TicketID, result.TotalCostUSD, result.NumTurns)
		}
		switch {
		case err != nil:
			failReason = "classifier run failed: " + firstNonEmptyString(result.Diagnostics, err.Error())
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

	// Drop rule: re-check the ticket after the run. A status change since the
	// claim means someone (agent self-report racing the seam, the chief, Victor)
	// acted — the verdict describes a state that no longer exists, drop silently.
	ticket, err := d.store.GetTicket(in.TicketID)
	if err != nil || ticket == nil {
		d.logf("ticket reconcile %s: ticket gone before verdict landed", in.TicketID)
		return
	}
	if ticket.Status != in.StatusAtClaim {
		d.logf("ticket reconcile %s: dropped verdict — status moved %s -> %s during classification",
			in.TicketID, in.StatusAtClaim, ticket.Status)
		return
	}

	comment := renderTicketReconcileComment(in, verdict, failReason)
	if _, err := d.store.AddTicketComment(in.TicketID, store.TicketAuthorAttn, comment, time.Now()); err != nil {
		d.logf("ticket reconcile %s: post verdict comment: %v", in.TicketID, err)
		return
	}
	// The comment notifies participants (the chief is one via the created event);
	// attn itself is an authoring identity, never an observer.
	d.notifyTicketObservers(in.TicketID)
	d.broadcastTicketsUpdated()
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// renderTicketReconcileComment renders the durable verdict (or rule-7 failure
// note). Everything starts with ticketReconcileCommentPrefix — the repair pass
// keys on it.
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

// execTicketReconcileClassifier is the production classifier spawn: always
// Claude Code headless, regardless of which CLI the judged agent ran — a
// transcript is just a file, and Claude Code is the one agent CLI with
// enforceable turn/dollar caps (--max-turns / --max-budget-usd) and
// schema-enforced output (--json-schema). Read-only tools; the caps are a
// runaway backstop, the prompt's early-exit instruction is the primary control.
func (d *Daemon) execTicketReconcileClassifier(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
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
		Prompt:       buildTicketReconcilePrompt(in),
		WorkDir:      tempDir,
		AllowedTools: []string{"Read", "Grep", "Glob"},
		MaxTurns:     ticketReconcileMaxTurns(),
		MaxBudgetUSD: ticketReconcileMaxBudgetUSD(),
		OutputSchema: json.RawMessage(ticketReconcileVerdictSchema),
	}
	return provider.RunHeadlessTask(ctx, request)
}

func buildTicketReconcilePrompt(in ticketReconcileInputs) string {
	return fmt.Sprintf(`A delegated agent session ended without driving its ticket to a terminal state. Judge the dead session's work against the ticket's brief and render a verdict.

Ticket: %s — %s
Ticket brief (the definition of done):
%s

Ticket column at session end: %s
How the session ended: %s
Transcript file (%s agent): %s

Read the transcript with the Read tool. Read backwards from the tail in chunks (check the file size first, then use offset/limit). Stop as soon as you can support a verdict — but judge against the BRIEF above, never the final messages alone: an agent can sound finished while the brief is half-done.

Claude transcripts are JSONL message lines; Codex rollout transcripts are JSONL response items. Either way, extract what the agent actually did and what remains.

Report via structured output:
- assessment: done | partial | interrupted | blocked_unreported
- confidence: high | medium | low
- whats_left: one line; empty string when assessment is done
- evidence: which turn(s) support the verdict — position/timestamp plus a short quote`,
		in.TicketID, in.Title, in.Brief, in.StatusAtClaim, in.CloseContext, in.Agent, in.TranscriptPath)
}

// --- the sweep backstop ---

// runTicketReconcileSweep is the periodic backstop for what the session-end
// seam structurally cannot cover: tickets orphaned before the feature shipped,
// a daemon death mid-seam (row removed, flag unclaimed), and claims whose
// verdict never landed (daemon death mid-run). No initial pass at boot —
// startup recovery's reap routes dead-worker sessions through the seam itself;
// the first tick lands after that churn settles.
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
		if ticket.ReconciledAt != nil {
			d.maybeRepairAbandonedReconcileClaim(ticket, now)
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
		firstSeen := d.orphanFirstSeen(ticket.ID, now)
		if now.Sub(firstSeen) < ticketReconcileGrace() {
			continue
		}
		if claims >= ticketReconcileSweepClaimCap {
			continue // next pass picks it up; repair checks above still ran
		}
		claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, now)
		if err != nil {
			d.logf("ticket reconcile sweep: claim %s: %v", ticket.ID, err)
			continue
		}
		if !claimed {
			continue
		}
		claims++
		d.clearOrphanFirstSeen(ticket.ID)

		// The session row may still exist (exited-in-place) or be long gone; use
		// the freshest source available for each input.
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
		go d.runTicketReconciliation(in)
	}
}

// reconcileSessionLive answers "does this ticket still have a live owning
// session?" from the same authority that feeds the death seam — the store row
// plus the PTY backend:
//   - no store row => dead (every close path deletes the row; that deletion ran
//     the seam, but pre-feature orphans and mid-seam daemon deaths slip through);
//   - row + backend runtime => the backend's liveness probe decides;
//   - row + NO backend runtime => treat as LIVE. This is the conservative arm:
//     CLI-registered and remote sessions never have a daemon PTY, and the
//     exited-in-place case (row idle-clobbered, runtime removed) already ran the
//     seam at exit — skipping it here loses nothing.
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

// maybeRepairAbandonedReconcileClaim closes the claim/comment atomicity gap:
// the claim and the verdict comment are separate writes, so a daemon death
// between them leaves a claimed ticket with no verdict — which would otherwise
// vanish silently, violating rule 7 (reconciliation failure must surface).
// Repair posts the failure note when a claim is old, no reconciliation comment
// ever landed, and NOTHING else happened since the claim (any later activity
// means someone acted — a deliberate verdict drop, a reassign, a human move —
// and a late failure note would be noise).
func (d *Daemon) maybeRepairAbandonedReconcileClaim(ticket *store.Ticket, now time.Time) {
	if ticket.ReconciledAt == nil || now.Sub(*ticket.ReconciledAt) < ticketReconcileGrace() {
		return
	}
	// A settled ticket (done/failed by someone's hand) needs no failure note;
	// crashed is attn's own stamp, so its abandoned claims still deserve repair.
	if ticket.Status.IsTerminal() && ticket.Status != store.TicketStatusCrashed {
		return
	}
	full, err := d.store.GetTicket(ticket.ID)
	if err != nil || full == nil {
		return
	}
	for _, a := range full.Activity {
		if a.Kind == store.TicketActivityComment && a.Author == store.TicketAuthorAttn &&
			strings.HasPrefix(a.Comment, ticketReconcileCommentPrefix) {
			return // a verdict (or failure note) landed; nothing to repair
		}
		if a.CreatedAt.After(*full.ReconciledAt) {
			return // post-claim activity: someone acted, a late note is noise
		}
	}
	comment := fmt.Sprintf("%s could not determine the outcome — needs a human look.\nReason: the reconciliation run was interrupted before a verdict landed (daemon restart?).",
		ticketReconcileCommentPrefix)
	if _, err := d.store.AddTicketComment(ticket.ID, store.TicketAuthorAttn, comment, now); err != nil {
		d.logf("ticket reconcile repair %s: %v", ticket.ID, err)
		return
	}
	d.logf("ticket reconcile repair %s: posted failure note for an abandoned claim", ticket.ID)
	d.notifyTicketObservers(ticket.ID)
	d.broadcastTicketsUpdated()
}
