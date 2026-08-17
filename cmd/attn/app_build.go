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
	manifest, err := appbuild.Scaffold(appbuild.ScaffoldOptions{
		Dir:         dir,
		Name:        name,
		Description: description,
		StoreDir:    config.AppsDir(),
		Log:         func(line string) { fmt.Fprintf(os.Stderr, "  %s\n", line) },
	})
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

// applyApp runs the pipeline and records the result.
//
// A refused apply leaves its artifact behind on purpose. Only the daemon knows
// whether the version row was written, and the CLI cannot tell a refusal from a
// commit whose response was lost — so removing the artifact here would sometimes
// delete the bundle a live version points at, which is a broken app rather than
// wasted bytes. The leftover is inert: nothing lists it, the path is
// content-addressed so re-applying the same content lands on it and reuses it
// instead of accumulating, and the daemon re-hashes it before any row can name
// it.
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
		return nil, appbuild.Result{}, err
	}
	return result, res, nil
}

func printApplied(result *protocol.AppApplyResult, res appbuild.Result) {
	moved := result.PreviousVersionID != nil && *result.PreviousVersionID != result.VersionID
	// Three outcomes, and the reader has to be able to tell them apart: a new
	// version, an old version this content already had a row for (so the pointer
	// moved back onto it), and nothing at all happening.
	var state string
	switch {
	case result.VersionCreated:
		state = fmt.Sprintf("new, %d bytes", totalArtifactBytes(res))
	case moved:
		state = "this content already had a version; nothing new was recorded"
	default:
		state = "unchanged — byte-identical to the version it already was"
	}
	fmt.Printf("applied app %s: version %d (%s), %s\n",
		result.Name, result.VersionID, appbuild.ShortHash(result.ContentHash), state)
	if moved {
		fmt.Printf("  was on version %d\n", *result.PreviousVersionID)
	}
	fmt.Printf("  artifact %s\n", result.ArtifactPath)
	// A version made of several artifacts gets each one's size named. There is no
	// bundle size cap — nothing measured yet would justify a number — so making
	// the numbers visible is what apply owes an author instead.
	if len(res.ViewBytes) > 0 {
		fmt.Printf("    %s  %d bytes\n", appbuild.ArtifactName, res.BundleBytes)
		for _, v := range res.ViewBytes {
			fmt.Printf("    views/%s.js  %d bytes\n", v.Name, v.Bytes)
		}
	}
}

// totalArtifactBytes is everything a version holds: the handler bundle and one
// module per view.
func totalArtifactBytes(res appbuild.Result) int64 {
	total := res.BundleBytes
	for _, v := range res.ViewBytes {
		total += v.Bytes
	}
	return total
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
	target := fmt.Sprintf("version %d (%s)", result.VersionID, appbuild.ShortHash(result.ContentHash))
	switch {
	// Bare rollback walks recorded history rather than the version list, and the
	// id it lands on can be higher than the one it left — a fix applied on top of
	// an old version leaves exactly that behind it. Saying which version it was
	// serving before is what makes the choice checkable, and the walk is always
	// backwards in time, whatever the ids did.
	case versionID == 0 && result.PreviousVersionID != nil:
		fmt.Printf("rolled app %s back to %s, which was serving before version %d\n",
			result.Name, target, *result.PreviousVersionID)
	// A version named explicitly can move the pointer forward, and calling that
	// "rolled back" would be a lie about which direction the app just went.
	case result.PreviousVersionID != nil && *result.PreviousVersionID < result.VersionID:
		fmt.Printf("moved app %s forward to %s\n  was on version %d\n",
			result.Name, target, *result.PreviousVersionID)
	case result.PreviousVersionID != nil:
		fmt.Printf("rolled app %s back to %s\n  was on version %d\n",
			result.Name, target, *result.PreviousVersionID)
	default:
		fmt.Printf("rolled app %s back to %s\n", result.Name, target)
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
	manifest, err := appbuild.LoadManifest(abs)
	if err != nil {
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
	fmt.Printf("shows apply results, build errors, and every handler invocation as it runs\n")
	fmt.Printf("ctrl-c to stop\n\n")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	stopWatching := make(chan struct{})
	defer close(stopWatching)
	go devWatchInvocations(manifest.Name, stopWatching)

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

// devDialRetry is how long the invocation stream waits before dialling again
// after the daemon closed on it. A `dev` session outlives a daemon restart —
// installing a new build is a normal thing to do while writing an app — and a
// stream that quietly never came back would read as "my handler stopped
// running".
const devDialRetry = 2 * time.Second

// devWatchInvocations prints every invocation of the app being edited, beside
// the apply results, until dev stops.
//
// It says nothing about a daemon it cannot reach. `attn app dev` builds and
// typechecks perfectly well against a stopped daemon (the apply itself will say
// so, loudly, with the socket path), and repeating that here every two seconds
// would bury the build output this command exists to show.
func devWatchInvocations(app string, stop <-chan struct{}) {
	for {
		_ = appClient().AppWatch(app, stop, func(inv protocol.AppInvocationInfo) bool {
			fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), devInvocationLine(inv))
			return true
		})
		select {
		case <-stop:
			return
		case <-time.After(devDialRetry):
		}
	}
}

func devInvocationLine(inv protocol.AppInvocationInfo) string {
	line := fmt.Sprintf("%s  %s [%s]", inv.Status, inv.Handler, appInvocationWork(inv))
	if inv.DurationMs != nil {
		line += fmt.Sprintf(" in %dms", *inv.DurationMs)
	}
	if inv.Error != nil && *inv.Error != "" {
		line += "\n            " + *inv.Error
	}
	return line
}

// devRelevant filters the noise. node_modules and .git are large and never part
// of what is built from here; the generated file is rewritten by the build
// itself, and while WriteGenerated skips an unchanged write, a manifest edit does
// change it — without this the rebuild it triggers would trigger another.
func devRelevant(path string) bool {
	base := filepath.Base(path)
	switch base {
	case filepath.Base(appbuild.GeneratedFile):
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
