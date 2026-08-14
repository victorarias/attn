package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

// newWakeableDaemon is a crew daemon that can spawn: a fake PTY backend records
// what a wake launched, and a real logger on disk carries the priming receipt.
func newWakeableDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, func() string) {
	t.Helper()
	d := newCrewDaemon(t)
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new test logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	d.logger = logger

	return d, backend, func() string {
		body, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read daemon log: %v", err)
		}
		return string(body)
	}
}

func crewSet(t *testing.T, d *Daemon, msg protocol.CrewSetMessage) protocol.Response {
	t.Helper()
	msg.Cmd = protocol.CmdCrewSet
	return gardenCall(t, func(c net.Conn) { d.handleCrewSet(c, &msg) })
}

// The slice's acceptance: one verb starts a member's day. The session launches
// in the member's own directory, bound to it before it spawns, and the roster
// says the member is awake.
func TestCrewWake_StartsADayBoundInTheMembersOwnDirectory(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	launchDir := t.TempDir()
	if resp := crewSet(t, d, protocol.CrewSetMessage{Member: "trellis", Cwd: protocol.Ptr(launchDir)}); !resp.Ok {
		t.Fatalf("crew set: %v", protocol.Deref(resp.Error))
	}

	result, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if result.Member != "trellis" || result.SessionID == "" {
		t.Fatalf("wake result = %+v, want trellis in a named session", result)
	}
	if result.AlreadyAwake {
		t.Fatal("a sleeping member reported as already awake")
	}

	backend.mu.Lock()
	spawns := append([]ptybackend.SpawnOptions(nil), backend.spawnOpts...)
	backend.mu.Unlock()
	if len(spawns) != 1 {
		t.Fatalf("wake spawned %d sessions, want 1", len(spawns))
	}
	if spawns[0].CWD != launchDir {
		t.Errorf("session launched in %q, want the member's own cwd %q", spawns[0].CWD, launchDir)
	}

	// Bound: the binding is what `crew_prime` answers, so a wake that spawned
	// without one would launch a session that is nobody.
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != result.SessionID {
		t.Fatalf("roster binding = %q, want the woken session %q", got, result.SessionID)
	}
	// One durable workspace per member, reused by every day.
	if d.store.GetWorkspace(crewWorkspaceID("trellis")) == nil {
		t.Fatalf("no workspace %q was created for the woken member", crewWorkspaceID("trellis"))
	}
	if result.WorkspaceID != crewWorkspaceID("trellis") {
		t.Errorf("wake result workspace = %q, want %q", result.WorkspaceID, crewWorkspaceID("trellis"))
	}
}

// Two agents with the same identity never run at once. The sidebar's one action
// must not fail exactly when the member is present, so a second wake names the
// live day instead of refusing — and launches nothing.
func TestCrewWake_AnAwakeMemberIsNotWokenTwice(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)

	first, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("first wake: %v", err)
	}
	second, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("second wake: %v", err)
	}
	if !second.AlreadyAwake {
		t.Fatal("a live member was woken a second time")
	}
	if second.SessionID != first.SessionID {
		t.Errorf("second wake named session %q, want the live day %q", second.SessionID, first.SessionID)
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 1 {
		t.Fatalf("%d sessions were spawned for one member, want 1", spawned)
	}
}

func TestCrewWake_AnUnknownMemberIsRefusedWithWhereToLook(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	_, err := d.crewWake("nobody", "")
	if err == nil {
		t.Fatal("a name nobody registered was woken")
	}
	if !strings.Contains(err.Error(), "attn crew list") {
		t.Errorf("refusal %q does not say where the roster is", err)
	}
}

