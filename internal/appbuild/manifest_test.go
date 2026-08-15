package appbuild

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/apps"
)

// validManifest is the shape everything here mutates one field of.
func validManifest(t *testing.T, extra string) string {
	t.Helper()
	return fmt.Sprintf(`
name = "approval-gate"
description = "Blocks risky delegation actions until a human approves."
attn_app_api = %d
entrypoint = "src/index.ts"

[[subscribe]]
events = ["delegation.*", "session.state.changed"]

[[collections]]
name = "decisions"
fields = ["status", "requested_by"]
%s`, APIVersion, extra)
}

func TestParseManifest_Valid(t *testing.T) {
	m, err := ParseManifest(validManifest(t, ""))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Name != "approval-gate" || m.Entrypoint != "src/index.ts" || m.AttnAppAPI != APIVersion || m.Reconcile {
		t.Fatalf("manifest = %+v", m)
	}
	if got := m.EventPatterns(); len(got) != 2 || got[0] != "delegation.*" || got[1] != "session.state.changed" {
		t.Fatalf("EventPatterns() = %v", got)
	}
	if len(m.Collections) != 1 || m.Collections[0].Name != "decisions" || len(m.Collections[0].Fields) != 2 {
		t.Fatalf("collections = %+v", m.Collections)
	}
}

// The manifest's idea of a legal app name has to be internal/apps' idea, because
// the same string is the registry key, the bus consumer and the document
// namespace. This is a differential test rather than a list of expectations: a
// parser that grew its own regexp would have to match apps.ValidateName on every
// one of these to stay green, and the fixtures cover each way the two rules
// could plausibly differ.
func TestParseManifest_NameRuleIsTheRegistrys(t *testing.T) {
	names := []string{
		"approval-gate", "a", "9lives", "standup-digest-v2",
		"", "-leading", "Approval", "with_underscore", "with space",
		"app/name", "app:name", "trailing-", strings.Repeat("a", apps.MaxNameLength+1),
	}
	for _, name := range names {
		text := strings.Replace(validManifest(t, ""), `name = "approval-gate"`, fmt.Sprintf("name = %q", name), 1)
		_, parseErr := ParseManifest(text)
		registryErr := apps.ValidateName(name)
		if (parseErr == nil) != (registryErr == nil) {
			t.Errorf("name %q: parser err=%v, registry err=%v — the two disagree", name, parseErr, registryErr)
		}
	}
}

func TestParseManifest_BadNameNamesTheRuleAndTheName(t *testing.T) {
	text := strings.Replace(validManifest(t, ""), `name = "approval-gate"`, `name = "Approval_Gate"`, 1)
	_, err := ParseManifest(text)
	if err == nil {
		t.Fatal("ParseManifest accepted an illegal name")
	}
	msg := err.Error()
	for _, want := range []string{"Approval_Gate", "lowercase letters, digits and dashes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not carry %q", msg, want)
		}
	}
}

