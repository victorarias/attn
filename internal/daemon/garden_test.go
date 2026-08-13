package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// gardenCall runs one seed handler over a pipe, the way an `attn seed`
// invocation reaches the daemon.
func gardenCall(t *testing.T, run func(net.Conn)) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		run(server)
		_ = server.Close()
	}()
	defer client.Close()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// newGardenDaemon returns a home daemon with the garden's collections declared
// and one session sitting in a workspace, which is what a planting is stamped
// from.
func newGardenDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	now := string(protocol.TimestampNow())
	// The session's workspace is left off the stored record deliberately: the
	// app puts a session in a workspace through the live registry, and the
	// persisted column only leads while startup rebuilds that registry. A
	// fixture that stamps the column instead hides every reader that never asks
	// the registry.
	d.store.Add(&protocol.Session{
		ID: "sess-a", Label: "a",
		State: "idle", StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.register("ws-1", "a", "/tmp/a", "a0", false, false)
	d.workspaces.associateSession("sess-a", "ws-1", "a")
	return d
}

func plant(t *testing.T, d *Daemon, msg protocol.SeedPlantMessage) protocol.Seed {
	t.Helper()
	msg.Cmd = protocol.CmdSeedPlant
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlant(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plant %q: %v", msg.Title, protocol.Deref(resp.Error))
	}
	return resp.SeedPlantResult.Seed
}

// addGardenSession puts a second real session in the workspace, the way the app
// does — through the registry, not the stored column.
func addGardenSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, State: "idle",
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.associateSession(id, "ws-1", id)
}

// move runs one lifecycle verb and fails the test if it was refused.
func move(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Seed {
	t.Helper()
	resp := transition(t, d, session, seedID, verb, reason, member)
	if !resp.Ok {
		t.Fatalf("%s %s: %v", verb, seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedTransitionResult.Seed
}

func transition(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Response {
	t.Helper()
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: string(verb),
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if reason != "" {
		msg.Reason = protocol.Ptr(reason)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
}

func note(t *testing.T, d *Daemon, session, seedID, body, member string) protocol.SeedNote {
	t.Helper()
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
	if !resp.Ok {
		t.Fatalf("note on %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNoteResult.Note
}

func show(t *testing.T, d *Daemon, seedID string) *protocol.SeedShowResult {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: seedID})
	})
	if !resp.Ok {
		t.Fatalf("show %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedShowResult
}

// The slice's acceptance, end to end: a seed lives its whole life through the
// daemon, and every move is one push to the panel.
func TestGarden_FullLifeIsVisibleAtEveryStep(t *testing.T) {
	d := newGardenDaemon(t)
	var pushed [][]protocol.Seed
	d.gardenBroadcastHook = func(seeds []protocol.Seed, _ int) { pushed = append(pushed, seeds) }

	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "live a life"})

	tended := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	if tended.Status != garden.StatusGrowing || tended.TenderMember != "trellis" || tended.TenderSession != "sess-a" {
		t.Fatalf("tend did not claim the seed: %+v", tended)
	}

	note(t, d, "sess-a", seed.ID, "found the seam in internal/daemon", "trellis")

	harvested := move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "shipped it", "trellis")
	if harvested.Status != garden.StatusHarvested {
		t.Fatalf("harvest landed in %q", harvested.Status)
	}
	if harvested.Reason == nil || *harvested.Reason != "shipped it" {
		t.Fatalf("harvest did not record why: %+v", harvested)
	}
	if harvested.TenderSession != "" || harvested.TenderMember != "" {
		t.Fatalf("a harvested seed is still claimed: %+v", harvested)
	}

	replanted := move(t, d, "sess-a", seed.ID, garden.VerbReplant, "", "trellis")
	if replanted.Status != garden.StatusPlanted || replanted.Reason != nil {
		t.Fatalf("replant did not reopen the seed cleanly: %+v", replanted)
	}

	withered := move(t, d, "sess-a", seed.ID, garden.VerbWither, "nobody is picking this up", "trellis")
	if withered.Status != garden.StatusWithered {
		t.Fatalf("wither landed in %q", withered.Status)
	}

	// One planting, one note and four moves: six facts, six pushes, each
	// carrying the state the panel must render at that moment.
	if len(pushed) != 6 {
		t.Fatalf("the life produced %d garden pushes, want one per change", len(pushed))
	}
	states := make([]string, 0, len(pushed))
	for _, garden := range pushed {
		if len(garden) != 1 {
			t.Fatalf("a push carried %d seeds, want the one that exists", len(garden))
		}
		states = append(states, garden[0].Status)
	}
	want := []string{"planted", "growing", "growing", "harvested", "planted", "withered"}
	if !slices.Equal(states, want) {
		t.Fatalf("the panel saw %v, want %v", states, want)
	}
}

// The showpiece refusal. Two real sessions, one seed: the second is told whose
// it is and what to do instead — and there is no override flag, deliberately.
func TestGarden_ASecondSessionCannotTakeALiveClaim(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "contended"})

	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")

	refused := transition(t, d, "sess-b", seed.ID, garden.VerbTend, "", "alder")
	if refused.Ok {
		t.Fatal("a second session took a claim that was already held")
	}
	message := protocol.Deref(refused.Error)
	for _, want := range []string{seed.ID, "trellis", "attn seed note"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the refusal does not name %q:\n%s", want, message)
		}
	}

	// And the claim did not move: a refused tend must leave the seed exactly
	// where the first session left it.
	still := show(t, d, seed.ID).Seed
	if still.TenderSession != "sess-a" || still.TenderMember != "trellis" {
		t.Fatalf("the refused claim changed the tender: %+v", still)
	}

	// The way through is the first session letting go, not a flag.
	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "trellis")
	taken := move(t, d, "sess-b", seed.ID, garden.VerbTend, "", "alder")
	if taken.TenderSession != "sess-b" {
		t.Fatalf("a parked seed did not hand over: %+v", taken)
	}
}

