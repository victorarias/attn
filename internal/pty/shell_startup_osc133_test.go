package pty

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	creackpty "github.com/creack/pty"
)

// Drives a real interactive shell through the startup overlay on a real PTY
// and returns both the arbiter's observations and the raw byte stream, so a
// test can assert the whole chain the poll cannot cover: command-start/end
// state edges with the command line and exit code, and the block table
// grouping the same stream into command blocks.
func runShellIntegrationScenario(t *testing.T, shellPath string, env []string, commands string) ([]Observation, []byte) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	if _, err := os.Stat(shellPath); err != nil {
		t.Skipf("%s unavailable: %v", shellPath, err)
	}

	launch, err := prepareShellPaneLaunch(shellPath, env)
	if err != nil {
		t.Fatalf("prepare shell pane launch: %v", err)
	}
	t.Cleanup(launch.cleanup)
	launch.command.Env = launch.env
	launch.command.Dir = t.TempDir()

	ptmx, err := creackpty.StartWithSize(launch.command, &creackpty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("start %s in PTY: %v", shellPath, err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if launch.command.Process != nil {
			_ = launch.command.Process.Kill()
		}
		_, _ = launch.command.Process.Wait()
	})

	arbiter := newShellSignalArbiter(launch.command.Process.Pid)
	var mu sync.Mutex
	var observations []Observation
	var stream []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				obs := arbiter.ObserveOutput(buf[:n], time.Now())
				mu.Lock()
				observations = append(observations, obs...)
				stream = append(stream, buf[:n]...)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	if _, err := ptmx.Write([]byte(commands)); err != nil {
		t.Fatalf("write commands: %v", err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("shell did not exit; integration output never completed")
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]Observation(nil), observations...), append([]byte(nil), stream...)
}

const integrationProbeCommands = "/bin/echo attn-integration-probe\rfalse\rexit\r"

func requireObservation(t *testing.T, observations []Observation, claim, detail string) {
	t.Helper()
	for _, obs := range observations {
		if obs.Claim == claim && obs.Detail == detail {
			return
		}
	}
	t.Fatalf("no %s/%q observation in %+v", claim, detail, observations)
}

// blocksFromStream runs a raw shell byte stream through the production
// segmenter and block table — the same pieces the worker wires in
// newBlockFeeder — with pure position refs standing in for the tracked grid.
func blocksFromStream(t *testing.T, stream []byte) []AttachBlockData {
	t.Helper()
	freed := 0
	row := 0
	table := newBlockTable()
	t.Cleanup(table.Close)
	seg := &osc133ScanSegmenter{}
	seg.Feed(stream, func(_ []byte, marker *osc133Marker) {
		if marker == nil {
			return
		}
		row++
		table.ApplyMarker(*marker, &fakeBlockRef{x: 0, y: row, freed: &freed}, false)
	})
	return table.SnapshotBlocks()
}

// requireCompletedBlock asserts the stream produced a completed command block
// carrying the given command text and exit code — the block contract the
// injected integrations promise.
func requireCompletedBlock(t *testing.T, blocks []AttachBlockData, command string, exitCode int32) {
	t.Helper()
	for _, b := range blocks {
		if b.Pending || b.Command == nil || b.ExitCode == nil {
			continue
		}
		if *b.Command == command && *b.ExitCode == exitCode {
			return
		}
	}
	t.Fatalf("no completed block command=%q exit=%d in %+v", command, exitCode, blocks)
}

func TestZshIntegrationEmitsCommandEdgesAndBlocks(t *testing.T) {
	root := t.TempDir()
	userZdotdir := filepath.Join(root, "user-zdotdir")
	if err := os.MkdirAll(userZdotdir, 0o755); err != nil {
		t.Fatalf("create user zdotdir: %v", err)
	}
	observations, stream := runShellIntegrationScenario(t, "/bin/zsh", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"TERM=xterm-256color",
	}, integrationProbeCommands)

	// The preexec hook marks the start of every command with its cmdline…
	requireObservation(t, observations, claimBusy, "command started: /bin/echo attn-integration-probe")
	requireObservation(t, observations, claimBusy, "command started: false")
	// …and the precmd hook closes it with the real exit code.
	requireObservation(t, observations, claimNotBusy, "command exited 0")
	requireObservation(t, observations, claimNotBusy, "command exited 1")

	// The same stream through the production block table: commands become
	// completed blocks with command text and exit code.
	blocks := blocksFromStream(t, stream)
	requireCompletedBlock(t, blocks, "/bin/echo attn-integration-probe", 0)
	requireCompletedBlock(t, blocks, "false", 1)
}

