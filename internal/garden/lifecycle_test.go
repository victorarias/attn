package garden

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	me    = "sess-me"
	other = "sess-you"
)

// alive is a daemon that still knows every session. Most of these cases are
// about the state machine, not about who walked away; the cases that are name
// their own predicate.
func alive(string) bool { return true }

// gone is a daemon that knows no session at all.
func gone(string) bool { return false }

func seedIn(status string, tender Tender) Seed {
	return Seed{
		ID: "s-7k3f9m", Title: "a seed", Status: status,
		TenderSession: tender.Session, TenderMember: tender.Member,
	}
}

// The whole matrix, in one table. Every cell is either a landing state or a
// refusal, and a cell that is neither is a state a seed can reach with nobody
// having decided what it means.
func TestTransitionMatrix(t *testing.T) {
	held := Tender{Session: other, Member: "alder"}
	mine := Tender{Session: me, Member: "trellis"}

	cases := []struct {
		name string
		seed Seed
		verb Verb
		// want is the status the seed lands in; refuse is the substring a
		// refusal must carry. Exactly one of them is set.
		want   string
		refuse string
	}{
		{name: "planted/tend", seed: seedIn(StatusPlanted, Tender{}), verb: VerbTend, want: StatusGrowing},
		{name: "planted/park", seed: seedIn(StatusPlanted, Tender{}), verb: VerbPark, want: StatusDormant},
		{name: "planted/harvest", seed: seedIn(StatusPlanted, Tender{}), verb: VerbHarvest, want: StatusHarvested},
		{name: "planted/wither", seed: seedIn(StatusPlanted, Tender{}), verb: VerbWither, want: StatusWithered},
		{name: "planted/replant", seed: seedIn(StatusPlanted, Tender{}), verb: VerbReplant, refuse: "not closed"},

		{name: "growing by me/tend", seed: seedIn(StatusGrowing, mine), verb: VerbTend, want: StatusGrowing},
		{name: "growing by me/park", seed: seedIn(StatusGrowing, mine), verb: VerbPark, want: StatusDormant},
		{name: "growing by me/harvest", seed: seedIn(StatusGrowing, mine), verb: VerbHarvest, want: StatusHarvested},
		{name: "growing by me/wither", seed: seedIn(StatusGrowing, mine), verb: VerbWither, want: StatusWithered},
		{name: "growing by me/replant", seed: seedIn(StatusGrowing, mine), verb: VerbReplant, refuse: "not closed"},

		// The showpiece: a second session cannot take a live claim. The other
		// four are fate calls and stay open to anybody, so a tender that walked
		// away never locks a seed shut.
		{name: "growing by another/tend", seed: seedIn(StatusGrowing, held), verb: VerbTend, refuse: "one tender at a time"},
		{name: "growing by another/park", seed: seedIn(StatusGrowing, held), verb: VerbPark, want: StatusDormant},
		{name: "growing by another/harvest", seed: seedIn(StatusGrowing, held), verb: VerbHarvest, want: StatusHarvested},
		{name: "growing by another/wither", seed: seedIn(StatusGrowing, held), verb: VerbWither, want: StatusWithered},
		{name: "growing by another/replant", seed: seedIn(StatusGrowing, held), verb: VerbReplant, refuse: "not closed"},

		{name: "dormant/tend", seed: seedIn(StatusDormant, Tender{}), verb: VerbTend, want: StatusGrowing},
		{name: "dormant/park", seed: seedIn(StatusDormant, Tender{}), verb: VerbPark, refuse: "already dormant"},
		{name: "dormant/harvest", seed: seedIn(StatusDormant, Tender{}), verb: VerbHarvest, want: StatusHarvested},
		{name: "dormant/wither", seed: seedIn(StatusDormant, Tender{}), verb: VerbWither, want: StatusWithered},
		{name: "dormant/replant", seed: seedIn(StatusDormant, Tender{}), verb: VerbReplant, refuse: "not closed"},

		{name: "harvested/tend", seed: seedIn(StatusHarvested, Tender{}), verb: VerbTend, refuse: "reopens before it moves"},
		{name: "harvested/park", seed: seedIn(StatusHarvested, Tender{}), verb: VerbPark, refuse: "reopens before it moves"},
		{name: "harvested/harvest", seed: seedIn(StatusHarvested, Tender{}), verb: VerbHarvest, refuse: "already harvested"},
		{name: "harvested/wither", seed: seedIn(StatusHarvested, Tender{}), verb: VerbWither, refuse: "reopens before it moves"},
		{name: "harvested/replant", seed: seedIn(StatusHarvested, Tender{}), verb: VerbReplant, want: StatusPlanted},

		{name: "withered/tend", seed: seedIn(StatusWithered, Tender{}), verb: VerbTend, refuse: "reopens before it moves"},
		{name: "withered/park", seed: seedIn(StatusWithered, Tender{}), verb: VerbPark, refuse: "reopens before it moves"},
		{name: "withered/harvest", seed: seedIn(StatusWithered, Tender{}), verb: VerbHarvest, refuse: "reopens before it moves"},
		{name: "withered/wither", seed: seedIn(StatusWithered, Tender{}), verb: VerbWither, refuse: "already withered"},
		{name: "withered/replant", seed: seedIn(StatusWithered, Tender{}), verb: VerbReplant, want: StatusPlanted},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.seed
			// Only the closing moves take a reason; the others refuse one, and
			// that refusal has its own test below.
			reason := ""
			if tc.verb == VerbHarvest || tc.verb == VerbWither {
				reason = "because"
			}
			next, err := Transition(tc.seed, tc.verb, mine, reason, alive)
			// A move never edits the seed it was handed: the daemon writes the
			// result against the revision it read, and a mutated input would make
			// a refused move leave a changed seed behind.
			if !reflect.DeepEqual(tc.seed, before) {
				t.Fatalf("the input seed was mutated: %+v", tc.seed)
			}
			if tc.refuse != "" {
				if err == nil {
					t.Fatalf("%s from %s was allowed, want a refusal", tc.verb, tc.seed.Status)
				}
				if !strings.Contains(err.Error(), tc.refuse) {
					t.Fatalf("refusal = %q, want it to say %q", err, tc.refuse)
				}
				// Every refusal names the seed, so an agent reading a log knows
				// which one it was talking about.
				if !strings.Contains(err.Error(), tc.seed.ID) {
					t.Fatalf("refusal does not name the seed: %s", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s from %s: %v", tc.verb, tc.seed.Status, err)
			}
			if next.Status != tc.want {
				t.Fatalf("%s from %s landed in %q, want %q", tc.verb, tc.seed.Status, next.Status, tc.want)
			}
			// A move never edits the seed it was handed: the daemon writes the
			// result against the revision it read, and a mutated input would
			// make that write carry a version nobody decided on.
			if tc.seed.Status == next.Status && tc.seed.TenderSession != seedIn(tc.seed.Status, Tender{Session: tc.seed.TenderSession, Member: tc.seed.TenderMember}).TenderSession {
				t.Fatal("the input seed was mutated")
			}
		})
	}
}

