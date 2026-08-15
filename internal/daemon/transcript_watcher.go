package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	transcriptPollInterval   = 500 * time.Millisecond
	transcriptQuietWindow    = 1500 * time.Millisecond
	assistantDedupWindow     = 2 * time.Second
	transcriptDiscoveryGrace = 2 * time.Second

	// Discovery is a directory walk — codex and copilot search their whole session
	// tree, thousands of files on a working machine — and it repeats every poll
	// until it lands. A session that never gets a transcript (one parked at a trust
	// prompt, one whose agent writes nowhere we look) would otherwise walk that
	// tree twice a second for as long as it lives. Fast while a transcript is
	// plausibly still being created, then slow, because after a minute it is not
	// arriving in the next half second either.
	transcriptDiscoveryFastAttempts = 20
	transcriptDiscoverySlowAttempts = 40
	transcriptDiscoverySlowInterval = 2 * time.Second
	transcriptDiscoveryIdleInterval = 5 * time.Second
)

// transcriptDiscoveryDelay is how long to wait before the next discovery attempt
// after `attempts` have failed. Zero means the next poll.
func transcriptDiscoveryDelay(attempts int) time.Duration {
	switch {
	case attempts < transcriptDiscoveryFastAttempts:
		return 0
	case attempts < transcriptDiscoverySlowAttempts:
		return transcriptDiscoverySlowInterval
	default:
		return transcriptDiscoveryIdleInterval
	}
}

type transcriptWatcher struct {
	sessionID string
	agent     protocol.SessionAgent
	cwd       string
	startedAt time.Time
	// preferredPath is the exact path reported by SessionStart. It is a runtime
	// hint; after restart the persisted native id resolves the same transcript.
	preferredPath string
	behavior      agentdriver.TranscriptWatcherBehavior
	stopCh        chan struct{}
	doneCh        chan struct{}

	mu             sync.RWMutex
	status         protocol.SessionMessageWindowStatus
	detail         string
	transcriptPath string
	window         *transcript.AssistantWindow
}

type assistantWindowSnapshot struct {
	Status   protocol.SessionMessageWindowStatus
	Messages []transcript.AssistantMessage
	Report   transcript.AssistantWindowReport
	Detail   string
}

func newTranscriptWatcher(sessionID string, agent protocol.SessionAgent, cwd string, startedAt time.Time, behavior agentdriver.TranscriptWatcherBehavior) *transcriptWatcher {
	return &transcriptWatcher{
		sessionID: sessionID,
		agent:     agent,
		cwd:       cwd,
		startedAt: startedAt,
		behavior:  behavior,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		status:    protocol.SessionMessageWindowStatusDiscovering,
		window:    newAnnotatableWindow(),
	}
}

func newAnnotatableWindow() *transcript.AssistantWindow {
	return transcript.NewAssistantWindow(transcript.AssistantWindowLimits{
		MaxMessages:     annotatableWindowMessages,
		MaxMessageChars: annotatableMessageMaxChars,
		MaxTotalChars:   annotatableWindowMaxChars,
	})
}

func (w *transcriptWatcher) snapshot() assistantWindowSnapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	messages, report := w.window.Snapshot()
	return assistantWindowSnapshot{Status: w.status, Messages: messages, Report: report, Detail: w.detail}
}

func (w *transcriptWatcher) exactPath() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.status != protocol.SessionMessageWindowStatusReady {
		return ""
	}
	return w.transcriptPath
}

func (w *transcriptWatcher) setStatus(status protocol.SessionMessageWindowStatus, detail string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	changed := w.status != status || w.detail != detail
	w.status = status
	w.detail = detail
	return changed
}

func (w *transcriptWatcher) resetSource(status protocol.SessionMessageWindowStatus, path, detail string, omittedPrefix bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
	w.detail = detail
	w.transcriptPath = path
	w.window = newAnnotatableWindow()
	if omittedPrefix {
		w.window.MarkPrefixOmitted()
	}
}

func (w *transcriptWatcher) applyEvents(events []transcript.Event) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.window.Apply(events)
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

func (d *Daemon) resolveExactTranscriptPathForWatcher(w *transcriptWatcher) string {
	if path := strings.TrimSpace(w.preferredPath); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	driver := agentdriver.Get(string(w.agent))
	tf, ok := agentdriver.GetTranscriptFinder(driver)
	if !ok {
		return ""
	}
	if resumeID := strings.TrimSpace(d.store.GetResumeSessionID(w.sessionID)); resumeID != "" {
		if path := strings.TrimSpace(tf.FindTranscriptForResume(resumeID)); path != "" {
			return path
		}
	}
	return ""
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
	d.startTranscriptWatcherAtPath(sessionID, agent, cwd, startedAt, "")
}

