package appbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Build is the apply pipeline up to (but not including) the database: parse,
// codegen, typecheck, bundle, hash, place the artifact.
//
// The ordering is the guarantee. Everything that can be refused is refused
// before anything leaves a temporary directory, and the only write outside the
// app's own source tree is the final rename of a fully-built artifact into the
// content-addressed store. A tsc or bundle failure therefore leaves no version
// row, no pointer move, and no artifact directory — not because a cleanup path
// runs, but because none of it had happened yet.
//
// Nothing here evaluates app code. `tsc --noEmit` reads source and bun's bundler
// resolves and concatenates modules; neither runs a module's top level. That is
// the rule the whole stage is built to keep: an app whose entrypoint throws on
// import still applies, and finds out at dispatch time.

// Options is one build.
type Options struct {
	// Dir is the app's source directory — the one holding attn-app.toml.
	Dir string
	// StoreDir is the artifact root, `<data-dir>/apps`. Built artifacts land in
	// StoreDir/<name>/<hash>/ and the shared TypeScript lives beside them.
	StoreDir string
	// Log receives progress lines. Optional.
	Log func(string)
}

// Result is what a successful build produced. It is everything the commit needs
// and nothing it has to recompute.
type Result struct {
	Manifest    Manifest
	Declaration string
	// ContentHash identifies the version: the declaration and every artifact
	// together. See versionHash.
	ContentHash  string
	ArtifactPath string
	// ArtifactWritten is false when the store already held this exact content —
	// a byte-identical re-apply, which reuses both the artifact and, at commit,
	// the version row.
	ArtifactWritten bool
	BundleBytes     int64
	// ViewBytes is the size of each built view's artifact, in declaration order.
	// A5 sets no bundle size cap — nothing measured yet would justify a number —
	// so what apply owes the author instead is the number itself, per artifact.
	ViewBytes []ViewSize
}

// ViewSize is one built view's artifact and how big it came out.
type ViewSize struct {
	Name  string
	Path  string
	Bytes int64
}

// ArtifactName is the file every built app's handlers are bundled into.
const ArtifactName = "bundle.js"

// viewsDirName is where a version's view bundles live, beside its handler
// bundle: one file per declared view, named by the view.
const viewsDirName = "views"

// ShortHash renders a version hash for a human-facing line. The full hash is
// always available in a command's --json output; a sentence carrying 64
// characters is a sentence nobody reads.
func ShortHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

// ArtifactDir is where a version's artifact lives, derived from the app name and
// the version hash rather than stored, so the CLI that builds it and the daemon
// that commits it cannot disagree about the location.
func ArtifactDir(storeDir, name, hash string) string {
	return filepath.Join(storeDir, name, hash)
}

// ArtifactPath is the built handler bundle for one version.
func ArtifactPath(storeDir, name, hash string) string {
	return filepath.Join(ArtifactDir(storeDir, name, hash), ArtifactName)
}

// ViewArtifactPath is the built module for one of a version's views. Derived
// from the app, the version and the view name for the same reason ArtifactPath
// is: the builder, the daemon and whatever serves it later cannot disagree about
// where a view's bytes are.
func ViewArtifactPath(storeDir, name, hash, view string) string {
	return filepath.Join(ArtifactDir(storeDir, name, hash), viewsDirName, view+".js")
}

