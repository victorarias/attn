package pty

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/config"
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
