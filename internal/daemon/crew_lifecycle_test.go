package daemon

import (
	"strings"
	"sync"
	"testing"
	"time"

	"net"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

// The tick takes `now` as an argument rather than reading the clock, so every
// timing question below — a cache 58 minutes old, a user three hours gone — is
// asked exactly, with no bubble and no sleeps. What is left of real time is the
// doorbell's paste-then-Enter gap, which these tests shrink.

// doorbellRecorder collects what a nudge typed into a session. The paste and
// its Enter arrive as two writes; only the first carries the prompt.
type doorbellRecorder struct {
	mu     sync.Mutex
	writes []string
}

// The prompt promises that an unattended day ends without buying a successor.
// The explicit flag makes that promise independent of presence when the letter
// finally lands.
func TestCrewSleepPrompt_ForcesThePromisedSleep(t *testing.T) {
	for _, want := range []string{
		"`attn handoff --sleep",
		"nobody wakes behind it",
		"not be woken again until the user asks",
	} {
		if !strings.Contains(crewSleepPrompt, want) {
			t.Errorf("sleep prompt does not carry %q", want)
		}
	}
}

func (r *doorbellRecorder) prompts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.writes))
	for _, write := range r.writes {
		if !strings.HasPrefix(write, bracketedPasteStart) {
			continue
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(write, bracketedPasteStart), bracketedPasteEnd))
	}
	return out
}

// newLifecycleDaemon is an awake member on a fast doorbell, with every prompt
// the tick delivers recorded.
func newLifecycleDaemon(t *testing.T) (*Daemon, string, *doorbellRecorder) {
	t.Helper()
	previous := doorbellSubmitDelay
	doorbellSubmitDelay = time.Millisecond
	t.Cleanup(func() { doorbellSubmitDelay = previous })

	d, backend, _ := newWakeableDaemon(t)
	recorder := &doorbellRecorder{}
	backend.onInput = func(_ string, data []byte) {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		recorder.writes = append(recorder.writes, string(data))
	}
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	return d, woken.SessionID, recorder
}

// setSessionActivity backdates when a session last talked to the model, which
// is what the cache estimate is measured from.
func setSessionActivity(t *testing.T, d *Daemon, sessionID string, state protocol.SessionState, at time.Time) {
	t.Helper()
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("no session %s in the store", sessionID)
	}
	// Get hands back a copy, so this is a re-add rather than a mutation of
	// store state.
	session.State = state
	session.StateSince = string(protocol.NewTimestamp(at))
	session.StateUpdatedAt = string(protocol.NewTimestamp(at))
	d.store.Add(session)
}

// crewMemberRecord reads a member's registry row. The wake ledger is stored,
// not broadcast: it bounds the daemon's own behaviour and the sidebar has
// nothing to do with it.
func crewMemberRecord(t *testing.T, d *Daemon, id string) crew.Member {
	t.Helper()
	members, _, err := d.readCrewMembers()
	if err != nil {
		t.Fatalf("read the roster: %v", err)
	}
	for _, member := range members {
		if member.ID == id {
			return member
		}
	}
	t.Fatalf("no member %q in the registry", id)
	return crew.Member{}
}

// setUserAway backdates the last moment anybody was present.
func setUserAway(d *Daemon, since time.Time) {
	d.presenceMu.Lock()
	defer d.presenceMu.Unlock()
	d.presentSince = since
}

// The acceptance the whole subsystem is judged by: on a quiet attended session
// it is silent. Not "rate-limited", not "cheap" — it sends nothing at all, so a
// member sitting idle beside a working user costs a roster read a minute.
func TestCrewLifecycleTick_IsSilentOnAQuietAttendedSession(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-10*time.Minute))

	// Forty ticks, a minute apart, on a session nobody touches — the whole span
	// in which the cache stays comfortably inside its assumed hour. Pressure is
	// what makes this subsystem act, so with none there is nothing to rate-limit
	// and nothing to send.
	for i := 0; i < 40; i++ {
		d.crewLifecycleTick(now.Add(time.Duration(i) * time.Minute))
	}
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("a quiet attended session was sent %d prompts: %q", len(got), got)
	}
}

