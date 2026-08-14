package appbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/apps"
)

// These tests run the real toolchain. Faking bun and tsc would test the shape of
// two exec.Command calls and nothing that matters here — the proofs this stage
// owes are "a declared subscription with no handler is a compiler error carrying
// a line" and "a module whose top level throws still applies", and neither
// survives a fake.
//
// CI installs bun for the daemon job precisely so these run there. A machine
// without it skips, loudly.
func requireToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not on PATH; the apply pipeline needs it to bundle and to install the pinned TypeScript")
	}
}

// buildEnv is one app directory plus the artifact store an apply writes into.
type buildEnv struct {
	dir   string
	store string
}

func newBuildEnv(t *testing.T, name string) buildEnv {
	t.Helper()
	requireToolchain(t)
	root := t.TempDir()
	env := buildEnv{dir: filepath.Join(root, name), store: filepath.Join(root, "store")}
	if _, err := Scaffold(ScaffoldOptions{Dir: env.dir}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	// One shared TypeScript install per test binary rather than one per test: the
	// install is network-bound and identical every time.
	env.store = sharedToolchainStore(t, env.store)
	return env
}

// sharedToolchainStore points a test's artifact store at a per-test directory but
// links the toolchain into it, so the compiler is installed once for the package.
func sharedToolchainStore(t *testing.T, store string) string {
	t.Helper()
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	shared := packageToolchainDir(t)
	if err := os.Symlink(shared, filepath.Join(store, toolchainDirName)); err != nil {
		t.Fatalf("linking the shared toolchain: %v", err)
	}
	return store
}

// packageToolchainDir installs the pinned TypeScript once for the whole test
// binary, in a directory that outlives individual tests.
var packageToolchain string

func packageToolchainDir(t *testing.T) string {
	t.Helper()
	if packageToolchain != "" {
		return packageToolchain
	}
	dir, err := os.MkdirTemp("", "attn-appbuild-toolchain-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveToolchain(dir, func(line string) { t.Log(line) }); err != nil {
		t.Fatalf("installing the toolchain: %v", err)
	}
	packageToolchain = filepath.Join(dir, toolchainDirName)
	return packageToolchain
}

func (e buildEnv) build(t *testing.T) (Result, error) {
	t.Helper()
	return Build(context.Background(), Options{Dir: e.dir, StoreDir: e.store})
}

func (e buildEnv) mustBuild(t *testing.T) Result {
	t.Helper()
	res, err := e.build(t)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return res
}

func (e buildEnv) editManifest(t *testing.T, old, new string) {
	t.Helper()
	e.edit(t, ManifestName, old, new)
}

func (e buildEnv) edit(t *testing.T, rel, old, new string) {
	t.Helper()
	path := filepath.Join(e.dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(text, old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dropScaffoldView returns the app to a subscription-only shape. The scaffold
// ships a working view and the command it calls, which is what an author should
// get; these tests are about what a build does with the views they add
// themselves, or with none at all.
func (e buildEnv) dropScaffoldView(t *testing.T) {
	t.Helper()
	e.editManifest(t, "\n[[views]]\n", "\n[views_removed_by_test]\n#")
	data, err := os.ReadFile(filepath.Join(e.dir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	cut := strings.Index(text, "\n[views_removed_by_test]\n")
	if cut < 0 {
		t.Fatal("the scaffold no longer declares a view where this helper expects one")
	}
	if err := os.WriteFile(filepath.Join(e.dir, ManifestName), []byte(text[:cut]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.edit(t, "src/index.ts", "\n  commands: { forget },", "")
	if err := os.RemoveAll(filepath.Join(e.dir, "src", "views")); err != nil {
		t.Fatal(err)
	}
}

// viewArtifact is the built view with this name, by name rather than by
// position: the scaffold ships one of its own, so an index says nothing.
func viewArtifact(t *testing.T, res Result, name string) ViewSize {
	t.Helper()
	for _, v := range res.ViewBytes {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no view named %q in %+v", name, res.ViewBytes)
	return ViewSize{}
}

// The scaffold's bar: `new` then `apply`, untouched. A scaffold that needs an
// edit first is a broken scaffold, and this is the whole assertion.
func TestScaffoldAppliesWithNoEdits(t *testing.T) {
	env := newBuildEnv(t, "hello-app")

	res := env.mustBuild(t)
	if res.Manifest.Name != "hello-app" {
		t.Fatalf("name = %q, want the directory's", res.Manifest.Name)
	}
	if _, err := os.Stat(res.ArtifactPath); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	if !res.ArtifactWritten || res.BundleBytes == 0 {
		t.Fatalf("result = %+v, want a written, non-empty artifact", res)
	}
}

func TestScaffoldWritesClaudeMDAsASymlinkToAgentsMD(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "linked-app")
	if _, err := Scaffold(ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md is not a symlink: %v", err)
	}
	if target != "AGENTS.md" {
		t.Fatalf("CLAUDE.md -> %q, want AGENTS.md", target)
	}
	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
	// It has to be a brief, not a placeholder: the claim is that an agent can
	// write an app from this file alone, so the things it cannot omit are the
	// commands, the contract that binds manifest to code, and the rule that
	// apply does not run what you wrote.
	for _, want := range []string{
		"attn app apply", "attn app rollback", "satisfies Handlers", "never runs your code",
		"ctx.collections",
		// Slice 5: a view is half of what an app is now, so the brief teaches all
		// of it — where a view sits, how it reads, how it acts, what it draws with.
		"useQuery", "useCommand", "params", "EmptyState",
	} {
		if !strings.Contains(string(agents), want) {
			t.Errorf("AGENTS.md does not mention %q", want)
		}
	}
}

func TestScaffoldRefusesADirectoryThatIsAlreadyAnApp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "twice-app")
	if _, err := Scaffold(ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	_, err := Scaffold(ScaffoldOptions{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestScaffoldRefusesADirectoryNameThatIsNotAnAppName(t *testing.T) {
	_, err := Scaffold(ScaffoldOptions{Dir: filepath.Join(t.TempDir(), "My_App")})
	if err == nil {
		t.Fatal("Scaffold accepted an illegal name")
	}
	// Where the name came from matters: the author never typed it.
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("error %q does not say how to choose another name", err)
	}
}

func TestGeneratedTypesCarryTheAppsIdentityAndItsSubscriptions(t *testing.T) {
	m, err := ParseManifest(validManifest(t, viewBlock+commandBlock))
	if err != nil {
		t.Fatal(err)
	}
	types := GenerateTypes(m)
	for _, want := range []string{
		apps.ConsumerName("approval-gate"),
		apps.Namespace("approval-gate"),
		`  subscriptions: {`,
		`"delegation.*": (event: AppEvent, ctx: Ctx)`,
		`"session.state.changed": (event: AppEvent, ctx: Ctx)`,
		`"decisions": Collection`,
		// A command lives in its own group, keyed by its bare name: the kind is
		// structure, so nothing has to keep the two sets of keys apart.
		`  commands: {`,
		`"approve": (payload: unknown, ctx: Ctx) => unknown`,
		`"reject": (payload: unknown, ctx: Ctx) => unknown`,
	} {
		if !strings.Contains(types, want) {
			t.Errorf("generated.ts does not contain %q:\n%s", want, types)
		}
	}
}

// tscError matches the compiler's own `file(line,col): error TSxxxx:` form. The
// assertion is on that shape, not on non-zero exit: an apply that says only
// "typecheck failed" is an apply an agent cannot act on.
var tscError = regexp.MustCompile(`src/index\.ts\((\d+),(\d+)\): error TS\d+:`)

func TestBuild_DeclaredSubscriptionWithNoHandlerIsACompilerError(t *testing.T) {
	env := newBuildEnv(t, "unhandled-app")
	env.editManifest(t, `events = ["session.state.changed"]`, `events = ["session.state.changed", "ticket.created"]`)

	_, err := env.build(t)
	if err == nil {
		t.Fatal("build accepted a subscription with no handler")
	}
	msg := err.Error()
	if !tscError.MatchString(msg) {
		t.Fatalf("error does not carry file(line,col): %s", msg)
	}
	if !strings.Contains(msg, `"ticket.created"`) {
		t.Errorf("error does not name the unhandled subscription: %s", msg)
	}
}

// The same arrow, for the other group: a button a view could press
// with nothing behind it is a compile error at apply, not a 404 at click time.
func TestBuild_DeclaredCommandWithNoHandlerIsACompilerError(t *testing.T) {
	env := newBuildEnv(t, "uncommanded-app")
	env.editManifest(t, "[[commands]]\nname = \"forget\"", "[[commands]]\nname = \"remember\"\n\n[[commands]]\nname = \"forget\"")

	_, err := env.build(t)
	if err == nil {
		t.Fatal("build accepted a command with no handler")
	}
	msg := err.Error()
	if !tscError.MatchString(msg) {
		t.Fatalf("error does not carry file(line,col): %s", msg)
	}
	if !strings.Contains(msg, `"remember"`) {
		t.Errorf("error does not name the unhandled command: %s", msg)
	}
}

// The other direction, which is what grouping by kind buys: a handler under a
// kind nothing declared is an excess property, so a command a view could never
// reach cannot be exported by accident either.
func TestBuild_UndeclaredCommandHandlerIsACompilerError(t *testing.T) {
	env := newBuildEnv(t, "overcommanded-app")
	env.edit(t, "src/index.ts", "commands: { forget },", "commands: { forget, remember: forget },")

	_, err := env.build(t)
	if err == nil {
		t.Fatal("build accepted a command handler the manifest never declared")
	}
	msg := err.Error()
	if !tscError.MatchString(msg) {
		t.Fatalf("error does not carry file(line,col): %s", msg)
	}
	if !strings.Contains(msg, "remember") {
		t.Errorf("error does not name the undeclared handler: %s", msg)
	}
}

func TestBuild_WrongShapedHandlerIsACompilerError(t *testing.T) {
	env := newBuildEnv(t, "misshapen-app")
	env.edit(t, "src/index.ts", "event: AppEvent, ctx: Ctx", "event: string, ctx: Ctx")

	_, err := env.build(t)
	if err == nil {
		t.Fatal("build accepted a wrong-shaped handler")
	}
	if !tscError.MatchString(err.Error()) {
		t.Fatalf("error does not carry file(line,col): %s", err)
	}
}

// The rule the whole slice is arranged around. The entrypoint writes a file and
// then throws at module top level: if any step of apply imported it, the file
// would be there and the apply would have failed.
func TestBuild_NeverEvaluatesAppCode(t *testing.T) {
	env := newBuildEnv(t, "exploding-app")
	sentinel := filepath.Join(t.TempDir(), "evaluated")
	env.edit(t, "src/index.ts", `import type { Ctx, Handlers } from "./generated"`,
		`import type { Ctx, Handlers } from "./generated"
declare function require(m: string): any
require("node:fs").writeFileSync(`+"`"+sentinel+"`"+`, "the apply pipeline evaluated app code")
throw new Error("this app throws the moment it is imported")`)

	res := env.mustBuild(t)
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("the apply pipeline evaluated the app's module top level")
	} else if !os.IsNotExist(err) {
		t.Fatalf("checking the sentinel: %v", err)
	}
	if _, err := os.Stat(res.ArtifactPath); err != nil {
		t.Fatalf("an app that throws at import must still apply: %v", err)
	}
}

func TestBuild_IdenticalContentIsTheSameVersion(t *testing.T) {
	env := newBuildEnv(t, "stable-app")

	first := env.mustBuild(t)
	second := env.mustBuild(t)

	if second.ContentHash != first.ContentHash {
		t.Fatalf("hash moved without an edit: %s then %s", first.ContentHash, second.ContentHash)
	}
	if second.ArtifactWritten {
		t.Error("the second build rewrote an artifact that was already there")
	}
	if second.ArtifactPath != first.ArtifactPath {
		t.Errorf("artifact path moved: %s then %s", first.ArtifactPath, second.ArtifactPath)
	}
}

// A manifest edit that leaves the bundle byte-identical still has to be a new
// version: the generated types are erased at build time, so the bundle cannot see
// the change, and reusing the row would freeze the previous declaration onto it.
func TestBuild_ManifestOnlyChangeIsANewVersion(t *testing.T) {
	env := newBuildEnv(t, "redeclared-app")
	first := env.mustBuild(t)

	env.editManifest(t, `description = "An attn app."`, `description = "Now it says something else."`)
	second := env.mustBuild(t)

	if second.ContentHash == first.ContentHash {
		t.Fatal("a manifest change left the version hash unchanged, so its frozen declaration would be the old one")
	}
	if !strings.Contains(second.Declaration, "Now it says something else.") {
		t.Errorf("declaration did not follow the manifest: %s", second.Declaration)
	}
}

func TestBuild_CodeChangeIsANewVersion(t *testing.T) {
	env := newBuildEnv(t, "edited-app")
	first := env.mustBuild(t)

	env.edit(t, "src/index.ts", "seq: event.seq,", "seq: event.seq, extra: true,")
	second := env.mustBuild(t)

	if second.ContentHash == first.ContentHash {
		t.Fatal("an edit left the version hash unchanged")
	}
	if !second.ArtifactWritten {
		t.Error("a new version did not write an artifact")
	}
	if _, err := os.Stat(first.ArtifactPath); err != nil {
		t.Errorf("the previous version's artifact must survive for rollback: %v", err)
	}
}

// A failed build leaves the artifact store exactly as it found it — no partial
// directory, no staging left behind.
func TestBuild_FailureLeavesTheStoreUntouched(t *testing.T) {
	env := newBuildEnv(t, "broken-app")
	good := env.mustBuild(t)

	env.edit(t, "src/index.ts", "seq: event.seq,", "seq: event.seq, broken: (undefinedIdentifier as string),")
	if _, err := env.build(t); err == nil {
		t.Fatal("build accepted broken code")
	}

	entries, err := os.ReadDir(filepath.Join(env.store, "broken-app"))
	if err != nil {
		t.Fatalf("reading the app's store: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(filepath.Dir(good.ArtifactPath)) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("store holds %v, want only the good version", names)
	}
	staging, err := os.ReadDir(filepath.Join(env.store, ".staging"))
	if err == nil && len(staging) != 0 {
		t.Fatalf("staging left %d directories behind", len(staging))
	}
}

// The view fixtures below import nothing that has to resolve. A view's real
// imports are the SDK's, and the SDK is a package this slice does not build yet
// — so these prove the build step (browser target, whole import graph, one
// artifact per view, hash over all of them) with source that stands alone.
const viewHelperMarker = "helper-from-the-import-graph"

// addView writes a view's source and declares it in the manifest.
func (e buildEnv) addView(t *testing.T, name, source string) {
	t.Helper()
	path := filepath.Join(e.dir, "src", "views", name+".tsx")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(e.dir, ManifestName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	block := fmt.Sprintf("\n[[views]]\nname = %q\nkind = \"tile\"\ntitle = \"Pending\"\nentrypoint = \"src/views/%s.tsx\"\n", name, name)
	if err := os.WriteFile(manifest, append(data, []byte(block)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The artifact layout the whole stage rests on: one module per declared view,
// beside the handler bundle, in the same content-addressed version directory.
func TestBuild_EachViewIsItsOwnArtifactBesideTheBundle(t *testing.T) {
	env := newBuildEnv(t, "viewed-app")
	env.dropScaffoldView(t)
	env.addView(t, "approvals", "export default function Approvals(): string { return \"approvals\" }\n")
	env.addView(t, "history", "export default function History(): string { return \"history\" }\n")

	res := env.mustBuild(t)

	if len(res.ViewBytes) != 2 {
		t.Fatalf("ViewBytes = %+v, want one entry per view", res.ViewBytes)
	}
	for _, v := range res.ViewBytes {
		if v.Bytes == 0 {
			t.Errorf("view %q built to nothing", v.Name)
		}
		want := ViewArtifactPath(env.store, "viewed-app", res.ContentHash, v.Name)
		if v.Path != want {
			t.Errorf("view %q is at %s, want %s", v.Name, v.Path, want)
		}
		if _, err := os.Stat(want); err != nil {
			t.Errorf("view %q has no artifact: %v", v.Name, err)
		}
	}
	if filepath.Dir(filepath.Dir(res.ViewBytes[0].Path)) != filepath.Dir(res.ArtifactPath) {
		t.Errorf("views are not in the version directory: %s vs %s", res.ViewBytes[0].Path, res.ArtifactPath)
	}
}

// The entrypoint is where the build starts, not what it contains: a view's local
// imports land inside its one artifact, so one view is one file is one URL.
func TestBuild_ViewCarriesItsWholeImportGraph(t *testing.T) {
	env := newBuildEnv(t, "graph-app")
	if err := os.MkdirAll(filepath.Join(env.dir, "src", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.dir, "src", "lib", "format.ts"),
		[]byte("export const MARKER = \""+viewHelperMarker+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.addView(t, "approvals", "import { MARKER } from \"../lib/format\"\nexport default function Approvals(): string { return MARKER }\n")

	res := env.mustBuild(t)

	built, err := os.ReadFile(viewArtifact(t, res, "approvals").Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(built), viewHelperMarker) {
		t.Errorf("the view's local import is not inside its artifact:\n%s", built)
	}
}

// The SDK specifiers stay external, so the module the frontend imports resolves
// them against attn's own React rather than carrying a second copy.
func TestBuild_ViewLeavesTheSDKSpecifierUnresolved(t *testing.T) {
	env := newBuildEnv(t, "external-app")
	env.addView(t, "approvals",
		"import { useState } from \""+SDKModule+"\"\nexport default function Approvals(): unknown { return useState }\n")

	res := env.mustBuild(t)

	built, err := os.ReadFile(viewArtifact(t, res, "approvals").Path)
	if err != nil {
		t.Fatal(err)
	}
	// The artifact is minified, so `from` and the specifier may or may not have a
	// space between them; what matters is that the specifier survived as one.
	if !regexp.MustCompile(`from\s*"` + regexp.QuoteMeta(SDKModule) + `"`).MatchString(string(built)) {
		t.Errorf("the SDK import was resolved into the artifact:\n%s", built)
	}
}

// The JSX runtime a view links against, which is a landmine rather than a
// preference: React's production build exports `jsxDEV` as `undefined`, so a
// view built in bun's default mode resolves cleanly against attn's frontend and
// then throws on its first element in the packaged app — where no test looks.
// `--production` is the only thing that selects the other runtime; the app
// tsconfig's `"jsx": "react-jsx"` does not, and neither does a NODE_ENV define.
func TestBuild_ViewLinksAgainstTheProductionJSXRuntime(t *testing.T) {
	env := newBuildEnv(t, "jsx-app")
	env.addView(t, "approvals", "export default function Approvals() { return <div>ok</div> }\n")

	res := env.mustBuild(t)

	built, err := os.ReadFile(viewArtifact(t, res, "approvals").Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(built), SDKModule+"/jsx-runtime") {
		t.Errorf("the view does not import the production JSX runtime:\n%s", built)
	}
	if strings.Contains(string(built), SDKModule+"/jsx-dev-runtime") {
		t.Errorf("the view imports the development JSX runtime, whose jsxDEV is undefined in production React:\n%s", built)
	}
}

// A view-only edit has to mint a new version. The declaration and the handler
// bundle are both untouched by it, so a hash over those alone would reuse a row
// whose artifacts had moved under it.
func TestBuild_ViewOnlyEditIsANewVersion(t *testing.T) {
	env := newBuildEnv(t, "reviewed-app")
	env.addView(t, "approvals", "export default function Approvals(): string { return \"before\" }\n")
	first := env.mustBuild(t)

	env.edit(t, "src/views/approvals.tsx", `"before"`, `"after"`)
	second := env.mustBuild(t)

	if second.ContentHash == first.ContentHash {
		t.Fatal("editing a view left the version hash unchanged, so the version row would name the old artifact")
	}
	if _, err := os.Stat(viewArtifact(t, first, "approvals").Path); err != nil {
		t.Errorf("the previous version's view must survive for rollback: %v", err)
	}
	if _, err := os.Stat(viewArtifact(t, second, "approvals").Path); err != nil {
		t.Errorf("the new version has no view artifact: %v", err)
	}
}

// The digest is over a set of named artifacts, not a list: reordering two
// [[views]] blocks changes nothing about what the version holds, so it must not
// mint a new one. The claim rests on one sort inside versionHash, which is
// exactly the kind of line a later refactor drops while the claim quietly dies.
func TestVersionHash_DoesNotDependOnViewOrder(t *testing.T) {
	declaration := `{"name":"ordered","attn_app_api":1}`
	bundle := []byte("export default {}\n")
	approvals := ViewArtifact{Name: "approvals", Content: []byte("export default 1\n")}
	history := ViewArtifact{Name: "history", Content: []byte("export default 2\n")}

	forward := VersionHash(declaration, bundle, []ViewArtifact{approvals, history})
	backward := VersionHash(declaration, bundle, []ViewArtifact{history, approvals})
	if forward != backward {
		t.Fatalf("reordering the views moved the hash: %s then %s", forward, backward)
	}

	// The names are hashed too, so two views cannot swap contents unnoticed —
	// which is what stops the ordering rule from collapsing the whole digest into
	// "some bytes, in some order".
	swapped := VersionHash(declaration, bundle, []ViewArtifact{
		{Name: approvals.Name, Content: history.Content},
		{Name: history.Name, Content: approvals.Content},
	})
	if swapped == forward {
		t.Fatal("two views swapping contents left the version hash unchanged")
	}
}

// An app with no views hashes exactly as it did before views existed, so
// re-applying an app built by an older attn lands on the row it already had.
func TestBuild_ViewlessAppHashesAsItDidBeforeViews(t *testing.T) {
	env := newBuildEnv(t, "unchanged-app")
	env.dropScaffoldView(t)
	res := env.mustBuild(t)

	bundle, err := os.ReadFile(res.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := sha256.New()
	legacy.Write([]byte(res.Declaration))
	legacy.Write([]byte{0})
	legacy.Write(bundle)
	if got := hex.EncodeToString(legacy.Sum(nil)); got != res.ContentHash {
		t.Fatalf("hash of a view-less app moved: %s, was %s", res.ContentHash, got)
	}
}

// A view is something that runs, so an app that declares one and no
// subscriptions builds and applies — a tile that only reads the document store
// is a legitimate whole app.
func TestBuild_AppWithAViewAndNoSubscriptionsBuilds(t *testing.T) {
	env := newBuildEnv(t, "board-app")
	env.dropScaffoldView(t)
	env.addView(t, "approvals", "export default function Approvals(): string { return \"approvals\" }\n")
	env.editManifest(t, "[[subscribe]]\nevents = [\"session.state.changed\"]\n", "")
	env.edit(t, "src/index.ts",
		"export default {\n  subscriptions: { \"session.state.changed\": onSessionState },\n} satisfies Handlers",
		"export default {} satisfies Handlers")

	res := env.mustBuild(t)

	if len(res.Manifest.Subscribe) != 0 || len(res.ViewBytes) != 1 {
		t.Fatalf("manifest = %+v, views = %+v", res.Manifest, res.ViewBytes)
	}
}

// A broken view fails the apply with the bundler's own words, and leaves the
// store as it found it — the same guarantee the handler bundle already had.
func TestBuild_BrokenViewFailsTheWholeApply(t *testing.T) {
	env := newBuildEnv(t, "brokenview-app")
	env.addView(t, "approvals", "import { missing } from \"./nowhere\"\nexport default function A(): unknown { return missing }\n")

	_, err := env.build(t)
	if err == nil {
		t.Fatal("build accepted a view whose import does not resolve")
	}
	for _, want := range []string{"approvals", "brokenview-app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(env.store, "brokenview-app")); err == nil && len(entries) != 0 {
		t.Fatalf("store holds %d version directories, want none", len(entries))
	}
}

// Codegen must not touch a file it is not changing: `attn app dev` watches this
// directory, and a rewrite with the same bytes would wake the watcher that
// triggered it.
func TestWriteGenerated_LeavesUnchangedFilesAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "quiet-app")
	if _, err := Scaffold(ScaffoldOptions{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.FromSlash(GeneratedFile))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteGenerated(dir, m); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("codegen rewrote an unchanged generated file")
	}
}

// The proof the SDK-as-a-package slice owes: an app can write React, and the
// only specifier it needs is the SDK's own.
//
// The typecheck rather than a whole apply, because bundling a view is the next
// slice's work — a handler bundle carries no SDK JavaScript by design, and the
// materialized package has none to give it. What is proven here is the half this
// slice owns: hooks, JSX and a view's props resolve, with no npm install and no
// `react` anywhere in the app.
func TestTypecheck_AnEntrypointWritingReactResolvesTheSDKAlone(t *testing.T) {
	env := newBuildEnv(t, "tsx-app")
	env.editManifest(t, `entrypoint = "src/index.ts"`, `entrypoint = "src/index.tsx"`)
	if err := os.Remove(filepath.Join(env.dir, "src", "index.ts")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(env.dir, "src", "index.tsx"), `
import { useState, type ReactNode, type ViewProps } from "@victorarias/attn-app"

export default function Approvals({ params, tileId }: ViewProps): ReactNode {
  const [open, setOpen] = useState(true)
  return (
    <div data-tile={tileId}>
      <button onClick={() => setOpen(!open)}>{open ? "hide" : "show"}</button>
      {open ? <p>{params || "everything"}</p> : null}
    </div>
  )
}
`)
	manifest, err := LoadManifest(env.dir)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := ResolveToolchain(env.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSDK(env.store, env.dir, nil); err != nil {
		t.Fatal(err)
	}

	if err := typecheck(context.Background(), tools, env.dir, manifest); err != nil {
		t.Fatalf("an entrypoint using the SDK's React does not typecheck: %v", err)
	}
}

// The other half of "one React": an app cannot reach React except through the
// SDK, so there is no way to end up with a second instance.
func TestBuild_ImportingReactDirectlyIsACompilerError(t *testing.T) {
	env := newBuildEnv(t, "two-reacts-app")
	env.edit(t, "src/index.ts", `import type { Ctx, Handlers } from "./generated"`,
		"import { useState } from \"react\"\nexport const unused = useState\nimport type { Ctx, Handlers } from \"./generated\"")

	_, err := env.build(t)

	if err == nil {
		t.Fatal("an app imported react and applied")
	}
	if !strings.Contains(err.Error(), "Cannot find module 'react'") {
		t.Fatalf("the error does not name the missing module: %v", err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}
