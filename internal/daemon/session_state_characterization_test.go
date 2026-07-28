package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

const characterizationOldTimestamp = "2000-01-01T00:00:00Z"

func addCharacterizationSession(
	t *testing.T,
	d *Daemon,
	id string,
	agent protocol.SessionAgent,
	state protocol.SessionState,
) string {
	t.Helper()
	directory := t.TempDir()
	workspaceID := "workspace-" + id
	addTestWorkspace(d, workspaceID, directory)
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          agent,
		Directory:      directory,
		State:          state,
		StateSince:     characterizationOldTimestamp,
		StateUpdatedAt: characterizationOldTimestamp,
		LastSeen:       characterizationOldTimestamp,
	})
	d.associateSessionWithWorkspace(id, workspaceID)
	return workspaceID
}

func characterizationEventCount(events []protocol.WebSocketEvent, eventName, sessionID string) int {
	count := 0
	for _, event := range events {
		if event.Event != eventName {
			continue
		}
		if sessionID != "" && (event.Session == nil || event.Session.ID != sessionID) {
			continue
		}
		count++
	}
	return count
}

func assertCharacterizationLiveEffects(t *testing.T, d *Daemon, capture *broadcastCapture, sessionID string) {
	t.Helper()
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatal("session missing after state transition")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("state=%q, want working", session.State)
	}
	if session.StateUpdatedAt == characterizationOldTimestamp || session.StateSince == characterizationOldTimestamp {
		t.Fatalf("state timestamps were not refreshed: since=%q updated=%q", session.StateSince, session.StateUpdatedAt)
	}
	if session.LastSeen == characterizationOldTimestamp {
		t.Fatal("live state signal did not Touch the session")
	}

	events := capture.snapshot()
	if got := characterizationEventCount(events, protocol.EventSessionStateChanged, sessionID); got != 1 {
		t.Fatalf("session_state_changed events=%d, want 1; events=%+v", got, events)
	}
	if got := characterizationEventCount(events, protocol.EventWorkspaceStateChanged, ""); got != 1 {
		t.Fatalf("workspace_state_changed events=%d, want 1; events=%+v", got, events)
	}
}

// The worker poll is the last live signal: the one claim from outside the
// resolver that still commits, because it is what ends `launching`.
func TestSessionStateCharacterization_TheWorkerPollIsTheLastLiveSignal(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	sessionID := "session-worker-poll"
	addCharacterizationSession(t, d, sessionID, protocol.SessionAgentClaude, protocol.SessionStateLaunching)
	capture := captureBroadcasts(d)

	d.handlePTYState(sessionID, pty.Observation{
		Source: pty.SourceWorkerInfo,
		Claim:  protocol.StateWorking,
		Detail: "test",
		At:     time.Now(),
	})

	assertCharacterizationLiveEffects(t, d, capture, sessionID)
}

// A hook reports evidence. It refreshes LastSeen — a hook firing is proof the
// session is alive, which is the one thing the reaper reads — and changes nothing
// else until the resolver's tick.
func TestSessionStateCharacterization_AHookOnlyFilesEvidence(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	sessionID := "session-hook"
	addCharacterizationSession(t, d, sessionID, protocol.SessionAgentCodex, protocol.SessionStateIdle)
	capture := captureBroadcasts(d)

	d.handleState(&syncConn{}, &protocol.StateMessage{ID: sessionID, State: protocol.StateWorking})

	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatal("session missing")
	}
	if session.State != protocol.SessionStateIdle {
		t.Fatalf("state=%q: the hook applied a state instead of filing it", session.State)
	}
	if session.LastSeen == characterizationOldTimestamp {
		t.Fatal("a hook firing did not Touch the session")
	}
	if events := capture.snapshot(); characterizationEventCount(events, protocol.EventSessionStateChanged, sessionID) != 0 {
		t.Fatalf("filing evidence broadcast a state change: %+v", events)
	}

	// And the resolver is what turns it into a color.
	d.resolveAllSessions(time.Now())
	if state := d.store.Get(sessionID).State; state != protocol.SessionStateWorking {
		t.Fatalf("state=%q after the tick, want working", state)
	}
}