// Two sessions tending at the same instant is the claim's real test: the write
// is conditional on the revision that was read, so the loser re-reads and finds
// a tender rather than overwriting one.
func TestGarden_ConcurrentClaimsProduceOneTender(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "raced for"})

	type outcome struct {
		resp    protocol.Response
		session string
	}
	results := make(chan outcome, 2)
	var start sync.WaitGroup
	start.Add(1)
	for _, session := range []string{"sess-a", "sess-b"} {
		go func() {
			start.Wait()
			results <- outcome{transition(t, d, session, seed.ID, garden.VerbTend, "", session), session}
		}()
	}
	start.Done()

	var winners []string
	var refusals []string
	for range 2 {
		got := <-results
		if got.resp.Ok {
			winners = append(winners, got.resp.SeedTransitionResult.Seed.TenderSession)
			continue
		}
		refusals = append(refusals, protocol.Deref(got.resp.Error))
	}
	if len(winners) != 1 || len(refusals) != 1 {
		t.Fatalf("two simultaneous claims produced %d winners and %d refusals, want one of each", len(winners), len(refusals))
	}
	if !strings.Contains(refusals[0], winners[0]) {
		t.Fatalf("the loser was not told who won:\n%s", refusals[0])
	}
	if held := show(t, d, seed.ID).Seed.TenderSession; held != winners[0] {
		t.Fatalf("the stored tender is %q but %q was told it won", held, winners[0])
	}
}

