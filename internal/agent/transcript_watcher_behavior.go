package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	copilotToolStartGraceTime = 1200 * time.Millisecond
	claudeHookStaleThreshold  = 2 * time.Minute
)

// TranscriptWatcherBehaviorProvider allows agents to customize real-time
// transcript watcher behavior (lifecycle parsing, activity policy, and
// classification guards).
type TranscriptWatcherBehaviorProvider interface {
	NewTranscriptWatcherBehavior() TranscriptWatcherBehavior
}

// TranscriptWatcherBehavior encapsulates per-agent transcript watcher rules.
type TranscriptWatcherBehavior interface {
	// Reset clears any in-memory watcher state after transcript rediscovery.
	Reset()

	// HandleLine consumes one transcript JSONL line and may request an immediate
	// session state transition.
	HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult

	// HandleAssistantMessage is called for every accepted assistant message line.
	HandleAssistantMessage(now time.Time)

	// DeduplicateAssistantEvents controls duplicate-assistant suppression.
	DeduplicateAssistantEvents() bool

	// QuietSince returns the activity timestamp used for quiet-window checks.
	QuietSince(lastAssistantAt time.Time) time.Time

	// Tick runs per poll and may request a state update and/or block quiet-window
	// classification while the agent is still considered active.
	Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult

	// SkipClassification allows agents to suppress quiet-window classification
	// based on current state metadata (e.g., hook freshness).
	SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string)
}

// WatcherLineResult captures immediate watcher actions from one transcript line.
type WatcherLineResult struct {
	State string
	Log   string

	// Aborted: the line records the user halting the turn. Separate from State
	// because it is not a state the watcher is asking for — it is a fact about the
	// turn, which the resolver weighs against everything else it knows. AbortDetail
	// carries what the agent said about it, for the diagnosis, and AbortAt when the
	// agent says it happened — the watcher re-reads history often enough that an
	// undated halt cannot be told from one that just occurred.
	Aborted     bool
	AbortDetail string
	AbortAt     time.Time

	// BracketClosed: the turn this watcher was tracking is over, with nothing to
	// say about how it ended. Clearing the behavior's own flag is not enough for
	// an agent whose bracket the watcher opened in the first place — the evidence
	// bracket outlives it, and for an agent with no heartbeat nothing retires a
	// bracket except the stuck timer. Distinct from Aborted, which also closes the
	// brackets but files a halt the resolver can report; and from State, which
	// asserts what the session is doing now.
	BracketClosed bool
}

// WatcherTickResult captures periodic watcher actions on each poll.
type WatcherTickResult struct {
	State               string
	BlockClassification bool
	Log                 string
}

func newDefaultTranscriptWatcherBehavior() TranscriptWatcherBehavior {
	return &defaultTranscriptWatcherBehavior{}
}

type defaultTranscriptWatcherBehavior struct{}

func (b *defaultTranscriptWatcherBehavior) Reset() {}

func (b *defaultTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	return WatcherLineResult{}
}

