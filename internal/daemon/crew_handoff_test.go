package daemon

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func crewHandoffCall(t *testing.T, d *Daemon, sessionID, note string) protocol.Response {
	t.Helper()
	msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: sessionID, Note: note}
	return gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
}

func spawnedSessions(t *testing.T, backend *fakeSpawnBackend) []ptybackend.SpawnOptions {
	t.Helper()
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]ptybackend.SpawnOptions(nil), backend.spawnOpts...)
}

func handoffFiles(t *testing.T, d *Daemon, member string) []string {
	t.Helper()
	dir := filepath.Join(d.dataRoot, crew.HomesDirName, member, crew.HandoffsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	crew.SortHandoffNames(names)
	return names
}

// The slice's acceptance: a member files its letter and the day turns over in
// one motion. The letter lands on disk as written, a fresh session takes over
// the member's day, and the member is awake throughout.
func TestCrewHandoff_FilesTheLetterAndTurnsTheDayOver(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	letter := "Dear next trellis,\n\n#901 is waiting on review.\n"
	resp := crewHandoffCall(t, d, woken.SessionID, letter)
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewHandoffResult
	if result == nil {
		t.Fatal("the daemon answered without saying where the letter landed")
	}
	if napErr := protocol.Deref(result.NapError); napErr != "" {
		t.Fatalf("the nap did not run: %s", napErr)
	}

	body, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read the filed letter: %v", err)
	}
	if string(body) != letter {
		t.Fatalf("the letter on disk is %q; attn must file the member's prose untouched", string(body))
	}
	if names := handoffFiles(t, d, "trellis"); len(names) != 2 || names[0] != filepath.Base(result.Path) {
		t.Fatalf("the handoffs dir holds %v; the new letter must join the line as its freshest", names)
	}

	newSessionID := protocol.Deref(result.SessionID)
	if newSessionID == "" || newSessionID == woken.SessionID {
		t.Fatalf("the successor's session is %q; a nap is a fresh session, not the one that just ended", newSessionID)
	}
	if d.store.Get(woken.SessionID) != nil {
		t.Error("the day that handed off is still running")
	}
	if d.store.Get(newSessionID) == nil {
		t.Error("the successor's session was not created")
	}
	// Still awake, now living the new day: the sidebar must never show the
	// member asleep because it handed off.
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != newSessionID {
		t.Fatalf("roster binding = %q, want the successor %q", got, newSessionID)
	}

	spawns := spawnedSessions(t, backend)
	if len(spawns) != 2 {
		t.Fatalf("the nap spawned %d sessions in total, want 2 (the wake and the successor)", len(spawns))
	}
	successor := spawns[1]
	if successor.ID != newSessionID {
		t.Errorf("the second spawn is %s, want the successor %s", successor.ID, newSessionID)
	}
	// The whole point of the design: no transcript, no compaction summary — the
	// member's letter is the only thread into the new day.
	if successor.ResumeSessionID != "" {
		t.Errorf("the successor resumes %q; a nap must never carry the closed day's transcript", successor.ResumeSessionID)
	}
	if successor.InitialPromptFile == "" {
		t.Error("the successor launched with no prompt; a woken member must be asked to pick the thread up")
	}
}

