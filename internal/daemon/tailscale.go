package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/config"
)

const (
	tailscaleStatusDisabled    = "disabled"
	tailscaleStatusNeedsLogin  = "needs_login"
	tailscaleStatusRunning     = "running"
	tailscaleStatusConflict    = "conflict"
	tailscaleStatusUnavailable = "unavailable"
	tailscaleStatusError       = "error"

	tailscaleServeHTTPSPort = "443"
)

type tailscaleCommandRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type execTailscaleCommandRunner struct{}

func (execTailscaleCommandRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "tailscale", args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return output, nil
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return output, err
	}
	return output, fmt.Errorf("%w: %s", err, trimmed)
}

type tailscaleRuntime struct {
	mu       sync.RWMutex
	cli      tailscaleCommandRunner
	snapshot tailscaleStateSnapshot
}

type tailscaleStateSnapshot struct {
	status    string
	domain    string
	authURL   string
	lastError string
}

type tailscaleHostStatus struct {
	backendState string
	authURL      string
	domain       string
}

type tailscaleServeConfig struct {
	rootProxy string
}

type tailscaleStatusJSON struct {
	BackendState string `json:"BackendState"`
	AuthURL      string `json:"AuthURL"`
	Self         struct {
		DNSName string `json:"DNSName"`
	} `json:"Self"`
}

