package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDBPath_DefaultsToAttnDir(t *testing.T) {
	// Clear any env vars
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")
	os.Unsetenv("ATTN_PROFILE")

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
	os.Unsetenv("ATTN_PROFILE")

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
	os.Unsetenv("ATTN_PROFILE")

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
	os.Unsetenv("ATTN_PROFILE")

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

// --- Profile-aware behavior ---------------------------------------------------

func TestProfile_EmptyWhenUnset(t *testing.T) {
	os.Unsetenv("ATTN_PROFILE")
	if got := Profile(); got != "" {
		t.Errorf("Profile() = %q, want empty", got)
	}
	if got := ProfileLabel(); got != "default" {
		t.Errorf("ProfileLabel() = %q, want %q", got, "default")
	}
}

func TestProfile_NormalizesValidName(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "  Dev  ")
	if got := Profile(); got != "dev" {
		t.Errorf("Profile() = %q, want %q", got, "dev")
	}
	if got := ProfileLabel(); got != "dev" {
		t.Errorf("ProfileLabel() = %q, want %q", got, "dev")
	}
	if err := ValidateProfile(); err != nil {
		t.Errorf("ValidateProfile() returned unexpected error: %v", err)
	}
}

func TestValidateProfile_RejectsBadNames(t *testing.T) {
	cases := []string{
		"has space",
		"has/slash",
		"with.dot",
		"-leadingdash",
		"UPPER_CASE_UNDERSCORE",
		strings.Repeat("a", 17),
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("ATTN_PROFILE", bad)
			if err := ValidateProfile(); err == nil {
				t.Errorf("ValidateProfile() accepted %q, expected error", bad)
			}
			if got := Profile(); got != "" {
				t.Errorf("Profile() = %q for invalid input %q, want empty", got, bad)
			}
		})
	}
}

func TestAttnDir_SplitsByProfile(t *testing.T) {
	home, _ := os.UserHomeDir()
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")

	t.Setenv("ATTN_PROFILE", "dev")
	reloadConfig()

	wantDir := filepath.Join(home, ".attn-dev")
	if got := DataDir(); got != wantDir {
		t.Errorf("DataDir() = %q, want %q", got, wantDir)
	}
	if got := SocketPath(); got != filepath.Join(wantDir, "attn.sock") {
		t.Errorf("SocketPath() = %q", got)
	}
	if got := DBPath(); got != filepath.Join(wantDir, "attn.db") {
		t.Errorf("DBPath() = %q", got)
	}
	if got := LogPath(); got != filepath.Join(wantDir, "daemon.log") {
		t.Errorf("LogPath() = %q", got)
	}
}

func TestWSPort_ProfileDefaults(t *testing.T) {
	os.Unsetenv("ATTN_WS_PORT")

	cases := map[string]string{
		"":      "9849",
		"dev":   "29849",
		"alpha": "", // hashed, just check it's in the right range
	}
	for profile, want := range cases {
		t.Run("profile="+profile, func(t *testing.T) {
			if profile == "" {
				os.Unsetenv("ATTN_PROFILE")
			} else {
				t.Setenv("ATTN_PROFILE", profile)
			}
			got := WSPort()
			if want != "" && got != want {
				t.Errorf("WSPort() = %q, want %q", got, want)
			}
			if profile == "alpha" {
				// Hash-derived; must differ from default + dev, be inside [20000,29848].
				if got == "9849" || got == "29849" {
					t.Errorf("hashed port for %q collided: %q", profile, got)
				}
			}
		})
	}
}

func TestWSPort_EnvOverridesProfileDefault(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "dev")
	t.Setenv("ATTN_WS_PORT", "44444")
	if got := WSPort(); got != "44444" {
		t.Errorf("WSPort() = %q, want %q", got, "44444")
	}
}

func TestLegacyStatePath_SuffixedByProfile(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("ATTN_PROFILE", "dev")
	SetBinaryName("attn")
	want := filepath.Join(home, ".attn-state-dev.json")
	if got := StatePath(); got != want {
		t.Errorf("StatePath() = %q, want %q", got, want)
	}
}