// Build runs the pipeline.
func Build(ctx context.Context, opts Options) (Result, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("resolving app directory %s: %w", opts.Dir, err)
	}
	manifest, err := LoadManifest(dir)
	if err != nil {
		return Result{}, err
	}
	declaration, err := manifest.Declaration()
	if err != nil {
		return Result{}, err
	}
	if err := WriteGenerated(dir, manifest); err != nil {
		return Result{}, err
	}
	tools, err := ResolveToolchain(opts.StoreDir, opts.Log)
	if err != nil {
		return Result{}, err
	}
	// The SDK is materialized before the typecheck because it is what the
	// typecheck resolves the specifier to.
	if _, err := EnsureSDK(opts.StoreDir, dir, opts.Log); err != nil {
		return Result{}, err
	}

	logf(opts.Log, "typechecking %s", manifest.Entrypoint)
	if err := typecheck(ctx, tools, dir, manifest); err != nil {
		return Result{}, err
	}

	// Staging lives inside the store, not in the system temp directory: the last
	// step is a rename into the store and a rename across filesystems fails —
	// /tmp is a different volume often enough on Linux (tmpfs) that this would be
	// a defect nobody sees on a Mac.
	stagingRoot := filepath.Join(opts.StoreDir, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("creating the app build staging directory %s: %w", stagingRoot, err)
	}
	staging, err := os.MkdirTemp(stagingRoot, manifest.Name+"-")
	if err != nil {
		return Result{}, fmt.Errorf("creating a build directory under %s: %w", stagingRoot, err)
	}
	defer os.RemoveAll(staging)

	logf(opts.Log, "bundling %s", manifest.Entrypoint)
	bundle := filepath.Join(staging, ArtifactName)
	if err := bundleApp(ctx, tools, dir, manifest, bundle); err != nil {
		return Result{}, err
	}
	built, err := os.ReadFile(bundle)
	if err != nil {
		return Result{}, fmt.Errorf("reading the built bundle: %w", err)
	}

	views := make([]ViewArtifact, 0, len(manifest.Views))
	for _, v := range manifest.Views {
		logf(opts.Log, "bundling view %s from %s", v.Name, v.Entrypoint)
		out := filepath.Join(staging, viewsDirName, v.Name+".js")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return Result{}, fmt.Errorf("creating the view build directory %s: %w", filepath.Dir(out), err)
		}
		if err := bundleView(ctx, tools, dir, manifest, v, out); err != nil {
			return Result{}, err
		}
		content, err := os.ReadFile(out)
		if err != nil {
			return Result{}, fmt.Errorf("reading the built view %q: %w", v.Name, err)
		}
		views = append(views, ViewArtifact{Name: v.Name, Content: content})
	}

	hash := versionHash(declaration, built, views)
	path, written, err := placeArtifact(opts.StoreDir, manifest.Name, hash, staging)
	if err != nil {
		return Result{}, err
	}
	sizes := make([]ViewSize, 0, len(views))
	for _, v := range views {
		sizes = append(sizes, ViewSize{
			Name:  v.Name,
			Path:  ViewArtifactPath(opts.StoreDir, manifest.Name, hash, v.Name),
			Bytes: int64(len(v.Content)),
		})
	}
	return Result{
		Manifest:        manifest,
		Declaration:     declaration,
		ContentHash:     hash,
		ArtifactPath:    path,
		ArtifactWritten: written,
		BundleBytes:     int64(len(built)),
		ViewBytes:       sizes,
	}, nil
}

// WriteGenerated rewrites the file codegen owns. It runs before the typecheck
// because it is what the typecheck checks against, and it writes into the app's
// own tree because the author's editor has to see the same errors apply does.
func WriteGenerated(dir string, m Manifest) error {
	files := map[string]string{
		GeneratedFile: GenerateTypes(m),
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}
		// Rewriting an unchanged file would touch its mtime, and `attn app dev`
		// watches this directory: codegen would wake the watcher that triggered
		// it, forever.
		if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// typecheck is what enforces manifest↔code sync, and it runs on attn's terms
// rather than the app's: the compiler, the flags and the file list all come from
// here.
//
// An app-provided tsconfig.json is deliberately not used. It is the author's
// editor configuration, and a check that honored it could be switched off by the
// thing being checked — an `include` that misses src/generated.ts would leave
// apply reporting a clean typecheck of an app whose handlers do not match its
// manifest. The app's own tsconfig still exists in the scaffold, carrying these
// same flags, so the editor and apply agree.
func typecheck(ctx context.Context, tools Toolchain, dir string, m Manifest) error {
	args := []string{
		"--noEmit",
		"--strict",
		"--target", "es2022",
		"--module", "esnext",
		"--moduleResolution", "bundler",
		"--skipLibCheck",
		"--pretty", "false",
		// TSX compiles to an import of the SDK's jsx-runtime rather than React's,
		// which is what makes React a specifier an app cannot write: there is no
		// `react` in an app's node_modules and nothing declares it.
		"--jsx", "react-jsx",
		"--jsxImportSource", SDKModule,
		filepath.FromSlash(m.Entrypoint),
	}
	cmd := exec.CommandContext(ctx, tools.TSC, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return fmt.Errorf("typechecking app %q failed with no output (%v)", m.Name, err)
	}
	// The compiler's own text, verbatim: it already carries file, line and column,
	// and rewording it would cost the reader the one thing they need.
	return fmt.Errorf("app %q does not typecheck against its manifest:\n%s", m.Name, text)
}

// bundleApp runs bun's bundler. `--target bun` is the runtime apps execute in
// (slice 5's sidecar); external is empty because an app's dependencies belong in
// its artifact — a version has to be the whole of what runs, or a rollback is
// not a rollback.
func bundleApp(ctx context.Context, tools Toolchain, dir string, m Manifest, outfile string) error {
	cmd := exec.CommandContext(ctx, tools.Bun, "build",
		filepath.FromSlash(m.Entrypoint),
		"--target", "bun",
		"--format", "esm",
		"--outfile", outfile,
	)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bundling app %q failed:\n%s", m.Name, strings.TrimSpace(string(out)))
	}
	return nil
}

