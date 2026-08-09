// Package apps holds an app's identity: the one name an app has, and the three
// places that name appears.
//
// An app is a manifest-declared automation running in attn's shared runtime.
// Its name is its registry key, and mechanically also its bus consumer
// (`app:<name>`) and its document namespace (`app/<name>`). Deriving both from
// the name rather than storing them is what keeps them from drifting: there is
// no second field anyone can set to something else.
//
// It is deliberately tiny and dependency-free — the manifest parser, the daemon
// handlers and the CLI all need the same rule, and a rule that lives in one of
// them ends up copied into the others.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.
package apps

import (
	"fmt"
	"regexp"
)

// nameRe is the app-name rule: lowercase letters, digits and dashes, starting
// with a letter or digit. It has to survive being a bus consumer name after the
// `app:` prefix, a document-store owner segment after `app/`, and a directory
// name, so it is the intersection of what all three accept.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// MaxNameLength bounds an app name. The tripwire is the document namespace: the
// store validates `owner/name` and an app's namespace is `app/<name>`, so a name
// nobody could address is refused here, where the error can say so, rather than
// deeper where it reads as a store failure. 64 is far past any real name —
// attn's longest today is "approval-gate" at 13.
const MaxNameLength = 64

// ValidateName reports whether a string can be an app name, and says what is
// wrong when it cannot.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("an app name is required, as lowercase letters, digits and dashes (for example approval-gate)")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("app name %q is %d characters, over the %d-character limit", name, len(name), MaxNameLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("app name %q must be lowercase letters, digits and dashes, starting with a letter or digit (for example approval-gate)", name)
	}
	return nil
}

// ConsumerName is the app's durable bus consumer. The prefix is what keeps an
// app from colliding with a platform consumer, and what makes `attn bus status`
// readable at a glance.
func ConsumerName(name string) string { return ConsumerPrefix + name }

// ConsumerPrefix is shared with anything that has to recognise an app's consumer
// among the platform's own.
const ConsumerPrefix = "app:"

// Namespace is the app's document-store namespace. It survives the app's removal
// on purpose: documents are the user's data, not the installation.
func Namespace(name string) string { return NamespacePrefix + name }

// NamespacePrefix is the reserved document-store owner segment for apps.
const NamespacePrefix = "app/"