// A recorded cwd that has since moved is named, with the verb that fixes it —
// and the member stays asleep rather than half-woken with a claimed binding.
func TestCrewWake_ADirectoryThatMovedIsNamedAndNothingIsClaimed(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	launchDir := filepath.Join(t.TempDir(), "moved")
	if err := os.Mkdir(launchDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if resp := crewSet(t, d, protocol.CrewSetMessage{Member: "alder", Cwd: protocol.Ptr(launchDir)}); !resp.Ok {
		t.Fatalf("crew set: %v", protocol.Deref(resp.Error))
	}
	if err := os.RemoveAll(launchDir); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err := d.crewWake("alder", "")
	if err == nil {
		t.Fatal("a member launched into a directory that is not there")
	}
	for _, want := range []string{"alder", launchDir, "attn crew set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 0 {
		t.Errorf("%d sessions spawned despite the refusal", spawned)
	}
	if binding := memberByID(t, crewList(t, d), "alder").BindingSession; binding != nil {
		t.Errorf("a failed wake left alder claimed by %q", *binding)
	}
}

// A member whose home records no cwd launches in its own home: the home is what
// made the member, so a wake never fails for want of a directory.
func TestCrewWake_AMemberWithNoRecordedDirectoryLaunchesInItsHome(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	if _, err := d.crewWake("keel", ""); err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	cwd := backend.spawnOpts[0].CWD
	backend.mu.Unlock()
	want := filepath.Join(d.dataRoot, crew.HomesDirName, "keel")
	if cwd != want {
		t.Fatalf("session launched in %q, want the member's home %q", cwd, want)
	}
}

// Priming is what makes the launched session the member. The block carries the
// charter and the freshest letter read off the home, and the size is logged —
// the budget receipt, one greppable line naming what each part cost.
func TestCrewPrime_ABoundSessionIsPrimedWithItsHomeAndTheSizeIsLogged(t *testing.T) {
	d, _, readLog := newWakeableDaemon(t)
	result, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	member, block, bound := d.crewPrimeForSession(result.SessionID)
	if !bound {
		t.Fatal("the session a wake just bound was primed as nobody")
	}
	if member.ID != "trellis" {
		t.Fatalf("primed as %q, want trellis", member.ID)
	}
	for _, want := range []string{
		"You are **trellis**",
		"What I care about.", // the charter on disk
		"Where I left off.",  // the freshest letter
		"2026-08-13T22-20Z-trellis.md",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the priming block does not carry %q", want)
		}
	}

	log := readLog()
	if !strings.Contains(log, "crew: priming trellis") {
		t.Fatalf("no greppable priming receipt in the daemon log:\n%s", log)
	}
	// The logged size is the size of what was injected, not an estimate of it.
	if !strings.Contains(log, fmt.Sprintf("%d bytes", len(block))) {
		t.Errorf("the receipt does not report the %d bytes that were injected:\n%s", len(block), log)
	}
}

// A session nobody woke as a member is primed with nothing: a worker in a pane
// never receives a crew identity it did not launch with.
func TestCrewPrime_AnUnboundSessionIsNobody(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	addSession(t, d, "sess-worker")
	if _, block, bound := d.crewPrimeForSession("sess-worker"); bound || block != "" {
		t.Fatalf("an unbound session was primed as somebody: %q", block)
	}
	if _, _, bound := d.crewPrimeForSession(""); bound {
		t.Fatal("a session with no id was primed")
	}
}

// `crew set` is where the launch directory and the awareness dirs come from —
// registry state, never read out of the member's prose. It has a way in and a
// way out: passing no awareness dirs clears them.
func TestCrewSet_RecordsAndClearsWhereAMemberWorks(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	launchDir, awareness := t.TempDir(), t.TempDir()

	resp := crewSet(t, d, protocol.CrewSetMessage{
		Member:        "keel",
		Cwd:           protocol.Ptr(launchDir),
		AwarenessDirs: []string{awareness},
	})
	if !resp.Ok {
		t.Fatalf("crew set: %v", protocol.Deref(resp.Error))
	}
	member := resp.CrewSetResult.Member
	if protocol.Deref(member.Cwd) != launchDir {
		t.Errorf("cwd = %q, want %q", protocol.Deref(member.Cwd), launchDir)
	}
	if len(member.AwarenessDirs) != 1 || member.AwarenessDirs[0] != awareness {
		t.Errorf("awareness dirs = %v, want [%s]", member.AwarenessDirs, awareness)
	}
	// It survives the round trip through the registry, which is what the wake
	// reads.
	if got := protocol.Deref(memberByID(t, crewList(t, d), "keel").Cwd); got != launchDir {
		t.Errorf("roster cwd = %q, want %q", got, launchDir)
	}

	// The way out.
	resp = crewSet(t, d, protocol.CrewSetMessage{Member: "keel", AwarenessDirs: []string{}})
	if !resp.Ok {
		t.Fatalf("clearing awareness dirs: %v", protocol.Deref(resp.Error))
	}
	if got := memberByID(t, crewList(t, d), "keel").AwarenessDirs; len(got) != 0 {
		t.Errorf("awareness dirs after the clear = %v, want none", got)
	}
	// Clearing one field leaves the other alone.
	if got := protocol.Deref(memberByID(t, crewList(t, d), "keel").Cwd); got != launchDir {
		t.Errorf("cwd = %q after clearing awareness dirs, want %q", got, launchDir)
	}
}

