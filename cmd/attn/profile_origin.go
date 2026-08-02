package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/ptyworker"
)

// originFileName lives inside the profile's data dir so `attn profile clean`
// removes it with everything else — a profile's provenance must not outlive the
// profile.
const originFileName = "origin.json"

// profileOrigin records where a profile was installed from. Nothing else in attn
// links a profile to the worktree that created it: the build fingerprint
// identifies source *content*, not the checkout, so a throwaway profile spun up
// for one branch is indistinguishable from a long-lived one once the agent that
// made it is gone. Recording it at install time is what lets tooling later ask
// "is this profile still mine?" and answer exactly instead of guessing.
type profileOrigin struct {
	Worktree   string `json:"worktree"`
	Branch     string `json:"branch,omitempty"`
	RecordedAt string `json:"recordedAt"`
}

func originPath(dataDir string) string { return filepath.Join(dataDir, originFileName) }

// writeProfileOrigin records provenance for a profile, creating the data dir if
// the daemon has not yet done so (install can run before first launch).
func writeProfileOrigin(dataDir string, origin profileOrigin) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	payload, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return err
	}
	path := originPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readProfileOrigin returns the recorded origin, or nil when the profile predates
// origin recording or was never installed from a worktree.
func readProfileOrigin(dataDir string) *profileOrigin {
	data, err := os.ReadFile(originPath(dataDir))
	if err != nil {
		return nil
	}
	var origin profileOrigin
	if err := json.Unmarshal(data, &origin); err != nil {
		return nil
	}
	if strings.TrimSpace(origin.Worktree) == "" {
		return nil
	}
	return &origin
}

// runProfileSetOrigin implements `attn profile set-origin <name> [--worktree dir]`.
// The Makefile calls it during `make install PROFILE=<name>`, which is the only
// moment the worktree that created a profile is known for certain.
func runProfileSetOrigin(args []string) {
	name := ""
	worktree := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--worktree":
			if i+1 >= len(args) {
				profileFatal("--worktree requires a directory")
			}
			i++
			worktree = args[i]
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				profileFatal(fmt.Sprintf("unknown flag %q", args[i]))
			}
			if name != "" {
				profileFatal(fmt.Sprintf("set-origin takes a single profile name, got %q and %q", name, args[i]))
			}
			name = args[i]
		}
	}
	if name == "" {
		profileFatal("set-origin requires a profile name (e.g. `attn profile set-origin agent7`)")
	}
	normalized, err := config.NormalizeProfileName(name)
	if err != nil {
		profileFatal(err.Error())
	}
	if normalized == "" {
		// The production profile is not a throwaway and must never be reported as
		// belonging to a worktree, or the cleanup nudge would target ~/.attn.
		profileFatal("refusing to record an origin for the default (production) profile")
	}
	if worktree == "" {
		worktree, err = os.Getwd()
		if err != nil {
			profileFatal(err.Error())
		}
	}
	abs, err := filepath.Abs(worktree)
	if err != nil {
		profileFatal(err.Error())
	}

	origin := profileOrigin{
		Worktree:   abs,
		Branch:     gitBranchAt(abs),
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	dataDir := config.DataDirForProfile(normalized)
	if err := writeProfileOrigin(dataDir, origin); err != nil {
		profileFatal(fmt.Sprintf("record origin: %v", err))
	}
	fmt.Printf("recorded origin for profile %s: %s", normalized, origin.Worktree)
	if origin.Branch != "" {
		fmt.Printf(" (%s)", origin.Branch)
	}
	fmt.Println()
}

func gitBranchAt(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return "" // detached; a bare SHA is not a useful label here
	}
	return branch
}

// profileListEntry is the machine-readable row behind `attn profile list --json`.
// It carries what a caller needs to decide whether a profile is worth cleaning:
// provenance, whether anything is installed, and what is currently running.
type profileListEntry struct {
	Profile       string         `json:"profile"`
	Label         string         `json:"label"`
	DataDir       string         `json:"dataDir"`
	AppPath       string         `json:"appPath"`
	WSPort        string         `json:"wsPort"`
	Active        bool           `json:"active"`
	HasData       bool           `json:"hasData"`
	HasApp        bool           `json:"hasApp"`
	DaemonRunning bool           `json:"daemonRunning"`
	LiveWorkers   int            `json:"liveWorkers"`
	Origin        *profileOrigin `json:"origin,omitempty"`
}

func newProfileListEntry(profile string, active string) profileListEntry {
	r := resolveProfile(profile)
	return profileListEntry{
		Profile:       profile,
		Label:         r.Label,
		DataDir:       r.DataDir,
		AppPath:       r.AppPath,
		WSPort:        r.WSPort,
		Active:        profile == active,
		HasData:       fileExists(r.DataDir),
		HasApp:        fileExists(r.AppPath),
		DaemonRunning: socketLive(r.Socket),
		LiveWorkers:   countLiveWorkers(r.DataDir),
		Origin:        readProfileOrigin(r.DataDir),
	}
}

// socketLive reports whether a daemon is actually accepting connections, rather
// than whether a socket file was left on disk by one that died.
func socketLive(path string) bool {
	if path == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// countLiveWorkers counts registered pty-workers whose process is still up.
// Registry entries outlive their processes, so the file count alone would
// overstate what a clean has left to do.
func countLiveWorkers(dataDir string) int {
	paths, err := filepath.Glob(filepath.Join(dataDir, "workers", "*", "registry", "*.json"))
	if err != nil {
		return 0
	}
	live := 0
	for _, path := range paths {
		entry, err := ptyworker.ReadRegistry(path)
		if err != nil {
			continue
		}
		if ptyworker.ProcessAlive(entry.WorkerPID) {
			live++
		}
	}
	return live
}
