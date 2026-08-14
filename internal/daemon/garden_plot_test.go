package daemon

import (
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/ptybackend"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func plot(t *testing.T, d *Daemon, msg protocol.SeedPlotMessage) protocol.SeedPlotResult {
	t.Helper()
	msg.Cmd = protocol.CmdSeedPlot
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlot(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plot: %v", protocol.Deref(resp.Error))
	}
	return *resp.SeedPlotResult
}

func list(t *testing.T, d *Daemon, msg protocol.SeedListMessage) protocol.SeedListResult {
	t.Helper()
	msg.Cmd = protocol.CmdSeedList
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedList(c, &msg) })
	if !resp.Ok {
		t.Fatalf("ls: %v", protocol.Deref(resp.Error))
	}
	return *resp.SeedListResult
}

// A plot is one command: the crown, its children, and the sequencing between
// them, all landing together so an agent captures a chunk of work in one move.
func TestGardenPlot_PlantsACrownWithItsChildren(t *testing.T) {
	d := newGardenDaemon(t)
	result := plot(t, d, protocol.SeedPlotMessage{
		SourceSessionID: protocol.Ptr("sess-a"),
		Member:          protocol.Ptr("trellis"),
		Title:           "ship the thing",
		Body:            protocol.Ptr("# the plan"),
		Children: []protocol.SeedPlotChild{
			{Title: "first step"},
			{Title: "second step"},
			{Title: "third step", Blocks: []string{"second-step"}},
		},
	})

	if len(result.Children) != 3 {
		t.Fatalf("plot planted %d children, want 3", len(result.Children))
	}
	if result.Crown.Title != "ship the thing" || result.Crown.Body != "# the plan" {
		t.Fatalf("crown = %+v", result.Crown)
	}
	byID := map[string]protocol.Seed{}
	for _, child := range result.Children {
		byID[child.StepSlug] = child
		if child.PlanterMember != "trellis" {
			t.Fatalf("child %s lost the planter: %+v", child.ID, child)
		}
	}

	// Every child is part-of the crown, and only the one that said so carries a
	// blocks edge: children are parallel by default.
	for slug, child := range byID {
		parents, blocks := 0, 0
		for _, edge := range child.Edges {
			switch edge.Kind {
			case garden.EdgePartOf:
				if edge.To != result.Crown.ID {
					t.Fatalf("child %s is part-of %s, want the crown", slug, edge.To)
				}
				parents++
			case garden.EdgeBlocks:
				blocks++
			}
		}
		want := 0
		if slug == "third-step" {
			want = 1
		}
		if parents != 1 || blocks != want {
			t.Fatalf("child %s has %d part-of and %d blocks edges, want 1 and %d", slug, parents, blocks, want)
		}
	}
	// The blocks edge names a real sibling id, not a slug: the plot is minted
	// before it is written precisely so the edges can point at something.
	if got := byID["third-step"].Edges; got[1].To != byID["second-step"].ID {
		t.Fatalf("third-step blocks %q, want the sibling %s", got[1].To, byID["second-step"].ID)
	}

	// Ready reads the sequencing straight away: two of three are pickable, the
	// blocked one waits, and the crown is never its own work.
	got := readyIDs(ready(t, d, protocol.SeedReadyMessage{Plot: protocol.Ptr(result.Crown.ID)}))
	if len(got) != 2 {
		t.Fatalf("ready in the fresh plot = %v, want the two unblocked children", got)
	}
}

// The whole plot is validated before anything is written: a payload that names
// a sibling nobody has must leave the garden exactly as it found it.
func TestGardenPlot_RefusesBeforeWritingAnything(t *testing.T) {
	d := newGardenDaemon(t)
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlot(c, &protocol.SeedPlotMessage{
			Cmd: protocol.CmdSeedPlot, Title: "ship it",
			Children: []protocol.SeedPlotChild{{Title: "a", Blocks: []string{"nobody"}}},
		})
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "nobody") {
		t.Fatalf("a plot with a dangling blocks was not refused by name: %+v", resp)
	}
	if got := list(t, d, protocol.SeedListMessage{}); got.Total != 0 {
		t.Fatalf("a refused plot left %d seeds behind", got.Total)
	}
}

