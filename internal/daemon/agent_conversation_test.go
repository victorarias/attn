package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestObserveAgentConversationTransitionsOnceAndRebindsRuntime(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)
	cwd := "/repo/project"
	now := time.Now()
	oldTranscript := writeCodexInteractiveRollout(t, codexHome, "codex-old", cwd, now.Add(-time.Minute))
	newTranscript := writeCodexInteractiveRollout(t, codexHome, "codex-new", cwd, now)

	d.store.Add(&protocol.Session{
		ID:        "session-1",
		Label:     "session-1",
		Agent:     protocol.SessionAgentCodex,
		Directory: cwd,
		State:     protocol.SessionStateWorking,
	})
	d.store.SetResumeSessionID("session-1", "codex-old")
	d.store.UpdateSessionActivity("session-1", "working in the old conversation", now, "old-cursor")
	d.noteSessionActivityRun("session-1", func(run *sessionActivityRun) {
		run.Transcript = oldTranscript
		run.ResumeID = "codex-old"
	})
	d.startTranscriptWatcherAtPath("session-1", protocol.SessionAgentCodex, cwd, now.Add(-time.Minute), oldTranscript)

	d.watchersMu.Lock()
	oldWatcher := d.transcriptWatch["session-1"]
	d.watchersMu.Unlock()
	if oldWatcher == nil {
		t.Fatal("old transcript watcher was not started")
	}

	var pushed []*protocol.WebSocketEvent
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) { pushed = append(pushed, event) }

	d.observeAgentConversation(agentConversationObservation{
		SessionID:      "session-1",
		NativeID:       "codex-new",
		TranscriptPath: newTranscript,
	})

	select {
	case <-oldWatcher.doneCh:
	case <-time.After(time.Second):
		t.Fatal("old transcript watcher did not stop after the transition")
	}

	if got := d.store.GetResumeSessionID("session-1"); got != "codex-new" {
		t.Fatalf("conversation binding = %q, want codex-new", got)
	}
	if got := d.store.GetSessionActivity("session-1"); got.Line != "" || got.Cursor != "" {
		t.Fatalf("old activity survived transition: %+v", got)
	}
	if got := d.sessionActivityRunRecord("session-1"); got != (sessionActivityRun{}) {
		t.Fatalf("old activity runtime survived transition: %+v", got)
	}

	d.watchersMu.Lock()
	newWatcher := d.transcriptWatch["session-1"]
	d.watchersMu.Unlock()
	if newWatcher == nil || newWatcher == oldWatcher {
		t.Fatalf("transcript watcher was not rebound: old=%p new=%p", oldWatcher, newWatcher)
	}
	if newWatcher.preferredPath != newTranscript {
		t.Fatalf("watcher preferred path = %q, want %q", newWatcher.preferredPath, newTranscript)
	}
	t.Cleanup(func() {
		d.stopTranscriptWatcher("session-1")
		<-newWatcher.doneCh
	})

	logged, err := d.store.BusEventsSince(0, 10)
	if err != nil {
		t.Fatalf("BusEventsSince: %v", err)
	}
	if len(logged) != 1 || logged[0].Name != FactSessionConversationChanged || logged[0].Subject != "session-1" {
		t.Fatalf("conversation facts = %+v, want one transition", logged)
	}
	foundSnapshot := false
	for _, event := range pushed {
		if event.Event == protocol.EventSessionStateChanged && event.Session != nil && event.Session.ID == "session-1" {
			foundSnapshot = event.Session.Activity == nil
		}
	}
	if !foundSnapshot {
		t.Fatalf("no refreshed session snapshot cleared the activity: %+v", pushed)
	}

	// SessionStart can be observed more than once. Repetition is not another
	// transition and must not restart runtime state or append another fact.
	d.observeAgentConversation(agentConversationObservation{
		SessionID:      "session-1",
		NativeID:       "codex-new",
		TranscriptPath: newTranscript,
	})
	d.watchersMu.Lock()
	repeatedWatcher := d.transcriptWatch["session-1"]
	d.watchersMu.Unlock()
	if repeatedWatcher != newWatcher {
		t.Fatal("repeated observation restarted the transcript watcher")
	}
	logged, err = d.store.BusEventsSince(0, 10)
	if err != nil || len(logged) != 1 {
		t.Fatalf("repeated observation appended a fact: events=%+v err=%v", logged, err)
	}
}

func TestTranscriptWatcherPrefersPersistedNativeConversationAfterRestart(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	cwd := "/repo/project"
	now := time.Now()
	want := writeCodexInteractiveRollout(t, codexHome, "codex-current", cwd, now.Add(-time.Minute))
	_ = writeCodexInteractiveRollout(t, codexHome, "codex-neighbor", cwd, now)

	d.store.Add(&protocol.Session{ID: "session-1", Agent: protocol.SessionAgentCodex, Directory: cwd})
	d.store.SetResumeSessionID("session-1", "codex-current")
	watcher := &transcriptWatcher{
		sessionID: "session-1",
		agent:     protocol.SessionAgentCodex,
		cwd:       cwd,
		startedAt: now,
	}
	if got := d.resolveExactTranscriptPathForWatcher(watcher); got != want {
		t.Fatalf("restart transcript = %q, want persisted conversation %q", got, want)
	}
}
