package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// Slice 4: a conversation survives its host. Everything here is about the two
// moments that used to lose one — the host dying, and the daemon coming back up
// without it.

// registerConversationDriver registers a driver shaped like attn-pi's: it
// declares `conversation` and deliberately NOT `resume`, which is the PTY
// agents' capability and the one the recovery rules used to key on.
func registerConversationDriver(t *testing.T, d *Daemon, agent string) {
	t.Helper()
	plugin := &pluginConnection{name: agent + "-plugin"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        agent,
		Capabilities: map[string]bool{pluginDriverConversationCapability: true, "state_reporting": true},
	}); err != nil {
		t.Fatalf("register conversation driver: %v", err)
	}
}

// A host that died left the whole conversation behind in its session file, so
// the session is not finished — it is waiting. `recoverable` is what puts the
// way back in front of the user instead of a dead pane.
func TestHostExitMakesTheSessionRecoverable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-r1")
	declare(d, "conv-r1", 1, "run_started", protocol.StateWorking)

	d.handleHostExit(hostsession.ExitInfo{SessionID: "conv-r1", ExitCode: -1, Signal: "SIGKILL", LifecycleID: "run-conv-r1"})

	if got := stateOf(t, d, "conv-r1"); got != string(protocol.SessionStateRecoverable) {
		t.Fatalf("state after the host was killed = %q, want recoverable", got)
	}
}

// A reload owns its own teardown: the kill it performs must not broadcast an
// exit or park the session it is in the middle of bringing back.
func TestHostExitDuringAReloadIsSuppressed(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-r2")
	declare(d, "conv-r2", 1, "run_started", protocol.StateWorking)

	d.markReloading("conv-r2")
	d.handleHostExit(hostsession.ExitInfo{SessionID: "conv-r2", ExitCode: 0, LifecycleID: "run-conv-r2"})

	if got := stateOf(t, d, "conv-r2"); got != protocol.StateWorking {
		t.Fatalf("state = %q, want working: the reload's own kill says nothing about the session", got)
	}
}

// A closed session has no row to move, and nothing should resurrect one.
func TestHostExitForAClosedSessionMovesNothing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	d.handleHostExit(hostsession.ExitInfo{SessionID: "never-existed", ExitCode: 0})

	if d.store.Get("never-existed") != nil {
		t.Fatal("a host exit created a session row")
	}
}

// The daemon restarting is the other way a host dies. A conversation session
// must come back as recoverable rather than be reaped: its history is on disk
// under attn's data dir, and a replacement host reopens it.
//
// The driver deliberately does not declare `resume` — that capability describes
// a PTY agent resumed from an argv flag, which a host does not have — so this is
// exactly the case the old rules dropped.
func TestConversationSessionSurvivesADaemonRestart(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registerConversationDriver(t, d, "pi-host")
	addHostSession(t, d, "conv-r3")
	// The exit path already closed the driver run, which is what made this
	// session look like an ordinary agent with nothing behind it.
	d.store.EndAgentDriverRun("conv-r3")

	session := d.store.Get("conv-r3")
	if !d.recoverOnMissingPTY(session) {
		t.Fatal("a conversation session with no live host was going to be dropped, not recovered")
	}
}

// The same rule must not sweep up an ordinary plugin agent that declares
// neither capability: it has nothing to come back to.
func TestNonConversationPluginSessionIsStillReaped(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	plugin := &pluginConnection{name: "snipe-plugin"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        "snipe",
		Capabilities: map[string]bool{},
	}); err != nil {
		t.Fatalf("register driver: %v", err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "plain-1", Agent: "snipe", Label: "plain-1", Directory: t.TempDir(),
		State: protocol.StateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	if d.recoverOnMissingPTY(d.store.Get("plain-1")) {
		t.Fatal("an agent with nothing to resume was marked recoverable")
	}
}

// A reload with nothing to relaunch from must leave the live host alone. This
// is the conversation half of the PTY reload's fail-safe: a running agent that
// kept going beats one that was killed and could not be brought back.
func TestConversationReloadWithoutALaunchIntentKeepsTheHost(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registerConversationDriver(t, d, "pi-host")
	addHostSession(t, d, "conv-r4")
	manager := spawnStubHost(t, d, "conv-r4")

	err := d.reloadSessionForClient("conv-r4", 0, 0)

	if err == nil || !strings.Contains(err.Error(), "launch intent") {
		t.Fatalf("reload error = %v, want one naming the missing launch intent", err)
	}
	if !manager.Has("conv-r4") {
		t.Fatal("the live host was killed for a reload that could not respawn it")
	}
}

// The one outcome a reload must never leave behind: a session that reads as
// live over a host that is gone. The kill has already happened by the time the
// respawn is refused, and its exit was suppressed — so nothing else will ever
// say the host died unless this path does. What the user is told is the truth:
// recoverable, with the way back still in front of them.
func TestConversationReloadThatCannotRespawnParksTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registerConversationDriver(t, d, "pi-host")
	addHostSession(t, d, "conv-r5")
	declare(d, "conv-r5", 1, "session_ready", protocol.StateIdle)
	manager := spawnStubHost(t, d, "conv-r5")
	d.store.SetLaunchIntent("conv-r5", store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser})

	if err := d.reloadSessionForClient("conv-r5", 0, 0); err == nil {
		t.Fatal("reload reported success with no plugin able to answer the spawn")
	}
	if manager.Has("conv-r5") {
		t.Fatal("the old host outlived the reload that killed it")
	}
	if got := stateOf(t, d, "conv-r5"); got != string(protocol.SessionStateRecoverable) {
		t.Fatalf("state = %q, want recoverable: the reload killed the host and could not replace it", got)
	}
}

