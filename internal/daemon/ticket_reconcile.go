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
	"github.com/victorarias/attn/internal/tasks"
	"github.com/victorarias/attn/internal/transcript"
)

// Orphaned-ticket reconciliation (docs/plans/2026-07-01-orphaned-ticket-
// reconciliation.md). Invariant: no non-terminal ticket without a live owning
// session. When an owning session ends, this seam judges every bound
// non-terminal ticket: mid-flight deaths keep the Crashed stamp
// (ticket_crash.go), and — for all deaths — the dead session's transcript is
// deterministically pre-sliced (internal/transcript) and inlined into a single
// tool-less, capped headless `claude -p` completion that judges it against the
// ticket's brief and posts a structured verdict as an attn-authored comment.
// The classifier never moves the column; the chief and the board badge
// present, Victor decides.

const (
	// ticketReconcileCommentPrefix marks every reconciliation comment (verdict or
	// failure note). The sweep's claim-crash repair keys on it to detect a claim
	// whose verdict never landed.
	ticketReconcileCommentPrefix = "🩺 Reconciliation"

	defaultTicketReconcileModel = "haiku"
	// The transcript is now deterministically pre-sliced and inlined, so the
	// classifier is a single tool-less completion — it cannot loop on tools and
	// cannot balloon on transcript size. These caps are a pure runaway backstop
	// with real headroom, NOT the primary control: an observed haiku run costs
	// ~$0.07 over ~2 model turns, so 4 turns / $0.20 leaves comfortable margin
	// while still catching a true runaway well below the old $0.50 a big
	// transcript used to blow.
	defaultTicketReconcileMaxTurns     = 4
	defaultTicketReconcileMaxBudgetUSD = "0.20"
	defaultTicketReconcileTimeout      = 5 * time.Minute

	// ticketReconcileFailureDetail{Head,Tail} bound the raw classifier output
	// echoed into a rule-7 failure comment, keeping both ends of the capture:
	// the head holds FailureOutput's stderr section (fatal CLI errors), the
	// tail holds the end of stdout — a failed `--output-format json` run puts
	// the human-readable error in the trailing result event.
	ticketReconcileFailureDetailHead = 300
	ticketReconcileFailureDetailTail = 700

	// reconcileKind is the durable-runner task kind for orphaned-ticket
	// reconciliation. Subject is the ticket id, so TaskID("reconcile", ticketID)
	// coalesces every trigger for one ticket onto a single record.
	reconcileKind = "reconcile"

	// reconcileInputsMetaKey stashes the JSON-encoded ticketReconcileInputs on the
	// task record (Task.Meta). The classifier inputs are captured at ENQUEUE time
	// because the owning session row is deleted moments after the death seam; the
	// executor, which may run much later, reads them back from here and never
	// re-reads the (gone) session.
	reconcileInputsMetaKey = "reconcile_inputs"

	// ticketReconcileConcurrency bounds simultaneous classifier processes: a
	// workspace teardown can kill several delegated sessions at once, and without
	// a cap that is N parallel sonnet runs. It is the reconcile executor's per-kind
	// MaxConcurrent in the durable runner (which now owns the cap the bespoke
	// semaphore used to enforce).
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

// reconcileInputsToMeta encodes the captured inputs into a Task.Meta map for the
// durable record. ticketReconcileInputs is all strings, so json.Marshal cannot
// fail; a defensive nil return degrades to "no inputs" (the executor then logs
// and retires the task rather than panicking).
func reconcileInputsToMeta(in ticketReconcileInputs) map[string]string {
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	return map[string]string{reconcileInputsMetaKey: string(data)}
}

// reconcileInputsFromMeta decodes the inputs the enqueue stashed. A missing or
// undecodable blob is an error the executor treats as terminal (the task cannot
// be run, and retrying would never fix a garbled record).
func reconcileInputsFromMeta(meta map[string]string) (ticketReconcileInputs, error) {
	var in ticketReconcileInputs
	raw, ok := meta[reconcileInputsMetaKey]
	if !ok {
		return in, fmt.Errorf("reconcile task missing %q meta", reconcileInputsMetaKey)
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return in, fmt.Errorf("decode reconcile inputs: %w", err)
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
// fires both, so the claim (a set-if-unset) dedupes: the first fire claims and
// enqueues a durable reconcile task with the freshest inputs (the session row is
// still present), and the loser exits. The claim also lights the board's orphan
// badge immediately (reconciled_at). Enqueue replaces the old inline goroutine so
// a daemon restart between here and the classifier run no longer loses the work —
// the task survives in the profile DB, and the runner's cap-2 dispatch (not a
// bespoke semaphore) bounds concurrent classifier processes.
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
	// The reconcile ENQUEUE needs a durable runner, but the crash stamp does not —
	// crashTicket must run regardless so a mid-flight death is always marked. When
	// the runner is unavailable (not expected in production, where the store is
	// always present), the periodic sweep rediscovers the still-orphaned ticket and
	// enqueues once the runner is up.
	runner := d.compactRunnerRef()

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
		if _, err := runner.Enqueue(reconcileKind, ticket.ID, tasks.EnqueueOptions{
			ZeroDebounce: true,
			Meta:         reconcileInputsToMeta(in),
		}); err != nil {
			d.logf("ticket reconcile: enqueue %s: %v", ticket.ID, err)
		}
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

// reconcileTaskExecutor is the durable-runner ExecutorFunc for the reconcile
// kind. It reads the classifier inputs captured at enqueue time (the session row
// is long gone by run time), runs the headless classifier under the runner-owned
// timeout ctx, and drives the reconciliation to its durable end: a verdict
// comment, a rule-7 failure comment, or a logged drop (status moved during the
// run — someone acted, the verdict is stale). The board's orphan badge
// (reconciled_at) was already stamped at enqueue time by the claim.
//
// Return contract: nil in every case where the reconciliation reached a
// conclusion (verdict posted, failure note posted, dropped, or inputs
// unrecoverable) — a reconcile is one-shot, exactly as the inline version was,
// so a classifier error becomes a posted failure note, not a runner retry. The
// ONLY retryable error is a failure to POST the comment (a transient store
// error): the verdict must eventually land, so the runner backs off and re-runs.
func (d *Daemon) reconcileTaskExecutor(ctx context.Context, task *tasks.Task) error {
	if d.ticketReconcileDone != nil {
		// Test observation hook: fire once the run reaches any terminal outcome.
		defer d.ticketReconcileDone(task.Subject)
	}
	in, err := reconcileInputsFromMeta(task.Meta)
	if err != nil {
		// A record with no/garbled inputs can never be run into health; log and
		// retire it (nil) so it doesn't hot-loop the dispatch queue.
		d.logf("ticket reconcile %s: %v", task.Subject, err)
		return nil
	}
	execFn := d.ticketReconcileExec
	if execFn == nil {
		// Test daemons without a wired classifier: the claim stands (provenance),
		// but no classifier runs and no comment lands. Production always wires the
		// real exec in New().
		d.logf("ticket reconcile %s: classifier not configured; skipping", in.TicketID)
		return nil
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
			// The comment carries the raw cause, not just the keyword bucket:
			// err summarizes (bucket + exit status), FailureOutput is the child's
			// actual output tail — without it a failure is undiagnosable (the
			// 2026-07-02 first fire surfaced only "keeper tools failed").
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
		// The comment can be dropped (status moved) or fail to post; the log is
		// the durable copy of what actually went wrong.
		d.logf("ticket reconcile %s: %s", in.TicketID, failReason)
	}

	// Drop rule: re-check the ticket after the run. A status change since the
	// claim means someone (agent self-report racing the seam, the chief, Victor)
	// acted — the verdict describes a state that no longer exists, drop silently.
	ticket, err := d.store.GetTicket(in.TicketID)
	if err != nil || ticket == nil {
		d.logf("ticket reconcile %s: ticket gone before verdict landed", in.TicketID)
		return nil
	}
	if ticket.Status != in.StatusAtClaim {
		d.logf("ticket reconcile %s: dropped verdict — status moved %s -> %s during classification",
			in.TicketID, in.StatusAtClaim, ticket.Status)
		return nil
	}

	comment := renderTicketReconcileComment(in, verdict, failReason)
	// Annotate-only ground-truth cross-check (ticket_reconcile_groundtruth.go):
	// never mutates the verdict, never fails the reconcile — any problem (no
	// cwd, no origin, no GitHub client, lookup errors) degrades to no
	// annotation.
	if lines := d.reconcileGroundTruth(ctx, verdict, ticket.Cwd); len(lines) > 0 {
		comment += "\n" + strings.Join(lines, "\n")
	}
	if _, err := d.store.AddTicketComment(in.TicketID, store.TicketAuthorAttn, comment, time.Now()); err != nil {
		// The only retryable path: the verdict must land, so ask the runner to back
		// off and re-run rather than silently dropping the reconciliation.
		return fmt.Errorf("post reconcile verdict comment: %w", err)
	}
	// The comment notifies participants (the chief is one via the created event);
	// attn itself is an authoring identity, never an observer.
	d.notifyTicketObservers(in.TicketID)
	d.broadcastTicketsUpdated()
	return nil
}

// truncateMiddleString keeps the first head and last tail bytes of s, marking
// the cut — both ends of a failed run's output matter (stderr leads, the fatal
// result event trails).
func truncateMiddleString(s string, head, tail int) string {
	if len(s) <= head+tail {
		return s
	}
	return s[:head] + " …(truncated) " + s[len(s)-tail:]
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
// schema-enforced output (--json-schema). The transcript is deterministically
// pre-sliced (internal/transcript) and inlined into the prompt, so the run
// needs no tools at all; the caps remain a runaway backstop, not the primary
// control.
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

// --- the sweep backstop ---

// runTicketReconcileSweep is the periodic backstop for what the session-end
// seam structurally cannot cover: tickets orphaned before the feature shipped,
// and a daemon death mid-seam (the session-end claim landed but the enqueue did
// not, so no reconcile task exists). No initial pass at boot — startup recovery's
// reap routes dead-worker sessions through the seam itself; the first tick lands
// after that churn settles. A daemon death mid-RUN no longer needs the sweep: the
// durable task survives and the runner's orphan-recovery re-runs it.
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
	runner := d.compactRunnerRef()
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
		// The durable task record is the "already triggered" ledger: if one exists
		// for this ticket (in any state), the session-end seam or a prior sweep
		// already enqueued it and the runner owns it from here — including
		// re-running one whose daemon died mid-flight. Only a ticket with NO task is
		// a genuine sweep discovery (pre-feature orphan, or a seam whose claim
		// landed but whose enqueue was lost to a crash — the abandoned-claim case
		// the old maybeRepair pass covered, now recovered by enqueuing for real).
		if existing, err := runner.Get(tasks.TaskID(reconcileKind, ticket.ID)); err != nil {
			d.logf("ticket reconcile sweep: lookup task for %s: %v", ticket.ID, err)
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
		// Light the board's orphan badge now (set-if-unset; a no-op if an abandoned
		// session-end claim already set it), then enqueue the durable task.
		if _, err := d.store.ClaimTicketReconciliation(ticket.ID, now); err != nil {
			d.logf("ticket reconcile sweep: claim %s: %v", ticket.ID, err)
		}

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
		if _, err := runner.Enqueue(reconcileKind, ticket.ID, tasks.EnqueueOptions{
			ZeroDebounce: true,
			Meta:         reconcileInputsToMeta(in),
		}); err != nil {
			d.logf("ticket reconcile sweep: enqueue %s: %v", ticket.ID, err)
		}
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
