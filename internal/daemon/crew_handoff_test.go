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

func crewHandoffRetryCall(t *testing.T, d *Daemon, sessionID string) protocol.Response {
	t.Helper()
	msg := protocol.CrewHandoffMessage{Cmd: protocol.CmdCrewHandoff, SessionID: sessionID, Retry: protocol.Ptr(true)}
	return gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
}

// A retry is retrying a turnover, so an absence must not answer a different
// question than the one it asked: the member ran --retry to get the successor
// its letter was written for, and sleeping instead would quietly change what
// the verb means. Saying so is one word away, and it is the member's word.
func TestCrewHandoff_ARetryTurnsTheDayOverEvenWithTheUserAway(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	backend.spawnErr = errors.New("no room to launch")
	backend.mu.Unlock()

	first := crewHandoffCall(t, d, woken.SessionID, "The letter, written once.")
	if !first.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(first.Error))
	}
	if protocol.Deref(first.CrewHandoffResult.NapError) == "" {
		t.Fatal("the nap was supposed to fail")
	}

	backend.mu.Lock()
	backend.spawnErr = nil
	backend.mu.Unlock()
	setUserAway(d, time.Now().Add(-3*time.Hour))

	retried := crewHandoffRetryCall(t, d, woken.SessionID)
	if !retried.Ok {
		t.Fatalf("retry: %v", protocol.Deref(retried.Error))
	}
	result := retried.CrewHandoffResult
	if got := protocol.Deref(result.Outcome); got != protocol.CrewDayCloseNap {
		t.Fatalf("outcome = %q, want the turnover the retry asked for", got)
	}
	if successor := protocol.Deref(result.SessionID); successor == "" || successor == woken.SessionID {
		t.Fatalf("the successor's session is %q; the retry must start the next day", successor)
	}
}

// The member can still end the day on a retry — it just has to say so.
func TestCrewHandoff_ARetryCanBeToldToSleepInstead(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	backend.spawnErr = errors.New("no room to launch")
	backend.mu.Unlock()

	if first := crewHandoffCall(t, d, woken.SessionID, "The letter, written once."); !first.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(first.Error))
	}
	backend.mu.Lock()
	backend.spawnErr = nil
	backend.mu.Unlock()

	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Retry: protocol.Ptr(true), Close: protocol.Ptr(protocol.CrewDayCloseSleep),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if !resp.Ok {
		t.Fatalf("retry --sleep: %v", protocol.Deref(resp.Error))
	}
	if got := protocol.Deref(resp.CrewHandoffResult.Outcome); got != protocol.CrewDayCloseSleep {
		t.Fatalf("outcome = %q, want sleep", got)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "alder").BindingSession); got != "" {
		t.Fatalf("the member is still bound to %q after being told to sleep", got)
	}
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

func TestCrewHandoff_RefusesASymlinkedHandoffsDirectory(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	handoffsDir := filepath.Join(d.dataRoot, crew.HomesDirName, "keel", crew.HandoffsDirName)
	if err := os.RemoveAll(handoffsDir); err != nil {
		t.Fatalf("remove fixture handoffs: %v", err)
	}
	foreign := t.TempDir()
	if err := os.Symlink(foreign, handoffsDir); err != nil {
		t.Fatalf("symlink handoffs: %v", err)
	}

	resp := crewHandoffCall(t, d, woken.SessionID, "This must stay in my profile.")
	if resp.Ok {
		t.Fatal("a handoff was filed through a symlink leaving the member home")
	}
	refusal := protocol.Deref(resp.Error)
	for _, want := range []string{handoffsDir, filepath.Join(d.dataRoot, crew.HomesDirName, "keel"), "symlink"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal %q does not name %q", refusal, want)
		}
	}
	if entries, err := os.ReadDir(foreign); err != nil || len(entries) != 0 {
		t.Fatalf("foreign handoff target was written: entries=%v err=%v", entries, err)
	}
	if len(spawnedSessions(t, backend)) != 1 {
		t.Fatal("a refused filing spawned a successor")
	}
}