func (d *Daemon) startTranscriptWatcherAtPath(sessionID string, agent protocol.SessionAgent, cwd string, startedAt time.Time, transcriptPath string) {
	if !isTranscriptWatchedAgent(agent) {
		return
	}

	driver := agentdriver.Get(string(agent))
	behavior, ok := agentdriver.GetTranscriptWatcherBehavior(driver)
	if !ok {
		return
	}

	d.stopTranscriptWatcher(sessionID)

	watcher := newTranscriptWatcher(sessionID, agent, cwd, startedAt, behavior)
	watcher.preferredPath = strings.TrimSpace(transcriptPath)

	d.watchersMu.Lock()
	if d.transcriptWatch == nil {
		d.transcriptWatch = make(map[string]*transcriptWatcher)
	}
	d.transcriptWatch[sessionID] = watcher
	d.watchersMu.Unlock()

	d.logf("transcript watcher: started session=%s agent=%s cwd=%s", sessionID, agent, cwd)
	go d.runTranscriptWatcher(watcher)
}

// restoreTranscriptWatchers gives surviving local runtimes their exact durable
// resume identity after a daemon restart. Sessions without one stay explicitly
// unavailable rather than guessing from cwd and recovery time.
func (d *Daemon) restoreTranscriptWatchers() {
	if d.store == nil || d.ptyBackend == nil {
		return
	}
	live := make(map[string]struct{})
	for _, id := range d.ptyBackend.SessionIDs(context.Background()) {
		live[id] = struct{}{}
	}
	for _, session := range d.store.List("") {
		if session == nil || session.Agent == protocol.SessionAgentShell {
			continue
		}
		if _, ok := live[session.ID]; !ok {
			continue
		}
		if strings.TrimSpace(d.store.GetResumeSessionID(session.ID)) == "" {
			continue
		}
		d.startTranscriptWatcher(session.ID, session.Agent, session.Directory, time.Now())
	}
}

func (d *Daemon) newSessionCostFollower(w *transcriptWatcher, path string) (*transcript.Follower, error) {
	state, err := d.store.SessionCost(w.sessionID)
	if err != nil {
		return nil, err
	}
	if !state.Initialized {
		cursor, err := transcript.HeadCursor(path)
		if err != nil {
			return nil, err
		}
		if err := d.store.SetSessionCostCursor(w.sessionID, cursor); err != nil {
			return nil, err
		}
		if cursor == "" {
			return transcript.NewFollower(path, string(w.agent), 0)
		}
		return transcript.NewFollowerAfterCursor(path, string(w.agent), cursor)
	}
	if state.Cursor == "" {
		return transcript.NewFollower(path, string(w.agent), 0)
	}
	follower, err := transcript.NewFollowerAfterCursor(path, string(w.agent), state.Cursor)
	if err == nil {
		return follower, nil
	}
	if !errors.Is(err, transcript.ErrCursorMismatch) &&
		!errors.Is(err, transcript.ErrCursorPastEnd) &&
		!errors.Is(err, transcript.ErrInvalidCursor) {
		return nil, err
	}

	// A cursor that no longer names this file cannot prove where new traffic
	// begins. Seed at the current head: replaying can double-charge Codex records,
	// while skipping an uncertain prefix preserves the forward-only contract.
	cursor, err := transcript.HeadCursor(path)
	if err != nil {
		return nil, err
	}
	if err := d.store.SetSessionCostCursor(w.sessionID, cursor); err != nil {
		return nil, err
	}
	if cursor == "" {
		return transcript.NewFollower(path, string(w.agent), 0)
	}
	return transcript.NewFollowerAfterCursor(path, string(w.agent), cursor)
}

func (d *Daemon) applySessionCostBatch(w *transcriptWatcher, follower *transcript.Follower, batch transcript.FollowBatch) error {
	if len(batch.Records) == 0 {
		return nil
	}
	cursor := follower.Cursor()
	changed := false
	if !transcript.SupportsUsage(string(w.agent)) {
		for _, event := range batch.Events {
			if event.Kind == transcript.EventKindAssistant {
				var err error
				changed, err = d.store.MarkSessionCostUsageUnavailable(w.sessionID, cursor)
				if err != nil {
					return err
				}
				break
			}
		}
		if !changed {
			if err := d.store.SetSessionCostCursor(w.sessionID, cursor); err != nil {
				return err
			}
		}
	} else {
		observations := make([]store.SessionCostObservation, 0, len(batch.Usage))
		for _, usage := range batch.Usage {
			model := strings.TrimSpace(usage.Model)
			if model == "" {
				model = "<unknown>"
			}
			observations = append(observations, store.SessionCostObservation{
				ObservationID: usage.Key,
				Model:         model,
				Usage: sessioncost.Usage{
					InputTokens:                  usage.InputTokens,
					OutputTokens:                 usage.OutputTokens,
					CacheReadInputTokens:         usage.CacheReadTokens,
					CacheWrite5mInputTokens:      usage.CacheWrite5mTokens,
					CacheWrite1hInputTokens:      usage.CacheWrite1hTokens,
					UnclassifiedCacheWriteTokens: usage.CacheWriteUnclassifiedTokens,
				},
			})
		}
		var err error
		changed, err = d.store.ApplySessionCostObservations(w.sessionID, cursor, observations)
		if err != nil {
			return err
		}
	}
	if changed {
		d.publishFact(FactSessionCostChanged, w.sessionID, nil)
	}
	return nil
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

func (d *Daemon) assistantWindow(sessionID string, agent protocol.SessionAgent) (assistantWindowSnapshot, bool) {
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return assistantWindowSnapshot{}, false
	}
	return watcher.snapshot(), true
}

