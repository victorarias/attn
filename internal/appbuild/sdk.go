package appbuild

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The app SDK, as a package on disk.
//
// The SDK is one TypeScript source, sdk/attn-app/, with two consumers: attn's
// frontend imports it through its own specifier (so a view's React is attn's
// React), and the binary carries its *declarations* and materializes a
// types-only package into the app being built. Nothing is published to npm and
// nothing is installed from it — the declarations ship inside the binary, so an
// author typechecks against exactly the SDK this attn speaks.
//
// `make generate-sdk` emits sdkdist from the TypeScript source and `make
// check-sdk` fails on a stale copy; the files are committed because //go:embed
// is what puts them in the binary.
//
// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md, "SDK packaging".

//go:embed sdkdist
var sdkDeclarations embed.FS

const (
	// sdkDistDir is where the emitted declarations live in this repo.
	sdkDistDir = "sdkdist"

	// sdkDirName is where materialized SDK versions live, under the apps store.
	sdkDirName = "sdk"

	// SDKLinkPath is where an app resolves the specifier from. It is a symlink
	// into the materialized package rather than a copy, so every app on a machine
	// shares one, and a stale one is repointed rather than merged.
	SDKLinkPath = "node_modules/" + SDKModule

	// LegacySDKFile is A4's ambient module declaration, written into every app
	// scaffolded or applied before the SDK became a package. Left beside a real
	// package it would give an app two conflicting declarations of one module, so
	// apply retires it — see retireLegacySDKFile.
	LegacySDKFile = "src/attn-app.d.ts"
)

// sdkPackageJSON is the materialized package's manifest. Types only: every
// specifier an app imports from the SDK is external at bundle time and supplied
// by the frontend at mount time, so shipping JavaScript here would only let a
// stored version freeze a copy of an SDK it is supposed to run against live.
const sdkPackageJSON = `{
  "name": "` + SDKModule + `",
  "version": "0.0.0",
  "private": true,
  "type": "module",
  "types": "./index.d.ts",
  "exports": {
    ".": { "types": "./index.d.ts" },
    "./jsx-runtime": { "types": "./jsx-runtime.d.ts" },
    "./jsx-dev-runtime": { "types": "./jsx-dev-runtime.d.ts" }
  }
}
`

// SDKFiles is what a materialized package holds, by relative name.
func SDKFiles() map[string]string {
	files := map[string]string{"package.json": sdkPackageJSON}
	entries, err := fs.ReadDir(sdkDeclarations, sdkDistDir)
	if err != nil {
		// The directory is embedded at compile time; a read failure is a broken
		// binary, not a runtime condition a caller can handle.
		panic(fmt.Sprintf("the embedded app SDK declarations are unreadable: %v", err))
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := sdkDeclarations.ReadFile(sdkDistDir + "/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("reading the embedded app SDK file %s: %v", entry.Name(), err))
		}
		files[entry.Name()] = string(data)
	}
	return files
}

