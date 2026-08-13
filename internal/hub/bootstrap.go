package hub

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/enrollment"
)

const githubRepo = "victorarias/attn"

const remoteDaemonReadyTimeout = 35 * time.Second
const remoteHarnessRootMarker = "/.attn/harness/"

type RemotePlatform struct {
	GOOS         string
	GOARCH       string
	ArtifactName string
	// RuntimeArtifactName is the app runtime host for this platform — the Bun
	// sidecar every installed app's handlers execute in. It travels with the
	// binary rather than being fetched on demand: a remote daemon that cannot
	// start its runtime parks every app it hosts.
	RuntimeArtifactName string
	// BunTarget is what `bun build --compile --target=` calls this platform.
	BunTarget string
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

// EnsureRemoteReady installs the binary, records the remote's enrollment, and
// leaves its daemon running. homeDaemonID is the id of the daemon doing the
// dialing — the home this remote becomes an outpost of. It is empty when the
// caller is not a home daemon itself, and then no enrollment is written: a
// daemon that does not own a garden cannot tell another daemon it does.
func (b *Bootstrapper) EnsureRemoteReady(ctx context.Context, sshTarget, profile, homeDaemonID string) error {
	platform, err := b.detectRemotePlatform(ctx, sshTarget, profile)
	if err != nil {
		return fmt.Errorf("detect remote platform for %s: %w", sshTarget, err)
	}

	localVersion, err := b.localVersion(ctx)
	if err != nil {
		return fmt.Errorf("determine local version: %w", err)
	}

	remoteVersion, err := b.remoteVersion(ctx, sshTarget, profile)
	if err != nil {
		return fmt.Errorf("check remote version on %s: %w", sshTarget, err)
	}

	preferSourceBuild := sourceCheckoutAvailable()
	var localBinary string
	binariesUpdated := false
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
		remoteHash, err := b.remoteBinarySHA256(ctx, sshTarget, profile)
		if err != nil {
			return fmt.Errorf("hash remote binary on %s: %w", sshTarget, err)
		}
		shouldInstall = shouldInstallRemoteBinary(localVersion, remoteVersion, preferSourceBuild, localHash, remoteHash)
		if shouldInstall {
			b.logf("remote binary hash mismatch for %s: remote=%s local=%s", sshTarget, remoteHash, localHash)
		}
	}

	remoteInstallPath, err := b.resolveRemoteInstall(ctx, sshTarget, profile)
	if err != nil {
		return fmt.Errorf("resolve the install path on %s: %w", sshTarget, err)
	}

	if shouldInstall {
		if err := b.installRemoteBinary(ctx, sshTarget, profile, localBinary, remoteInstallPath); err != nil {
			return fmt.Errorf("install attn on %s: %w", sshTarget, err)
		}
		binariesUpdated = true
	}

	// The app runtime host ships beside the binary and is gated on its own
	// content, so it is checked on every sync rather than only when the binary
	// moved: the two are built from different trees and an apphost-only change
	// leaves the attn binary identical. A remote that cannot get one still runs
	// sessions — only apps park — so this reports and continues.
	runtimeUpdated, err := b.ensureRemoteAppRuntime(ctx, sshTarget, profile, platform, localVersion, remoteInstallPath)
	if err != nil {
		b.logf("%v", err)
	}
	if runtimeUpdated {
		// A replaced sidecar is a new file; the running one keeps the old inode
		// until something restarts it, and the daemon is what starts it.
		binariesUpdated = true
	}

	// Enroll before the daemon starts, so a remote coming up for the first time
	// already knows whose outpost it is. A remote enrolled to a different home
	// refuses, and that refusal stops the sync — re-homing is the operator's
	// decision to make, on that machine.
	if err := b.enrollRemote(ctx, sshTarget, profile, homeDaemonID); err != nil {
		return err
	}

	if err := b.ensureRemoteDaemonRunning(ctx, sshTarget, profile, binariesUpdated); err != nil {
		return fmt.Errorf("ensure remote daemon on %s: %w", sshTarget, err)
	}
	return nil
}

// enrollmentRefusedExitCode is what `attn enrollment enroll` exits with when the
// remote is already an outpost of a different home. Any other non-zero code is a
// remote that could not answer the question — an older binary without the
// command, a missing data dir — which is logged and does not block the sync,
// because enrollment is a record, not a precondition for sessions.
const enrollmentRefusedExitCode = 3

