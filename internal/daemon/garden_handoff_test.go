package daemon

import (
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// handoff writes a note addressed to whoever tends the seed next.
func handoff(t *testing.T, d *Daemon, session, seedID, body, member string) protocol.SeedNote {
	t.Helper()
	msg := protocol.SeedNoteMessage{
		Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body, Kind: protocol.Ptr(garden.NoteKindHandoff),
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff on %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNoteResult.Note
}

// The slice's acceptance, with two real sessions: session A tends a seed, files
// a handoff and ends; session B picks the seed up and the handoff is in the
// answer to its claim, before it has done anything.
func TestGarden_AHandoffReachesTheNextTender(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "carry this across sessions",
	})

	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	note(t, d, "sess-a", seed.ID, "read the docstore compiler first", "trellis")
	left := handoff(t, d, "sess-a", seed.ID,
		"the join test is the gate; run it before touching wireProjections", "trellis")
	if left.Kind != garden.NoteKindHandoff {
		t.Fatalf("the note was stored as %q, want a handoff", left.Kind)
	}

	// Session A ends. The seed keeps A's name on it, and nothing settles it.
	d.store.Remove("sess-a")

	resp := transition(t, d, "sess-b", seed.ID, garden.VerbTend, "", "alder")
	if !resp.Ok {
		t.Fatalf("the successor could not tend the seed: %v", protocol.Deref(resp.Error))
	}
	got := resp.SeedTransitionResult.Handoff
	if got == nil {
		t.Fatal("tend handed back no handoff; the successor starts blind")
	}
	if got.ID != left.ID || got.Body != left.Body {
		t.Fatalf("tend carried %+v, want the handoff A left", got)
	}
	if got.AuthorMember != "trellis" {
		t.Fatalf("the handoff does not name who wrote it: %+v", got)
	}

	// And the same note is what `show` puts in front of a reader.
	shown := show(t, d, seed.ID)
	if shown.Handoff == nil || shown.Handoff.ID != left.ID {
		t.Fatalf("show surfaced %+v, want the handoff", shown.Handoff)
	}
}

// A handoff outlives the trail window. It is the case a scan of `show`'s newest
// few notes would miss, and the one that matters most: a busy seed is exactly
// where a successor needs the note that was written to them.
func TestGarden_TheFreshestHandoffSurvivesABusyTrail(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "busy"})

	handoff(t, d, "sess-a", seed.ID, "the first word", "keel")
	newest := handoff(t, d, "sess-a", seed.ID, "the last word", "keel")
	for range garden.ShowNotes + 2 {
		note(t, d, "sess-a", seed.ID, "ordinary progress", "keel")
	}

	shown := show(t, d, seed.ID)
	if shown.Handoff == nil {
		t.Fatal("the handoff fell out of the trail window and was lost")
	}
	if shown.Handoff.ID != newest.ID {
		t.Fatalf("show surfaced %q, want the freshest handoff %q", shown.Handoff.ID, newest.ID)
	}
	// The window itself is unchanged: `show` still renders the newest few notes
	// and still counts the whole trail.
	if len(shown.Notes) != garden.ShowNotes {
		t.Fatalf("show rendered %d notes, want the usual %d", len(shown.Notes), garden.ShowNotes)
	}
	if want := garden.ShowNotes + 4; shown.NotesTotal != want {
		t.Fatalf("the trail counts %d, want %d — handoffs are notes and are counted as such", shown.NotesTotal, want)
	}
}

// Tending is the pickup, so it is the only move whose answer primes. The other
// four settle a seed; nobody is picking it up and a handoff there is noise.
func TestGarden_OnlyTendCarriesTheHandoff(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "settled"})
	handoff(t, d, "sess-a", seed.ID, "what I learned", "keel")

	tended := transition(t, d, "sess-a", seed.ID, garden.VerbTend, "", "keel")
	if tended.SeedTransitionResult.Handoff == nil {
		t.Fatal("tend carried no handoff")
	}
	for _, tc := range []struct {
		verb   garden.Verb
		reason string
	}{{garden.VerbPark, ""}, {garden.VerbHarvest, "done"}, {garden.VerbReplant, ""}} {
		resp := transition(t, d, "sess-a", seed.ID, tc.verb, tc.reason, "keel")
		if !resp.Ok {
			t.Fatalf("%s: %v", tc.verb, protocol.Deref(resp.Error))
		}
		if resp.SeedTransitionResult.Handoff != nil {
			t.Fatalf("%s carried a handoff; only the pickup primes", tc.verb)
		}
	}
}

// A seed nobody handed over says nothing about handoffs, and a note written the
// way `attn seed note` always wrote one is still a plain trail entry.
func TestGarden_NoHandoffIsNoHandoff(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "quiet"})
	plain := note(t, d, "sess-a", seed.ID, "what happened", "keel")
	if plain.Kind != garden.NoteKindNote {
		t.Fatalf("an unkinded note was stored as %q", plain.Kind)
	}

	if shown := show(t, d, seed.ID); shown.Handoff != nil {
		t.Fatalf("show invented a handoff: %+v", shown.Handoff)
	}
	tended := transition(t, d, "sess-a", seed.ID, garden.VerbTend, "", "keel")
	if tended.SeedTransitionResult.Handoff != nil {
		t.Fatal("tend invented a handoff")
	}
}

func TestGarden_AnUnknownNoteKindIsRefusedByName(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "kinds"})

	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedNote(c, &protocol.SeedNoteMessage{
			Cmd: protocol.CmdSeedNote, SeedID: seed.ID, Body: "words", Kind: protocol.Ptr("farewell"),
		})
	})
	if resp.Ok {
		t.Fatal("a note kind nothing knows about was written")
	}
	message := protocol.Deref(resp.Error)
	for _, want := range garden.NoteKinds {
		if !strings.Contains(message, want) {
			t.Fatalf("the refusal does not offer %q:\n%s", want, message)
		}
	}
}

// A handoff is a note: it rides `garden.noted` like every other trail entry, so
// the panel re-push a note already produced covers it and no new fact exists to
// leave unprojected.
func TestGarden_AHandoffPublishesTheNoteFact(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "facts"})

	var seen []string
	unsubscribe := d.eventBus.Subscribe(bus.Filter{"garden.*"}, func(ev bus.Event) {
		if ev.Subject != seed.ID {
			t.Errorf("fact %s names subject %q, want the seed", ev.Name, ev.Subject)
		}
		seen = append(seen, ev.Name)
	})
	defer unsubscribe()

	handoff(t, d, "sess-a", seed.ID, "over to you", "keel")

	if !slices.Equal(seen, []string{FactGardenNoted}) {
		t.Fatalf("a handoff published %v, want just %s", seen, FactGardenNoted)
	}
}