func TestBashIntegrationEmitsCommandEdgesAndBlocks(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	if err := os.MkdirAll(userHome, 0o755); err != nil {
		t.Fatalf("create user home: %v", err)
	}
	observations, stream := runShellIntegrationScenario(t, "/bin/bash", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + userHome,
		"TERM=xterm-256color",
	}, integrationProbeCommands)

	// The DEBUG-trap pre-exec works on every bash, macOS's 3.2 included, and
	// carries the command line.
	requireObservation(t, observations, claimBusy, "command started: /bin/echo attn-integration-probe")
	requireObservation(t, observations, claimBusy, "command started: false")
	requireObservation(t, observations, claimNotBusy, "command exited 0")
	requireObservation(t, observations, claimNotBusy, "command exited 1")

	blocks := blocksFromStream(t, stream)
	requireCompletedBlock(t, blocks, "/bin/echo attn-integration-probe", 0)
	requireCompletedBlock(t, blocks, "false", 1)
}

// A DEBUG trap the user already owns is off limits: the integration must
// leave it running and degrade to the PS0 fallback instead of clobbering it.
func TestBashIntegrationLeavesAUserDebugTrapAlone(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	if err := os.MkdirAll(userHome, 0o755); err != nil {
		t.Fatalf("create user home: %v", err)
	}
	trapLog := filepath.Join(root, "trap-log")
	profile := "trap 'echo fired >> \"$ATTN_TEST_TRAP_LOG\"' DEBUG\n"
	if err := os.WriteFile(filepath.Join(userHome, ".bash_profile"), []byte(profile), 0o600); err != nil {
		t.Fatalf("write user bash profile: %v", err)
	}

	observations, _ := runShellIntegrationScenario(t, "/bin/bash", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + userHome,
		"TERM=xterm-256color",
		"ATTN_TEST_TRAP_LOG=" + trapLog,
	}, integrationProbeCommands)

	// The user's trap kept firing after startup…
	logged, err := os.ReadFile(trapLog)
	if err != nil || len(logged) == 0 {
		t.Fatalf("user DEBUG trap did not survive integration: err=%v log=%q", err, logged)
	}
	// …and the integration never took the trap for itself: no cmdline-carrying
	// C marks, which only the trap can produce. (PS0, where bash has it, still
	// emits a bare C; exit codes still arrive via D.)
	for _, obs := range observations {
		if strings.HasPrefix(obs.Detail, "command started: ") {
			t.Fatalf("cmdline mark emitted despite user-owned DEBUG trap: %+v", obs)
		}
	}
	requireObservation(t, observations, claimNotBusy, "command exited 1")
}

// The user's opt-out: with ATTN_NO_SHELL_INTEGRATION set, the overlay never
// sources the integration and no marks are emitted.
func TestShellIntegrationOptOutEmitsNoMarks(t *testing.T) {
	root := t.TempDir()
	userZdotdir := filepath.Join(root, "user-zdotdir")
	if err := os.MkdirAll(userZdotdir, 0o755); err != nil {
		t.Fatalf("create user zdotdir: %v", err)
	}
	observations, _ := runShellIntegrationScenario(t, "/bin/zsh", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"TERM=xterm-256color",
		"ATTN_NO_SHELL_INTEGRATION=1",
	}, integrationProbeCommands)

	for _, obs := range observations {
		if strings.HasPrefix(obs.Detail, "command ") {
			t.Fatalf("opt-out still emitted %+v", obs)
		}
	}
}
