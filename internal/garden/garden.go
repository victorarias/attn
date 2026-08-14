// Package garden is attn's work graph. A seed is the unit of work — one
// document, one id — and everything else in the vocabulary is a seed too: a
// plot is a seed with children, its root is the crown, a packet is a plot
// flagged as template.
//
// This package owns what a seed IS: its id shape, its stored body, the limits a
// planting must respect, and how a crown renders to markdown. It holds no
// database handle and knows nothing about the daemon — internal/daemon stores
// what is built here, under the collections declared here.
//
// Seed ids are fully-qualified-ready. The stored id is short, stable and
// sayable, unique within its hub; the fully qualified form a future central
// server sees is `<daemon-id>/<local-id>`, minted only where an id leaves its
// daemon. Nothing local ever mints or parses that form, and it cannot leak
// inward by accident: the docstore id charset forbids `/`.
//
// Design: docs/plans/2026-08-06-the-garden-vertical-slices.md and
// docs/plans/2026-08-10-home-garden-crew-arc.md.
package garden

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
)

// Surface is what the enrollment fence refuses by name on an outpost. One
// string, so every garden command refuses in the same words.
const Surface = "the garden"

// The garden's document-store address. `core/` is attn's own namespace owner;
// seeds and notes are separate collections because a long-tended seed must not
// bloat its own document with its log.
const (
	Namespace            = "core/garden"
	CollectionSeeds      = "seeds"
	CollectionNotes      = "notes"
	CollectionDispatches = "dispatches"
)

// Lifecycle states. `planted` → `growing` when someone tends it → `harvested`
// (done) or `withered` (abandoned); `dormant` parks it deliberately. Only
// `planted` is reachable in slice 1; the rest are the vocabulary the transitions
// in slice 2 move between.
const (
	StatusPlanted   = "planted"
	StatusGrowing   = "growing"
	StatusHarvested = "harvested"
	StatusWithered  = "withered"
	StatusDormant   = "dormant"
)

// Edge is one typed relation to another seed. Structure lives here rather than
// in the id, so re-parenting a seed never renames it.
type Edge struct {
	// Kind is `blocks`, `part-of`, `sown-from`, `discovered-from` or
	// `relates-to`; the list is additive and slice 3 gives the first two meaning.
	Kind string `json:"kind"`
	To   string `json:"to"`
}

// Var is one declared variable on a packet's crown, filled at sow time. Carried
// from slice 1 so a packet needs no migration, inert until packets ship.
type Var struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Seed is a seed's stored body — the document, without the store's own envelope
// (id-in-the-table, revision, timestamps). Every declared field is written
// unconditionally, empty string and all: a field a query filters on must exist
// in every body, or `tender_session = ""` would not match the seeds nobody
// holds.
//
// The whole designed schema is here from the first slice even though only part
// of it moves: adding a field later costs nothing (bodies are never rewritten),
// but a seed planted today that a later slice cannot read would.
//
// There is no workspace field. The garden is one space and plots are its only
// grouping (ruled 2026-08-13): the workspace stamp scattered one project's
// seeds across its worktrees, and it retired destructively in slice 5 — no
// compatibility reads, because no production install ever held seed data.
type Seed struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Body is markdown. On a crown it is the plan.
	Body           string `json:"body"`
	Status         string `json:"status"`
	StepSlug       string `json:"step_slug"`
	PlanterSession string `json:"planter_session"`
	PlanterMember  string `json:"planter_member"`
	// TenderSession/TenderMember are who holds it now. Set by `tend` (slice 2);
	// declared from the start because `ready` filters on "no live tender".
	TenderSession string `json:"tender_session"`
	TenderMember  string `json:"tender_member"`
	Edges         []Edge `json:"edges"`
	// Template flags a packet; Gate flags a seed that needs a human and opens a
	// turn rather than entering agent `ready`. Both inert until their slices, both
	// declared so a query can exclude them without a redeclare.
	Template bool  `json:"template"`
	Gate     bool  `json:"gate"`
	Vars     []Var `json:"vars"`
	// Reason is why a seed was harvested or withered.
	Reason string `json:"reason,omitempty"`
}

