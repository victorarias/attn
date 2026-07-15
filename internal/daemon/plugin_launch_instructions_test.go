package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestPreparePluginLaunchInstructionsBeforeSessionPersistence(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	addTestWorkspace(d, "workspace-a", t.TempDir())
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-a", "# Shared\ncurrent decision\n", "source", 0); err != nil {
		t.Fatalf("seed workspace context: %v", err)
	}

	instructions, rollback, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", false)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if d.store.Get("session-a") != nil {
		t.Fatal("instruction preparation persisted a provisional session")
	}
	if instructions.Kind != pluginInstructionKindWorkspace || instructions.ContextRevision != 1 {
		t.Fatalf("instructions = %+v, want workspace revision 1", instructions)
	}
	if !strings.Contains(instructions.Content, instructions.ContextPath) || !strings.Contains(instructions.Content, "attn tracks work as tickets") {
		t.Fatalf("instructions content did not compose existing guidance: %q", instructions.Content)
	}
	if got, err := os.ReadFile(instructions.ContextPath); err != nil || string(got) != "# Shared\ncurrent decision\n" {
		t.Fatalf("checkout = %q, err=%v", got, err)
	}
	other, otherRollback, err := d.preparePluginLaunchInstructions("session-b", "workspace-a", false)
	if err != nil {
		t.Fatalf("prepare second session: %v", err)
	}
	if other.ContextPath == instructions.ContextPath {
		t.Fatalf("two sessions shared checkout path %q", other.ContextPath)
	}
	otherRollback()
	if _, _, err := d.store.UpdateWorkspaceContext("workspace-a", "# Shared\nnew decision\n", "source", 1); err != nil {
		t.Fatalf("update workspace context: %v", err)
	}
	refreshed, _, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", false)
	if err != nil {
		t.Fatalf("refresh launch instructions: %v", err)
	}
	if refreshed.ContextRevision != 2 {
		t.Fatalf("refreshed revision=%d, want 2", refreshed.ContextRevision)
	}
	if got, err := os.ReadFile(refreshed.ContextPath); err != nil || string(got) != "# Shared\nnew decision\n" {
		t.Fatalf("refreshed checkout = %q, err=%v", got, err)
	}

	rollback()
	if _, err := os.Stat(workspaceContextCheckoutDir(d.dataRoot, "session-a")); !os.IsNotExist(err) {
		t.Fatalf("rollback left newly-created checkout: %v", err)
	}
}

func TestPreparePluginLaunchInstructionsPreservesExistingCheckout(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	setupWorkspaceContextSession(t, d, "session-a", "workspace-a")
	checkout, err := d.checkoutWorkspaceContext(&protocol.WorkspaceContextCheckoutMessage{SourceSessionID: "session-a"})
	if err != nil {
		t.Fatalf("checkoutWorkspaceContext: %v", err)
	}
	if err := os.WriteFile(checkout.Path, []byte("local edit\n"), 0o600); err != nil {
		t.Fatalf("edit checkout: %v", err)
	}

	_, rollback, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", false)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	rollback()
	if got, err := os.ReadFile(checkout.Path); err != nil || string(got) != "local edit\n" {
		t.Fatalf("rollback changed existing checkout = %q, err=%v", got, err)
	}
}

func TestPreparePluginChiefInstructionsUsesNotebookNotWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	addTestWorkspace(d, "workspace-a", t.TempDir())
	notebookRoot := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, notebookRoot)

	instructions, _, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", true)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if instructions.Kind != pluginInstructionKindChief || instructions.NotebookRoot != notebookRoot || instructions.ContextPath != "" {
		t.Fatalf("chief instructions = %+v", instructions)
	}
	if !strings.Contains(instructions.Content, "You are the chief of staff") || !strings.Contains(instructions.Content, notebookRoot) {
		t.Fatalf("chief guidance = %q", instructions.Content)
	}
	if _, err := os.Stat(workspaceContextCheckoutDir(d.dataRoot, "session-a")); !os.IsNotExist(err) {
		t.Fatalf("chief preparation created workspace checkout: %v", err)
	}
}
