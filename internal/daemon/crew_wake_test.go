package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
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

// A member runs on the pinned model, and nothing in the registry can change
// that: a member on another model is wrong in a way only reading its prose
// catches, so the wake decides it rather than a per-member setting.
func TestCrewWake_AMemberWakesOnThePinnedModel(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	if _, err := d.crewWake("trellis", ""); err != nil {
		t.Fatalf("wake: %v", err)
	}

	backend.mu.Lock()
	model := backend.spawnOpts[0].Model
	backend.mu.Unlock()
	if model != crewWakeModel {
		t.Fatalf("member woke on model %q, want %q", model, crewWakeModel)
	}
}

// The pin names a Claude model, so a wake onto another harness takes that
// harness's own default rather than a model it cannot run.
func TestCrewWake_AnotherHarnessIsNotGivenTheClaudeModel(t *testing.T) {
	if pin := crewWakeModelPin("codex"); pin != nil {
		t.Fatalf("a codex wake was pinned to %q", *pin)
	}
	if pin := crewWakeModelPin("CLAUDE"); pin == nil || *pin != crewWakeModel {
		t.Fatalf("the default harness was not pinned: %v", pin)
	}
}

// Display capitalizes, identity does not. Everything a person reads off a woken
// member — its session label, its pane, its workspace — is written as a name,
// while the workspace id, the binding and the wire's member field stay the
// lowercase id.
func TestCrewWake_NamesTheDayAndKeepsTheIDLowercase(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)

	result, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	session := d.store.Get(result.SessionID)
	if session == nil {
		t.Fatalf("no session %q was stored", result.SessionID)
	}
	if session.Label != "Trellis" {
		t.Errorf("session label = %q, want Trellis", session.Label)
	}
	workspace := d.store.GetWorkspace(result.WorkspaceID)
	if workspace == nil {
		t.Fatalf("no workspace %q was created", result.WorkspaceID)
	}
	if workspace.Title != "Trellis" {
		t.Errorf("workspace title = %q, want Trellis", workspace.Title)
	}
	if result.WorkspaceID != "workspace-crew-trellis" {
		t.Errorf("workspace id = %q, want workspace-crew-trellis", result.WorkspaceID)
	}
	if result.Member != "trellis" {
		t.Errorf("wire member = %q, want the lowercase id", result.Member)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != result.SessionID {
		t.Errorf("roster binding = %q, want %q", got, result.SessionID)
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

type crewRuntimeBackend struct {
	*fakeSpawnBackend
	running map[string]bool
}

func (b *crewRuntimeBackend) Spawn(ctx context.Context, opts ptybackend.SpawnOptions) error {
	if err := b.fakeSpawnBackend.Spawn(ctx, opts); err != nil {
		return err
	}
	b.running[opts.ID] = true
	return nil
}

func (b *crewRuntimeBackend) SessionInfo(_ context.Context, sessionID string) (ptybackend.SessionInfo, error) {
	running, ok := b.running[sessionID]
	if !ok {
		return ptybackend.SessionInfo{}, pty.ErrSessionNotFound
	}
	return ptybackend.SessionInfo{SessionID: sessionID, Running: running}, nil
}

// A session row is history, not liveness. Wake probes the bound runtime; when
// the process exited it releases that exact binding, starts a fresh day, and
// names the repair in the result.
func TestCrewWake_ReleasesAnExitedBindingAndStartsAFreshDay(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
	d.ptyBackend = runtime

	first, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("first wake: %v", err)
	}
	runtime.running[first.SessionID] = false

	second, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake after exit: %v", err)
	}
	if second.AlreadyAwake || second.SessionID == first.SessionID {
		t.Fatalf("wake result = %+v, want a fresh day", second)
	}
	if got := protocol.Deref(second.ReleasedSessionID); got != first.SessionID {
		t.Fatalf("released_session_id = %q, want exited day %q", got, first.SessionID)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != second.SessionID {
		t.Fatalf("roster binding = %q, want fresh day %q", got, second.SessionID)
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 2 {
		t.Fatalf("wake spawned %d sessions, want the original and replacement", spawned)
	}
}

// PTY exit is the runtime seam: the generic session row may remain for history
// or recovery, but the member is asleep as soon as its process is gone.
func TestCrewBinding_ProcessExitReleasesTheDay(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	if !d.handlePTYExit(ptybackend.ExitInfo{ID: woken.SessionID, ExitCode: 1}) {
		t.Fatal("process exit was suppressed")
	}
	if binding := memberByID(t, crewList(t, d), "alder").BindingSession; binding != nil {
		t.Fatalf("exited day still holds binding %q", *binding)
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("the generic session row was removed; crew release should not erase history")
	}

	replacement, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake after process exit: %v", err)
	}
	if got := protocol.Deref(replacement.ReleasedSessionID); got != woken.SessionID {
		t.Fatalf("wake named released session %q, want %q", got, woken.SessionID)
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 2 {
		t.Fatalf("process-exit wake spawned %d days, want original and replacement", spawned)
	}
}

// Startup may keep a dead generic session as recoverable, but it never keeps
// the member's seat: the letter and home, not the process row, carry the crew.
func TestCrewBinding_StartupRecoveryReleasesADeadDay(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	home := newRecoveryHome(t)
	home.resumableClaude(t, "native-keel")
	giveRestorationEvidence(t, d, woken.SessionID, "native-keel")

	if removed := d.pruneSessionsWithoutPTY(time.Time{}); removed != 0 {
		t.Fatalf("startup removed %d sessions, want the resumable row kept", removed)
	}
	if session := d.store.Get(woken.SessionID); session == nil || session.State != protocol.SessionStateRecoverable {
		t.Fatalf("session = %+v, want generic row recoverable", session)
	}
	if binding := memberByID(t, crewList(t, d), "keel").BindingSession; binding != nil {
		t.Fatalf("recoverable corpse still holds binding %q", *binding)
	}
	replacement, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake after startup release: %v", err)
	}
	if got := protocol.Deref(replacement.ReleasedSessionID); got != woken.SessionID {
		t.Fatalf("wake named released session %q, want %q", got, woken.SessionID)
	}
}

// The binding alone is not a liveness fence: the claimed session becomes
// visible only later in the spawn pipeline. A second wake that enters during
// that gap must wait, then resolve to the first day instead of stealing the
// member and launching a second identity.
func TestCrewWake_ConcurrentWakesShareTheFirstDay(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	started := make(chan string, 2)
	firstClaimed := make(chan struct{})
	releaseFirst := make(chan struct{})
	var claims atomic.Int32
	d.crewWakeStartHook = func(memberID string) { started <- memberID }
	d.crewWakeAfterClaimHook = func(_, _ string) {
		if claims.Add(1) == 1 {
			close(firstClaimed)
			<-releaseFirst
		}
	}

	type outcome struct {
		result *protocol.CrewWakeResult
		err    error
	}
	results := make(chan outcome, 2)
	wake := func() {
		result, err := d.crewWake("keel", "")
		results <- outcome{result: result, err: err}
	}
	go wake()
	if member := <-started; member != "keel" {
		t.Fatalf("first wake started for %q", member)
	}
	<-firstClaimed
	go wake()
	if member := <-started; member != "keel" {
		t.Fatalf("second wake started for %q", member)
	}
	close(releaseFirst)

	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("wake errors = %v, %v", first.err, second.err)
	}
	if first.result.SessionID != second.result.SessionID {
		t.Fatalf("concurrent wakes launched sessions %q and %q", first.result.SessionID, second.result.SessionID)
	}
	if first.result.AlreadyAwake == second.result.AlreadyAwake {
		t.Fatalf("results = %+v and %+v, want one launch and one live-day resolution", first.result, second.result)
	}
	if got := claims.Load(); got != 1 {
		t.Fatalf("the member was claimed %d times, want one", got)
	}
	backend.mu.Lock()
	spawned := len(backend.spawnOpts)
	backend.mu.Unlock()
	if spawned != 1 {
		t.Fatalf("concurrent wakes spawned %d sessions, want one", spawned)
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
	// The prose names Alder; the command it hands back takes the id.
	for _, want := range []string{"Alder launches in", launchDir, "attn crew set alder --cwd"} {
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
		"You are **Trellis**",
		"Where I left off.", // the freshest letter
		"2026-08-13T22-20Z-trellis.md",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the priming block does not carry %q", want)
		}
	}

	log := readLog()
	if !strings.Contains(log, "crew: priming Trellis") {
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
