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
	if m.Name != "approval-gate" || m.Entrypoint != "src/index.ts" || m.AttnAppAPI != APIVersion {
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
	m, err := ParseManifest(validManifest(t, ""))
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
	if back.Name != m.Name || back.Entrypoint != m.Entrypoint || len(back.Subscribe) != len(m.Subscribe) {
		t.Fatalf("declaration lost fields: %+v", back)
	}
	if len(back.Collections) != 1 || back.Collections[0].Name != "decisions" {
		t.Fatalf("declaration lost collections: %+v", back.Collections)
	}
}