// Ordering, asserted where it matters: at the instant the successor spawns, the
// registry already names it. A release-then-rebind would show the member
// unbound here, which is the gap another wake could claim.
func TestCrewHandoff_TheMemberIsNeverUnboundDuringTheNap(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	var atSpawn string
	backend.mu.Lock()
	backend.onSpawn = func(opts ptybackend.SpawnOptions) {
		if opts.ID == woken.SessionID {
			return
		}
		atSpawn = protocol.Deref(memberByID(t, crewList(t, d), "keel").BindingSession)
	}
	backend.mu.Unlock()

	resp := crewHandoffCall(t, d, woken.SessionID, "Filed and gone.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	successor := protocol.Deref(resp.CrewHandoffResult.SessionID)
	if atSpawn != successor {
		t.Fatalf("at the successor's spawn the binding was %q, want %q — the binding must move in one write, never release and re-claim", atSpawn, successor)
	}
}

// A session that is nobody has no day-line to close. The refusal names the
// other axis, because filing one where the other belongs is the mistake the
// guidance exists to prevent.
func TestCrewHandoff_ASessionThatIsNobodyHasNoDayLine(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	addSession(t, d, "errand-session")

	resp := crewHandoffCall(t, d, "errand-session", "I did some work today.")
	if resp.Ok {
		t.Fatal("an unbound session filed a member letter")
	}
	if !strings.Contains(protocol.Deref(resp.Error), "attn seed note") {
		t.Errorf("the refusal %q does not point at the seed axis", protocol.Deref(resp.Error))
	}
	if len(spawnedSessions(t, backend)) != 0 {
		t.Error("a refused handoff spawned something")
	}
	if d.store.Get("errand-session") == nil {
		t.Error("a refused handoff tore the session down")
	}
}

// Append-only, from the daemon's side: a name already taken is refused, and the
// refusal costs the member nothing — its day is still running and it is still
// bound to it, with its letter unfiled and still in its hands.
func TestCrewHandoff_ARefusedFilingLeavesTheDayRunning(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	// Take both candidate names, so a minute rolling over between here and the
	// call cannot make this pass by accident.
	dir := filepath.Join(d.dataRoot, crew.HomesDirName, "trellis", crew.HandoffsDirName)
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		taken := filepath.Join(dir, crew.HandoffFileName("trellis", at))
		if err := os.WriteFile(taken, []byte("somebody else's closure\n"), 0o644); err != nil {
			t.Fatalf("take %s: %v", taken, err)
		}
	}

	resp := crewHandoffCall(t, d, woken.SessionID, "My letter.")
	if resp.Ok {
		t.Fatal("a letter was filed over one already there")
	}
	if !strings.Contains(protocol.Deref(resp.Error), "never overwritten") {
		t.Errorf("the refusal %q does not say the line is append-only", protocol.Deref(resp.Error))
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("a member was torn down with its letter unfiled")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != woken.SessionID {
		t.Fatalf("roster binding = %q, want the still-running day %q", got, woken.SessionID)
	}
	if len(spawnedSessions(t, backend)) != 1 {
		t.Error("a refused filing woke a successor")
	}
}

// The letter is filed first and is never rolled back: a nap that cannot spawn
// is a day that did not start, not a letter that was not written. The member
// keeps the day it has, and the caller is told which half failed.
func TestCrewHandoff_ANapThatCannotSpawnKeepsTheLetterAndTheDay(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	backend.spawnErr = errors.New("no room to launch")
	backend.mu.Unlock()

	resp := crewHandoffCall(t, d, woken.SessionID, "Filed anyway.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewHandoffResult
	if napErr := protocol.Deref(result.NapError); !strings.Contains(napErr, "no room to launch") {
		t.Fatalf("nap_error = %q, want the spawn failure named", napErr)
	}
	if protocol.Deref(result.SessionID) != "" {
		t.Error("a failed nap reported a successor session")
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("the filed letter was rolled back: %v", err)
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("the day was closed with no successor to take it over")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "alder").BindingSession); got != woken.SessionID {
		t.Fatalf("roster binding = %q, want the day still running %q — a failed nap must give the binding back", got, woken.SessionID)
	}
}

// The successor is the same member running the same way. A member woken yolo
// with a pinned model must not come back attended and unpinned at its first
// nap — the launch intent of the day that closed is the authority.
func TestCrewHandoff_TheSuccessorCarriesTheClosedDaysLaunchParams(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	intent, ok := d.store.LaunchIntent(woken.SessionID)
	if !ok {
		t.Fatal("the woken day recorded no launch intent")
	}
	intent.YoloMode = true
	intent.ApprovalRoute = launchcontract.ApprovalRouteBypass
	intent.Model = "opus"
	intent.Effort = "high"
	d.store.SetLaunchIntent(woken.SessionID, intent)

	resp := crewHandoffCall(t, d, woken.SessionID, "Carry it forward.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	spawns := spawnedSessions(t, backend)
	successor := spawns[len(spawns)-1]
	if successor.ApprovalRoute != launchcontract.ApprovalRouteBypass || !successor.YoloMode {
		t.Errorf("the successor launched route=%q yolo=%t; a nap must not silently change how a member runs", successor.ApprovalRoute, successor.YoloMode)
	}
	if successor.Model != "opus" || successor.Effort != "high" {
		t.Errorf("the successor launched model=%q effort=%q, want the closed day's opus/high", successor.Model, successor.Effort)
	}
}

// The letter just filed is what primes the successor. This is the whole nap:
// the member's own words, and nothing else, thread the new day.
func TestCrewHandoff_TheSuccessorIsPrimedByTheLetterJustFiled(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	resp := crewHandoffCall(t, d, woken.SessionID, "The one thing to know: the fence lands first.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	successor := protocol.Deref(resp.CrewHandoffResult.SessionID)

	member, block, bound := d.crewPrimeForSession(successor)
	if !bound {
		t.Fatal("the successor is nobody")
	}
	if member.ID != "trellis" {
		t.Fatalf("the successor is primed as %s", member.ID)
	}
	if !strings.Contains(block, "The one thing to know: the fence lands first.") {
		t.Fatal("the successor's priming does not carry the letter that was just filed")
	}
	if !strings.Contains(block, filepath.Base(resp.CrewHandoffResult.Path)) {
		t.Error("the successor's priming does not name the letter it is reading")
	}
}

// Home-only, like every crew verb: an outpost holds no part of the crew, so it
// refuses by naming the home rather than filing into a home that is not its to
// write.
func TestCrewHandoff_AnOutpostHoldsNoneOfIt(t *testing.T) {
	const home = "d-cccccccccccccccccccccccccccccccc"
	d := newEnrolledDaemon(t, home)
	t.Cleanup(d.stopEventBus)
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	addSession(t, d, "sess-outpost")

	resp := crewHandoffCall(t, d, "sess-outpost", "Filed from an outpost.")
	if resp.Ok {
		t.Fatal("an outpost filed a crew letter")
	}
	if !strings.Contains(protocol.Deref(resp.Error), home) {
		t.Errorf("the refusal %q does not name the home", protocol.Deref(resp.Error))
	}
	if names := handoffFiles(t, d, "trellis"); len(names) != 1 {
		t.Fatalf("the handoffs dir holds %v; an outpost wrote into a home", names)
	}
}