// Notes are the trail. They read newest first, they say what they withheld, and
// show carries them because a trail behind a verb nobody runs is not read.
func TestGarden_TrailReadsNewestFirstAndSaysWhatItWithheld(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "with a trail"})

	bodies := []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh"}
	for _, body := range bodies {
		note(t, d, "sess-a", seed.ID, body, "trellis")
	}

	shown := show(t, d, seed.ID)
	if shown.NotesTotal != len(bodies) {
		t.Fatalf("show reports %d notes, want %d", shown.NotesTotal, len(bodies))
	}
	if len(shown.Notes) != garden.ShowNotes {
		t.Fatalf("show rendered %d notes inline, want %d", len(shown.Notes), garden.ShowNotes)
	}
	if shown.Notes[0].Body != "seventh" {
		t.Fatalf("the trail leads with %q, want the newest note", shown.Notes[0].Body)
	}
	if shown.Notes[0].AuthorMember != "trellis" || shown.Notes[0].AuthorSession != "sess-a" {
		t.Fatalf("a note does not record who wrote it: %+v", shown.Notes[0])
	}

	all := gardenCall(t, func(c net.Conn) {
		d.handleSeedNotes(c, &protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: seed.ID})
	})
	if !all.Ok {
		t.Fatalf("notes: %v", protocol.Deref(all.Error))
	}
	if len(all.SeedNotesResult.Notes) != len(bodies) || all.SeedNotesResult.Total != len(bodies) {
		t.Fatalf("the full trail is %d of %d, want all %d", len(all.SeedNotesResult.Notes), all.SeedNotesResult.Total, len(bodies))
	}

	// A trail belongs to its seed and to no other.
	elsewhere := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "no trail"})
	if got := show(t, d, elsewhere.ID); len(got.Notes) != 0 || got.NotesTotal != 0 {
		t.Fatalf("another seed's trail leaked: %+v", got.Notes)
	}
}

func TestGarden_LifecycleRefusalsNameWhatIsWrong(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "refusals"})

	unknown := transition(t, d, "sess-a", seed.ID, "compost", "", "trellis")
	if unknown.Ok || !strings.Contains(protocol.Deref(unknown.Error), "harvest") {
		t.Fatalf("an unknown verb was not answered with the ones that exist: %+v", unknown)
	}

	wordless := transition(t, d, "sess-a", seed.ID, garden.VerbHarvest, "", "trellis")
	if wordless.Ok || !strings.Contains(protocol.Deref(wordless.Error), "-m") {
		t.Fatalf("a wordless harvest was not refused with the flag to fix it: %+v", wordless)
	}

	missing := transition(t, d, "sess-a", "s-zzzzzz", garden.VerbTend, "", "trellis")
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("a move on an unplanted seed was not refused by name: %+v", missing)
	}

	emptyNote := gardenCall(t, func(c net.Conn) {
		d.handleSeedNote(c, &protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seed.ID, Body: "  "})
	})
	if emptyNote.Ok || !strings.Contains(protocol.Deref(emptyNote.Error), "attn seed note") {
		t.Fatalf("an empty note was not refused with the command to fix it: %+v", emptyNote)
	}

	noSeed := gardenCall(t, func(c net.Conn) {
		d.handleSeedNote(c, &protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: "s-zzzzzz", Body: "into the void"})
	})
	if noSeed.Ok || !strings.Contains(protocol.Deref(noSeed.Error), "s-zzzzzz") {
		t.Fatalf("a note on an unplanted seed was written or refused vaguely: %+v", noSeed)
	}
}

// Every move publishes its own fact, named for what happened. A single
// `garden.changed` would make a sync engine or a nudge diff documents to find
// out what a name already says.
func TestGarden_EveryMovePublishesItsOwnFact(t *testing.T) {
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

	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	note(t, d, "sess-a", seed.ID, "on the trail", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "done", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbReplant, "", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbWither, "", "trellis")

	want := []string{
		FactGardenTended, FactGardenNoted, FactGardenParked,
		FactGardenHarvested, FactGardenReplanted, FactGardenWithered,
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("the bus saw %v, want %v", seen, want)
	}
}

