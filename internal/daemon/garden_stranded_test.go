package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// seedStrandedTicket writes one board ticket and moves it to the status a dead
// session leaves behind.
func seedStrandedTicket(t *testing.T, d *Daemon, id, title, description string, status store.TicketStatus, assignee string) {
	t.Helper()
	seedBacklogTicket(t, d, id, title, description, store.TicketStatusWorking, assignee)
	if _, err := d.store.SetTicketStatus(id, status, store.TicketAuthorAttn, "", time.Now()); err != nil {
		t.Fatalf("stamp %s as %s: %v", id, status, err)
	}
}

func seedByTitle(t *testing.T, d *Daemon, title string) garden.Seed {
	t.Helper()
	for _, seed := range gardenSeeds(t, d) {
		if seed.Title == title {
			return seed
		}
	}
	t.Fatalf("no seed titled %q in %+v", title, gardenSeeds(t, d))
	return garden.Seed{}
}

// The bug: a session dies mid-flight, its ticket is stamped crashed, and the
// work is invisible from every garden surface. After the pass it is a seed —
// growing, still held by the session that died, and offered to whoever picks it
// up because that session is gone.
func TestStrandedCrashedTicketBecomesATendedSeed(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusCrashed, "sess-dead")

	d.replantStrandedTickets()

	seed := seedByTitle(t, d, "Wire the thing")
	if seed.Body != "the whole brief" {
		t.Fatalf("replanted seed lost the brief: %+v", seed)
	}
	if seed.Status != garden.StatusGrowing {
		t.Fatalf("replanted seed status = %q, want growing — the work was cut off, not finished", seed.Status)
	}
	if seed.TenderSession != "sess-dead" {
		t.Fatalf("replanted seed tender = %q, want the session that died", seed.TenderSession)
	}
	if ids := readyIDs(ready(t, d, protocol.SeedReadyMessage{All: protocol.Ptr(true)})); len(ids) != 1 || ids[0] != seed.ID {
		t.Fatalf("ready = %v, want just %s — a dead tender holds nothing", ids, seed.ID)
	}

	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "wire-the-thing") || !strings.Contains(notes[0].Body, "sess-dead") {
		t.Fatalf("replanted seed does not say where it came from: %+v", notes)
	}

	ticket, err := d.store.GetTicket("wire-the-thing")
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v %v", ticket, err)
	}
	if ticket.ArchivedAt == nil || ticket.Status != store.TicketStatusDone {
		t.Fatalf("replanted ticket is still on the board: status=%q archived=%v", ticket.Status, ticket.ArchivedAt)
	}
	if ticket.Description != "the whole brief" {
		t.Fatalf("replanted ticket lost its record: %+v", ticket)
	}
}

// The hold survives the migration with no code to move it: a seed's tender is
// derived from session liveness, so the session coming back takes its work back.
func TestReplantedSeedReturnsToItsSessionWhenItRevives(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusCrashed, "sess-dead")

	d.replantStrandedTickets()
	seed := seedByTitle(t, d, "Wire the thing")

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "sess-dead", Label: "back",
		State: "idle", StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	if ids := readyIDs(ready(t, d, protocol.SeedReadyMessage{All: protocol.Ptr(true)})); len(ids) != 0 {
		t.Fatalf("ready = %v, want none — %s is held by a session that is live again", ids, seed.ID)
	}
}

// Failed is a decision its agent already made, so the seed lands withered with
// the reason on it: on the record, out of ready, replantable by anyone who
// disagrees.
func TestStrandedFailedTicketBecomesAWitheredSeed(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "gave-up", "Gave up", "could not do it", store.TicketStatusFailed, "sess-dead")

	d.replantStrandedTickets()

	seed := seedByTitle(t, d, "Gave up")
	if seed.Status != garden.StatusWithered {
		t.Fatalf("failed ticket landed as %q, want withered", seed.Status)
	}
	if !strings.Contains(seed.Reason, "gave-up") {
		t.Fatalf("withered seed reason = %q, want the ticket it came from", seed.Reason)
	}
	if seed.TenderSession != "" {
		t.Fatalf("withered seed is held by %q; a decided outcome holds nobody", seed.TenderSession)
	}
	if ids := readyIDs(ready(t, d, protocol.SeedReadyMessage{All: protocol.Ptr(true)})); len(ids) != 0 {
		t.Fatalf("ready = %v, want none", ids)
	}
}

