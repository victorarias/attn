package crew

import (
	"strings"
	"testing"
	"time"
)

// The policy in one table. Every row is a sentence about the product: the
// columns are what attn measured, the answer is what it does about it.
func TestDecide(t *testing.T) {
	const (
		ttl   = time.Hour
		lead  = 5 * time.Minute
		limit = 2 * time.Hour
	)
	warm := CacheState{Age: 10 * time.Minute, TTL: ttl}
	expiring := CacheState{Age: 58 * time.Minute, TTL: ttl}
	lapsed := CacheState{Age: 90 * time.Minute, TTL: ttl}

	// Every row runs with the context half ON and no reading taken, so each one
	// also asserts that a member attn cannot measure is never asked to close on
	// that ground.
	base := Signals{
		AwayLimit: limit, Lead: lead, Reachable: true, Settled: true,
		HeartbeatEnabled: true, AutoSleepEnabled: true, ContextHandoffEnabled: true,
	}
	full := ContextPressure{Tokens: 160000, Budget: 160000}
	roomy := ContextPressure{Tokens: 40000, Budget: 160000}
	with := func(mutate func(*Signals)) Signals {
		s := base
		mutate(&s)
		return s
	}

	cases := []struct {
		name    string
		signals Signals
		want    Action
	}{
		{
			// The idle case, and the whole reason this is safe to run every minute:
			// the user is right here and the cache is fresh, so nothing is sent.
			name:    "a quiet attended session with a fresh cache is left alone",
			signals: with(func(s *Signals) { s.Cache = warm }),
			want:    ActionNone,
		},
		{
			name:    "a quiet UNattended session with a fresh cache is still left alone",
			signals: with(func(s *Signals) { s.Cache = warm; s.AwayFor = 6 * time.Hour }),
			want:    ActionNone,
		},
		{
			name:    "the user is here and the cache is about to lapse, so warm it",
			signals: with(func(s *Signals) { s.Cache = expiring }),
			want:    ActionHeartbeat,
		},
		{
			// The lead is what makes this act early rather than on the stroke, so a
			// cache already past the estimate is the same decision, not a new one.
			name:    "a cache the estimate says has already lapsed still warms",
			signals: with(func(s *Signals) { s.Cache = lapsed }),
			want:    ActionHeartbeat,
		},
		{
			name:    "the user is gone and the cache is about to lapse, so end the day",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = 3 * time.Hour }),
			want:    ActionSleep,
		},
		{
			// An absence has to last before attn believes it: a user who stepped out
			// for a coffee gets their day kept warm, not ended.
			name:    "an absence shorter than the limit is not an absence",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = limit - time.Second }),
			want:    ActionHeartbeat,
		},
		{
			// A heartbeat is not an answer to anything, so it waits for a session
			// that owes nobody one: typed at a member holding a question for the
			// user, it answers that question with filler and buries it.
			name:    "an unsettled session is not heartbeated",
			signals: with(func(s *Signals) { s.Cache = expiring; s.Settled = false }),
			want:    ActionNone,
		},
		{
			// Ending the day IS an answer to whatever the member was waiting for,
			// which is why this half asks only that the session take input.
			name:    "an unsettled session is still put to sleep",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = 3 * time.Hour; s.Settled = false }),
			want:    ActionSleep,
		},
		{
			// A prompt typed at a session mid-turn queues behind work nobody asked
			// to interrupt, so neither half acts on one.
			name:    "an unreachable session is never nudged, however pressed its cache",
			signals: with(func(s *Signals) { s.Cache = lapsed; s.Reachable = false }),
			want:    ActionNone,
		},
		{
			name:    "an unreachable session is not put to sleep either",
			signals: with(func(s *Signals) { s.Cache = lapsed; s.AwayFor = 3 * time.Hour; s.Reachable = false }),
			want:    ActionNone,
		},
		{
			name:    "heartbeat off means no heartbeat",
			signals: with(func(s *Signals) { s.Cache = expiring; s.HeartbeatEnabled = false }),
			want:    ActionNone,
		},
		{
			// Either switch off means that half does nothing — never that the other
			// half covers for it. Sleeping a member because heartbeats are off would
			// be attn answering a question the user did not ask.
			name:    "heartbeat off does not become sleep",
			signals: with(func(s *Signals) { s.Cache = lapsed; s.HeartbeatEnabled = false }),
			want:    ActionNone,
		},
		{
			name:    "auto-sleep off does not become a heartbeat while the user is gone",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = 3 * time.Hour; s.AutoSleepEnabled = false }),
			want:    ActionNone,
		},
		{
			// The whole point of the third half: the user is right here and the cache
			// is fresh, so neither cache-driven half has anything to say, and the day
			// still has to end because the harness is about to compact it.
			name:    "a full context ends the day with the user watching and the cache warm",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full }),
			want:    ActionContextHandoff,
		},
		{
			// Warming the cache of a day that is about to end is money spent on
			// context nobody will use.
			name:    "a full context outranks the heartbeat its cache would have earned",
			signals: with(func(s *Signals) { s.Cache = expiring; s.Context = full }),
			want:    ActionContextHandoff,
		},
		{
			name:    "a context with room left decides nothing on its own",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = roomy }),
			want:    ActionNone,
		},
		{
			// Closing IS an answer, so an unsettled member gets asked — the same rule
			// auto-sleep follows, for the same reason.
			name:    "an unsettled session with a full context is still asked to close",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full; s.Settled = false }),
			want:    ActionContextHandoff,
		},
		{
			// Mid-turn the ask would queue behind work nobody asked to interrupt, and
			// the member is talking to the model right now anyway.
			name:    "an unreachable session is not asked to close a full context",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full; s.Reachable = false }),
			want:    ActionNone,
		},
		{
			name:    "the context half off leaves a full context alone",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full; s.ContextHandoffEnabled = false }),
			want:    ActionNone,
		},
		{
			// Off means off, not "the other halves cover for it".
			name:    "the context half off does not become a heartbeat",
			signals: with(func(s *Signals) { s.Cache = expiring; s.Context = full; s.ContextHandoffEnabled = false }),
			want:    ActionHeartbeat,
		},
		{
			name:    "a budget attn could not resolve never reads as full",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = ContextPressure{Tokens: 900000} }),
			want:    ActionNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.signals); got != tc.want {
				t.Fatalf("Decide() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCacheStateRemaining(t *testing.T) {
	fresh := CacheState{Age: time.Minute, TTL: time.Hour}
	if got := fresh.Remaining(); got != 59*time.Minute {
		t.Fatalf("Remaining() = %s, want 59m", got)
	}
	// Negative rather than clamped: how far past the estimate a cache is says
	// something, and the caller prints it.
	stale := CacheState{Age: 2 * time.Hour, TTL: time.Hour}
	if got := stale.Remaining(); got != -time.Hour {
		t.Fatalf("Remaining() = %s, want -1h", got)
	}
}

func TestWakeLedger_CountsOnlyWakesInsideTheWindow(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	ledger := WakeLedger{
		Limit:  2,
		Window: 12 * time.Hour,
		Stamps: []time.Time{
			now.Add(-30 * time.Hour), // last night's absence, long since paid for
			now.Add(-11 * time.Hour),
		},
	}
	kept, err := ledger.Allows("trellis", now)
	if err != nil {
		t.Fatalf("Allows() refused a wake with one stamp inside the window: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("Allows() kept %d stamps, want the in-window one plus this wake", len(kept))
	}
	// The window is what trims the ledger, so the record cannot grow forever.
	if !kept[len(kept)-1].Equal(now) {
		t.Fatalf("the newest kept stamp is %s, want this wake at %s", kept[len(kept)-1], now)
	}
	for _, at := range kept {
		if at.Before(now.Add(-ledger.Window)) {
			t.Fatalf("a stamp from %s survived a %s window", at, ledger.Window)
		}
	}
}

// A limit somebody can hit is a limit they must see: the refusal names the
// limit, its value, the ask, and how to get past it.
func TestWakeLedger_RefusalNamesTheLimitAndTheAsk(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	ledger := WakeLedger{
		Limit:  2,
		Window: 12 * time.Hour,
		Stamps: []time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour)},
	}
	_, err := ledger.Allows("trellis", now)
	if err == nil {
		t.Fatal("Allows() let a third wake through a limit of 2")
	}
	for _, want := range []string{"Trellis", "crew.wake_limit=2", "crew.wake_limit_window_seconds=43200", "nothing was woken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
}

// Zero is a legitimate setting — it turns autonomous wakes off — so it refuses
// with its own sentence rather than the count-based one, which would read as a
// bug ("woken 0 times, and the limit is 0").
func TestWakeLedger_ZeroTurnsAutonomousWakesOff(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	_, err := WakeLedger{Limit: 0, Window: 12 * time.Hour}.Allows("trellis", now)
	if err == nil {
		t.Fatal("Allows() woke a member with the limit set to 0")
	}
	if !strings.Contains(err.Error(), "turned off") || !strings.Contains(err.Error(), "crew.wake_limit=0") {
		t.Fatalf("the refusal %q does not say autonomous wakes are off", err)
	}
}
