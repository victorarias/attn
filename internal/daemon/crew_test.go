package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func writeCrewHomes(t *testing.T, dataRoot string) {
	t.Helper()
	root := filepath.Join(dataRoot, crew.HomesDirName)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(root, "CREW.md"), "# The crew\n")
	for _, member := range []struct{ id, handoff string }{
		{"alder", "2026-08-10T19-20Z-alder.md"},
		{"keel", "2026-08-13T22-10Z-keel.md"},
		{"trellis", "2026-08-13T22-20Z-trellis.md"},
	} {
		home := filepath.Join(root, member.id)
		write(filepath.Join(home, crew.CharterFileName), "# "+member.id+"\n\nWhat I care about.\n")
		write(filepath.Join(home, "handoffs", member.handoff), "Where I left off.\n")
	}
}

func newCrewDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	return d
}

func crewList(t *testing.T, d *Daemon) []protocol.CrewMember {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleCrewList(c, &protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
	})
	if !resp.Ok {
		t.Fatalf("crew list: %v", protocol.Deref(resp.Error))
	}
	return resp.CrewListResult.Members
}

func addSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, State: "idle",
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func memberByID(t *testing.T, members []protocol.CrewMember, id string) protocol.CrewMember {
	t.Helper()
	for _, m := range members {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no member %q in the roster", id)
	return protocol.CrewMember{}
}

func TestCrew_AMemberIsImportedBoundAndReleased(t *testing.T) {
	d := newCrewDaemon(t)

	members := crewList(t, d)
	if len(members) != 3 {
		t.Fatalf("roster = %d members, want the 3 homes on disk", len(members))
	}
	trellis := memberByID(t, members, "trellis")
	if trellis.BindingSession != nil {
		t.Fatalf("a freshly imported member is awake: %v", *trellis.BindingSession)
	}
	if _, err := os.Stat(trellis.CharterPath); err != nil {
		t.Fatalf("charter path does not point at a real file: %v", err)
	}

	addSession(t, d, "sess-trellis")
	memberID, err := d.claimCrewBinding("trellis", "sess-trellis")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if memberID != "trellis" {
		t.Fatalf("claim returned %q, want trellis", memberID)
	}

	session := d.sessionForBroadcast(d.store.Get("sess-trellis"))
	if got := protocol.Deref(session.CrewMember); got != "trellis" {
		t.Fatalf("broadcast session's crew_member = %q, want trellis", got)
	}
	if got := protocol.Deref(d.agentPeekResult(d.store.Get("sess-trellis")).CrewMember); got != "trellis" {
		t.Fatalf("peek's crew_member = %q, want trellis", got)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != "sess-trellis" {
		t.Fatalf("roster binding = %q, want sess-trellis", got)
	}

	d.closeSession("sess-trellis", store.SessionClose{By: store.SessionClosedByUser})
	if binding := memberByID(t, crewList(t, d), "trellis").BindingSession; binding != nil {
		t.Fatalf("the binding survived its session: %v", *binding)
	}
}

func TestCrew_SecondClaimOnALiveMemberIsRefusedByName(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-first")
	addSession(t, d, "sess-second")

	if _, err := d.claimCrewBinding("keel", "sess-first"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, err := d.claimCrewBinding("keel", "sess-second")
	if err == nil {
		t.Fatal("a second keel was allowed to wake")
	}
	for _, want := range []string{"Keel", "sess-fir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "keel").BindingSession); got != "sess-first" {
		t.Fatalf("binding after the refusal = %q, want sess-first", got)
	}
}

func TestCrew_ASessionTakingASecondNameDropsTheFirst(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-one")

	if _, err := d.claimCrewBinding("keel", "sess-one"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := d.claimCrewBinding("trellis", "sess-one"); err != nil {
		t.Fatalf("second claim: %v", err)
	}

	members := crewList(t, d)
	if got := protocol.Deref(memberByID(t, members, "trellis").BindingSession); got != "sess-one" {
		t.Errorf("trellis binding = %q, want sess-one", got)
	}
	if got := protocol.Deref(memberByID(t, members, "keel").BindingSession); got != "" {
		t.Errorf("keel is still bound to %q after the session became trellis", got)
	}
	if got := d.crewMembersBySession()["sess-one"]; got != "trellis" {
		t.Errorf("session decorates as %q, want trellis", got)
	}
}

func TestCrew_ARefusedSecondNameKeepsTheFirst(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-one")
	addSession(t, d, "sess-two")

	if _, err := d.claimCrewBinding("keel", "sess-one"); err != nil {
		t.Fatalf("claim keel: %v", err)
	}
	if _, err := d.claimCrewBinding("trellis", "sess-two"); err != nil {
		t.Fatalf("claim trellis: %v", err)
	}
	if _, err := d.claimCrewBinding("trellis", "sess-one"); err == nil {
		t.Fatal("a live trellis was handed to a second session")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "keel").BindingSession); got != "sess-one" {
		t.Errorf("keel binding = %q, want sess-one — a refused claim wrote something", got)
	}
}

func TestCrew_ABindingWhoseSessionIsGoneDoesNotHold(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-crashed")
	if _, err := d.claimCrewBinding("alder", "sess-crashed"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	d.store.Remove("sess-crashed")

	if binding := memberByID(t, crewList(t, d), "alder").BindingSession; binding != nil {
		t.Fatalf("the roster reports a dead session as awake: %v", *binding)
	}
	addSession(t, d, "sess-fresh")
	if _, err := d.claimCrewBinding("alder", "sess-fresh"); err != nil {
		t.Fatalf("a member whose session died could not be woken: %v", err)
	}
}

func TestCrew_ReclaimingItsOwnBindingIsIdempotent(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-keel")
	if _, err := d.claimCrewBinding("keel", "sess-keel"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := d.claimCrewBinding("keel", "sess-keel"); err != nil {
		t.Fatalf("re-announcing a live session lost its binding: %v", err)
	}
}

func TestCrew_ClaimingAnUnregisteredNameIsRefusedWithWhereToLook(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-a")
	_, err := d.claimCrewBinding("nobody", "sess-a")
	if err == nil {
		t.Fatal("an unregistered name was bound")
	}
	if !strings.Contains(err.Error(), "attn crew list") {
		t.Errorf("refusal %q does not say where the roster is", err)
	}
}

func TestCrew_ReimportingHomesLeavesLiveRecordsAlone(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-keel")
	if _, err := d.claimCrewBinding("keel", "sess-keel"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	d.importCrewHomes()

	if got := protocol.Deref(memberByID(t, crewList(t, d), "keel").BindingSession); got != "sess-keel" {
		t.Fatalf("re-import clobbered a live binding: binding = %q", got)
	}
	if len(crewList(t, d)) != 3 {
		t.Fatal("re-import duplicated the roster")
	}
}

func TestCrew_ImportRefusesAStoredHomeFromAnotherProfile(t *testing.T) {
	d, _, readLog := newWakeableDaemon(t)
	members, docs, err := d.readCrewMembers()
	if err != nil {
		t.Fatalf("read roster: %v", err)
	}
	member, ok := crew.Resolve("trellis", members)
	if !ok {
		t.Fatal("trellis is missing from the fixture roster")
	}
	foreignRoot := filepath.Join(t.TempDir(), "copied-default", crew.HomesDirName)
	member.HomeDir = filepath.Join(foreignRoot, member.ID)
	member.CharterPath = filepath.Join(member.HomeDir, crew.CharterFileName)
	body, err := member.Encode()
	if err != nil {
		t.Fatalf("encode copied record: %v", err)
	}
	expected := docs[member.ID].Rev
	schema, err := d.crewCollection()
	if err != nil {
		t.Fatalf("crew collection: %v", err)
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.ID, false)
	if _, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: member.ID, Body: body, Expected: &expected,
	}, fact, time.Now()); err != nil {
		t.Fatalf("seed copied registry record: %v", err)
	}

	d.importCrewHomes()
	log := readLog()
	for _, want := range []string{"import refused", member.HomeDir, filepath.Join(d.dataRoot, crew.HomesDirName), "attn.db copied from another profile"} {
		if !strings.Contains(log, want) {
			t.Errorf("import refusal does not name %q:\n%s", want, log)
		}
	}
	if _, _, err := d.readCrewMembers(); err == nil {
		t.Fatal("an operational roster read accepted the copied member record")
	} else if !strings.Contains(err.Error(), member.HomeDir) {
		t.Fatalf("read refusal %q does not name the foreign home", err)
	}

	addSession(t, d, "copied-db-wake")
	if _, err := d.claimCrewBinding(member.ID, "copied-db-wake"); err == nil {
		t.Fatal("a copied member record was writable")
	} else if !strings.Contains(err.Error(), member.HomeDir) {
		t.Fatalf("write refusal %q does not name the foreign home", err)
	}
}

func TestCrew_AHandAddedHomeJoinsTheRoster(t *testing.T) {
	d := newCrewDaemon(t)
	home := filepath.Join(d.dataRoot, crew.HomesDirName, "sable")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, crew.CharterFileName), []byte("# sable\n"), 0o644); err != nil {
		t.Fatalf("write charter: %v", err)
	}

	d.importCrewHomes()

	if got := len(crewList(t, d)); got != 4 {
		t.Fatalf("roster = %d members, want 4 after a home was added by hand", got)
	}
	memberByID(t, crewList(t, d), "sable")
}

func TestCrew_AnOutpostIsFenced(t *testing.T) {
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()

	resp := gardenCall(t, func(c net.Conn) {
		d.handleCrewList(c, &protocol.CrewListMessage{Cmd: protocol.CmdCrewList})
	})
	if resp.Ok {
		t.Fatal("an outpost served the crew roster")
	}
	message := protocol.Deref(resp.Error)
	if !strings.Contains(message, home) {
		t.Errorf("refusal %q does not name the home", message)
	}
	if !strings.Contains(message, enrollment.PlanPath) {
		t.Errorf("refusal %q does not name the plan tracking the gap", message)
	}

	addSession(t, d, "sess-a")
	if _, err := d.claimCrewBinding("keel", "sess-a"); err == nil {
		t.Fatal("an outpost bound a crew member")
	}
}

func TestCrew_TenderNamesResolveWhereAMemberExists(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-keel")
	if _, err := d.claimCrewBinding("keel", "sess-keel"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if got := d.resolveTenderMember("Keel", ""); got != "keel" {
		t.Errorf("a registered name resolved to %q, want keel", got)
	}
	if got := d.resolveTenderMember("some-worker", ""); got != "some-worker" {
		t.Errorf("an unregistered name became %q; workers keep tending unbound", got)
	}
	if got := d.resolveTenderMember("", "sess-keel"); got != "keel" {
		t.Errorf("a bound session resolved to %q, want keel", got)
	}
	addSession(t, d, "sess-worker")
	if got := d.resolveTenderMember("", "sess-worker"); got != "" {
		t.Errorf("an unbound session resolved to %q, want no member", got)
	}
}

func TestCrew_ExplicitUnregisteredMemberActsAsMember(t *testing.T) {
	d := newGardenDaemon(t)
	d.ensureCrewCollections()
	writeCrewHomes(t, d.dataRoot)
	d.importCrewHomes()

	seed := plant(t, d, protocol.SeedPlantMessage{Title: "work an unbound session picks up"})
	tended := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "some-worker")
	if tended.TenderMember != "some-worker" {
		t.Fatalf("tender member = %q, want the free string as typed", tended.TenderMember)
	}
	if tended.TenderSession != "" {
		t.Fatalf("tender session = %q, want explicit member to act instead", tended.TenderSession)
	}
	addGardenSession(t, d, "sess-b")
	resp := transition(t, d, "sess-b", seed.ID, garden.VerbTend, "", "")
	if resp.Ok {
		t.Fatal("another session took a seed held by an unbound tender")
	}
	if !strings.Contains(protocol.Deref(resp.Error), "Some-worker") {
		t.Fatalf("refusal did not name the unbound tender: %v", protocol.Deref(resp.Error))
	}
	move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "done", "some-worker")

	session := d.sessionForBroadcast(d.store.Get("sess-a"))
	if session.CrewMember != nil {
		t.Fatalf("an unbound session broadcasts a crew member: %q", *session.CrewMember)
	}
}

func TestCrew_AMemberHoldsItsSeedHoweverTheNameIsTyped(t *testing.T) {
	d := newGardenDaemon(t)
	d.ensureCrewCollections()
	writeCrewHomes(t, d.dataRoot)
	d.importCrewHomes()

	seed := plant(t, d, protocol.SeedPlantMessage{Title: "a member's work"})
	move(t, d, "", seed.ID, garden.VerbTend, "", "trellis")
	if resp := transition(t, d, "", seed.ID, garden.VerbTend, "", "Trellis"); !resp.Ok {
		t.Fatalf("a member could not re-tend its own seed under another spelling: %v", protocol.Deref(resp.Error))
	}
	if resp := transition(t, d, "", seed.ID, garden.VerbTend, "", "keel"); resp.Ok {
		t.Fatal("another member took a seed trellis holds")
	}
}
