package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSessionID(t *testing.T) {
	id1 := GenerateSessionID()
	id2 := GenerateSessionID()

	if id1 == id2 {
		t.Error("session IDs should be unique")
	}

	if len(id1) < 8 {
		t.Errorf("session ID too short: %q", id1)
	}
}

func TestDefaultLabel(t *testing.T) {
	// Create temp directory with known name
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "my-project")
	os.Mkdir(testDir, 0755)

	// Change to that directory temporarily
	oldDir, _ := os.Getwd()
	os.Chdir(testDir)
	defer os.Chdir(oldDir)

	label := DefaultLabel()
	if label != "my-project" {
		t.Errorf("DefaultLabel() = %q, want %q", label, "my-project")
	}
}

func TestWriteHooksConfig(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := "test-session"
	socketPath := "/tmp/test.sock"

	configPath, err := WriteHooksConfig(tmpDir, sessionID, socketPath, "/tmp/attn")
	if err != nil {
		t.Fatalf("WriteHooksConfig error: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("config file not created: %s", configPath)
	}

	// Read and verify content
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config error: %v", err)
	}

	if !strings.Contains(string(content), sessionID) {
		t.Error("config should contain session ID")
	}
}

func TestCleanupHooksConfig(t *testing.T) {
	tmpDir := t.TempDir()

	configPath, _ := WriteHooksConfig(tmpDir, "test", "/tmp/test.sock", "/tmp/attn")

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config file should exist before cleanup")
	}

	CleanupHooksConfig(configPath)

	// Verify file is gone
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("config file should be deleted after cleanup")
	}
}