// The tender moves with the claim: tending takes it, and every other move lets
// it go — stepping away, finishing and abandoning all end the claim.
func TestTransitionMovesTheTender(t *testing.T) {
	actor := Tender{Session: me, Member: "trellis"}

	claimed, err := Transition(seedIn(StatusPlanted, Tender{}), VerbTend, actor, "", alive)
	if err != nil {
		t.Fatalf("tend: %v", err)
	}
	if claimed.TenderSession != me || claimed.TenderMember != "trellis" {
		t.Fatalf("tend did not record the tender: %+v", claimed)
	}

	for _, tc := range []struct {
		verb   Verb
		reason string
	}{{VerbPark, ""}, {VerbHarvest, "done"}, {VerbWither, "done"}} {
		released, err := Transition(claimed, tc.verb, actor, tc.reason, alive)
		if err != nil {
			t.Fatalf("%s: %v", tc.verb, err)
		}
		if released.TenderSession != "" || released.TenderMember != "" {
			t.Fatalf("%s left the seed claimed: %+v", tc.verb, released)
		}
	}
}

// Three of the five moves record nothing, so a reason handed to one of them
// would be dropped on the floor. Text somebody wrote never vanishes without a
// word: the move is refused and the refusal points at the log.
func TestTransitionRefusesAReasonTheMoveWouldDrop(t *testing.T) {
	actor := Tender{Session: me}
	for _, verb := range []Verb{VerbTend, VerbPark, VerbReplant} {
		from := StatusPlanted
		if verb == VerbReplant {
			from = StatusHarvested
		}
		_, err := Transition(seedIn(from, Tender{}), verb, actor, "some words", alive)
		if err == nil {
			t.Fatalf("%s swallowed a reason instead of refusing it", verb)
		}
		for _, want := range []string{string(verb), "attn seed note", "s-7k3f9m"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s refusal = %q, want it to say %q", verb, err, want)
			}
		}
	}
}

