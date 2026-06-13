package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/config"
)

// runProfile is the `attn profile <subcommand>` group: the human-facing surface
// over the single profile authority in internal/config. Bare `attn profile`
// prints status (the most useful default).
//
//	attn profile               → status of the active profile
//	attn profile status        → same
//	attn profile resolve [...]  → machine-readable resolution (JSON or --field)
//	attn profile list          → every profile with data and/or an installed app
//	attn profile env <name>     → alias of `attn profile-env`
func runProfile() {
	if len(os.Args) < 3 {
		runProfileStatus()
		return
	}
	switch os.Args[2] {
	case "status":
		runProfileStatus()
	case "resolve":
		runProfileResolve(os.Args[3:])
	case "tauri-config":
		runProfileTauriConfig(os.Args[3:])
	case "clean":
		runProfileClean(os.Args[3:])
	case "list":
		runProfileList()
	case "env":
		// `attn profile env …` mirrors the top-level `attn profile-env …`.
		runProfileEnvArgs(os.Args[3:])
	case "help", "-h", "--help":
		printProfileHelp(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown profile subcommand %q\n\n", os.Args[2])
		printProfileHelp(os.Stderr)
		os.Exit(1)
	}
}

// profileResolved is the single authority payload: every resource derived from
// one profile name. Emitted by `attn profile resolve --json` and consumed by
// the Makefile, the e2e harness, and the real-app harness so the derivation
// lives in exactly one place (internal/config) instead of being re-encoded.
type profileResolved struct {
	Profile        string `json:"profile"` // normalized ("" for default)
	Label          string `json:"label"`   // "default" | name
	DataDir        string `json:"dataDir"`
	Socket         string `json:"socket"`
	DBPath         string `json:"dbPath"`
	WSPort         string `json:"wsPort"`
	BundleID       string `json:"bundleId"`
	AppName        string `json:"appName"`
	AppPath        string `json:"appPath"`
	DeepLinkScheme string `json:"deepLinkScheme"`
	E2EDaemonPort  string `json:"e2eDaemonPort"`
	E2EVitePort    string `json:"e2eVitePort"`
}

func resolveProfile(profile string) profileResolved {
	label := profile
	if label == "" {
		label = "default"
	}
	return profileResolved{
		Profile:        profile,
		Label:          label,
		DataDir:        config.DataDirForProfile(profile),
		Socket:         config.SocketPathForProfile(profile),
		DBPath:         filepath.Join(config.DataDirForProfile(profile), "attn.db"),
		WSPort:         config.WSPortForProfile(profile),
		BundleID:       config.BundleIdentifierForProfile(profile),
		AppName:        config.AppNameForProfile(profile),
		AppPath:        config.AppPathForProfile(profile),
		DeepLinkScheme: config.DeepLinkSchemeForProfile(profile),
		E2EDaemonPort:  config.E2EDaemonPortForProfile(profile),
		E2EVitePort:    config.E2EVitePortForProfile(profile),
	}
}

func (r profileResolved) field(key string) (string, bool) {
	switch key {
	case "profile":
		return r.Profile, true
	case "label":
		return r.Label, true
	case "dataDir":
		return r.DataDir, true
	case "socket":
		return r.Socket, true
	case "dbPath":
		return r.DBPath, true
	case "wsPort":
		return r.WSPort, true
	case "bundleId":
		return r.BundleID, true
	case "appName":
		return r.AppName, true
	case "appPath":
		return r.AppPath, true
	case "deepLinkScheme":
		return r.DeepLinkScheme, true
	case "e2eDaemonPort":
		return r.E2EDaemonPort, true
	case "e2eVitePort":
		return r.E2EVitePort, true
	}
	return "", false
}

func runProfileStatus() {
	r := resolveProfile(config.Profile())
	socketUp := fileExists(r.Socket)
	appInstalled := fileExists(r.AppPath)

	fmt.Printf("attn profile: %s\n\n", r.Label)
	fmt.Printf("  data dir   %s\n", r.DataDir)
	fmt.Printf("  socket     %s  (%s)\n", r.Socket, ynLabel(socketUp, "daemon socket present", "no daemon socket"))
	fmt.Printf("  ws port    %s\n", r.WSPort)
	fmt.Printf("  bundle id  %s\n", r.BundleID)
	fmt.Printf("  app        %s  (%s)\n", r.AppPath, ynLabel(appInstalled, "installed", "not installed"))
	fmt.Printf("  scheme     %s\n", r.DeepLinkScheme)
	fmt.Printf("  e2e ports  daemon %s · vite %s\n\n", r.E2EDaemonPort, r.E2EVitePort)

	fmt.Println("Switch:   attn profile-env <name> | source   (fish: attn profile-env --fish <name> | source)")
	fmt.Println("Resolve:  attn profile resolve --json         (single value: --field wsPort)")
	fmt.Println("List:     attn profile list")
}

func runProfileResolve(args []string) {
	profile := config.Profile()
	field := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			// JSON is the default; accepted for explicitness.
		case "--profile":
			if i+1 >= len(args) {
				profileFatal("--profile requires a value")
			}
			i++
			p, err := config.NormalizeProfileName(args[i])
			if err != nil {
				profileFatal(err.Error())
			}
			profile = p
		case "--field":
			if i+1 >= len(args) {
				profileFatal("--field requires a key")
			}
			i++
			field = args[i]
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			profileFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	r := resolveProfile(profile)
	if field != "" {
		v, ok := r.field(field)
		if !ok {
			profileFatal(fmt.Sprintf("unknown field %q (valid: profile,label,dataDir,socket,dbPath,wsPort,bundleId,appName,appPath,deepLinkScheme,e2eDaemonPort,e2eVitePort)", field))
		}
		fmt.Println(v)
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		profileFatal(err.Error())
	}
	fmt.Println(string(b))
}

// runProfileTauriConfig emits a Tauri `--config` overlay for a profile's
// packaged build: productName, bundle identifier, deep-link scheme, and window
// title, all derived from the single authority. The Makefile writes this to a
// gitignored tauri.<name>.gen.conf.json and passes it to `tauri build --config`
// so a named profile's bundle metadata is never hand-maintained. Structurally
// identical to the (now removed) committed tauri.dev.conf.json, so the dev build
// is byte-for-byte equivalent after unification.
func runProfileTauriConfig(args []string) {
	profile := config.Profile()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--profile":
			if i+1 >= len(args) {
				profileFatal("--profile requires a value")
			}
			i++
			p, err := config.NormalizeProfileName(args[i])
			if err != nil {
				profileFatal(err.Error())
			}
			profile = p
		case "-h", "--help":
			printProfileHelp(os.Stdout)
			return
		default:
			profileFatal(fmt.Sprintf("unknown flag %q", args[i]))
		}
	}

	r := resolveProfile(profile)
	overlay := map[string]any{
		"$schema":     "https://schema.tauri.app/config/2",
		"productName": r.AppName,
		"identifier":  r.BundleID,
		"app": map[string]any{
			"windows": []any{
				map[string]any{
					"title":                r.AppName,
					"backgroundThrottling": "disabled",
				},
			},
		},
		"plugins": map[string]any{
			"deep-link": map[string]any{
				"desktop": map[string]any{
					"schemes": []string{r.DeepLinkScheme},
				},
			},
		},
	}
	b, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		profileFatal(err.Error())
	}
	fmt.Println(string(b))
}

