package daemon

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// newGardenDelegationDaemon is a home daemon with the garden declared and a
// delegating session in a workspace: the shape a real dispatch runs against.
func newGardenDelegationDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, string) {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	return d, backend, sourceSessionID
}

// capturePrompt records what a delegated agent is actually launched with.
func capturePrompt(t *testing.T, backend *fakeSpawnBackend, prompt *string) {
	t.Helper()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.InitialPromptFile == "" {
			return
		}
		raw, err := os.ReadFile(opts.InitialPromptFile)
		if err != nil {
			t.Errorf("read initial prompt: %v", err)
			return
		}
		*prompt = string(raw)
		if err := os.Remove(opts.InitialPromptFile); err != nil {
			t.Errorf("remove initial prompt: %v", err)
		}
	}
}

// The weight transfer: a delegation plants its own seed, the brief is the body,
// the delegate tends it, and the delegate is told where its work lives.
func TestDelegationPlantsASeedTendedByItsDelegate(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	var prompt string
	capturePrompt(t, backend, &prompt)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Label:           protocol.Ptr("Store migration"),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}

	seedID, bound := d.gardenDispatchCrown(result.SessionID)
	if !bound {
		t.Fatal("the delegation bound no seed to its session")
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read the bound seed: %v", err)
	}
	if seed.Body != "Migrate the store to X" {
		t.Fatalf("seed body = %q, want the brief", seed.Body)
	}
	if seed.Title != "Store migration" {
		t.Fatalf("seed title = %q, want the delegation's name", seed.Title)
	}
	if seed.TenderSession != result.SessionID {
		t.Fatalf("tender = %q, want the delegate session %q", seed.TenderSession, result.SessionID)
	}
	if seed.Status != garden.StatusGrowing {
		t.Fatalf("status = %q, want growing — a tended seed is not still planted", seed.Status)
	}
	// The planter is who asked for the work, not who does it: the tender above
	// is the delegate.
	if seed.PlanterSession != sourceSessionID {
		t.Fatalf("planter = %q, want the delegating session %q", seed.PlanterSession, sourceSessionID)
	}
	// A seed nobody is told about is a log nobody writes to.
	if !strings.Contains(prompt, seedID) {
		t.Fatalf("the delegate's prompt never names its seed %s:\n%s", seedID, prompt)
	}
	for _, verb := range []string{"attn seed note", "attn seed attach", "attn seed detach"} {
		if !strings.Contains(prompt, verb) {
			t.Fatalf("the delegate's prompt never offers %q", verb)
		}
	}
	// The ticket is untouched: both bind during the transition.
	ticket, err := d.store.ActiveTicketForSession(result.SessionID)
	if err != nil || ticket == nil {
		t.Fatalf("ActiveTicketForSession = %v, %v; the ticket must still bind", ticket, err)
	}
}

// A delegation aimed at a crown reports to that crown. Nothing is planted, and
// nothing is claimed — a plot's work is its children, and tending stays per-seed.
func TestDelegationAtACrownBindsItWithoutPlanting(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	crown := plantForDelegation(t, d, sourceSessionID, "The epic")

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "work the plot",
		Plot:            protocol.Ptr(crown.ID),
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}

	bound, ok := d.gardenDispatchCrown(result.SessionID)
	if !ok || bound != crown.ID {
		t.Fatalf("bound seed = %q, want the crown %q", bound, crown.ID)
	}
	read, err := d.readGarden()
	if err != nil {
		t.Fatalf("readGarden: %v", err)
	}
	if len(read.seeds) != 1 {
		t.Fatalf("the garden holds %d seeds, want only the crown", len(read.seeds))
	}
	seed, _, err := d.readSeed(crown.ID)
	if err != nil {
		t.Fatalf("read the crown: %v", err)
	}
	if seed.TenderSession != "" {
		t.Fatalf("the crown was claimed by %q; a plot dispatch is scope, not a claim", seed.TenderSession)
	}
}

// Recovery runs the same delegation again against a reserved session. The
// dispatch record is the binding, so it re-binds rather than planting twice.
func TestDelegationRecoveryRebindsTheSameSeed(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	msg := &protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	}
	result, err := d.delegate(msg)
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	first, _ := d.gardenDispatchCrown(result.SessionID)

	again, err := d.bindDelegationSeed(result.SessionID, sourceSessionID, "Migrate the store to X", "Store migration", "")
	if err != nil {
		t.Fatalf("re-bind: %v", err)
	}
	if again != first {
		t.Fatalf("re-bind produced %q, want the already-bound %q", again, first)
	}
	read, err := d.readGarden()
	if err != nil {
		t.Fatalf("readGarden: %v", err)
	}
	if len(read.seeds) != 1 {
		t.Fatalf("the garden holds %d seeds; a re-bind planted a second one", len(read.seeds))
	}
}

