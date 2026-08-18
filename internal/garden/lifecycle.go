package garden

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/crew"
)

// A seed's life, as one table. `planted` → `growing` when someone tends it →
// `harvested` (done) or `withered` (abandoned); `dormant` parks it. Every door
// is two-way: `replant` reopens a closed seed, and tending un-parks a dormant
// one.
//
// The rules are here, pure, so the whole matrix is one readable table and one
// table-driven test. The daemon is what validates a move — it reads the seed,
// calls Transition, and writes the result against the revision it read — but it
// never re-decides what a legal move is.

// Verb is one lifecycle move.
type Verb string

const (
	VerbTend    Verb = "tend"
	VerbPark    Verb = "park"
	VerbHarvest Verb = "harvest"
	VerbWither  Verb = "wither"
	VerbReplant Verb = "replant"
)

// Verbs is every move, in the order a seed's life runs through them. It is what
// an unknown verb is refused against, so a typo is answered with the whole set.
var Verbs = []Verb{VerbTend, VerbPark, VerbHarvest, VerbWither, VerbReplant}

// Tender is who is asking: an attn session, a crew member, or both. Member is
// the free-string crew name from the skill-layer simulation and snaps to member
// ids when the crew primitive lands.
type Tender struct {
	Session string
	Member  string
}

// Name is how a tender is written into a refusal — the member if it has one,
// because that is the name a person says, and the session id otherwise.
func (t Tender) Name() string {
	if member := strings.TrimSpace(t.Member); member != "" {
		return member
	}
	return strings.TrimSpace(t.Session)
}

// DisplayName is Name() written for a person to read: a crew member reads as
// the name it is, and a bare session id is left exactly as it is. Separate from
// Name() because that one doubles as an identity and emptiness test.
func (t Tender) DisplayName() string { return crew.HolderName(t.Member, t.Session) }

// Named reports whether this tender identifies anybody at all.
func (t Tender) Named() bool { return t.Name() != "" }

// Is reports whether two tenders are the same holder.
//
// A session id is the strong identity, so two calls carrying one are the same
// holder when it matches — the crew name beside it is a label and may be typed
// differently on each call. Without a session id there is nothing else to go on
// and the name is the identity: a terminal pane runs with no ATTN_SESSION_ID by
// design (internal/pty/manager.go strips it, so a shell command never reports
// against a session), and comparing sessions alone would make every member-only
// caller the same person and hand a live claim to whoever asked next.
func (t Tender) Is(other Tender) bool {
	mine, theirs := strings.TrimSpace(t.Session), strings.TrimSpace(other.Session)
	if mine != "" && theirs != "" {
		return mine == theirs
	}
	return mine == theirs && strings.TrimSpace(t.Member) == strings.TrimSpace(other.Member)
}

// Holds reports whether this tender still holds its seed, given a way to ask
// whether a session is one the daemon still knows.
//
// It is the one rule `ready` and `tend` both read. They used to decide
// separately, and a seed whose tender's session had ended was offered by
// `ready` and then refused by `tend` naming a session that no longer exists —
// an answer nobody can act on.
//
// A tender that names only a crew member always holds: attn has no signal that
// a person in a terminal pane walked away, and handing their seed to somebody
// else on a guess is worse than leaving it claimed. A tender with a session
// holds while the daemon still knows it, `recoverable` included — revive brings
// that session back to the seed it was tending.
func (t Tender) Holds(sessionLive func(sessionID string) bool) bool {
	if !t.Named() {
		return false
	}
	if session := strings.TrimSpace(t.Session); session != "" {
		return sessionLive(session)
	}
	return true
}

// move is one verb's rule: which states it accepts, where it lands, and what it
// does to the tender.
type move struct {
	to   string
	from []string
	// claims sets the tender; every other move releases it, because stepping
	// away, finishing and abandoning are all the same thing to the claim.
	claims bool
	// needsReason is harvest's alone: a seed closes as done with what got done.
	// Withering may be wordless — the judgment is often "nobody will ever pick
	// this up" and there is nothing more to say.
	needsReason bool
	// keepsReason marks the two moves that close a seed, the only ones that
	// record why. The other three refuse a reason rather than drop it.
	keepsReason bool
	// resume is what to run to get a seed out of the state this move lands in.
	// It is what an "already there" refusal offers instead of a dead end.
	resume string
}

var moves = map[Verb]move{
	VerbTend: {
		to:     StatusGrowing,
		from:   []string{StatusPlanted, StatusDormant, StatusGrowing},
		claims: true,
		resume: "attn seed park",
	},
	VerbPark: {
		to:     StatusDormant,
		from:   []string{StatusPlanted, StatusGrowing},
		resume: "attn seed tend",
	},
	VerbHarvest: {
		to:          StatusHarvested,
		from:        []string{StatusPlanted, StatusGrowing, StatusDormant},
		needsReason: true,
		keepsReason: true,
		resume:      "attn seed replant",
	},
	VerbWither: {
		to:          StatusWithered,
		from:        []string{StatusPlanted, StatusGrowing, StatusDormant},
		keepsReason: true,
		resume:      "attn seed replant",
	},
	VerbReplant: {
		to:     StatusPlanted,
		from:   []string{StatusHarvested, StatusWithered},
		resume: "attn seed tend",
	},
}