func TestSessionStateCharacterization_ALateVerdictDoesNotOverwriteAnApproval(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	sessionID := "stale-classifier"
	addCharacterizationSession(t, d, sessionID, protocol.SessionAgentCodex, protocol.SessionStateWorking)
	classifier := newBlockingClassifier(protocol.StateIdle)
	d.classifier = classifier

	transcriptPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := `{"type":"assistant","message":{"role":"assistant","content":"Finished."}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	capture := captureBroadcasts(d)

	classified := make(chan struct{})
	go func() {
		d.classifySessionState(sessionID, transcriptPath)
		close(classified)
	}()

	select {
	case <-classifier.started:
	case <-time.After(2 * time.Second):
		close(classifier.release)
		t.Fatal("classifier did not start")
	}

	d.handleState(&syncConn{}, &protocol.StateMessage{ID: sessionID, State: protocol.StatePendingApproval})
	d.resolveAllSessions(time.Now())
	fresh := d.store.Get(sessionID)
	if fresh.State != protocol.SessionStatePendingApproval {
		t.Fatalf("state=%q before the verdict lands; the rest proves nothing", fresh.State)
	}
	stateEventsBeforeRelease := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID)
	close(classifier.release)
	select {
	case <-classified:
	case <-time.After(2 * time.Second):
		t.Fatal("classifier did not finish")
	}

	d.resolveAllSessions(time.Now())

	after := d.store.Get(sessionID)
	if after == nil || after.State != protocol.SessionStatePendingApproval {
		t.Fatalf("session=%+v, the late verdict overwrote pending_approval", after)
	}
	if after.StateUpdatedAt != fresh.StateUpdatedAt || after.LastSeen != fresh.LastSeen {
		t.Fatalf("stale classifier changed timestamps: fresh=%+v after=%+v", fresh, after)
	}
	if got := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID); got != stateEventsBeforeRelease {
		t.Fatalf("stale classifier emitted state event: before=%d after=%d", stateEventsBeforeRelease, got)
	}
}

func TestSessionStateCharacterization_PluginCASGatesEffects(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	sessionID := "plugin-state"
	addCharacterizationSession(t, d, sessionID, "snipe", protocol.SessionStateLaunching)
	if !d.store.BeginAgentDriverRun(sessionID, "snipe-plugin", "run-current") {
		t.Fatal("failed to begin plugin run")
	}
	capture := captureBroadcasts(d)

	if !d.applyPluginReportedState(pluginReportStateParams{
		SessionID: sessionID,
		RunID:     "run-current",
		Seq:       2,
		State:     protocol.StateWorking,
	}) {
		t.Fatal("fresh plugin report was rejected")
	}
	accepted := d.store.Get(sessionID)
	if accepted == nil || accepted.State != protocol.SessionStateWorking {
		t.Fatalf("session=%+v, want working", accepted)
	}
	if accepted.LastSeen == characterizationOldTimestamp {
		t.Fatal("accepted plugin report did not Touch the session")
	}
	stateEventsAfterAccepted := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID)

	if d.applyPluginReportedState(pluginReportStateParams{
		SessionID: sessionID,
		RunID:     "run-current",
		Seq:       1,
		State:     protocol.StateIdle,
	}) {
		t.Fatal("stale plugin report was accepted")
	}
	afterStale := d.store.Get(sessionID)
	if afterStale == nil || afterStale.State != protocol.SessionStateWorking || afterStale.StateUpdatedAt != accepted.StateUpdatedAt || afterStale.LastSeen != accepted.LastSeen {
		t.Fatalf("stale plugin report changed session: accepted=%+v after=%+v", accepted, afterStale)
	}
	if got := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID); got != stateEventsAfterAccepted {
		t.Fatalf("stale plugin report emitted state event: before=%d after=%d", stateEventsAfterAccepted, got)
	}
}

func TestSessionStateCharacterization_ProcessExitEffects(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	sessionID := "process-exit"
	addCharacterizationSession(t, d, sessionID, protocol.SessionAgentClaude, protocol.SessionStateWorking)
	capture := captureBroadcasts(d)

	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 0})
	d.resolveAllSessions(time.Now())

	session := d.store.Get(sessionID)
	if session == nil || session.State != protocol.SessionStateIdle {
		t.Fatalf("session=%+v, want idle after exit", session)
	}
	events := capture.snapshot()
	if got := characterizationEventCount(events, protocol.EventSessionStateChanged, sessionID); got != 1 {
		t.Fatalf("session_state_changed events=%d, want 1; events=%+v", got, events)
	}
	if got := characterizationEventCount(events, protocol.EventWorkspaceStateChanged, ""); got != 1 {
		t.Fatalf("workspace_state_changed events=%d, want 1; events=%+v", got, events)
	}
	if got := characterizationEventCount(events, protocol.EventSessionExited, ""); got != 1 {
		t.Fatalf("session_exited events=%d, want 1; events=%+v", got, events)
	}
}

// evidenceObs builds a title-glyph observation. The heartbeat is the only PTY
// source that is evidence rather than a writer, so it is what these tests use to
// exercise the daemon's handling of one; its claims are levels in its own
// vocabulary ("busy"), not protocol state names.
func evidenceObs(claim string) pty.Observation {
	return pty.Observation{
		Source: pty.SourceHeartbeat,
		Claim:  claim,
		Detail: "test",
		At:     time.Now(),
	}
}
