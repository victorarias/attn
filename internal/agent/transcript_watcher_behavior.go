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
