package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
  attn profile list            every profile with data and/or an installed app
  attn profile env <name>      alias of: attn profile-env <name>

Profile names must match [a-z0-9][a-z0-9-]{0,15}. "dev" is the development
sibling (port 29849, ~/.attn-dev).`)
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
