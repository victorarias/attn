// Package crew is attn's roster of durable named identities. A crew member is
// a charter, a handoff line, and an address; its sessions are its days. The
// member's home stays plain hand-editable markdown on disk — this package owns
// the registry over those homes: what a member record IS, how a home directory
// becomes one, and how a free-string member name resolves to a registered id.
//
// It holds no database handle and knows nothing about the daemon —
// internal/daemon stores what is built here, under the collection declared
// here. Identity is the invocation, never the files: a session is a member
// because the daemon stamped a binding at its launch, and reading a charter
// confers nothing.
//
// Design: docs/plans/2026-08-11-the-crew-primitive.md.
package crew

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
)

// Surface is what the enrollment fence refuses by name on an outpost. One
// string, so every crew verb refuses in the same words.
const Surface = "the crew"

// The crew's document-store address. `core/` is attn's own namespace owner.
const (
	Namespace         = "core/crew"
	CollectionMembers = "members"
)

// HomesDirName is where members live under the data dir: `~/.attn/crew/<id>/`,
// plain markdown, hand-editable by design. The registry serves reads over it
// and never becomes a second authority for the prose.
const HomesDirName = "crew"

// CharterFileName is the one file that makes a directory a member's home.
const CharterFileName = "CHARTER.md"

// Member is a member's stored body — the registry record, without the store's
// own envelope. Every declared field is written unconditionally, empty string
// included, so a filter on `binding_session = ""` matches the members that are
// asleep.
//
// The whole designed schema is here from the first slice even though only part
// of it moves: `cwd` and `awareness_dirs` are written and returned, and the
// wake slice gives them meaning without touching a single stored body.
type Member struct {
	// ID is the member's durable address — the home directory's name, lowercase,
	// sayable. It is what recognition will later ride, so nothing renames it.
	ID string `json:"id"`
	// CharterPath and HomeDir point at the canonical files; the registry never
	// copies their prose.
	CharterPath string `json:"charter_path"`
	HomeDir     string `json:"home_dir"`
	// CWD is where the member's sessions launch; empty until the wake slice
	// records one.
	CWD string `json:"cwd"`
	// AwarenessDirs are the places the member's charter is about.
	AwarenessDirs []string `json:"awareness_dirs"`
	// BindingSession is the session living this member's current day, or empty.
	// One active binding per member is the single-holder rule; whether a
	// non-empty value still binds is judged at read against live sessions, so a
	// day that ended without ceremony releases on its own.
	BindingSession string `json:"binding_session"`
	// LetterPath and LetterSession are the letter the current day has already
	// filed, and who filed it. They exist so a turnover that failed after the
	// filing can be retried against the letter already on disk rather than
	// against a second one: append-only means the letter is not writable twice,
	// so without this the only way out of a failed nap would be to wait out the
	// minute and file a correction. Cleared when a day starts — a fresh day has
	// written nothing yet.
	LetterPath    string `json:"letter_path"`
	LetterSession string `json:"letter_session"`
}

// FiledLetterFor answers whether sessionID has already filed this member's
// closing letter, and where it landed. A letter recorded against a different
// session belongs to a day that has ended and says nothing about this one.
func (m Member) FiledLetterFor(sessionID string) (string, bool) {
	if sessionID == "" || m.LetterPath == "" || m.LetterSession != sessionID {
		return "", false
	}
	return m.LetterPath, true
}

// MembersSchema declares which fields a query may name. Everything else in the
// body is stored and returned untouched.
func MembersSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionMembers,
		Fields: []docstore.FieldSpec{
			{Name: "binding_session", Type: docstore.FieldString},
		},
	}
}

// Encode renders a member as its stored body.
func (m Member) Encode() ([]byte, error) {
	if m.AwarenessDirs == nil {
		m.AwarenessDirs = []string{}
	}
	return json.Marshal(m)
}

// Decode reads a stored body. Unknown keys are ignored on purpose: a record
// written by a later attn stays readable by an older one.
func Decode(body []byte) (Member, error) {
	var member Member
	if err := json.Unmarshal(body, &member); err != nil {
		return Member{}, fmt.Errorf("this member's stored record is not readable: %w", err)
	}
	return member, nil
}

// MaxIDChars bounds a member id. A member's name is said out loud and typed
// into a flag; the longest real one is 7 characters (`trellis`), so 40 is a
// tripwire only a generated string touches.
const MaxIDChars = 40

var memberIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidateID accepts a member id, naming the shape it wanted. The rule is the
// home directory's: lowercase, starts with a letter, sayable.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("a member id is required")
	}
	if len(id) > MaxIDChars {
		return fmt.Errorf("%q is %d characters and a member id's limit is %d — a member's name is said out loud", id, len(id), MaxIDChars)
	}
	if !memberIDRe.MatchString(id) {
		return fmt.Errorf("%q is not a member id: lowercase letters, digits and - only, starting with a letter, like `trellis`", id)
	}
	return docstore.ValidateDocumentID(id)
}

// Resolve finds the registered member a free-string name addresses, folding
// case so `Trellis` reaches `trellis`. The second return is false when nothing
// is registered under that name — which is a normal answer, not an error: the
// garden's free-string members keep tending unregistered.
func Resolve(name string, members []Member) (Member, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Member{}, false
	}
	for _, m := range members {
		if strings.EqualFold(m.ID, name) {
			return m, true
		}
	}
	return Member{}, false
}

// ScanHomes reads a crew directory into the member records it holds: every
// subdirectory with a CHARTER.md is a home. Loose files beside the homes
// (CREW.md, notes) are not members and are skipped silently; a directory whose
// name is not a member id is skipped with its reason, so a typo'd home is
// named rather than quietly absent from the roster.
//
// A missing crew directory is an empty roster, not an error: every fresh
// install starts without one.
func ScanHomes(dir string, warn func(format string, args ...any)) ([]Member, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading crew homes at %s: %w", dir, err)
	}
	if warn == nil {
		warn = func(string, ...any) {}
	}
	var members []Member
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		home := filepath.Join(dir, id)
		charter := filepath.Join(home, CharterFileName)
		if _, err := os.Stat(charter); err != nil {
			continue
		}
		if err := ValidateID(id); err != nil {
			warn("crew: skipping home %s: %v", home, err)
			continue
		}
		members = append(members, Member{
			ID:            id,
			CharterPath:   charter,
			HomeDir:       home,
			AwarenessDirs: []string{},
		})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members, nil
}