// SeedsSchema declares which of a seed's fields a query may name. Everything
// else in the body is stored and returned untouched — declaring is about
// queryability and its index, not about shape.
func SeedsSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionSeeds,
		Fields: []docstore.FieldSpec{
			{Name: "status", Type: docstore.FieldString},
			{Name: "step_slug", Type: docstore.FieldString},
			{Name: "tender_session", Type: docstore.FieldString},
			{Name: "template", Type: docstore.FieldBool},
			{Name: "gate", Type: docstore.FieldBool},
		},
	}
}

// NotesSchema declares the log collection. Notes are written from slice 2; the
// collection exists now so the garden's storage is whole from the first day.
func NotesSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionNotes,
		Fields: []docstore.FieldSpec{
			{Name: "seed", Type: docstore.FieldString},
			{Name: "kind", Type: docstore.FieldString},
			{Name: "author_session", Type: docstore.FieldString},
			{Name: "author_member", Type: docstore.FieldString},
		},
	}
}

// Dispatch records that a session was dispatched at a crown. It is scope
// inference, nothing more: flag-free `ready` inside that session answers with
// the plot's ready seeds and launch priming starts from the crown — never a
// fence (the session may tend or plant anything) and never an assignment
// (who-holds-what stays the per-seed tender, which is why no surface renders
// this record). Keyed by session id: a session is dispatched at one crown.
type Dispatch struct {
	SessionID string `json:"session_id"`
	Crown     string `json:"crown"`
}

// DispatchesSchema declares the dispatch collection. `crown` is declared so
// "which sessions were dispatched here" stays one query.
func DispatchesSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace:  Namespace,
		Collection: CollectionDispatches,
		Fields: []docstore.FieldSpec{
			{Name: "crown", Type: docstore.FieldString},
		},
	}
}

// EncodeDispatch renders a dispatch as its stored body.
func (d Dispatch) Encode() ([]byte, error) { return json.Marshal(d) }

// DecodeDispatch reads a stored dispatch body.
func DecodeDispatch(body []byte) (Dispatch, error) {
	var dispatch Dispatch
	if err := json.Unmarshal(body, &dispatch); err != nil {
		return Dispatch{}, fmt.Errorf("this dispatch's stored body is not readable: %w", err)
	}
	return dispatch, nil
}

// Seed ids: `s-` plus six characters of Crockford's base32, which drops i, l, o
// and u — so an id never misreads over a call and never spells a word.
//
// Six characters is 32^6 = 1,073,741,824. At ten thousand seeds (twenty-seven
// years at one a day) one mint lands on a taken id with probability 1e4/2^30 ≈
// 1e-5; across the whole garden's life the chance some pair shares an id is
// n(n-1)/2N ≈ 4.7%, which is why the daemon mints again on a collision instead
// of pretending the case away. Neither number is a corruption risk: planting
// writes create-only, so the store refuses the second write rather than
// overwriting somebody's seed.
// Notes carry the same shape under `n-`, so an id says which collection it
// addresses without a lookup.
const (
	idPrefix     = "s-"
	noteIDPrefix = "n-"
	idBodyLen    = 6
	idAlphabet   = "0123456789abcdefghjkmnpqrstvwxyz"
	idAlphabetN  = 32
)

// NewID mints a seed id.
func NewID() (string, error) { return mintID(idPrefix) }

// mintID mints one id under a prefix. 256 is a whole multiple of the alphabet's
// 32, so the modulo is unbiased and no rejection loop is needed.
func mintID(prefix string) (string, error) {
	buf := make([]byte, idBodyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint %sid: %w", prefix, err)
	}
	out := make([]byte, 0, len(prefix)+idBodyLen)
	out = append(out, prefix...)
	for _, b := range buf {
		out = append(out, idAlphabet[int(b)%idAlphabetN])
	}
	return string(out), nil
}

