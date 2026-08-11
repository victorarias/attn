package hub

import (
	"strings"
	"testing"
)

func TestShouldInstallRemoteBinary(t *testing.T) {
	tests := []struct {
		name              string
		localVersion      string
		remoteVersion     string
		preferSourceBuild bool
		localHash         string
		remoteHash        string
		want              bool
	}{
		{
			name:              "different version always installs",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.1",
			preferSourceBuild: false,
			want:              true,
		},
		{
			name:              "same version no source build skips install",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.2",
			preferSourceBuild: false,
			localHash:         "abc",
			remoteHash:        "def",
			want:              false,
		},
		{
			name:              "same version source build installs on hash mismatch",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.2",
			preferSourceBuild: true,
			localHash:         "abc",
			remoteHash:        "def",
			want:              true,
		},
		{
			name:              "same version source build skips on matching hash",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.2",
			preferSourceBuild: true,
			localHash:         "abc",
			remoteHash:        "abc",
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldInstallRemoteBinary(tt.localVersion, tt.remoteVersion, tt.preferSourceBuild, tt.localHash, tt.remoteHash)
			if got != tt.want {
				t.Fatalf("shouldInstallRemoteBinary(...) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRemoteHarnessOverridePath(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "/home/victor/.attn/attn.sock", want: false},
		{value: "/home/victor/.attn/harness/run-123/attn.sock", want: true},
		{value: "~/.attn/harness/run-123/bin/attn", want: true},
	}

	for _, tt := range tests {
		if got := isRemoteHarnessOverridePath(tt.value); got != tt.want {
			t.Fatalf("isRemoteHarnessOverridePath(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestRemoteHarnessCleanupEnabled(t *testing.T) {
	t.Setenv("ATTN_REMOTE_SOCKET_PATH", "")
	t.Setenv("ATTN_REMOTE_DB_PATH", "")
	t.Setenv("ATTN_REMOTE_ATTN_BIN", "")
	if remoteHarnessCleanupEnabled() {
		t.Fatal("remoteHarnessCleanupEnabled() = true, want false without harness overrides")
	}

	t.Setenv("ATTN_REMOTE_SOCKET_PATH", "/home/victor/.attn/harness/run-456/attn.sock")
	if !remoteHarnessCleanupEnabled() {
		t.Fatal("remoteHarnessCleanupEnabled() = false, want true with harness socket override")
	}
}

func TestStartRemoteDaemonScript_DefaultProfile(t *testing.T) {
	script := startRemoteDaemonScript("")
	if !strings.Contains(script, `mkdir -p "$HOME/.attn"`) {
		t.Fatalf("default script missing default attn dir: %s", script)
	}
	if !strings.Contains(script, "$HOME/.local/bin/attn") {
		t.Fatalf("default script missing default binary path: %s", script)
	}
	if strings.Contains(script, "$HOME/.local/bin/attn-") {
		t.Fatalf("default script unexpectedly references named-profile binary: %s", script)
	}
	if !strings.Contains(script, `>>"$HOME/.attn"/daemon.log`) {
		t.Fatalf("default script missing default log path: %s", script)
	}
}

func TestStartRemoteDaemonScript_NamedProfile(t *testing.T) {
	script := startRemoteDaemonScript("dev")
	if !strings.Contains(script, "$HOME/.local/bin/attn-dev") {
		t.Fatalf("dev script missing dev binary path: %s", script)
	}
	if !strings.Contains(script, `mkdir -p "$HOME/.attn-${ATTN_PROFILE}"`) {
		t.Fatalf("dev script missing profile-aware data dir: %s", script)
	}
	if !strings.Contains(script, `>>"$HOME/.attn-${ATTN_PROFILE}"/daemon.log`) {
		t.Fatalf("dev script missing profile-aware log path: %s", script)
	}
}

func TestStopRemoteDaemonScript_PortByProfile(t *testing.T) {
	defaultScript := stopRemoteDaemonScript("")
	if !strings.Contains(defaultScript, "${ATTN_WS_PORT:-9849}") {
		t.Fatalf("default stop script should fall back to 9849: %s", defaultScript)
	}

	devScript := stopRemoteDaemonScript("dev")
	if !strings.Contains(devScript, "${ATTN_WS_PORT:-29849}") {
		t.Fatalf("dev stop script should fall back to 29849: %s", devScript)
	}
}

// TestStopRemoteDaemonScript_LeavesPIDFileInPlace pins the invariant that
// stopping a remote daemon never unlinks its PID file. The PID file's flock
// (not its presence on disk), is the sole mutual-exclusion mechanism a
// remote daemon and a concurrent `attn db restore` on that same host share
// (see removeStaleRemoteSocketScript and internal/daemonctl/ensure.go's
// removeStaleSocketFiles for the identical local-daemon invariant).
// Unlinking the pathname here would let a restore holding the old inode's
// flock go uncontended against whatever daemon later creates a fresh inode
// at the same pathname.
func TestStopRemoteDaemonScript_LeavesPIDFileInPlace(t *testing.T) {
	script := stopRemoteDaemonScript("")
	if !strings.Contains(script, `rm -f "$socket_path"`) {
		t.Fatalf("stop script should remove the stale socket: %s", script)
	}
	for _, line := range extractRemovalLines(script) {
		if strings.Contains(line, "pid_path") {
			t.Fatalf("stop script unlinks the PID path, want it left in place: %s", line)
		}
	}
}

// TestRemoveStaleRemoteSocketScript_LeavesPIDFileInPlace pins the same
// invariant for the stale-cleanup script shared by ensureRemoteDaemonRunning
// (stale-socket recovery) and restartRemoteDaemon.
func TestRemoveStaleRemoteSocketScript_LeavesPIDFileInPlace(t *testing.T) {
	script := removeStaleRemoteSocketScript()
	if !strings.Contains(script, `rm -f "$socket_path"`) {
		t.Fatalf("stale-cleanup script should remove the stale socket: %s", script)
	}
	for _, line := range extractRemovalLines(script) {
		if strings.Contains(line, "pid_path") {
			t.Fatalf("stale-cleanup script unlinks the PID path, want it left in place: %s", line)
		}
	}
}

// extractRemovalLines returns the lines of a generated shell script that
// invoke `rm`, so tests can assert on exactly what gets unlinked without
// being fooled by pid_path appearing elsewhere (e.g. reads via `cat`).
func extractRemovalLines(script string) []string {
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "rm ") || strings.Contains(line, "rm\t") || strings.HasPrefix(strings.TrimSpace(line), "rm ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestRemoteSocketConfigScriptHonorsProfileEnv(t *testing.T) {
	script := remoteSocketConfigScript()
	if !strings.Contains(script, `attn_profile="${ATTN_PROFILE:-}"`) {
		t.Fatalf("socket-config script missing ATTN_PROFILE read: %s", script)
	}
	if !strings.Contains(script, `attn_dir="$HOME/.attn-$attn_profile"`) {
		t.Fatalf("socket-config script missing named-profile data dir: %s", script)
	}
	if !strings.Contains(script, `attn_dir="$HOME/.attn"`) {
		t.Fatalf("socket-config script missing default data dir: %s", script)
	}
}

func TestResolveRemoteInstallPath(t *testing.T) {
	cases := []struct {
		remoteHome string
		override   string
		profile    string
		want       string
	}{
		{"/home/v", "", "", "/home/v/.local/bin/attn"},
		{"/home/v", "", "dev", "/home/v/.local/bin/attn-dev"},
		{"/home/v", "/opt/bin/attn", "dev", "/opt/bin/attn"},
		{"/home/v", "~/bin/attn", "", "/home/v/bin/attn"},
	}
	for _, c := range cases {
		got := resolveRemoteInstallPath(c.remoteHome, c.override, c.profile)
		if got != c.want {
			t.Fatalf("resolveRemoteInstallPath(%q,%q,%q) = %q, want %q",
				c.remoteHome, c.override, c.profile, got, c.want)
		}
	}
}

// The app runtime host lands beside the attn binary that starts it, because
// that is the one place the remote daemon looks. A named profile gets its own
// copy: several profile-isolated daemons share one ~/.local/bin, and one shared
// file name would have the newest sync replace another profile's runtime.
func TestRemoteAppRuntimePath(t *testing.T) {
	cases := []struct {
		remoteInstallPath string
		profile           string
		want              string
	}{
		{"/home/v/.local/bin/attn", "", "/home/v/.local/bin/attn-app-runtime"},
		{"/home/v/.local/bin/attn-dev", "dev", "/home/v/.local/bin/attn-app-runtime-dev"},
		{"/home/v/.attn/harness/run-1/bin/attn", "", "/home/v/.attn/harness/run-1/bin/attn-app-runtime"},
	}
	for _, c := range cases {
		if got := remoteAppRuntimePath(c.remoteInstallPath, c.profile); got != c.want {
			t.Errorf("remoteAppRuntimePath(%q, %q) = %q, want %q", c.remoteInstallPath, c.profile, got, c.want)
		}
	}
}

// Go and Bun name the same machine differently, and a target either toolchain
// rejects is a cross-build that fails at the last step of a remote sync.
func TestRemoteLinuxPlatformArtifacts(t *testing.T) {
	cases := []struct {
		machine   string
		goarch    string
		runtime   string
		bunTarget string
	}{
		{"x86_64", "amd64", "attn-app-runtime-linux-amd64", "bun-linux-x64"},
		{"amd64", "amd64", "attn-app-runtime-linux-amd64", "bun-linux-x64"},
		{"aarch64", "arm64", "attn-app-runtime-linux-arm64", "bun-linux-arm64"},
		{"arm64", "arm64", "attn-app-runtime-linux-arm64", "bun-linux-arm64"},
	}
	for _, c := range cases {
		platform, err := remoteLinuxPlatform(c.machine)
		if err != nil {
			t.Fatalf("remoteLinuxPlatform(%q): %v", c.machine, err)
		}
		if platform.GOARCH != c.goarch || platform.RuntimeArtifactName != c.runtime || platform.BunTarget != c.bunTarget {
			t.Errorf("remoteLinuxPlatform(%q) = %+v, want goarch %s runtime %s bun %s",
				c.machine, platform, c.goarch, c.runtime, c.bunTarget)
		}
	}
	if _, err := remoteLinuxPlatform("riscv64"); err == nil {
		t.Fatal("an unsupported architecture was accepted")
	}
}

// A file that is not there yet answers NOT_FOUND rather than failing the sync:
// the first bootstrap of a remote has no copy of anything.
func TestRemoteSHA256ScriptHandlesAMissingFile(t *testing.T) {
	script := remoteSHA256Script(shellQuote("/home/v/.local/bin/attn-app-runtime"))
	if !strings.Contains(script, "printf NOT_FOUND") {
		t.Fatalf("hash script has no missing-file answer: %s", script)
	}
	if !strings.Contains(script, `sha256sum '/home/v/.local/bin/attn-app-runtime'`) {
		t.Fatalf("hash script does not hash the path it was given: %s", script)
	}
	if !strings.Contains(script, "shasum -a 256") {
		t.Fatalf("hash script has no shasum fallback: %s", script)
	}
}