func TestCrewLifecycleTick_WarmsAContextThatIsAboutToLapse(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	// Claude's assumed hour, 58 minutes in: inside the five-minute lead.
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))

	d.crewLifecycleTick(now)

	prompts := recorder.prompts()
	if len(prompts) != 1 || prompts[0] != crewHeartbeatPrompt {
		t.Fatalf("the tick sent %q, want one heartbeat", prompts)
	}

	// The condition holds until the member answers, so the next tick must not
	// send a second copy of the same nudge.
	d.crewLifecycleTick(now.Add(time.Minute))
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("the tick sent %d prompts a minute later; an unanswered nudge must not repeat every tick", len(got))
	}
}

// The other half of the acceptance: the user is gone, the cache is about to
// lapse, and the day should end rather than be kept warm for nobody.
func TestCrewLifecycleTick_AsksForTheHandoffWhenTheUserIsGone(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))
	setUserAway(d, now.Add(-3*time.Hour))

	d.crewLifecycleTick(now)

	prompts := recorder.prompts()
	if len(prompts) != 1 || prompts[0] != crewSleepPrompt {
		t.Fatalf("the tick sent %q, want the handoff ask", prompts)
	}
	// A member mid-thought may take minutes to close; re-asking every tick would
	// bury the first ask under copies of itself.
	d.crewLifecycleTick(now.Add(2 * time.Minute))
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("the handoff was asked for %d times inside the grace", len(got))
	}
}

// A prompt typed at a session mid-turn or waiting on an approval queues behind
// work nobody asked to interrupt, so neither half acts on one. `working` is
// here rather than only in the doorbell's rule: an in-flight question selector
// reads as `working`, and a paste into it answers the selector.
func TestCrewLifecycleTick_LeavesAnUnreachableSessionAlone(t *testing.T) {
	for _, state := range []protocol.SessionState{
		protocol.SessionStatePendingApproval,
		protocol.SessionStateWorking,
	} {
		t.Run(string(state), func(t *testing.T) {
			d, sessionID, recorder := newLifecycleDaemon(t)
			now := time.Now()
			setSessionActivity(t, d, sessionID, state, now.Add(-2*time.Hour))

			// Both halves: the user here, then the user gone.
			d.crewLifecycleTick(now)
			setUserAway(d, now.Add(-3*time.Hour))
			d.crewLifecycleTick(now.Add(time.Minute))

			if got := recorder.prompts(); len(got) != 0 {
				t.Fatalf("a session in %s was sent %q", state, got)
			}
		})
	}
}

// The witness for the split: a member holding a question for the user takes the
// handoff ask, because ending the day is an answer to what it is waiting for —
// and never takes a heartbeat, which would answer that question with filler.
func TestCrewLifecycleTick_WillEndAWaitingDayButNeverWarmOne(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWaitingInput, now.Add(-58*time.Minute))

	d.crewLifecycleTick(now)
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("a member holding a question for the user was sent %q", got)
	}

	setUserAway(d, now.Add(-3*time.Hour))
	d.crewLifecycleTick(now.Add(time.Minute))
	prompts := recorder.prompts()
	if len(prompts) != 1 || prompts[0] != crewSleepPrompt {
		t.Fatalf("the tick sent %q, want the handoff ask", prompts)
	}
}

// Either switch off means that half does nothing at all — never that the other
// half covers for it.
func TestCrewLifecycleTick_HonoursItsSwitches(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-58*time.Minute))

	d.store.SetSetting(SettingCrewHeartbeatEnabled, "false")
	d.crewLifecycleTick(now)
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("heartbeats are off and the tick sent %q", got)
	}

	d.store.SetSetting(SettingCrewAutoSleepEnabled, "false")
	setUserAway(d, now.Add(-3*time.Hour))
	d.crewLifecycleTick(now.Add(time.Minute))
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("auto-sleep is off and the tick sent %q", got)
	}
}