// A closing reason says why the seed closed. Reopening it makes that reason
// untrue, so replant clears it rather than leaving it to read as the reason the
// seed is open.
func TestReplantClearsTheClosingReason(t *testing.T) {
	actor := Tender{Session: me}
	harvested, err := Transition(seedIn(StatusPlanted, Tender{}), VerbHarvest, actor, "shipped it", alive)
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if harvested.Reason != "shipped it" {
		t.Fatalf("harvest did not record the reason: %+v", harvested)
	}
	replanted, err := Transition(harvested, VerbReplant, actor, "", alive)
	if err != nil {
		t.Fatalf("replant: %v", err)
	}
	if replanted.Reason != "" {
		t.Fatalf("replant kept the closing reason %q", replanted.Reason)
	}
}

// The refusal an agent is most likely to hit has to be actionable on its own:
// which seed, who holds it, and what to do instead.
func TestTendRefusalNamesTheTenderAndTheWayForward(t *testing.T) {
	held := seedIn(StatusGrowing, Tender{Session: other, Member: "alder"})
	_, err := Transition(held, VerbTend, Tender{Session: me, Member: "trellis"}, "", alive)
	if err == nil {
		t.Fatal("a second session was allowed to take a live claim")
	}
	for _, want := range []string{held.ID, "alder", "attn seed note"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not name %q:\n%s", want, err)
		}
	}
}

// A session id is an ugly name, but it is a name. A tender with no member still
// blocks a second claim, and the refusal says whose it is.
// Two names, no session ids: the case a terminal pane actually produces.
// internal/pty/manager.go strips ATTN_SESSION_ID from a shell pane on purpose,
// so `attn seed tend --member <name>` is how a person in a pane claims a seed —
// and comparing sessions alone made every one of them the same holder, handing
// a live claim to whoever asked next. Caught in live verification, not by the
// session-carrying tests above.
func TestTendRefusesAnotherMemberWhenNeitherCarriesASession(t *testing.T) {
	held := Seed{ID: "s-abc123", Status: StatusGrowing, TenderMember: "trellis"}

	if _, err := Transition(held, VerbTend, Tender{Member: "alder"}, "", alive); err == nil {
		t.Fatal("a different member took a live claim; the seed has one tender at a time")
	} else if !strings.Contains(err.Error(), "trellis") {
		t.Fatalf("refusal does not name who holds it: %v", err)
	}

	// The same name is the same person picking their own work back up.
	if _, err := Transition(held, VerbTend, Tender{Member: "trellis"}, "", alive); err != nil {
		t.Fatalf("trellis was refused their own claim: %v", err)
	}
}