func TestGarden_PlantListShowRoundTrip(t *testing.T) {
	d := newGardenDaemon(t)

	planted := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"),
		Title:           "Plant and see",
		Body:            protocol.Ptr("# slice 1\n\nthe first vertical"),
		Member:          protocol.Ptr("trellis"),
	})
	if err := garden.ValidateID(planted.ID); err != nil {
		t.Fatalf("plant returned an id that is not a seed id: %v", err)
	}
	if planted.Status != garden.StatusPlanted {
		t.Fatalf("a fresh seed is %q, want %q", planted.Status, garden.StatusPlanted)
	}
	if planted.StepSlug != "plant-and-see" {
		t.Fatalf("step slug = %q, want plant-and-see", planted.StepSlug)
	}
	// The whole point of the one-line plant: the daemon knows who is asking.
	if planted.WorkspaceID != "ws-1" {
		t.Fatalf("workspace = %q, want it stamped from the calling session", planted.WorkspaceID)
	}
	if planted.PlanterSession != "sess-a" || planted.PlanterMember != "trellis" {
		t.Fatalf("planter not recorded: %+v", planted)
	}

	listResp := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, SourceSessionID: protocol.Ptr("sess-a")})
	})
	if !listResp.Ok {
		t.Fatalf("ls: %v", protocol.Deref(listResp.Error))
	}
	if got := listResp.SeedListResult; len(got.Seeds) != 1 || got.Seeds[0].ID != planted.ID {
		t.Fatalf("flag-free ls did not return the seed just planted: %+v", got)
	}
	if listResp.SeedListResult.WorkspaceID != "ws-1" {
		t.Fatalf("ls scoped to %q, want the session's workspace", listResp.SeedListResult.WorkspaceID)
	}

	showResp := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: planted.ID})
	})
	if !showResp.Ok {
		t.Fatalf("show: %v", protocol.Deref(showResp.Error))
	}
	shown := showResp.SeedShowResult.Seed
	if shown.Title != "Plant and see" || shown.Body != "# slice 1\n\nthe first vertical" {
		t.Fatalf("show lost the seed: %+v", shown)
	}
	if shown.Rev < 1 || shown.CreatedAt == "" {
		t.Fatalf("show did not carry the document's own revision and stamp: %+v", shown)
	}
	// Inert-but-present: these fields ship in slice 1 so later slices add
	// behavior rather than schema.
	if shown.Edges == nil || shown.Vars == nil || shown.Template || shown.Gate {
		t.Fatalf("the designed schema is not whole on a fresh seed: %+v", shown)
	}
}

func TestGarden_ListScopesAndAllSeesTheWholeGarden(t *testing.T) {
	d := newGardenDaemon(t)
	mine := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "in my workspace"})
	elsewhere := plant(t, d, protocol.SeedPlantMessage{Title: "planted outside any workspace"})
	if elsewhere.WorkspaceID != "" {
		t.Fatalf("a seed planted with no session carries workspace %q, want none", elsewhere.WorkspaceID)
	}

	scoped := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, SourceSessionID: protocol.Ptr("sess-a")})
	}).SeedListResult
	if len(scoped.Seeds) != 1 || scoped.Seeds[0].ID != mine.ID {
		t.Fatalf("workspace scope leaked or dropped seeds: %+v", scoped.Seeds)
	}

	all := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, All: protocol.Ptr(true)})
	}).SeedListResult
	if len(all.Seeds) != 2 || !all.All {
		t.Fatalf("--all did not read the whole garden: %+v", all)
	}
	// Newest first, so a fresh planting is the first thing anybody reads.
	if all.Seeds[0].ID != elsewhere.ID {
		t.Fatalf("--all order = %q first, want the newest seed %q", all.Seeds[0].ID, elsewhere.ID)
	}
}

// A list carries the count of what its own scope holds. Counting the whole
// garden against a workspace-scoped list would report a shortfall that is not
// there — "showing 1 of 2" for a workspace that holds exactly one seed.
func TestGarden_ListCountsItsOwnScope(t *testing.T) {
	d := newGardenDaemon(t)
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "in my workspace"})
	plant(t, d, protocol.SeedPlantMessage{Title: "planted outside any workspace"})

	scoped := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, SourceSessionID: protocol.Ptr("sess-a")})
	}).SeedListResult
	if scoped.Total != 1 {
		t.Fatalf("scoped total = %d, want 1: the workspace holds one seed", scoped.Total)
	}

	all := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, All: protocol.Ptr(true)})
	}).SeedListResult
	if all.Total != 2 {
		t.Fatalf("--all total = %d, want 2", all.Total)
	}
}

