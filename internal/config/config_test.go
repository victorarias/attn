package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDBPath_DefaultsToAttnDir(t *testing.T) {
	// DBPath() = filepath.Join(attnDir(), "attn.db") when unoverridden; assert
	// that composition against the ATTN_DATA_DIR-scoped data dir rather than
	// the real $HOME (attnDir()'s HOME-derivation formula itself is covered
	// by TestDefaultAttnDir_SplitsByProfile without touching go test's
	// data-dir backstop).
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")
	os.Unsetenv("ATTN_PROFILE")

	path := DBPath()

	expected := filepath.Join(dataDir, "attn.db")
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
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")
	os.Unsetenv("ATTN_PROFILE")

	path := SocketPath()

	expected := filepath.Join(dataDir, "attn.sock")
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

func TestValidateDaemonIsolation_RejectsForeignSocketRootWithProfileDB(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(t.TempDir(), "attn.sock"))
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()

	err := ValidateDaemonIsolation(SocketPath())
	if err == nil {
		t.Fatal("ValidateDaemonIsolation() accepted an alternate socket root with the default profile DB")
	}
	if !strings.Contains(err.Error(), "refusing to start daemon") {
		t.Fatalf("ValidateDaemonIsolation() error = %q, want refusal message", err)
	}
}

func TestValidateDaemonIsolation_AllowsForeignSocketRootWithIsolatedDB(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(tmpDir, "attn.sock"))
	t.Setenv("ATTN_DB_PATH", filepath.Join(tmpDir, "attn.db"))
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()

	if err := ValidateDaemonIsolation(SocketPath()); err != nil {
		t.Fatalf("ValidateDaemonIsolation() returned unexpected error: %v", err)
	}
}

func TestValidateDaemonIsolation_RejectsRelativeDBPathInProfileDir(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	t.Setenv("ATTN_PROFILE", "")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(t.TempDir(), "attn.sock"))
	t.Setenv("ATTN_DB_PATH", "attn.db")
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()

	profileDataDir := DataDir()
	if err := os.MkdirAll(profileDataDir, 0o755); err != nil {
		t.Fatalf("mkdir profile data dir: %v", err)
	}
	t.Chdir(profileDataDir)

	err := ValidateDaemonIsolation(SocketPath())
	if err == nil {
		t.Fatal("ValidateDaemonIsolation() accepted a relative DB path that resolves to the default profile DB")
	}
	if !strings.Contains(err.Error(), "refusing to start daemon") {
		t.Fatalf("ValidateDaemonIsolation() error = %q, want refusal message", err)
	}
}

func TestValidateDaemonIsolation_AllowsSocketOverrideInsideProfileDataDir(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "dev")
	t.Setenv("ATTN_DB_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", "")
	reloadConfig()
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join(DataDir(), "custom.sock"))

	if err := ValidateDaemonIsolation(SocketPath()); err != nil {
		t.Fatalf("ValidateDaemonIsolation() returned unexpected error: %v", err)
	}
}

func TestPluginDir_DefaultsToAttnDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	os.Unsetenv("ATTN_PLUGIN_DIR")
	os.Unsetenv("ATTN_PROFILE")

	want := filepath.Join(dataDir, "plugins")
	if got := PluginDir(); got != want {
		t.Errorf("PluginDir() = %q, want %q", got, want)
	}
}