type tailscaleServeStatusJSON struct {
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

func newTailscaleRuntime() *tailscaleRuntime {
	return newTailscaleRuntimeWithCLI(execTailscaleCommandRunner{})
}

func newTailscaleRuntimeWithCLI(cli tailscaleCommandRunner) *tailscaleRuntime {
	if cli == nil {
		cli = execTailscaleCommandRunner{}
	}
	return &tailscaleRuntime{
		cli:      cli,
		snapshot: tailscaleStateSnapshot{status: tailscaleStatusDisabled},
	}
}

func (d *Daemon) tailscaleStateSnapshot() tailscaleStateSnapshot {
	if d.tailscale == nil {
		return tailscaleStateSnapshot{status: tailscaleStatusDisabled}
	}
	d.tailscale.mu.RLock()
	defer d.tailscale.mu.RUnlock()
	return d.tailscale.snapshot
}

func (d *Daemon) setTailscaleStateSnapshot(snapshot tailscaleStateSnapshot) {
	if d.tailscale == nil {
		d.tailscale = newTailscaleRuntime()
	}
	d.tailscale.mu.Lock()
	d.tailscale.snapshot = snapshot
	d.tailscale.mu.Unlock()
}

func (d *Daemon) ensureTailscaleServeFromSettings() tailscaleStateSnapshot {
	if d.tailscale == nil {
		d.tailscale = newTailscaleRuntime()
	}
	snapshot := d.reconcileTailscaleServe(parseBooleanSetting(d.store.GetSetting(SettingTailscaleEnabled)))
	d.setTailscaleStateSnapshot(snapshot)
	return snapshot
}

func (d *Daemon) refreshTailscaleServeState() {
	if d.tailscale == nil {
		d.tailscale = newTailscaleRuntime()
	}
	snapshot := d.inspectTailscaleServe(parseBooleanSetting(d.store.GetSetting(SettingTailscaleEnabled)))
	d.setTailscaleStateSnapshot(snapshot)
}

func (d *Daemon) ensureTailscaleServeFromSettingsAndBroadcast() {
	d.ensureTailscaleServeFromSettings()
	d.broadcastCurrentSettings("")
}

func (d *Daemon) reconcileTailscaleServe(enabled bool) tailscaleStateSnapshot {
	host, serve, err := d.queryTailscaleServeState()
	if err != nil {
		return tailscaleSnapshotFromError(err)
	}
	if !enabled {
		if host.backendState != "Running" {
			return tailscaleSnapshotFromHost(host, false, "")
		}
		if serve.rootProxy == tailscaleServeProxyTarget() {
			if _, err := d.runTailscaleCommand("serve", "--https="+tailscaleServeHTTPSPort, "--set-path=/", "off"); err != nil {
				return tailscaleSnapshotFromError(err)
			}
			host, serve, err = d.queryTailscaleServeState()
			if err != nil {
				return tailscaleSnapshotFromError(err)
			}
		}
		return tailscaleSnapshotFromHost(host, false, serve.rootProxy)
	}

	if host.backendState != "Running" {
		return tailscaleSnapshotFromHost(host, true, serve.rootProxy)
	}
	if serve.rootProxy != "" && serve.rootProxy != tailscaleServeProxyTarget() {
		return tailscaleSnapshotFromHost(host, true, serve.rootProxy)
	}

	if serve.rootProxy != tailscaleServeProxyTarget() {
		if _, err := d.runTailscaleCommand("serve", "--bg", "--https="+tailscaleServeHTTPSPort, "--set-path=/", "127.0.0.1:"+config.WSPort()); err != nil {
			return tailscaleSnapshotFromError(err)
		}
		host, serve, err = d.queryTailscaleServeState()
		if err != nil {
			return tailscaleSnapshotFromError(err)
		}
	}

	return tailscaleSnapshotFromHost(host, true, serve.rootProxy)
}

func (d *Daemon) inspectTailscaleServe(enabled bool) tailscaleStateSnapshot {
	host, serve, err := d.queryTailscaleServeState()
	if err != nil {
		return tailscaleSnapshotFromError(err)
	}
	return tailscaleSnapshotFromHost(host, enabled, serve.rootProxy)
}

func (d *Daemon) queryTailscaleServeState() (tailscaleHostStatus, tailscaleServeConfig, error) {
	host, err := d.readTailscaleHostStatus()
	if err != nil {
		return tailscaleHostStatus{}, tailscaleServeConfig{}, err
	}
	serve, err := d.readTailscaleServeConfig(host.domain)
	if err != nil {
		return host, tailscaleServeConfig{}, err
	}
	return host, serve, nil
}

func (d *Daemon) readTailscaleHostStatus() (tailscaleHostStatus, error) {
	output, err := d.runTailscaleCommand("status", "--json")
	if err != nil {
		return tailscaleHostStatus{}, err
	}

	var parsed tailscaleStatusJSON
	if err := json.Unmarshal(output, &parsed); err != nil {
		return tailscaleHostStatus{}, fmt.Errorf("parse tailscale status: %w", err)
	}

	return tailscaleHostStatus{
		backendState: strings.TrimSpace(parsed.BackendState),
		authURL:      strings.TrimSpace(parsed.AuthURL),
		domain:       strings.TrimSuffix(strings.TrimSpace(parsed.Self.DNSName), "."),
	}, nil
}

func (d *Daemon) readTailscaleServeConfig(domain string) (tailscaleServeConfig, error) {
	output, err := d.runTailscaleCommand("serve", "status", "--json")
	if err != nil {
		return tailscaleServeConfig{}, err
	}

	var parsed tailscaleServeStatusJSON
	if err := json.Unmarshal(output, &parsed); err != nil {
		return tailscaleServeConfig{}, fmt.Errorf("parse tailscale serve status: %w", err)
	}

	serviceKey := domain + ":" + tailscaleServeHTTPSPort
	if service, ok := parsed.Web[serviceKey]; ok {
		if handler, ok := service.Handlers["/"]; ok {
			return tailscaleServeConfig{rootProxy: strings.TrimSpace(handler.Proxy)}, nil
		}
	}
	return tailscaleServeConfig{}, nil
}

func (d *Daemon) runTailscaleCommand(args ...string) ([]byte, error) {
	if d.tailscale == nil {
		d.tailscale = newTailscaleRuntime()
	}
	if d.tailscale.cli == nil {
		d.tailscale.cli = execTailscaleCommandRunner{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return d.tailscale.cli.Run(ctx, args...)
}

func tailscaleSnapshotFromError(err error) tailscaleStateSnapshot {
	if err == nil {
		return tailscaleStateSnapshot{status: tailscaleStatusDisabled}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return tailscaleStateSnapshot{
			status:    tailscaleStatusUnavailable,
			lastError: "tailscale CLI not found; install Tailscale and sign this machine into your tailnet first",
		}
	}
	return tailscaleStateSnapshot{
		status:    tailscaleStatusError,
		lastError: err.Error(),
	}
}

func tailscaleSnapshotFromHost(host tailscaleHostStatus, enabled bool, rootProxy string) tailscaleStateSnapshot {
	snapshot := tailscaleStateSnapshot{
		status:  tailscaleStatusDisabled,
		domain:  host.domain,
		authURL: host.authURL,
	}
	switch host.backendState {
	case "", "NoState":
		snapshot.status = tailscaleStatusUnavailable
		if snapshot.lastError == "" {
			snapshot.lastError = "tailscale is not running on this machine"
		}
		return snapshot
	case "Running":
	default:
		snapshot.status = tailscaleStatusNeedsLogin
		if host.authURL == "" {
			snapshot.lastError = "tailscale is not authenticated on this machine"
		}
		return snapshot
	}

	if rootProxy == tailscaleServeProxyTarget() {
		snapshot.status = tailscaleStatusRunning
		return snapshot
	}
	if enabled && rootProxy != "" {
		snapshot.status = tailscaleStatusConflict
		snapshot.lastError = fmt.Sprintf("tailscale serve already uses https://%s/ for %s", host.domain, rootProxy)
		return snapshot
	}
	snapshot.status = tailscaleStatusDisabled
	return snapshot
}

func tailscaleServeProxyTarget() string {
	return "http://127.0.0.1:" + config.WSPort()
}

func (d *Daemon) removeLegacyEmbeddedTailscaleState() {
	if strings.TrimSpace(d.dataRoot) == "" {
		return
	}
	legacyDir := filepath.Join(d.dataRoot, "tsnet")
	info, err := os.Stat(legacyDir)
	if err != nil {
		return
	}
	if !info.IsDir() {
		return
	}
	if err := os.RemoveAll(legacyDir); err != nil {
		d.logf("failed to remove legacy embedded Tailscale state at %s: %v", legacyDir, err)
		return
	}
	d.logf("removed legacy embedded Tailscale state at %s", legacyDir)
}
