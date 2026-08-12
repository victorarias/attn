// Package appbuild is the apply pipeline: everything that turns an app
// directory into a built artifact, and nothing that touches the database.
//
// The rule the whole package is arranged around: **apply never evaluates app
// code.** The manifest is data, codegen is string templating, the typecheck and
// the bundle are subprocesses that read source and never run it. Every refusal
// therefore happens with nothing executed and nothing changed — the version flip
// the daemon performs afterwards is the first and only state change an apply
// makes.
//
// The database half lives in internal/store (CommitAppVersion) and the fact and
// the IPC surface in internal/daemon; an app's identity — its name rule, its bus
// consumer, its document namespace — lives in internal/apps and is imported here
// rather than restated, so a manifest cannot disagree with the registry about
// what an app is called.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.
package appbuild

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/docstore"
)

const (
	// APIVersion is the app contract this daemon speaks. A manifest declaring a
	// higher number is refused: it asks for behavior this build does not have,
	// and half-loading it would give the app a runtime that silently ignores
	// what it declared.
	APIVersion = 1

	// ManifestName is the file `attn app apply <path>` looks for.
	ManifestName = "attn-app.toml"
)

// Manifest is `attn-app.toml`, parsed.
//
// The JSON tags are not decoration: the marshalled form is the declaration
// snapshot frozen into the version row, so what an app declared at apply time
// survives editing the file afterwards.
type Manifest struct {
	Name        string       `toml:"name" json:"name"`
	Description string       `toml:"description" json:"description,omitempty"`
	AttnAppAPI  int          `toml:"attn_app_api" json:"attn_app_api"`
	Entrypoint  string       `toml:"entrypoint" json:"entrypoint"`
	Subscribe   []Subscribe  `toml:"subscribe" json:"subscribe,omitempty"`
	Collections []Collection `toml:"collections" json:"collections,omitempty"`
}

// Subscribe is one `[[subscribe]]` block: the event patterns that wake this app.
type Subscribe struct {
	Events []string `toml:"events" json:"events"`
}

// Collection is one `[[collections]]` block. Fields are the queryable ones, all
// string-typed in this API version — the manifest has no syntax for a declared
// type yet, and inventing one before an app needs to sort numerically would be a
// contract written against a guess.
type Collection struct {
	Name   string   `toml:"name" json:"name"`
	Fields []string `toml:"fields" json:"fields,omitempty"`
}

// knownTables is what a manifest may declare, named in the error an unknown
// table produces. Future stages add entries here as they add the runtime that
// honors them — A5 `[[tiles]]`, C1 `[[hooks]]`, B2 `[[workflows]]`.
var knownTables = []string{"subscribe", "collections"}

