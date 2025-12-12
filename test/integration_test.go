//go:build integration

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_DaemonAndClient(t *testing.T) {
	// Build the binary
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "attn")

	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/attn")
	if err := cmd.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Start daemon
	os.Setenv("HOME", tmpDir) // Use temp socket
	daemon := exec.Command(binPath, "daemon")
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer daemon.Process.Kill()

	// Wait for daemon
	time.Sleep(100 * time.Millisecond)

	// Test status command
	status := exec.Command(binPath, "status")
	output, _ := status.Output()
	t.Logf("status output: %q", output)

	// Test list command
	list := exec.Command(binPath, "list")
	output, err := list.Output()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	t.Logf("list output: %s", output)
}
