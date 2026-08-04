package agent

import (
	"errors"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// ErrNoNewAssistantTurn indicates there is no new assistant turn to classify.
var ErrNoNewAssistantTurn = errors.New("no new assistant turn")

// SessionRecoveryPolicyProvider customizes startup behavior when a stored session
// is missing a live PTY backend runtime.
type SessionRecoveryPolicyProvider interface {
	RecoverOnMissingPTY() bool
}

// RecoveredStatePolicyProvider customizes how a recovered session's PTY state
// maps onto a session state at startup. The second return is whether the driver
// has an opinion at all; see RecoveredRunningSessionState.
type RecoveredStatePolicyProvider interface {
	RecoveredRunningState(ptyState string) (protocol.SessionState, bool)
}

// ResumePolicyProvider customizes resume ID lifecycle behavior.
type ResumePolicyProvider interface {
	ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string
	SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string
	ResumeSessionIDFromStopTranscriptPath(transcriptPath string) string
}

// ResumeAvailabilityProvider reports whether a resolved resume target can
// actually be resumed on disk right now. A session that booted to its prompt but
// took zero turns leaves nothing to resume — Claude writes its transcript lazily
// on the first turn — so a resume against it fails and kills the agent. Drivers
// that can cheaply check implement this; the daemon downgrades a doomed resume to
// a fresh spawn instead.
type ResumeAvailabilityProvider interface {
	// ResumeAvailable reports whether resumeID can be resumed. resumeID is the
	// already-resolved resume target and is never empty when called.
	ResumeAvailable(resumeID string) bool
}

// TranscriptClassificationExtractor customizes transcript parsing for stop-time
// classification.
type TranscriptClassificationExtractor interface {
	ExtractLastAssistantForClassification(
		transcriptPath string,
		maxChars int,
		classificationStart time.Time,
		lastClassifiedTurnID string,
	) (content string, turnID string, err error)
}

// ExecutableClassifierProvider is an optional classifier extension for agents
// that need an explicit executable path at classification time.
type ExecutableClassifierProvider interface {
	ClassifyWithExecutable(text, executable, workDir string, timeout time.Duration) (string, error)
}

func RecoverOnMissingPTY(d Driver) bool {
	if d == nil {
		return false
	}
	if p, ok := d.(SessionRecoveryPolicyProvider); ok {
		return p.RecoverOnMissingPTY()
	}
	return false
}

// RecoveredRunningSessionState maps a recovered worker's cached PTY state onto a
// session state, and reports false when there is nothing there to map.
//
// The false case is the common one and it is not a failure: since the screen
// scraper was deleted, no observer inside a worker names a protocol state, so a
// running worker's cached state is empty for every agent. It used to be filled
// in with `launching` on the theory that a recovered session had not proved
// itself yet — but a daemon restart does not un-run an agent, and the session's
// persisted state is the last thing the resolver concluded about it. Overwriting
// that with `launching` discarded the state of every live session on every
// restart, and left the quiet ones there indefinitely: an agent parked at its
// prompt paints nothing, so no evidence ever arrived to move it again.
//
// So the rule is now the honest one — say something only when the worker
// actually observed something, and otherwise leave the persisted state alone.
func RecoveredRunningSessionState(d Driver, ptyState string) (protocol.SessionState, bool) {
	if p, ok := d.(RecoveredStatePolicyProvider); ok {
		return p.RecoveredRunningState(ptyState)
	}
	return recoveredStateFromPTYClaim(ptyState)
}

// recoveredStateFromPTYClaim is the shared mapping: the two states a worker can
// still claim, and no opinion about anything else.
func recoveredStateFromPTYClaim(ptyState string) (protocol.SessionState, bool) {
	switch ptyState {
	case protocol.StateWaitingInput:
		return protocol.SessionStateWaitingInput, true
	case protocol.StatePendingApproval:
		return protocol.SessionStatePendingApproval, true
	default:
		return "", false
	}
}

func ResolveSpawnResumeSessionID(d Driver, existingSessionID, requestedResumeID, storedResumeID string) string {
	requested := strings.TrimSpace(requestedResumeID)
	stored := strings.TrimSpace(storedResumeID)
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.ResolveSpawnResumeSessionID(existingSessionID, requested, stored))
	}
	return requested
}

// ResumeAvailable reports whether resumeID is resumable for this driver. Drivers
// that don't implement ResumeAvailabilityProvider are assumed always-resumable
// (true) so resume behavior is unchanged for them. An empty resumeID is reported
// as not resumable (there is nothing to resume).
func ResumeAvailable(d Driver, resumeID string) bool {
	if strings.TrimSpace(resumeID) == "" {
		return false
	}
	if p, ok := d.(ResumeAvailabilityProvider); ok {
		return p.ResumeAvailable(resumeID)
	}
	return true
}

func SpawnResumeSessionID(d Driver, sessionID, resolvedResumeID string, resumePicker bool) string {
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.SpawnResumeSessionID(sessionID, resolvedResumeID, resumePicker))
	}
	return ""
}

func ResumeSessionIDFromStopTranscriptPath(d Driver, transcriptPath string) string {
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.ResumeSessionIDFromStopTranscriptPath(transcriptPath))
	}
	return ""
}

func ExtractLastAssistantForClassification(
	d Driver,
	transcriptPath string,
	maxChars int,
	classificationStart time.Time,
	lastClassifiedTurnID string,
) (content string, turnID string, err error) {
	if p, ok := d.(TranscriptClassificationExtractor); ok {
		return p.ExtractLastAssistantForClassification(
			transcriptPath,
			maxChars,
			classificationStart,
			lastClassifiedTurnID,
		)
	}
	content, err = transcript.ExtractLastAssistantMessage(transcriptPath, maxChars)
	return content, "", err
}

func ClassifyWithDriver(d Driver, text, executable, workDir string, timeout time.Duration) (state string, err error, ok bool) {
	cp, hasClassifier := GetClassifier(d)
	if !hasClassifier {
		return "", nil, false
	}
	if ecp, supportsExecutable := cp.(ExecutableClassifierProvider); supportsExecutable {
		state, err = ecp.ClassifyWithExecutable(text, strings.TrimSpace(executable), strings.TrimSpace(workDir), timeout)
		return state, err, true
	}
	state, err = cp.Classify(text, timeout)
	return state, err, true
}
