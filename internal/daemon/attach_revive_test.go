package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

type attachReviveBackend struct {
	fakeSpawnBackend
}

func (b *attachReviveBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	if _, spawned := b.LastSpawn(); !spawned {
		return ptybackend.AttachInfo{}, nil, pty.ErrSessionNotFound
	}
	return ptybackend.AttachInfo{Running: true}, newFakeOutputStream(), nil
}

func newAttachReviveTestDaemon(t *testing.T, state protocol.SessionState, intent *store.LaunchIntent) (*Daemon, *attachReviveBackend, *wsClient, string) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	backend := &attachReviveBackend{}
	d.ptyBackend = backend
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "recoverable",
		Label:          "recoverable",
		Agent:          protocol.SessionAgentClaude,
		Directory:      cwd,
		WorkspaceID:    "workspace",
		State:          state,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if intent != nil {
		d.store.SetLaunchIntent("recoverable", *intent)
	}
	return d, backend, spawnTestClient(), cwd
}

func readAttachReviveResult(t *testing.T, client *wsClient) protocol.AttachResultMessage {
	t.Helper()
	outbound := <-client.send
	var result protocol.AttachResultMessage
	if err := json.Unmarshal(outbound.payload, &result); err != nil {
		t.Fatalf("decode attach_result: %v", err)
	}
	return result
}

func assertAttachReviveDidNotSpawn(t *testing.T, backend *attachReviveBackend) {
	t.Helper()
	if _, spawned := backend.LastSpawn(); spawned {
		t.Fatal("backend Spawn called, want no spawn")
	}
}

func TestAttachReviveRespawnsRecoverableSessionFromStoredIntent(t *testing.T) {
	intent := store.LaunchIntent{
		YoloMode:   true,
		Executable: "/opt/claude",
		Model:      "claude-opus",
		Effort:     "high",
	}
	d, backend, client, _ := newAttachReviveTestDaemon(t, protocol.SessionStateRecoverable, &intent)
	d.handleAttachSession(client, &protocol.AttachSessionMessage{
		Cmd:          protocol.CmdAttachSession,
		ID:           "recoverable",
		AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive),
		Cols:         protocol.Ptr(101),
		Rows:         protocol.Ptr(37),
	})

	result := readAttachReviveResult(t, client)
	if !result.Success || !protocol.Deref(result.Revived) {
		t.Fatalf("attach result = %+v, want success with revived=true", result)
	}
	opts, spawned := backend.LastSpawn()
	if !spawned {
		t.Fatal("backend Spawn not called")
	}
	if opts.Cols != 101 || opts.Rows != 37 || !opts.YoloMode || opts.Executable != intent.Executable || opts.Model != intent.Model || opts.Effort != intent.Effort {
		t.Fatalf("spawn options = %+v, want attach geometry and stored intent", opts)
	}
}

func TestAttachDoesNotReviveWithoutPolicy(t *testing.T) {
	d, backend, client, _ := newAttachReviveTestDaemon(t, protocol.SessionStateRecoverable, &store.LaunchIntent{})
	d.handleAttachSession(client, &protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: "recoverable"})

	result := readAttachReviveResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, pty.ErrSessionNotFound.Error()) {
		t.Fatalf("attach result = %+v, want session-not-found failure", result)
	}
	assertAttachReviveDidNotSpawn(t, backend)
}

func TestAttachReviveRefusesNonRecoverableSession(t *testing.T) {
	d, backend, client, _ := newAttachReviveTestDaemon(t, protocol.SessionStateWorking, &store.LaunchIntent{})
	d.handleAttachSession(client, &protocol.AttachSessionMessage{
		Cmd:          protocol.CmdAttachSession,
		ID:           "recoverable",
		AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive),
		Cols:         protocol.Ptr(80),
		Rows:         protocol.Ptr(24),
	})

	result := readAttachReviveResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, "session not recoverable") {
		t.Fatalf("attach result = %+v, want non-recoverable failure", result)
	}
	assertAttachReviveDidNotSpawn(t, backend)
}

func TestAttachReviveRequiresGeometry(t *testing.T) {
	d, backend, client, _ := newAttachReviveTestDaemon(t, protocol.SessionStateRecoverable, &store.LaunchIntent{})
	d.handleAttachSession(client, &protocol.AttachSessionMessage{
		Cmd:          protocol.CmdAttachSession,
		ID:           "recoverable",
		AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive),
		Cols:         protocol.Ptr(0),
		Rows:         protocol.Ptr(24),
	})

	result := readAttachReviveResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, "revive requires pty geometry") {
		t.Fatalf("attach result = %+v, want geometry failure", result)
	}
	assertAttachReviveDidNotSpawn(t, backend)
}

func TestAttachReviveRefusesUnattendedSession(t *testing.T) {
	d, backend, client, _ := newAttachReviveTestDaemon(t, protocol.SessionStateRecoverable, &store.LaunchIntent{Unattended: true})
	d.handleAttachSession(client, &protocol.AttachSessionMessage{
		Cmd:          protocol.CmdAttachSession,
		ID:           "recoverable",
		AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive),
		Cols:         protocol.Ptr(80),
		Rows:         protocol.Ptr(24),
	})

	result := readAttachReviveResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, "unattended session cannot be revived from store") {
		t.Fatalf("attach result = %+v, want unattended failure", result)
	}
	assertAttachReviveDidNotSpawn(t, backend)
}
