package pty

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/launchenv"
)

func TestParseNullSeparatedEnv(t *testing.T) {
	input := []byte("FOO=bar\x00EMPTY=\x00INVALID\x00PATH=/bin\x00")
	got := parseNullSeparatedEnv(input)
	want := []string{"FOO=bar", "EMPTY=", "PATH=/bin"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMergeEnvironment_OverlayWins(t *testing.T) {
	base := []string{"FOO=base", "BAR=1"}
	overlay := []string{"FOO=overlay", "BAZ=2"}
	got := mergeEnvironment(base, overlay)

	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "FOO=overlay") {
		t.Fatalf("expected overlay value for FOO in %v", got)
	}
	if strings.Contains(joined, "FOO=base") {
		t.Fatalf("did not expect base value for FOO in %v", got)
	}
	if !strings.Contains(joined, "BAR=1") || !strings.Contains(joined, "BAZ=2") {
		t.Fatalf("missing expected keys in %v", got)
	}
}

func TestPreferredShellCandidates(t *testing.T) {
	got := preferredShellCandidates("/opt/homebrew/bin/fish")
	if len(got) == 0 {
		t.Fatal("expected non-empty candidate list")
	}
	if got[0] != "/opt/homebrew/bin/fish" {
		t.Fatalf("first candidate = %q, want login shell", got[0])
	}
	seen := map[string]struct{}{}
	for _, shell := range got {
		if _, ok := seen[shell]; ok {
			t.Fatalf("duplicate shell candidate %q in %v", shell, got)
		}
		seen[shell] = struct{}{}
	}
	if _, ok := seen["/bin/sh"]; !ok {
		t.Fatalf("expected /bin/sh fallback in %v", got)
	}
	if runtime.GOOS == "darwin" {
		if _, ok := seen["/bin/zsh"]; !ok {
			t.Fatalf("expected /bin/zsh fallback on darwin in %v", got)
		}
	}
}

func TestShouldFallbackShell(t *testing.T) {
	if !shouldFallbackShell(fmt.Errorf("wrapped: %w", syscall.EPERM)) {
		t.Fatal("expected EPERM to trigger fallback")
	}
	if shouldFallbackShell(syscall.EINVAL) {
		t.Fatal("expected EINVAL to not trigger fallback")
	}
}

func TestResolveAttnPath_PrefersEnvWrapperPath(t *testing.T) {
	tmpDir := t.TempDir()
	wrapperPath := filepath.Join(tmpDir, "attn-wrapper")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write wrapperPath: %v", err)
	}

	old := os.Getenv("ATTN_WRAPPER_PATH")
	t.Setenv("ATTN_WRAPPER_PATH", wrapperPath)
	t.Cleanup(func() {
		_ = os.Setenv("ATTN_WRAPPER_PATH", old)
	})

	got := launchenv.ActiveAttnExecutable()
	if got != wrapperPath {
		t.Fatalf("resolveAttnPath() = %q, want %q", got, wrapperPath)
	}
}

func TestBuildSpawnEnv_SetsWrapperPath(t *testing.T) {
	env := buildSpawnEnv("", SpawnOptions{ID: "session-1"}, "codex", "/tmp/attn-wrapper", nil)

	found := false
	for _, entry := range env {
		if entry == "ATTN_WRAPPER_PATH=/tmp/attn-wrapper" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ATTN_WRAPPER_PATH in env, got %v", env)
	}
}

func TestBuildSpawnEnv_WorkerLaunchPinsOverrideCachedShellEnvironment(t *testing.T) {
	t.Setenv("ATTN_PTY_WORKER", "1")
	t.Setenv("ATTN_MODEL", "automation-model")
	t.Setenv("ATTN_EFFORT", "low")
	env := buildSpawnEnv("", SpawnOptions{
		ID:            "session-1",
		LoginShellEnv: []string{"ATTN_MODEL=parent-model", "ATTN_EFFORT=medium"},
	}, "codex", "/tmp/attn", nil)
	if got, _ := lookupEnv(env, "ATTN_MODEL"); got != "automation-model" {
		t.Fatalf("ATTN_MODEL = %q, want automation-model", got)
	}
	if got, _ := lookupEnv(env, "ATTN_EFFORT"); got != "low" {
		t.Fatalf("ATTN_EFFORT = %q, want low", got)
	}
	if _, ok := lookupEnv(env, "ATTN_PTY_WORKER"); ok {
		t.Fatal("ATTN_PTY_WORKER leaked into the launched session")
	}
}

