package daemon

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	transcriptPollInterval = 500 * time.Millisecond
	transcriptQuietWindow  = 1500 * time.Millisecond
)

type transcriptWatcher struct {
	sessionID string
	agent     protocol.SessionAgent
	cwd       string
	startedAt time.Time
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func isTranscriptWatchedAgent(agent protocol.SessionAgent) bool {
	return agent == protocol.SessionAgentCodex || agent == protocol.SessionAgentCopilot
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

		if transcriptPath == "" {
			switch w.agent {
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
				continue
			}
			lastOffset = info.Size()
			partialLine = ""
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
				content := strings.TrimSpace(transcript.ExtractAssistantContent([]byte(line)))
				if content == "" {
					continue
				}
				assistantSeq++
				lastAssistantAt = time.Now()

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

		if assistantSeq > classifiedSeq && !lastAssistantAt.IsZero() && time.Since(lastAssistantAt) >= transcriptQuietWindow {
			classifiedSeq = assistantSeq
			d.logf(
				"transcript watcher: quiet window reached session=%s seq=%d transcript=%s",
				w.sessionID,
				assistantSeq,
				transcriptPath,
			)
			go d.classifySessionState(w.sessionID, transcriptPath)
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