// A session mid-request is reading its cache entry right now, so its age is
// zero however long it has been working — the state stamp is when the turn
// started, not when it last talked to the model.
func TestCrewCacheState_TreatsAWorkingSessionAsMidRequest(t *testing.T) {
	d, sessionID, _ := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWorking, now.Add(-2*time.Hour))

	state := d.crewCacheState(d.store.Get(sessionID), now)
	if state.Age != 0 {
		t.Fatalf("a working session's cache reads %s old, want 0", state.Age)
	}
	if state.TTL != crewCacheTTLClaude*time.Second {
		t.Fatalf("cache TTL = %s, want claude's assumed %ds", state.TTL, crewCacheTTLClaude)
	}
}

func TestCrewCacheTTL_TakesThePerAgentAssumptionAndItsOverride(t *testing.T) {
	d := newCrewDaemon(t)
	if got := d.crewCacheTTL("codex"); got != crewCacheTTLCodex*time.Second {
		t.Fatalf("codex TTL = %s, want %ds", got, crewCacheTTLCodex)
	}
	// An unnamed harness gets the long assumption, because the cheap mistake is
	// one lapsed cache rather than a heartbeat every few minutes all day.
	if got := d.crewCacheTTL(""); got != crewCacheTTLDefault*time.Second {
		t.Fatalf("unnamed TTL = %s, want %ds", got, crewCacheTTLDefault)
	}
	d.store.SetSetting(SettingCrewCacheTTLPrefix+"codex", "600")
	if got := d.crewCacheTTL("codex"); got != 10*time.Minute {
		t.Fatalf("overridden codex TTL = %s, want 10m", got)
	}
	// Out of bounds falls back to the assumption — and says so, rather than
	// looking like a setting that does nothing.
	d.store.SetSetting(SettingCrewCacheTTLPrefix+"codex", "not-a-number")
	if got := d.crewCacheTTL("codex"); got != crewCacheTTLCodex*time.Second {
		t.Fatalf("a bad override gave %s, want the %ds assumption back", got, crewCacheTTLCodex)
	}
}

// The wake limit, where the user can actually hit it: a turnover that runs
// while nobody is there is a wake nobody asked for.

func TestChargeAutonomousWake_BooksWakesAndRefusesPastTheLimit(t *testing.T) {
	d := newCrewDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "2")
	now := time.Now()

	for i := 0; i < 2; i++ {
		if err := d.chargeAutonomousWake("trellis", now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("wake %d was refused: %v", i+1, err)
		}
	}
	// Durable: the limit bounds a night, and a daemon restart in the middle of
	// one must not hand back a fresh allowance.
	member := crewMemberRecord(t, d, "trellis")
	if got := len(member.AutonomousWakes); got != 2 {
		t.Fatalf("the roster records %d autonomous wakes, want 2", got)
	}

	err := d.chargeAutonomousWake("trellis", now.Add(2*time.Minute))
	if err == nil {
		t.Fatal("a third wake was allowed past a limit of 2")
	}
	for _, want := range []string{"Trellis", "crew.wake_limit=2", "nothing was woken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
	// The refused wake is not booked, so raising the limit lets it through.
	if got := len(crewMemberRecord(t, d, "trellis").AutonomousWakes); got != 2 {
		t.Fatalf("a refused wake was charged anyway: %d stamps", got)
	}
}

func TestChargeAutonomousWake_ForgetsWakesOlderThanTheWindow(t *testing.T) {
	d := newCrewDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "1")
	d.store.SetSetting(SettingCrewWakeLimitWindowSeconds, "3600")
	now := time.Now()

	if err := d.chargeAutonomousWake("trellis", now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("first wake: %v", err)
	}
	if err := d.chargeAutonomousWake("trellis", now); err != nil {
		t.Fatalf("a wake was refused on last night's allowance: %v", err)
	}
	if got := len(crewMemberRecord(t, d, "trellis").AutonomousWakes); got != 1 {
		t.Fatalf("the ledger kept %d stamps, want only the one inside the window", got)
	}
}

