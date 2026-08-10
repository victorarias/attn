package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// A machine crash kills every worker at once and says nothing about any of
// them. What decides whether a session survives it is the evidence left on
// disk, so these tests are written in those terms: a resume target its driver
// can still find, and a launch intent to start the replacement from.

// recoveryHome points the tool homes at temp dirs once, so one test can give
// several sessions resume targets without each fixture clobbering the last.
type recoveryHome struct {
	claudeProjects string
	codexSessions  string
}

func newRecoveryHome(t *testing.T) recoveryHome {
	t.Helper()
	home, codexHome := t.TempDir(), t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	t.Setenv("CODEX_HOME", codexHome)
	h := recoveryHome{
		claudeProjects: filepath.Join(home, ".claude", "projects", "proj"),
		codexSessions:  filepath.Join(codexHome, "sessions", "2026", "08", "10"),
	}
	for _, dir := range []string{h.claudeProjects, h.codexSessions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return h
}

// resumableClaude writes the transcript `claude --resume <id>` needs.
func (h recoveryHome) resumableClaude(t *testing.T, resumeID string) {
	t.Helper()
	path := filepath.Join(h.claudeProjects, resumeID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write claude transcript for %s: %v", resumeID, err)
	}
}

// resumableCodex writes the rollout `codex resume <id>` needs.
func (h recoveryHome) resumableCodex(t *testing.T, resumeID string) {
	t.Helper()
	rollout := []byte(`{"type":"session_meta","payload":{"id":"` + resumeID + `","cwd":"/tmp"}}` + "\n")
	path := filepath.Join(h.codexSessions, "rollout-"+resumeID+".jsonl")
	if err := os.WriteFile(path, rollout, 0o644); err != nil {
		t.Fatalf("write codex rollout for %s: %v", resumeID, err)
	}
}

// giveRestorationEvidence files the two durable things startup recovery looks
// for: where the conversation is, and how to relaunch into it.
func giveRestorationEvidence(t *testing.T, d *Daemon, sessionID, resumeID string) {
	t.Helper()
	d.store.SetResumeSessionID(sessionID, resumeID)
	giveLaunchIntent(t, d, sessionID)
}

func giveLaunchIntent(t *testing.T, d *Daemon, sessionID string) {
	t.Helper()
	d.store.SetLaunchIntent(sessionID, store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser})
}

func addStaleSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent, state protocol.SessionState) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          agent,
		Directory:      "/tmp/" + id,
		State:          state,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	t.Cleanup(func() { d.store.Remove(id) })
}

func deadWorkerBackend() *fakeWorkerReconcileBackend {
	return &fakeWorkerReconcileBackend{liveIDs: nil, info: map[string]ptybackend.SessionInfo{}}
}

func saveTwoPaneLayout(workspaceID, firstSessionID, secondSessionID string) workspacelayout.WorkspaceLayout {
	pane := func(sessionID string) workspacelayout.Pane {
		return workspacelayout.Pane{
			PaneID:    "pane-" + sessionID,
			RuntimeID: sessionID,
			SessionID: sessionID,
			Kind:      workspacelayout.PaneKindAgent,
			Title:     workspacelayout.DefaultPaneTitle,
		}
	}
	return workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: "pane-" + firstSessionID,
		Layout: workspacelayout.Node{
			Type:      "split",
			SplitID:   "split-" + workspaceID,
			Direction: workspacelayout.DirectionVertical,
			Ratio:     workspacelayout.DefaultSplitRatio,
			Children: []workspacelayout.Node{
				{Type: "pane", PaneID: "pane-" + firstSessionID},
				{Type: "pane", PaneID: "pane-" + secondSessionID},
			},
		},
		Panes: []workspacelayout.Pane{pane(firstSessionID), pane(secondSessionID)},
	}
}

// The defect this replaces: a crash deleted a `working` codex session that had
// a rollout on disk and kept an `idle` one that did not, because recovery asked
// which agent it was and whether it looked busy. Both axes are gone — every
// agent with a resume target survives, in every state.
func TestRecoveryKeepsAnyResumableSessionWhateverItWasDoing(t *testing.T) {
	home := newRecoveryHome(t)
	states := []protocol.SessionState{
		protocol.SessionStateIdle,
		protocol.SessionStateWorking,
		protocol.SessionStateWaitingInput,
		protocol.SessionStatePendingApproval,
	}
	for _, state := range states {
		for _, agent := range []protocol.SessionAgent{protocol.SessionAgentCodex, protocol.SessionAgentClaude} {
			t.Run(string(agent)+"/"+string(state), func(t *testing.T) {
				d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
				id := "crashed-" + string(agent) + "-" + string(state)
				resumeID := "native-" + id
				addStaleSession(t, d, id, agent, state)
				if agent == protocol.SessionAgentCodex {
					home.resumableCodex(t, resumeID)
				} else {
					home.resumableClaude(t, resumeID)
				}
				giveRestorationEvidence(t, d, id, resumeID)
				d.ptyBackend = deadWorkerBackend()

				report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

				if report.MarkedRecoverable != 1 {
					t.Fatalf("marked_recoverable = %d, want 1", report.MarkedRecoverable)
				}
				session := d.store.Get(id)
				if session == nil {
					t.Fatal("a resumable session was deleted by startup recovery")
				}
				if session.State != protocol.SessionStateRecoverable {
					t.Fatalf("state = %q, want recoverable", session.State)
				}
			})
		}
	}
}

