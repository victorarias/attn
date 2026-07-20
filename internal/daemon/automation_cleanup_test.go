package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// TestAutomationCleanupPartitionsCleanAndDirtyWorktrees pins A4's core
// behavior: given two terminal runs with real worktrees, one clean and one
// dirty, cleanup removes the clean one's worktree and reports it in
// `cleaned`, leaves the dirty one's worktree in place and reports it in
// `kept_dirty` — with no age/count gate (unlike A3's retention sweep).
func TestAutomationCleanupPartitionsCleanAndDirtyWorktrees(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")

	cleanWorktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-clean", cleanWorktree)

	dirtyWorktree := filepath.Join(root, "repo--dirty")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-dirty", dirtyWorktree)
	if err := os.WriteFile(filepath.Join(dirtyWorktree, "untracked.txt"), []byte("uncommitted"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Recent runs (well within any retention window) confirm cleanup has no
	// age or count gate: A3 would leave these alone entirely.
	now := time.Now()
	cleanRun := claimTerminalAutomationRun(t, s, def, "cleanup-clean-1", now, automationResolvedLocationJSON(t, mainRepo, cleanWorktree))
	dirtyRun := claimTerminalAutomationRun(t, s, def, "cleanup-dirty-1", now, automationResolvedLocationJSON(t, mainRepo, dirtyWorktree))

	cleaned, keptDirty, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != cleanRun.ID {
		t.Fatalf("cleaned = %v, want [%s]", cleaned, cleanRun.ID)
	}
	if len(keptDirty) != 1 || keptDirty[0] != dirtyRun.ID {
		t.Fatalf("keptDirty = %v, want [%s]", keptDirty, dirtyRun.ID)
	}
	if _, err := os.Stat(cleanWorktree); !os.IsNotExist(err) {
		t.Fatalf("expected the clean worktree to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(dirtyWorktree); err != nil {
		t.Fatalf("expected the dirty worktree to survive, stat err=%v", err)
	}
}

// TestAutomationCleanupNeverTouchesRowsOrArtifacts pins that, unlike A3's
// retention sweep, cleanup is disk-only: after cleaning a clean worktree,
// the run/occurrence rows and the occurrence artifact file are all still
// present — only the worktree is gone.
func TestAutomationCleanupNeverTouchesRowsOrArtifacts(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-rows", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-rows-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	artifactDir := filepath.Join(root, "automation", "occurrences")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(artifactDir, run.ID+".json")
	if err := os.WriteFile(artifactPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cleaned, keptDirty, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != run.ID {
		t.Fatalf("cleaned = %v, want [%s]", cleaned, run.ID)
	}
	if len(keptDirty) != 0 {
		t.Fatalf("keptDirty = %v, want none", keptDirty)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("expected the worktree to be removed, stat err=%v", err)
	}
	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("run row must survive cleanup, got %#v err=%v", got, err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("occurrence artifact must survive cleanup, stat err=%v", err)
	}
}

// TestAutomationCleanupSecondRunIsNoOp pins that invoking cleanup again
// after a successful pass reports nothing new: the worktree that was
// already removed is not re-reported as cleaned.
func TestAutomationCleanupSecondRunIsNoOp(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--clean")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-noop", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-noop-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	cleaned, keptDirty, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != run.ID {
		t.Fatalf("first pass cleaned = %v, want [%s]", cleaned, run.ID)
	}
	if len(keptDirty) != 0 {
		t.Fatalf("first pass keptDirty = %v, want none", keptDirty)
	}

	cleaned2, keptDirty2, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned2) != 0 {
		t.Fatalf("second pass cleaned = %v, want none", cleaned2)
	}
	if len(keptDirty2) != 0 {
		t.Fatalf("second pass keptDirty = %v, want none", keptDirty2)
	}
	if got, err := s.GetAutomationRun(run.ID); err != nil || got == nil {
		t.Fatalf("run row must still survive after the second pass, got %#v err=%v", got, err)
	}
}

// TestAutomationCleanupLiveSessionSkipped pins that a terminal run whose
// session still exists in the store keeps its worktree. Unlike A3's
// retention sweep, cleanup has no age/count gate at all, so without this
// check it would remove a live thread's worktree out from under it on the
// very next on-demand cleanup.
func TestAutomationCleanupLiveSessionSkipped(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--live")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-live", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	run := claimTerminalAutomationRun(t, s, def, "cleanup-live-1", now, automationResolvedLocationJSON(t, mainRepo, worktree))
	s.Add(&protocol.Session{
		ID: run.SessionID, Label: "reviewer", Agent: string(protocol.SessionAgentCodex), Directory: t.TempDir(), State: protocol.SessionStateIdle,
		StateSince: now.Format(time.RFC3339), StateUpdatedAt: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), WorkspaceID: run.WorkspaceID,
	})

	cleaned, keptDirty, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 0 || len(keptDirty) != 0 {
		t.Fatalf("expected a live-session run to be skipped entirely, cleaned=%v keptDirty=%v", cleaned, keptDirty)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("expected the live-session worktree to survive cleanup, stat err=%v", err)
	}
}

// TestAutomationCleanupReclaimsSoftDeletedDefinition pins that cleanup still
// works after its definition is soft-deleted: automationCleanup deliberately
// uses GetAutomationDefinitionIncludingDeleted rather than
// GetAutomationDefinition, precisely so a user can reclaim a retired
// automation's worktrees on demand instead of waiting for the retention
// sweep to eventually reach it.
func TestAutomationCleanupReclaimsSoftDeletedDefinition(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "repo")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, mainRepo, "init")
	runGitDaemon(t, mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(root, "repo--deleted-def")
	runGitDaemon(t, mainRepo, "worktree", "add", "-b", "automation/cleanup-deleted-def", worktree)

	s := store.New()
	d := &Daemon{store: s, dataRoot: root, wsHub: newWSHub()}
	raw := fmt.Sprintf(manualAutomationYAML, t.TempDir())
	def, err := d.automationApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	run := claimTerminalAutomationRun(t, s, def, "cleanup-deleted-def-1", time.Now(), automationResolvedLocationJSON(t, mainRepo, worktree))

	if err := d.automationDelete(context.Background(), def.ID); err != nil {
		t.Fatal(err)
	}

	cleaned, keptDirty, err := d.automationCleanup(context.Background(), def.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != run.ID {
		t.Fatalf("cleaned = %v, want [%s]", cleaned, run.ID)
	}
	if len(keptDirty) != 0 {
		t.Fatalf("keptDirty = %v, want none", keptDirty)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("expected the deleted definition's worktree to be reclaimed, stat err=%v", err)
	}
}