// cleanPlan parses `attn profile clean` arguments and applies the safety guard.
// It is pure (no side effects, no os.Exit) so the rules that decide WHAT gets
// destroyed are unit-tested in isolation from the destruction itself.
//
// Returns the normalized profile name ("" for default/prod) and whether --force
// was given. Refuses the default/prod profile unless --force is explicit, since
// cleaning it would remove ~/.attn and ~/Applications/attn.app.
func cleanPlan(args []string) (normalized string, force bool, err error) {
	name := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force", "-f":
			force = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", false, fmt.Errorf("unknown flag %q", args[i])
			}
			if name != "" {
				return "", false, fmt.Errorf("clean takes a single profile name, got %q and %q", name, args[i])
			}
			name = args[i]
		}
	}
	if name == "" {
		return "", false, fmt.Errorf("clean requires a profile name (e.g. `attn profile clean agent7`)")
	}
	normalized, err = config.NormalizeProfileName(name)
	if err != nil {
		return "", false, err
	}
	if normalized == "" && !force {
		return "", false, fmt.Errorf("refusing to clean the default (production) profile without --force; this removes %s and %s",
			config.DataDirForProfile(""), config.AppPathForProfile(""))
	}
	return normalized, force, nil
}

// runProfileClean tears down a profile: stop its daemon, quit its app, forget
// the bundle in LaunchServices, and remove its app bundle and data dir. Every
// step is best-effort and reported, so cleaning a partially-installed profile is
// idempotent. The destructive removals (app, data) are the only hard failures.
func runProfileClean(args []string) {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			printProfileHelp(os.Stdout)
			return
		}
	}
	normalized, _, err := cleanPlan(args)
	if err != nil {
		profileFatal(err.Error())
	}
	r := resolveProfile(normalized)

	fmt.Printf(">>> Cleaning profile %s\n", r.Label)

	// Quit the app first so it stops talking to the daemon, then stop the
	// daemon itself (it outlives the app by design).
	quitProfileApp(r.BundleID)
	if msg := stopProfileDaemon(r); msg != "" {
		fmt.Printf("  daemon   %s\n", msg)
	} else {
		fmt.Printf("  daemon   stopped\n")
	}

	// App bundle: forget it in LaunchServices (so the deep-link scheme and
	// bundle id stop resolving to a path we're about to delete), then remove it.
	if fileExists(r.AppPath) {
		lsregisterForget(r.AppPath)
		if err := os.RemoveAll(r.AppPath); err != nil {
			profileFatal(fmt.Sprintf("remove app bundle %s: %v", r.AppPath, err))
		}
		fmt.Printf("  app      removed %s\n", r.AppPath)
	} else {
		fmt.Printf("  app      not installed (%s)\n", r.AppPath)
	}

	// Data dir: socket, pid file, db, tokens — everything for this profile.
	if fileExists(r.DataDir) {
		if err := os.RemoveAll(r.DataDir); err != nil {
			profileFatal(fmt.Sprintf("remove data dir %s: %v", r.DataDir, err))
		}
		fmt.Printf("  data     removed %s\n", r.DataDir)
	} else {
		fmt.Printf("  data     none (%s)\n", r.DataDir)
	}

	fmt.Printf("Cleaned profile %s.\n", r.Label)
}

