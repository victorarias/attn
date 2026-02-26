package daemon

import (
	"io"
	"os"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	transcriptPollInterval = 500 * time.Millisecond
	transcriptQuietWindow  = 1500 * time.Millisecond
	assistantDedupWindow   = 2 * time.Second
)

type transcriptWatcher struct {
	sessionID string
	agent     protocol.SessionAgent
	cwd       string
	startedAt time.Time
	behavior  agentdriver.TranscriptWatcherBehavior
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func isDuplicateAssistantEvent(lastContent string, lastAt time.Time, content string, now time.Time) bool {
	return content == lastContent && !lastAt.IsZero() && now.Sub(lastAt) <= assistantDedupWindow
}

func isTranscriptWatchedAgent(agent protocol.SessionAgent) bool {
	d := agentdriver.Get(string(agent))
	if d == nil {
		return false
	}
	caps := agentdriver.EffectiveCapabilities(d)
	if !caps.HasTranscript || !caps.HasTranscriptWatcher {
		return false
	}
	if _, ok := agentdriver.GetTranscriptFinder(d); !ok {
		return false
	}
	return true
}

func (d *Daemon) findTranscriptPathForWatcher(w *transcriptWatcher) string {
	driver := agentdriver.Get(string(w.agent))
	tf, ok := agentdriver.GetTranscriptFinder(driver)
	if !ok {
		return ""
	}
	return strings.TrimSpace(tf.FindTranscript(w.sessionID, w.cwd, w.startedAt))
}

func (d *Daemon) transcriptBootstrapBytesForAgent(agent protocol.SessionAgent) int64 {
	driver := agentdriver.Get(string(agent))
	if tf, ok := agentdriver.GetTranscriptFinder(driver); ok {
		if n := tf.BootstrapBytes(); n > 0 {
			return n
		}
	}
	return 0
}

func (d *Daemon) startTranscriptWatcher(sessionID string, agent protocol.SessionAgent, cwd string, startedAt time.Time) {
	if !isTranscriptWatchedAgent(agent) {
		return
	}

	driver := agentdriver.Get(string(agent))
	behavior, ok := agentdriver.GetTranscriptWatcherBehavior(driver)
	if !ok {
		return
	}

	d.stopTranscriptWatcher(sessionID)

	watcher := &transcriptWatcher{
		sessionID: sessionID,
		agent:     agent,
		cwd:       cwd,
		startedAt: startedAt,
		behavior:  behavior,
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

	if w.behavior == nil {
		d.logf("transcript watcher: no behavior configured session=%s agent=%s", w.sessionID, w.agent)
		return
	}

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

		lastDiscoveryLog time.Time
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
		sessionState := session.State

		if transcriptPath == "" {
			transcriptPath = d.findTranscriptPathForWatcher(w)
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
				lastOffset = 0
				partialLine = ""
				w.behavior.Reset()
				continue
			}
			lastOffset = info.Size()
			bootstrapBytes := d.transcriptBootstrapBytesForAgent(w.agent)
			if bootstrapBytes > 0 && info.Size() > bootstrapBytes {
				lastOffset = info.Size() - bootstrapBytes
			} else if bootstrapBytes > 0 {
				lastOffset = 0
			}
			partialLine = ""
			w.behavior.Reset()
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
				w.behavior.Reset()
				continue
			}
			d.logf("transcript watcher: transcript stat error session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
			continue
		}

		if info.Size() < lastOffset {
			lastOffset = 0
			partialLine = ""
			w.behavior.Reset()
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

				lineResult := w.behavior.HandleLine([]byte(line), now, sessionState)
				if lineResult.Log != "" {
					d.logf("%s session=%s", lineResult.Log, w.sessionID)
				}
				if lineResult.State != "" && protocol.SessionState(lineResult.State) != sessionState {
					d.updateAndBroadcastState(w.sessionID, lineResult.State)
					sessionState = protocol.SessionState(lineResult.State)
				}

				content := strings.TrimSpace(transcript.ExtractAssistantContent([]byte(line)))
				if content == "" {
					continue
				}

				w.behavior.HandleAssistantMessage(now)
				if w.behavior.DeduplicateAssistantEvents() &&
					isDuplicateAssistantEvent(lastAssistant, lastAssistantAt, content, now) {
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

		tickResult := w.behavior.Tick(time.Now(), sessionState)
		if tickResult.Log != "" {
			d.logf("%s session=%s", tickResult.Log, w.sessionID)
		}
		if tickResult.State != "" && protocol.SessionState(tickResult.State) != sessionState {
			d.updateAndBroadcastState(w.sessionID, tickResult.State)
			sessionState = protocol.SessionState(tickResult.State)
		}
		if tickResult.BlockClassification {
			continue
		}

		quietSince := w.behavior.QuietSince(lastAssistantAt)
		if assistantSeq > classifiedSeq &&
			!lastAssistantAt.IsZero() &&
			!quietSince.IsZero() &&
			time.Since(quietSince) >= transcriptQuietWindow {
			if current := d.store.Get(w.sessionID); current != nil {
				if skip, reason := w.behavior.SkipClassification(current.State, current.LastSeen, time.Now()); skip {
					if strings.TrimSpace(reason) == "" {
						reason = "transcript watcher: skipping classification"
					}
					d.logf("%s session=%s state=%s", reason, w.sessionID, current.State)
					continue
				}
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