// ValidateID accepts a minted seed id, naming the shape it wanted so a caller
// that typed one wrong can fix it.
func ValidateID(id string) error {
	body, ok := strings.CutPrefix(id, idPrefix)
	if !ok || len(body) != idBodyLen {
		return fmt.Errorf("%q is not a seed id: a seed id is %q followed by %d characters, like s-7k3f9m", id, idPrefix, idBodyLen)
	}
	for _, r := range body {
		if !strings.ContainsRune(idAlphabet, r) {
			return fmt.Errorf("%q is not a seed id: %q is not one of %s (i, l, o and u are left out so an id never misreads)", id, string(r), idAlphabet)
		}
	}
	return nil
}

// Planting limits. Both are tripwires — a healthy planting never feels them.
//
// Measured 2026-08-12 against production ~/.attn: 59 tickets, longest title 81
// characters, mean 18; longest description 14,920 characters. The largest plan
// doc in this repo, the kind of text a crown's body carries, is 75,843 bytes.
// So the title cap is about five times the longest real title, and the body cap
// about fourteen times the largest plan.
const (
	MaxTitleChars = 400
	MaxBodyBytes  = 1 << 20
	// MaxSlugChars keeps a step slug sayable without ever truncating a real
	// title — it is past the longest one measured above.
	MaxSlugChars = 100
)

// ValidatePlant refuses a planting that cannot be one, naming the limit, its
// value, and what was asked.
func ValidatePlant(title, body string) error {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return fmt.Errorf("a seed needs a title: `attn seed plant \"what this is\"`")
	}
	if n := len([]rune(trimmed)); n > MaxTitleChars {
		return fmt.Errorf("that title is %d characters and the limit is %d; a title names the work in a line — the detail goes in the body (`-m`, or `-m -` to read stdin)", n, MaxTitleChars)
	}
	if n := len(body); n > MaxBodyBytes {
		return fmt.Errorf("that body is %d bytes and the limit is %d; a seed's body is a plan, not an archive", n, MaxBodyBytes)
	}
	return nil
}

// StepSlug derives a seed's name within its plot from its title. It is what
// packets, bond points and narrative cross-references address, so it is derived
// once at planting and then editable — never recomputed behind an author's back.
//
// Only ASCII letters and digits survive. A slug is an address someone types and
// says, not a rendering of the title: "ǅ" lowercases to a character no keyboard
// offers, and a title in a non-Latin script yields "seed" rather than an id
// nobody can retype.
func StepSlug(title string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped by starting "already dashed"
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if runes := []rune(slug); len(runes) > MaxSlugChars {
		slug = strings.Trim(string(runes[:MaxSlugChars]), "-")
	}
	if slug == "" {
		// A title of nothing but punctuation or non-Latin script still gets a
		// slug: an empty one would collide with every other empty one.
		return "seed"
	}
	return slug
}

// Encode renders a seed as its stored body.
func (s Seed) Encode() ([]byte, error) {
	if s.Edges == nil {
		s.Edges = []Edge{}
	}
	if s.Vars == nil {
		s.Vars = []Var{}
	}
	return json.Marshal(s)
}

// Decode reads a stored body. Unknown keys are ignored on purpose: a document
// written by a later attn stays readable by an older one, which is what lets
// the schema move without migrating anything.
func Decode(body []byte) (Seed, error) {
	var seed Seed
	if err := json.Unmarshal(body, &seed); err != nil {
		return Seed{}, fmt.Errorf("this seed's stored body is not readable: %w", err)
	}
	return seed, nil
}

// ExportStamp is the line every export carries. It is visible in the rendered
// markdown rather than an HTML comment, because the file exists to be read and
// annotated by a person who has to know it is not the source.
func ExportStamp(id string) string {
	return fmt.Sprintf("*generated from crown `%s` — edit the crown, not this file.*", id)
}

// Export renders a crown as the markdown a person reads and annotates: the
// title, the stamp, and the body. The child ledger joins it when children exist
// (slice 3 wires edges); slice 6 deletes this bridge by rendering the seed
// itself.
func Export(seed Seed) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(seed.Title))
	fmt.Fprintf(&b, "%s\n", ExportStamp(seed.ID))
	if body := strings.TrimRight(seed.Body, "\n"); body != "" {
		fmt.Fprintf(&b, "\n%s\n", body)
	}
	return b.String()
}
