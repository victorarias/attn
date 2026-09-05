package daemon

import (
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func newAgentCloseDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(func() {
		d.stopEventBus()
		d.sessionInputs().stopRetries()
	})
	d.ensureGardenCollections()
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	d.ptyBackend = &fakeSpawnBackend{}
	return d
}

func addAgentCloseSession(t *testing.T, d *Daemon, id, label string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: label, Agent: protocol.SessionAgentClaude,
		Directory: "/tmp/" + id, WorkspaceID: "ws-" + id,
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func callAgentClose(t *testing.T, d *Daemon, target, source, reason string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentClose(conn, &protocol.AgentCloseMessage{
			Cmd:             protocol.CmdAgentClose,
			TargetSessionID: target,
			SourceSessionID: source,
			Reason:          reason,
		})
	})
}

func closedEntry(t *testing.T, d *Daemon, sessionID string) protocol.SessionLedgerEntry {
	t.Helper()
	entry := d.store.SessionLedgerEntry(sessionID)
	if entry == nil {
		t.Fatalf("SessionLedgerEntry(%s) = nil, want a closed row", sessionID)
	}
	if protocol.Deref(entry.ClosedAt) == "" {
		t.Fatalf("%s is still live; want closed_at set", sessionID)
	}
	return *entry
}

func seedNotes(t *testing.T, d *Daemon, seedID string) []protocol.SeedNote {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedNotes(c, &protocol.SeedNotesMessage{Cmd: protocol.CmdSeedNotes, SeedID: seedID})
	})
	if !resp.Ok || resp.SeedNotesResult == nil {
		t.Fatalf("seed notes %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNotesResult.Notes
}

func refusal(t *testing.T, resp protocol.Response) (string, string) {
	t.Helper()
	if resp.Ok {
		t.Fatalf("response = %+v, want a refusal", resp)
	}
	return protocol.Deref(resp.ErrorCode), protocol.Deref(resp.Error)
}

func TestAgentCloseLetsASessionCloseItself(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "worker", "Worker")

	resp := callAgentClose(t, d, "worker", "worker", "the PR merged, nothing left to drive")

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	result := resp.AgentCloseResult
	if result.Rule != protocol.AgentCloseRuleSelf {
		t.Errorf("rule = %q, want self", result.Rule)
	}
	if result.TargetSessionID != "worker" || result.Label != "Worker" {
		t.Errorf("result = %+v, want it to name the closed session", result)
	}
	entry := closedEntry(t, d, "worker")
	if by := protocol.Deref(entry.ClosedBy); by != "worker" {
		t.Errorf("closed_by = %q, want the caller's session id", by)
	}
	if reason := protocol.Deref(entry.CloseReason); reason != "the PR merged, nothing left to drive" {
		t.Errorf("close_reason = %q, want the reason the caller gave", reason)
	}
	if d.store.Get("worker") != nil {
		t.Error("a closed session is still answered by store.Get; live surfaces would keep showing it")
	}

	page, err := d.store.SessionLedger(store.SessionLedgerQuery{Scope: store.SessionLedgerClosed})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].ID != "worker" {
		t.Errorf("closed ledger page = %+v, want the closed session listed", page.Entries)
	}
	live, err := d.store.SessionLedger(store.SessionLedgerQuery{Scope: store.SessionLedgerLive})
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Entries) != 0 {
		t.Errorf("live ledger page = %+v, want the closed session gone from it", live.Entries)
	}
}

func TestAgentCloseLetsADispatcherCloseWhatItDispatched(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "orchestrator", "Orchestrator")
	addAgentCloseSession(t, d, "delegate", "Delegate")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Ship the sweep", Body: protocol.Ptr("sweep the worktrees")})
	if err := d.recordGardenDispatch("delegate", seed.ID, "orchestrator", "/tmp/delegate", "claude", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	resp := callAgentClose(t, d, "delegate", "orchestrator", "reported back, work is on the seed")

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	if rule := resp.AgentCloseResult.Rule; rule != protocol.AgentCloseRuleDispatcher {
		t.Errorf("rule = %q, want dispatcher", rule)
	}
	entry := closedEntry(t, d, "delegate")
	if by := protocol.Deref(entry.ClosedBy); by != "orchestrator" {
		t.Errorf("closed_by = %q, want the dispatcher", by)
	}
	if d.store.Get("orchestrator") == nil {
		t.Error("the dispatcher closed itself along with its delegate")
	}
}

