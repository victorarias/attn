package daemon

import (
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// link runs one `attn seed link`/`unlink` over the pipe.
func link(t *testing.T, d *Daemon, from, kind, to string, unlink bool) protocol.Response {
	t.Helper()
	msg := protocol.SeedLinkMessage{Cmd: protocol.CmdSeedLink, SeedID: from, Kind: kind, ToSeedID: to}
	if unlink {
		msg.Unlink = protocol.Ptr(true)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedLink(c, &msg) })
}

func mustLink(t *testing.T, d *Daemon, from, kind, to string) protocol.SeedLinkResult {
	t.Helper()
	resp := link(t, d, from, kind, to, false)
	if !resp.Ok {
		t.Fatalf("link %s %s %s: %v", from, kind, to, protocol.Deref(resp.Error))
	}
	return *resp.SeedLinkResult
}

func ready(t *testing.T, d *Daemon, msg protocol.SeedReadyMessage) protocol.SeedReadyResult {
	t.Helper()
	msg.Cmd = protocol.CmdSeedReady
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedReady(c, &msg) })
	if !resp.Ok {
		t.Fatalf("ready: %v", protocol.Deref(resp.Error))
	}
	return *resp.SeedReadyResult
}

func readyIDs(result protocol.SeedReadyResult) []string {
	out := make([]string, 0, len(result.Seeds))
	for _, seed := range result.Seeds {
		out = append(out, seed.ID)
	}
	return out
}

// The slice's acceptance, end to end: the plan's three-seed chain, the one seed
// it leaves ready, and the dependent surfacing the moment its blocker is
// harvested — nothing nudged, nothing cleared by hand.
func TestGardenEdges_HarvestingABlockerSurfacesTheDependent(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	c := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})

	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)
	mustLink(t, d, b.ID, garden.EdgePartOf, c.ID)

	first := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if got := readyIDs(first); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("ready = %v, want only the unblocked seed %s", got, a.ID)
	}
	if first.Scope != "garden" || first.ScopeID != "" {
		t.Fatalf("flag-free ready scoped to %s/%s, want the whole garden", first.Scope, first.ScopeID)
	}

	move(t, d, "sess-a", a.ID, garden.VerbTend, "", "trellis")
	move(t, d, "sess-a", a.ID, garden.VerbHarvest, "done", "trellis")

	second := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if got := readyIDs(second); len(got) != 1 || got[0] != b.ID {
		t.Fatalf("after harvesting the blocker, ready = %v, want %s", got, b.ID)
	}
}

// Readiness is computed per read and carried on the wire, so the panel renders
// the same answer the CLI gives instead of deriving its own.
func TestGardenEdges_ReadyRidesTheSeedOnTheWire(t *testing.T) {
	d := newGardenDaemon(t)
	var pushed []protocol.Seed
	d.gardenBroadcastHook = func(seeds []protocol.Seed, _ int) { pushed = seeds }

	blocker := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "blocker"})
	blocked := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "blocked"})
	mustLink(t, d, blocker.ID, garden.EdgeBlocks, blocked.ID)

	states := map[string]bool{}
	for _, seed := range pushed {
		states[seed.ID] = seed.Ready
	}
	if len(pushed) != 2 {
		t.Fatalf("linking pushed %d seeds, want the whole garden", len(pushed))
	}
	if !states[blocker.ID] || states[blocked.ID] {
		t.Fatalf("the push carries ready=%v, want the blocker ready and the blocked one not", states)
	}
}

// An edge is stored on one side, so `show` has to read the garden to answer the
// other: what blocks this, and what is part of it.
func TestGardenEdges_ShowListsBothDirections(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	c := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)
	mustLink(t, d, b.ID, garden.EdgePartOf, c.ID)

	relations := show(t, d, b.ID).Relations
	got := map[string]string{}
	for _, relation := range relations {
		got[relation.Label] = relation.SeedID
		if relation.Title == "" || relation.Status == "" {
			t.Fatalf("relation %+v carries no title or status; a bare id is not readable", relation)
		}
	}
	if got[garden.EdgePartOf] != c.ID || got["blocked-by"] != a.ID || len(relations) != 2 {
		t.Fatalf("show relations = %+v", relations)
	}

	if got := show(t, d, a.ID).Relations; len(got) != 1 || got[0].Label != garden.EdgeBlocks || got[0].SeedID != b.ID {
		t.Fatalf("the blocking side reads %+v, want one outbound blocks edge", got)
	}
}

