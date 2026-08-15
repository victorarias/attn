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

	base := Signals{
		AwayLimit: limit, Lead: lead, Reachable: true, Settled: true,
		HeartbeatEnabled: true, AutoSleepEnabled: true,
	}
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
