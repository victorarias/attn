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
	codexActiveWindow         = 3 * time.Second
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

type codexPendingTool struct {
	name      string
	startedAt time.Time
}

type codexTranscriptWatcherBehavior struct {
	turnOpen        bool
	pendingTools    map[string]codexPendingTool
	lastActivityAt  time.Time
	assistantInTurn int
	sawTurnStart    bool
}

func (b *codexTranscriptWatcherBehavior) Reset() {
	b.turnOpen = false
	b.pendingTools = make(map[string]codexPendingTool)
	b.lastActivityAt = time.Time{}
	b.assistantInTurn = 0
	b.sawTurnStart = false
}

func (b *codexTranscriptWatcherBehavior) HandleLine(line []byte, now time.Time, sessionState protocol.SessionState) WatcherLineResult {
	evt, ok := transcript.ExtractCodexLifecycle(line)
	if !ok {
		return WatcherLineResult{}
	}
	switch evt.Kind {
	case "turn_start":
		b.turnOpen = true
		b.lastActivityAt = now
		b.assistantInTurn = 0
		b.sawTurnStart = true
		return WatcherLineResult{
			Log: "transcript watcher: codex turn start",
		}
	case "turn_end":
		b.turnOpen = false
		b.lastActivityAt = now
		b.pendingTools = make(map[string]codexPendingTool)
		sawTurnStart := b.sawTurnStart
		assistantCount := b.assistantInTurn
		b.assistantInTurn = 0
		b.sawTurnStart = false
		if shouldPromoteCodexNoOutputTurn(sawTurnStart, assistantCount, sessionState) {
			return WatcherLineResult{
				State: protocol.StateWaitingInput,
				Log:   "transcript watcher: codex turn ended with no assistant output, setting waiting_input",
			}
		}
		return WatcherLineResult{
			Log: fmt.Sprintf("transcript watcher: codex turn end assistant_messages=%d", assistantCount),
		}
	case "turn_aborted":
		b.turnOpen = false
		b.lastActivityAt = now
		b.pendingTools = make(map[string]codexPendingTool)
		b.assistantInTurn = 0
		b.sawTurnStart = false
		if sessionState != protocol.SessionStatePendingApproval && sessionState != protocol.SessionStateWaitingInput {
			return WatcherLineResult{
				State: protocol.StateWaitingInput,
				Log:   "transcript watcher: codex turn aborted, setting waiting_input",
			}
		}
	case "tool_start":
		b.turnOpen = true
		b.lastActivityAt = now
		if evt.ToolCallID != "" {
			b.pendingTools[evt.ToolCallID] = codexPendingTool{
				name:      evt.ToolName,
				startedAt: now,
			}
			return WatcherLineResult{
				Log: fmt.Sprintf("transcript watcher: codex tool start tool=%s call=%s", evt.ToolName, evt.ToolCallID),
			}
		}
	case "tool_complete":
		b.lastActivityAt = now
		if evt.ToolCallID != "" {
			delete(b.pendingTools, evt.ToolCallID)
			return WatcherLineResult{
				Log: fmt.Sprintf("transcript watcher: codex tool complete call=%s", evt.ToolCallID),
			}
		}
	case "activity":
		b.lastActivityAt = now
	}
	return WatcherLineResult{}
}

func (b *codexTranscriptWatcherBehavior) HandleAssistantMessage(now time.Time) {
	b.lastActivityAt = now
	b.assistantInTurn++
}

func (b *codexTranscriptWatcherBehavior) DeduplicateAssistantEvents() bool { return true }

func (b *codexTranscriptWatcherBehavior) QuietSince(lastAssistantAt time.Time) time.Time {
	if b.lastActivityAt.After(lastAssistantAt) {
		return b.lastActivityAt
	}
	return lastAssistantAt
}

func (b *codexTranscriptWatcherBehavior) Tick(now time.Time, sessionState protocol.SessionState) WatcherTickResult {
	if !shouldKeepCodexWorking(b.turnOpen, b.pendingTools, b.lastActivityAt, now) {
		return WatcherTickResult{}
	}
	if sessionState == protocol.SessionStateWorking || sessionState == protocol.SessionStatePendingApproval {
		return WatcherTickResult{BlockClassification: true}
	}
	activityAge := int64(-1)
	if !b.lastActivityAt.IsZero() {
		activityAge = now.Sub(b.lastActivityAt).Milliseconds()
	}
	return WatcherTickResult{
		State:               protocol.StateWorking,
		BlockClassification: true,
		Log: fmt.Sprintf(
			"transcript watcher: keeping codex working turn_open=%v pending_tools=%d activity_age_ms=%d",
			b.turnOpen,
			len(b.pendingTools),
			activityAge,
		),
	}
}

func (b *codexTranscriptWatcherBehavior) SkipClassification(sessionState protocol.SessionState, lastSeen string, now time.Time) (bool, string) {
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

func shouldKeepCodexWorking(turnOpen bool, pendingTools map[string]codexPendingTool, lastActivityAt time.Time, now time.Time) bool {
	if turnOpen {
		return true
	}
	if len(pendingTools) > 0 {
		return true
	}
	if !lastActivityAt.IsZero() && now.Sub(lastActivityAt) <= codexActiveWindow {
		return true
	}
	return false
}

func shouldPromoteCodexNoOutputTurn(sawTurnStart bool, assistantMessages int, sessionState protocol.SessionState) bool {
	if !sawTurnStart {
		return false
	}
	if assistantMessages > 0 {
		return false
	}
	if sessionState == protocol.SessionStatePendingApproval || sessionState == protocol.SessionStateWaitingInput {
		return false
	}
	return true
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