// The attach verb crosses the same one-way pipe the others do; the snapshot
// comes back as an envelope, so what is asserted is that the ask arrived.
func TestAgentAttachReachesTheHost(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-r6")

	received := make(chan string, 4)
	manager := hostsession.New(d.logf, func(event hostsession.Event) {
		if verb, ok := event.Body["verb"].(string); ok {
			received <- verb
		}
	}, func(hostsession.ExitInfo) {})
	d.hostSessions = manager
	if err := manager.Spawn(hostsession.SpawnOptions{SessionID: "conv-r6", Command: []string{echoHostScript(t, "conv-r6")}}); err != nil {
		t.Fatalf("spawn echo host: %v", err)
	}
	t.Cleanup(func() { _ = manager.Kill("conv-r6") })

	client := &wsClient{send: make(chan outboundMessage, 10)}
	d.handleAgentAttach(client, &protocol.AgentAttachMessage{Cmd: protocol.CmdAgentAttach, ID: "conv-r6"})

	select {
	case verb := <-received:
		if !strings.Contains(verb, `"verb":"snapshot"`) {
			t.Fatalf("host received %q, want the snapshot verb", verb)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the snapshot verb never reached the host")
	}
}

// A client asking a session with no host must be told, or it draws an empty
// conversation forever and calls that the truth.
func TestAgentAttachWithoutAHostIsAnError(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{send: make(chan outboundMessage, 10)}

	d.handleAgentAttach(client, &protocol.AgentAttachMessage{Cmd: protocol.CmdAgentAttach, ID: "gone"})

	select {
	case msg := <-client.send:
		if !strings.Contains(string(msg.payload), "no live conversation host") {
			t.Fatalf("client got %q, want an error naming the missing host", string(msg.payload))
		}
	default:
		t.Fatal("no command error for an attach against a session with no host")
	}
}

// echoHostScript writes a host that answers every verb it is handed with an
// envelope carrying that verb, so a test can assert on what crossed the pipe.
func echoHostScript(t *testing.T, sessionID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "echo-host.sh")
	script := "#!/bin/sh\nwhile IFS= read -r line; do\n" +
		"  escaped=$(printf '%s' \"$line\" | sed 's/\"/\\\\\"/g')\n" +
		"  printf '{\"session_id\":\"" + sessionID + "\",\"seq\":1,\"kind\":\"message_end\",\"body\":{\"verb\":\"%s\"}}\\n' \"$escaped\" >&3\n" +
		"done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write echo host: %v", err)
	}
	return path
}

// spawnStubHost gives the daemon a live host that outlives nothing but the
// test: it reads its stdin until the pipe closes, so a Kill is what ends it.
func spawnStubHost(t *testing.T, d *Daemon, sessionID string) *hostsession.Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub-host.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nwhile IFS= read -r line; do :; done\n"), 0o755); err != nil {
		t.Fatalf("write stub host: %v", err)
	}
	manager := hostsession.New(d.logf, d.handleHostEvent, d.handleHostExit)
	d.hostSessions = manager
	if err := manager.Spawn(hostsession.SpawnOptions{SessionID: sessionID, Command: []string{path}}); err != nil {
		t.Fatalf("spawn stub host: %v", err)
	}
	t.Cleanup(func() { _ = manager.Kill(sessionID) })
	return manager
}
