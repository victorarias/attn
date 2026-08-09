package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// The four commands that turn a directory into an installed app: `new`, `apply`,
// `rollback` and `dev`.
//
// Building runs here rather than in the daemon. The toolchain is the developer's
// — a version-managed bun is on their PATH and not on the PATH of a daemon the
// macOS app launched — and the output that matters most, a compiler's file and
// line, belongs in the terminal they are watching. The daemon owns the part that
// has to be atomic and observed: the version row, the pointer, the fact.

func runAppNew(args []string) {
	var dir, name, description string
	rest := args
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--name" && i+1 < len(rest):
			i++
			name = rest[i]
		case a == "--description" && i+1 < len(rest):
			i++
			description = rest[i]
		case a == "-h" || a == "--help":
			writeAppHelp(os.Stdout)
			return
		case strings.HasPrefix(a, "-"):
			appFail("new", fmt.Errorf("unknown flag %q", a))
		default:
			if dir != "" {
				appFail("new", fmt.Errorf("takes one directory; got %q and %q", dir, a))
			}
			dir = a
		}
	}
	if dir == "" {
		fmt.Fprintln(os.Stderr, "usage: attn app new <path> [--name <name>] [--description <text>]")
		os.Exit(2)
	}
	manifest, err := appbuild.Scaffold(appbuild.ScaffoldOptions{Dir: dir, Name: name, Description: description})
	if err != nil {
		appFail("new", err)
	}
	abs, _ := filepath.Abs(dir)
	fmt.Printf("created app %s in %s\n", manifest.Name, abs)
	fmt.Printf("  edit src/index.ts, then: attn app apply %s\n", dir)
	fmt.Printf("  AGENTS.md (and CLAUDE.md, a symlink to it) is the whole brief — an agent needs nothing else\n")
}

func runAppApply(args []string) {
	dir, asJSON := appPathArgs("apply", args)
	result, res, err := applyApp(dir, os.Stderr)
	if err != nil {
		appFail("apply", err)
	}
	if asJSON {
		writeJSON(result)
		return
	}
	printApplied(result, res)
}

// applyApp runs the pipeline and records the result. It is one function because
// the two halves are one act: if the daemon refuses what was just built, the
// artifact this run placed is removed again, so a failed apply leaves the store
// exactly as it found it.
func applyApp(dir string, progress *os.File) (*protocol.AppApplyResult, appbuild.Result, error) {
	res, err := appbuild.Build(context.Background(), appbuild.Options{
		Dir:      dir,
		StoreDir: config.AppsDir(),
		Log: func(line string) {
			if progress != nil {
				fmt.Fprintf(progress, "  %s\n", line)
			}
		},
	})
	if err != nil {
		return nil, appbuild.Result{}, err
	}
	abs, _ := filepath.Abs(dir)
	result, err := appClient().AppApply(res.Manifest.Name, res.ContentHash, res.Declaration, abs)
	if err != nil {
		if res.ArtifactWritten {
			_ = os.RemoveAll(filepath.Dir(res.ArtifactPath))
		}
		return nil, appbuild.Result{}, err
	}
	return result, res, nil
}

func printApplied(result *protocol.AppApplyResult, res appbuild.Result) {
	state := "unchanged — byte-identical to the version it already was"
	if result.VersionCreated {
		state = fmt.Sprintf("new, %d bytes", res.BundleBytes)
	}
	fmt.Printf("applied app %s: version %d (%s), %s\n",
		result.Name, result.VersionID, shortHash(result.ContentHash), state)
	if result.PreviousVersionID != nil && *result.PreviousVersionID != result.VersionID {
		fmt.Printf("  was on version %d\n", *result.PreviousVersionID)
	}
	fmt.Printf("  artifact %s\n", result.ArtifactPath)
}

func runAppRollback(args []string) {
	var name string
	versionID := 0
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help":
			writeAppHelp(os.Stdout)
			return
		case strings.HasPrefix(a, "-"):
			appFail("rollback", fmt.Errorf("unknown flag %q", a))
		case name == "":
			name = a
		case versionID == 0:
			n, err := strconv.Atoi(a)
			if err != nil || n <= 0 {
				appFail("rollback", fmt.Errorf("%q is not a version id; `attn app status %s` lists them", a, name))
			}
			versionID = n
		default:
			appFail("rollback", fmt.Errorf("takes an app name and at most one version id; got an extra %q", a))
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: attn app rollback <name> [version]")
		os.Exit(2)
	}
	result, err := appClient().AppRollback(name, versionID)
	if err != nil {
		appFail("rollback", err)
	}
	if asJSON {
		writeJSON(result)
		return
	}
	fmt.Printf("rolled app %s back to version %d (%s)\n", result.Name, result.VersionID, shortHash(result.ContentHash))
	if result.PreviousVersionID != nil {
		fmt.Printf("  was on version %d\n", *result.PreviousVersionID)
	}
	fmt.Printf("  artifact %s\n", result.ArtifactPath)
}

