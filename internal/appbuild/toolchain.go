package appbuild

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The two external tools the pipeline shells out to, and how they are found.
//
// bun comes from PATH: it is the runtime apps are built for, and a machine that
// can run an app can run its bundler. TypeScript does not come from PATH, and
// does not come from the app either — attn keeps one copy under its data dir and
// every apply uses it.
//
// That choice has a receipt. `bun x tsc` needs no install but re-resolves the
// package on every run: measured at 2.1s against 0.77s for a direct exec of an
// installed compiler, and `attn app dev` pays that difference on every keystroke
// pause. Per-app installs pay it once each but cost ~30MB per app, in a platform
// whose premise is that apps are cheap and numerous. One shared, pinned copy
// costs one install per machine and nothing after.

const (
	// TypeScriptVersion is the compiler apply typechecks with. Pinned so an app
	// that builds today builds tomorrow, and matched to the version the attn
	// frontend uses so one repo has one TypeScript.
	TypeScriptVersion = "5.8.3"

	// ReactTypesVersion is React's own declarations, which the SDK re-exports:
	// a view's props, its hooks and the JSX namespace are React's types, so an
	// app cannot typecheck a .tsx without them. Pinned to the version the
	// frontend resolves, so the types an author checks against are the types the
	// running frontend provides — TestReactTypesPinMatchesTheFrontend fails when
	// the two drift.
	ReactTypesVersion = "19.2.7"

	// toolchainDirName is where the shared compiler lives, under the apps store.
	toolchainDirName = "toolchain"

	// toolchainStamp records what is installed. A pin bump in a new attn release
	// is what invalidates it; without the stamp an upgraded attn would keep using
	// whatever the last one installed.
	toolchainStamp = ".attn-typescript-version"
)

// toolchainPins is the stamp's content: every pinned package, so a bump to
// either one reinstalls.
func toolchainPins() string {
	return fmt.Sprintf("typescript@%s @types/react@%s", TypeScriptVersion, ReactTypesVersion)
}

// DefaultNPMRegistry is where attn fetches its own pinned packages from.
//
// Set explicitly rather than inherited: a work machine commonly exports
// NPM_CONFIG_REGISTRY for a corporate mirror, and attn's build then depends on
// that mirror's credentials, availability, and VPN. Measured here as a 401 on
// every install. ATTN_NPM_REGISTRY overrides it for anyone who does need one.
const DefaultNPMRegistry = "https://registry.npmjs.org/"

// npmRegistryEnv is the environment an attn-owned package install runs in: the
// caller's, with the registry decided here.
func npmRegistryEnv(environ []string) []string {
	registry := strings.TrimSpace(os.Getenv("ATTN_NPM_REGISTRY"))
	if registry == "" {
		registry = DefaultNPMRegistry
	}
	out := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if strings.HasPrefix(entry, "NPM_CONFIG_REGISTRY=") || strings.HasPrefix(entry, "npm_config_registry=") {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "NPM_CONFIG_REGISTRY="+registry)
}

// Toolchain is the resolved location of every external tool an apply runs.
type Toolchain struct {
	Bun string
	TSC string
}

// ResolveToolchain finds bun and makes sure the pinned TypeScript is installed
// under toolchainRoot, installing it if it is not there yet.
//
// The install is the one part of apply that can need the network, and it happens
// once per machine per pinned version. Its failure says so rather than reading as
// a compiler failure.
func ResolveToolchain(toolchainRoot string, log func(string)) (Toolchain, error) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		return Toolchain{}, fmt.Errorf("bun is not on PATH, and an app is built with bun (bundle) and TypeScript (typecheck); install it from https://bun.sh and try again")
	}
	tsc, err := ensureTypeScript(bun, filepath.Join(toolchainRoot, toolchainDirName), log)
	if err != nil {
		return Toolchain{}, err
	}
	return Toolchain{Bun: bun, TSC: tsc}, nil
}

// ensureTypeScript returns the path to the pinned compiler, installing it — and
// React's pinned declarations, which the SDK re-exports — when the stamp says the
// directory holds something else or nothing.
//
// The whole check-and-install runs under a lock on the toolchain directory: an
// `attn app dev` loop and a manual `attn app apply` racing on a cold machine
// would otherwise run two `bun install`s into the same node_modules.
func ensureTypeScript(bun, dir string, log func(string)) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating the app toolchain directory %s: %w", dir, err)
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return "", err
	}
	defer unlock()

	tsc := filepath.Join(dir, "node_modules", ".bin", "tsc")
	reactTypes := filepath.Join(dir, "node_modules", "@types", "react")
	if installedPins(dir) == toolchainPins() {
		_, tscErr := os.Stat(tsc)
		_, typesErr := os.Stat(reactTypes)
		if tscErr == nil && typesErr == nil {
			return tsc, nil
		}
	}

	if log != nil {
		log(fmt.Sprintf("installing TypeScript %s and React's types %s into %s (once per machine)", TypeScriptVersion, ReactTypesVersion, dir))
	}
	pkg := fmt.Sprintf("{\n  \"name\": \"attn-app-toolchain\",\n  \"private\": true,\n  \"dependencies\": { \"typescript\": \"%s\", \"@types/react\": \"%s\" }\n}\n",
		TypeScriptVersion, ReactTypesVersion)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		return "", fmt.Errorf("writing the app toolchain package.json: %w", err)
	}
	cmd := exec.Command(bun, "install", "--no-save")
	cmd.Dir = dir
	cmd.Env = npmRegistryEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("installing %s into %s failed (%v); apply needs a typechecker and React's declarations, and these are the ones it uses. Output:\n%s",
			toolchainPins(), dir, err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(tsc); err != nil {
		return "", fmt.Errorf("installing TypeScript %s into %s reported success but left no compiler at %s", TypeScriptVersion, dir, tsc)
	}
	if _, err := os.Stat(reactTypes); err != nil {
		return "", fmt.Errorf("installing @types/react %s into %s reported success but left no declarations at %s; the app SDK re-exports React's types and cannot resolve them without it",
			ReactTypesVersion, dir, reactTypes)
	}
	if err := os.WriteFile(filepath.Join(dir, toolchainStamp), []byte(toolchainPins()+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("recording the installed toolchain versions: %w", err)
	}
	return tsc, nil
}

func installedPins(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, toolchainStamp))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// lockDir takes an exclusive advisory lock on the toolchain directory, waiting
// for whoever holds it. The wait is bounded: a lock nobody releases would
// otherwise hang an apply with no explanation, and saying which file is held is
// what makes that recoverable by hand.
func lockDir(dir string) (func(), error) {
	path := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening the app toolchain lock %s: %w", path, err)
	}
	deadline := time.Now().Add(toolchainLockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another apply has held the app toolchain lock %s for over %s; if no apply is running, delete that file", path, toolchainLockWait)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// toolchainLockWait is a tripwire, not a budget: the work under the lock is one
// `bun install` of a single package, measured at under 10s cold on a warm cache
// and instant afterwards. Two minutes is far past any healthy case, so only a
// stuck holder reaches it.
const toolchainLockWait = 2 * time.Minute