// One push for a whole plot, not one per seed: a panel that repaints once per
// child would flicker through a plot's planting.
func TestGardenPlot_PushesTheGardenOnce(t *testing.T) {
	d := newGardenDaemon(t)
	var pushes int
	d.gardenBroadcastHook = func([]protocol.Seed, int) { pushes++ }

	plot(t, d, protocol.SeedPlotMessage{
		Title:    "ship it",
		Children: []protocol.SeedPlotChild{{Title: "a"}, {Title: "b"}, {Title: "c"}},
	})
	if pushes != 1 {
		t.Fatalf("planting one plot pushed the garden %d times, want 1", pushes)
	}
}

// Planting under a crown is the one-seed form of the same move, and it refuses
// an edge to a seed that is not here rather than planting an orphan.
func TestGardenPlot_PlantUnderACrown(t *testing.T) {
	d := newGardenDaemon(t)
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})
	child := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "inside", PartOf: protocol.Ptr(crown.ID),
	})
	if len(child.Edges) != 1 || child.Edges[0].Kind != garden.EdgePartOf || child.Edges[0].To != crown.ID {
		t.Fatalf("--part-of did not plant into the plot: %+v", child.Edges)
	}

	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{
			Cmd: protocol.CmdSeedPlant, Title: "orphan", PartOf: protocol.Ptr("s-zzzzzz"),
		})
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "s-zzzzzz") {
		t.Fatalf("planting under a crown that is not here was not refused by name: %+v", resp)
	}
}

// A crown wears its plot everywhere a seed is rendered — the listing that a
// panel row is built from, and the seed's own page.
func TestGardenPlot_ProgressRidesWithTheCrown(t *testing.T) {
	d := newGardenDaemon(t)
	result := plot(t, d, protocol.SeedPlotMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "ship it",
		Children: []protocol.SeedPlotChild{{Title: "a"}, {Title: "b", Blocks: []string{"a"}}},
	})
	move(t, d, "sess-a", result.Children[0].ID, garden.VerbTend, "", "trellis")
	move(t, d, "sess-a", result.Children[0].ID, garden.VerbHarvest, "done", "trellis")

	want := protocol.SeedPlotProgress{Total: 2, Done: 1, Ready: 1}
	if got := show(t, d, result.Crown.ID).Seed.PlotProgress; got == nil || *got != want {
		t.Fatalf("show progress = %+v, want %+v", got, want)
	}
	for _, seed := range list(t, d, protocol.SeedListMessage{}).Seeds {
		switch seed.ID {
		case result.Crown.ID:
			if seed.PlotProgress == nil || *seed.PlotProgress != want {
				t.Fatalf("listed crown progress = %+v, want %+v", seed.PlotProgress, want)
			}
		default:
			// A childless seed has no plot, and an empty progress block on every
			// row would render "0 of 0 done" beside ordinary work.
			if seed.PlotProgress != nil {
				t.Fatalf("seed %s carries a plot it does not have: %+v", seed.ID, seed.PlotProgress)
			}
		}
	}
}

