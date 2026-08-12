package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

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
	d.gardenBroadcastHook = func(seeds []protocol.Seed) {
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
	d.gardenBroadcastHook = func([]protocol.Seed) { pushes++ }

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
