package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/toolhome"
)

func spawnTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func seedClaudeTranscript(t *testing.T, home, resumeID string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "seed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, resumeID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func seedReloadableClaudeSession(t *testing.T, d *Daemon, sessionID string) (workspaceID, cwd string) {
	t.Helper()
	workspaceID = "workspace-" + sessionID
	cwd = t.TempDir()
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "revive",
		Directory: cwd,
	})
	d.store.Add(&protocol.Session{
		ID:          sessionID,
		Agent:       protocol.SessionAgentClaude,
		WorkspaceID: workspaceID,
		Directory:   cwd,
		Label:       "revive",
	})
	return workspaceID, cwd
}

// A relaunch of a recoverable claude session (the sidebar Reload button, or the
// pane-mount auto-revive) resolves the session's own id as its resume target.
// When claude never wrote a transcript for that id, `claude --resume <id>` would
// exit non-zero ("No conversation found"). handleSpawnSession must downgrade to a
// fresh launch (empty ResumeSessionID, reusing --session-id) rather than spawn a
// doomed resume. Mirrors buildReloadSpawnOptions and the ticket ResumePicker
// branch.
func TestSpawnDowngradesResumeWhenTranscriptMissing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-revive-claude"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	// The resolver returns the session's own id as the resume target.
	d.persistResumeSessionID(sessionID, sessionID)
	// Empty tool home → claude's transcript lookup finds nothing → not resumable.
	t.Setenv(toolhome.EnvVar, t.TempDir())

	since := spawnCount(backend)
	d.handleSpawnSession(spawnTestClient(), &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})

	spawn := resumeSpawnForSession(t, backend, sessionID, since)
	if spawn.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want empty (no transcript → fresh spawn reusing --session-id)", spawn.ResumeSessionID)
	}
}

// The safety counterpart: a session-own-id resume WITH a transcript on disk (the
// session actually took a turn) must be preserved so the conversation restores —
// the self-id downgrade must not fire for a genuinely resumable session.
func TestSpawnPreservesSelfResumeWhenTranscriptPresent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-revive-claude-live"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	// Claude resumes under the attn session id (--session-id), so a session that
	// took a turn has its transcript on disk keyed by its own id.
	d.persistResumeSessionID(sessionID, sessionID)
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	seedClaudeTranscript(t, home, sessionID)

	since := spawnCount(backend)
	d.handleSpawnSession(spawnTestClient(), &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})

	spawn := resumeSpawnForSession(t, backend, sessionID, since)
	if spawn.ResumeSessionID != sessionID {
		t.Fatalf("ResumeSessionID = %q, want %q (transcript present → self-resume preserved)", spawn.ResumeSessionID, sessionID)
	}
}

// A distinct agent-native resume id (not the session's own id) is trusted and
// passed through even without a transcript visible here — the self-id downgrade
// is scoped to the lazy-transcript case and must not touch it. Guards the
// established "uses stored resume id" contract.
func TestSpawnPreservesDistinctNativeResumeID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-revive-claude-distinct"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	nativeID := "claude-native-xyz"
	d.persistResumeSessionID(sessionID, nativeID)
	t.Setenv(toolhome.EnvVar, t.TempDir()) // no transcript on disk

	since := spawnCount(backend)
	d.handleSpawnSession(spawnTestClient(), &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})

	spawn := resumeSpawnForSession(t, backend, sessionID, since)
	if spawn.ResumeSessionID != nativeID {
		t.Fatalf("ResumeSessionID = %q, want %q (distinct native id trusted, not downgraded)", spawn.ResumeSessionID, nativeID)
	}
}

// TestSpawnReviveReentersLaunchLifecycle guards that spawning over a recoverable
// session re-enters the launch lifecycle instead of letting commitSpawn's
// snapshotted record resurrect stale recoverable state.
func TestSpawnReviveReentersLaunchLifecycle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "recoverable",
		Label:          "recoverable",
		Agent:          protocol.SessionAgentClaude,
		Directory:      cwd,
		WorkspaceID:    "workspace",
		State:          protocol.SessionStateRecoverable,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	client := spawnTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          "recoverable",
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: "workspace",
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, "recoverable", true)

	if session := d.store.Get("recoverable"); session == nil || session.State != protocol.SessionStateLaunching {
		t.Fatalf("session = %+v, want launching", session)
	}
}
