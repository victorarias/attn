package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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

	d.longRunMu.Lock()
	tracked := !d.longRun[sessionID].workingSince.IsZero()
	d.longRunMu.Unlock()
	if !tracked {
		t.Fatal("working state did not start long-run tracking")
	}

	events := capture.snapshot()
	if got := characterizationEventCount(events, protocol.EventSessionStateChanged, sessionID); got != 1 {
		t.Fatalf("session_state_changed events=%d, want 1; events=%+v", got, events)
	}
	if got := characterizationEventCount(events, protocol.EventWorkspaceStateChanged, ""); got != 1 {
		t.Fatalf("workspace_state_changed events=%d, want 1; events=%+v", got, events)
	}
}

func TestSessionStateCharacterization_LiveSignalsShareEffects(t *testing.T) {
	for _, tc := range []struct {
		name         string
		agent        protocol.SessionAgent
		initialState protocol.SessionState
		apply        func(*Daemon, string)
	}{
		{
			name:         "hook",
			agent:        protocol.SessionAgentCodex,
			initialState: protocol.SessionStateIdle,
			apply: func(d *Daemon, id string) {
				d.handleState(&syncConn{}, &protocol.StateMessage{ID: id, State: protocol.StateWorking})
			},
		},
		{
			name:         "trusted PTY",
			agent:        protocol.SessionAgentClaude,
			initialState: protocol.SessionStateWaitingInput,
			apply: func(d *Daemon, id string) {
				d.handlePTYState(id, protocol.StateWorking)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
			sessionID := "session-" + tc.name
			addCharacterizationSession(t, d, sessionID, tc.agent, tc.initialState)
			capture := captureBroadcasts(d)

			tc.apply(d, sessionID)

			assertCharacterizationLiveEffects(t, d, capture, sessionID)
		})
	}
}

func TestSessionStateCharacterization_DaemonObservationDoesNotTouch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	sessionID := "long-run-handoff"
	addCharacterizationSession(t, d, sessionID, protocol.SessionAgentCodex, protocol.SessionStateWorking)
	d.longRun[sessionID] = longRunSession{workingSince: time.Now().Add(-longRunReviewThreshold - time.Minute)}
	capture := captureBroadcasts(d)

	d.classifyOrDeferAfterStop(sessionID, "/tmp/characterization-transcript.jsonl")

	session := d.store.Get(sessionID)
	if session == nil || session.State != protocol.SessionStateWaitingInput {
		t.Fatalf("session=%+v, want waiting_input", session)
	}
	if session.LastSeen != characterizationOldTimestamp {
		t.Fatalf("daemon observation touched LastSeen=%q, want %q", session.LastSeen, characterizationOldTimestamp)
	}
	if !d.sessionNeedsReviewAfterLongRun(sessionID) {
		t.Fatal("long-run handoff did not preserve needs-review tracking")
	}
	events := capture.snapshot()
	if got := characterizationEventCount(events, protocol.EventSessionStateChanged, sessionID); got != 1 {
		t.Fatalf("session_state_changed events=%d, want 1; events=%+v", got, events)
	}
	if got := characterizationEventCount(events, protocol.EventWorkspaceStateChanged, ""); got != 1 {
		t.Fatalf("workspace_state_changed events=%d, want 1; events=%+v", got, events)
	}
}

func TestSessionStateCharacterization_StaleClassifierHasNoEffects(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "state.sock"))
	sessionID := "stale-classifier"
	addCharacterizationSession(t, d, sessionID, protocol.SessionAgentCodex, protocol.SessionStateWorking)
	d.longRun[sessionID] = longRunSession{
		workingSince:       time.Now().Add(-longRunReviewThreshold - time.Minute),
		deferredTranscript: "/tmp/deferred.jsonl",
		needsReview:        true,
	}
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
	fresh := d.store.Get(sessionID)
	stateEventsBeforeRelease := characterizationEventCount(capture.snapshot(), protocol.EventSessionStateChanged, sessionID)
	close(classifier.release)
	select {
	case <-classified:
	case <-time.After(2 * time.Second):
		t.Fatal("classifier did not finish")
	}

	after := d.store.Get(sessionID)
	if after == nil || after.State != protocol.SessionStatePendingApproval {
		t.Fatalf("session=%+v, stale classifier overwrote pending_approval", after)
	}
	if after.StateUpdatedAt != fresh.StateUpdatedAt || after.LastSeen != fresh.LastSeen {
		t.Fatalf("stale classifier changed timestamps: fresh=%+v after=%+v", fresh, after)
	}
	if !d.sessionNeedsReviewAfterLongRun(sessionID) {
		t.Fatal("stale classifier cleared long-run review tracking")
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
	d.longRunMu.Lock()
	tracked := !d.longRun[sessionID].workingSince.IsZero()
	d.longRunMu.Unlock()
	if !tracked {
		t.Fatal("accepted plugin working report did not start long-run tracking")
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
	d.longRun[sessionID] = longRunSession{workingSince: time.Now().Add(-time.Minute)}
	capture := captureBroadcasts(d)

	d.handlePTYExit(ptybackend.ExitInfo{ID: sessionID, ExitCode: 0})

	session := d.store.Get(sessionID)
	if session == nil || session.State != protocol.SessionStateIdle {
		t.Fatalf("session=%+v, want idle after exit", session)
	}
	if session.LastSeen == characterizationOldTimestamp {
		t.Fatal("process exit did not Touch the session")
	}
	d.longRunMu.Lock()
	_, tracked := d.longRun[sessionID]
	d.longRunMu.Unlock()
	if tracked {
		t.Fatal("process exit did not clear long-run tracking")
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