// The other half of the same rule: nothing to come back to means the row goes,
// and it goes the one way a row ever goes — reaped, with its pane. A session
// left sitting in `recoverable` with a resume id pointing at nothing would
// offer the user a Reload that cannot work.
func TestRecoveryReapsWhatItCannotBringBack(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, d *Daemon, home recoveryHome, id string)
	}{
		{
			name: "resume target never existed",
			setup: func(t *testing.T, d *Daemon, _ recoveryHome, id string) {
				giveRestorationEvidence(t, d, id, "native-"+id)
			},
		},
		{
			name: "resume target is gone from disk",
			setup: func(t *testing.T, d *Daemon, home recoveryHome, id string) {
				home.resumableCodex(t, "native-"+id)
				giveRestorationEvidence(t, d, id, "native-"+id)
				if err := os.RemoveAll(home.codexSessions); err != nil {
					t.Fatalf("remove rollouts: %v", err)
				}
			},
		},
		{
			name: "no launch intent to relaunch from",
			setup: func(t *testing.T, d *Daemon, home recoveryHome, id string) {
				home.resumableCodex(t, "native-"+id)
				d.store.SetResumeSessionID(id, "native-"+id)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := newRecoveryHome(t)
			d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
			id := "unrecoverable"
			addStaleSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
			tc.setup(t, d, home, id)
			d.ptyBackend = deadWorkerBackend()

			report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

			if report.Reaped != 1 {
				t.Fatalf("reaped = %d, want 1", report.Reaped)
			}
			if session := d.store.Get(id); session != nil {
				t.Fatalf("session = %+v, want reaped: there is nothing to bring it back to", session)
			}
		})
	}
}

// A verdict is only as good as the evidence behind it right now. A session
// parked in `recoverable` whose rollout has since been pruned is not a session
// waiting to be reloaded, it is a dead row, and skipping it because of the
// state it is already in would keep it forever.
func TestRecoveryRedecidesSessionsAlreadyParkedAsRecoverable(t *testing.T) {
	home := newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addStaleSession(t, d, "still-there", protocol.SessionAgentCodex, protocol.SessionStateRecoverable)
	addStaleSession(t, d, "target-pruned", protocol.SessionAgentCodex, protocol.SessionStateRecoverable)
	home.resumableCodex(t, "native-still-there")
	giveRestorationEvidence(t, d, "still-there", "native-still-there")
	giveRestorationEvidence(t, d, "target-pruned", "native-target-pruned")
	d.ptyBackend = deadWorkerBackend()

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if session := d.store.Get("still-there"); session == nil || session.State != protocol.SessionStateRecoverable {
		t.Fatalf("still-there = %+v, want left recoverable", session)
	}
	// It was already there, so nothing moved and nothing is announced.
	if report.MarkedRecoverable != 0 {
		t.Fatalf("marked_recoverable = %d, want 0 for a session already parked there", report.MarkedRecoverable)
	}
	if session := d.store.Get("target-pruned"); session != nil {
		t.Fatalf("target-pruned = %+v, want reaped once its rollout was gone", session)
	}
}

// A close the user asked for is not a crash. The mark outlives the daemon
// precisely so a reap that runs on the next boot can still tell the two apart.
func TestRecoveryDoesNotResurrectAnIntentionalClose(t *testing.T) {
	home := newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addStaleSession(t, d, "closed-on-purpose", protocol.SessionAgentCodex, protocol.SessionStateWorking)
	home.resumableCodex(t, "native-closed-on-purpose")
	giveRestorationEvidence(t, d, "closed-on-purpose", "native-closed-on-purpose")
	d.store.MarkSessionIntentionalClose("closed-on-purpose", time.Now())
	d.ptyBackend = deadWorkerBackend()

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if session := d.store.Get("closed-on-purpose"); session != nil {
		t.Fatalf("session = %+v, want gone: the user already dismissed it", session)
	}
}

// A shell holds no conversation, so there is nothing to check for and nothing
// to lose: the intent describes it completely and the pane comes back.
func TestRecoveryKeepsShellPanes(t *testing.T) {
	newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addStaleSession(t, d, "utility-shell", protocol.SessionAgentShell, protocol.SessionStateIdle)
	giveLaunchIntent(t, d, "utility-shell")
	d.ptyBackend = deadWorkerBackend()

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	session := d.store.Get("utility-shell")
	if session == nil || session.State != protocol.SessionStateRecoverable {
		t.Fatalf("session = %+v, want recoverable", session)
	}
}