// A session id outranks the label beside it: the same session re-tending under a
// different crew name is one holder, not two.
func TestTendIdentifiesASessionByItsIDNotItsLabel(t *testing.T) {
	held := Seed{ID: "s-abc123", Status: StatusGrowing, TenderSession: "sess-a", TenderMember: "trellis"}

	if _, err := Transition(held, VerbTend, Tender{Session: "sess-a", Member: "keel"}, "", alive); err != nil {
		t.Fatalf("the holding session was refused its own claim: %v", err)
	}
	// And a session arriving at a claim nobody attached a session to is somebody
	// else, whatever it calls itself.
	memberOnly := Seed{ID: "s-abc123", Status: StatusGrowing, TenderMember: "trellis"}
	if _, err := Transition(memberOnly, VerbTend, Tender{Session: "sess-a", Member: "trellis"}, "", alive); err == nil {
		t.Fatal("a session took a claim held with no session id")
	}
}

// `ready` and `tend` read one rule, so a seed offered by one is accepted by the
// other. They used to decide separately: a tender whose session had ended
// released the seed into `ready` and then refused the claim, naming a session
// that no longer exists — an answer nobody can act on, and the wall the
// handoff flow hits, since leaving a handoff and ending is exactly what a
// successor picks up from.
func TestTendReleasesASeedWhoseTenderSessionIsGone(t *testing.T) {
	held := seedIn(StatusGrowing, Tender{Session: other, Member: "alder"})

	if got := held.Tender().Holds(gone); got {
		t.Fatal("a tender whose session the daemon no longer knows still holds its seed")
	}
	claimed, err := Transition(held, VerbTend, Tender{Session: me, Member: "trellis"}, "", gone)
	if err != nil {
		t.Fatalf("a successor was refused a seed whose tender's session ended: %v", err)
	}
	if claimed.TenderSession != me || claimed.TenderMember != "trellis" {
		t.Fatalf("the claim did not move to the successor: %+v", claimed)
	}

	// A tender that names only a member always holds: attn has no signal that a
	// person in a terminal pane walked away, so an ended-session rule must not
	// reach them.
	pane := Seed{ID: "s-abc123", Status: StatusGrowing, TenderMember: "trellis"}
	if !pane.Tender().Holds(gone) {
		t.Fatal("a member-only tender was released by a session rule that cannot see them")
	}
	if _, err := Transition(pane, VerbTend, Tender{Member: "alder"}, "", gone); err == nil {
		t.Fatal("a member-only claim was taken because no session was alive")
	}
}

func TestTendRefusalFallsBackToTheSessionID(t *testing.T) {
	held := seedIn(StatusGrowing, Tender{Session: other})
	_, err := Transition(held, VerbTend, Tender{Session: me}, "", alive)
	if err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("a member-less tender did not hold the claim by name: %v", err)
	}
}

func TestTendNeedsSomebodyToRecord(t *testing.T) {
	_, err := Transition(seedIn(StatusPlanted, Tender{}), VerbTend, Tender{}, "", alive)
	if err == nil || !strings.Contains(err.Error(), "--member") {
		t.Fatalf("a tend that names nobody was accepted or refused unhelpfully: %v", err)
	}
}

func TestHarvestNeedsAReason(t *testing.T) {
	_, err := Transition(seedIn(StatusPlanted, Tender{}), VerbHarvest, Tender{Session: me}, "  ", alive)
	if err == nil || !strings.Contains(err.Error(), "-m") {
		t.Fatalf("a wordless harvest was accepted or refused unhelpfully: %v", err)
	}
	// Withering may be wordless: often there is nothing to say beyond "nobody
	// will pick this up".
	withered, err := Transition(seedIn(StatusPlanted, Tender{}), VerbWither, Tender{Session: me}, "", alive)
	if err != nil {
		t.Fatalf("a wordless wither was refused: %v", err)
	}
	if withered.Status != StatusWithered {
		t.Fatalf("wither landed in %q", withered.Status)
	}
}

