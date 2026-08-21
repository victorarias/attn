package daemon

import (
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedNudges_DeliverThroughTheAgentMessageDoorbell(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	writes := make(chan string, 4)
	fixture.d.ptyBackend = &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		writes <- string(data)
	}}
	previousWindow := agentMessageTakenWindow
	agentMessageTakenWindow = 0
	t.Cleanup(func() { agentMessageTakenWindow = previousWindow })
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "look now", true)
	for {
		write := <-writes
		if !strings.HasPrefix(write, bracketedPasteStart) {
			continue
		}
		prompt := strings.TrimSuffix(strings.TrimPrefix(write, bracketedPasteStart), bracketedPasteEnd)
		if !strings.Contains(prompt, fixture.leaf.ID+" moved: note") || strings.Contains(prompt, "look now") {
			t.Fatalf("doorbell = %q", prompt)
		}
		break
	}
}

type seededNudgeGarden struct {
	d                  *Daemon
	crown, child, leaf protocol.Seed
}

// newSeededNudgeGarden is a copy-shaped fixture: several live sessions and a
// real two-level plot already exist before a test starts exercising bells.
func newSeededNudgeGarden(t *testing.T) seededNudgeGarden {
	t.Helper()
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	addGardenSession(t, d, "sess-c")
	addGardenSession(t, d, "sess-d")
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "ship seed nudges"})
	child := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "daemon mechanics", PartOf: protocol.Ptr(crown.ID),
	})
	leaf := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "delivery proof", PartOf: protocol.Ptr(child.ID),
	})
	return seededNudgeGarden{d: d, crown: crown, child: child, leaf: leaf}
}

func watchSeed(t *testing.T, d *Daemon, sessionID, seedID string, unwatch bool) *protocol.SeedWatchResult {
	t.Helper()
	msg := protocol.SeedWatchMessage{
		Cmd: protocol.CmdSeedWatch, SourceSessionID: sessionID, SeedID: seedID,
	}
	if unwatch {
		msg.Unwatch = protocol.Ptr(true)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedWatch(c, &msg) })
	if !resp.Ok {
		t.Fatalf("watch %s from %s: %v", seedID, sessionID, protocol.Deref(resp.Error))
	}
	return resp.SeedWatchResult
}

func ringingNote(t *testing.T, d *Daemon, sessionID, seedID, body string, ring bool) protocol.SeedNote {
	t.Helper()
	msg := protocol.SeedNoteMessage{
		Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body,
		SourceSessionID: protocol.Ptr(sessionID),
	}
	if ring {
		msg.Ring = protocol.Ptr(true)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
	if !resp.Ok {
		t.Fatalf("note on %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNoteResult.Note
}

func queuedSeedBells(t *testing.T, d *Daemon, sessionID string) []string {
	t.Helper()
	messages, err := d.store.UndeliveredAgentMessages(sessionID)
	if err != nil {
		t.Fatalf("queued bells for %s: %v", sessionID, err)
	}
	contents := make([]string, 0, len(messages))
	for _, message := range messages {
		contents = append(contents, message.Content)
	}
	return contents
}

func assertOneSeedBell(t *testing.T, d *Daemon, sessionID, seedID, event string) {
	t.Helper()
	queued := queuedSeedBells(t, d, sessionID)
	if len(queued) != 1 || !strings.Contains(queued[0], seedID+" moved: "+event) {
		t.Fatalf("queued bells for %s = %q, want one %s/%s doorbell", sessionID, queued, seedID, event)
	}
}

func TestSeedNudges_DispatcherHearsTheDelegatesHarvest(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	d := fixture.d
	move(t, d, "sess-b", fixture.leaf.ID, garden.VerbTend, "", "")
	if err := d.recordGardenDispatch("sess-b", fixture.leaf.ID, "sess-a", "/tmp/a", "codex", false); err != nil {
		t.Fatalf("record dispatch: %v", err)
	}

	move(t, d, "sess-b", fixture.leaf.ID, garden.VerbHarvest, "proof complete", "")
	assertOneSeedBell(t, d, "sess-a", fixture.leaf.ID, "harvested")
}

func TestSeedNudges_NotesRingOnlyByChoice(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "ordinary progress", false)
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("plain note rang: %q", queued)
	}
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "please look", true)
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")
}

func TestSeedNudges_CrownWatchBubblesFromAGrandchild(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.crown.ID, false)

	move(t, fixture.d, "sess-c", fixture.leaf.ID, garden.VerbTend, "", "")
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "tended")
}

func TestSeedNudges_CoalesceUntilShowAndThenRingAgain(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "first", true)
	move(t, fixture.d, "sess-c", fixture.leaf.ID, garden.VerbTend, "", "")
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")

	resp := gardenCall(t, func(c net.Conn) {
		fixture.d.handleSeedShow(c, &protocol.SeedShowMessage{
			Cmd: protocol.CmdSeedShow, SeedID: fixture.leaf.ID, SourceSessionID: protocol.Ptr("sess-b"),
		})
	})
	if !resp.Ok || !resp.SeedShowResult.Watching {
		t.Fatalf("show after watch = %+v", resp)
	}
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("show left a queued bell: %q", queued)
	}

	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "after the read", true)
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")
}

func TestSeedNudges_NotesReadResetsTheBell(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "first", true)

	resp := gardenCall(t, func(c net.Conn) {
		fixture.d.handleSeedNotes(c, &protocol.SeedNotesMessage{
			Cmd: protocol.CmdSeedNotes, SeedID: fixture.leaf.ID, SourceSessionID: protocol.Ptr("sess-b"),
		})
	})
	if !resp.Ok {
		t.Fatalf("notes: %v", protocol.Deref(resp.Error))
	}
	ringingNote(t, fixture.d, "sess-c", fixture.leaf.ID, "second", true)
	assertOneSeedBell(t, fixture.d, "sess-b", fixture.leaf.ID, "note")
}

func TestSeedNudges_NeverRingTheWriter(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.leaf.ID, false)

	move(t, fixture.d, "sess-b", fixture.leaf.ID, garden.VerbTend, "", "")
	ringingNote(t, fixture.d, "sess-b", fixture.leaf.ID, "my own words", true)
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("writer rang itself: %q", queued)
	}
}

func TestSeedNudges_UnwatchStopsThePlot(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	watchSeed(t, fixture.d, "sess-b", fixture.crown.ID, false)
	result := watchSeed(t, fixture.d, "sess-b", fixture.crown.ID, true)
	if result.Watching || !result.Changed {
		t.Fatalf("unwatch result = %+v", result)
	}

	move(t, fixture.d, "sess-c", fixture.leaf.ID, garden.VerbTend, "", "")
	if queued := queuedSeedBells(t, fixture.d, "sess-b"); len(queued) != 0 {
		t.Fatalf("unwatched session rang: %q", queued)
	}
}