// LoadManifest reads and validates the manifest in dir.
func LoadManifest(dir string) (Manifest, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, fmt.Errorf("%s has no %s, so it is not an app directory; `attn app new %s` scaffolds one", dir, ManifestName, dir)
		}
		return Manifest{}, fmt.Errorf("reading %s: %w", path, err)
	}
	m, err := ParseManifest(string(data))
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.checkEntrypoint(dir); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// ParseManifest decodes and validates manifest text. It is separate from
// LoadManifest so every rule that does not need the filesystem can be tested as
// one, and so the api-version gate refuses before anything else is inspected.
func ParseManifest(text string) (Manifest, error) {
	var m Manifest
	md, err := toml.Decode(text, &m)
	if err != nil {
		return Manifest{}, fmt.Errorf("not valid TOML: %w", err)
	}
	if err := refuseUnknownKeys(md); err != nil {
		return Manifest{}, err
	}

	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	m.Entrypoint = strings.TrimSpace(m.Entrypoint)

	// The api gate runs before the rest: a manifest from a newer attn may use
	// syntax this build reads as an error, and "unknown table [tiles]" is a
	// worse answer than "this app wants app api 2, this attn speaks 1".
	if err := m.checkAPIVersion(); err != nil {
		return Manifest{}, err
	}
	// The name rule is internal/apps', not a copy of it: the same string is the
	// registry key, the bus consumer and the document namespace, and a parser
	// with its own opinion is how those three drift apart.
	if err := apps.ValidateName(m.Name); err != nil {
		return Manifest{}, err
	}
	if m.Entrypoint == "" {
		return Manifest{}, fmt.Errorf("entrypoint is required, as a path relative to the app directory (for example entrypoint = \"src/index.ts\")")
	}
	if filepath.IsAbs(m.Entrypoint) {
		return Manifest{}, fmt.Errorf("entrypoint %q must be relative to the app directory", m.Entrypoint)
	}
	if err := m.checkSubscriptions(); err != nil {
		return Manifest{}, err
	}
	if err := m.checkCollections(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// refuseUnknownKeys turns TOML this build does not understand into an error that
// names the table and what is supported.
//
// Silently ignoring a declaration is the failure mode this exists to prevent: an
// app that declares `[[tiles]]` against a daemon with no tile runtime would
// install, run its handlers, and never render — half-loaded, with nothing saying
// so. The manifest is a contract, and a contract with an unread clause is not one.
func refuseUnknownKeys(md toml.MetaData) error {
	undecoded := md.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var keys []string
	for _, key := range undecoded {
		full := key.String()
		if seen[full] {
			continue
		}
		seen[full] = true
		keys = append(keys, full)
	}
	sort.Strings(keys)
	subject, verb := "this", "it"
	if len(keys) > 1 {
		subject, verb = "these", "they"
	}
	return fmt.Errorf("declares %s, which this attn does not understand (attn_app_api %d supports the tables %s, and the top-level keys name, description, attn_app_api, entrypoint). "+
		"An app must not half-load: %s ignored, %s would leave the app declaring behavior nothing provides",
		strings.Join(quoteAll(keys), ", "), APIVersion, strings.Join(knownTables, ", "), subject, verb)
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func (m Manifest) checkAPIVersion() error {
	switch {
	case m.AttnAppAPI == 0:
		return fmt.Errorf("attn_app_api is required; this attn speaks app api %d, so set attn_app_api = %d", APIVersion, APIVersion)
	case m.AttnAppAPI < 0:
		return fmt.Errorf("attn_app_api %d is not a version; this attn speaks app api %d", m.AttnAppAPI, APIVersion)
	case m.AttnAppAPI > APIVersion:
		return fmt.Errorf("needs app api %d but this attn speaks app api %d, so it would run against a runtime missing what it declares; upgrade attn, or lower attn_app_api to %d and drop what it added",
			m.AttnAppAPI, APIVersion, APIVersion)
	}
	return nil
}

// checkSubscriptions validates the event patterns and refuses a duplicate.
//
// A duplicate matters beyond tidiness: every pattern becomes one key of the
// generated `Handlers` type, and two blocks naming the same pattern would
// silently collapse into one handler slot — a subscription the author believes
// they declared twice and can only bind once.
func (m Manifest) checkSubscriptions() error {
	seen := map[string]bool{}
	total := 0
	for _, block := range m.Subscribe {
		for _, raw := range block.Events {
			pattern := strings.TrimSpace(raw)
			if err := validateEventPattern(pattern); err != nil {
				return err
			}
			if seen[pattern] {
				return fmt.Errorf("subscribes to %q twice; each event pattern binds one handler, so list it once", pattern)
			}
			seen[pattern] = true
			total++
		}
	}
	if total == 0 {
		// An app with no subscriptions has no way to run: nothing dispatches to
		// it, and it would install as a version that can never execute.
		return fmt.Errorf("declares no subscriptions, so nothing would ever run it; add a [[subscribe]] block with events = [\"session.state.changed\"] (patterns end in .* to match a family)")
	}
	return nil
}

// validateEventPattern accepts what internal/bus can match: an exact dotted fact
// name, or a family prefix ending in `.*`. A bare `*` is refused — an app that
// wakes for every fact in the system is almost always a typo, and the cost of
// being wrong is a handler running on every state change attn makes.
func validateEventPattern(pattern string) error {
	switch {
	case pattern == "":
		return fmt.Errorf("subscribes to an empty event pattern; use a fact name (session.state.changed) or a family (session.*)")
	case pattern == "*":
		return fmt.Errorf("subscribes to %q, which is every fact attn publishes; name the families you handle instead (for example session.*, ticket.*)", pattern)
	}
	body := strings.TrimSuffix(pattern, ".*")
	if body == "" {
		return fmt.Errorf("subscribes to %q, which has no family before the wildcard", pattern)
	}
	for _, segment := range strings.Split(body, ".") {
		if segment == "" {
			return fmt.Errorf("subscribes to %q, which has an empty segment; patterns are dotted names like session.state.changed or session.*", pattern)
		}
		if strings.Contains(segment, "*") {
			return fmt.Errorf("subscribes to %q; a wildcard is only valid as a trailing .* (session.* matches session.state.changed)", pattern)
		}
	}
	return nil
}

// checkCollections validates each declaration against the document store's own
// rules, under the namespace the app will actually write to, so a name the store
// would refuse at write time is refused here instead — at apply, with the
// manifest in hand.
//
// This function does not create the collections. The daemon does, in
// declareAppCollections, once the version is committed and the app is pointed at
// it — creating storage for a build that may still fail typecheck would be a
// state change validation is not allowed to make.
func (m Manifest) checkCollections() error {
	seen := map[string]bool{}
	for _, c := range m.Collections {
		name := strings.TrimSpace(c.Name)
		if seen[name] {
			return fmt.Errorf("declares collection %q twice", name)
		}
		seen[name] = true
		schema := docstore.CollectionSchema{
			Namespace:  apps.Namespace(m.Name),
			Collection: name,
			Fields:     make([]docstore.FieldSpec, 0, len(c.Fields)),
		}
		for _, f := range c.Fields {
			schema.Fields = append(schema.Fields, docstore.FieldSpec{
				Name: strings.TrimSpace(f), Type: docstore.FieldString,
			})
		}
		if err := schema.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// checkEntrypoint is the one rule that needs the directory: the file has to be
// there, inside the app, and a file.
func (m Manifest) checkEntrypoint(dir string) error {
	abs := filepath.Clean(filepath.Join(dir, m.Entrypoint))
	root := filepath.Clean(dir)
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return fmt.Errorf("entrypoint %q points outside the app directory", m.Entrypoint)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("entrypoint %q does not exist (looked for %s)", m.Entrypoint, abs)
		}
		return fmt.Errorf("entrypoint %q: %w", m.Entrypoint, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("entrypoint %q is not a regular file", m.Entrypoint)
	}
	return nil
}

// EventPatterns is every pattern the app subscribes to, in declaration order —
// the keys of the generated `Handlers` type, and the bus filter the runtime
// registers.
func (m Manifest) EventPatterns() []string {
	var out []string
	for _, block := range m.Subscribe {
		for _, e := range block.Events {
			out = append(out, strings.TrimSpace(e))
		}
	}
	return out
}

// Declaration is the frozen snapshot stored on the version row: what this
// manifest said at apply time, readable after the file has moved on.
func (m Manifest) Declaration() (string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encoding the declaration of app %q: %w", m.Name, err)
	}
	return string(data), nil
}