// devDebounce is how long `attn app dev` waits for edits to stop before
// rebuilding. An editor's save is several filesystem events — write, rename,
// attribute change — and a formatter on save adds more; 200ms is past the tail
// of that burst and under the point a person notices a delay.
const devDebounce = 200 * time.Millisecond

func runAppDev(args []string) {
	dir, _ := appPathArgs("dev", args)
	abs, err := filepath.Abs(dir)
	if err != nil {
		appFail("dev", err)
	}
	// Parse before watching so a directory that is not an app says so now rather
	// than after the first save.
	if _, err := appbuild.LoadManifest(abs); err != nil {
		appFail("dev", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		appFail("dev", fmt.Errorf("watching %s: %w", abs, err))
	}
	defer watcher.Close()
	if err := watchAppTree(watcher, abs); err != nil {
		appFail("dev", err)
	}

	fmt.Printf("watching %s — every change is parsed, typechecked, bundled and applied\n", abs)
	// Say what this does not show. Streaming what an app actually does needs the
	// runtime that dispatches to it, which is not in this attn; promising it here
	// would have the reader wait for output that cannot arrive.
	fmt.Printf("shows apply results and build errors. Handler invocations are not streamed: nothing dispatches to apps yet — `attn app status <name>` is where that will appear.\n")
	fmt.Printf("ctrl-c to stop\n\n")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	devApply(abs)

	var timer <-chan time.Time
	for {
		select {
		case <-stop:
			fmt.Println("\nstopped watching")
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !devRelevant(event.Name) {
				continue
			}
			// A new directory has to be watched too, or an app that grows a
			// subdirectory silently stops rebuilding on edits inside it.
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = watchAppTree(watcher, event.Name)
				}
			}
			timer = time.After(devDebounce)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
		case <-timer:
			timer = nil
			devApply(abs)
		}
	}
}

// devApply is one pass of the loop. It never exits the process: a build that
// fails is the normal state of a directory being edited, and a watcher that dies
// on the first type error is a watcher nobody can use.
func devApply(dir string) {
	started := time.Now()
	result, res, err := applyApp(dir, nil)
	stamp := time.Now().Format("15:04:05")
	if err != nil {
		fmt.Printf("%s  not applied:\n%v\n\n", stamp, err)
		return
	}
	fmt.Printf("%s  ", stamp)
	printApplied(result, res)
	fmt.Printf("  in %s\n\n", time.Since(started).Round(time.Millisecond))
}

// devRelevant filters the noise. node_modules and .git are large and never part
// of what is built from here; the generated files are rewritten by the build
// itself, and while WriteGenerated skips an unchanged write, a manifest edit does
// change them — without this the rebuild they trigger would trigger another.
func devRelevant(path string) bool {
	base := filepath.Base(path)
	switch base {
	case filepath.Base(appbuild.GeneratedFile), filepath.Base(appbuild.SDKFile):
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "node_modules" || part == ".git" {
			return false
		}
	}
	// Editors write through temporary files; reacting to each one rebuilds
	// against a half-written tree.
	return !strings.HasSuffix(base, "~") && !strings.HasPrefix(base, ".#")
}

func watchAppTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if name := entry.Name(); path != root && (name == "node_modules" || name == ".git") {
			return filepath.SkipDir
		}
		if err := watcher.Add(path); err != nil {
			return fmt.Errorf("watching %s: %w", path, err)
		}
		return nil
	})
}

// appPathArgs reads the `<path> [--json]` shape apply and dev share. The path
// defaults to the working directory, because the common case is running it from
// inside the app.
func appPathArgs(verb string, args []string) (string, bool) {
	dir := ""
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			if verb != "apply" {
				appFail(verb, errors.New("--json is only for apply"))
			}
			asJSON = true
		case a == "-h" || a == "--help":
			writeAppHelp(os.Stdout)
			os.Exit(0)
		case strings.HasPrefix(a, "-"):
			appFail(verb, fmt.Errorf("unknown flag %q", a))
		case dir != "":
			appFail(verb, fmt.Errorf("takes one directory; got %q and %q", dir, a))
		default:
			dir = a
		}
	}
	if dir == "" {
		dir = "."
	}
	return dir, asJSON
}

// shortHash matches the daemon's: full hashes belong in --json, not in a
// sentence.
func shortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}
