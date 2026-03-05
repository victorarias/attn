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

// PTYStatePolicyProvider customizes recovered-state mapping and live PTY state
// update filtering for an agent.
type PTYStatePolicyProvider interface {
	RecoveredRunningState(ptyState string) protocol.SessionState
	ShouldApplyPTYState(current protocol.SessionState, incoming string) bool
}

// ResumePolicyProvider customizes resume ID lifecycle behavior.
type ResumePolicyProvider interface {
	ResolveSpawnResumeSessionID(existingSessionID, requestedResumeID, storedResumeID string) string
	SpawnResumeSessionID(sessionID, resolvedResumeID string, resumePicker bool) string
	ResumeSessionIDFromStopTranscriptPath(transcriptPath string) string
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
	ClassifyWithExecutable(text, executable string, timeout time.Duration) (string, error)
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

func RecoveredRunningSessionState(d Driver, ptyState string) protocol.SessionState {
	if p, ok := d.(PTYStatePolicyProvider); ok {
		return p.RecoveredRunningState(ptyState)
	}
	switch ptyState {
	case protocol.StateWaitingInput:
		return protocol.SessionStateWaitingInput
	case protocol.StatePendingApproval:
		return protocol.SessionStatePendingApproval
	default:
		// Recovered sessions should default to launching unless we have explicit
		// runtime evidence that the session is waiting for input/approval.
		return protocol.SessionStateLaunching
	}
}

func ShouldApplyPTYState(d Driver, current protocol.SessionState, incoming string) bool {
	if p, ok := d.(PTYStatePolicyProvider); ok {
		return p.ShouldApplyPTYState(current, incoming)
	}
	return true
}

func ResolveSpawnResumeSessionID(d Driver, existingSessionID, requestedResumeID, storedResumeID string) string {
	requested := strings.TrimSpace(requestedResumeID)
	stored := strings.TrimSpace(storedResumeID)
	if p, ok := d.(ResumePolicyProvider); ok {
		return strings.TrimSpace(p.ResolveSpawnResumeSessionID(existingSessionID, requested, stored))
	}
	return requested
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

func ClassifyWithDriver(d Driver, text, executable string, timeout time.Duration) (state string, err error, ok bool) {
	cp, hasClassifier := GetClassifier(d)
	if !hasClassifier {
		return "", nil, false
	}
	if ecp, supportsExecutable := cp.(ExecutableClassifierProvider); supportsExecutable {
		state, err = ecp.ClassifyWithExecutable(text, strings.TrimSpace(executable), timeout)
		return state, err, true
	}
	state, err = cp.Classify(text, timeout)
	return state, err, true
}