// withoutProfileBanner drops the `[attn profile=… socket=… port=…]` line every
// remote `attn` command prints on stderr. The refusal below is shown to a person
// whose endpoint just failed to sync; the remote's socket path is not part of
// the answer.
func withoutProfileBanner(message string) string {
	lines := strings.Split(message, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[attn profile=") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func remoteEnrollScript(profile, homeDaemonID string) string {
	return remoteAttnCommand(profile, "enrollment", "enroll", "--home", homeDaemonID, "--json")
}

func (b *Bootstrapper) enrollRemote(ctx context.Context, sshTarget, profile, homeDaemonID string) error {
	if strings.TrimSpace(homeDaemonID) == "" {
		b.logf("skipping enrollment of %s: this daemon is not a home daemon", sshTarget)
		return nil
	}
	stdout, stderr, code, err := runSSHExit(ctx, sshTarget, profile, remoteEnrollScript(profile, homeDaemonID))
	if err != nil {
		b.logf("enrollment check on %s could not run: %v", sshTarget, err)
		return nil
	}
	switch code {
	case 0:
		var result enrollment.Result
		if jsonErr := json.Unmarshal([]byte(stdout), &result); jsonErr != nil {
			b.logf("enrollment on %s returned unreadable output %q", sshTarget, stdout)
			return nil
		}
		if result.Changed() {
			b.logf("enrolled %s as an outpost of %s", sshTarget, homeDaemonID)
		}
		return nil
	case enrollmentRefusedExitCode:
		message := stderr
		if message == "" {
			message = stdout
		}
		return fmt.Errorf("%s is enrolled to another home: %s", sshTarget, withoutProfileBanner(message))
	default:
		detail := stderr
		if detail == "" {
			detail = stdout
		}
		b.logf("enrollment of %s skipped (exit %d): %s", sshTarget, code, detail)
		return nil
	}
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

func (b *Bootstrapper) detectRemotePlatform(ctx context.Context, sshTarget, profile string) (RemotePlatform, error) {
	out, err := runSSH(ctx, sshTarget, profile, "uname -sm")
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
	return remoteLinuxPlatform(fields[1])
}

// remoteLinuxPlatform maps a Linux `uname -m` onto the artifacts a remote needs.
// The Go and Bun names for the same machine disagree (amd64 vs x64), and each is
// the one its own toolchain accepts, so both are recorded here rather than
// derived at the call site.
func remoteLinuxPlatform(machine string) (RemotePlatform, error) {
	switch machine {
	case "x86_64", "amd64":
		return RemotePlatform{
			GOOS:                "linux",
			GOARCH:              "amd64",
			ArtifactName:        "attn-linux-amd64",
			RuntimeArtifactName: apps.RuntimeHostBinaryName + "-linux-amd64",
			BunTarget:           "bun-linux-x64",
		}, nil
	case "aarch64", "arm64":
		return RemotePlatform{
			GOOS:                "linux",
			GOARCH:              "arm64",
			ArtifactName:        "attn-linux-arm64",
			RuntimeArtifactName: apps.RuntimeHostBinaryName + "-linux-arm64",
			BunTarget:           "bun-linux-arm64",
		}, nil
	default:
		return RemotePlatform{}, fmt.Errorf("unsupported architecture %q", machine)
	}
}

func (b *Bootstrapper) remoteVersion(ctx context.Context, sshTarget, profile string) (string, error) {
	binName := remoteBinaryName(profile)
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
`, binName, binName)
	out, err := runSSH(ctx, sshTarget, profile, script)
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
		if err := b.downloadReleaseArtifact(ctx, version, platform.ArtifactName, filepath.Dir(cachePath)); err == nil {
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

func (b *Bootstrapper) downloadReleaseArtifact(ctx context.Context, version, artifact, destDir string) error {
	tag := version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	cmd := exec.CommandContext(ctx, "gh", "release", "download", tag, "--repo", githubRepo, "--pattern", artifact, "--dir", destDir, "--clobber")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh release download %s: %s", tag, strings.TrimSpace(string(out)))
	}
	return nil
}

// appRuntimeCacheDir is where prepared app runtime hosts live, keyed by what
// produced them.
//
// A source build overwrites one file per platform instead of accumulating a copy
// per attn build: the sidecar is built from apphost/, which no version or
// daemon-binary fingerprint covers, so any such key would serve a stale runtime
// after an apphost-only edit — and the artifact is ~90MB, so a key per build is
// also a disk leak. The compile is fast enough (~0.4s measured, bun embeds its
// runtime rather than compiling it) that rebuilding every sync costs nothing. A
// downloaded release artifact is immutable and does cache per version.
func appRuntimeCacheDir(home, key string) string {
	return filepath.Join(home, ".attn", "remotes", "app-runtime", key)
}

// ensureLocalAppRuntime produces the app runtime host for the remote's platform
// and returns its local path. Source build first, published artifact second —
// the same order, and for the same reason, as the attn binary beside it.
func (b *Bootstrapper) ensureLocalAppRuntime(ctx context.Context, platform RemotePlatform, version string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	var reasons []string
	if sourceCheckoutAvailable() {
		stageDir := appRuntimeCacheDir(home, platform.GOOS+"_"+platform.GOARCH)
		if err := b.buildAppRuntimeFromSource(ctx, platform, stageDir); err == nil {
			return filepath.Join(stageDir, apps.RuntimeHostBinaryName), nil
		} else {
			reasons = append(reasons, fmt.Sprintf("source build: %v", err))
		}
	}

	if version != "" && version != "dev" {
		cachePath := filepath.Join(appRuntimeCacheDir(home, version), platform.RuntimeArtifactName)
		if info, err := os.Stat(cachePath); err == nil && info.Mode().IsRegular() {
			return cachePath, nil
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return "", err
		}
		if err := b.downloadReleaseArtifact(ctx, version, platform.RuntimeArtifactName, filepath.Dir(cachePath)); err == nil {
			return cachePath, nil
		} else {
			reasons = append(reasons, fmt.Sprintf("release download: %v", err))
		}
	} else {
		reasons = append(reasons, "no published release to download it from (this hub reports version "+version+")")
	}

	return "", fmt.Errorf("no %s available (%s)", platform.RuntimeArtifactName, strings.Join(reasons, "; "))
}

func (b *Bootstrapper) buildAppRuntimeFromSource(ctx context.Context, platform RemotePlatform, stageDir string) error {
	root := sourceRoot()
	if root == "" {
		return fmt.Errorf("source checkout not available")
	}
	if platform.BunTarget == "" {
		return fmt.Errorf("no bun target for %s/%s", platform.GOOS, platform.GOARCH)
	}
	script := filepath.Join(root, "scripts", "build-app-runtime-host.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("%s is not in this checkout", script)
	}
	cmd := exec.CommandContext(ctx, "bash", script, stageDir, platform.BunTarget)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// remoteAppRuntimePath is where the sidecar lands on the remote: beside the attn
// binary that starts it, under the name that binary's profile resolves.
func remoteAppRuntimePath(remoteInstallPath, profile string) string {
	return filepath.Join(filepath.Dir(remoteInstallPath), apps.RuntimeHostBinaryNameForProfile(profile))
}

// ensureRemoteAppRuntime ships the app runtime host to the remote and reports
// whether it changed. The gate is content: the artifact is ~90MB, and a hub that
// syncs its endpoints on a timer would otherwise push it across the network on
// every pass.
//
// Its error is the missing-sidecar report, so it names the remote path, the
// platform, and what would make the upload possible — the daemon on the far end
// can only say the file is not there.
func (b *Bootstrapper) ensureRemoteAppRuntime(ctx context.Context, sshTarget, profile string, platform RemotePlatform, version, remoteInstallPath string) (bool, error) {
	remotePath := remoteAppRuntimePath(remoteInstallPath, profile)

	localPath, err := b.ensureLocalAppRuntime(ctx, platform, version)
	if err != nil {
		return false, fmt.Errorf(
			"the app runtime host is missing from %s at %s: %w. Apps on that daemon park until it is there; run the hub from a source checkout with bun installed, or copy %s there yourself",
			sshTarget, remotePath, err, platform.RuntimeArtifactName)
	}

	localHash, err := fileSHA256(localPath)
	if err != nil {
		return false, fmt.Errorf("hash the local app runtime host %s: %w", localPath, err)
	}
	remoteHash, err := b.remoteFileSHA256(ctx, sshTarget, profile, shellQuote(remotePath))
	if err != nil {
		return false, fmt.Errorf("hash the app runtime host on %s at %s: %w", sshTarget, remotePath, err)
	}
	if remoteHash == localHash {
		return false, nil
	}

	if err := b.uploadRemoteFile(ctx, sshTarget, profile, localPath, remotePath); err != nil {
		return false, fmt.Errorf(
			"the app runtime host could not be installed on %s at %s: %w. Apps on that daemon park until it is there",
			sshTarget, remotePath, err)
	}
	b.logf("installed the app runtime host on %s at %s (%s)", sshTarget, remotePath, localHash[:12])
	return true, nil
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

// remoteSHA256Script hashes whatever pathExpr expands to in the remote shell —
// a quoted literal, or a variable an earlier line resolved. A file that is not
// there answers NOT_FOUND rather than failing: "no copy yet" is the first sync,
// not an error.
func remoteSHA256Script(pathExpr string) string {
	return fmt.Sprintf(`
if [ ! -f %[1]s ]; then
  printf NOT_FOUND
  exit 0
fi
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum %[1]s | awk '{print $1}'
  exit 0
fi
if command -v shasum >/dev/null 2>&1; then
  shasum -a 256 %[1]s | awk '{print $1}'
  exit 0
fi
printf NO_HASH_TOOL
`, pathExpr)
}

func (b *Bootstrapper) runRemoteSHA256(ctx context.Context, sshTarget, profile, script string) (string, error) {
	out, err := runSSH(ctx, sshTarget, profile, script)
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

// remoteFileSHA256 returns the hash of a remote file, or "" when it is not
// there. pathExpr is a shell expression, so callers quote their own literals.
func (b *Bootstrapper) remoteFileSHA256(ctx context.Context, sshTarget, profile, pathExpr string) (string, error) {
	return b.runRemoteSHA256(ctx, sshTarget, profile, remoteSHA256Script(pathExpr))
}

func (b *Bootstrapper) remoteBinarySHA256(ctx context.Context, sshTarget, profile string) (string, error) {
	binName := remoteBinaryName(profile)
	resolve := fmt.Sprintf(`
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
`, binName, binName)
	return b.runRemoteSHA256(ctx, sshTarget, profile, resolve+remoteSHA256Script(`"$ATTN_BIN"`))
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
	// The worker's server-authoritative terminal links libghostty-vt via cgo on
	// Linux too (internal/ghosttyvt), so the cross-compile needs that target's
	// native archive present. It is download-first (no zig for the archive
	// itself), keyed by pin+patch, and installs under
	// third_party/ghostty-vt/<goos>_<goarch>/. Ensure it before the go build.
	if err := ensureNativeVTArchive(ctx, root, platform); err != nil {
		return err
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

// ensureNativeVTArchive fetches (or, on a locally-edited pin, source-builds) the
// libghostty-vt archive for the cross-compile target so its cgo link resolves.
// The download-first script is idempotent and a no-op once the archive is
// present for the current key. It is scoped to the build target via
// GHOSTTY_VT_GOOS/GOARCH so a Mac hub lays down the Linux archive, not its own.
func ensureNativeVTArchive(ctx context.Context, root string, platform RemotePlatform) error {
	script := filepath.Join(root, "scripts", "build-libghostty-vt.sh")
	if _, err := os.Stat(script); err != nil {
		// No script in this checkout (older source tree): nothing to ensure.
		return nil
	}
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GHOSTTY_VT_GOOS="+platform.GOOS,
		"GHOSTTY_VT_GOARCH="+platform.GOARCH,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ensure native libghostty-vt for %s: %s", platform.ArtifactName, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveRemoteInstallPath(remoteHome, override, profile string) string {
	path := strings.TrimSpace(override)
	if path == "" {
		return filepath.Join(remoteHome, ".local", "bin", remoteBinaryName(profile))
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(remoteHome, path[2:])
	}
	return path
}

// resolveRemoteInstall asks the remote where $HOME is and returns the path the
// attn binary installs to. Every file this bootstrap ships lands beside it.
func (b *Bootstrapper) resolveRemoteInstall(ctx context.Context, sshTarget, profile string) (string, error) {
	remoteHome, err := runSSH(ctx, sshTarget, profile, `printf '%s' "$HOME"`)
	if err != nil {
		return "", err
	}
	return resolveRemoteInstallPath(strings.TrimSpace(remoteHome), os.Getenv("ATTN_REMOTE_ATTN_BIN"), profile), nil
}

func (b *Bootstrapper) installRemoteBinary(ctx context.Context, sshTarget, profile, localBinary, remoteInstallPath string) error {
	attnDir := remoteAttnDirShell(profile)
	if _, err := runSSH(ctx, sshTarget, profile, fmt.Sprintf("mkdir -p %s", attnDir)); err != nil {
		return err
	}
	return b.uploadRemoteFile(ctx, sshTarget, profile, localBinary, remoteInstallPath)
}

// uploadRemoteFile streams a local executable to the remote and installs it in
// place. `install` unlinks the destination before writing it, so replacing a
// binary that is running right now hands the new file a new inode instead of
// failing with ETXTBSY — the running process keeps the old one until it exits.
func (b *Bootstrapper) uploadRemoteFile(ctx context.Context, sshTarget, profile, localPath, remotePath string) error {
	remoteDir := filepath.Dir(remotePath)
	remoteTmpPath := filepath.Join("/tmp", fmt.Sprintf("%s.%d.%d.tmp", filepath.Base(remotePath), os.Getpid(), time.Now().UnixNano()))
	if _, err := runSSH(ctx, sshTarget, profile, fmt.Sprintf("mkdir -p %s", shellQuote(remoteDir))); err != nil {
		return err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer file.Close()
	cmd := exec.CommandContext(
		ctx,
		"ssh",
		append(sshBaseArgs(sshTarget), remoteShellCommand(profile, fmt.Sprintf("cat > %s", shellQuote(remoteTmpPath))))...,
	)
	cmd.Stdin = file
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy %s over ssh: %s", filepath.Base(localPath), strings.TrimSpace(string(out)))
	}
	if probe, probeErr := runSSH(
		ctx,
		sshTarget,
		profile,
		fmt.Sprintf("if [ -f %s ]; then wc -c < %s; else printf MISSING; fi", shellQuote(remoteTmpPath), shellQuote(remoteTmpPath)),
	); probeErr == nil {
		b.logf("remote upload probe: target=%s tmp=%s result=%s", sshTarget, remoteTmpPath, strings.TrimSpace(probe))
	}
	_, err = runSSH(
		ctx,
		sshTarget,
		profile,
		fmt.Sprintf(
			"install -m 755 %s %s && rm -f %s",
			shellQuote(remoteTmpPath),
			shellQuote(remotePath),
			shellQuote(remoteTmpPath),
		),
	)
	return err
}

// remoteAttnDirShell returns a shell expression that resolves to the
// remote attn data dir for the given profile. The script picks the path up
// from $ATTN_PROFILE (which remoteShellEnvScript exports), so it stays
// self-contained when fed through `sh -lc`.
func remoteAttnDirShell(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return `"$HOME/.attn"`
	}
	// Even when we know the profile here, prefer reading $ATTN_PROFILE in the
	// remote shell so the script and the env script stay consistent.
	return `"$HOME/.attn-${ATTN_PROFILE}"`
}

func remoteSocketConfigScript() string {
	return `
attn_profile="${ATTN_PROFILE:-}"
if [ -n "$attn_profile" ]; then
  attn_dir="$HOME/.attn-$attn_profile"
else
  attn_dir="$HOME/.attn"
fi
config_path="${ATTN_CONFIG_PATH:-$attn_dir/config.json}"
socket_path="${ATTN_SOCKET_PATH:-}"
if [ -z "$socket_path" ] && [ -f "$config_path" ]; then
  socket_path="$(sed -n 's/.*"socket_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$config_path" | head -n 1)"
fi
if [ -z "$socket_path" ]; then
  socket_path="$attn_dir/attn.sock"
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

func (b *Bootstrapper) probeRemoteDaemon(ctx context.Context, sshTarget, profile string) (remoteDaemonState, error) {
	port := config.WSPortForProfile(profile)
	script := remoteSocketConfigScript() + fmt.Sprintf(`
listener_pid="$(ss -H -ltnp "( sport = :${ATTN_WS_PORT:-%s} )" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n 1)"
if [ -n "$listener_pid" ]; then
  printf 'running %%s\n' "$listener_pid"
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
`, port)
	out, err := runSSH(ctx, sshTarget, profile, script)
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

func (b *Bootstrapper) ensureRemoteDaemonRunning(ctx context.Context, sshTarget, profile string, binariesUpdated bool) error {
	state, err := b.probeRemoteDaemon(ctx, sshTarget, profile)
	if err != nil {
		return err
	}

	if state.Stale {
		if _, err := runSSH(ctx, sshTarget, profile, removeStaleRemoteSocketScript()); err != nil {
			return err
		}
		state = remoteDaemonState{}
	}

	if (state.Running || state.Starting) && binariesUpdated {
		if err := b.restartRemoteDaemon(ctx, sshTarget, profile, state.PID); err != nil {
			return err
		}
		state = remoteDaemonState{}
	}

	if !state.Running && !state.Starting {
		if err := b.startRemoteDaemon(ctx, sshTarget, profile); err != nil {
			return err
		}
	}

	deadline := time.Now().Add(remoteDaemonReadyTimeout)
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		current, err := b.probeRemoteDaemon(probeCtx, sshTarget, profile)
		cancel()
		if err == nil && current.Running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become ready")
}

// startRemoteDaemonScript returns the shell script that launches the remote
// daemon for a given profile. Pure string for testability.
func startRemoteDaemonScript(profile string) string {
	binName := remoteBinaryName(profile)
	attnDir := remoteAttnDirShell(profile)
	return fmt.Sprintf(`
mkdir -p %s
ATTN_BIN="${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/%s}"
if [ ! -x "$ATTN_BIN" ] && [ -z "${ATTN_REMOTE_ATTN_BIN:-}" ]; then
  ATTN_BIN="$(command -v %s 2>/dev/null || true)"
fi
if [ -z "$ATTN_BIN" ] || [ ! -x "$ATTN_BIN" ]; then
  printf 'missing attn binary\n' >&2
  exit 127
fi
nohup setsid "$ATTN_BIN" daemon </dev/null >>%s/daemon.log 2>&1 &
`, attnDir, binName, binName, attnDir)
}

func (b *Bootstrapper) startRemoteDaemon(ctx context.Context, sshTarget, profile string) error {
	_, err := runSSH(
		ctx,
		sshTarget,
		profile,
		startRemoteDaemonScript(profile),
	)
	return err
}

// stopRemoteDaemonScript returns the shell script that stops the remote daemon
// for a given profile. It deliberately leaves the PID file in place after
// stopping the daemon — see the comment on the stale-cleanup branch in
// ensureRemoteDaemonRunning for why unlinking it would be unsafe.
func stopRemoteDaemonScript(profile string) string {
	port := config.WSPortForProfile(profile)
	return remoteSocketConfigScript() + fmt.Sprintf(`
listener_pid="$(ss -H -ltnp "( sport = :${ATTN_WS_PORT:-%s} )" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n 1)"
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
rm -f "$socket_path"
`, port)
}

func (b *Bootstrapper) StopRemoteDaemon(ctx context.Context, sshTarget, profile string) error {
	if !remoteHarnessCleanupEnabled() {
		return nil
	}
	_, err := runSSH(ctx, sshTarget, profile, stopRemoteDaemonScript(profile))
	return err
}

func (b *Bootstrapper) restartRemoteDaemon(ctx context.Context, sshTarget, profile, pid string) error {
	if strings.TrimSpace(pid) != "" {
		_, _ = runSSH(ctx, sshTarget, profile, fmt.Sprintf("kill %s 2>/dev/null || true", shellQuote(pid)))
		time.Sleep(500 * time.Millisecond)
		_, _ = runSSH(ctx, sshTarget, profile, fmt.Sprintf("kill -9 %s 2>/dev/null || true", shellQuote(pid)))
	}
	if _, err := runSSH(ctx, sshTarget, profile, removeStaleRemoteSocketScript()); err != nil {
		return err
	}
	return b.startRemoteDaemon(ctx, sshTarget, profile)
}

// removeStaleRemoteSocketScript returns the shell script fragment used to
// clear the way for a fresh remote daemon start. It deliberately unlinks
// only the socket, never the PID path: the PID file's flock (not its
// presence on disk) is the sole mutual-exclusion mechanism a remote daemon
// and a concurrent `attn db restore` on that same host share. Unlinking the
// PID path here — right before a subsequent daemon start reopens the
// pathname with O_CREATE — would let a restore holding the old inode's
// flock go uncontended against a new daemon's freshly created inode at the
// same pathname. See internal/daemonctl/ensure.go's removeStaleSocketFiles
// for the identical local-daemon invariant.
func removeStaleRemoteSocketScript() string {
	return remoteSocketConfigScript() + `rm -f "$socket_path"`
}