// The push is bounded and the total is what keeps that honest, so the number on
// the wire has to be the garden's and not the truncated list's length.
func TestGarden_PushCarriesTheWholeGardensCount(t *testing.T) {
	d := newGardenDaemon(t)
	var totals []int
	d.gardenBroadcastHook = func(_ []protocol.Seed, total int) { totals = append(totals, total) }

	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "one"})
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "two"})

	if len(totals) == 0 {
		t.Fatal("planting pushed no garden")
	}
	if got := totals[len(totals)-1]; got != 2 {
		t.Fatalf("push total = %d, want 2", got)
	}
}

func TestGarden_ListWithNothingToScopeToRefusesLoudly(t *testing.T) {
	d := newGardenDaemon(t)
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList})
	})
	if resp.Ok {
		t.Fatal("ls with no session and no scope answered instead of refusing")
	}
	// Handing back "no seeds" would read as an empty garden. The refusal has to
	// name both ways to ask.
	for _, want := range []string{"--all", "--workspace"} {
		if !strings.Contains(protocol.Deref(resp.Error), want) {
			t.Fatalf("refusal does not name %q: %s", want, protocol.Deref(resp.Error))
		}
	}
}

func TestGarden_RefusalsNameWhatIsWrong(t *testing.T) {
	d := newGardenDaemon(t)

	empty := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: "   "})
	})
	if empty.Ok || !strings.Contains(protocol.Deref(empty.Error), "attn seed plant") {
		t.Fatalf("an empty title was not refused with the command to fix it: %+v", empty)
	}

	malformed := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: "nope"})
	})
	if malformed.Ok || !strings.Contains(protocol.Deref(malformed.Error), "seed id") {
		t.Fatalf("a malformed id was not refused by shape: %+v", malformed)
	}

	missing := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: "s-zzzzzz"})
	})
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("an unknown seed was not refused by name: %+v", missing)
	}
}

func TestGarden_OutpostRefusesEverySeedCommand(t *testing.T) {
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()

	calls := map[string]func(net.Conn){
		"plant": func(c net.Conn) {
			d.handleSeedPlant(c, &protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: "anything"})
		},
		"ls": func(c net.Conn) {
			d.handleSeedList(c, &protocol.SeedListMessage{Cmd: protocol.CmdSeedList, All: protocol.Ptr(true)})
		},
		"show": func(c net.Conn) {
			d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: "s-7k3f9m"})
		},
		"tend": func(c net.Conn) {
			d.handleSeedTransition(c, &protocol.SeedTransitionMessage{
				Cmd: protocol.CmdSeedTransition, SeedID: "s-7k3f9m", Verb: string(garden.VerbTend),
			})
		},
		"note": func(c net.Conn) {
			d.handleSeedNote(c, &protocol.SeedNoteMessage{
				Cmd: protocol.CmdSeedNote, SeedID: "s-7k3f9m", Body: "anything",
			})
		},
		"notes": func(c net.Conn) {
			d.handleSeedNotes(c, &protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: "s-7k3f9m"})
		},
	}
	for verb, call := range calls {
		resp := gardenCall(t, call)
		if resp.Ok {
			t.Fatalf("seed %s answered on an outpost, want the fence", verb)
		}
		message := protocol.Deref(resp.Error)
		// The fence, not a bespoke check: the refusal names the surface, this
		// daemon, its home, the way out, and the plan.
		for _, want := range []string{garden.Surface, home, "attn enrollment leave", enrollment.PlanPath} {
			if !strings.Contains(message, want) {
				t.Fatalf("seed %s refusal does not name %q:\n%s", verb, want, message)
			}
		}
	}

	// And nothing about the garden reaches a client of an outpost either.
	if seeds := initialStateEvent(t, d).Seeds; len(seeds) != 0 {
		t.Fatalf("an outpost's initial_state carries %d seeds, want none", len(seeds))
	}
}