// SDKHash identifies the materialized package: every file it holds, by name and
// content. It is the directory name, so an attn carrying different declarations
// materializes a different package rather than rewriting one an editor is
// reading.
func SDKHash() string {
	files := SDKFiles()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(files[name]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SDKDir is where one materialized SDK lives, derived rather than stored so
// every caller computes the same path.
func SDKDir(storeDir, hash string) string {
	return filepath.Join(storeDir, sdkDirName, hash)
}

// EnsureSDK materializes the SDK under storeDir and points appDir's node_modules
// at it, so `tsc` and an editor resolve the specifier with no network and no
// install. It returns the package directory.
//
// Both halves are idempotent and both are re-asserted every time: an app
// directory is the author's and may have been moved, cleaned, or copied from a
// machine whose data dir is somewhere else.
func EnsureSDK(storeDir, appDir string, log func(string)) (string, error) {
	pkg, err := materializeSDK(storeDir)
	if err != nil {
		return "", err
	}
	if err := linkSDK(appDir, pkg); err != nil {
		return "", err
	}
	retireLegacySDKFile(appDir, log)
	return pkg, nil
}

// materializeSDK writes the package under storeDir/sdk/<hash>, once per machine
// per attn build.
//
// It runs under the toolchain lock — the same one an install takes — because the
// package it writes points at the toolchain's node_modules for React's types,
// and two applies racing on a cold machine would otherwise stage against a
// half-installed toolchain.
func materializeSDK(storeDir string) (string, error) {
	root := filepath.Join(storeDir, sdkDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("creating the app SDK directory %s: %w", root, err)
	}
	toolchain := filepath.Join(storeDir, toolchainDirName)
	if err := os.MkdirAll(toolchain, 0o755); err != nil {
		return "", fmt.Errorf("creating the app toolchain directory %s: %w", toolchain, err)
	}
	unlock, err := lockDir(toolchain)
	if err != nil {
		return "", err
	}
	defer unlock()

	target := SDKDir(storeDir, SDKHash())
	if _, err := os.Stat(filepath.Join(target, "package.json")); err == nil {
		// The content is addressed by its hash, so what is there is what would be
		// written. Only the link out to React's types can have gone missing.
		return target, linkSDKTypes(target)
	}

	staging, err := os.MkdirTemp(root, ".staging-")
	if err != nil {
		return "", fmt.Errorf("creating a staging directory under %s: %w", root, err)
	}
	defer os.RemoveAll(staging)
	for name, content := range SDKFiles() {
		if err := os.WriteFile(filepath.Join(staging, name), []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("writing the app SDK file %s: %w", name, err)
		}
	}
	if err := linkSDKTypes(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		if _, statErr := os.Stat(filepath.Join(target, "package.json")); statErr == nil {
			return target, linkSDKTypes(target)
		}
		return "", fmt.Errorf("placing the app SDK at %s: %w", target, err)
	}
	return target, nil
}

// linkSDKTypes points a materialized package at the toolchain's node_modules.
//
// The SDK's declarations re-export React's — a view's props, its hooks and the
// JSX namespace are React's own types — so the package has to be able to resolve
// `react` from where it sits. The toolchain already installs those types beside
// the compiler, and one relative symlink is what lets every SDK version share
// that install and survive the data directory being moved.
func linkSDKTypes(pkgDir string) error {
	link := filepath.Join(pkgDir, "node_modules")
	const target = "../../toolchain/node_modules"
	if existing, err := os.Readlink(link); err == nil {
		if existing == target {
			return nil
		}
	} else if _, statErr := os.Lstat(link); statErr == nil {
		return fmt.Errorf("%s is not a symlink, and the app SDK owns it; remove it and re-apply", link)
	}
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("linking the app SDK at %s to the toolchain's types: %w", link, err)
	}
	return nil
}

// linkSDK points the app's node_modules at the materialized package.
//
// A real directory there is never removed. It would be the author's own install
// of a package under this name, and deleting it is not a decision apply gets to
// make silently.
func linkSDK(appDir, pkgDir string) error {
	link := filepath.Join(appDir, filepath.FromSlash(SDKLinkPath))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(link), err)
	}
	if existing, err := os.Readlink(link); err == nil {
		if existing == pkgDir {
			return nil
		}
	} else if info, statErr := os.Lstat(link); statErr == nil {
		return fmt.Errorf("%s already exists and is not attn's SDK link (%s); remove it and re-apply, or move the app out of a tree that installs %s",
			link, describeEntry(info), SDKModule)
	}
	_ = os.Remove(link)
	if err := os.Symlink(pkgDir, link); err != nil {
		return fmt.Errorf("linking %s into %s: %w", SDKModule, link, err)
	}
	return nil
}

func describeEntry(info os.FileInfo) string {
	if info.IsDir() {
		return "a directory"
	}
	return "a file"
}

// retireLegacySDKFile removes A4's ambient module declaration.
//
// It is attn's own generated file, carrying attn's own do-not-edit header, so
// deleting it retires a generated artifact rather than touching an author's
// work. That header is also the whole test: a file under this name that attn did
// not write is left alone and reported, because the alternative is apply
// deleting something a person put there.
func retireLegacySDKFile(appDir string, log func(string)) {
	path := filepath.Join(appDir, filepath.FromSlash(LegacySDKFile))
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if !strings.HasPrefix(string(data), generatedHeader) {
		logf(log, "%s is not attn's generated file, so it was kept — it now declares %s a second time, and the app will typecheck against whichever one wins; delete it",
			LegacySDKFile, SDKModule)
		return
	}
	if err := os.Remove(path); err != nil {
		logf(log, "could not remove %s (%v); it now declares %s a second time and should be deleted by hand", LegacySDKFile, err, SDKModule)
		return
	}
	logf(log, "removed %s — the SDK is a package now, linked at %s", LegacySDKFile, SDKLinkPath)
}