// The way out has to survive the wire. An empty awareness list marshals away
// under `omitempty`, so a clear sent as an empty slice reaches the daemon as
// "leave it alone" and the CLI reports success while nothing changed. This
// drives the same bytes the client sends, which is the only place that shows.
func TestCrewSet_ClearingAwarenessDirsSurvivesTheWire(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	awareness := t.TempDir()
	if resp := crewSetOverTheWire(t, d, "keel", nil, []string{awareness}); !resp.Ok {
		t.Fatalf("crew set: %v", protocol.Deref(resp.Error))
	}

	if resp := crewSetOverTheWire(t, d, "keel", nil, []string{}); !resp.Ok {
		t.Fatalf("clearing awareness dirs: %v", protocol.Deref(resp.Error))
	}
	if got := memberByID(t, crewList(t, d), "keel").AwarenessDirs; len(got) != 0 {
		t.Fatalf("awareness dirs after the clear = %v, want none", got)
	}
}

// crewSetOverTheWire builds the message the client builds, marshals it, and
// decodes it the way the daemon does — so a field the encoder drops is dropped
// here too.
func crewSetOverTheWire(t *testing.T, d *Daemon, member string, cwd *string, dirs []string) protocol.Response {
	t.Helper()
	msg := protocol.CrewSetMessage{Cmd: protocol.CmdCrewSet, Member: member, Cwd: cwd, AwarenessDirs: dirs}
	if dirs != nil && len(dirs) == 0 {
		msg.ClearAwarenessDirs = protocol.Ptr(true)
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal crew_set: %v", err)
	}
	_, decoded, err := protocol.ParseMessage(encoded)
	if err != nil {
		t.Fatalf("parse crew_set: %v", err)
	}
	return gardenCall(t, func(c net.Conn) { d.handleCrewSet(c, decoded.(*protocol.CrewSetMessage)) })
}

// A directory that is not there is refused at set time. Recording it and failing
// at the next wake is the wrong end of the day to learn about a typo.
func TestCrewSet_ADirectoryThatIsNotThereIsRefused(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	missing := filepath.Join(t.TempDir(), "nope")
	resp := crewSet(t, d, protocol.CrewSetMessage{Member: "keel", Cwd: protocol.Ptr(missing)})
	if resp.Ok {
		t.Fatal("a cwd that is not there was recorded")
	}
	if !strings.Contains(protocol.Deref(resp.Error), missing) {
		t.Errorf("refusal %q does not name the directory", protocol.Deref(resp.Error))
	}
}

// Every crew verb passes the fence. An outpost holds no part of the crew, so a
// wake, a set and a prime all refuse there by name.
func TestCrewWake_AnOutpostHoldsNoneOfIt(t *testing.T) {
	const home = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()

	_, err := d.crewWake("keel", "")
	if err == nil {
		t.Fatal("an outpost woke a crew member")
	}
	if !strings.Contains(err.Error(), home) {
		t.Errorf("wake refusal %q does not name the home", err)
	}

	resp := crewSet(t, d, protocol.CrewSetMessage{Member: "keel", Cwd: protocol.Ptr(t.TempDir())})
	if resp.Ok {
		t.Fatal("an outpost recorded crew state")
	}
	if !strings.Contains(protocol.Deref(resp.Error), home) {
		t.Errorf("set refusal %q does not name the home", protocol.Deref(resp.Error))
	}

	if _, _, bound := d.crewPrimeForSession("sess-anything"); bound {
		t.Fatal("an outpost primed a session as a crew member")
	}
}
