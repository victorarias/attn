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
