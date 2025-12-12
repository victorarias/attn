package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDBPath_DefaultsToAttnDir(t *testing.T) {
	// Clear any env vars
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")

	path := DBPath()

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".attn", "attn.db")
	if path != expected {
		t.Errorf("DBPath() = %q, want %q", path, expected)
	}
}

func TestDBPath_EnvVarOverridesDefault(t *testing.T) {
	os.Setenv("ATTN_DB_PATH", "/custom/path/test.db")
	defer os.Unsetenv("ATTN_DB_PATH")

	path := DBPath()

	if path != "/custom/path/test.db" {
		t.Errorf("DBPath() = %q, want %q", path, "/custom/path/test.db")
	}
}

func TestSocketPath_DefaultsToAttnDir(t *testing.T) {
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")

	path := SocketPath()

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".attn", "attn.sock")
	if path != expected {
		t.Errorf("SocketPath() = %q, want %q", path, expected)
	}
}

func TestSocketPath_EnvVarOverridesDefault(t *testing.T) {
	os.Setenv("ATTN_SOCKET_PATH", "/tmp/custom.sock")
	defer os.Unsetenv("ATTN_SOCKET_PATH")

	path := SocketPath()

	if path != "/tmp/custom.sock" {
		t.Errorf("SocketPath() = %q, want %q", path, "/tmp/custom.sock")
	}
}

func TestDBPath_ConfigFileOverridesDefault(t *testing.T) {
	os.Unsetenv("ATTN_DB_PATH")

	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configContent := `{"db_path": "/from/config/file.db"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("ATTN_CONFIG_PATH", configPath)
	defer os.Unsetenv("ATTN_CONFIG_PATH")

	// Force reload config
	reloadConfig()

	path := DBPath()

	if path != "/from/config/file.db" {
		t.Errorf("DBPath() = %q, want %q", path, "/from/config/file.db")
	}
}

func TestDBPath_EnvVarOverridesConfigFile(t *testing.T) {
	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configContent := `{"db_path": "/from/config/file.db"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("ATTN_CONFIG_PATH", configPath)
	os.Setenv("ATTN_DB_PATH", "/from/env/var.db")
	defer os.Unsetenv("ATTN_CONFIG_PATH")
	defer os.Unsetenv("ATTN_DB_PATH")

	// Force reload config
	reloadConfig()

	path := DBPath()

	// Env var should win over config file
	if path != "/from/env/var.db" {
		t.Errorf("DBPath() = %q, want %q (env var should override config file)", path, "/from/env/var.db")
	}
}

func TestSocketPath_ConfigFileOverridesDefault(t *testing.T) {
	os.Unsetenv("ATTN_SOCKET_PATH")

	// Create temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configContent := `{"socket_path": "/from/config/file.sock"}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("ATTN_CONFIG_PATH", configPath)
	defer os.Unsetenv("ATTN_CONFIG_PATH")

	// Force reload config
	reloadConfig()

	path := SocketPath()

	if path != "/from/config/file.sock" {
		t.Errorf("SocketPath() = %q, want %q", path, "/from/config/file.sock")
	}
}
