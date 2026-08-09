package apps

import (
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

func TestDerivedIdentities(t *testing.T) {
	if got := ConsumerName("approval-gate"); got != "app:approval-gate" {
		t.Errorf("ConsumerName = %q", got)
	}
	if got := Namespace("approval-gate"); got != "app/approval-gate" {
		t.Errorf("Namespace = %q", got)
	}
}