func TestReasonLimitNamesItselfAndPointsAtTheLog(t *testing.T) {
	_, err := Transition(seedIn(StatusPlanted, Tender{}), VerbHarvest, Tender{Session: me}, strings.Repeat("x", MaxReasonChars+1), alive)
	if err == nil {
		t.Fatal("an oversized reason was accepted")
	}
	for _, want := range []string{"401", "400", "attn seed note"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the limit refusal does not name %q: %s", want, err)
		}
	}
}

func TestParseVerbNamesTheWholeSet(t *testing.T) {
	for _, verb := range Verbs {
		if got, err := ParseVerb(string(verb)); err != nil || got != verb {
			t.Fatalf("ParseVerb(%q) = %q, %v", verb, got, err)
		}
	}
	if _, err := ParseVerb("  HARVEST "); err != nil {
		t.Fatalf("a verb is read case- and space-insensitively: %v", err)
	}
	_, err := ParseVerb("compost")
	if err == nil {
		t.Fatal("an unknown verb was accepted")
	}
	for _, verb := range Verbs {
		if !strings.Contains(err.Error(), string(verb)) {
			t.Fatalf("the refusal does not offer %q: %s", verb, err)
		}
	}
}

func TestValidateNote(t *testing.T) {
	if err := ValidateNote("  \n "); err == nil {
		t.Fatal("an empty note was accepted")
	}
	if err := ValidateNote(strings.Repeat("x", MaxNoteBytes+1)); err == nil {
		t.Fatal("a note past the limit was accepted")
	} else if !strings.Contains(err.Error(), strconv.Itoa(MaxNoteBytes+1)) {
		t.Fatalf("the limit refusal does not name the ask: %v", err)
	}
	// The note travels as one JSON object in a single unix-socket frame, and
	// that frame is 64KiB. A note limit at or above it is answered by the
	// transport instead — the caller reads a frame-size error rather than the
	// note limit and where to put the text.
	if MaxNoteBytes >= 64<<10 {
		t.Fatalf("MaxNoteBytes is %d, at or past the 64KiB socket frame it travels through", MaxNoteBytes)
	}
	if err := ValidateNote("what happened"); err != nil {
		t.Fatalf("a real note was refused: %v", err)
	}
}

func TestParseNoteKindNamesTheWholeSet(t *testing.T) {
	// An unnamed kind is the plain log entry: `attn seed note` wrote one
	// before kinds existed and must keep writing one.
	if got, err := ParseNoteKind(""); err != nil || got != NoteKindNote {
		t.Fatalf("ParseNoteKind(\"\") = %q, %v; want the plain note", got, err)
	}
	for _, kind := range NoteKinds {
		if got, err := ParseNoteKind(kind); err != nil || got != kind {
			t.Fatalf("ParseNoteKind(%q) = %q, %v", kind, got, err)
		}
	}
	if got, err := ParseNoteKind(" HANDOFF "); err != nil || got != NoteKindHandoff {
		t.Fatalf("a kind is read case- and space-insensitively: %q, %v", got, err)
	}
	_, err := ParseNoteKind("farewell")
	if err == nil {
		t.Fatal("an unknown note kind was accepted")
	}
	for _, kind := range NoteKinds {
		if !strings.Contains(err.Error(), kind) {
			t.Fatalf("the refusal does not offer %q: %s", kind, err)
		}
	}
}

func TestNoteIDsAreTheirOwnShape(t *testing.T) {
	id, err := NewNoteID()
	if err != nil {
		t.Fatalf("NewNoteID: %v", err)
	}
	if !strings.HasPrefix(id, "n-") || len(id) != len("n-")+idBodyLen {
		t.Fatalf("note id %q is not n- plus %d characters", id, idBodyLen)
	}
	// A note id must never pass for a seed id: they address different
	// collections and a swap would read one as the other.
	if err := ValidateID(id); err == nil {
		t.Fatalf("note id %q validates as a seed id", id)
	}
}
