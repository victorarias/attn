package hub

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/config"
)

const githubRepo = "victorarias/attn"

type RemotePlatform struct {
	GOOS         string
	GOARCH       string
	ArtifactName string
}

type Bootstrapper struct {
	logf func(format string, args ...interface{})

	versionOnce sync.Once
	version     string
	versionErr  error
}

func NewBootstrapper(logf func(format string, args ...interface{})) *Bootstrapper {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return &Bootstrapper{logf: logf}
}

func (b *Bootstrapper) EnsureRemoteReady(ctx context.Context, sshTarget string) error {
	platform, err := b.detectRemotePlatform(ctx, sshTarget)
	if err != nil {
		return fmt.Errorf("detect remote platform for %s: %w", sshTarget, err)
	}

	localVersion, err := b.localVersion(ctx)
	if err != nil {
		return fmt.Errorf("determine local version: %w", err)
	}

	remoteVersion, err := b.remoteVersion(ctx, sshTarget)
	if err != nil {
		return fmt.Errorf("check remote version on %s: %w", sshTarget, err)
	}

	binaryUpdated := false
	if remoteVersion != localVersion {
		localBinary, err := b.ensureLocalBinary(ctx, platform, localVersion)
		if err != nil {
			return fmt.Errorf("prepare %s binary for %s: %w", platform.ArtifactName, sshTarget, err)
		}
		if err := b.installRemoteBinary(ctx, sshTarget, localBinary); err != nil {
			return fmt.Errorf("install attn on %s: %w", sshTarget, err)
		}
		binaryUpdated = true
	}

	if err := b.ensureRemoteDaemonRunning(ctx, sshTarget, binaryUpdated); err != nil {
		return fmt.Errorf("ensure remote daemon on %s: %w", sshTarget, err)
	}
	return nil
}

func (b *Bootstrapper) detectRemotePlatform(ctx context.Context, sshTarget string) (RemotePlatform, error) {
	out, err := runSSH(ctx, sshTarget, "uname -sm")
	if err != nil {
		return RemotePlatform{}, err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 2 {
		return RemotePlatform{}, fmt.Errorf("unexpected uname output: %q", out)
	}
	if fields[0] != "Linux" {
		return RemotePlatform{}, fmt.Errorf("unsupported platform %q (Linux only)", out)
	}

	switch fields[1] {
	case "x86_64", "amd64":
		return RemotePlatform{GOOS: "linux", GOARCH: "amd64", ArtifactName: "attn-linux-amd64"}, nil
	case "aarch64", "arm64":
		return RemotePlatform{GOOS: "linux", GOARCH: "arm64", ArtifactName: "attn-linux-arm64"}, nil
	default:
		return RemotePlatform{}, fmt.Errorf("unsupported architecture %q", fields[1])
	}
}

func (b *Bootstrapper) remoteVersion(ctx context.Context, sshTarget string) (string, error) {
	out, err := runSSH(ctx, sshTarget, remoteAttnCommand("--version")+" 2>/dev/null || printf NOT_FOUND")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "NOT_FOUND" {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

func (b *Bootstrapper) localVersion(ctx context.Context) (string, error) {
	b.versionOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			b.versionErr = err
			return
		}
		out, err := exec.CommandContext(ctx, exe, "--version").Output()
		if err != nil {
			b.versionErr = err
			return
		}
		b.version = strings.TrimSpace(string(out))
		if b.version == "" {
			b.versionErr = fmt.Errorf("empty version output")
		}
	})
	return b.version, b.versionErr
}

func (b *Bootstrapper) ensureLocalBinary(ctx context.Context, platform RemotePlatform, version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cachePath := filepath.Join(home, ".attn", "remotes", "binaries", version, platform.ArtifactName)
	if info, err := os.Stat(cachePath); err == nil && info.Mode().IsRegular() {
		return cachePath, nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "", err
	}

	if version != "" && version != "dev" {
		if err := b.downloadReleaseBinary(ctx, version, platform, filepath.Dir(cachePath)); err == nil {
			return cachePath, nil
		} else {
			b.logf("release download failed for %s %s: %v", version, platform.ArtifactName, err)
		}
	}

	if err := b.buildBinaryFromSource(ctx, platform, version, cachePath); err != nil {
		return "", err
	}
	return cachePath, nil
}

func (b *Bootstrapper) downloadReleaseBinary(ctx context.Context, version string, platform RemotePlatform, destDir string) error {
	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	cmd := exec.CommandContext(ctx, "gh", "release", "download", tag, "--repo", githubRepo, "--pattern", platform.ArtifactName, "--dir", destDir, "--clobber")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh release download %s: %s", tag, strings.TrimSpace(string(out)))
	}
	return nil
}

func sourceRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func (b *Bootstrapper) buildBinaryFromSource(ctx context.Context, platform RemotePlatform, version, outputPath string) error {
	root := sourceRoot()
	if root == "" {
		return fmt.Errorf("source checkout not available for fallback build")
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return fmt.Errorf("source checkout not available for fallback build")
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.version="+version, "-o", outputPath, "./cmd/attn")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+platform.GOOS, "GOARCH="+platform.GOARCH)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cross-compile %s: %s", platform.ArtifactName, strings.TrimSpace(string(out)))
	}
	return nil
}

func (b *Bootstrapper) installRemoteBinary(ctx context.Context, sshTarget, localBinary string) error {
	if _, err := runSSH(ctx, sshTarget, "mkdir -p ~/.local/bin ~/.attn"); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "scp", localBinary, sshTarget+":~/.local/bin/"+config.BinaryName()+".tmp")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp binary: %s", strings.TrimSpace(string(out)))
	}
	_, err = runSSH(ctx, sshTarget, fmt.Sprintf("chmod +x ~/.local/bin/%s.tmp && mv ~/.local/bin/%s.tmp ~/.local/bin/%s", config.BinaryName(), config.BinaryName(), config.BinaryName()))
	return err
}

func remoteSocketConfigScript() string {
	return `
config_path="${ATTN_CONFIG_PATH:-$HOME/.attn/config.json}"
socket_path="${ATTN_SOCKET_PATH:-}"
if [ -z "$socket_path" ] && [ -f "$config_path" ]; then
  socket_path="$(sed -n 's/.*"socket_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$config_path" | head -n 1)"
fi
if [ -z "$socket_path" ]; then
  socket_path="$HOME/.attn/attn.sock"
fi
case "$socket_path" in
  "~/"*) socket_path="$HOME/${socket_path#~/}" ;;
esac
pid_path="$(dirname "$socket_path")/attn.pid"
`
}

type remoteDaemonState struct {
	Running bool
	Stale   bool
	PID     string
}

func (b *Bootstrapper) probeRemoteDaemon(ctx context.Context, sshTarget string) (remoteDaemonState, error) {
	script := remoteSocketConfigScript() + `
if [ -S "$socket_path" ] && [ -f "$pid_path" ]; then
  pid="$(cat "$pid_path" 2>/dev/null || true)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    printf 'running %%s\n' "$pid"
    exit 0
  fi
  printf 'stale %%s\n' "$pid"
  exit 0
fi
printf 'stopped\n'
`
	out, err := runSSH(ctx, sshTarget, script)
	if err != nil {
		return remoteDaemonState{}, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return remoteDaemonState{}, fmt.Errorf("empty probe response")
	}
	switch fields[0] {
	case "running":
		state := remoteDaemonState{Running: true}
		if len(fields) > 1 {
			state.PID = fields[1]
		}
		return state, nil
	case "stale":
		state := remoteDaemonState{Stale: true}
		if len(fields) > 1 {
			state.PID = fields[1]
		}
		return state, nil
	case "stopped":
		return remoteDaemonState{}, nil
	default:
		return remoteDaemonState{}, fmt.Errorf("unexpected probe response %q", out)
	}
}

func (b *Bootstrapper) ensureRemoteDaemonRunning(ctx context.Context, sshTarget string, binaryUpdated bool) error {
	state, err := b.probeRemoteDaemon(ctx, sshTarget)
	if err != nil {
		return err
	}

	if state.Stale {
		if _, err := runSSH(ctx, sshTarget, remoteSocketConfigScript()+`rm -f "$socket_path" "$pid_path"`); err != nil {
			return err
		}
		state = remoteDaemonState{}
	}

	if state.Running && binaryUpdated {
		if err := b.restartRemoteDaemon(ctx, sshTarget, state.PID); err != nil {
			return err
		}
		state = remoteDaemonState{}
	}

	if !state.Running {
		if err := b.startRemoteDaemon(ctx, sshTarget); err != nil {
			return err
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		current, err := b.probeRemoteDaemon(probeCtx, sshTarget)
		cancel()
		if err == nil && current.Running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become ready")
}

func (b *Bootstrapper) startRemoteDaemon(ctx context.Context, sshTarget string) error {
	_, err := runSSH(ctx, sshTarget, "mkdir -p ~/.attn && nohup setsid "+remoteAttnCommand("daemon")+` </dev/null >>~/.attn/daemon.log 2>&1 &`)
	return err
}

func (b *Bootstrapper) restartRemoteDaemon(ctx context.Context, sshTarget, pid string) error {
	if strings.TrimSpace(pid) != "" {
		_, _ = runSSH(ctx, sshTarget, fmt.Sprintf("kill %s 2>/dev/null || true", shellQuote(pid)))
		time.Sleep(500 * time.Millisecond)
		_, _ = runSSH(ctx, sshTarget, fmt.Sprintf("kill -9 %s 2>/dev/null || true", shellQuote(pid)))
	}
	if _, err := runSSH(ctx, sshTarget, remoteSocketConfigScript()+`rm -f "$socket_path" "$pid_path"`); err != nil {
		return err
	}
	return b.startRemoteDaemon(ctx, sshTarget)
}
