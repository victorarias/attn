package daemon

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	transcriptPollInterval   = 500 * time.Millisecond
	transcriptQuietWindow    = 1500 * time.Millisecond
	assistantDedupWindow     = 2 * time.Second
	toolStartGraceWindow     = 1200 * time.Millisecond
	codexActiveWindow        = 3 * time.Second
	codexBootstrapBytes      = 256 * 1024
	copilotBootstrapBytes    = 512 * 1024
	claudeBootstrapBytes     = 256 * 1024
	claudeHookStaleThreshold = 2 * time.Minute
)

type copilotPendingTool struct {
	name      string
	startedAt time.Time
}

type codexPendingTool struct {
	name      string
	startedAt time.Time
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
		if !tool.startedAt.IsZero() && now.Sub(tool.startedAt) >= toolStartGraceWindow {
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

func extractEventType(line []byte) string {
	var evt struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &evt); err != nil {
		return ""
	}
	return evt.Type
}

type transcriptWatcher struct {
	sessionID string
	agent     protocol.SessionAgent
	cwd       string
	startedAt time.Time
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func isDuplicateAssistantEvent(lastContent string, lastAt time.Time, content string, now time.Time) bool {
	return content == lastContent && !lastAt.IsZero() && now.Sub(lastAt) <= assistantDedupWindow
}

// shouldSkipClaudeWatcherClassification returns true when the transcript watcher
// should not trigger classification for a Claude session. Hooks are the
// authoritative source for "working" and "pending_approval" states; the watcher
// only classifies when the session appears done or when hooks have gone stale.
func shouldSkipClaudeWatcherClassification(agent protocol.SessionAgent, sessionState protocol.SessionState, lastSeen string) bool {
	if agent != protocol.SessionAgentClaude {
		return false
	}
	if sessionState != protocol.SessionStateWorking && sessionState != protocol.SessionStatePendingApproval {
		return false
	}
	parsed := protocol.Timestamp(lastSeen).Time()
	if parsed.IsZero() {
		return false
	}
	return time.Since(parsed) < claudeHookStaleThreshold
}

func isTranscriptWatchedAgent(agent protocol.SessionAgent) bool {
	return agent == protocol.SessionAgentCodex || agent == protocol.SessionAgentCopilot || agent == protocol.SessionAgentClaude
}

func (d *Daemon) startTranscriptWatcher(sessionID string, agent protocol.SessionAgent, cwd string, startedAt time.Time) {
	if !isTranscriptWatchedAgent(agent) {
		return
	}

	d.stopTranscriptWatcher(sessionID)

	watcher := &transcriptWatcher{
		sessionID: sessionID,
		agent:     agent,
		cwd:       cwd,
		startedAt: startedAt,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}

	d.watchersMu.Lock()
	if d.transcriptWatch == nil {
		d.transcriptWatch = make(map[string]*transcriptWatcher)
	}
	d.transcriptWatch[sessionID] = watcher
	d.watchersMu.Unlock()

	d.logf("transcript watcher: started session=%s agent=%s cwd=%s", sessionID, agent, cwd)
	go d.runTranscriptWatcher(watcher)
}

func (d *Daemon) stopTranscriptWatcher(sessionID string) {
	d.watchersMu.Lock()
	watcher, ok := d.transcriptWatch[sessionID]
	if ok {
		delete(d.transcriptWatch, sessionID)
	}
	d.watchersMu.Unlock()
	if ok {
		close(watcher.stopCh)
	}
}

func (d *Daemon) stopAllTranscriptWatchers() {
	d.watchersMu.Lock()
	watchers := make([]*transcriptWatcher, 0, len(d.transcriptWatch))
	for _, watcher := range d.transcriptWatch {
		watchers = append(watchers, watcher)
	}
	d.transcriptWatch = make(map[string]*transcriptWatcher)
	d.watchersMu.Unlock()

	for _, watcher := range watchers {
		close(watcher.stopCh)
	}
}

func (d *Daemon) runTranscriptWatcher(w *transcriptWatcher) {
	defer close(w.doneCh)

	ticker := time.NewTicker(transcriptPollInterval)
	defer ticker.Stop()

	var (
		transcriptPath string
		lastOffset     int64
		partialLine    string

		lastAssistantAt time.Time
		lastAssistant   string
		assistantSeq    int64
		classifiedSeq   int64

		lastDiscoveryLog      time.Time
		pendingTools          = make(map[string]copilotPendingTool)
		codexPendingTools     = make(map[string]codexPendingTool)
		transcriptPendingLive bool
		copilotTurnOpen       bool
		codexTurnOpen         bool
		codexActivityAt       time.Time
		codexAssistantInTurn  int
		codexTurnSawStart     bool
	)

	for {
		select {
		case <-w.stopCh:
			d.logf("transcript watcher: stopped session=%s", w.sessionID)
			return
		case <-ticker.C:
		}

		session := d.store.Get(w.sessionID)
		if session == nil {
			d.logf("transcript watcher: session removed, stopping session=%s", w.sessionID)
			return
		}
		if session.Agent != w.agent {
			d.logf("transcript watcher: agent changed, stopping session=%s old=%s new=%s", w.sessionID, w.agent, session.Agent)
			return
		}

		if transcriptPath == "" {
			switch w.agent {
			case protocol.SessionAgentClaude:
				transcriptPath = transcript.FindClaudeTranscript(w.sessionID)
			case protocol.SessionAgentCodex:
				transcriptPath = transcript.FindCodexTranscript(w.cwd, w.startedAt)
			case protocol.SessionAgentCopilot:
				transcriptPath = transcript.FindCopilotTranscript(w.cwd, w.startedAt)
			}
			if transcriptPath == "" {
				if time.Since(lastDiscoveryLog) >= 5*time.Second {
					d.logf("transcript watcher: waiting for transcript session=%s agent=%s cwd=%s", w.sessionID, w.agent, w.cwd)
					lastDiscoveryLog = time.Now()
				}
				continue
			}
			info, err := os.Stat(transcriptPath)
			if err != nil {
				d.logf("transcript watcher: transcript stat failed session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
				transcriptPath = ""
				pendingTools = make(map[string]copilotPendingTool)
				codexPendingTools = make(map[string]codexPendingTool)
				transcriptPendingLive = false
				copilotTurnOpen = false
				codexTurnOpen = false
				codexActivityAt = time.Time{}
				codexAssistantInTurn = 0
				codexTurnSawStart = false
				continue
			}
			lastOffset = info.Size()
			if w.agent == protocol.SessionAgentCodex {
				if info.Size() > codexBootstrapBytes {
					lastOffset = info.Size() - codexBootstrapBytes
				} else {
					lastOffset = 0
				}
			}
			if w.agent == protocol.SessionAgentCopilot {
				if info.Size() > copilotBootstrapBytes {
					lastOffset = info.Size() - copilotBootstrapBytes
				} else {
					lastOffset = 0
				}
			}
			if w.agent == protocol.SessionAgentClaude {
				if info.Size() > claudeBootstrapBytes {
					lastOffset = info.Size() - claudeBootstrapBytes
				} else {
					lastOffset = 0
				}
			}
			partialLine = ""
			pendingTools = make(map[string]copilotPendingTool)
			codexPendingTools = make(map[string]codexPendingTool)
			transcriptPendingLive = false
			copilotTurnOpen = false
			codexTurnOpen = false
			codexActivityAt = time.Time{}
			codexAssistantInTurn = 0
			codexTurnSawStart = false
			d.logf("transcript watcher: transcript discovered session=%s path=%s offset=%d", w.sessionID, transcriptPath, lastOffset)
			continue
		}

		info, err := os.Stat(transcriptPath)
		if err != nil {
			if os.IsNotExist(err) {
				d.logf("transcript watcher: transcript disappeared, rediscovering session=%s path=%s", w.sessionID, transcriptPath)
				transcriptPath = ""
				lastOffset = 0
				partialLine = ""
				pendingTools = make(map[string]copilotPendingTool)
				codexPendingTools = make(map[string]codexPendingTool)
				transcriptPendingLive = false
				copilotTurnOpen = false
				codexTurnOpen = false
				codexActivityAt = time.Time{}
				codexAssistantInTurn = 0
				codexTurnSawStart = false
				continue
			}
			d.logf("transcript watcher: transcript stat error session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
			continue
		}

		if info.Size() < lastOffset {
			lastOffset = 0
			partialLine = ""
		}

		if info.Size() > lastOffset {
			newBytes, readErr := readTranscriptDelta(transcriptPath, lastOffset)
			if readErr != nil {
				d.logf("transcript watcher: read delta error session=%s path=%s err=%v", w.sessionID, transcriptPath, readErr)
				continue
			}
			lastOffset += int64(len(newBytes))

			combined := partialLine + string(newBytes)
			lines := strings.Split(combined, "\n")
			partialLine = lines[len(lines)-1]
			lines = lines[:len(lines)-1]

			for _, line := range lines {
				line = strings.TrimRight(line, "\r")
				if strings.TrimSpace(line) == "" {
					continue
				}
				now := time.Now()
				if w.agent == protocol.SessionAgentCodex {
					if evt, ok := transcript.ExtractCodexLifecycle([]byte(line)); ok {
						switch evt.Kind {
						case "turn_start":
							codexTurnOpen = true
							codexActivityAt = now
							codexAssistantInTurn = 0
							codexTurnSawStart = true
							d.logf("transcript watcher: codex turn start session=%s", w.sessionID)
						case "turn_end":
							codexTurnOpen = false
							codexActivityAt = now
							codexPendingTools = make(map[string]codexPendingTool)
							d.logf(
								"transcript watcher: codex turn end session=%s assistant_messages=%d",
								w.sessionID,
								codexAssistantInTurn,
							)
							if codexAssistantInTurn == 0 {
								current := d.store.Get(w.sessionID)
								if current != nil &&
									shouldPromoteCodexNoOutputTurn(codexTurnSawStart, codexAssistantInTurn, current.State) {
									d.logf("transcript watcher: codex turn ended with no assistant output, setting waiting_input session=%s", w.sessionID)
									d.updateAndBroadcastState(w.sessionID, protocol.StateWaitingInput)
								}
							}
							codexAssistantInTurn = 0
							codexTurnSawStart = false
						case "turn_aborted":
							codexTurnOpen = false
							codexActivityAt = now
							codexPendingTools = make(map[string]codexPendingTool)
							codexAssistantInTurn = 0
							codexTurnSawStart = false
							current := d.store.Get(w.sessionID)
							if current != nil &&
								current.State != protocol.SessionStatePendingApproval &&
								current.State != protocol.SessionStateWaitingInput {
								d.logf("transcript watcher: codex turn aborted, setting waiting_input session=%s", w.sessionID)
								d.updateAndBroadcastState(w.sessionID, protocol.StateWaitingInput)
							}
						case "tool_start":
							codexTurnOpen = true
							codexActivityAt = now
							if evt.ToolCallID != "" {
								codexPendingTools[evt.ToolCallID] = codexPendingTool{
									name:      evt.ToolName,
									startedAt: now,
								}
								d.logf(
									"transcript watcher: codex tool start session=%s tool=%s call=%s",
									w.sessionID,
									evt.ToolName,
									evt.ToolCallID,
								)
							}
						case "tool_complete":
							codexActivityAt = now
							if evt.ToolCallID != "" {
								delete(codexPendingTools, evt.ToolCallID)
								d.logf("transcript watcher: codex tool complete session=%s call=%s", w.sessionID, evt.ToolCallID)
							}
						case "activity":
							codexActivityAt = now
						}
					}
				}
				if w.agent == protocol.SessionAgentCopilot {
					switch extractEventType([]byte(line)) {
					case "assistant.turn_start":
						copilotTurnOpen = true
						d.logf("transcript watcher: copilot turn start session=%s", w.sessionID)
						continue
					case "assistant.turn_end":
						copilotTurnOpen = false
						d.logf("transcript watcher: copilot turn end session=%s", w.sessionID)
						continue
					}
					if evt, ok := transcript.ExtractCopilotToolLifecycle([]byte(line)); ok {
						switch evt.Kind {
						case "start":
							if evt.ToolCallID != "" {
								pendingTools[evt.ToolCallID] = copilotPendingTool{
									name:      evt.ToolName,
									startedAt: time.Now(),
								}
								d.logf(
									"transcript watcher: tool start session=%s tool=%s call=%s",
									w.sessionID,
									evt.ToolName,
									evt.ToolCallID,
								)
							}
						case "complete":
							if evt.ToolCallID != "" {
								delete(pendingTools, evt.ToolCallID)
								d.logf("transcript watcher: tool complete session=%s call=%s", w.sessionID, evt.ToolCallID)
							}
						}
						continue
					}
				}
				content := strings.TrimSpace(transcript.ExtractAssistantContent([]byte(line)))
				if content == "" {
					continue
				}
				if w.agent == protocol.SessionAgentCopilot {
					copilotTurnOpen = true
				}
				if w.agent == protocol.SessionAgentCodex {
					codexActivityAt = now
					codexAssistantInTurn++
				}
				if w.agent != protocol.SessionAgentClaude && isDuplicateAssistantEvent(lastAssistant, lastAssistantAt, content, now) {
					continue
				}
				assistantSeq++
				lastAssistant = content
				lastAssistantAt = now

				logMsg := content
				if len(logMsg) > 120 {
					logMsg = logMsg[:120] + "..."
				}
				d.logf(
					"transcript watcher: assistant event session=%s seq=%d chars=%d preview=%q",
					w.sessionID,
					assistantSeq,
					len(content),
					logMsg,
				)
			}
		}

		pendingFromTranscript := false
		if w.agent == protocol.SessionAgentCopilot {
			pendingFromTranscript = hasCopilotTranscriptPendingApproval(pendingTools, time.Now(), copilotTurnOpen)
			if pendingFromTranscript {
				if shouldPromoteTranscriptPending(session.State) {
					d.logf("transcript watcher: promoting pending approval from transcript session=%s", w.sessionID)
					d.updateAndBroadcastState(w.sessionID, protocol.StatePendingApproval)
				}
				transcriptPendingLive = true
			} else if transcriptPendingLive {
				transcriptPendingLive = false
				current := d.store.Get(w.sessionID)
				if current != nil && current.State == protocol.SessionStatePendingApproval {
					d.logf("transcript watcher: clearing transcript pending approval session=%s", w.sessionID)
					d.updateAndBroadcastState(w.sessionID, protocol.StateWorking)
				}
			}
		}

		if pendingFromTranscript {
			continue
		}

		if w.agent == protocol.SessionAgentCodex && shouldKeepCodexWorking(codexTurnOpen, codexPendingTools, codexActivityAt, time.Now()) {
			current := d.store.Get(w.sessionID)
			if current != nil &&
				current.State != protocol.SessionStateWorking &&
				current.State != protocol.SessionStatePendingApproval {
				activityAge := int64(-1)
				if !codexActivityAt.IsZero() {
					activityAge = time.Since(codexActivityAt).Milliseconds()
				}
				d.logf(
					"transcript watcher: keeping codex working session=%s turn_open=%v pending_tools=%d activity_age_ms=%d",
					w.sessionID,
					codexTurnOpen,
					len(codexPendingTools),
					activityAge,
				)
				d.updateAndBroadcastState(w.sessionID, protocol.StateWorking)
			}
			continue
		}

		if w.agent == protocol.SessionAgentCopilot && copilotTurnOpen {
			current := d.store.Get(w.sessionID)
			if current != nil &&
				current.State != protocol.SessionStateWorking &&
				current.State != protocol.SessionStatePendingApproval {
				d.logf("transcript watcher: keeping copilot working while turn open session=%s", w.sessionID)
				d.updateAndBroadcastState(w.sessionID, protocol.StateWorking)
			}
			continue
		}

		quietSince := lastAssistantAt
		if w.agent == protocol.SessionAgentCodex && codexActivityAt.After(quietSince) {
			quietSince = codexActivityAt
		}

		if assistantSeq > classifiedSeq && !lastAssistantAt.IsZero() && !quietSince.IsZero() && time.Since(quietSince) >= transcriptQuietWindow {
			// For Claude, hooks are authoritative for working/pending states.
			// Skip watcher classification when hooks confirm the session is active.
			// Don't consume classifiedSeq here — the watcher must re-check each
			// poll so the 2-minute stale-hook safety valve can eventually fire.
			if current := d.store.Get(w.sessionID); current != nil &&
				shouldSkipClaudeWatcherClassification(w.agent, current.State, current.LastSeen) {
				d.logf(
					"transcript watcher: skipping classification, hooks active session=%s state=%s",
					w.sessionID,
					current.State,
				)
				continue
			}

			classifiedSeq = assistantSeq
			d.logf(
				"transcript watcher: quiet window reached session=%s seq=%d transcript=%s quiet_since=%s",
				w.sessionID,
				assistantSeq,
				transcriptPath,
				quietSince.Format(time.RFC3339Nano),
			)
			go d.classifyOrDeferAfterStop(w.sessionID, transcriptPath)
		}
	}
}

func readTranscriptDelta(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