// Closed reports whether a status is an end state. A closed seed reopens before
// it moves again — one rule instead of ten arrows, and it is why `replant` is
// the only verb a harvested seed answers.
func Closed(status string) bool {
	return status == StatusHarvested || status == StatusWithered
}

// ParseVerb reads a wire verb, naming the whole set when it is not one.
func ParseVerb(raw string) (Verb, error) {
	verb := Verb(strings.TrimSpace(strings.ToLower(raw)))
	if _, ok := moves[verb]; ok {
		return verb, nil
	}
	names := make([]string, 0, len(Verbs))
	for _, v := range Verbs {
		names = append(names, string(v))
	}
	return "", fmt.Errorf("%q is not something a seed does; the moves are %s", raw, strings.Join(names, ", "))
}

// Transition applies one move and hands back the seed as it should be stored,
// or refuses by name. It never mutates its argument.
//
// The tender is guarded on `tend` alone, and that is deliberate. Tending is the
// atomic claim, so a second session taking a live one silently is the bug this
// refusal exists for. The other four are fate calls — parking, finishing,
// abandoning — and a seed whose tender walked away must stay reachable by
// somebody; guarding those too would make an ended session a one-way door with
// no key, which is worse than a rude harvest that the log records anyway.
//
// sessionLive is how the claim asks whether the tender is still around, and it
// is the same predicate `ready` reads: a seed `ready` offers must be one `tend`
// accepts.
func Transition(seed Seed, verb Verb, actor Tender, reason string, sessionLive func(sessionID string) bool) (Seed, error) {
	rule, ok := moves[verb]
	if !ok {
		return Seed{}, fmt.Errorf("%q is not something a seed does", verb)
	}
	reason = strings.TrimSpace(reason)

	if !slices.Contains(rule.from, seed.Status) {
		return Seed{}, refuseState(seed, verb, rule)
	}
	if rule.claims {
		if !actor.Named() {
			return Seed{}, fmt.Errorf(
				"tending %s records who holds it and this call named nobody; run it from an attn session, or pass --member <name>", seed.ID)
		}
		if held := seed.Tender(); held.Holds(sessionLive) && !held.Is(actor) {
			return Seed{}, fmt.Errorf(
				"%s is being tended by %s, and a seed has one tender at a time.\n"+
					"Wait for %s to harvest or park it, or say what you need on the log: attn seed note %s -m \"…\"",
				seed.ID, held.DisplayName(), held.DisplayName(), seed.ID)
		}
	}
	if rule.needsReason && reason == "" {
		return Seed{}, fmt.Errorf(
			"harvesting %s records what got done: attn seed harvest %s -m \"what got done\"", seed.ID, seed.ID)
	}
	// Only the two moves that close a seed keep a reason. The five verbs share a
	// flag set, so a reason handed to the other three would otherwise be dropped
	// on the floor — and text somebody wrote must never vanish without a word.
	if reason != "" && !rule.keepsReason {
		return Seed{}, fmt.Errorf(
			"%s records no reason — harvest and wither are the moves that close a seed with one. Put it on the log instead: attn seed note %s -m \"…\"",
			verb, seed.ID)
	}
	if n := len(reason); n > MaxReasonChars {
		return Seed{}, fmt.Errorf(
			"that reason is %d characters and the limit is %d; the detail belongs on the log (`attn seed note %s -m …`)",
			n, MaxReasonChars, seed.ID)
	}

	next := seed
	next.Status = rule.to
	switch {
	case rule.claims:
		next.TenderSession = strings.TrimSpace(actor.Session)
		next.TenderMember = strings.TrimSpace(actor.Member)
	default:
		next.TenderSession = ""
		next.TenderMember = ""
	}
	switch {
	case rule.keepsReason:
		next.Reason = reason
	case verb == VerbReplant:
		// The reason said why it closed. It is reopened, so it no longer applies
		// and leaving it would read as the reason it is open.
		next.Reason = ""
	}
	return next, nil
}

// Tender is who holds the seed now.
func (s Seed) Tender() Tender {
	return Tender{Session: s.TenderSession, Member: s.TenderMember}
}

// refuseState names the seed, the state it is actually in, and the way out —
// the three things an agent needs to fix the call itself.
func refuseState(seed Seed, verb Verb, rule move) error {
	switch {
	// Replant first: its landing state is `planted`, so an "already planted"
	// answer would hide the rule the caller actually needs.
	case verb == VerbReplant:
		return fmt.Errorf(
			"%s is %s, not closed; replant reopens a harvested or withered seed, and `attn seed tend %s` picks up a live one",
			seed.ID, seed.Status, seed.ID)
	case seed.Status == rule.to:
		return fmt.Errorf("%s is already %s; `%s %s` is the way out of it", seed.ID, seed.Status, rule.resume, seed.ID)
	case Closed(seed.Status):
		return fmt.Errorf(
			"%s is %s, and a closed seed reopens before it moves again: `attn seed replant %s`, then %s it",
			seed.ID, seed.Status, seed.ID, verb)
	default:
		return fmt.Errorf("%s is %s and cannot be %sed from there", seed.ID, seed.Status, verb)
	}
}