func TestCrewPriming_RefusesASymlinkedLetter(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	members, _, err := d.readCrewMembers()
	if err != nil {
		t.Fatalf("read roster: %v", err)
	}
	member, ok := crew.Resolve("keel", members)
	if !ok {
		t.Fatal("keel is missing from the fixture roster")
	}
	handoffsDir := filepath.Join(member.HomeDir, crew.HandoffsDirName)
	if err := os.RemoveAll(handoffsDir); err != nil {
		t.Fatalf("remove fixture handoffs: %v", err)
	}
	if err := os.MkdirAll(handoffsDir, 0o755); err != nil {
		t.Fatalf("recreate handoffs: %v", err)
	}
	foreignLetter := filepath.Join(t.TempDir(), "foreign-letter.md")
	if err := os.WriteFile(foreignLetter, []byte("foreign words\n"), 0o644); err != nil {
		t.Fatalf("write foreign letter: %v", err)
	}
	linkedLetter := filepath.Join(handoffsDir, "2099-01-01T00-00Z-keel.md")
	if err := os.Symlink(foreignLetter, linkedLetter); err != nil {
		t.Fatalf("symlink letter: %v", err)
	}
	newerLetter := filepath.Join(handoffsDir, "2100-01-01T00-00Z-keel.md")
	if err := os.WriteFile(newerLetter, []byte("newer local words\n"), 0o644); err != nil {
		t.Fatalf("write newer local letter: %v", err)
	}

	if _, err := d.crewPriming(member); err == nil {
		t.Fatal("priming read a symlinked letter outside the member home")
	} else {
		for _, want := range []string{linkedLetter, handoffsDir, "symlink"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not name %q", err, want)
			}
		}
	}
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

// The successor wakes on the pinned model too. The nap otherwise inherits the
// closed day's launch intent, so a member that once started on another model
// would carry it forever without this.
func TestCrewHandoff_TheSuccessorWakesOnThePinnedModel(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	resp := crewHandoffCall(t, d, woken.SessionID, "Filed and gone.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	spawns := spawnedSessions(t, backend)
	if len(spawns) != 2 {
		t.Fatalf("the nap spawned %d sessions in total, want 2", len(spawns))
	}
	if spawns[1].Model != crewWakeModel {
		t.Fatalf("the successor woke on model %q, want %q", spawns[1].Model, crewWakeModel)
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

// The way out of a nap that failed behind a filed letter. The line is
// append-only, so the letter cannot be written twice: a retry runs the turnover
// again against the one already on disk, and files nothing.
func TestCrewHandoff_ARetryTurnsTheDayOverWithTheLetterAlreadyFiled(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	backend.spawnErr = errors.New("no room to launch")
	backend.mu.Unlock()

	first := crewHandoffCall(t, d, woken.SessionID, "The letter, written once.")
	if !first.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(first.Error))
	}
	filed := first.CrewHandoffResult.Path
	if protocol.Deref(first.CrewHandoffResult.NapError) == "" {
		t.Fatal("the nap was supposed to fail")
	}
	before := handoffFiles(t, d, "alder")

	backend.mu.Lock()
	backend.spawnErr = nil
	backend.mu.Unlock()

	retried := crewHandoffRetryCall(t, d, woken.SessionID)
	if !retried.Ok {
		t.Fatalf("retry: %v", protocol.Deref(retried.Error))
	}
	result := retried.CrewHandoffResult
	if napErr := protocol.Deref(result.NapError); napErr != "" {
		t.Fatalf("the retried nap did not run: %s", napErr)
	}
	if result.Path != filed {
		t.Errorf("the retry reports %s, want the letter already filed at %s", result.Path, filed)
	}
	if after := handoffFiles(t, d, "alder"); len(after) != len(before) {
		t.Fatalf("the handoffs dir went from %v to %v; a retry writes no second letter", before, after)
	}
	successor := protocol.Deref(result.SessionID)
	if successor == "" || successor == woken.SessionID {
		t.Fatalf("the successor's session is %q; the retry must start the next day", successor)
	}
	if d.store.Get(woken.SessionID) != nil {
		t.Error("the day that handed off is still running after the retry")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "alder").BindingSession); got != successor {
		t.Fatalf("roster binding = %q, want the successor %q", got, successor)
	}
	// The letter the member actually wrote is what threads the new day — the
	// retry must not have primed the successor off some other file.
	_, block, bound, err := d.crewPrimeForSession(successor)
	if err != nil {
		t.Fatalf("prime successor: %v", err)
	}
	if !bound || !strings.Contains(block, "The letter, written once.") {
		t.Error("the successor was not primed by the letter that was already filed")
	}
}

