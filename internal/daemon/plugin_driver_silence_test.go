package daemon

import (
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
)

func seedDriverRun(t *testing.T, d *Daemon, sessionID, pluginName, runID string, state protocol.SessionState) {
	t.Helper()
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID:             sessionID,
		Label:          "snipe",
		Agent:          "snipe",
		Directory:      t.TempDir(),
		State:          state,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun(sessionID, pluginName, runID) {
		t.Fatalf("BeginAgentDriverRun(%s) failed", sessionID)
	}
}

func TestHandlePTYState_VetoNamesTheDriverThatHasNotRegisteredYet(t *testing.T) {
	d := newTraceDaemon(t)
	seedDriverRun(t, d, "unregistered-driver", "snipe-plugin", "run-1", protocol.SessionStateIdle)

	// The watch-subscribe replay a daemon restart fires beats driver.register by about a
	// second on a live pi session.
	d.handlePTYState("unregistered-driver", pty.Observation{
		Source: pty.SourceWorkerInfo,
		Claim:  protocol.StateWorking,
		Detail: "worker info",
		At:     time.Now(),
	})

	if got := d.store.Get("unregistered-driver").State; got != protocol.SessionStateIdle {
		t.Fatalf("state=%q after replay, want the driver's own idle", got)
	}
	got := onlyObservation(t, d, "unregistered-driver")
	if got.Reason != "plugin_driver_not_registered" {
		t.Fatalf("veto reason=%q, want plugin_driver_not_registered", got.Reason)
	}
}

func TestPluginDriverSilence_DeclaresUnknownWhenNobodySpeaksForTheRun(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		d.pluginDriverSilenceGraceOverride = time.Minute
		seedDriverRun(t, d, "gone-driver", "snipe-plugin", "run-1", protocol.SessionStateWorking)

		d.armPluginDriverSilenceWatch("snipe-plugin")
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if got := d.store.Get("gone-driver").State; got != protocol.SessionStateWorking {
			t.Fatalf("state=%q inside the grace, want the declaration left alone", got)
		}

		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := d.store.Get("gone-driver").State; got != protocol.SessionStateUnknown {
			t.Fatalf("state=%q past the grace, want unknown", got)
		}
	})
}

func TestPluginDriverSilence_ArmsRunsWhosePluginIsNotEvenInstalled(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		d.pluginDriverSilenceGraceOverride = time.Minute
		seedDriverRun(t, d, "orphaned", "removed-plugin", "run-1", protocol.SessionStateWorking)

		d.armPluginDriverSilenceWatchForEveryRun()
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if got := d.store.Get("orphaned").State; got != protocol.SessionStateUnknown {
			t.Fatalf("state=%q, want unknown", got)
		}
	})
}

func TestPluginDriverSilence_AnyReportFromTheDriverDisarmsIt(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		d.pluginDriverSilenceGraceOverride = time.Minute
		seedDriverRun(t, d, "live-driver", "snipe-plugin", "run-1", protocol.SessionStateWorking)

		d.armPluginDriverSilenceWatch("snipe-plugin")
		d.notePluginDriverReport("live-driver")

		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if got := d.store.Get("live-driver").State; got != protocol.SessionStateWorking {
			t.Fatalf("state=%q after a driver spoke, want working", got)
		}
	})
}

func TestPluginDriverSilence_LeavesAloneWhatNoDriverDeclares(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		d.pluginDriverSilenceGraceOverride = time.Minute
		seedDriverRun(t, d, "recoverable-driver", "snipe-plugin", "run-1", protocol.SessionStateRecoverable)

		d.armPluginDriverSilenceWatch("snipe-plugin")
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if got := d.store.Get("recoverable-driver").State; got != protocol.SessionStateRecoverable {
			t.Fatalf("state=%q, want recoverable left alone", got)
		}
	})
}

func TestPluginDriverSilence_ARelaunchedRunOutranksTheOldAlarm(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		d.pluginDriverSilenceGraceOverride = time.Minute
		seedDriverRun(t, d, "relaunched", "snipe-plugin", "run-1", protocol.SessionStateWorking)

		d.armPluginDriverSilenceWatch("snipe-plugin")
		d.store.EndAgentDriverRun("relaunched")
		if !d.store.BeginAgentDriverRun("relaunched", "snipe-plugin", "run-2") {
			t.Fatal("BeginAgentDriverRun(run-2) failed")
		}

		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if got := d.store.Get("relaunched").State; got != protocol.SessionStateWorking {
			t.Fatalf("state=%q, want the new run's working", got)
		}
	})
}

func TestPluginDriverSilence_ClosedSessionCancelsTheAlarm(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		d.pluginDriverSilenceGraceOverride = time.Minute
		seedDriverRun(t, d, "closed-driver", "snipe-plugin", "run-1", protocol.SessionStateWorking)

		d.armPluginDriverSilenceWatch("snipe-plugin")
		d.closeSession("closed-driver", store.SessionClose{By: store.SessionClosedByUser})
		if d.pluginDriverSilence().disarm("closed-driver") {
			t.Fatal("alarm still pending for a session that is gone")
		}

		time.Sleep(5 * time.Minute)
		synctest.Wait()
	})
}

// A reconnect must not restamp `state_since` and re-open a settled turn.
func TestPluginReportedState_OnlyIfUnknownRestatesNothingElse(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	seedDriverRun(t, d, "restated", "snipe-plugin", "run-1", protocol.SessionStateUnknown)
	seedDriverRun(t, d, "settled", "snipe-plugin", "run-2", protocol.SessionStateIdle)
	settledBefore := d.store.Get("settled").StateSince

	d.applyPluginReportedState(pluginReportStateParams{
		SessionID: "restated", RunID: "run-1", Seq: 1, State: protocol.StateWorking, OnlyIfUnknown: true,
	})
	d.applyPluginReportedState(pluginReportStateParams{
		SessionID: "settled", RunID: "run-2", Seq: 1, State: protocol.StateIdle, OnlyIfUnknown: true,
	})

	if got := d.store.Get("restated").State; got != protocol.SessionStateWorking {
		t.Fatalf("state=%q for the session attn could not tell about, want working", got)
	}
	if got := d.store.Get("settled").StateSince; got != settledBefore {
		t.Fatalf("state_since moved to %q on a session attn already knew about", got)
	}
}

func TestPluginReportedState_OnlyIfUnknownStillDisarmsTheAlarm(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		d.pluginDriverSilenceGraceOverride = time.Minute
		seedDriverRun(t, d, "reconnected", "snipe-plugin", "run-1", protocol.SessionStateWorking)

		d.armPluginDriverSilenceWatch("snipe-plugin")
		d.applyPluginReportedState(pluginReportStateParams{
			SessionID: "reconnected", RunID: "run-1", Seq: 1, State: protocol.StateIdle, OnlyIfUnknown: true,
		})

		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if got := d.store.Get("reconnected").State; got != protocol.SessionStateWorking {
			t.Fatalf("state=%q, want the declaration the driver is still backing", got)
		}
	})
}
