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
	"sort"
	"strings"
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

// reserved is the set of names an app may not take. Two groups, one rule.
//
// `runtime` is the shared sidecar's own name: it is the supervised child, the
// subject of `app.runtime.*` diagnostics and the stem of `runtime.log`. The rest
// are the `attn app` subcommands. The grammar never confuses either group with an
// app — a subcommand and its argument sit in different positions — but every
// human-facing surface would: "app runtime parked" and `attn app logs runtime`
// stop having one meaning the moment an app can be called that.
//
// Refusing them costs nothing while zero real apps exist. Refusing them later is
// a migration, so the list is deliberately generous rather than minimal.
var reserved = map[string]string{
	"runtime":  "the shared app runtime is called `runtime`",
	"new":      "it is an `attn app` subcommand",
	"apply":    "it is an `attn app` subcommand",
	"rollback": "it is an `attn app` subcommand",
	"enable":   "it is an `attn app` subcommand",
	"disable":  "it is an `attn app` subcommand",
	"remove":   "it is an `attn app` subcommand",
	"list":     "it is an `attn app` subcommand",
	"status":   "it is an `attn app` subcommand",
	"logs":     "it is an `attn app` subcommand",
	"dev":      "it is an `attn app` subcommand",
}

// ReservedNames lists what ValidateName refuses, sorted. It is exported so an
// error, a test or a scaffold can name the whole set rather than restate part of
// it.
func ReservedNames() []string {
	out := make([]string, 0, len(reserved))
	for name := range reserved {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

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
	if why, taken := reserved[name]; taken {
		return fmt.Errorf("app name %q is reserved because %s; an app named %q would make every log line, fact and notification about it ambiguous. Reserved names: %s",
			name, why, name, strings.Join(ReservedNames(), ", "))
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
