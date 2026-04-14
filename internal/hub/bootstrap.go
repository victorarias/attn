package hub

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
)

const githubRepo = "victorarias/attn"

const remoteDaemonReadyTimeout = 35 * time.Second
const remoteHarnessRootMarker = "/.attn/harness/"

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

	preferSourceBuild := sourceCheckoutAvailable()
	var localBinary string
	binaryUpdated := false
	if remoteVersion != localVersion || preferSourceBuild {
		localBinary, err = b.ensureLocalBinary(ctx, platform, localVersion)
		if err != nil {
			return fmt.Errorf("prepare %s binary for %s: %w", platform.ArtifactName, sshTarget, err)
		}
	}

	shouldInstall := remoteVersion != localVersion
	if !shouldInstall && preferSourceBuild {
		localHash, err := fileSHA256(localBinary)
		if err != nil {
			return fmt.Errorf("hash local binary for %s: %w", sshTarget, err)
		}
		remoteHash, err := b.remoteBinarySHA256(ctx, sshTarget)
		if err != nil {
			return fmt.Errorf("hash remote binary on %s: %w", sshTarget, err)
		}
		shouldInstall = shouldInstallRemoteBinary(localVersion, remoteVersion, preferSourceBuild, localHash, remoteHash)
		if shouldInstall {
			b.logf("remote binary hash mismatch for %s: remote=%s local=%s", sshTarget, remoteHash, localHash)
		}
	}

	if shouldInstall {
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

func shouldInstallRemoteBinary(localVersion, remoteVersion string, preferSourceBuild bool, localHash, remoteHash string) bool {
	if remoteVersion != localVersion {
		return true
	}
	if preferSourceBuild && remoteHash != localHash {
		return true
	}
	return false
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
	script := fmt.Sprintf(`
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
if [ -z "$ATTN_BIN" ] || [ ! -x "$ATTN_BIN" ]; then
  printf NOT_FOUND
  exit 0
fi
"$ATTN_BIN" --version 2>/dev/null || printf NOT_FOUND
`, config.BinaryName(), config.BinaryName())
	out, err := runSSH(ctx, sshTarget, script)
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
	cacheKey, preferSourceBuild, err := b.localBinaryCacheKey(version)
	if err != nil {
		return "", err
	}
	cachePath := filepath.Join(home, ".attn", "remotes", "binaries", cacheKey, platform.ArtifactName)
	if info, err := os.Stat(cachePath); err == nil && info.Mode().IsRegular() {
		return cachePath, nil
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "", err
	}

	if preferSourceBuild {
		if err := b.buildBinaryFromSource(ctx, platform, version, cachePath); err == nil {
			return cachePath, nil
		} else {
			b.logf("source build failed for %s %s: %v", cacheKey, platform.ArtifactName, err)
		}
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

func sourceCheckoutAvailable() bool {
	root := sourceRoot()
	if root == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return false
	}
	return true
}

func localBinaryFingerprint() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fileSHA256(exe)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (b *Bootstrapper) remoteBinarySHA256(ctx context.Context, sshTarget string) (string, error) {
	script := fmt.Sprintf(`
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
if [ -z "$ATTN_BIN" ] || [ ! -f "$ATTN_BIN" ]; then
  printf NOT_FOUND
  exit 0
fi
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$ATTN_BIN" | awk '{print $1}'
  exit 0
fi
if command -v shasum >/dev/null 2>&1; then
  shasum -a 256 "$ATTN_BIN" | awk '{print $1}'
  exit 0
fi
printf NO_HASH_TOOL
`, config.BinaryName(), config.BinaryName())
	out, err := runSSH(ctx, sshTarget, script)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(out)
	switch value {
	case "", "NOT_FOUND":
		return "", nil
	case "NO_HASH_TOOL":
		return "", fmt.Errorf("remote host has neither sha256sum nor shasum")
	default:
		return value, nil
	}
}

func (b *Bootstrapper) localBinaryCacheKey(version string) (string, bool, error) {
	cacheVersion := strings.TrimSpace(version)
	if cacheVersion == "" {
		cacheVersion = "unknown"
	}
	if !sourceCheckoutAvailable() {
		return cacheVersion, false, nil
	}

	fingerprint, err := localBinaryFingerprint()
	if err != nil {
		return "", false, fmt.Errorf("fingerprint local binary: %w", err)
	}
	if len(fingerprint) > 12 {
		fingerprint = fingerprint[:12]
	}
	return fmt.Sprintf("source-%s-%s", cacheVersion, fingerprint), true, nil
}

func zigTargetForPlatform(platform RemotePlatform) (string, error) {
	switch {
	case platform.GOOS == "linux" && platform.GOARCH == "amd64":
		return "x86_64-linux-gnu", nil
	case platform.GOOS == "linux" && platform.GOARCH == "arm64":
		return "aarch64-linux-gnu", nil
	default:
		return "", fmt.Errorf("unsupported zig target for %s/%s", platform.GOOS, platform.GOARCH)
	}
}

func (b *Bootstrapper) buildBinaryFromSource(ctx context.Context, platform RemotePlatform, version, outputPath string) error {
	root := sourceRoot()
	if root == "" {
		return fmt.Errorf("source checkout not available for fallback build")
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return fmt.Errorf("source checkout not available for fallback build")
	}

	ldflags := "-X github.com/victorarias/attn/internal/buildinfo.Version=" + version
	if fp := buildinfo.SourceFingerprint; fp != "" && fp != "unknown" {
		ldflags += " -X github.com/victorarias/attn/internal/buildinfo.SourceFingerprint=" + fp
	}
	if gc := buildinfo.GitCommit; gc != "" && gc != "unknown" {
		ldflags += " -X github.com/victorarias/attn/internal/buildinfo.GitCommit=" + gc
	}
	cmd := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-ldflags",
		ldflags,
		"-o",
		outputPath,
		"./cmd/attn",
	)
	cmd.Dir = root
	env := append(os.Environ(), "GOOS="+platform.GOOS, "GOARCH="+platform.GOARCH)
	if platform.GOOS == "linux" {
		env = append(env, "CGO_ENABLED=1")
		if runtime.GOOS != "linux" {
			if _, err := exec.LookPath("zig"); err != nil {
				return fmt.Errorf(
					"zig is required to cross-compile %s with cgo from %s; install zig or use the published Linux artifact",
					platform.ArtifactName,
					runtime.GOOS,
				)
			}
			target, err := zigTargetForPlatform(platform)
			if err != nil {
				return err
			}
			env = append(env,
				"CC=zig cc -target "+target,
				"CXX=zig c++ -target "+target,
			)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cross-compile %s: %s", platform.ArtifactName, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveRemoteInstallPath(remoteHome, override string) string {
	path := strings.TrimSpace(override)
	if path == "" {
		return filepath.Join(remoteHome, ".local", "bin", config.BinaryName())
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(remoteHome, path[2:])
	}
	return path
}

func (b *Bootstrapper) installRemoteBinary(ctx context.Context, sshTarget, localBinary string) error {
	remoteHome, err := runSSH(ctx, sshTarget, `printf '%s' "$HOME"`)
	if err != nil {
		return err
	}
	remoteInstallPath := resolveRemoteInstallPath(strings.TrimSpace(remoteHome), os.Getenv("ATTN_REMOTE_ATTN_BIN"))
	remoteInstallDir := filepath.Dir(remoteInstallPath)
	remoteTmpPath := filepath.Join("/tmp", fmt.Sprintf("%s.%d.%d.tmp", filepath.Base(remoteInstallPath), os.Getpid(), time.Now().UnixNano()))
	if _, err := runSSH(ctx, sshTarget, fmt.Sprintf("mkdir -p %s ~/.attn", shellQuote(remoteInstallDir))); err != nil {
		return err
	}
	file, err := os.Open(localBinary)
	if err != nil {
		return fmt.Errorf("open local binary: %w", err)
	}
	defer file.Close()
	cmd := exec.CommandContext(
		ctx,
		"ssh",
		append(sshBaseArgs(sshTarget), remoteShellCommand(fmt.Sprintf("cat > %s", shellQuote(remoteTmpPath))))...,
	)
	cmd.Stdin = file
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy binary over ssh: %s", strings.TrimSpace(string(out)))
	}
	if probe, probeErr := runSSH(
		ctx,
		sshTarget,
		fmt.Sprintf("if [ -f %s ]; then wc -c < %s; else printf MISSING; fi", shellQuote(remoteTmpPath), shellQuote(remoteTmpPath)),
	); probeErr == nil {
		b.logf("remote binary upload probe: target=%s tmp=%s result=%s", sshTarget, remoteTmpPath, strings.TrimSpace(probe))
	}
	_, err = runSSH(
		ctx,
		sshTarget,
		fmt.Sprintf(
			"install -m 755 %s %s && rm -f %s",
			shellQuote(remoteTmpPath),
			shellQuote(remoteInstallPath),
			shellQuote(remoteTmpPath),
		),
	)
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

func isRemoteHarnessOverridePath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, remoteHarnessRootMarker) || strings.Contains(trimmed, "~"+remoteHarnessRootMarker)
}

func remoteHarnessCleanupEnabled() bool {
	return isRemoteHarnessOverridePath(os.Getenv("ATTN_REMOTE_SOCKET_PATH")) ||
		isRemoteHarnessOverridePath(os.Getenv("ATTN_REMOTE_DB_PATH")) ||
		isRemoteHarnessOverridePath(os.Getenv("ATTN_REMOTE_ATTN_BIN"))
}

type remoteDaemonState struct {
	Running  bool
	Starting bool
	Stale    bool
	PID      string
}

func (b *Bootstrapper) probeRemoteDaemon(ctx context.Context, sshTarget string) (remoteDaemonState, error) {
	script := remoteSocketConfigScript() + `
listener_pid="$(ss -H -ltnp "( sport = :${ATTN_WS_PORT:-9849} )" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n 1)"
if [ -n "$listener_pid" ]; then
  printf 'running %s\n' "$listener_pid"
  exit 0
fi
if [ -S "$socket_path" ] && [ -f "$pid_path" ]; then
  pid="$(cat "$pid_path" 2>/dev/null || true)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    printf 'starting %%s\n' "$pid"
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
	case "starting":
		state := remoteDaemonState{Starting: true}
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

	if (state.Running || state.Starting) && binaryUpdated {
		if err := b.restartRemoteDaemon(ctx, sshTarget, state.PID); err != nil {
			return err
		}
		state = remoteDaemonState{}
	}

	if !state.Running && !state.Starting {
		if err := b.startRemoteDaemon(ctx, sshTarget); err != nil {
			return err
		}
	}

	deadline := time.Now().Add(remoteDaemonReadyTimeout)
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
	launchScript := fmt.Sprintf(`
mkdir -p ~/.attn
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
if [ -z "$ATTN_BIN" ] || [ ! -x "$ATTN_BIN" ]; then
  printf 'missing attn binary\n' >&2
  exit 127
fi
nohup setsid "$ATTN_BIN" daemon </dev/null >>~/.attn/daemon.log 2>&1 &
`, config.BinaryName(), config.BinaryName())
	_, err := runSSH(
		ctx,
		sshTarget,
		launchScript,
	)
	return err
}

func (b *Bootstrapper) StopRemoteDaemon(ctx context.Context, sshTarget string) error {
	if !remoteHarnessCleanupEnabled() {
		return nil
	}
	stopScript := remoteSocketConfigScript() + `
listener_pid="$(ss -H -ltnp "( sport = :${ATTN_WS_PORT:-9849} )" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n 1)"
pid_file_pid=""
if [ -f "$pid_path" ]; then
  pid_file_pid="$(cat "$pid_path" 2>/dev/null || true)"
fi
seen_pids=""
for pid in "$listener_pid" "$pid_file_pid"; do
  [ -n "$pid" ] || continue
  case " $seen_pids " in
    *" $pid "*) continue ;;
  esac
  seen_pids="$seen_pids $pid"
  kill "$pid" 2>/dev/null || true
done
sleep 0.5
for pid in $seen_pids; do
  kill -0 "$pid" 2>/dev/null || continue
  kill -9 "$pid" 2>/dev/null || true
done
rm -f "$socket_path" "$pid_path"
`
	_, err := runSSH(ctx, sshTarget, stopScript)
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