// An outpost holds no garden. The delegation still launches, on its ticket, and
// the delegate is never pointed at a seed that does not exist.
func TestDelegationOnAnOutpostBindsNoSeedAndStillLaunches(t *testing.T) {
	d := newEnrolledDaemon(t, "d-"+strings.Repeat("a", 32))
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	var prompt string
	capturePrompt(t, backend, &prompt)

	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate() on an outpost: %v", err)
	}
	if bound, ok := d.gardenDispatchCrown(result.SessionID); ok {
		t.Fatalf("an outpost bound seed %q", bound)
	}
	if strings.Contains(prompt, "attn seed note") {
		t.Fatalf("the delegate was pointed at a garden that is not here:\n%s", prompt)
	}
	if ticket, err := d.store.ActiveTicketForSession(result.SessionID); err != nil || ticket == nil {
		t.Fatalf("the ticket must still bind on an outpost: %v, %v", ticket, err)
	}
}

// plantForDelegation plants one seed through the real handler.
func plantForDelegation(t *testing.T, d *Daemon, sessionID, title string) protocol.Seed {
	t.Helper()
	msg := protocol.SeedPlantMessage{
		Cmd: protocol.CmdSeedPlant, Title: title, SourceSessionID: protocol.Ptr(sessionID),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlant(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plant %q: %v", title, protocol.Deref(resp.Error))
	}
	return resp.SeedPlantResult.Seed
}

// Status reports become log notes: whatever the delegate reports onto its
// ticket lands on its seed too, so the seed's log is the whole thread.
func TestStatusReportsLandOnTheBoundSeedsLog(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: sourceSessionID,
		Brief:           "Migrate the store to X",
		Agent:           protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	seedID, _ := d.gardenDispatchCrown(result.SessionID)

	awaitSeedNotes(t, d, seedID, 2, func() {
		for _, report := range []struct{ state, comment string }{
			{string(protocol.DispatchWorkStateInProgress), "reading the store layer"},
			{string(protocol.DispatchWorkStateReadyForReview), "PR #1 is up"},
		} {
			resp := callSetTicketStatus(t, d, result.SessionID, report.state, report.comment)
			if resp.TicketStatusResult == nil || !resp.TicketStatusResult.Applied {
				t.Fatalf("report %s was not applied: %+v", report.state, resp.TicketStatusResult)
			}
		}
	})

	log := show(t, d, seedID)
	if log.NotesTotal != 2 {
		t.Fatalf("the seed's log holds %d entries, want one per report", log.NotesTotal)
	}
	// Both reports are on the log, each carrying its state and its comment. The
	// pair is matched by content rather than by position: two writes inside one
	// clock tick share a stamp, and the log's tiebreaker is the note id, so
	// which of them reads as newest is not a property to assert on.
	for _, want := range []struct{ state, comment string }{
		{"in_progress", "reading the store layer"},
		{"ready_for_review", "PR #1 is up"},
	} {
		found := false
		for _, note := range log.Notes {
			if strings.Contains(note.Body, want.state) && strings.Contains(note.Body, want.comment) {
				found = true
				if note.AuthorSession != result.SessionID {
					t.Fatalf("author of the %s note = %q, want the reporting delegate", want.state, note.AuthorSession)
				}
			}
		}
		if !found {
			t.Fatalf("no note reported %s with its comment; the log holds %+v", want.state, log.Notes)
		}
	}
	// The column moved; the seed did not close. Harvesting stays deliberate.
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	if seed.Status != garden.StatusGrowing {
		t.Fatalf("status = %q; a status report must not move the seed's lifecycle", seed.Status)
	}
}

// A completed report is still a note. It reads as what happened, and closing
// the seed stays `attn seed harvest` with what got done in its own words.
func TestCompletedReportDoesNotHarvestTheSeed(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Migrate the store to X", Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	seedID, _ := d.gardenDispatchCrown(result.SessionID)

	awaitSeedNotes(t, d, seedID, 1, func() {
		callSetTicketStatus(t, d, result.SessionID, string(protocol.DispatchWorkStateCompleted), "merged")
	})

	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatalf("read the seed: %v", err)
	}
	if garden.Closed(seed.Status) {
		t.Fatalf("status = %q; the seed closed on a report nobody accepted yet", seed.Status)
	}
	if seed.TenderSession != result.SessionID {
		t.Fatalf("tender = %q; a report must not release the claim", seed.TenderSession)
	}
}