func TestAgentCloseRefusesASiblingAndNamesTheRule(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "orchestrator", "Orchestrator")
	addAgentCloseSession(t, d, "first", "First")
	addAgentCloseSession(t, d, "second", "Second")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Two children", Body: protocol.Ptr("one each")})
	for _, child := range []string{"first", "second"} {
		if err := d.recordGardenDispatch(child, seed.ID, "orchestrator", "/tmp/"+child, "claude", false); err != nil {
			t.Fatalf("recordGardenDispatch(%s): %v", child, err)
		}
	}

	resp := callAgentClose(t, d, "second", "first", "I think it is done")

	code, message := refusal(t, resp)
	if code != "close_not_authorized" {
		t.Errorf("error code = %q, want close_not_authorized", code)
	}
	for _, want := range []string{"close itself", "dispatched", "chief of staff"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal %q does not name the rule (%q missing)", message, want)
		}
	}
	if !strings.Contains(message, shortSessionID("orchestrator")) {
		t.Errorf("refusal %q does not name who did dispatch the target", message)
	}
	if d.store.Get("second") == nil {
		t.Fatal("the sibling was closed despite the refusal")
	}
}

func TestAgentCloseLetsTheChiefOfStaffCloseAnySession(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "chief", "Chief")
	addAgentCloseSession(t, d, "stranger", "Stranger")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	resp := callAgentClose(t, d, "stranger", "chief", "abandoned, nobody is driving it")

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	if rule := resp.AgentCloseResult.Rule; rule != protocol.AgentCloseRuleChiefOfStaff {
		t.Errorf("rule = %q, want chief_of_staff", rule)
	}
	if by := protocol.Deref(closedEntry(t, d, "stranger").ClosedBy); by != "chief" {
		t.Errorf("closed_by = %q, want the chief's session id", by)
	}
}

func TestAgentCloseKeepsTheChiefOfStaffProtectedFromItself(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "chief", "Chief")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	resp := callAgentClose(t, d, "chief", "chief", "done for the day")

	code, message := refusal(t, resp)
	if code != "session_close_protected" {
		t.Errorf("error code = %q, want session_close_protected", code)
	}
	if message != errChiefOfStaffProtected.Error() {
		t.Errorf("refusal = %q, want the existing chief guard message", message)
	}
	if d.store.Get("chief") == nil {
		t.Fatal("the chief closed itself despite the guard")
	}
}

func TestAgentCloseKeepsACrewBoundSessionProtected(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "orchestrator", "Orchestrator")
	addAgentCloseSession(t, d, "trellis-day", "Trellis")
	if _, err := d.claimCrewBinding("trellis", "trellis-day"); err != nil {
		t.Fatal(err)
	}
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Crew work", Body: protocol.Ptr("a member is on it")})
	if err := d.recordGardenDispatch("trellis-day", seed.ID, "orchestrator", "/tmp/trellis", "claude", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	resp := callAgentClose(t, d, "trellis-day", "orchestrator", "looks finished to me")

	code, message := refusal(t, resp)
	if code != "session_close_protected" {
		t.Errorf("error code = %q, want session_close_protected", code)
	}
	if message != "Trellis is protected from closing; put Trellis to sleep first" {
		t.Errorf("refusal = %q, want the existing crew guard message", message)
	}
	if d.store.Get("trellis-day") == nil {
		t.Fatal("a crew member's session was closed by its dispatcher")
	}
}

func TestAgentCloseNotesTheCloseOnTheSeedAndLeavesTheSeedWhereItIs(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "orchestrator", "Orchestrator")
	addAgentCloseSession(t, d, "delegate", "Delegate")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Land the ledger", Body: protocol.Ptr("close keeps the row")})
	move(t, d, "delegate", seed.ID, garden.VerbTend, "", "")
	if err := d.recordGardenDispatch("delegate", seed.ID, "orchestrator", "/tmp/delegate", "claude", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	resp := callAgentClose(t, d, "delegate", "orchestrator", "stalled on a question only the user can answer")

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	if got := resp.AgentCloseResult.SeedIds; len(got) != 1 || got[0] != seed.ID {
		t.Fatalf("seed_ids = %v, want the seed the closed session tended", got)
	}

	notes := seedNotes(t, d, seed.ID)
	if len(notes) == 0 {
		t.Fatal("the seed got no note about the close")
	}
	body := notes[0].Body
	wants := []string{
		"Delegate (delegate)", "Orchestrator (orchestr)",
		"stalled on a question only the user can answer", "did not move",
		"attn session show delegate",
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("close note %q is missing %q", body, want)
		}
	}
	if author := notes[0].AuthorSession; author != "orchestrator" {
		t.Errorf("note author = %q, want the closing session", author)
	}

	after, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != garden.StatusGrowing {
		t.Errorf("seed status = %q, want the close to leave it growing", after.Status)
	}
	if after.TenderSession != "delegate" {
		t.Errorf("tender = %q, want the closed session still holding the seed", after.TenderSession)
	}
}