func (d *Daemon) liveTranscriptPath(sessionID string, agent protocol.SessionAgent) string {
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return ""
	}
	return watcher.exactPath()
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
		follower       *transcript.Follower
		costFollower   *transcript.Follower
		costFileSize   int64 = -1

		lastAssistantAt time.Time
		lastAssistant   string
		assistantSeq    int64
		classifiedSeq   int64

		lastDiscoveryLog  time.Time
		discoveryAttempts int
		nextDiscoveryAt   time.Time
		discoverySince    = time.Now()
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
		windowChanged := false

		if transcriptPath == "" {
			if !nextDiscoveryAt.IsZero() && time.Now().Before(nextDiscoveryAt) {
				continue
			}
			transcriptPath = d.resolveExactTranscriptPathForWatcher(w)
			if transcriptPath == "" {
				discoveryAttempts++
				nextDiscoveryAt = time.Now().Add(transcriptDiscoveryDelay(discoveryAttempts))
				if time.Since(discoverySince) >= transcriptDiscoveryGrace {
					if w.setStatus(protocol.SessionMessageWindowStatusUnavailable, "no exact transcript has been discovered for this live session") {
						d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
					}
				}
				if time.Since(lastDiscoveryLog) >= 5*time.Second {
					d.logf("transcript watcher: waiting for transcript session=%s agent=%s cwd=%s attempts=%d", w.sessionID, w.agent, w.cwd, discoveryAttempts)
					lastDiscoveryLog = time.Now()
				}
				continue
			}
			discoveryAttempts = 0
			nextDiscoveryAt = time.Time{}
			info, err := os.Stat(transcriptPath)
			if err != nil {
				d.logf("transcript watcher: transcript stat failed session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be opened", false)
				d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				transcriptPath = ""
				follower = nil
				costFollower = nil
				costFileSize = -1
				discoverySince = time.Now()
				w.behavior.Reset()
				continue
			}
			startOffset := info.Size()
			bootstrapBytes := d.transcriptBootstrapBytesForAgent(w.agent)
			if bootstrapBytes > 0 && info.Size() > bootstrapBytes {
				startOffset = info.Size() - bootstrapBytes
			} else if bootstrapBytes > 0 {
				startOffset = 0
			}
			follower, err = transcript.NewFollower(transcriptPath, string(w.agent), startOffset)
			if err != nil {
				d.logf("transcript watcher: follower init failed session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
				transcriptPath = ""
				continue
			}
			w.behavior.Reset()
			w.resetSource(protocol.SessionMessageWindowStatusReady, transcriptPath, "", startOffset > 0)
			windowChanged = true
			d.logf("transcript watcher: transcript discovered session=%s path=%s offset=%d", w.sessionID, transcriptPath, startOffset)
		}
		if costFollower == nil {
			var costErr error
			costFollower, costErr = d.newSessionCostFollower(w, transcriptPath)
			if costErr != nil {
				d.logf("transcript watcher: cost follower init failed session=%s path=%s err=%v", w.sessionID, transcriptPath, costErr)
			} else {
				costFileSize = -1
			}
		}

		info, err := os.Stat(transcriptPath)
		if err != nil {
			d.logf("transcript watcher: transcript unavailable, rediscovering session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
			w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript became unavailable", false)
			d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
			transcriptPath = ""
			follower = nil
			costFollower = nil
			costFileSize = -1
			discoverySince = time.Now()
			w.behavior.Reset()
			continue
		}

		if info.Size() > 0 && follower != nil {
			batch, readErr := follower.Read()
			if errors.Is(readErr, transcript.ErrCursorMismatch) || errors.Is(readErr, transcript.ErrCursorPastEnd) {
				d.logf("transcript watcher: transcript replaced, rediscovering session=%s path=%s", w.sessionID, transcriptPath)
				w.resetSource(protocol.SessionMessageWindowStatusDiscovering, "", "", false)
				d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				transcriptPath = ""
				follower = nil
				costFollower = nil
				costFileSize = -1
				discoverySince = time.Now()
				w.behavior.Reset()
				continue
			}
			if readErr != nil {
				d.logf("transcript watcher: read delta error session=%s path=%s err=%v", w.sessionID, transcriptPath, readErr)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be read", false)
				d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				transcriptPath = ""
				follower = nil
				costFollower = nil
				costFileSize = -1
				discoverySince = time.Now()
				w.behavior.Reset()
				continue
			}

			for _, record := range batch.Records {
				if len(record.Raw) == 0 {
					continue
				}
				now := time.Now()

				lineResult := w.behavior.HandleLine(record.Raw, now, sessionState)
				if lineResult.Log != "" {
					d.logf("%s session=%s", lineResult.Log, w.sessionID)
				}
				if lineResult.Aborted {
					if skip, reason := staleTranscriptAbort(lineResult.AbortAt, w.startedAt); skip {
						d.logf("transcript watcher: ignoring turn abort session=%s reason=%s abort_at=%s", w.sessionID, reason, lineResult.AbortAt.Format(time.RFC3339Nano))
					} else {
						d.recordTurnAbortedEvidence(w.sessionID, lineResult.AbortDetail, lineResult.AbortAt, now)
						// The halted turn has nothing to classify: what it left on record is
						// a truncated fragment, and a verdict drawn from that describes a
						// question the agent never finished asking. Marking the sequence
						// consumed is what keeps the quiet window below from picking it up.
						classifiedSeq = assistantSeq
					}
				}
				if lineResult.BracketClosed {
					d.recordTurnBracketClosedEvidence(w.sessionID, now)
				}
				if lineResult.State != "" && protocol.SessionState(lineResult.State) != sessionState {
					d.recordTranscriptEvidence(w.sessionID, lineResult.State, "transcript line", now)
					sessionState = protocol.SessionState(lineResult.State)
				}

				for _, event := range record.Events {
					if event.Kind != transcript.EventKindAssistant || strings.TrimSpace(event.Text) == "" {
						continue
					}
					content := event.Text
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
					d.logf("transcript watcher: assistant event session=%s seq=%d chars=%d preview=%q", w.sessionID, assistantSeq, len(content), logMsg)
				}
			}
			windowChanged = w.applyEvents(batch.Events) || windowChanged
		}
		// The existing watcher stat is the movement gate: accounting opens the
		// transcript only when its byte length changed, so an idle session does
		// not double the watcher's recurring file reads.
		if info.Size() > 0 && costFollower != nil && info.Size() != costFileSize {
			batch, readErr := costFollower.Read()
			if readErr != nil {
				d.logf("transcript watcher: cost read failed session=%s path=%s err=%v", w.sessionID, transcriptPath, readErr)
				costFollower = nil
				costFileSize = -1
			} else if err := d.applySessionCostBatch(w, costFollower, batch); err != nil {
				d.logf("transcript watcher: cost persist failed session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
				costFollower = nil
				costFileSize = -1
			} else {
				costFileSize = info.Size()
			}
		}
		if windowChanged {
			d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
		}

		tickResult := w.behavior.Tick(time.Now(), sessionState)
		if tickResult.Log != "" {
			d.logf("%s session=%s", tickResult.Log, w.sessionID)
		}
		if tickResult.State != "" && protocol.SessionState(tickResult.State) != sessionState {
			d.recordTranscriptEvidence(w.sessionID, tickResult.State, "watcher tick", time.Now())
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
			go d.classifySessionState(w.sessionID, transcriptPath)
		}
	}
}

// staleTranscriptAbort decides whether a halt read out of the transcript
// describes this session's life or someone else's.
//
// The watcher re-reads history as a matter of course: it rewinds up to a
// bootstrap window behind the end of the file at discovery, and starts over at
// offset zero whenever a transcript shrinks. A codex session resumed onto an
// existing rollout, or a claude transcript rewritten in place, therefore replays
// old lines — and a halt among them, filed as if it had just happened, settles a
// session that is working. Dating the halt is what tells the two apart, so a halt
// that cannot be dated is not believed at all: losing the feature loudly beats
// settling live sessions on the strength of last week's ESC.
func staleTranscriptAbort(abortAt, sessionStartedAt time.Time) (bool, string) {
	if abortAt.IsZero() {
		return true, "undated"
	}
	if !sessionStartedAt.IsZero() && abortAt.Before(sessionStartedAt) {
		return true, "predates session"
	}
	return false, ""
}
