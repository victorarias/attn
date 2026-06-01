package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/config"
)

// warnIfDaemonVersionMismatch best-effort compares this CLI's build version
// against the running daemon's reported version and prints a one-line stderr
// warning when they differ.
//
// It exists to surface the failure mode where a stale or unrelated `attn`
// shadows the current one on PATH: the daemon is spawned from the installed
// app, so its version is authoritative for "what attn actually is" on this
// machine. A version skew almost always means the CLI on PATH is not the one
// the app installed.
//
// Always best-effort and non-fatal: a missing daemon, a non-comparable build
// (dev/unknown), a probe timeout, or any decode error simply skips the warning.
func warnIfDaemonVersionMismatch() {
	// Skip the network probe entirely for non-comparable builds.
	if !comparableBuildVersion(strings.TrimSpace(buildinfo.Version)) {
		return
	}
	if msg, ok := versionMismatchWarning(buildinfo.Version, fetchDaemonVersion()); ok {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// versionMismatchWarning is the pure decision behind warnIfDaemonVersionMismatch:
// it returns a warning message and true only when both versions are comparable
// release versions and they differ.
func versionMismatchWarning(cliVersion, daemonVersion string) (string, bool) {
	cliVersion = strings.TrimSpace(cliVersion)
	daemonVersion = strings.TrimSpace(daemonVersion)
	if !comparableBuildVersion(cliVersion) || !comparableBuildVersion(daemonVersion) {
		return "", false
	}
	if cliVersion == daemonVersion {
		return "", false
	}
	return fmt.Sprintf(
		"attn: warning: this CLI is %s but the running attn app is %s. "+
			"You may be running a stale attn — check `which -a attn`, then reinstall or use $ATTN_WRAPPER_PATH.",
		cliVersion, daemonVersion), true
}

// comparableBuildVersion reports whether a version string is a real release
// version we can meaningfully compare. Source/dev builds carry sentinel values
// that would produce noisy false positives.
func comparableBuildVersion(v string) bool {
	switch v {
	case "", "dev", "unknown":
		return false
	default:
		return true
	}
}

// fetchDaemonVersion reads the daemon's reported version from its /health
// endpoint. Returns "" on any failure. The probe targets 127.0.0.1 directly
// (the daemon always listens locally) so it works even when bound to 0.0.0.0.
func fetchDaemonVersion() string {
	httpClient := &http.Client{Timeout: 400 * time.Millisecond}
	url := "http://" + net.JoinHostPort("127.0.0.1", config.WSPort()) + "/health"
	resp, err := httpClient.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var health struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return ""
	}
	return strings.TrimSpace(health.Version)
}
