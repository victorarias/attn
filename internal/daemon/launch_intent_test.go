package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

type failingLaunchIntentBackend struct {
	fakeSpawnBackend
}

func (b *failingLaunchIntentBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return errors.New("spawn failed")
}

type launchIntentParamsErrorBackend struct {
	fakeSpawnBackend
	err error
}

func (b *launchIntentParamsErrorBackend) SessionLaunchParams(context.Context, string) (ptybackend.SessionLaunchParams, error) {
	return ptybackend.SessionLaunchParams{}, b.err
}

func TestLaunchIntentSpawnSuccessPersistsResolvedValues(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	client := spawnTestClient()
	msg := &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          "persist-launch-intent",
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: "workspace",
		Cols:        80,
		Rows:        24,
		YoloMode:    protocol.Ptr(true),
		Executable:  protocol.Ptr("/opt/claude"),
		Model:       protocol.Ptr("claude-opus"),
		Effort:      protocol.Ptr("high"),
	}

	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)

	got, ok := d.store.LaunchIntent(msg.ID)
	if !ok {
		t.Fatal("LaunchIntent() = ok false, want true after successful spawn")
	}
	want := store.LaunchIntent{
		YoloMode:   true,
		Executable: "/opt/claude",
		Model:      "claude-opus",
		Effort:     "high",
	}
	if got != want {
		t.Fatalf("LaunchIntent() = %+v, want %+v", got, want)
	}
}

func TestLaunchIntentSpawnFailurePersistsNothing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &failingLaunchIntentBackend{}
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	client := spawnTestClient()
	msg := &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          "failed-launch-intent",
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: "workspace",
		Cols:        80,
		Rows:        24,
	}

	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	if _, ok := d.store.LaunchIntent(msg.ID); ok {
		t.Fatal("LaunchIntent() = ok true after failed spawn, want false")
	}
}

func TestLaunchIntentReloadStoreFallback(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	// fakeSpawnBackend has no SessionLaunchParamsProvider, modeling a daemon after
	// restart whose worker registry is unavailable.
	d.ptyBackend = &fakeSpawnBackend{}
	now := string(protocol.TimestampNow())
	session := &protocol.Session{
		ID:             "stored-intent-reload",
		Label:          "Stored reload",
		Agent:          protocol.SessionAgentClaude,
		Directory:      "/tmp/stored-intent-reload",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	}
	d.store.Add(session)
	d.store.SetLaunchIntent(session.ID, store.LaunchIntent{
		YoloMode:   true,
		Executable: "/opt/claude",
		Model:      "claude-opus",
		Effort:     "high",
	})

	opts, err := d.buildReloadSpawnOptions(session)
	if err != nil {
		t.Fatalf("buildReloadSpawnOptions() error = %v", err)
	}
	if opts.YoloMode != true || opts.Executable != "/opt/claude" || opts.Model != "claude-opus" || opts.Effort != "high" {
		t.Fatalf("reload options = %+v, want persisted yolo/executable/model/effort", opts)
	}
	if opts.CWD != session.Directory || opts.Label != session.Label {
		t.Fatalf("reload identity = (cwd=%q, label=%q), want (%q, %q)", opts.CWD, opts.Label, session.Directory, session.Label)
	}

	d.ptyBackend = &launchIntentParamsErrorBackend{err: errors.Join(errors.New("worker registry missing"), pty.ErrSessionNotFound)}
	opts, err = d.buildReloadSpawnOptions(session)
	if err != nil {
		t.Fatalf("buildReloadSpawnOptions() with missing worker error = %v", err)
	}
	if opts.YoloMode != true || opts.Executable != "/opt/claude" || opts.Model != "claude-opus" || opts.Effort != "high" {
		t.Fatalf("reload options from missing worker error = %+v, want persisted yolo/executable/model/effort", opts)
	}

	d.ptyBackend = &launchIntentParamsErrorBackend{err: errors.New("registry corrupt")}
	if _, err := d.buildReloadSpawnOptions(session); err == nil {
		t.Fatal("buildReloadSpawnOptions() with corrupt registry = nil, want error")
	}

	d.ptyBackend = &fakeSpawnBackend{}
	unattended := launchcontract.UnattendedLaunchSpec{
		Agent: "claude", Model: "sonnet", Effort: "high", Executable: "/opt/claude",
		ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAuto,
		DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
	}
	d.store.SetLaunchIntent(session.ID, store.LaunchIntent{UnattendedLaunch: unattended})
	opts, err = d.buildReloadSpawnOptions(session)
	if err != nil {
		t.Fatalf("buildReloadSpawnOptions() for unattended stored intent error = %v", err)
	}
	if opts.UnattendedLaunch != unattended || opts.YoloMode || opts.Model != "" || opts.Effort != "" || opts.Executable != "" || opts.AutoApprove {
		t.Fatalf("unattended reload options = %+v, want contract as the only launch policy", opts)
	}

	noIntent := *session
	noIntent.ID = "missing-stored-intent"
	d.store.Add(&noIntent)
	if _, err := d.buildReloadSpawnOptions(&noIntent); err == nil {
		t.Fatal("buildReloadSpawnOptions() without stored intent = nil, want error")
	}
}