// Auto-sleep runs the nap unattended, so the wake limit has to be the thing
// standing between a runaway loop and a night of primings. Refusing has to be
// loud: the day it was protecting is still running, and the letter is filed.
func TestCrewHandoff_AWakeLimitRefusalLeavesTheDayRunningAndSaysWhy(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	d.store.SetSetting(SettingCrewWakeLimit, "0")
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	setUserAway(d, time.Now().Add(-3*time.Hour))

	// --nap: the member insisting on the next day even though nobody is there.
	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Note: "the tests are green\n", Close: protocol.Ptr(protocol.CrewDayCloseNap),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	napErr := protocol.Deref(resp.CrewHandoffResult.NapError)
	if !strings.Contains(napErr, "crew.wake_limit=0") {
		t.Fatalf("the nap failed with %q, which does not name the limit that stopped it", napErr)
	}
	// Nothing was spawned, the day that asked is still running, and the letter
	// is on disk — a refusal must not cost the member the day it had.
	if got := spawnedSessions(t, backend); len(got) != 1 {
		t.Fatalf("%d sessions were spawned; a refused wake must spawn nothing", len(got))
	}
	if d.store.Get(woken.SessionID) == nil {
		t.Fatal("the day was closed behind a wake that never happened")
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != woken.SessionID {
		t.Fatalf("binding = %q, want the day that is still running %q", got, woken.SessionID)
	}
}

// Sleep, not a turnover: a day that closes while nobody is there does not start
// another one, and the sidebar shows the member asleep, one click from a day.
func TestCrewHandoff_EndsTheDayWhenTheUserHasBeenAway(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	setUserAway(d, time.Now().Add(-3*time.Hour))

	resp := crewHandoffCall(t, d, woken.SessionID, "nothing is in flight\n")
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewHandoffResult
	if got := protocol.Deref(result.Outcome); got != protocol.CrewDayCloseSleep {
		t.Fatalf("outcome = %q, want sleep", got)
	}
	if protocol.Deref(result.SessionID) != "" {
		t.Fatalf("a successor %q was woken for a user who is not there", protocol.Deref(result.SessionID))
	}
	if got := spawnedSessions(t, backend); len(got) != 1 {
		t.Fatalf("%d sessions were spawned, want only the original wake", len(got))
	}
	if d.store.Get(woken.SessionID) != nil {
		t.Fatal("the day that filed its letter is still running")
	}
	// Asleep in the roster is what the sidebar reads: no binding, and the wake
	// button is one click.
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != "" {
		t.Fatalf("the member is still bound to %q after going to sleep", got)
	}
	// The letter is filed either way — sleeping is what happens behind it.
	if names := handoffFiles(t, d, "trellis"); len(names) != 2 {
		t.Fatalf("the handoffs dir holds %v, want the seeded letter and this one", names)
	}
}

// The member may insist either way, which is what makes the default safe to be
// a guess: a member closing on the user's own ask says --nap and gets its day.
func TestCrewHandoff_NapOverridesTheAbsence(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	setUserAway(d, time.Now().Add(-3*time.Hour))

	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Note: "picked up #901\n", Close: protocol.Ptr(protocol.CrewDayCloseNap),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	result := resp.CrewHandoffResult
	if napErr := protocol.Deref(result.NapError); napErr != "" {
		t.Fatalf("the nap did not run: %s", napErr)
	}
	if got := protocol.Deref(result.Outcome); got != protocol.CrewDayCloseNap {
		t.Fatalf("outcome = %q, want nap", got)
	}
	// It is still a wake nobody asked for, so it is charged against the limit.
	if got := len(crewMemberRecord(t, d, "trellis").AutonomousWakes); got != 1 {
		t.Fatalf("an unattended turnover booked %d wakes, want 1", got)
	}
}

// Sleep is a member going to bed, not a member forgetting its day: the next
// wake is a cold one, primed by the letter it just filed.
func TestCrewHandoff_SleepIsExplicitEvenWithTheUserHere(t *testing.T) {
	d, _, _ := newWakeableDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}

	msg := protocol.CrewHandoffMessage{
		Cmd: protocol.CmdCrewHandoff, SessionID: woken.SessionID,
		Note: "signing off for the night\n", Close: protocol.Ptr(protocol.CrewDayCloseSleep),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleCrewHandoff(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff: %v", protocol.Deref(resp.Error))
	}
	if got := protocol.Deref(resp.CrewHandoffResult.Outcome); got != protocol.CrewDayCloseSleep {
		t.Fatalf("outcome = %q, want sleep", got)
	}
	if got := protocol.Deref(memberByID(t, crewList(t, d), "trellis").BindingSession); got != "" {
		t.Fatalf("the member is still bound to %q after being told to sleep", got)
	}
}

// The memo is per session, so a member that napped does not inherit the
// previous day's grace — and a closed session does not leak a row forever.
func TestCrewLifecycleMemo_ForgetsAClosedSession(t *testing.T) {
	memo := newCrewLifecycleMemo()
	now := time.Now()
	if !memo.mayHeartbeat("a", now, time.Hour) {
		t.Fatal("the first heartbeat was refused")
	}
	if memo.mayHeartbeat("a", now.Add(time.Minute), time.Hour) {
		t.Fatal("a second heartbeat slipped through the grace")
	}
	if !memo.mayAsk("a", now, time.Hour) {
		t.Fatal("a heartbeat's grace blocked the handoff ask; they are separate acts")
	}
	memo.forget("a")
	if !memo.mayHeartbeat("a", now.Add(time.Minute), time.Hour) {
		t.Fatal("a forgotten session is still holding its grace")
	}
}

// The context-full handoff.

// setSessionContextOccupancy plants the reading the transcript watcher would
// have taken from the session's last turn. The watcher itself is exercised in
// internal/transcript; what matters here is what the tick does with the number.
func setSessionContextOccupancy(t *testing.T, d *Daemon, sessionID string, tokens, window int64) {
	t.Helper()
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	if watcher == nil {
		watcher = newTranscriptWatcher(sessionID, protocol.SessionAgentClaude, "", time.Now(), nil)
		if d.transcriptWatch == nil {
			d.transcriptWatch = make(map[string]*transcriptWatcher)
		}
		d.transcriptWatch[sessionID] = watcher
	}
	d.watchersMu.Unlock()
	watcher.observeOccupancy(transcript.ContextObservation{Tokens: tokens, Window: window})
}

// The incident this exists for: a member working away with the user right there
// and a cache nowhere near lapsing, whose context is about to be compacted by
// its harness. Neither cache-driven half has anything to say about it.
func TestCrewLifecycleTick_AsksForTheHandoffWhenTheContextIsFull(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)

	d.crewLifecycleTick(now)

	prompts := recorder.prompts()
	if len(prompts) != 1 {
		t.Fatalf("the tick sent %q, want the context handoff ask", prompts)
	}
	// The ask has to carry both numbers: an agent can act on "at X of Y" and
	// cannot act on a silent compact. And it has to ask for `--nap`, because a
	// day that ran out of room says nothing about whether the work is done, so
	// letting presence decide would park whatever was in flight.
	for _, want := range []string{
		"160000 of the 160000 tokens",
		"`attn handoff --nap -m",
		"Write the letter first",
		"carry on without asking",
	} {
		if !strings.Contains(prompts[0], want) {
			t.Fatalf("the context handoff ask does not carry %q: %q", want, prompts[0])
		}
	}
}

