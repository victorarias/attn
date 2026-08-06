package hostsession

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The host registry is the durable trace of every host process, one JSON file
// per session under <data-dir>/hosts/registry. The manager's in-memory map is
// authoritative while the daemon lives; the registry exists for when it does
// not. A daemon that dies without running its shutdown — SIGKILL, a crash, a
// power cut — leaves its hosts running, reparented to init, findable through
// nothing but this record. `attn profile clean` reaps from it exactly as it
// reaps pty-workers from theirs, because deleting the data dir destroys the
// only way those processes will ever be found.

// RegistryEntry records what the manager knew about a host at spawn: enough to
// find the process again and to prove it is still the same one before
// signalling it.
type RegistryEntry struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
	// PID doubles as the process group id — the host is spawned as its group's
	// leader.
	PID     int      `json:"pid"`
	Command []string `json:"command"`
	// ProcessStartTime is the platform's own identity stamp for the pid, read
	// back from the live process right after spawn (see processStartTime). A
	// pid alone can be recycled; a pid whose start time still matches is the
	// process we started.
	ProcessStartTime string `json:"process_start_time"`
	StartedAt        string `json:"started_at"`
}

const registryVersion = 1

// RegistryPath is where a session's host record lives under a data dir.
func RegistryPath(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "hosts", "registry", sessionID+".json")
}

func writeRegistry(path string, entry RegistryEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create host registry dir: %w", err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode host registry entry: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		return fmt.Errorf("write host registry entry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit host registry entry: %w", err)
	}
	return nil
}

// ReadRegistry loads one host record. Exported for the same reason the worker
// registry's reader is: the reaper and any inventory tooling read entries the
// manager wrote.
func ReadRegistry(path string) (RegistryEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return RegistryEntry{}, err
	}
	var entry RegistryEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return RegistryEntry{}, fmt.Errorf("decode host registry entry %s: %w", path, err)
	}
	if entry.Version != registryVersion {
		return RegistryEntry{}, fmt.Errorf("host registry entry %s has version %d, want %d", path, entry.Version, registryVersion)
	}
	return entry, nil
}

func newRegistryEntry(sessionID string, pid int, command []string) RegistryEntry {
	startTime, err := processStartTime(pid)
	if err != nil {
		// A host can exit faster than we can stamp it; the record still gets
		// written so the reaper sees the session existed. An empty stamp reads
		// as "cannot identify", never as "safe to signal".
		startTime = ""
	}
	return RegistryEntry{
		Version:          registryVersion,
		SessionID:        sessionID,
		PID:              pid,
		Command:          append([]string(nil), command...),
		ProcessStartTime: startTime,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

// processStartTime returns an opaque, platform-native stamp of when the pid's
// process started, stable across reads for the same process. Comparing the
// stamp recorded at spawn against a fresh read is the identity check that makes
// signalling a recorded pid safe: a recycled pid carries a different start
// time. On Linux it is /proc/<pid>/stat's starttime field (clock ticks since
// boot); elsewhere it is `ps -o lstart=`, whose second granularity is far finer
// than any realistic pid-reuse window.
func processStartTime(pid int) (string, error) {
	if raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err == nil {
		// comm (field 2) is an arbitrary string in parentheses; everything
		// after the closing paren is space-separated. starttime is field 22
		// overall, so the 20th after the state field that follows the paren.
		rest := string(raw)
		if i := strings.LastIndexByte(rest, ')'); i >= 0 {
			fields := strings.Fields(rest[i+1:])
			if len(fields) >= 20 {
				return fields[19], nil
			}
		}
		return "", fmt.Errorf("unparseable /proc/%d/stat", pid)
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	stamp := strings.TrimSpace(string(out))
	if stamp == "" {
		return "", fmt.Errorf("pid %d has no start time (gone?)", pid)
	}
	return stamp, nil
}
