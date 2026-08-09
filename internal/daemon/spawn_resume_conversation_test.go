package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

// A conversation picked up by file is named in the launch intent, and the intent
// is re-offered to every replacement host. If the file is gone by the time the
// host runs, the fork throws and the host exits — and the revive hands it the
// same missing path again. The user sees a session flap and is told nothing.
// The spawn refuses instead, naming the file.
func TestSpawnRefusesAMissingConversationToPickUp(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-resume-missing"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	missing := filepath.Join(t.TempDir(), "deleted-by-a-profile-clean.jsonl")

	since := spawnCount(backend)
	rejection := d.runSpawnPipeline(&protocol.SpawnSessionMessage{
		Cmd:                    protocol.CmdSpawnSession,
		ID:                     sessionID,
		Cwd:                    cwd,
		Agent:                  "claude",
		WorkspaceID:            workspaceID,
		Cols:                   80,
		Rows:                   24,
		ResumeConversationFile: protocol.Ptr(missing),
	}, internalSpawnPolicy{})

	if rejection == nil {
		t.Fatal("spawn was accepted; want a refusal naming the conversation file that is gone")
	}
	if got := rejection.reason().Error(); !strings.Contains(got, missing) {
		t.Fatalf("refusal = %q, want it to name %q — an agent cannot fix a failure that does not say which file", got, missing)
	}
	if spawnCount(backend) != since {
		t.Fatal("a session was spawned anyway; the refusal must stop before the host starts")
	}
}

// The counterpart, and the reason the check is scoped rather than blanket: once
// a session has forked its own copy, the host continues that copy and never
// opens the resume file again. A revive must not start depending on a file it
// will not read — an established conversation stays revivable long after the one
// it was picked up from is deleted.
func TestSpawnStillRevivesWhenTheSourceConversationIsGone(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-resume-established"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)

	// This session already holds its own forked history.
	stateDir := hostSessionStateDir(sessionID)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir host state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "2026-08-09T00-00-00-000Z_forked.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write forked conversation: %v", err)
	}

	rejection := d.runSpawnPipeline(&protocol.SpawnSessionMessage{
		Cmd:                    protocol.CmdSpawnSession,
		ID:                     sessionID,
		Cwd:                    cwd,
		Agent:                  "claude",
		WorkspaceID:            workspaceID,
		Cols:                   80,
		Rows:                   24,
		ResumeConversationFile: protocol.Ptr(filepath.Join(t.TempDir(), "long-since-deleted.jsonl")),
	}, internalSpawnPolicy{})

	if rejection != nil {
		t.Fatalf("spawn refused with %v; a session holding its own history does not read the resume file", rejection.reason())
	}
}

// A directory is not a conversation. The picker cannot produce one, but the
// launch intent is a string and this is the shape of the failure that would
// otherwise reach pi as an unreadable-file throw.
func TestSpawnRefusesADirectoryAsAConversation(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}

	sessionID := "attn-resume-directory"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)

	rejection := d.runSpawnPipeline(&protocol.SpawnSessionMessage{
		Cmd:                    protocol.CmdSpawnSession,
		ID:                     sessionID,
		Cwd:                    cwd,
		Agent:                  "claude",
		WorkspaceID:            workspaceID,
		Cols:                   80,
		Rows:                   24,
		ResumeConversationFile: protocol.Ptr(t.TempDir()),
	}, internalSpawnPolicy{})

	if rejection == nil {
		t.Fatal("a directory was accepted as a conversation to pick up")
	}
	if got := rejection.reason().Error(); !strings.Contains(got, "directory") {
		t.Fatalf("refusal = %q, want it to say the path is a directory", got)
	}
}