func TestAgentCloseResolvesASeedToWhoeverTendsIt(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "orchestrator", "Orchestrator")
	addAgentCloseSession(t, d, "delegate", "Delegate")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Reach it by seed", Body: protocol.Ptr("the id an orchestrator knows")})
	move(t, d, "delegate", seed.ID, garden.VerbTend, "", "")
	if err := d.recordGardenDispatch("delegate", seed.ID, "orchestrator", "/tmp/delegate", "claude", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	resp := callHandler(t, func(conn net.Conn) {
		d.handleAgentClose(conn, &protocol.AgentCloseMessage{
			Cmd:             protocol.CmdAgentClose,
			TargetSeedID:    protocol.Ptr(seed.ID),
			SourceSessionID: "orchestrator",
			Reason:          "its report landed",
		})
	})

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("response = %+v", resp)
	}
	if got := resp.AgentCloseResult.TargetSessionID; got != "delegate" {
		t.Errorf("target = %q, want the seed's tender", got)
	}
	closedEntry(t, d, "delegate")
}

func TestAgentCloseRefusesWithoutAReason(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "worker", "Worker")

	code, message := refusal(t, callAgentClose(t, d, "worker", "worker", "   "))

	if code != "close_reason_required" {
		t.Errorf("error code = %q, want close_reason_required", code)
	}
	if !strings.Contains(message, "reason") {
		t.Errorf("refusal = %q, want it to say a reason is required", message)
	}
	if d.store.Get("worker") == nil {
		t.Fatal("the session closed without a reason")
	}
}

func TestAgentCloseRefusesAReasonPastTheLimitAndNamesBothNumbers(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "worker", "Worker")
	tooLong := strings.Repeat("x", agentCloseReasonMaxChars+1)

	code, message := refusal(t, callAgentClose(t, d, "worker", "worker", tooLong))

	if code != "close_reason_required" {
		t.Errorf("error code = %q, want close_reason_required", code)
	}
	for _, want := range []string{"401", "400"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal %q does not name the ask and the limit (%q missing)", message, want)
		}
	}
	if d.store.Get("worker") == nil {
		t.Fatal("an over-long reason still closed the session")
	}
}

func TestAgentCloseCountsAReasonInCharactersNotBytes(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "worker", "Worker")
	addAgentCloseSession(t, d, "other", "Other")

	full := strings.Repeat("🌱", agentCloseReasonMaxChars)
	resp := callAgentClose(t, d, "worker", "worker", full)
	if !resp.Ok {
		t.Fatalf("%d characters of Unicode were refused: %+v", agentCloseReasonMaxChars, resp)
	}

	code, message := refusal(t, callAgentClose(t, d, "other", "other", full+"🌱"))
	if code != "close_reason_required" {
		t.Errorf("error code = %q, want close_reason_required", code)
	}
	for _, want := range []string{"401", "400"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal %q counts bytes, not characters (%q missing)", message, want)
		}
	}
	if d.store.Get("other") == nil {
		t.Fatal("an over-long Unicode reason still closed the session")
	}
}

func TestAgentCloseRefusesAnAmbiguousPrefix(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "chief", "Chief")
	addAgentCloseSession(t, d, "dupe-one", "One")
	addAgentCloseSession(t, d, "dupe-two", "Two")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	code, message := refusal(t, callAgentClose(t, d, "dupe", "chief", "tidying up"))

	if code != "ambiguous_session" {
		t.Errorf("error code = %q, want ambiguous_session", code)
	}
	if !strings.Contains(message, "more than one session") {
		t.Errorf("refusal = %q, want it to say the prefix is ambiguous", message)
	}
	if d.store.Get("dupe-one") == nil || d.store.Get("dupe-two") == nil {
		t.Fatal("an ambiguous prefix closed a session anyway")
	}
}

func TestAgentCloseRefusesAnUnknownCaller(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "worker", "Worker")

	code, _ := refusal(t, callAgentClose(t, d, "worker", "ghost", "cleaning up"))

	if code != "sender_session_not_found" {
		t.Errorf("error code = %q, want sender_session_not_found", code)
	}
	if d.store.Get("worker") == nil {
		t.Fatal("a caller that is not a session closed one")
	}
}
