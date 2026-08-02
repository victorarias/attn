package daemon

import (
	"errors"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/protocol"
)

// Deciding what a settled turn means is a sequence of rules interleaved with two
// slow, fallible steps: reading the transcript off disk and asking an LLM. The
// rules are here as pure functions so they can be read and tested as rules,
// beside classifySessionState — the shell that performs the IO between them and
// files the verdict.
//
// Each function answers with a classifyDecision: apply this state, or take the
// next IO step, or leave the state alone.

type classifyAction int

const (
	// classifyApply: the state is decided; apply it.
	classifyApply classifyAction = iota
	// classifyReadTranscript: the transcript must be read to decide.
	classifyReadTranscript
	// classifyRunClassifier: the last assistant message must go to the classifier.
	classifyRunClassifier
	// classifySkip: nothing to decide, and nothing to write.
	classifySkip
)

type classifyDecision struct {
	action classifyAction
	// state is meaningful only for classifyApply.
	state string
	// reason is the diagnostic label logged with the decision. The names match the
	// ones the previous inline log lines used ("transcript_parse_error",
	// "classifier_error", "classifier_unknown_response"), so a log search for a
	// past incident still finds them.
	reason string
}

// stopClassification is what handleStop knew about the stop being judged.
//
// yielded marks a stop whose payload reported background work still running, so
// the turn can resume on its own. A yielded stop is still judged — its last
// message is the only thing separating "waiting on my build" from "done, but a
// process I started is still running" — but with two differences: the judge sees
// the yield (the harness-facts line the PARKED verdict keys on), and every
// no-answer outcome files nothing instead of settling, because an unjudgeable
// message must not put a turn that may be about to resume into the user's queue.
type stopClassification struct {
	yielded                bool
	runningBackgroundTasks int
}

// classifyPreTranscript decides from what the daemon already holds, before paying
// for any IO.
//
// pendingTodos outranks everything on a terminal stop: a turn that stopped with
// unfinished todos is waiting on the user. On a yield it decides nothing — an
// agent sitting out its own build mid-plan has open todos precisely because it is
// not finished. transcriptEnabled / classifierEnabled are the per-agent
// capability gates; an agent with either disabled settles idle rather than being
// left in whatever state it was — except on a yield, where no judgment means no
// filing and the resolver's fallback ladder decides.
func classifyPreTranscript(pendingTodos int, transcriptEnabled, classifierEnabled bool, stop stopClassification) classifyDecision {
	switch {
	case stop.yielded && (!transcriptEnabled || !classifierEnabled):
		return classifyDecision{action: classifySkip, reason: "yield_unjudgeable"}
	case !stop.yielded && pendingTodos > 0:
		return classifyDecision{action: classifyApply, state: protocol.StateWaitingInput, reason: "pending_todos"}
	case !transcriptEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "transcript_disabled"}
	case !classifierEnabled:
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "classifier_disabled"}
	default:
		return classifyDecision{action: classifyReadTranscript}
	}
}

// classifyPostTranscript decides from the transcript read.
//
// ErrNoNewAssistantTurn means the turn we would classify has already been
// classified, so there is nothing to say — importantly not "unknown", which would
// overwrite a good state with a bad one. Any other read error is genuinely
// unknown. An empty last message means the agent said nothing, which settles idle
// without paying for a classifier call. A yield files nothing in every non-answer
// case, empty message included: silence is not evidence the turn is over when the
// payload says it will resume.
func classifyPostTranscript(lastMessage string, err error, stop stopClassification) classifyDecision {
	if err != nil {
		if errors.Is(err, agentdriver.ErrNoNewAssistantTurn) {
			return classifyDecision{action: classifySkip, reason: "no_new_assistant_turn"}
		}
		if stop.yielded {
			return classifyDecision{action: classifySkip, reason: "yield_transcript_parse_error"}
		}
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "transcript_parse_error"}
	}
	if strings.TrimSpace(lastMessage) == "" {
		if stop.yielded {
			return classifyDecision{action: classifySkip, reason: "yield_empty_message"}
		}
		return classifyDecision{action: classifyApply, state: protocol.StateIdle, reason: "empty_last_message"}
	}
	return classifyDecision{action: classifyRunClassifier}
}