func TestGardenEdges_UnlinkPutsTheSeedBack(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)

	// Linking the same edge twice is not a refusal and not a write: the garden
	// did not move, and the caller is told so.
	if again := mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID); again.Changed {
		t.Fatal("re-linking the same edge reported a change")
	}

	resp := link(t, d, a.ID, garden.EdgeBlocks, b.ID, true)
	if !resp.Ok {
		t.Fatalf("unlink: %v", protocol.Deref(resp.Error))
	}
	if len(resp.SeedLinkResult.Seed.Edges) != 0 {
		t.Fatalf("unlink left %+v", resp.SeedLinkResult.Seed.Edges)
	}
	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 2 {
		t.Fatalf("after unlinking, ready = %v, want both seeds", got)
	}
}

func TestGardenEdges_RefusalsNameBothSeeds(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)

	cycle := link(t, d, b.ID, garden.EdgeBlocks, a.ID, false)
	if cycle.Ok {
		t.Fatal("a blocks cycle was accepted")
	}
	// Loud means the caller can fix it without reading the garden: both seeds,
	// what it would do, and the edge to remove.
	for _, want := range []string{a.ID, b.ID, "deadlock", "attn seed unlink"} {
		if !strings.Contains(protocol.Deref(cycle.Error), want) {
			t.Fatalf("cycle refusal does not name %q: %s", want, protocol.Deref(cycle.Error))
		}
	}

	kind := link(t, d, a.ID, "sort-of", b.ID, false)
	if kind.Ok || !strings.Contains(protocol.Deref(kind.Error), "blocks and part-of") {
		t.Fatalf("an unknown kind was not refused with the kinds that exist: %+v", kind)
	}

	missing := link(t, d, a.ID, garden.EdgeBlocks, "s-zzzzzz", false)
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("an unknown seed was not refused by name: %+v", missing)
	}

	malformed := link(t, d, a.ID, garden.EdgeBlocks, "nope", false)
	if malformed.Ok || !strings.Contains(protocol.Deref(malformed.Error), "seed id") {
		t.Fatalf("a malformed id was not refused by shape: %+v", malformed)
	}

	stray := link(t, d, a.ID, garden.EdgePartOf, b.ID, true)
	if stray.Ok || !strings.Contains(protocol.Deref(stray.Error), "does not part-of") {
		t.Fatalf("unlinking an edge that is not there was not refused: %+v", stray)
	}
}

// Dispatch is scope inference and nothing more: a session dispatched at a crown
// gets that plot from a flag-free ready, --all steps back out to the garden, and
// the seeds themselves are never fenced — anybody may tend anything.
func TestGardenEdges_ReadyInfersTheDispatchedPlot(t *testing.T) {
	d := newGardenDaemon(t)
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})
	inside := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "inside", PartOf: protocol.Ptr(crown.ID),
	})
	outside := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "outside"})

	// Undispatched, the same session sees the whole garden.
	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 2 {
		t.Fatalf("undispatched ready = %v, want both seeds", got)
	}

	if err := d.recordGardenDispatch("sess-a", crown.ID); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	dispatched := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if got := readyIDs(dispatched); len(got) != 1 || got[0] != inside.ID {
		t.Fatalf("dispatched ready = %v, want the plot's child %s", got, inside.ID)
	}
	if dispatched.Scope != "plot" || dispatched.ScopeID != crown.ID {
		t.Fatalf("dispatched scope = %s/%s, want plot/%s", dispatched.Scope, dispatched.ScopeID, crown.ID)
	}
	if dispatched.Crown == nil || dispatched.Crown.PlotProgress == nil {
		t.Fatalf("a plot answer did not carry its crown and progress: %+v", dispatched.Crown)
	}

	// Not a fence: --all is the way back out, and the seed outside the plot is
	// still there to be tended.
	all := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a"), All: protocol.Ptr(true)})
	if got := readyIDs(all); len(got) != 2 || all.Scope != "garden" {
		t.Fatalf("--all from a dispatched session = %v (scope %s), want the whole garden", got, all.Scope)
	}
	move(t, d, "sess-a", outside.ID, garden.VerbTend, "", "trellis")
}

// A dispatch record outlives the crown it names — a withered plot, a garden
// somebody rearranged. Inference then infers nothing rather than refusing a
// caller who asked with no flags at all.
func TestGardenEdges_ReadyFallsBackWhenTheCrownIsGone(t *testing.T) {
	d := newGardenDaemon(t)
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "still here"})
	if err := d.recordGardenDispatch("sess-a", "s-zzzzzz"); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	result := ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})
	if result.Scope != "garden" || len(result.Seeds) != 1 {
		t.Fatalf("ready with a dangling dispatch = %+v, want the whole garden", result)
	}
}