func (b *defaultTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {}

func (b *defaultTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *defaultTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *defaultTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	return WatcherTickResult{}
}

func (b *defaultTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return false, ""
}

// --- Claude behavior ---

type claudeTranscriptWatcherBehavior struct{}

func (b *claudeTranscriptWatcherBehavior) Reset() {}

func (b *claudeTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	if abort, ok := transcript.ClaudeTurnAborted(line); ok {
		return WatcherLineResult{
			Aborted:     true,
			AbortDetail: abort.Reason,
			AbortAt:     abort.At,
			Log:         "transcript watcher: claude turn aborted by user",
		}
	}
	return WatcherLineResult{}
}

func (b *claudeTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {}

func (b *claudeTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return false }

func (b *claudeTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *claudeTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	return WatcherTickResult{}
}

func (b *claudeTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	// A scheduled session is parked on a /loop or cron and was set
	// authoritatively by the Stop hook. The transcript only shows the last turn
	// — which the classifier would read as idle/waiting/done, the wrong answer —
	// and the session leaves "scheduled" through the normal hook path when the
	// cron fires or the user acts. A genuinely dead park is demoted by session
	// reaping, not here. So never let the watcher reclassify it. This is an
	// UNCONDITIONAL skip (unlike the freshness-gated working/pending_approval
	// case below): parks routinely outlast the 2-minute hook-stale threshold,
	// and we must not flip the tile back mid-park.
	if sessionState == protocol.SessionStateScheduled {
		return true, "transcript watcher: skipping classification, session scheduled"
	}
	if sessionState != protocol.SessionStateWorking && sessionState != protocol.SessionStatePendingApproval {
		return false, ""
	}
	parsed := protocol.Timestamp(lastSeen).Time()
	if parsed.IsZero() {
		return false, ""
	}
	if now.Sub(parsed) < claudeHookStaleThreshold {
		return true, "transcript watcher: skipping classification, hooks active"
	}
	return false, ""
}

// --- Codex behavior ---

// Codex runs the watcher for one reason: an aborted turn is the only thing its
// hooks do not report. Everything else about a codex session is hook-owned and
// already arbitrated by the resolver, so this behavior asks for no states, keeps
// no lifecycle of its own, and — unlike claude, whose watcher predates the hook
// path and still classifies when hooks go stale — never classifies. A second
// classification driver would be racing the Stop hook's verdict to describe the
// same turn.
type codexTranscriptWatcherBehavior struct{}

func (b *codexTranscriptWatcherBehavior) Reset() {}

func (b *codexTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	abort, ok := transcript.CodexTurnAborted(line)
	if !ok {
		return WatcherLineResult{}
	}
	if !abort.UserHalt {
		// Codex owns its own state through hooks; a turn it abandoned for its own
		// reasons is already described there, and `replaced` in particular is
		// followed straight away by the turn that replaced it.
		return WatcherLineResult{
			Log: fmt.Sprintf("transcript watcher: codex turn aborted without a user halt reason=%s", abort.Reason),
		}
	}
	return WatcherLineResult{
		Aborted:     true,
		AbortDetail: abort.Reason,
		AbortAt:     abort.At,
		Log:         fmt.Sprintf("transcript watcher: codex turn aborted reason=%s", abort.Reason),
	}
}

func (b *codexTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {}

func (b *codexTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *codexTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *codexTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	return WatcherTickResult{}
}

func (b *codexTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return true, "transcript watcher: skipping classification, codex classification is hook-owned"
}

// --- Copilot behavior ---

type copilotPendingTool struct {
	name      string
	startedAt time.Time
}

type copilotTranscriptWatcherBehavior struct {
	turnOpen              bool
	pendingTools          map[string]copilotPendingTool
	transcriptPendingLive bool
}

func (b *copilotTranscriptWatcherBehavior) Reset() {
	b.turnOpen = false
	b.pendingTools = make(map[string]copilotPendingTool)
	b.transcriptPendingLive = false
}

func (b *copilotTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	// An abort is the one turn ending copilot does not follow with
	// `assistant.turn_end`. Measured on copilot 1.0.77: halting mid-reply writes
	// `{"type":"abort","data":{"reason":"user_initiated"}}` and nothing else, so
	// without this the bracket below stays open and Tick pins the session working
	// for the rest of its life. The bracket closes for every abort; only the user's
	// own settles the session.
	//
	// Closing it means closing both: this watcher's flag and the evidence bracket
	// the same `assistant.turn_start` opened through Tick. Copilot paints no
	// heartbeat, so an evidence bracket nothing retires is not held for StaleAfter
	// — it is held until the stuck timer, and the session reports `unknown`.
	if abort, ok := transcript.CopilotTurnAborted(line); ok {
		b.turnOpen = false
		b.pendingTools = make(map[string]copilotPendingTool)
		b.transcriptPendingLive = false
		if !abort.UserHalt {
			return WatcherLineResult{
				BracketClosed: true,
				Log:           fmt.Sprintf("transcript watcher: copilot turn aborted without a user halt reason=%s", abort.Reason),
			}
		}
		return WatcherLineResult{
			Aborted:     true,
			AbortDetail: abort.Reason,
			AbortAt:     abort.At,
			Log:         fmt.Sprintf("transcript watcher: copilot turn aborted reason=%s", abort.Reason),
		}
	}

	switch extractTranscriptEventType(line) {
	case "assistant.turn_start":
		b.turnOpen = true
		return WatcherLineResult{Log: "transcript watcher: copilot turn start"}
	case "assistant.turn_end":
		b.turnOpen = false
		return WatcherLineResult{Log: "transcript watcher: copilot turn end"}
	}
	evt, ok := transcript.ExtractCopilotToolLifecycle(line)
	if !ok {
		return WatcherLineResult{}
	}
	switch evt.Kind {
	case "start":
		if evt.ToolCallID != "" {
			b.pendingTools[evt.ToolCallID] = copilotPendingTool{
				name:      evt.ToolName,
				startedAt: now,
			}
			return WatcherLineResult{
				Log: fmt.Sprintf("transcript watcher: tool start tool=%s call=%s", evt.ToolName, evt.ToolCallID),
			}
		}
	case "complete":
		if evt.ToolCallID != "" {
			delete(b.pendingTools, evt.ToolCallID)
			return WatcherLineResult{
				Log: fmt.Sprintf("transcript watcher: tool complete call=%s", evt.ToolCallID),
			}
		}
	}
	return WatcherLineResult{}
}

func (b *copilotTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {
	b.turnOpen = true
}

func (b *copilotTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *copilotTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	return lastAssistantAt
}

func (b *copilotTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	result := WatcherTickResult{}

	pendingFromTranscript := hasCopilotTranscriptPendingApproval(b.pendingTools, now, b.turnOpen)
	if pendingFromTranscript {
		result.BlockClassification = true
		if shouldPromoteTranscriptPending(sessionState) {
			result.State = protocol.StatePendingApproval
			result.Log = "transcript watcher: promoting pending approval from transcript"
		}
		b.transcriptPendingLive = true
		return result
	}

	if b.transcriptPendingLive {
		b.transcriptPendingLive = false
		if sessionState == protocol.SessionStatePendingApproval {
			result.State = protocol.StateWorking
			result.Log = "transcript watcher: clearing transcript pending approval"
		}
	}

	if b.turnOpen {
		result.BlockClassification = true
		if result.State == "" &&
			sessionState != protocol.SessionStateWorking &&
			sessionState != protocol.SessionStatePendingApproval {
			result.State = protocol.StateWorking
			result.Log = "transcript watcher: keeping copilot working while turn open"
		}
	}
	return result
}

func (b *copilotTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
	return false, ""
}

func isCopilotApprovalTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "bash", "create":
		return true
	default:
		return false
	}
}

func hasCopilotTranscriptPendingApproval(pending map[string]copilotPendingTool, now time.Time, turnOpen bool) bool {
	if !turnOpen {
		return false
	}
	for _, tool := range pending {
		if !isCopilotApprovalTool(tool.name) {
			continue
		}
		if !tool.startedAt.IsZero() && now.Sub(tool.startedAt) >= copilotToolStartGraceTime {
			return true
		}
	}
	return false
}

func shouldPromoteTranscriptPending(sessionState protocol.SessionState) bool {
	switch sessionState {
	case protocol.SessionStateIdle,
		protocol.SessionStateWaitingInput,
		protocol.SessionStateUnknown,
		protocol.SessionStateLaunching:
		return true
	default:
		return false
	}
}

func extractTranscriptEventType(line []byte) string {
	var evt struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &evt); err != nil {
		return ""
	}
	return evt.Type
}