// Fire once. A member mid-letter re-nudged about the same full context reads it
// as a second, different instruction rather than a repeat of the first — and
// the condition holds for as long as the session lives, so without this it
// would be re-sent every minute.
func TestCrewLifecycleTick_AsksAboutAFullContextExactlyOnce(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault+50000, 0)

	for i := 0; i < 30; i++ {
		d.crewLifecycleTick(now.Add(time.Duration(i) * time.Minute))
	}
	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("a full context was asked about %d times over half an hour", len(got))
	}
}

// If the harness compacted anyway, the session has room again and a later fill
// is a new day's worth of context, not the same one.
func TestCrewLifecycleTick_ReArmsAfterAContextThatCameBackUnderBudget(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))

	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)
	d.crewLifecycleTick(now)
	setSessionContextOccupancy(t, d, sessionID, 20000, 0)
	d.crewLifecycleTick(now.Add(time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)
	d.crewLifecycleTick(now.Add(2 * time.Minute))

	if got := recorder.prompts(); len(got) != 2 {
		t.Fatalf("the tick sent %d asks across two separate fills: %q", len(got), got)
	}
}

// A session attn has taken no reading of — a harness it cannot parse, or one
// that has not spoken since the daemon started watching — is never asked on
// this ground. One turn of blindness, never a guess.
func TestCrewLifecycleTick_SaysNothingWithoutAReading(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))

	for i := 0; i < 10; i++ {
		d.crewLifecycleTick(now.Add(time.Duration(i) * time.Minute))
	}
	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("a session with no occupancy reading was sent %q", got)
	}
}