// The by-id form is deliberately permissive so a peer can nudge any column for
// awareness. That is not a report about the peer's own work, so it mirrors
// nothing onto the peer's seed.
func TestNudgingSomebodyElsesTicketMirrorsNothing(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	worker, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Migrate the store to X", Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	peer, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Something else entirely", Label: protocol.Ptr("Peer work"), Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	workerSeed, _ := d.gardenDispatchCrown(worker.SessionID)
	peerSeed, _ := d.gardenDispatchCrown(peer.SessionID)
	workerTicket, err := d.store.ActiveTicketForSession(worker.SessionID)
	if err != nil || workerTicket == nil {
		t.Fatalf("ActiveTicketForSession: %v, %v", workerTicket, err)
	}

	awaitStatusHandled(t, d, &protocol.SetTicketStatusMessage{
		Cmd:             protocol.CmdSetTicketStatus,
		SourceSessionID: peer.SessionID,
		WorkState:       protocol.DispatchWorkStateNeedsInput,
		Comment:         protocol.Ptr("waiting on you"),
		TicketID:        protocol.Ptr(workerTicket.ID),
	})

	if total := show(t, d, workerSeed).NotesTotal; total != 0 {
		t.Fatalf("a peer's nudge wrote %d notes onto the worker's log", total)
	}
	if total := show(t, d, peerSeed).NotesTotal; total != 0 {
		t.Fatalf("a peer's nudge wrote %d notes onto its own log", total)
	}
}

// Steering reaches the tender: a caller holding a seed id addresses whoever is
// working on it, without reading the tender out of `attn seed show` first.
func TestAgentMsgToASeedReachesItsTender(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegate(&protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, SourceSessionID: sourceSessionID,
		Brief: "Migrate the store to X", Agent: protocol.Ptr("codex"),
	})
	if err != nil {
		t.Fatalf("delegate(): %v", err)
	}
	seedID, _ := d.gardenDispatchCrown(result.SessionID)

	tender, err := d.seedTenderSession(seedID)
	if err != nil {
		t.Fatalf("seedTenderSession(%s): %v", seedID, err)
	}
	if tender != result.SessionID {
		t.Fatalf("resolved %q, want the delegate session %q", tender, result.SessionID)
	}
}

func TestAgentMsgToAnUntendedSeedRefusesByName(t *testing.T) {
	d, _, sourceSessionID := newGardenDelegationDaemon(t)
	seed := plantForDelegation(t, d, sourceSessionID, "Nobody has this")

	_, err := d.seedTenderSession(seed.ID)
	if err == nil {
		t.Fatal("an untended seed resolved to somebody")
	}
	for _, want := range []string{"nobody is tending", seed.ID, "attn seed note"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// awaitSeedNotes runs work and returns once want notes have landed on seedID's
// log. The status reply is written before the mirror runs — the agent is told
// its column moved without waiting on bookkeeping — so a test that read the log
// straight after the reply would race the write.
//
// The wait is on the note fact itself, the same signal the panel re-push rides.
func awaitSeedNotes(t *testing.T, d *Daemon, seedID string, want int, work func()) {
	t.Helper()
	landed := make(chan struct{}, want+4)
	unsubscribe := d.eventBus.Subscribe(bus.Filter{FactGardenNoted}, func(ev bus.Event) {
		if ev.Subject == seedID {
			landed <- struct{}{}
		}
	})
	defer unsubscribe()
	work()
	for i := 0; i < want; i++ {
		select {
		case <-landed:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d notes reached %s", i, want, seedID)
		}
	}
}

// awaitStatusHandled runs one status change and returns when the handler has
// finished, not merely when it replied — the mirror runs after the reply, so a
// test asserting that nothing was written needs the whole handler behind it.
//
// The signal is the server side of the pipe closing, which gardenCall's shape
// already gives: the handler returns, the connection closes, the read ends.
func awaitStatusHandled(t *testing.T, d *Daemon, msg *protocol.SetTicketStatusMessage) {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		d.handleSetTicketStatus(server, msg)
		_ = server.Close()
	}()
	defer client.Close()
	if _, err := io.ReadAll(client); err != nil {
		t.Fatalf("read the status handler to completion: %v", err)
	}
}