// The stale query names open seeds nothing has touched inside the window, and
// says which window it applied — the rule is half the answer.
func TestGardenPlot_StaleNamesTheQuietOpenSeeds(t *testing.T) {
	d := newGardenDaemon(t)
	fresh := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "just planted"})
	quiet := plantAt(t, d, "quiet for a month", time.Now().Add(-30*24*time.Hour))
	closed := plantAt(t, d, "quiet but harvested", time.Now().Add(-30*24*time.Hour))
	move(t, d, "sess-a", closed, garden.VerbTend, "", "trellis")
	move(t, d, "sess-a", closed, garden.VerbHarvest, "done", "trellis")

	all := list(t, d, protocol.SeedListMessage{})
	if all.Total != 3 || all.StaleWindowSeconds != nil {
		t.Fatalf("a plain listing answered as a stale query: %+v", all)
	}

	stale := list(t, d, protocol.SeedListMessage{Stale: protocol.Ptr(true)})
	if len(stale.Seeds) != 1 || stale.Seeds[0].ID != quiet {
		t.Fatalf("stale = %+v, want only the quiet open seed %s", stale.Seeds, quiet)
	}
	if stale.Total != 1 {
		t.Fatalf("stale total = %d, want the count of what it answered", stale.Total)
	}
	if stale.StaleWindowSeconds == nil || *stale.StaleWindowSeconds != int(garden.DefaultStaleWindow/time.Second) {
		t.Fatalf("stale did not name the window it applied: %+v", stale.StaleWindowSeconds)
	}
	if stale.Seeds[0].ID == fresh.ID {
		t.Fatal("a seed planted a moment ago was called stale")
	}

	// The window is the caller's to move, and the answer says what it used.
	wide := list(t, d, protocol.SeedListMessage{
		Stale: protocol.Ptr(true), StaleWindowSeconds: protocol.Ptr(int((100 * 24 * time.Hour) / time.Second)),
	})
	if len(wide.Seeds) != 0 || wide.StaleWindowSeconds == nil || *wide.StaleWindowSeconds != 8640000 {
		t.Fatalf("a wider window = %+v (window %v), want nothing stale", wide.Seeds, wide.StaleWindowSeconds)
	}
}

// A note is trail movement even when the seed document itself never changed:
// somebody writing down what they learned is exactly the seed not being
// neglected.
func TestGardenPlot_StaleReadsTheTrailNotJustTheDocument(t *testing.T) {
	d := newGardenDaemon(t)
	quiet := plantAt(t, d, "old document, live trail", time.Now().Add(-30*24*time.Hour))
	if got := list(t, d, protocol.SeedListMessage{Stale: protocol.Ptr(true)}); len(got.Seeds) != 1 {
		t.Fatalf("stale = %+v, want the quiet seed before its trail moves", got.Seeds)
	}

	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedNote(c, &protocol.SeedNoteMessage{
			Cmd: protocol.CmdSeedNote, SeedID: quiet, Body: "still on this", SourceSessionID: protocol.Ptr("sess-a"),
		})
	})
	if !resp.Ok {
		t.Fatalf("note: %v", protocol.Deref(resp.Error))
	}
	if got := list(t, d, protocol.SeedListMessage{Stale: protocol.Ptr(true)}); len(got.Seeds) != 0 {
		t.Fatalf("stale = %+v, want nothing: the trail moved just now", got.Seeds)
	}
}

// plantAt writes a seed stamped in the past, which is the only way to witness a
// window that a test may not wait out. It goes through the same document write
// the daemon uses, so the stamp is the one the query reads.
func plantAt(t *testing.T, d *Daemon, title string, at time.Time) string {
	t.Helper()
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	id, err := d.mintUnplantedSeedID(*schema)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	body, err := garden.Seed{
		ID: id, Title: title, Status: garden.StatusPlanted, StepSlug: garden.StepSlug(title),
		Edges: []garden.Edge{}, Vars: []garden.Var{},
	}.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	expected := docstore.ExpectAbsent
	fact := documentChangedFact(garden.Namespace, garden.CollectionSeeds, id, false)
	if _, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: id, Body: body, Expected: &expected,
	}, fact, at); err != nil {
		t.Fatalf("write a seed stamped in the past: %v", err)
	}
	return id
}