func TestGardenEdges_ReadyScopesToAPlot(t *testing.T) {
	d := newGardenDaemon(t)
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "the plot"})
	inside := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "inside"})
	deeper := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "deeper"})
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "outside"})
	mustLink(t, d, inside.ID, garden.EdgePartOf, crown.ID)
	mustLink(t, d, deeper.ID, garden.EdgePartOf, inside.ID)

	result := ready(t, d, protocol.SeedReadyMessage{Plot: protocol.Ptr(crown.ID)})
	// The crown and the middle seed both have children, so the leaf is the only
	// thing in this plot anybody can pick up.
	if got := readyIDs(result); len(got) != 1 || got[0] != deeper.ID {
		t.Fatalf("plot ready = %v, want the one leaf %s", got, deeper.ID)
	}
	if result.Scope != "plot" || result.ScopeID != crown.ID {
		t.Fatalf("plot scope = %s/%s", result.Scope, result.ScopeID)
	}

	missing := gardenCall(t, func(c net.Conn) {
		d.handleSeedReady(c, &protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady, Plot: protocol.Ptr("s-zzzzzz")})
	})
	if missing.Ok || !strings.Contains(protocol.Deref(missing.Error), "s-zzzzzz") {
		t.Fatalf("an unknown plot was not refused by name: %+v", missing)
	}
}

// Oldest first, against the newest-first order every other read uses: ready is a
// work queue, and the seed that has waited longest is the one to hand over.
func TestGardenEdges_ReadyHandsOverTheOldestFirst(t *testing.T) {
	d := newGardenDaemon(t)
	first := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "waited longest"})
	second := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "just planted"})

	got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")}))
	if len(got) != 2 || got[0] != first.ID || got[1] != second.ID {
		t.Fatalf("ready order = %v, want %s before %s", got, first.ID, second.ID)
	}
}

// A tender holds its seed only while its session is one the daemon knows. A
// session that is gone must not park work forever.
func TestGardenEdges_ReadyReleasesASeedWhoseSessionIsGone(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "held"})
	move(t, d, "sess-b", seed.ID, garden.VerbTend, "", "")

	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 0 {
		t.Fatalf("ready = %v, want nothing: a live session holds the seed", got)
	}

	d.store.Remove("sess-b")
	if got := readyIDs(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")})); len(got) != 1 {
		t.Fatalf("ready = %v, want the seed back once its session is gone", got)
	}
}

// The count an agent is primed with at launch is the same answer `attn seed
// ready` gives with no flags — one computation, so guidance and the CLI cannot
// drift apart.
func TestGardenEdges_LaunchPrimerCountsTheSameReady(t *testing.T) {
	d := newGardenDaemon(t)
	a := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "first"})
	b := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "second"})
	mustLink(t, d, a.ID, garden.EdgeBlocks, b.ID)

	prime, err := d.gardenPrime("sess-a")
	if err != nil {
		t.Fatalf("gardenPrime: %v", err)
	}
	if want := len(ready(t, d, protocol.SeedReadyMessage{SourceSessionID: protocol.Ptr("sess-a")}).Seeds); prime.Ready != want {
		t.Fatalf("primer count = %d, want %d", prime.Ready, want)
	}
	if prime.Ready != 1 {
		t.Fatalf("primer count = %d, want 1", prime.Ready)
	}
	if prime.Crown != nil {
		t.Fatalf("an undispatched session was primed with a plot: %+v", prime.Crown)
	}
}

// An outpost has no garden, so it must not answer these two either — and the
// primer must hand a launching agent nothing rather than a loop it cannot run.
func TestGardenEdges_OutpostRefusesLinkAndReady(t *testing.T) {
	d := newEnrolledDaemon(t, "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()

	if resp := link(t, d, "s-7k3f9m", garden.EdgeBlocks, "s-7k3f9n", false); resp.Ok {
		t.Fatal("seed link answered on an outpost")
	}
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedReady(c, &protocol.SeedReadyMessage{Cmd: protocol.CmdSeedReady, All: protocol.Ptr(true)})
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), garden.Surface) {
		t.Fatalf("seed ready on an outpost: %+v", resp)
	}
	if primer := d.gardenPrimeForLaunch("sess-a"); primer != nil {
		t.Fatalf("an outpost primed a launching agent with %+v", primer)
	}
}