func TestBuildSpawnEnv_EmbeddedLaunchPinsOverrideInheritedEnvironment(t *testing.T) {
	t.Setenv("ATTN_MODEL", "parent-model")
	t.Setenv("ATTN_EFFORT", "medium")
	t.Setenv("ATTN_AUTO_APPROVE", "parent-value")
	env := buildSpawnEnv("", SpawnOptions{
		ID: "session-1", LoginShellEnv: []string{"ATTN_MODEL=login-model"},
		AutoApprove: true, TrustWorkingDirectory: true, Model: "automation-model", Effort: "low",
	}, "codex", "/tmp/attn", nil)
	for key, want := range map[string]string{
		"ATTN_AUTO_APPROVE": "1", "ATTN_TRUST_WORKING_DIRECTORY": "1",
		"ATTN_MODEL": "automation-model", "ATTN_EFFORT": "low",
	} {
		if got, _ := lookupEnv(env, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestBuildSpawnEnv_UnattendedContractOverridesInheritedWorkerAndLoginValues(t *testing.T) {
	t.Setenv("ATTN_PTY_WORKER", "1")
	for key, value := range map[string]string{
		"ATTN_AUTO_APPROVE": "parent", "ATTN_TRUST_WORKING_DIRECTORY": "parent",
		"ATTN_MODEL": "parent-model", "ATTN_EFFORT": "medium",
		"ATTN_WORKFLOW_GUIDANCE_ENABLED": "1", "ATTN_CHIEF_AUTO_COMPACT_WINDOW": "12345",
	} {
		t.Setenv(key, value)
	}
	spec := launchcontract.UnattendedLaunchSpec{
		Agent: "codex", Model: "exact-model", Effort: "high",
		ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAutoReview,
		DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh,
	}
	env := buildSpawnEnv("", SpawnOptions{
		ID: "session-1", UnattendedLaunch: spec,
		LoginShellEnv: []string{"ATTN_MODEL=login-model", "ATTN_EFFORT=low", "ATTN_AUTO_APPROVE=login"},
	}, "codex", "/tmp/attn", nil)
	for key, want := range map[string]string{
		"ATTN_AUTO_APPROVE": "1", "ATTN_TRUST_WORKING_DIRECTORY": "1",
		"ATTN_MODEL": "exact-model", "ATTN_EFFORT": "high",
		"ATTN_WORKFLOW_GUIDANCE_ENABLED": "1", "ATTN_CHIEF_AUTO_COMPACT_WINDOW": "12345",
	} {
		if got, _ := lookupEnv(env, key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestBuildSpawnEnv_SetsAttnPresence(t *testing.T) {
	env := buildSpawnEnv("", SpawnOptions{ID: "session-1"}, "codex", "/tmp/attn-wrapper", nil)

	for _, expected := range []string{"ATTN_INSIDE_APP=1", "ATTN_SESSION_ID=session-1"} {
		found := false
		for _, entry := range env {
			if entry == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s in env, got %v", expected, env)
		}
	}
}

func TestBuildSpawnEnv_PutsActiveAttnFirstForAgentsAndShells(t *testing.T) {
	profileDir := filepath.Join(t.TempDir(), "attn-profile")
	wrapperPath := filepath.Join(profileDir, "attn")
	staleDir := filepath.Join(t.TempDir(), "stale-attn")
	otherDir := filepath.Join(t.TempDir(), "other-tools")
	loginPath := strings.Join([]string{staleDir, profileDir, otherDir, profileDir}, string(os.PathListSeparator))

	t.Setenv("ATTN_SESSION_ID", "inherited-session")
	t.Setenv("ATTN_AGENT", "inherited-agent")
	for _, agent := range []string{"codex", "shell"} {
		t.Run(agent, func(t *testing.T) {
			env := buildSpawnEnv("", SpawnOptions{
				ID:            "managed-session",
				LoginShellEnv: []string{"PATH=" + loginPath},
			}, agent, wrapperPath, nil)

			path := envValue(t, env, "PATH")
			wantPath := strings.Join([]string{profileDir, staleDir, otherDir}, string(os.PathListSeparator))
			if path != wantPath {
				t.Fatalf("PATH = %q, want active profile first with duplicate removed: %q", path, wantPath)
			}

			if agent == "shell" {
				for _, key := range []string{"ATTN_SESSION_ID", "ATTN_AGENT"} {
					if got := envValue(t, env, key); got != "" {
						t.Fatalf("shell pane inherited managed identity %s=%q", key, got)
					}
				}
				return
			}
			if got := envValue(t, env, "ATTN_SESSION_ID"); got != "managed-session" {
				t.Fatalf("managed agent session id = %q, want managed-session", got)
			}
			if got := envValue(t, env, "ATTN_AGENT"); got != "codex" {
				t.Fatalf("managed agent = %q, want codex", got)
			}
		})
	}
}

func envValue(t *testing.T, env []string, key string) string {
	t.Helper()
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func TestBuildSpawnEnv_DoesNotSetAgentExecutableForDefaultBinary(t *testing.T) {
	env := buildSpawnEnv("", SpawnOptions{ID: "session-1", Executable: "codex"}, "codex", "/tmp/attn-wrapper", nil)
	for _, entry := range env {
		if strings.HasPrefix(entry, "ATTN_CODEX_EXECUTABLE=") {
			t.Fatalf("did not expect ATTN_CODEX_EXECUTABLE for default binary, got %v", env)
		}
	}
}

func TestBuildSpawnEnv_SetsAgentExecutableForExplicitOverride(t *testing.T) {
	env := buildSpawnEnv("", SpawnOptions{ID: "session-1", Executable: "/custom/codex"}, "codex", "/tmp/attn-wrapper", nil)
	found := false
	for _, entry := range env {
		if entry == "ATTN_CODEX_EXECUTABLE=/custom/codex" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ATTN_CODEX_EXECUTABLE override in env, got %v", env)
	}
}

func TestBuildSpawnCommand_UsesExternalPluginDriverArgvDirectly(t *testing.T) {
	cmd := buildSpawnCommand(SpawnOptions{
		ExternalCommand: []string{"snipe", "--session-id", "session-1"},
	}, "snipe", "/bin/zsh", "/tmp/attn-wrapper", nil)
	if got := cmd.Args; len(got) != 3 || got[0] != "snipe" || got[2] != "session-1" {
		t.Fatalf("cmd args=%v, want external driver argv", got)
	}
}

func TestBuildSpawnCommand_ResolvesExternalPluginCommandFromLaunchPath(t *testing.T) {
	tmpDir := t.TempDir()
	commandPath := filepath.Join(tmpDir, "snipe")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write plugin command: %v", err)
	}

	cmd := buildSpawnCommand(SpawnOptions{
		ExternalCommand: []string{"snipe", "--session-id", "session-1"},
	}, "snipe", "/bin/zsh", "/tmp/attn-wrapper", []string{"PATH=" + tmpDir})
	if cmd.Path != commandPath {
		t.Fatalf("cmd.Path=%q, want command resolved from effective PATH %q", cmd.Path, commandPath)
	}
}

func TestBuildSpawnCommand_ReassertsActiveAttnPathAfterLoginStartup(t *testing.T) {
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	staleDir := filepath.Join(root, "stale")
	resultPath := filepath.Join(root, "resolved-attn")
	for _, dir := range []string{activeDir, staleDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	activeAttn := filepath.Join(activeDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\ncommand -v attn > \"$RESULT_PATH\"\n"), 0o755); err != nil {
		t.Fatalf("write active attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "attn"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write stale attn: %v", err)
	}

	loginShell := filepath.Join(root, "login-shell")
	loginShellScript := `#!/bin/sh
if [ "$1" = "-l" ]; then
  shift
  PATH="$STALE_ATTN_DIR:$PATH"
  export PATH
fi
if [ "$1" = "-c" ]; then
  shift
  exec /bin/sh -c "$1"
fi
if [ "$1" = "-i" ]; then
  command -v attn > "$RESULT_PATH"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(loginShell, []byte(loginShellScript), 0o755); err != nil {
		t.Fatalf("write login shell: %v", err)
	}

	env := []string{
		"PATH=" + activeDir + string(os.PathListSeparator) + staleDir,
		"STALE_ATTN_DIR=" + staleDir,
		"RESULT_PATH=" + resultPath,
	}
	cmd := buildSpawnCommand(SpawnOptions{}, "codex", loginShell, activeAttn, env)
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		t.Fatalf("run managed agent command: %v", err)
	}
	resolved, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read resolved attn: %v", err)
	}
	if got := strings.TrimSpace(string(resolved)); got != activeAttn {
		t.Fatalf("bare attn resolved to %q after login startup, want active wrapper %q", got, activeAttn)
	}
}

func TestPrepareShellPaneLaunch_ReassertsPathAfterInteractiveLoginStartup(t *testing.T) {
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	staleDir := filepath.Join(root, "stale")
	userZdotdir := filepath.Join(root, "user-zdotdir")
	resultPath := filepath.Join(root, "resolved-attn")
	for _, dir := range []string{activeDir, staleDir, userZdotdir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	activeAttn := filepath.Join(activeDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\ncommand -v attn > \"$RESULT_PATH\"\n"), 0o755); err != nil {
		t.Fatalf("write active attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "attn"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write stale attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userZdotdir, ".zshrc"), []byte("PATH=\"$STALE_ATTN_DIR:$PATH\"\nexport PATH\n"), 0o600); err != nil {
		t.Fatalf("write user zshrc: %v", err)
	}

	loginShell := filepath.Join(root, "zsh")
	loginShellScript := `#!/bin/sh
[ "$1" = "-l" ] || exit 2
[ "$#" = 1 ] || exit 3
for file in .zshenv .zprofile .zshrc .zlogin; do
  if [ -r "$ZDOTDIR/$file" ]; then
    . "$ZDOTDIR/$file"
  fi
done
[ "$ZDOTDIR" = "$EXPECTED_ZDOTDIR" ] || exit 4
command -v attn > "$RESULT_PATH"
`
	if err := os.WriteFile(loginShell, []byte(loginShellScript), 0o755); err != nil {
		t.Fatalf("write fake zsh: %v", err)
	}

	env := []string{
		"PATH=" + activeDir + string(os.PathListSeparator) + staleDir,
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"STALE_ATTN_DIR=" + staleDir,
		"RESULT_PATH=" + resultPath,
		"EXPECTED_ZDOTDIR=" + userZdotdir,
	}
	launch, err := prepareShellPaneLaunch(loginShell, env)
	if err != nil {
		t.Fatalf("prepare shell pane launch: %v", err)
	}
	defer launch.cleanup()
	launch.command.Env = launch.env
	if err := launch.command.Run(); err != nil {
		t.Fatalf("run shell pane: %v", err)
	}
	resolved, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read resolved attn: %v", err)
	}
	if got := strings.TrimSpace(string(resolved)); got != activeAttn {
		t.Fatalf("bare attn resolved to %q after interactive login startup, want active wrapper %q", got, activeAttn)
	}
}

func TestPrepareShellPaneLaunch_ReassertsPathAfterRealZshStartup(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("attn supports macOS; this exercises the system zsh startup contract")
	}
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	staleDir := filepath.Join(root, "stale")
	userZdotdir := filepath.Join(root, "user-zdotdir")
	resultPath := filepath.Join(root, "resolved-attn")
	for _, dir := range []string{activeDir, staleDir, userZdotdir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	activeAttn := filepath.Join(activeDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write active attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "attn"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stale attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userZdotdir, ".zshrc"), []byte("path=(\"$STALE_ATTN_DIR\" $path)\n"), 0o600); err != nil {
		t.Fatalf("write user zshrc: %v", err)
	}

	env := []string{
		"PATH=" + activeDir + string(os.PathListSeparator) + staleDir + string(os.PathListSeparator) + "/usr/bin:/bin",
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"STALE_ATTN_DIR=" + staleDir,
		"RESULT_PATH=" + resultPath,
	}
	launch, err := prepareShellPaneLaunch("/bin/zsh", env)
	if err != nil {
		t.Fatalf("prepare shell pane launch: %v", err)
	}
	defer launch.cleanup()
	launch.command = exec.Command("/bin/zsh", "-l", "-i", "-c", "[[ -o login ]] || exit 8; command -v attn > \"$RESULT_PATH\"")
	launch.command.Env = launch.env
	if output, err := launch.command.CombinedOutput(); err != nil {
		t.Fatalf("run real zsh startup: %v\n%s", err, output)
	}
	resolved, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read resolved attn: %v", err)
	}
	if got := strings.TrimSpace(string(resolved)); got != activeAttn {
		t.Fatalf("bare attn resolved to %q after real zsh startup, want active wrapper %q", got, activeAttn)
	}
}

func TestPrepareShellPaneLaunch_ReassertsPathAfterBashRCStartup(t *testing.T) {
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	staleDir := filepath.Join(root, "stale")
	userHome := filepath.Join(root, "home")
	resultPath := filepath.Join(root, "resolved-attn")
	for _, dir := range []string{activeDir, staleDir, userHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	activeAttn := filepath.Join(activeDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\ncommand -v attn > \"$RESULT_PATH\"\n"), 0o755); err != nil {
		t.Fatalf("write active attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "attn"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write stale attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".bashrc"), []byte("PATH=\"$STALE_ATTN_DIR:$PATH\"\nexport PATH\n"), 0o600); err != nil {
		t.Fatalf("write user bashrc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".bash_profile"), []byte(". \"$HOME/.bashrc\"\n"), 0o600); err != nil {
		t.Fatalf("write user bash profile: %v", err)
	}

	loginShell := filepath.Join(root, "bash")
	loginShellScript := `#!/bin/sh
[ "$1" = "-l" ] || exit 2
[ "$#" = 1 ] || exit 3
. "$HOME/.bash_profile"
[ "$HOME" = "$EXPECTED_HOME" ] || exit 4
command -v attn > "$RESULT_PATH"
`
	if err := os.WriteFile(loginShell, []byte(loginShellScript), 0o755); err != nil {
		t.Fatalf("write fake bash: %v", err)
	}

	env := []string{
		"PATH=" + activeDir + string(os.PathListSeparator) + staleDir,
		"HOME=" + userHome,
		"STALE_ATTN_DIR=" + staleDir,
		"RESULT_PATH=" + resultPath,
		"EXPECTED_HOME=" + userHome,
	}
	launch, err := prepareShellPaneLaunch(loginShell, env)
	if err != nil {
		t.Fatalf("prepare shell pane launch: %v", err)
	}
	defer launch.cleanup()
	launch.command.Env = launch.env
	if err := launch.command.Run(); err != nil {
		t.Fatalf("run shell pane: %v", err)
	}
	resolved, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read resolved attn: %v", err)
	}
	if got := strings.TrimSpace(string(resolved)); got != activeAttn {
		t.Fatalf("bare attn resolved to %q after bash rc startup, want active wrapper %q", got, activeAttn)
	}
}

func TestPrepareShellPaneLaunch_ReassertsPathAfterRealBashStartup(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("attn supports macOS; this exercises the system bash startup contract")
	}
	const bashPath = "/bin/bash"
	if _, err := os.Stat(bashPath); err != nil {
		t.Skipf("system bash is unavailable: %v", err)
	}
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	staleDir := filepath.Join(root, "stale")
	userHome := filepath.Join(root, "home")
	resultPath := filepath.Join(root, "resolved-attn")
	for _, dir := range []string{activeDir, staleDir, userHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	activeAttn := filepath.Join(activeDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write active attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "attn"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stale attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".bashrc"), []byte("PATH=\"$STALE_ATTN_DIR:$PATH\"\nexport PATH\n"), 0o600); err != nil {
		t.Fatalf("write user bashrc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userHome, ".bash_profile"), []byte(". \"$HOME/.bashrc\"\n"), 0o600); err != nil {
		t.Fatalf("write user bash profile: %v", err)
	}

	env := []string{
		"PATH=" + activeDir + string(os.PathListSeparator) + staleDir + string(os.PathListSeparator) + "/usr/bin:/bin",
		"HOME=" + userHome,
		"STALE_ATTN_DIR=" + staleDir,
		"RESULT_PATH=" + resultPath,
	}
	launch, err := prepareShellPaneLaunch(bashPath, env)
	if err != nil {
		t.Fatalf("prepare bash pane launch: %v", err)
	}
	defer launch.cleanup()
	args := append([]string(nil), launch.command.Args[1:]...)
	args = append(args, "-i", "-c", "shopt -q login_shell || exit 8; command -v attn > \"$RESULT_PATH\"")
	launch.command = exec.Command(launch.command.Path, args...)
	launch.command.Env = launch.env
	if output, err := launch.command.CombinedOutput(); err != nil {
		t.Fatalf("run real bash startup: %v\n%s", err, output)
	}
	resolved, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read resolved attn: %v", err)
	}
	if got := strings.TrimSpace(string(resolved)); got != activeAttn {
		t.Fatalf("bare attn resolved to %q after real bash startup, want active wrapper %q", got, activeAttn)
	}
}

func TestPrepareShellPaneLaunch_ReassertsPathAfterRealFishStartup(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("attn supports macOS; this exercises the installed fish startup contract")
	}
	fishPath, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	staleDir := filepath.Join(root, "stale")
	configHome := filepath.Join(root, "config")
	resultPath := filepath.Join(root, "resolved-attn")
	for _, dir := range []string{activeDir, staleDir, filepath.Join(configHome, "fish")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	activeAttn := filepath.Join(activeDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write active attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, "attn"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stale attn: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "fish", "config.fish"), []byte("set -gx PATH $STALE_ATTN_DIR $PATH\n"), 0o600); err != nil {
		t.Fatalf("write fish config: %v", err)
	}

	env := []string{
		"PATH=" + activeDir + string(os.PathListSeparator) + staleDir + string(os.PathListSeparator) + "/usr/bin:/bin",
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + configHome,
		"STALE_ATTN_DIR=" + staleDir,
		"RESULT_PATH=" + resultPath,
	}
	launch, err := prepareShellPaneLaunch(fishPath, env)
	if err != nil {
		t.Fatalf("prepare fish pane launch: %v", err)
	}
	defer launch.cleanup()
	args := append([]string(nil), launch.command.Args[1:]...)
	args = append(args, "-i", "-c", "status is-login; or exit 8; command -v attn > $RESULT_PATH")
	launch.command = exec.Command(launch.command.Path, args...)
	launch.command.Env = launch.env
	if output, err := launch.command.CombinedOutput(); err != nil {
		t.Fatalf("run real fish startup: %v\n%s", err, output)
	}
	resolved, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read resolved attn: %v", err)
	}
	if got := strings.TrimSpace(string(resolved)); got != activeAttn {
		t.Fatalf("bare attn resolved to %q after fish startup, want active wrapper %q", got, activeAttn)
	}
}

func TestPrepareShellPaneLaunch_UnknownShellPreservesConfiguredLoginShell(t *testing.T) {
	root := t.TempDir()
	loginShell := filepath.Join(root, "nushell")
	if err := os.WriteFile(loginShell, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write custom login shell: %v", err)
	}
	env := []string{"PATH=/active:/stale", "HOME=" + root}

	launch, err := prepareShellPaneLaunch(loginShell, env)
	if err != nil {
		t.Fatalf("prepare custom shell pane launch: %v", err)
	}
	defer launch.cleanup()
	if launch.command.Path != loginShell {
		t.Fatalf("shell command path = %q, want configured shell %q", launch.command.Path, loginShell)
	}
	if got, want := launch.command.Args, []string{loginShell, "-l"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shell command args = %q, want %q", got, want)
	}
	if got, want := launch.env, env; !reflect.DeepEqual(got, want) {
		t.Fatalf("shell environment = %q, want unchanged %q", got, want)
	}
}

func TestBuildSpawnEnv_AppliesExternalPluginEnvironment(t *testing.T) {
	t.Setenv("SNIPE_BRIDGE", "stale")
	t.Setenv("ATTN_PTY_EXTERNAL_ENV", `["SNIPE_BRIDGE=secret"]`)
	env := buildSpawnEnv("", SpawnOptions{
		ID:          "session-1",
		ExternalEnv: []string{"SNIPE_BRIDGE=ready"},
	}, "snipe", "/tmp/attn-wrapper", nil)
	found := false
	for _, entry := range env {
		if entry == "SNIPE_BRIDGE=ready" {
			found = true
		}
		if strings.HasPrefix(entry, "ATTN_PTY_EXTERNAL_ENV=") {
			t.Fatalf("did not expect worker environment transport in plugin env, got %v", env)
		}
	}
	if !found {
		t.Fatalf("expected external plugin environment override, got %v", env)
	}
}

func TestBuildSpawnEnv_StripsInheritedNoColorFromInteractiveSessions(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	for _, agent := range []string{"shell", "codex"} {
		t.Run(agent, func(t *testing.T) {
			env := buildSpawnEnv(
				"",
				SpawnOptions{ID: "session-1", LoginShellEnv: []string{"NO_COLOR=1"}},
				agent,
				"/tmp/attn-wrapper",
				nil,
			)
			for _, entry := range env {
				if strings.HasPrefix(entry, "NO_COLOR=") {
					t.Fatalf("did not expect NO_COLOR in %s PTY environment, got %v", agent, env)
				}
			}
		})
	}
}

func TestBuildSpawnEnv_PinsTermProgramToGhosttyAndScrubsVersion(t *testing.T) {
	for _, agent := range []string{"shell", "codex"} {
		t.Run(agent, func(t *testing.T) {
			env := buildSpawnEnv(
				"",
				SpawnOptions{
					ID: "session-1",
					// Simulate an inherited TERM_PROGRAM and TERM_PROGRAM_VERSION
					// that should be replaced and scrubbed.
					LoginShellEnv: []string{
						"TERM_PROGRAM=something-else",
						"TERM_PROGRAM_VERSION=1.0.0",
					},
				},
				agent,
				"/tmp/attn-wrapper",
				nil,
			)

			termProgramCount := 0
			termProgramValue := ""
			termProgramVersionFound := false

			for _, entry := range env {
				if strings.HasPrefix(entry, "TERM_PROGRAM=") {
					termProgramCount++
					termProgramValue = entry
				}
				if strings.HasPrefix(entry, "TERM_PROGRAM_VERSION=") {
					termProgramVersionFound = true
				}
			}

			if termProgramCount != 1 {
				t.Fatalf("expected exactly 1 TERM_PROGRAM entry in %s PTY environment, got %d: %v", agent, termProgramCount, env)
			}
			if termProgramValue != "TERM_PROGRAM=ghostty" {
				t.Fatalf("expected TERM_PROGRAM=ghostty in %s PTY environment, got %s", agent, termProgramValue)
			}
			if termProgramVersionFound {
				t.Fatalf("did not expect TERM_PROGRAM_VERSION in %s PTY environment, got %v", agent, env)
			}
		})
	}
}

// TestBuildSpawnEnv_OmitsScrubbedAgentSessionEnv ties the daemon/worker startup
// scrub to the real spawn-env builder: once the process env has been scrubbed
// (as runDaemon/runPTYWorker do before spawning), a per-session agent var that
// leaked into the process must not reappear in any spawned PTY's environment.
func TestBuildSpawnEnv_OmitsScrubbedAgentSessionEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "cbcaa879-leaked")

	// Simulate the daemon/worker scrubbing its inherited env at startup.
	config.ScrubInheritedAgentSessionEnv()

	for _, agent := range []string{"shell", "codex"} {
		t.Run(agent, func(t *testing.T) {
			env := buildSpawnEnv("", SpawnOptions{ID: "session-1"}, agent, "/tmp/attn-wrapper", nil)
			for _, entry := range env {
				if strings.HasPrefix(entry, "CLAUDE_CODE_SESSION_ID=") {
					t.Fatalf("spawned %s PTY leaked CLAUDE_CODE_SESSION_ID: %v", agent, env)
				}
			}
		})
	}
}