// The reconciler's verdict is the whole decision-support for picking the work
// back up, and it lives on a board nobody opens. It rides onto the seed.
func TestReplantedSeedCarriesTheReconcileVerdict(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusCrashed, "sess-dead")
	verdict := ticketReconcileCommentPrefix + " verdict — the branch is pushed, the PR is not opened"
	if _, err := d.store.AddTicketComment("wire-the-thing", store.TicketAuthorAttn, verdict, time.Now()); err != nil {
		t.Fatalf("AddTicketComment: %v", err)
	}

	d.replantStrandedTickets()

	seed := seedByTitle(t, d, "Wire the thing")
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "the branch is pushed, the PR is not opened") {
		t.Fatalf("replanted seed dropped the verdict that explains it: %+v", notes)
	}
}

// The pass runs on every boot, and the archive is what makes the second one
// find nothing.
func TestStrandedReplantIsIdempotent(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusCrashed, "sess-dead")

	d.replantStrandedTickets()
	d.replantStrandedTickets()

	if seeds := gardenSeeds(t, d); len(seeds) != 1 {
		t.Fatalf("seeds after two passes = %d, want 1: %+v", len(seeds), seeds)
	}
}

// Live work still drains itself, closed work is over, and an automation run's
// ticket is daemon bookkeeping that already has its seed through the mirror.
func TestStrandedReplantLeavesTheRestOfTheBoardAlone(t *testing.T) {
	d := newGardenDaemon(t)
	seedBacklogTicket(t, d, "in-flight", "In flight", "being worked", store.TicketStatusWorking, "sess-a")
	seedBacklogTicket(t, d, "finished", "Finished", "already shipped", store.TicketStatusDone, "sess-a")
	if _, err := d.createTicketWithUniqueSlug(store.Ticket{
		Title: "Automation run", Description: "a run's own ticket",
		Status: store.TicketStatusWorking, Assignee: "sess-auto", AutomationRunID: "run-1",
	}, "automation-run", "chief", store.TicketRoleChiefOfStaff, nil, time.Now()); err != nil {
		t.Fatalf("seed automation ticket: %v", err)
	}
	if _, err := d.store.SetTicketStatus("automation-run", store.TicketStatusCrashed, store.TicketAuthorAttn, "", time.Now()); err != nil {
		t.Fatalf("stamp the automation ticket crashed: %v", err)
	}

	d.replantStrandedTickets()

	if seeds := gardenSeeds(t, d); len(seeds) != 0 {
		t.Fatalf("replant planted seeds it should not have: %+v", seeds)
	}
	for _, id := range []string{"in-flight", "finished", "automation-run"} {
		ticket, err := d.store.GetTicket(id)
		if err != nil || ticket == nil {
			t.Fatalf("GetTicket %s: %v %v", id, ticket, err)
		}
		if ticket.ArchivedAt != nil {
			t.Fatalf("replant archived %s: %+v", id, ticket)
		}
	}
}

// The live seam. A session dies now, the reconciler classifies it, and the work
// is in the garden before anyone reboots the daemon — carrying the verdict the
// reconciler just wrote, which is only possible because the replant waits for it.
func TestReconcilingADeathReplantsTheTicketIntoTheGarden(t *testing.T) {
	d := newGardenDaemon(t)
	seedStrandedTicket(t, d, "wire-the-thing", "Wire the thing", "the whole brief", store.TicketStatusCrashed, "sess-dead")

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	d.ticketReconcileExec = func(ctx context.Context, in ticketReconcileInputs) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			StructuredOutput: []byte(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"tests pass except e2e"}`),
		}, nil
	}

	if _, err := d.reconcileJobHandler(context.Background(), reconcileTask(ticketReconcileInputs{
		TicketID:       "wire-the-thing",
		StatusAtClaim:  store.TicketStatusCrashed,
		SessionID:      "sess-dead",
		Agent:          "codex",
		TranscriptPath: transcript,
	})); err != nil {
		t.Fatalf("reconcileJobHandler: %v", err)
	}

	seed := seedByTitle(t, d, "Wire the thing")
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read notes: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "e2e spec never ran") {
		t.Fatalf("seed planted before the verdict landed on it: %+v", notes)
	}
	ticket, err := d.store.GetTicket("wire-the-thing")
	if err != nil || ticket == nil || ticket.ArchivedAt == nil {
		t.Fatalf("reconciled ticket is still on the board: %+v (%v)", ticket, err)
	}
}
