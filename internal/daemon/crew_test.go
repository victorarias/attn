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

// writeCrewHomes builds a copy of the real `~/.attn/crew` shape under a
// daemon's data dir: three members, each with a charter and dated handoff
// files, plus the loose CREW.md beside them. Copied by shape — the live
// directory is never read.
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

// newCrewDaemon returns a home daemon whose roster was imported from homes on
// disk, which is how every real daemon starts.
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

// addSession puts a live session in the store, the thing a binding names.
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

// The slice's acceptance: homes on disk become a roster, a session launches
// bound, the binding is what `agent list` and `peek` show, and it is released
// when the day ends.
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
	// Files stay canonical: the registry points at the home rather than copying it.
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

	// `agent list` and `agent peek` both say who this session is today.
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

	// The day ends: the binding is released and the member is asleep again.
	d.forgetSession("sess-trellis")
	if binding := memberByID(t, crewList(t, d), "trellis").BindingSession; binding != nil {
		t.Fatalf("the binding survived its session: %v", *binding)
	}
}

// Two agents with the same identity never run at once. The refusal names the
// member and the session holding it, so the caller can go look.
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
	// The first session keeps its identity: a refused claim writes nothing.
	if got := protocol.Deref(memberByID(t, crewList(t, d), "keel").BindingSession); got != "sess-first" {
		t.Fatalf("binding after the refusal = %q, want sess-first", got)
	}
}

// One session answers to one name. Binding a session that already holds a
// member to a second member moves the identity rather than handing that session
// both — otherwise "two agents with the same identity never run at once" holds
// while one agent quietly runs as two.
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
	// The session decorates as exactly one member, not whichever the roster
	// order happened to reach first.
	if got := d.crewMembersBySession()["sess-one"]; got != "trellis" {
		t.Errorf("session decorates as %q, want trellis", got)
	}
}

// A refused second name leaves the session the member it already was: the
// release runs only once the claim is certain to land.
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

// A binding naming a session the daemon no longer knows has let go on its own —
// the same liveness rule the garden's tender uses — so a member whose day ended
// without ceremony can be woken again.
func TestCrew_ABindingWhoseSessionIsGoneDoesNotHold(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-crashed")
	if _, err := d.claimCrewBinding("alder", "sess-crashed"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// The session vanishes without any release path running (a crash, a reap
	// the daemon never saw).
	d.store.Remove("sess-crashed")

	if binding := memberByID(t, crewList(t, d), "alder").BindingSession; binding != nil {
		t.Fatalf("the roster reports a dead session as awake: %v", *binding)
	}
	addSession(t, d, "sess-fresh")
	if _, err := d.claimCrewBinding("alder", "sess-fresh"); err != nil {
		t.Fatalf("a member whose session died could not be woken: %v", err)
	}
}

// Re-claiming the binding a session already holds is idempotent: a client
// re-announcing a live session must not lose its own identity to itself.
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

// Importing is create-only: a home rescanned at every startup never overwrites
// the registry record, which is where a live binding lives.
func TestCrew_ReimportingHomesLeavesLiveRecordsAlone(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "sess-keel")
	if _, err := d.claimCrewBinding("keel", "sess-keel"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	d.importCrewHomes() // the next daemon start

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

// A home the user adds by hand joins the roster at the next start; nothing has
// to be registered through attn.
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

// The crew has exactly one owner. An outpost holds no part of it: the read
// refuses by name, and startup imports nothing.
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

// The garden touches the registry lightly. A member's free-string name becomes
// its registry id so Tender.Is compares real addresses — and a name nobody
// registered passes through, because tending never required a registry.
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
	// A bound session that named nobody IS its member — the invocation already
	// said who it is.
	if got := d.resolveTenderMember("", "sess-keel"); got != "keel" {
		t.Errorf("a bound session resolved to %q, want keel", got)
	}
	// An unbound session that named nobody stays nobody: a worker in a pane is
	// not silently given an identity.
	addSession(t, d, "sess-worker")
	if got := d.resolveTenderMember("", "sess-worker"); got != "" {
		t.Errorf("an unbound session resolved to %q, want no member", got)
	}
}

// The slice's safety property: nothing changes for a session with no binding.
// A worker plants, tends and hands off exactly as it did before the registry
// existed, and its broadcast carries no crew field at all.
func TestCrew_UnboundSessionsBehaveExactlyAsBefore(t *testing.T) {
	d := newGardenDaemon(t)
	d.ensureCrewCollections()
	writeCrewHomes(t, d.dataRoot)
	d.importCrewHomes()

	seed := plant(t, d, protocol.SeedPlantMessage{Title: "work an unbound session picks up"})
	// A free-string member nobody registered is stored as typed.
	tended := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "some-worker")
	if tended.TenderMember != "some-worker" {
		t.Fatalf("tender member = %q, want the free string as typed", tended.TenderMember)
	}
	if tended.TenderSession != "sess-a" {
		t.Fatalf("tender session = %q, want sess-a", tended.TenderSession)
	}
	// The claim still holds against another session: an unconfirmed take is
	// refused by name, unchanged for tenders the registry knows nothing about.
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

// A registered member tending under a differently-typed name still holds its
// own seed: resolution is what makes the claim an address rather than a
// spelling.
func TestCrew_AMemberHoldsItsSeedHoweverTheNameIsTyped(t *testing.T) {
	d := newGardenDaemon(t)
	d.ensureCrewCollections()
	writeCrewHomes(t, d.dataRoot)
	d.importCrewHomes()

	seed := plant(t, d, protocol.SeedPlantMessage{Title: "a member's work"})
	// Tended from a terminal pane, which carries no session id — so the member
	// name is the whole identity, and its spelling is what decides.
	move(t, d, "", seed.ID, garden.VerbTend, "", "trellis")
	if resp := transition(t, d, "", seed.ID, garden.VerbTend, "", "Trellis"); !resp.Ok {
		t.Fatalf("a member could not re-tend its own seed under another spelling: %v", protocol.Deref(resp.Error))
	}
	// Somebody else is still refused: resolution widens who counts as the same
	// person, never who may take the claim.
	if resp := transition(t, d, "", seed.ID, garden.VerbTend, "", "keel"); resp.Ok {
		t.Fatal("another member took a seed trellis holds")
	}
}