func TestGarden_PlantingPushesTheGardenOnce(t *testing.T) {
	d := newGardenDaemon(t)

	var pushes int
	var last []protocol.Seed
	d.gardenBroadcastHook = func(seeds []protocol.Seed, _ int) {
		pushes++
		last = seeds
	}

	planted := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "see it appear"})

	// One planting is one fact is one garden push — the panel's whole contract
	// is that a seed appears without anyone asking for it.
	if pushes != 1 {
		t.Fatalf("one planting produced %d garden pushes, want exactly 1", pushes)
	}
	if len(last) != 1 || last[0].ID != planted.ID {
		t.Fatalf("the pushed garden does not carry the new seed: %+v", last)
	}
}

func TestGarden_BulkPlantingCoalescesToOnePush(t *testing.T) {
	d := newGardenDaemon(t)
	var pushes int
	d.gardenBroadcastHook = func([]protocol.Seed, int) { pushes++ }

	// What planting a plot looks like from the outside: several seeds, one
	// wire message.
	d.coalesceSnapshots(func() {
		for _, title := range []string{"a", "b", "c"} {
			plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: title})
		}
	})
	if pushes != 1 {
		t.Fatalf("three plantings inside one coalesce produced %d pushes, want 1", pushes)
	}
}

func TestGarden_SeedsReachInitialState(t *testing.T) {
	d := newGardenDaemon(t)
	planted := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "on connect"})

	seeds := initialStateEvent(t, d).Seeds
	if len(seeds) != 1 || seeds[0].ID != planted.ID {
		t.Fatalf("initial_state does not carry the garden: %+v", seeds)
	}
}

// A seed id is the document id, so a collision would be a lost seed rather than
// a refused write. Planting is create-only; this pins that.
func TestGarden_PlantingIsCreateOnly(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	seed := garden.Seed{ID: "s-7k3f9m", Title: "first", Status: garden.StatusPlanted}
	if _, err := d.plantSeed(*schema, seed); err != nil {
		t.Fatalf("first planting: %v", err)
	}
	seed.Title = "second"
	if _, err := d.plantSeed(*schema, seed); err == nil {
		t.Fatal("planting over an existing seed was allowed")
	}
}

// A minted id can land on one already planted. The planter did nothing wrong and
// has nothing to fix, so the daemon mints again rather than answering a refusal
// about a coin flip; only a mint source that keeps repeating itself is reported.
func TestGarden_PlantingMintsAgainWhenAnIDIsTaken(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	if _, err := d.plantSeed(*schema, garden.Seed{ID: "s-7k3f9m", Title: "already here", Status: garden.StatusPlanted}); err != nil {
		t.Fatalf("seeding the collision: %v", err)
	}
	minted := []string{"s-7k3f9m", "s-7k3f9m", "s-fresh1"}
	d.gardenMintID = func() (string, error) {
		next := minted[0]
		minted = minted[1:]
		return next, nil
	}

	planted := plant(t, d, protocol.SeedPlantMessage{Title: "planted anyway"})
	if planted.ID != "s-fresh1" {
		t.Fatalf("seed id = %q, want the third mint after two taken ones", planted.ID)
	}
	if len(minted) != 0 {
		t.Fatalf("%d mints unused: the retry stopped early", len(minted))
	}

	// A mint that only ever repeats itself is a broken source, and the refusal
	// says which rather than blaming the title.
	d.gardenMintID = func() (string, error) { return "s-7k3f9m", nil }
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: "no id left"})
	})
	if resp.Ok {
		t.Fatal("a mint source that never moves was allowed to plant")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "random source") {
		t.Fatalf("refusal = %q, want it to name the mint source", msg)
	}
}

func TestGarden_CollectionsAreDeclaredOnStartup(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	// Declaring twice is what a restart does.
	d.ensureGardenCollections()

	for _, collection := range []string{garden.CollectionSeeds, garden.CollectionNotes} {
		if _, err := d.collectionFor(garden.Namespace, collection); err != nil {
			t.Fatalf("%s/%s is not declared after startup: %v", garden.Namespace, collection, err)
		}
	}
}