// A plugin runtime is judged on the handle it persisted, not on what a
// reconnected driver says it can do in general: the capability is about the
// driver, the handle is about this conversation. At startup the plugin has
// usually not reconnected anyway.
func TestRecoveryJudgesPluginSessionsOnTheirPersistedHandle(t *testing.T) {
	newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	addStaleSession(t, d, "plugin-with-handle", "snipe", protocol.SessionStateWorking)
	giveLaunchIntent(t, d, "plugin-with-handle")
	if !d.store.BeginAgentDriverRun("plugin-with-handle", "snipe-plugin", "run-handle") {
		t.Fatal("BeginAgentDriverRun(plugin-with-handle) failed")
	}
	if !d.store.ApplyAgentDriverMetadata("plugin-with-handle", "run-handle", 1, `{"native_id":"resume-me"}`) {
		t.Fatal("ApplyAgentDriverMetadata(plugin-with-handle) failed")
	}
	d.store.EndAgentDriverRun("plugin-with-handle")

	addStaleSession(t, d, "plugin-capability-only", "snipe-live", protocol.SessionStateWorking)
	giveLaunchIntent(t, d, "plugin-capability-only")
	plugin := &pluginConnection{name: "snipe-live-plugin"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        "snipe-live",
		Capabilities: map[string]bool{"resume": true},
	}); err != nil {
		t.Fatalf("register resumable driver: %v", err)
	}

	d.ptyBackend = deadWorkerBackend()
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	if session := d.store.Get("plugin-with-handle"); session == nil || session.State != protocol.SessionStateRecoverable {
		t.Fatalf("plugin-with-handle = %+v, want recoverable", session)
	}
	if session := d.store.Get("plugin-capability-only"); session != nil {
		t.Fatalf("plugin-capability-only = %+v, want reaped: nothing names the conversation", session)
	}
}

// A conversation session's history is a file under attn's data dir, and before
// the host has written one the intent's own brief is what the replacement
// opens. Neither needs the plugin to be back.
func TestRecoveryKeepsConversationSessionsFromTheirOwnHistory(t *testing.T) {
	newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	addStaleSession(t, d, "conv-with-history", "nisse", protocol.SessionStateIdle)
	giveLaunchIntent(t, d, "conv-with-history")
	stateDir := hostSessionStateDir("conv-with-history")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir host state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	if err := os.WriteFile(filepath.Join(stateDir, "session.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write host session file: %v", err)
	}

	addStaleSession(t, d, "conv-crashed-early", "nisse", protocol.SessionStateWorking)
	d.store.SetLaunchIntent("conv-crashed-early", store.LaunchIntent{
		ApprovalRoute: launchcontract.ApprovalRouteUser,
		InitialPrompt: "review the crash recovery plan",
	})

	addStaleSession(t, d, "conv-empty", "nisse", protocol.SessionStateWorking)
	giveLaunchIntent(t, d, "conv-empty")

	d.ptyBackend = deadWorkerBackend()
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	for _, id := range []string{"conv-with-history", "conv-crashed-early"} {
		if session := d.store.Get(id); session == nil || session.State != protocol.SessionStateRecoverable {
			t.Fatalf("%s = %+v, want recoverable", id, session)
		}
	}
	if session := d.store.Get("conv-empty"); session != nil {
		t.Fatalf("conv-empty = %+v, want reaped: no history and no brief to reopen", session)
	}
}

// The layout follows the verdict: a recoverable session keeps its pane, so the
// user has somewhere to click Reload, and only the reaped one takes its pane
// down with it.
func TestRecoveryKeepsThePaneOfARecoverableSession(t *testing.T) {
	home := newRecoveryHome(t)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	workspaceID := "ws-crash"
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "crash", Directory: "/tmp/crash",
	})
	for _, id := range []string{"pane-keeps", "pane-goes"} {
		addStaleSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
		d.associateSessionWithWorkspace(id, workspaceID)
	}
	home.resumableCodex(t, "native-pane-keeps")
	giveRestorationEvidence(t, d, "pane-keeps", "native-pane-keeps")
	giveRestorationEvidence(t, d, "pane-goes", "native-pane-goes")
	if err := d.store.SaveWorkspaceLayout(saveTwoPaneLayout(workspaceID, "pane-keeps", "pane-goes")); err != nil {
		t.Fatalf("SaveWorkspaceLayout: %v", err)
	}

	d.ptyBackend = deadWorkerBackend()
	d.reconcileSessionsWithWorkerBackend(context.Background(), true, time.Time{})

	layout := d.store.GetWorkspaceLayout(workspaceID)
	if layout == nil {
		t.Fatal("workspace layout was removed with the reaped session")
	}
	sessions := map[string]bool{}
	for _, pane := range layout.Panes {
		sessions[pane.SessionID] = true
	}
	if !sessions["pane-keeps"] {
		t.Fatalf("recoverable session lost its pane; layout panes = %+v", layout.Panes)
	}
	if sessions["pane-goes"] {
		t.Fatalf("reaped session kept its pane; layout panes = %+v", layout.Panes)
	}
}