// Note is one entry on a seed's log: the seed's memory of itself, written by
// whoever was there and read by whoever tends it next. A note is routed to
// nobody — it is anchored to the work, not addressed to a person.
type Note struct {
	ID   string `json:"id"`
	Seed string `json:"seed"`
	// Kind is `note`, `handoff`, `attach` or `detach`; the notes collection
	// indexes it, which is what makes "the freshest handoff on this seed" one
	// query.
	Kind          string `json:"kind"`
	Body          string `json:"body"`
	AuthorSession string `json:"author_session"`
	AuthorMember  string `json:"author_member"`
	// Artifact is the typed association an `attach` or `detach` carries, and is
	// nil on every other kind. The current set is projected from these entries;
	// see artifact.go.
	Artifact *ArtifactReference `json:"artifact,omitempty"`
}

// The kinds a note may carry. A plain note is the seed's memory of itself; a
// handoff is written to whoever tends the seed next, which is why `show` and
// `tend` put the freshest one in front of them instead of leaving it in the
// log to be scrolled past. `attach` and `detach` associate a document with the
// seed and take it back; they are the only kinds that carry a reference.
const (
	NoteKindNote    = "note"
	NoteKindHandoff = "handoff"
	NoteKindAttach  = "attach"
	NoteKindDetach  = "detach"
)

// NoteKinds is every kind, in the order they are offered to a caller that named
// one that is not a kind.
var NoteKinds = []string{NoteKindNote, NoteKindHandoff, NoteKindAttach, NoteKindDetach}

// CarriesArtifact reports whether a kind is one of the two that associate a
// document. It is what refuses a reference on a plain note and a bare attach —
// both would store something no surface can act on.
func CarriesArtifact(kind string) bool {
	return kind == NoteKindAttach || kind == NoteKindDetach
}

// ParseNoteKind reads a wire kind, naming the whole set when it is not one. An
// empty kind is the plain log entry: a caller that never heard of kinds writes
// a note, which is what `attn seed note` has always done.
func ParseNoteKind(raw string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(raw))
	if kind == "" {
		return NoteKindNote, nil
	}
	if slices.Contains(NoteKinds, kind) {
		return kind, nil
	}
	return "", fmt.Errorf("%q is not a kind of note; the kinds are %s", raw, strings.Join(NoteKinds, ", "))
}

// Note limits.
//
// MaxNoteBytes is a tripwire: the longest description in production ~/.attn on
// 2026-08-12 was 14,920 characters, and a note is a paragraph about what
// happened, not a document. 32KiB is 2.1x that longest real body.
//
// It cannot sit on 64KiB, which is what the note travels through: a note
// reaches the daemon as one JSON object in a single unix-socket frame, and that
// frame is capped at exactly 64KiB. JSON escaping grows the body on the way in
// — measured 2026-08-12, 45KB of ordinary text with light escaping arrives as
// 75KB — so a limit set at the frame size is answered by the transport first,
// and the caller reads "initial socket frame exceeds 65536 bytes" instead of
// being told the note limit and pointed at the log. Same arithmetic as
// protocol.AgentMessageMaxChars, which travels the same frame.
//
// MaxReasonChars keeps a closing reason to the one line `ls` and the panel
// show; anything longer is a note, and the refusal says so.
const (
	MaxNoteBytes   = 32 << 10
	MaxReasonChars = 400
	// ShowNotes is how many of a seed's notes `show` renders inline. Five is
	// what fits above the fold beside the seed's own fields; the rest are
	// counted and named, never silently dropped.
	ShowNotes = 5
)

// ValidateNote refuses a note that carries nothing, naming the limit and the ask.
func ValidateNote(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("a note needs something in it: `attn seed note <id> -m \"what happened\"`")
	}
	if n := len(body); n > MaxNoteBytes {
		return fmt.Errorf("that note is %d bytes and the limit is %d; a note is what happened and what you learned, not an archive", n, MaxNoteBytes)
	}
	return nil
}

// Author is who wrote a note, rendered the way a tender is.
func (n Note) Author() Tender {
	return Tender{Session: n.AuthorSession, Member: n.AuthorMember}
}

// NewNoteID mints a note id — the same shape as a seed id under its own prefix,
// so nothing that reads an id has to guess which collection it addresses.
func NewNoteID() (string, error) { return mintID(noteIDPrefix) }

// Encode renders a note as its stored body.
func (n Note) Encode() ([]byte, error) { return json.Marshal(n) }

// DecodeNote reads a stored note body. Unknown keys are ignored for the same
// reason a seed's are: a document written by a later attn stays readable.
func DecodeNote(body []byte) (Note, error) {
	var note Note
	if err := json.Unmarshal(body, &note); err != nil {
		return Note{}, fmt.Errorf("this note's stored body is not readable: %w", err)
	}
	return note, nil
}