// classifyVerdict maps the classifier's answer to a state. A failed call and an
// unknown answer both land on unknown, but they are separate reasons: one is our
// plumbing failing, the other is the classifier declining to decide. (Neither
// files any evidence — there is no unknown claim — so on a yield the resolver's
// fallback ladder still decides.)
//
// A parked verdict outside a yield is the judge misapplying a rule whose
// precondition — the harness-facts line — was never in its input. Filed as
// nothing rather than remapped: the session settles on its own fallback, and the
// trace says what the judge actually answered.
func classifyVerdict(state string, err error, stop stopClassification) classifyDecision {
	if err != nil {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_error"}
	}
	if state == protocol.StateUnknown {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_unknown_response"}
	}
	if state == classifier.VerdictParked && !stop.yielded {
		return classifyDecision{action: classifyApply, state: protocol.StateUnknown, reason: "classifier_parked_without_yield"}
	}
	return classifyDecision{action: classifyApply, state: state, reason: "classifier"}
}

// classifySessionState judges a terminal stop. See classifyStop.
func (d *Daemon) classifySessionState(sessionID, transcriptPath string) {
	d.classifyStop(sessionID, transcriptPath, stopClassification{})
}

// classifyStop decides what a stopped turn means and files the result.
// It is the IO shell around the rules in classify_decision.go: it gathers inputs,
// performs the transcript read and the classifier call between rules, and owns the
// single store write. The rules themselves make no decision about when to pay for
// IO — they say what is still needed.
func (d *Daemon) classifyStop(sessionID, transcriptPath string, stop stopClassification) {
	// Capture the timestamp BEFORE any classification work: applyState rejects a
	// classifierObservation older than the state it would overwrite, so a slow
	// classifier cannot clobber a newer live signal.
	classificationStartTime := time.Now()
	d.logf("classifySessionState: starting for session=%s, transcript=%s", sessionID, transcriptPath)

	session := d.store.Get(sessionID)
	if session == nil {
		d.logf("classifySessionState: session %s not found, aborting", sessionID)
		return
	}

	// Tell the resolver a verdict is coming, so its tick holds the pre-settle
	// state instead of publishing idle and being corrected seconds later. The
	// deferred clear covers every exit, including the early returns below: a
	// classification that ends without a verdict is precisely when the session
	// must be free to settle on its own.
	d.recordClassifierStarted(sessionID, classificationStartTime)
	defer d.recordClassifierFinished(sessionID)

	apply := func(decision classifyDecision) {
		if decision.action != classifyApply {
			d.logf("classifySessionState: session=%s no state applied reason=%s", sessionID, decision.reason)
			d.traceStateSkip(sessionID, stateSourceClassifier, decision.reason)
			return
		}
		d.logf("classifySessionState: session=%s state=%s reason=%s", sessionID, decision.state, decision.reason)
		// Filed, not applied. The verdict is one witness to how a turn ended and
		// the weakest-timed of them: it describes the transcript as of
		// classificationStartTime and lands seconds later, which is why it used to
		// need a timestamp guard to stop it overwriting a newer state. As evidence
		// it gets a stronger version of the same guard for free — the resolver drops
		// a verdict the agent has since gone busy past, whatever the clock says.
		d.recordClassifierEvidence(sessionID, decision.state, classificationStartTime)
		d.traceStateEvidence(
			sessionID,
			stateOrigin{
				source:     stateSourceClassifier,
				detail:     decision.reason,
				observedAt: classificationStartTime,
			},
			decision.state,
		)
	}

	// Capability gates: agents can independently disable transcript parsing and
	// classification.
	transcriptEnabled := true
	classifierEnabled := true
	if driver := agentdriver.Get(string(session.Agent)); driver != nil {
		caps := agentdriver.EffectiveCapabilities(driver)
		transcriptEnabled = caps.HasTranscript
		classifierEnabled = caps.HasClassifier
	}

	// Todos are stored as "[✓] task" (completed), "[→] task" (in_progress),
	// "[ ] task" (pending).
	pendingTodos := 0
	for _, todo := range session.Todos {
		if !strings.HasPrefix(todo, "[✓]") {
			pendingTodos++
		}
	}
	d.logf("classifySessionState: session %s has %d total todos, %d pending", sessionID, len(session.Todos), pendingTodos)

	decision := classifyPreTranscript(pendingTodos, transcriptEnabled, classifierEnabled, stop)
	if decision.action != classifyReadTranscript {
		apply(decision)
		return
	}

	resolvedTranscriptPath := d.resolveTranscriptPathForSession(session, transcriptPath)
	if resolvedTranscriptPath != transcriptPath {
		d.logf(
			"classifySessionState: session %s resolved transcript path %q -> %q",
			sessionID,
			transcriptPath,
			resolvedTranscriptPath,
		)
	}

	d.logf("classifySessionState: parsing transcript for session %s", sessionID)
	extract := d.extractLastAssistantMessage
	if d.classificationTranscriptExtractor != nil {
		extract = d.classificationTranscriptExtractor
	}
	lastMessage, assistantTurnID, err := extract(session, resolvedTranscriptPath, 500, classificationStartTime)
	if err != nil {
		d.logf("classifySessionState: transcript read failed for %s: %v (transcript=%s)", sessionID, err, resolvedTranscriptPath)
	}
	if strings.TrimSpace(assistantTurnID) != "" && err == nil {
		defer d.clearClassifyingTurn(sessionID)
	}

	decision = classifyPostTranscript(lastMessage, err, stop)
	if decision.action != classifyRunClassifier {
		apply(decision)
		return
	}
	lastMessage = strings.TrimSpace(lastMessage)

	logMsg := lastMessage
	if len(logMsg) > 100 {
		logMsg = logMsg[:100] + "..."
	}
	d.logf("classifySessionState: last message for session %s: %s", sessionID, logMsg)

	// On a yield the judge must see the yield: the harness-facts line is the
	// precondition of the PARKED verdict, and without it the same message reads
	// as a plain ending.
	classifierInput := lastMessage
	if stop.yielded {
		classifierInput = classifier.ComposeYieldInput(lastMessage, stop.runningBackgroundTasks)
	}

	// Can be slow — 30+ seconds.
	d.logf("classifySessionState: calling classifier for session %s", sessionID)
	state, err := d.runClassifier(session, classifierInput, 30*time.Second)
	if err != nil {
		d.logf("classifySessionState: classifier error for %s: %v", sessionID, err)
	}
	decision = classifyVerdict(state, err, stop)
	if strings.TrimSpace(assistantTurnID) != "" {
		d.setClassifiedTurnID(sessionID, assistantTurnID)
	}
	apply(decision)
}

func (d *Daemon) runClassifier(session *protocol.Session, text string, timeout time.Duration) (string, error) {
	if d.classifier != nil {
		return d.classifier.Classify(text, timeout)
	}
	if session != nil {
		driver := agentdriver.Get(string(session.Agent))
		if state, err, ok := agentdriver.ClassifyWithDriver(
			driver,
			text,
			d.store.GetSetting(executableSettingKey(string(session.Agent))),
			session.Directory,
			timeout,
		); ok {
			return state, err
		}
	}
	// Fallback for a session whose driver has no classifier (and for a nil
	// session): judge with headless Claude, the same backend Claude sessions use.
	claude := agentdriver.Get("claude")
	if state, err, ok := agentdriver.ClassifyWithDriver(
		claude,
		text,
		d.store.GetSetting(canonicalExecutableSettingKey("claude")),
		"",
		timeout,
	); ok {
		return state, err
	}
	return protocol.StateUnknown, errors.New("no classifier backend available")
}