// A retry and a correction share a symptom — the name this letter would take is
// taken — and must never share an exit. When this day is the one that took it,
// the refusal names the retry; nothing is filed either way.
func TestCrewHandoff_AFilingCollisionAfterAFailedNapNamesTheRetry(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	backend.mu.Lock()
	backend.spawnErr = errors.New("no room to launch")
	backend.mu.Unlock()

	first := crewHandoffCall(t, d, woken.SessionID, "The letter, written once.")
	if !first.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(first.Error))
	}
	// Take the next minute's name too, so a minute rolling over between the two
	// calls cannot let the second one through.
	dir := filepath.Join(d.dataRoot, crew.HomesDirName, "alder", crew.HandoffsDirName)
	next := filepath.Join(dir, crew.HandoffFileName("alder", time.Now().Add(time.Minute)))
	if err := os.WriteFile(next, []byte("taken\n"), 0o644); err != nil {
		t.Fatalf("take %s: %v", next, err)
	}
	before := handoffFiles(t, d, "alder")

	second := crewHandoffCall(t, d, woken.SessionID, "The letter, written twice.")
	if second.Ok {
		t.Fatal("a second letter was filed over the one this day already wrote")
	}
	refusal := protocol.Deref(second.Error)
	if !strings.Contains(refusal, "--retry") {
		t.Errorf("the refusal %q does not name the retry path", refusal)
	}
	if !strings.Contains(refusal, first.CrewHandoffResult.Path) {
		t.Errorf("the refusal %q does not name the letter already filed", refusal)
	}
	if after := handoffFiles(t, d, "alder"); len(after) != len(before) {
		t.Fatalf("the handoffs dir went from %v to %v; a refused filing writes nothing", before, after)
	}
}

// The other half of the pair: a retry with nothing to retry says so, and points
// at the verb that writes a letter. A member must never be told "already filed"
// when nothing is.
func TestCrewHandoff_ARetryWithNoFiledLetterSaysToWriteOne(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("keel", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	resp := crewHandoffRetryCall(t, d, woken.SessionID)
	if resp.Ok {
		t.Fatal("a day with no letter turned over on a retry")
	}
	if refusal := protocol.Deref(resp.Error); !strings.Contains(refusal, `attn handoff -m`) {
		t.Errorf("the refusal %q does not name the verb that writes a letter", refusal)
	}
	if len(spawnedSessions(t, backend)) != 1 {
		t.Error("a refused retry woke a successor")
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Error("a refused retry tore the day down")
	}
}

// A letter belongs to the day that wrote it. Once the turnover succeeds, the
// new day has written nothing — so its retry is refused rather than turning the
// day over a second time off its predecessor's letter.
func TestCrewHandoff_ANewDayInheritsNoLetterToRetry(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	first := crewHandoffCall(t, d, woken.SessionID, "Filed and gone.")
	if !first.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(first.Error))
	}
	successor := protocol.Deref(first.CrewHandoffResult.SessionID)

	resp := crewHandoffRetryCall(t, d, successor)
	if resp.Ok {
		t.Fatal("a fresh day turned over on its predecessor's letter")
	}
	if refusal := protocol.Deref(resp.Error); !strings.Contains(refusal, "filed no letter yet") {
		t.Errorf("the refusal %q does not say this day has written nothing", refusal)
	}
}

// The successor is the same member running the same way. A member woken yolo
// must not come back attended at its first nap — the launch intent of the day
// that closed is the authority for everything but the model, which is pinned.
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
	if successor.Effort != "high" {
		t.Errorf("the successor launched effort=%q, want the closed day's high", successor.Effort)
	}
	// The one thing a nap does not inherit: an intent naming another model is
	// overruled, so no member drifts onto one nap by nap.
	if successor.Model != crewWakeModel {
		t.Errorf("the successor launched model=%q, want the pinned %q", successor.Model, crewWakeModel)
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

	member, block, bound, err := d.crewPrimeForSession(successor)
	if err != nil {
		t.Fatalf("prime successor: %v", err)
	}
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

// The nap replaces the session rather than reloading in place, so it is
// agent-agnostic by construction — this pins that: a codex member's successor
// comes back on codex, primed by the letter the day just filed, and is not
// handed the Claude model pin on the way.
func TestCrewHandoff_ACodexMembersSuccessorComesBackOnCodex(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	if resp := crewSet(t, d, protocol.CrewSetMessage{Member: "trellis", Agent: protocol.Ptr("codex")}); !resp.Ok {
		t.Fatalf("crew set: %v", protocol.Deref(resp.Error))
	}
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	resp := crewHandoffCall(t, d, woken.SessionID, "Codex signing off: the fence lands first.")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	spawns := spawnedSessions(t, backend)
	successor := spawns[len(spawns)-1]
	if successor.Agent != "codex" {
		t.Errorf("the successor launched on %q, want codex", successor.Agent)
	}
	if successor.Model != "" {
		t.Errorf("the codex successor was pinned to model %q, want the harness default", successor.Model)
	}

	_, block, bound, err := d.crewPrimeForSession(protocol.Deref(resp.CrewHandoffResult.SessionID))
	if err != nil || !bound {
		t.Fatalf("prime successor: bound=%t err=%v", bound, err)
	}
	if !strings.Contains(block, "Codex signing off: the fence lands first.") {
		t.Fatal("the codex successor's priming does not carry the letter just filed")
	}
}