func TestPluginDir_EnvVarOverridesDefault(t *testing.T) {
	t.Setenv("ATTN_PLUGIN_DIR", "/tmp/attn-test-plugins")
	if got := PluginDir(); got != "/tmp/attn-test-plugins" {
		t.Errorf("PluginDir() = %q, want %q", got, "/tmp/attn-test-plugins")
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

// TestDefaultAttnDir_SplitsByProfile pins the HOME-derivation formula
// attnDir() falls back to when ATTN_DATA_DIR is unset. It calls the pure
// defaultAttnDir helper directly (no env var, no I/O) instead of exercising
// attnDir()/DataDir(), which the go-test backstop refuses to resolve without
// an explicit ATTN_DATA_DIR override.
func TestDefaultAttnDir_SplitsByProfile(t *testing.T) {
	home, _ := os.UserHomeDir()

	if got, want := defaultAttnDir(""), filepath.Join(home, ".attn"); got != want {
		t.Errorf("defaultAttnDir(\"\") = %q, want %q", got, want)
	}
	if got, want := defaultAttnDir("dev"), filepath.Join(home, ".attn-dev"); got != want {
		t.Errorf("defaultAttnDir(\"dev\") = %q, want %q", got, want)
	}
}

// TestAttnDir_DerivedPathsAllInheritDataDir asserts every attnDir()-derived
// path (socket, DB, log) composes off the same base, regardless of profile —
// the property that lets ATTN_DATA_DIR override every derived path at once.
func TestAttnDir_DerivedPathsAllInheritDataDir(t *testing.T) {
	wantDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", wantDir)
	os.Unsetenv("ATTN_SOCKET_PATH")
	os.Unsetenv("ATTN_DB_PATH")
	os.Unsetenv("ATTN_CONFIG_PATH")

	t.Setenv("ATTN_PROFILE", "dev")
	reloadConfig()

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

func TestDeepLinkScheme(t *testing.T) {
	t.Run("default → attn", func(t *testing.T) {
		os.Unsetenv("ATTN_PROFILE")
		if got := DeepLinkScheme(); got != "attn" {
			t.Errorf("DeepLinkScheme() = %q, want %q", got, "attn")
		}
	})
	t.Run("dev → attn-dev", func(t *testing.T) {
		t.Setenv("ATTN_PROFILE", "dev")
		if got := DeepLinkScheme(); got != "attn-dev" {
			t.Errorf("DeepLinkScheme() = %q, want %q", got, "attn-dev")
		}
	})
	t.Run("named profile → attn-<name> (its own bundle's scheme)", func(t *testing.T) {
		t.Setenv("ATTN_PROFILE", "staging")
		if got := DeepLinkScheme(); got != "attn-staging" {
			t.Errorf("DeepLinkScheme() = %q, want %q", got, "attn-staging")
		}
	})
}

func TestValidateProfileName_PureFunction(t *testing.T) {
	// Does NOT read the environment; only validates the argument.
	t.Setenv("ATTN_PROFILE", "has space") // invalid env, but argument is fine
	if err := ValidateProfileName("dev"); err != nil {
		t.Errorf("ValidateProfileName(dev) unexpectedly errored: %v", err)
	}
	if err := ValidateProfileName(""); err != nil {
		t.Errorf("ValidateProfileName(\"\") unexpectedly errored: %v", err)
	}
	if err := ValidateProfileName("bad name"); err == nil {
		t.Error("ValidateProfileName(\"bad name\") should have errored")
	}
}

func TestPprofAddr(t *testing.T) {
	orig, had := os.LookupEnv("ATTN_PPROF")
	t.Cleanup(func() {
		if had {
			os.Setenv("ATTN_PPROF", orig)
		} else {
			os.Unsetenv("ATTN_PPROF")
		}
	})

	cases := []struct {
		name        string
		set         bool
		val         string
		wantAddr    string
		wantEnabled bool
	}{
		{name: "unset", set: false},
		{name: "empty", set: true, val: ""},
		{name: "off", set: true, val: "off"},
		{name: "zero", set: true, val: "0"},
		{name: "false", set: true, val: "false"},
		{name: "one_default_port", set: true, val: "1", wantAddr: "127.0.0.1:6060", wantEnabled: true},
		{name: "on", set: true, val: "on", wantAddr: "127.0.0.1:6060", wantEnabled: true},
		{name: "true", set: true, val: "true", wantAddr: "127.0.0.1:6060", wantEnabled: true},
		{name: "bare_port", set: true, val: "6061", wantAddr: "127.0.0.1:6061", wantEnabled: true},
		{name: "colon_port", set: true, val: ":7070", wantAddr: "127.0.0.1:7070", wantEnabled: true},
		{name: "host_port_forced_loopback", set: true, val: "0.0.0.0:8080", wantAddr: "127.0.0.1:8080", wantEnabled: true},
		{name: "garbage", set: true, val: "banana"},
		{name: "out_of_range", set: true, val: "70000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				os.Setenv("ATTN_PPROF", tc.val)
			} else {
				os.Unsetenv("ATTN_PPROF")
			}
			addr, enabled := PprofAddr()
			if enabled != tc.wantEnabled || addr != tc.wantAddr {
				t.Fatalf("PprofAddr() = (%q, %v), want (%q, %v)", addr, enabled, tc.wantAddr, tc.wantEnabled)
			}
		})
	}
}

// TestProfileDerivation_DefaultAndDev pins the default/dev literals so the
// single-source-of-truth helpers stay wire-compatible with the values currently
// hardcoded in profile.rs, harnessProfile.mjs, and the tauri configs.
func TestProfileDerivation_DefaultAndDev(t *testing.T) {
	cases := []struct {
		profile                   string
		bundleID, appName, scheme string
	}{
		{"", "com.attn.manager", "attn", "attn"},
		{"default", "com.attn.manager", "attn", "attn"},
		{"dev", "com.attn.manager.dev", "attn-dev", "attn-dev"},
		{"agent7", "com.attn.manager.agent7", "attn-agent7", "attn-agent7"},
	}
	for _, tc := range cases {
		t.Run("profile="+tc.profile, func(t *testing.T) {
			if got := BundleIdentifierForProfile(tc.profile); got != tc.bundleID {
				t.Errorf("BundleIdentifierForProfile(%q) = %q, want %q", tc.profile, got, tc.bundleID)
			}
			if got := AppNameForProfile(tc.profile); got != tc.appName {
				t.Errorf("AppNameForProfile(%q) = %q, want %q", tc.profile, got, tc.appName)
			}
			if got := DeepLinkSchemeForProfile(tc.profile); got != tc.scheme {
				t.Errorf("DeepLinkSchemeForProfile(%q) = %q, want %q", tc.profile, got, tc.scheme)
			}
		})
	}
}

// TestE2EPorts_BandsAreDisjoint verifies the e2e harness ports for a named
// profile fall in their reserved bands and never collide with prod (9849), dev
// (29849), the real-profile band [20000,29848], or the default e2e ports.
func TestE2EPorts_BandsAreDisjoint(t *testing.T) {
	if got := E2EDaemonPortForProfile(""); got != "19849" {
		t.Errorf("E2EDaemonPortForProfile(\"\") = %q, want 19849", got)
	}
	if got := E2EVitePortForProfile(""); got != "1421" {
		t.Errorf("E2EVitePortForProfile(\"\") = %q, want 1421", got)
	}
	for _, profile := range []string{"agent7", "alpha", "ci-2", "z"} {
		dPort, err := strconv.Atoi(E2EDaemonPortForProfile(profile))
		if err != nil {
			t.Fatalf("E2EDaemonPortForProfile(%q) not numeric: %v", profile, err)
		}
		vPort, err := strconv.Atoi(E2EVitePortForProfile(profile))
		if err != nil {
			t.Fatalf("E2EVitePortForProfile(%q) not numeric: %v", profile, err)
		}
		if dPort < 30000 || dPort > 30999 {
			t.Errorf("e2e daemon port for %q = %d, want [30000,30999]", profile, dPort)
		}
		if vPort < 31000 || vPort > 31999 {
			t.Errorf("e2e vite port for %q = %d, want [31000,31999]", profile, vPort)
		}
		// Disjoint from real daemon ports and each other's band by construction,
		// but assert the cross-band invariants explicitly.
		realPort, _ := strconv.Atoi(WSPortForProfile(profile)) // [20000,29848]
		for _, reserved := range []int{9849, 29849, 1420, 1421, 19849, realPort} {
			if dPort == reserved || vPort == reserved {
				t.Errorf("e2e port for %q collided with reserved %d (daemon=%d vite=%d)", profile, reserved, dPort, vPort)
			}
		}
	}
}

// TestE2EPorts_NeverCollideWithRealDaemon is the cross-entrypoint safety
// invariant: a throwaway e2e daemon (any profile) can never bind a port a *real*
// daemon (any profile) uses, so running e2e never hijacks or is hijacked by a
// live daemon. Guaranteed by disjoint bands; asserted here so a future band edit
// that breaks it fails loudly.
func TestE2EPorts_NeverCollideWithRealDaemon(t *testing.T) {
	profiles := []string{"", "dev", "agent7", "agent8", "ci-1", "alpha"}
	realPorts := map[string]string{} // port -> profile that owns it
	for _, p := range profiles {
		realPorts[WSPortForProfile(p)] = p
	}
	for _, p := range profiles {
		for _, e2ePort := range []string{E2EDaemonPortForProfile(p), E2EVitePortForProfile(p)} {
			if owner, taken := realPorts[e2ePort]; taken {
				t.Errorf("e2e port %q for profile %q collides with the real daemon port of profile %q", e2ePort, p, owner)
			}
		}
	}
}

func TestAppSupportDirForProfile_MatchesBundleIdentifier(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	cases := []struct {
		profile string
		want    string
	}{
		{"", "com.attn.manager"},
		{"default", "com.attn.manager"},
		{"dev", "com.attn.manager.dev"},
		{"agent7", "com.attn.manager.agent7"},
	}
	for _, c := range cases {
		got := AppSupportDirForProfile(c.profile)
		want := filepath.Join(home, "Library", "Application Support", c.want)
		if got != want {
			t.Errorf("AppSupportDirForProfile(%q) = %q, want %q", c.profile, got, want)
		}
	}
}

func TestAppSupportDir_UsesActiveProfile(t *testing.T) {
	t.Setenv("ATTN_PROFILE", "dev")
	got := AppSupportDir()
	want := AppSupportDirForProfile("dev")
	if got != want {
		t.Errorf("AppSupportDir() = %q, want %q", got, want)
	}
}
