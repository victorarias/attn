package daemon

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

func newReloadClientTestDaemon(t *testing.T, intent *store.LaunchIntent) (*Daemon, *fakeSpawnBackend, *wsClient) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "session",
		Label:          "session",
		Agent:          protocol.SessionAgentClaude,
		Directory:      cwd,
		WorkspaceID:    "workspace",
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if intent != nil {
		d.store.SetLaunchIntent("session", *intent)
	}
	return d, backend, spawnTestClient()
}

func readReloadSessionResult(t *testing.T, client *wsClient) protocol.ReloadSessionResultMessage {
	t.Helper()
	outbound := <-client.send
	var result protocol.ReloadSessionResultMessage
	if err := json.Unmarshal(outbound.payload, &result); err != nil {
		t.Fatalf("decode reload_session_result: %v", err)
	}
	return result
}

func TestReloadSessionDeadSessionRelaunchesFromStoredIntent(t *testing.T) {
	intent := store.LaunchIntent{
		YoloMode:   true,
		Executable: "/opt/claude",
		Model:      "claude-opus",
		Effort:     "high",
	}
	d, backend, client := newReloadClientTestDaemon(t, &intent)

	d.handleReloadSession(client, &protocol.ReloadSessionMessage{
		Cmd: protocol.CmdReloadSession, ID: "session", Cols: 101, Rows: 37,
	})

	if result := readReloadSessionResult(t, client); !result.Success {
		t.Fatalf("reload result = %+v, want success", result)
	}
	opts, spawned := backend.LastSpawn()
	if !spawned {
		t.Fatal("backend Spawn not called")
	}
	if opts.Cols != 101 || opts.Rows != 37 || !opts.YoloMode || opts.Executable != intent.Executable || opts.Model != intent.Model || opts.Effort != intent.Effort {
		t.Fatalf("spawn options = %+v, want geometry and stored intent", opts)
	}
}

func TestReloadSessionDeadSessionRequiresGeometry(t *testing.T) {
	d, backend, client := newReloadClientTestDaemon(t, &store.LaunchIntent{})

	d.handleReloadSession(client, &protocol.ReloadSessionMessage{
		Cmd: protocol.CmdReloadSession, ID: "session", Cols: 0, Rows: 24,
	})

	result := readReloadSessionResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, "geometry") {
		t.Fatalf("reload result = %+v, want geometry failure", result)
	}
	if _, spawned := backend.LastSpawn(); spawned {
		t.Fatal("backend Spawn called, want no spawn")
	}
}

func TestReloadSessionUnknownSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := spawnTestClient()

	d.handleReloadSession(client, &protocol.ReloadSessionMessage{
		Cmd: protocol.CmdReloadSession, ID: "unknown", Cols: 80, Rows: 24,
	})

	result := readReloadSessionResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, "session not found") {
		t.Fatalf("reload result = %+v, want session-not-found failure", result)
	}
}

func TestReloadSessionUnattendedDeadSessionCarriesContract(t *testing.T) {
	spec := launchcontract.UnattendedLaunchSpec{
		Agent: "claude", Model: "sonnet", Effort: "high", Executable: "/opt/claude",
		ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAuto,
		DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
	}
	d, backend, client := newReloadClientTestDaemon(t, &store.LaunchIntent{UnattendedLaunch: spec})

	d.handleReloadSession(client, &protocol.ReloadSessionMessage{
		Cmd: protocol.CmdReloadSession, ID: "session", Cols: 80, Rows: 24,
	})

	if result := readReloadSessionResult(t, client); !result.Success {
		t.Fatalf("reload result = %+v, want success", result)
	}
	opts, spawned := backend.LastSpawn()
	if !spawned {
		t.Fatal("backend Spawn not called")
	}
	if opts.UnattendedLaunch != spec || opts.YoloMode || opts.Model != "" || opts.Effort != "" || opts.Executable != "" || opts.AutoApprove {
		t.Fatalf("unattended reload options = %+v, want contract as the only launch policy", opts)
	}
}

func TestReloadSessionLiveWorkerRespawnsInPlace(t *testing.T) {
	backend := &fakeReloadBackend{
		liveIDs: []string{"session"},
		info:    ptybackend.SessionInfo{Cols: 120, Rows: 40},
		params:  ptybackend.SessionLaunchParams{Recorded: true, YoloMode: true, Executable: "/opt/claude"},
	}
	d := newReloadTestDaemon(t, backend)
	addReloadSession(d, "session", protocol.SessionAgentClaude, protocol.SessionStateWorking)
	client := spawnTestClient()

	d.handleReloadSession(client, &protocol.ReloadSessionMessage{
		Cmd: protocol.CmdReloadSession, ID: "session", Cols: 80, Rows: 24,
	})

	if result := readReloadSessionResult(t, client); !result.Success {
		t.Fatalf("reload result = %+v, want success", result)
	}
	if order := backend.callOrder(); !reflect.DeepEqual(order, []string{"kill:session", "remove:session", "spawn:session"}) {
		t.Fatalf("orchestration order = %v, want [kill remove spawn]", order)
	}
}
