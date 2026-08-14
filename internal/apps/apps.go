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

// RuntimeHostBinaryName is the compiled sidecar every app's handlers run in.
// Three places need the same string and none of them can see the others: the
// build (scripts/build-app-runtime-host.sh) writes it, the daemon resolves it
// beside itself, and the hub ships it to a remote alongside the attn binary.
// TestRuntimeHostBinaryNameMatchesTheBuild pins the build to this one.
const RuntimeHostBinaryName = "attn-app-runtime"

// RuntimeHostBinaryNameForProfile is the same binary under the name a given
// profile installs it as. One machine hosts several profile-isolated daemons —
// `~/.local/bin/attn` beside `~/.local/bin/attn-dev` — and each resolves its
// runtime beside its own binary, so sharing one file name there would have the
// newest sync silently replace another profile's sidecar with a different build.
func RuntimeHostBinaryNameForProfile(profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" {
		return RuntimeHostBinaryName
	}
	return RuntimeHostBinaryName + "-" + p
}

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

// MaxViewNameLength bounds a view name, for the same reason MaxNameLength
// bounds an app's: the name is addressed, not just displayed. It is a file name
// under the version directory and a segment of the tile kind an app view docks
// as, and 64 is far past any real name.
const MaxViewNameLength = 64

// ValidateViewName reports whether a string can name one of an app's views.
//
// It is the app-name rule again, minus the reserved set: a view name is scoped
// to its app, so nothing it could collide with is global. It lives here rather
// than in the manifest parser because the same string is a file name in the
// version directory and a segment of the `app:<app>/<view>` tile kind, and a
// parser with its own opinion is how those drift apart.
func ValidateViewName(name string) error {
	if name == "" {
		return fmt.Errorf("a view name is required, as lowercase letters, digits and dashes (for example approvals)")
	}
	if len(name) > MaxViewNameLength {
		return fmt.Errorf("view name %q is %d characters, over the %d-character limit", name, len(name), MaxViewNameLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("view name %q must be lowercase letters, digits and dashes, starting with a letter or digit (for example pending-approvals)", name)
	}
	return nil
}

// MaxCommandNameLength bounds a command name. The name is addressed — it
// travels on the wire and is a key of the generated Handlers type — so it is
// bounded for the same reason a view name is.
const MaxCommandNameLength = 64

// ValidateCommandName reports whether a string can name one of an app's
// commands.
//
// The view-name rule again, and deliberately the same one: a command name is
// scoped to its app, and the shape that reads well as a tile kind reads well as
// an action a button invokes. Living here rather than in the manifest parser is
// what stops the parser, the daemon's dispatch key and the SDK's hook from
// disagreeing about what a command may be called.
func ValidateCommandName(name string) error {
	if name == "" {
		return fmt.Errorf("a command name is required, as lowercase letters, digits and dashes (for example approve)")
	}
	if len(name) > MaxCommandNameLength {
		return fmt.Errorf("command name %q is %d characters, over the %d-character limit", name, len(name), MaxCommandNameLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("command name %q must be lowercase letters, digits and dashes, starting with a letter or digit (for example approve-request)", name)
	}
	return nil
}

// CommandHandlerKey is the key a command binds to in an app's default export,
// and the handler name its invocations are recorded under.
//
// The prefix is what keeps a command and a subscription apart in one map: an
// event pattern is a dotted name and can never contain a colon, and neither can
// a command name, so `command:approve` collides with nothing. Deriving it here
// means the codegen that writes the type, the daemon that resolves the handler
// and the host that looks it up cannot disagree.
func CommandHandlerKey(command string) string { return CommandHandlerPrefix + command }

// CommandHandlerPrefix is that prefix, shared with anything that has to
// recognise a command among an app's handlers.
const CommandHandlerPrefix = "command:"

// ViewTileKindPrefix is what makes a workspace layout's `tile_kind` an app's
// rather than a built-in one. The prefix is reserved from A5 on: a built-in tile
// kind may never start with it, so a future kind cannot collide with an app's
// name.
//
// `tile_kind` stays daemon-opaque — internal/workspacelayout accepts any
// non-empty string and never looks inside one. This is a naming rule the CLI and
// the frontend both derive from, not a validation the layout performs.
const ViewTileKindPrefix = ConsumerPrefix

// ViewTileKind is how one of an app's views docks: `app:<app>/<view>`. Both
// segments are validated names, so the string has exactly one `/` and parses by
// splitting on the first one.
func ViewTileKind(app, view string) string { return ViewTileKindPrefix + app + "/" + view }

// NoSubscriptionsPattern is the bus filter of an app that declared no
// subscriptions — a fact name nothing publishes. A filter has to be *something*,
// and every other candidate ("", "*") means "everything" somewhere in the bus.
//
// It lives here because two surfaces that cannot see each other need the same
// string: the daemon derives it, and the CLI recognises it to say "nothing"
// where it would otherwise print a fact name no reader could look up.
const NoSubscriptionsPattern = "app.subscribes.to.nothing"

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