// Measured over every auto-compaction in the corpus behind this feature: 7 of
// 286 finished their whole climb inside one turn, the worst burning 159,674
// tokens without the session ever going idle. A rule that waits for the turn to
// end loses those days, so this one does not wait.
func TestCrewLifecycleTick_AsksAMemberThatIsStillWorking(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateWorking, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)

	d.crewLifecycleTick(now)

	if got := recorder.prompts(); len(got) != 1 {
		t.Fatalf("a working member with a full context was sent %q, want the handoff ask", got)
	}
}

// The other two halves still wait for the turn to end, and a mid-turn member is
// the case that would otherwise collect a heartbeat every minute.
func TestCrewLifecycleTick_LeavesAWorkingMemberAloneWithoutContextPressure(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	// Old enough that the cache is past the heartbeat lead, which is what would
	// fire if the mid-turn rule were dropped for every action rather than one.
	setSessionActivity(t, d, sessionID, protocol.SessionStateWorking, now.Add(-3*time.Hour))

	d.crewLifecycleTick(now)

	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("a working member with room left was sent %q", got)
	}
}

func TestCrewLifecycleTick_ContextHandoffCanBeTurnedOff(t *testing.T) {
	d, sessionID, recorder := newLifecycleDaemon(t)
	now := time.Now()
	setSessionActivity(t, d, sessionID, protocol.SessionStateIdle, now.Add(-2*time.Minute))
	setSessionContextOccupancy(t, d, sessionID, crewContextBudgetDefault, 0)
	d.store.SetSetting(SettingCrewContextHandoffEnabled, "false")

	d.crewLifecycleTick(now)

	if got := recorder.prompts(); len(got) != 0 {
		t.Fatalf("the context half was off and the tick still sent %q", got)
	}
}

// The budget: the setting, lowered to fit whenever a window IS known.
func TestCrewContextBudget(t *testing.T) {
	d, sessionID, _ := newLifecycleDaemon(t)
	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("no session %s", sessionID)
	}

	t.Run("the shipped default with no window in sight", func(t *testing.T) {
		setSessionContextOccupancy(t, d, sessionID, 1000, 0)
		if got := d.crewContextPressure(session); got.Budget != crewContextBudgetDefault {
			t.Fatalf("budget = %d, want %d", got.Budget, crewContextBudgetDefault)
		}
	})

	t.Run("a stated window smaller than the budget lowers it, minus the letter's room", func(t *testing.T) {
		// Codex states its model's window on every token_count. A member whose
		// window is under the budget would otherwise never be asked at all.
		setSessionContextOccupancy(t, d, sessionID, 1000, 100000)
		want := int64(100000 - crewContextHandoffMargin)
		if got := d.crewContextPressure(session); got.Budget != want {
			t.Fatalf("budget = %d, want %d", got.Budget, want)
		}
	})

	t.Run("a window bigger than the budget does not raise it", func(t *testing.T) {
		setSessionContextOccupancy(t, d, sessionID, 1000, 1000000)
		if got := d.crewContextPressure(session); got.Budget != crewContextBudgetDefault {
			t.Fatalf("budget = %d, want the setting to stay the ceiling", got.Budget)
		}
	})

	t.Run("the setting moves it", func(t *testing.T) {
		d.store.SetSetting(SettingCrewContextHandoffTokens, "90000")
		t.Cleanup(func() { d.store.SetSetting(SettingCrewContextHandoffTokens, "") })
		setSessionContextOccupancy(t, d, sessionID, 1000, 0)
		if got := d.crewContextPressure(session); got.Budget != 90000 {
			t.Fatalf("budget = %d, want the configured 90000", got.Budget)
		}
	})

	t.Run("no reading is no pressure", func(t *testing.T) {
		other := *session
		other.ID = "no-watcher-session"
		if got := d.crewContextPressure(&other); got.Tokens != 0 || got.Budget != 0 {
			t.Fatalf("pressure = %+v, want nothing at all", got)
		}
	})
}