// stopProfileDaemon stops a profile's daemon via its pid file (SIGTERM, then
// SIGKILL if it lingers), for an arbitrary profile resolved by path rather than
// the current process's config. Returns a human note ("" on a clean stop)
// instead of an error because a missing/dead daemon is an expected, non-fatal
// state during clean.
//
// Safety: the pid file is only removed on a *graceful* daemon shutdown
// (daemon.releasePIDLock); a crash/SIGKILL leaves it behind pointing at a dead
// pid that macOS may have recycled to an unrelated process. We therefore never
// trust the pid alone. The daemon holds an exclusive advisory lock on the pid
// file for its whole lifetime (daemon.acquirePIDLock), so we use that same lock
// as the liveness+ownership gate: if WE can take the lock, no live daemon owns
// the file and the pid is stale — we must not signal it.
func stopProfileDaemon(r profileResolved) string {
	pidPath := filepath.Join(r.DataDir, "attn.pid")
	lockFile, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return "not running (no pid file)"
	}
	if err != nil {
		return fmt.Sprintf("could not open pid file: %v", err)
	}
	if flockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr == nil {
		// Acquired the lock → no live daemon holds it. The pid on disk is stale;
		// signaling it could hit a recycled, unrelated process.
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return "not running (stale pid file)"
	}
	lockFile.Close()

	// The lock is held → a live daemon owns this file and wrote its own pid into
	// it under the lock, so the pid genuinely names that daemon: safe to signal.
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Sprintf("could not read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Sprintf("ignoring malformed pid file %q", strings.TrimSpace(string(data)))
	}
	// Never signal our own process tree (e.g. cleaning the profile we're
	// running under): killing it would take down this very command.
	if pid == os.Getpid() || pid == os.Getppid() {
		return fmt.Sprintf("skipped (pid %d is this command's own process tree)", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return "not running (stale pid file)"
		}
		return fmt.Sprintf("SIGTERM pid %d failed: %v", pid, err)
	}
	if processGoneWithin(pid, 5*time.Second) {
		return ""
	}
	// Escalate: a throwaway profile's daemon should always die on SIGTERM, but
	// don't leave a wedged process holding the data dir we're removing.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if processGoneWithin(pid, 2*time.Second) {
		return fmt.Sprintf("force-killed pid %d (did not exit on SIGTERM)", pid)
	}
	return fmt.Sprintf("warning: pid %d did not exit", pid)
}

