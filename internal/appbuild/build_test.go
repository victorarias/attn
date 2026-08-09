package appbuild

import (
	"context"
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
	for _, want := range []string{"attn app apply", "attn app rollback", "satisfies Handlers", "never runs your code", "ctx.collections"} {
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
	m, err := ParseManifest(validManifest(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	types := GenerateTypes(m)
	for _, want := range []string{
		apps.ConsumerName("approval-gate"),
		apps.Namespace("approval-gate"),
		`"delegation.*": (event: AppEvent, ctx: Ctx)`,
		`"session.state.changed": (event: AppEvent, ctx: Ctx)`,
		`"decisions": Collection`,
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