// An unknown table must be a loud refusal, not a shrug: an app that declares
// capabilities the runtime cannot honor would otherwise install and half-work.
func TestParseManifest_UnknownTableNamesItAndWhatIsSupported(t *testing.T) {
	text := validManifest(t, `
[[tiles]]
name = "pending"
component = "src/Pending.tsx"
`)
	_, err := ParseManifest(text)
	if err == nil {
		t.Fatal("ParseManifest accepted an unknown table")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"tiles"`) {
		t.Errorf("error %q does not name the offending table", msg)
	}
	for _, supported := range knownTables {
		if !strings.Contains(msg, supported) {
			t.Errorf("error %q does not name the supported table %q", msg, supported)
		}
	}
}

func TestParseManifest_UnknownTopLevelKeyIsRefused(t *testing.T) {
	text := "author = \"someone\"\n" + validManifest(t, "")
	_, err := ParseManifest(text)
	if err == nil || !strings.Contains(err.Error(), `"author"`) {
		t.Fatalf("err = %v, want a refusal naming author", err)
	}
}

func TestParseManifest_ReconcileDefaultsFalseAndFreezesTrue(t *testing.T) {
	m, err := ParseManifest(validManifest(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if m.Reconcile {
		t.Fatal("reconcile defaults true")
	}

	with := strings.Replace(validManifest(t, ""), `entrypoint = "src/index.ts"`, "entrypoint = \"src/index.ts\"\nreconcile = true", 1)
	m, err = ParseManifest(with)
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := m.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(declaration, `"reconcile":true`) {
		t.Fatalf("declaration does not freeze reconcile support: %s", declaration)
	}
}

// A key attn does not understand inside a table it does is named in full, not
// reported as its enclosing table: "collections" is supported, and saying so
// would send the reader looking for the wrong mistake.
func TestParseManifest_UnknownKeyInsideAKnownTableIsNamedInFull(t *testing.T) {
	text := validManifest(t, "\nretention = \"30d\"\n")
	_, err := ParseManifest(text)
	if err == nil || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("err = %v, want a refusal naming the key", err)
	}
}

func TestParseManifest_APIVersionGate(t *testing.T) {
	newer := strings.Replace(validManifest(t, ""),
		fmt.Sprintf("attn_app_api = %d", APIVersion),
		fmt.Sprintf("attn_app_api = %d", APIVersion+1), 1)
	_, err := ParseManifest(newer)
	if err == nil {
		t.Fatal("ParseManifest accepted a manifest from a newer attn")
	}
	msg := err.Error()
	// Both numbers, because the reader has to know which side is behind.
	if !strings.Contains(msg, fmt.Sprint(APIVersion+1)) || !strings.Contains(msg, fmt.Sprint(APIVersion)) {
		t.Errorf("error %q does not name both the manifest's version and attn's", msg)
	}

	missing := strings.Replace(validManifest(t, ""), fmt.Sprintf("attn_app_api = %d", APIVersion), "", 1)
	if _, err := ParseManifest(missing); err == nil || !strings.Contains(err.Error(), "attn_app_api") {
		t.Fatalf("missing attn_app_api: err = %v", err)
	}
}

func TestParseManifest_Subscriptions(t *testing.T) {
	cases := map[string]string{
		"empty pattern":      `events = [""]`,
		"everything":         `events = ["*"]`,
		"interior wildcard":  `events = ["session.*.changed"]`,
		"bare wildcard tail": `events = [".*"]`,
		"empty segment":      `events = ["session..changed"]`,
		"duplicate":          `events = ["session.state.changed", "session.state.changed"]`,
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			text := strings.Replace(validManifest(t, ""),
				`events = ["delegation.*", "session.state.changed"]`, block, 1)
			if _, err := ParseManifest(text); err == nil {
				t.Fatalf("ParseManifest accepted %s", block)
			}
		})
	}

	t.Run("no subscriptions at all", func(t *testing.T) {
		text := `
name = "inert"
attn_app_api = 1
entrypoint = "src/index.ts"
`
		_, err := ParseManifest(text)
		if err == nil || !strings.Contains(err.Error(), "nothing would ever run it") {
			t.Fatalf("err = %v, want a refusal explaining the app could never run", err)
		}
	})
}

// viewBlock is the declaration everything below mutates one field of.
const viewBlock = `
[[views]]
name = "approvals"
kind = "tile"
title = "Pending approvals"
entrypoint = "src/views/Approvals.tsx"
`

func TestParseManifest_Views(t *testing.T) {
	m, err := ParseManifest(validManifest(t, viewBlock))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Views) != 1 {
		t.Fatalf("views = %+v", m.Views)
	}
	v := m.Views[0]
	if v.Name != "approvals" || v.Kind != ViewKindTile || v.Title != "Pending approvals" || v.Entrypoint != "src/views/Approvals.tsx" {
		t.Fatalf("view = %+v", v)
	}
	if v.Params != nil {
		t.Errorf("params = %+v, want none when the manifest declares none", v.Params)
	}
	if got := m.ViewNames(); len(got) != 1 || got[0] != "approvals" {
		t.Errorf("ViewNames() = %v", got)
	}
}

// kind is optional and the declaration records the resolved value, so a default
// that changes in a later api version cannot rewrite what an old version meant.
func TestParseManifest_ViewKindDefaultsToTileAndIsFrozenResolved(t *testing.T) {
	m, err := ParseManifest(validManifest(t, strings.Replace(viewBlock, "kind = \"tile\"\n", "", 1)))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Views[0].Kind != ViewKindTile {
		t.Fatalf("kind = %q, want %q", m.Views[0].Kind, ViewKindTile)
	}
	declaration, err := m.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(declaration, `"kind":"tile"`) {
		t.Errorf("the frozen declaration does not record the resolved kind: %s", declaration)
	}
}

// A kind attn cannot mount is refused at apply, naming the kinds it does mount.
// Installing it would give the app a view nothing renders — half-loaded, with
// nothing saying so.
func TestParseManifest_UnmountableViewKindIsRefused(t *testing.T) {
	_, err := ParseManifest(validManifest(t, strings.Replace(viewBlock, `kind = "tile"`, `kind = "panel"`, 1)))
	if err == nil {
		t.Fatal("ParseManifest accepted a kind this attn cannot mount")
	}
	for _, want := range []string{"panel", ViewKindTile, "approvals"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}

func TestParseManifest_ViewRefusals(t *testing.T) {
	cases := map[string]struct{ old, new, want string }{
		"no name":        {`name = "approvals"`, `name = ""`, "view name is required"},
		"illegal name":   {`name = "approvals"`, `name = "Approvals!"`, "Approvals!"},
		"no title":       {"title = \"Pending approvals\"\n", "", "no title"},
		"no entrypoint":  {"entrypoint = \"src/views/Approvals.tsx\"\n", "", "no entrypoint"},
		"abs entrypoint": {`entrypoint = "src/views/Approvals.tsx"`, `entrypoint = "/etc/passwd"`, "relative"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			text := validManifest(t, strings.Replace(viewBlock, c.old, c.new, 1))
			_, err := ParseManifest(text)
			if err == nil {
				t.Fatalf("ParseManifest accepted %s", name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not carry %q", err, c.want)
			}
		})
	}

	t.Run("duplicate", func(t *testing.T) {
		_, err := ParseManifest(validManifest(t, viewBlock+viewBlock))
		if err == nil || !strings.Contains(err.Error(), "twice") {
			t.Fatalf("err = %v, want a refusal naming the repeated view", err)
		}
	})
}

// The view name rule is internal/apps', because the same string is a file name
// in the version directory and a segment of the `app:<app>/<view>` tile kind.
func TestParseManifest_ViewNameRuleIsTheRegistrys(t *testing.T) {
	names := []string{
		"approvals", "a", "9lives", "pending-v2",
		"", "-leading", "Approvals", "with_underscore", "with space",
		"app/name", "trailing-", strings.Repeat("a", apps.MaxViewNameLength+1),
	}
	for _, name := range names {
		text := validManifest(t, strings.Replace(viewBlock, `name = "approvals"`, fmt.Sprintf("name = %q", name), 1))
		_, parseErr := ParseManifest(text)
		registryErr := apps.ValidateViewName(name)
		if (parseErr == nil) != (registryErr == nil) {
			t.Errorf("view name %q: parser err=%v, registry err=%v — the two disagree", name, parseErr, registryErr)
		}
	}
}

func TestParseManifest_ViewParams(t *testing.T) {
	declared := viewBlock + `params = { label = "Repository", placeholder = "victorarias/attn" }` + "\n"
	m, err := ParseManifest(validManifest(t, declared))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Views[0].Params == nil || m.Views[0].Params.Label != "Repository" || m.Views[0].Params.Placeholder != "victorarias/attn" {
		t.Fatalf("params = %+v", m.Views[0].Params)
	}

	// A params block with nothing to put on the field is a picker asking for an
	// unlabelled string, which is a question the user cannot answer.
	unlabelled := viewBlock + `params = { placeholder = "victorarias/attn" }` + "\n"
	_, err = ParseManifest(validManifest(t, unlabelled))
	if err == nil || !strings.Contains(err.Error(), "label") {
		t.Fatalf("err = %v, want a refusal naming the missing label", err)
	}
}

// The relaxation A5 makes to A4's rule: a view is something that runs, so an app
// that is all view and no handler is a whole app. What stays refused is a
// manifest declaring neither.
func TestParseManifest_AViewCountsAsSomethingThatRuns(t *testing.T) {
	text := `
name = "board"
attn_app_api = 1
entrypoint = "src/index.ts"
` + viewBlock
	m, err := ParseManifest(text)
	if err != nil {
		t.Fatalf("ParseManifest refused an app that is all view: %v", err)
	}
	if len(m.EventPatterns()) != 0 || len(m.Views) != 1 {
		t.Fatalf("manifest = %+v", m)
	}

	neither := `
name = "inert"
attn_app_api = 1
entrypoint = "src/index.ts"
`
	_, err = ParseManifest(neither)
	if err == nil || !strings.Contains(err.Error(), "nothing would ever run it") {
		t.Fatalf("err = %v, want a refusal explaining the app could never run", err)
	}
	// The refusal has to name both ways in, or it teaches half the rule.
	if !strings.Contains(err.Error(), "[[views]]") || !strings.Contains(err.Error(), "[[subscribe]]") {
		t.Errorf("error %q does not name both ways an app can run", err)
	}
}

// commandBlock is what a view's button is bound to.
const commandBlock = `
[[commands]]
name = "approve"
description = "Approve the request."

[[commands]]
name = "reject"
`

func TestParseManifest_Commands(t *testing.T) {
	m, err := ParseManifest(validManifest(t, viewBlock+commandBlock))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if names := m.CommandNames(); len(names) != 2 || names[0] != "approve" || names[1] != "reject" {
		t.Fatalf("CommandNames() = %v", names)
	}
	if m.Commands[0].Description != "Approve the request." || m.Commands[1].Description != "" {
		t.Fatalf("commands = %+v", m.Commands)
	}
}

func TestParseManifest_CommandRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
		want  string
	}{
		{"no name", "\n[[commands]]\ndescription = \"x\"\n", "name"},
		{"not a name", "\n[[commands]]\nname = \"Approve!\"\n", "Approve!"},
		{"declared twice", commandBlock + "\n[[commands]]\nname = \"approve\"\n", "approve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest(validManifest(t, viewBlock+tc.block))
			if err == nil {
				t.Fatalf("ParseManifest accepted %s", tc.block)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// A command is invoked from a view. An app that declares one and has neither a
// view nor a subscription could never run it, and the refusal says which of the
// two is missing rather than repeating the generic "nothing would run it".
func TestParseManifest_ACommandAloneIsNotSomethingThatRuns(t *testing.T) {
	text := `
name = "board"
attn_app_api = 1
entrypoint = "src/index.ts"
` + commandBlock
	_, err := ParseManifest(text)
	if err == nil {
		t.Fatal("ParseManifest accepted an app whose only declaration is a command")
	}
	if !strings.Contains(err.Error(), "[[views]]") || !strings.Contains(err.Error(), "invoked from") {
		t.Fatalf("err = %v, want it to say a command needs a view", err)
	}
}

// The daemon holds the frozen declaration and nothing else, so what an app
// answers is read back out of it — same rule as the views beside it.
func TestDeclaredCommands(t *testing.T) {
	m, err := ParseManifest(validManifest(t, viewBlock+commandBlock))
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := m.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	names, err := DeclaredCommands(declaration)
	if err != nil || len(names) != 2 || names[0] != "approve" {
		t.Fatalf("DeclaredCommands() = %v, %v", names, err)
	}
	if _, err := DeclaredCommands(`{"name":"x","commands":[{"name":"Approve!"}]}`); err == nil {
		t.Fatal("DeclaredCommands accepted a name that is not a command name")
	}
}

// The daemon holds a version's declaration and nothing else, so reading the
// views back out of it is what tells it which artifacts the version is made of.
func TestDeclaredViewNames(t *testing.T) {
	m, err := ParseManifest(validManifest(t, viewBlock))
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := m.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	names, err := DeclaredViewNames(declaration)
	if err != nil || len(names) != 1 || names[0] != "approvals" {
		t.Fatalf("DeclaredViewNames() = %v, %v", names, err)
	}

	// A view name is a path segment by the time it is a file, so a declaration
	// carrying one that is not a name is refused rather than joined into a path.
	if _, err := DeclaredViewNames(`{"name":"x","views":[{"name":"../../etc/passwd"}]}`); err == nil {
		t.Fatal("DeclaredViewNames accepted a name that is not a view name")
	}
}

// A collection name the document store would refuse at write time is refused
// here, at apply, with the app's real namespace in the message.
func TestParseManifest_CollectionsAreCheckedAgainstTheStore(t *testing.T) {
	text := strings.Replace(validManifest(t, ""), `name = "decisions"`, `name = "Decisions!"`, 1)
	_, err := ParseManifest(text)
	if err == nil {
		t.Fatal("ParseManifest accepted a collection name the store would refuse")
	}
	if !strings.Contains(err.Error(), "Decisions!") {
		t.Errorf("error %q does not name the collection", err)
	}

	// The field check runs against the app's real namespace, which is what proves
	// the parser asked the store about the collection this app will actually
	// write to rather than a placeholder.
	reserved := strings.Replace(validManifest(t, ""), `fields = ["status", "requested_by"]`, `fields = ["created_at"]`, 1)
	err = nil
	if _, err = ParseManifest(reserved); err == nil {
		t.Fatal("ParseManifest accepted a reserved field name")
	}
	if !strings.Contains(err.Error(), apps.Namespace("approval-gate")) {
		t.Errorf("error %q does not name the app's namespace", err)
	}

	dup := validManifest(t, "\n[[collections]]\nname = \"decisions\"\n")
	if _, err := ParseManifest(dup); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate collection: err = %v", err)
	}
}

func TestLoadManifest_EntrypointMustBeAFileInsideTheApp(t *testing.T) {
	dir := t.TempDir()
	write := func(entrypoint string) {
		text := strings.Replace(validManifest(t, ""), `entrypoint = "src/index.ts"`, fmt.Sprintf("entrypoint = %q", entrypoint), 1)
		if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("src/index.ts")
	if _, err := LoadManifest(dir); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing entrypoint: err = %v", err)
	}

	write("../outside.ts")
	if _, err := LoadManifest(dir); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("escaping entrypoint: err = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "index.ts"), []byte("export default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write("src/index.ts")
	if _, err := LoadManifest(dir); err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
}

func TestLoadManifest_NoManifestSaysWhatToDo(t *testing.T) {
	_, err := LoadManifest(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "attn app new") {
		t.Fatalf("err = %v, want a refusal pointing at the scaffold", err)
	}
}

// The frozen snapshot is what a version row carries, so it has to survive the
// round trip with the fields the runtime will read back.
func TestManifest_DeclarationRoundTrips(t *testing.T) {
	text := strings.Replace(validManifest(t, ""), `entrypoint = "src/index.ts"`, "entrypoint = \"src/index.ts\"\nreconcile = true", 1)
	m, err := ParseManifest(text)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Declaration()
	if err != nil {
		t.Fatal(err)
	}
	var back Manifest
	if err := json.Unmarshal([]byte(snapshot), &back); err != nil {
		t.Fatalf("declaration is not JSON: %v", err)
	}
	if back.Name != m.Name || back.Entrypoint != m.Entrypoint || !back.Reconcile || len(back.Subscribe) != len(m.Subscribe) {
		t.Fatalf("declaration lost fields: %+v", back)
	}
	if len(back.Collections) != 1 || back.Collections[0].Name != "decisions" {
		t.Fatalf("declaration lost collections: %+v", back.Collections)
	}
}