// processGoneWithin polls `kill(pid, 0)` until the process is gone (ESRCH) or
// the deadline passes.
func processGoneWithin(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// quitProfileApp asks the app to quit by bundle id. Best-effort: a not-running
// app makes osascript fail harmlessly.
func quitProfileApp(bundleID string) {
	_ = exec.Command("osascript", "-e", fmt.Sprintf("tell application id %q to quit", bundleID)).Run()
}

// lsregisterPath is macOS's LaunchServices registration tool. Used to forget a
// bundle so its identifier and deep-link scheme stop resolving to a path we are
// about to delete.
const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

func lsregisterForget(appPath string) {
	if !fileExists(lsregisterPath) {
		return
	}
	_ = exec.Command(lsregisterPath, "-u", appPath).Run()
}

func runProfileList() {
	home, err := os.UserHomeDir()
	if err != nil {
		profileFatal("cannot resolve home directory: " + err.Error())
	}

	// The default profile always exists conceptually.
	known := map[string]bool{"": true}

	// Data dirs: ~/.attn (default) and ~/.attn-<profile>.
	if entries, err := os.ReadDir(home); err == nil {
		for _, e := range entries {
			name := e.Name()
			if name == ".attn" {
				known[""] = true
				continue
			}
			if p, ok := strings.CutPrefix(name, ".attn-"); ok && e.IsDir() {
				if config.ValidateProfileName(p) == nil {
					known[strings.ToLower(p)] = true
				}
			}
		}
	}

	// Installed apps: ~/Applications/attn.app and attn-<profile>.app.
	appsDir := filepath.Join(home, "Applications")
	if entries, err := os.ReadDir(appsDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if name == "attn.app" {
				known[""] = true
				continue
			}
			if base, ok := strings.CutSuffix(name, ".app"); ok {
				if p, ok := strings.CutPrefix(base, "attn-"); ok {
					if config.ValidateProfileName(p) == nil {
						known[strings.ToLower(p)] = true
					}
				}
			}
		}
	}

	names := make([]string, 0, len(known))
	for p := range known {
		names = append(names, p)
	}
	sort.Strings(names) // "" sorts first → default listed first

	active := config.Profile()
	fmt.Printf("%-3s %-16s %-7s %-9s %s\n", "", "PROFILE", "PORT", "DATA", "APP")
	for _, p := range names {
		r := resolveProfile(p)
		marker := "  "
		if p == active {
			marker = "* "
		}
		fmt.Printf("%-3s %-16s %-7s %-9s %s\n",
			marker,
			r.Label,
			r.WSPort,
			ynLabel(fileExists(r.DataDir), "yes", "—"),
			ynLabel(fileExists(r.AppPath), "installed", "—"),
		)
	}
	fmt.Println("\n* = active (ATTN_PROFILE)")
}

func printProfileHelp(w *os.File) {
	fmt.Fprintln(w, `attn profile — inspect and resolve attn profiles

A profile fully isolates attn's runtime: data dir, socket, websocket port,
macOS app bundle, and bundle identifier. ATTN_PROFILE selects it for every
entrypoint (CLI, daemon, e2e, real-app harness, build).

Usage:
  attn profile                 status of the active profile (ATTN_PROFILE)
  attn profile status          same
  attn profile resolve         resolved resources as JSON
  attn profile resolve --field wsPort      print one resolved value
  attn profile resolve --profile agent7    resolve a different profile
  attn profile tauri-config    Tauri --config overlay for the profile's build
  attn profile clean <name>    stop daemon, quit app, remove its app + data dir
  attn profile list            every profile with data and/or an installed app
  attn profile env <name>      alias of: attn profile-env <name>

Profile names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is the development
sibling (port 29849, ~/.attn-dev). `+"`clean`"+` refuses the default (production)
profile unless given --force.`)
}

// --- small local helpers (profile-prefixed to avoid package collisions) ---

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ynLabel(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

func profileFatal(msg string) {
	fmt.Fprintln(os.Stderr, "attn profile: "+msg)
	os.Exit(1)
}
