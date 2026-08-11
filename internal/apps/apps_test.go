package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/docstore"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"approval-gate", "a", "app2", "standup-digest-v2", "9lives"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "-leading", "Approval", "with_underscore", "with space", "app/name", "app:name", strings.Repeat("a", MaxNameLength+1)} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", bad)
		}
	}
}

// The name rule exists to serve three surfaces; the document namespace is the
// strictest, so a name this package accepts must be one the store can address.
func TestAcceptedNamesMakeValidNamespaces(t *testing.T) {
	for _, name := range []string{"approval-gate", "a", "9lives", strings.Repeat("a", MaxNameLength)} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q): %v", name, err)
		}
		if err := docstore.ValidateNamespace(Namespace(name)); err != nil {
			t.Errorf("docstore rejects the namespace for app %q: %v", name, err)
		}
	}
}

func TestNameErrorsSayWhatIsWrong(t *testing.T) {
	err := ValidateName(strings.Repeat("a", MaxNameLength+1))
	if err == nil {
		t.Fatal("an over-long name was accepted")
	}
	// A limit someone can hit is a limit they must see: the message names the
	// limit and the ask.
	if !strings.Contains(err.Error(), "65") || !strings.Contains(err.Error(), "64") {
		t.Fatalf("error does not name the ask and the limit: %v", err)
	}
}

// The reserved set is refused by the same rule every surface uses, so a manifest,
// the CLI and the daemon cannot disagree about whether `runtime` is an app.
func TestReservedNamesAreRefused(t *testing.T) {
	for _, name := range ReservedNames() {
		err := ValidateName(name)
		if err == nil {
			t.Fatalf("ValidateName(%q) = nil, want a refusal", name)
		}
		// The refusal teaches: why this one, and what else is taken.
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("ValidateName(%q) does not say the name is reserved: %v", name, err)
		}
		for _, other := range ReservedNames() {
			if !strings.Contains(err.Error(), other) {
				t.Errorf("ValidateName(%q) does not list reserved name %q: %v", name, other, err)
			}
		}
	}
}

// `runtime` is the one that is not a subcommand: it is the supervised child's own
// name, and the reason the whole list exists. Naming it explicitly keeps a future
// edit to the map from quietly dropping it.
func TestRuntimeIsReserved(t *testing.T) {
	if err := ValidateName("runtime"); err == nil {
		t.Fatal("an app could be named runtime, which collides with the shared runtime")
	}
	// A name that merely contains a reserved word is still fine — the rule is the
	// whole name, not a substring.
	for _, ok := range []string{"runtime-monitor", "my-runtime", "statusboard"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
}

func TestDerivedIdentities(t *testing.T) {
	if got := ConsumerName("approval-gate"); got != "app:approval-gate" {
		t.Errorf("ConsumerName = %q", got)
	}
	if got := Namespace("approval-gate"); got != "app/approval-gate" {
		t.Errorf("Namespace = %q", got)
	}
}

// The build writes the sidecar under a name the daemon and the hub both look
// for. Nothing links the shell script to this constant, so a rename there would
// produce a daemon that cannot find its runtime and a remote that is shipped a
// file nobody starts.
func TestRuntimeHostBinaryNameMatchesTheBuild(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test's own file")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "scripts", "build-app-runtime-host.sh")
	contents, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading %s: %v", script, err)
	}
	want := `binary_name="` + RuntimeHostBinaryName + `"`
	if !strings.Contains(string(contents), want) {
		t.Fatalf("%s does not build %q (looked for %s)", script, RuntimeHostBinaryName, want)
	}
}

func TestRuntimeHostBinaryNameForProfile(t *testing.T) {
	if got := RuntimeHostBinaryNameForProfile(""); got != "attn-app-runtime" {
		t.Errorf("default profile = %q", got)
	}
	if got := RuntimeHostBinaryNameForProfile("dev"); got != "attn-app-runtime-dev" {
		t.Errorf("named profile = %q", got)
	}
}
