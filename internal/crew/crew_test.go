package crew

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCrewFixture builds a copy of the real `~/.attn/crew` shape: three
// members, each with a charter and dated handoff files, plus the loose CREW.md
// that sits beside the homes. Copied by shape, never from the live directory.
func writeCrewFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "crew")
	mustWrite(t, filepath.Join(root, "CREW.md"), "# The crew\n\nkeel, alder, trellis.\n")
	for _, member := range []struct {
		id       string
		handoffs []string
	}{
		{"alder", []string{"2026-08-06T22-51Z-alder.md", "2026-08-10T19-20Z-alder.md"}},
		{"keel", []string{"2026-08-06T00-30Z-keel.md", "2026-08-13T22-10Z-keel.md"}},
		{"trellis", []string{"2026-08-13T22-20Z-trellis.md"}},
	} {
		home := filepath.Join(root, member.id)
		mustWrite(t, filepath.Join(home, CharterFileName), "# "+member.id+"\n\nWhat I care about.\n")
		for _, name := range member.handoffs {
			mustWrite(t, filepath.Join(home, "handoffs", name), "Where I left off.\n")
		}
	}
	return root
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestScanHomes_ReadsEveryHomeAndPointsAtItsFiles(t *testing.T) {
	root := writeCrewFixture(t)

	members, err := ScanHomes(root, nil)
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}

	var ids []string
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	if got, want := strings.Join(ids, ","), "alder,keel,trellis"; got != want {
		t.Fatalf("members = %q, want %q", got, want)
	}
	for _, m := range members {
		if want := filepath.Join(root, m.ID); m.HomeDir != want {
			t.Errorf("%s home = %q, want %q", m.ID, m.HomeDir, want)
		}
		if want := filepath.Join(root, m.ID, CharterFileName); m.CharterPath != want {
			t.Errorf("%s charter = %q, want %q", m.ID, m.CharterPath, want)
		}
		if _, err := os.Stat(m.CharterPath); err != nil {
			t.Errorf("%s charter does not exist: %v", m.ID, err)
		}
	}
}

// The registry records where a home lives; the prose stays on disk. Nothing a
// scan produces carries the charter's text.
func TestScanHomes_CarriesNoProse(t *testing.T) {
	root := writeCrewFixture(t)
	body := "the charter's own words"
	mustWrite(t, filepath.Join(root, "keel", CharterFileName), body)

	members, err := ScanHomes(root, nil)
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}
	for _, m := range members {
		encoded, err := m.Encode()
		if err != nil {
			t.Fatalf("encode %s: %v", m.ID, err)
		}
		if strings.Contains(string(encoded), body) {
			t.Fatalf("%s's record carries the charter's prose: %s", m.ID, encoded)
		}
	}
}

func TestScanHomes_SkipsWhatIsNotAHome(t *testing.T) {
	root := writeCrewFixture(t)
	// A directory with no charter is not a home — a stray notes folder.
	mustWrite(t, filepath.Join(root, "scratch", "note.md"), "not a member\n")
	// A directory whose name cannot be a member id is named, not swallowed.
	mustWrite(t, filepath.Join(root, "Not A Member", CharterFileName), "# nope\n")

	var warnings []string
	members, err := ScanHomes(root, func(format string, args ...any) {
		warnings = append(warnings, format)
	})
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("members = %d, want the 3 real homes", len(members))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d (%v), want exactly the unusable home named", len(warnings), warnings)
	}
}

// A fresh install has no crew directory. That is an empty roster, not a
// failure: nothing should log or refuse over it.
func TestScanHomes_MissingDirectoryIsAnEmptyRoster(t *testing.T) {
	members, err := ScanHomes(filepath.Join(t.TempDir(), "no-crew-here"), func(string, ...any) {
		t.Error("a missing crew directory warned")
	})
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("members = %d, want none", len(members))
	}
}

func TestValidateID(t *testing.T) {
	for _, ok := range []string{"trellis", "keel", "a", "night-shift", "k2"} {
		if err := ValidateID(ok); err != nil {
			t.Errorf("ValidateID(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{"", "Trellis", "2keel", "with space", "with/slash", "-lead", strings.Repeat("a", MaxIDChars+1)} {
		if err := ValidateID(bad); err == nil {
			t.Errorf("ValidateID(%q) = nil, want refused", bad)
		}
	}
}

// The limit failure names the limit, its value, and the ask.
func TestValidateID_LongNameNamesTheLimitAndTheAsk(t *testing.T) {
	asked := strings.Repeat("a", MaxIDChars+7)
	err := ValidateID(asked)
	if err == nil {
		t.Fatal("ValidateID accepted an over-long id")
	}
	for _, want := range []string{"47", "40"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}

func TestResolve_FoldsCaseAndAnswersNoForStrangers(t *testing.T) {
	members := []Member{{ID: "keel"}, {ID: "trellis"}}

	for _, name := range []string{"trellis", "Trellis", " TRELLIS "} {
		member, ok := Resolve(name, members)
		if !ok || member.ID != "trellis" {
			t.Errorf("Resolve(%q) = %v, %v; want trellis", name, member.ID, ok)
		}
	}
	// A worker's free-string name is not a member and resolving says so rather
	// than erroring: unregistered tenders keep tending.
	if _, ok := Resolve("some-worker", members); ok {
		t.Error("Resolve matched an unregistered name")
	}
	if _, ok := Resolve("", members); ok {
		t.Error("Resolve matched the empty name")
	}
}

// A record written by a later attn stays readable: unknown keys are ignored,
// which is what lets the schema move without migrating anything.
func TestDecode_IgnoresUnknownKeys(t *testing.T) {
	member, err := Decode([]byte(`{"id":"keel","home_dir":"/h","mood":"curious"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if member.ID != "keel" || member.HomeDir != "/h" {
		t.Fatalf("Decode = %+v", member)
	}
}

// A declared field a query filters on must exist in every stored body, or a
// filter on `binding_session = ""` would not match the sleeping members.
func TestEncode_WritesDeclaredFieldsEvenWhenEmpty(t *testing.T) {
	encoded, err := Member{ID: "keel"}.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, field := range MembersSchema().Fields {
		if !strings.Contains(string(encoded), `"`+field.Name+`"`) {
			t.Errorf("declared field %q is missing from an empty member's body: %s", field.Name, encoded)
		}
	}
}

func TestMembersSchema_IsAValidDeclaration(t *testing.T) {
	if err := MembersSchema().Validate(); err != nil {
		t.Fatalf("MembersSchema is not declarable: %v", err)
	}
}