// Dispatch-at-plot rides the delegation: the record is written before the
// runtime spawns, because the launch primer reads it — a delegate must launch
// already knowing its plot rather than discovering it on its second command.
func TestGardenPlot_DelegationDispatchesAtACrown(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	planted := plot(t, d, protocol.SeedPlotMessage{
		Title:    "ship it",
		Children: []protocol.SeedPlotChild{{Title: "a"}, {Title: "b", Blocks: []string{"a"}}},
	})
	outside := plant(t, d, protocol.SeedPlantMessage{Title: "somewhere else"})

	var primed *hooks.GardenPrime
	backend.onSpawn = func(ptybackend.SpawnOptions) {
		// Read where the launch guidance reads it: after the dispatch is
		// recorded and while the session is spawning.
		primed = d.gardenPrimeForLaunch(delegatedSessionID(t, d, sourceSessionID))
	}

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "tend this plot", Plot: protocol.Ptr(planted.Crown.ID),
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if crown, ok := d.gardenDispatchCrown(result.SessionID); !ok || crown != planted.Crown.ID {
		t.Fatalf("dispatch record = %q (%v), want %s", crown, ok, planted.Crown.ID)
	}
	if primed == nil || primed.Crown == nil {
		t.Fatalf("the delegate launched without its plot: %+v", primed)
	}
	if primed.Crown.ID != planted.Crown.ID || len(primed.Crown.ReadySeeds) != 1 {
		t.Fatalf("primed with %+v, want the crown and its one ready child", primed.Crown)
	}

	// Scope, not a fence: the delegate's flag-free ready is the plot, and the
	// seed outside it is still there to be tended.
	scoped := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr(result.SessionID)})
	if scoped.Scope != "plot" || len(scoped.Seeds) != 1 {
		t.Fatalf("the delegate's flag-free ready = %+v, want its plot", scoped)
	}
	all := ready(t, d, protocol.SeedReadyMessage{
		SourceSessionID: protocol.Ptr(result.SessionID), All: protocol.Ptr(true),
	})
	if !slices.Contains(readyIDs(all), outside.ID) {
		t.Fatalf("--all from the delegate = %v, want the seed outside the plot too", readyIDs(all))
	}
}

// A delegation aimed at a seed that is not here refuses before any worktree or
// runtime side effect: launching unaimed is worse than not launching.
func TestGardenPlot_DelegationRefusesACrownThatIsNotHere(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)

	spawned := false
	backend.onSpawn = func(ptybackend.SpawnOptions) { spawned = true }

	_, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "tend nothing", Plot: protocol.Ptr("s-zzzzzz"),
	})
	if err == nil || !strings.Contains(err.Error(), "s-zzzzzz") {
		t.Fatalf("delegate error = %v, want a refusal naming the crown", err)
	}
	if spawned {
		t.Fatal("a delegation aimed at nothing spawned anyway")
	}
}

// delegatedSessionID is the one session in the source workspace that is not the
// source itself — the delegate, mid-spawn, before delegate() returns its id.
func delegatedSessionID(t *testing.T, d *Daemon, sourceSessionID string) string {
	t.Helper()
	for _, session := range d.store.List("") {
		if session.ID != sourceSessionID {
			return session.ID
		}
	}
	t.Fatal("no delegated session exists yet")
	return ""
}

// The primer and flag-free `ready` are two builders over one answer: the daemon
// composes the launch block, the CLI renders the wire result. Undispatched, the
// count is pinned elsewhere; dispatched, this is the pin — same seeds, same
// order, or a delegate is primed with work its own `ready` will not offer.
func TestGardenPlot_ThePrimerAndReadyAgreeInsideAPlot(t *testing.T) {
	d := newGardenDaemon(t)
	planted := plot(t, d, protocol.SeedPlotMessage{
		SourceSessionID: protocol.Ptr("sess-a"),
		Title:           "ship the thing",
		Body:            protocol.Ptr("# the plan"),
		Children: []protocol.SeedPlotChild{
			{Title: "first step", Blocks: []string{"third-step"}},
			{Title: "second step"},
			{Title: "third step"},
		},
	})
	if err := d.recordGardenDispatch("sess-b", planted.Crown.ID); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	prime, err := d.gardenPrime("sess-b")
	if err != nil {
		t.Fatalf("gardenPrime: %v", err)
	}
	if prime.Crown == nil {
		t.Fatal("a dispatched session was primed without its plot")
	}
	var primed []string
	for _, seed := range prime.Crown.ReadySeeds {
		primed = append(primed, seed.ID)
	}
	var offered []string
	for _, seed := range ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-b")}).Seeds {
		offered = append(offered, seed.ID)
	}
	if !slices.Equal(primed, offered) {
		t.Fatalf("the primer lists %v, ready offers %v", primed, offered)
	}
	if len(offered) != 2 {
		t.Fatalf("the plot offered %v, want the two unblocked children", offered)
	}
	if prime.Ready != len(offered) {
		t.Fatalf("primer count = %d, want %d", prime.Ready, len(offered))
	}
}
