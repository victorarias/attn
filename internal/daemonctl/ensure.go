package daemonctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	defaultEnsureTimeout = 10 * time.Second
	stopTimeout          = 5 * time.Second
)

type EnsureResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type healthResponse struct {
	Status            string `json:"status"`
	Protocol          string `json:"protocol"`
	Version           string `json:"version"`
	SourceFingerprint string `json:"source_fingerprint"`
}

func Ensure(ctx context.Context, binaryPath string) (EnsureResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(binaryPath) == "" {
		return EnsureResult{}, fmt.Errorf("missing binary path")
	}

	if !isSocketLive(config.SocketPath()) {
		if err := removeStaleSocketFiles(); err != nil {
			return EnsureResult{}, err
		}
		if err := spawnDaemon(binaryPath); err != nil {
			return EnsureResult{}, err
		}
		if err := waitForReady(ctx); err != nil {
			return EnsureResult{}, err
		}
		return EnsureResult{Status: "started"}, nil
	}

	health, err := fetchHealth(ctx)
	if err == nil && daemonMatchesCurrentBinary(health) {
		return EnsureResult{Status: "already_running"}, nil
	}

	reason := mismatchReason(err, health)
	if err := stopRunningDaemon(ctx); err != nil {
		return EnsureResult{}, err
	}
	if err := removeStaleSocketFiles(); err != nil {
		return EnsureResult{}, err
	}
	if err := spawnDaemon(binaryPath); err != nil {
		return EnsureResult{}, err
	}
	if err := waitForReady(ctx); err != nil {
		return EnsureResult{}, err
	}
	return EnsureResult{Status: "restarted", Reason: reason}, nil
}

func daemonMatchesCurrentBinary(health healthResponse) bool {
	currentFingerprint := normalizedFingerprint(buildinfo.SourceFingerprint)
	if currentFingerprint != "" {
		return normalizedFingerprint(health.SourceFingerprint) == currentFingerprint
	}
	return strings.TrimSpace(health.Protocol) == protocol.ProtocolVersion
}

func mismatchReason(healthErr error, health healthResponse) string {
	if healthErr != nil {
		return "health_unavailable"
	}
	currentFingerprint := normalizedFingerprint(buildinfo.SourceFingerprint)
	runningFingerprint := normalizedFingerprint(health.SourceFingerprint)
	if currentFingerprint == "" {
		return "protocol_mismatch"
	}
	if runningFingerprint == "" {
		return "source_fingerprint_missing"
	}
	return "source_fingerprint_mismatch"
}

func normalizedFingerprint(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "unknown" {
		return ""
	}
	return trimmed
}

func spawnDaemon(binaryPath string) error {
	cmd := exec.Command(binaryPath, "daemon")
	cmd.Env = append(os.Environ(), "ATTN_WRAPPER_PATH="+binaryPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	return nil
}

func removeStaleSocketFiles() error {
	socketPath := config.SocketPath()
	pidPath := config.PIDPath()
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}
	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale pid %s: %w", pidPath, err)
	}
	return nil
}

func stopRunningDaemon(ctx context.Context) error {
	pidBytes, err := os.ReadFile(config.PIDPath())
	if err != nil {
		return fmt.Errorf("read daemon pid file: %w", err)
	}
	pidText := strings.TrimSpace(string(pidBytes))
	if pidText == "" {
		return fmt.Errorf("daemon pid file is empty")
	}
	pid, err := strconvAtoi(pidText)
	if err != nil || pid <= 0 {
		return fmt.Errorf("parse daemon pid %q: %w", pidText, err)
	}
	if pid == os.Getpid() || pid == os.Getppid() {
		return fmt.Errorf("refusing to stop daemon pid %d because it matches the current process tree", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("stop daemon pid %d: %w", pid, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !isSocketLive(config.SocketPath()) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for daemon to stop")
		case <-ticker.C:
		}
	}
}

func waitForReady(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, defaultEnsureTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if isSocketLive(config.SocketPath()) {
			health, err := fetchHealth(waitCtx)
			if err == nil && strings.TrimSpace(health.Status) == "ok" {
				return nil
			}
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("daemon did not become ready before timeout")
		case <-ticker.C:
		}
	}
}

func fetchHealth(ctx context.Context) (healthResponse, error) {
	host := strings.TrimSpace(config.WSBindAddress())
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 1 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, config.WSPort())+"/health", nil)
	if err != nil {
		return healthResponse{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return healthResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return healthResponse{}, fmt.Errorf("health status %d", resp.StatusCode)
	}
	var health healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return healthResponse{}, err
	}
	return health, nil
}

func isSocketLive(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func ResolveAppOwnedBinary() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ATTN_DAEMON_BINARY")); override != "" {
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "attn"), nil
}

func strconvAtoi(value string) (int, error) {
	sign := 1
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = strings.TrimPrefix(value, "-")
	}
	if value == "" {
		return 0, fmt.Errorf("empty integer")
	}
	total := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		total = total*10 + int(r-'0')
	}
	return sign * total, nil
}