// ViewArtifact is one built view: the name it was declared under and the module
// bytes that name resolves to.
type ViewArtifact struct {
	Name    string
	Content []byte
}

// bundleView builds one view for the browser.
//
// Two differences from the handler bundle, and nothing else. The target is the
// browser, because this artifact is imported by attn's frontend rather than run
// in the sidecar. And the SDK specifiers are external: they are supplied by the
// host at mount time from the frontend's own modules, which is what makes an
// app's component and attn's UI share one React — bundling a copy in here would
// give the view a second React and a hook dispatcher that is not the one
// rendering it. Everything else an app imports is bundled in, unchanged from the
// handler build: a version has to be the whole of what runs.
func bundleView(ctx context.Context, tools Toolchain, dir string, m Manifest, v View, outfile string) error {
	cmd := exec.CommandContext(ctx, tools.Bun, "build",
		filepath.FromSlash(v.Entrypoint),
		"--target", "browser",
		"--format", "esm",
		"--external", SDKModule,
		"--external", SDKModule+"/jsx-runtime",
		"--outfile", outfile,
	)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bundling view %q of app %q failed:\n%s", v.Name, m.Name, strings.TrimSpace(string(out)))
	}
	return nil
}

// versionHash is the version's identity: the frozen declaration and every
// artifact the version holds, hashed together.
//
// Hashing the handler bundle alone would be wrong in two ways that are easy to
// miss. The generated types are erased at build time, so a manifest edit that
// changes what the app declares — a new collection, a changed description — can
// leave that bundle byte-identical; the version row would then be reused and its
// frozen declaration would describe the *previous* manifest. And a view is built
// from its own entrypoint, so editing only a view leaves both the declaration
// and the handler bundle untouched: without the views in here, saving a
// component would reuse a version row whose artifacts had moved under it.
//
// Views are hashed by name as well as content, in name order, so the digest does
// not depend on declaration order and two views cannot swap names unnoticed.
func versionHash(declaration string, bundle []byte, views []ViewArtifact) string {
	h := sha256.New()
	h.Write([]byte(declaration))
	h.Write([]byte{0})
	h.Write(bundle)
	ordered := append([]ViewArtifact(nil), views...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, v := range ordered {
		h.Write([]byte{0})
		h.Write([]byte(v.Name))
		h.Write([]byte{0})
		h.Write(v.Content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VersionHash recomputes a built version's identity from its inputs. The daemon
// uses it to check that the artifacts it is about to record are the ones the
// hash claims, so a commit never points a version row at content that does not
// match its own name.
//
// An app with no views hashes exactly as it did before views existed: the loop
// writes nothing, so re-applying an app built by an older attn lands on the row
// it already had.
func VersionHash(declaration string, bundle []byte, views []ViewArtifact) string {
	return versionHash(declaration, bundle, views)
}

// ReadViewArtifacts reads the built modules of the named views out of a
// version's directory in the store.
//
// It is how the daemon assembles the inputs to VersionHash: the declaration
// names the views, and a version whose directory is missing one of them is a
// version whose hash cannot be checked — so a missing artifact is an error
// naming the view and the path, not a silently shorter list.
func ReadViewArtifacts(storeDir, name, hash string, views []string) ([]ViewArtifact, error) {
	out := make([]ViewArtifact, 0, len(views))
	for _, view := range views {
		path := ViewArtifactPath(storeDir, name, hash, view)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading the built view %q of app %q at %s: %w", view, name, path, err)
		}
		out = append(out, ViewArtifact{Name: view, Content: content})
	}
	return out, nil
}

// placeArtifact moves a staged build into the content-addressed store, and
// reports whether it had to.
//
// Stage-then-rename: the artifact appears at its final path complete or not at
// all. Content addressing does the rest — an existing directory under this hash
// already holds this exact content, so a byte-identical re-apply keeps what is
// there rather than rewriting a file some running version may be reading.
func placeArtifact(storeDir, name, hash, staging string) (string, bool, error) {
	target := ArtifactDir(storeDir, name, hash)
	final := filepath.Join(target, ArtifactName)
	if _, err := os.Stat(final); err == nil {
		return final, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", false, fmt.Errorf("creating the app artifact store %s: %w", filepath.Dir(target), err)
	}
	if err := os.Rename(staging, target); err != nil {
		// Another apply of the same content won the race. Its directory holds the
		// same bytes by construction, so the loser has nothing to do.
		if _, statErr := os.Stat(final); statErr == nil {
			return final, false, nil
		}
		return "", false, fmt.Errorf("placing the artifact of app %q at %s: %w", name, target, err)
	}
	return final, true, nil
}

func logf(log func(string), format string, args ...any) {
	if log != nil {
		log(fmt.Sprintf(format, args...))
	}
}
