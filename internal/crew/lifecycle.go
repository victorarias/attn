package crew

import (
	"fmt"
	"time"
)

// The lifecycle policy: what attn does about an awake member as the user's
// attention comes and goes. Pure arithmetic over signals the daemon measures —
// how long the user has been gone, how old a session's prompt cache is — so the
// decision can be read, argued with, and tested without a daemon.
//
// The whole policy is the plan's arithmetic. A member's day sits at ~100k
// tokens. Letting its cache lapse and carrying on costs a re-write of all of it
// (~$3.00 on Opus-class list prices, 1h-TTL cache write at 2x); keeping it warm
// costs one cache read per TTL (~$0.15); sleeping and waking fresh costs the
// priming (~$0.30, and the day's live context). So: warm it while the user is
// there, and end the day when they are not, because warmth bought through an
// absence is warmth nobody uses.
//
// Context fill is the third thing it watches, and the only one that is not
// about money: a member whose context fills up is compacted by its harness, and
// a compact is not a nap. What the day was survives as the harness's summary of
// itself rather than as the letter the member would have written.

// CacheState is what attn believes about a session's prompt cache. It is an
// ESTIMATE and says so in its name: no API reports a cache entry's remaining
// life, so this is time since the session last talked to the model, judged
// against an assumed TTL for its harness. Wrong-long costs one lapsed cache;
// wrong-short costs a heartbeat that was not needed.
type CacheState struct {
	// Age is time since the session's last request. The TTL is measured from the
	// start of the request that writes OR reads the entry and is refreshed for
	// free on every hit, which is what makes "time since the last request" the
	// right quantity rather than "time since the cache was written".
	Age time.Duration
	// TTL is the assumed lifetime for this session's harness.
	TTL time.Duration
}

// Remaining is how long the estimate says the cache has left. Negative once the
// estimate says it has lapsed.
func (c CacheState) Remaining() time.Duration { return c.TTL - c.Age }

// ContextPressure is how full a member's context is against the budget its day
// gets. Both in tokens; a zero Tokens means attn has no reading — a harness it
// cannot parse, or a session that has not spoken yet — and a member attn cannot
// measure is never asked to close on this ground.
type ContextPressure struct {
	Tokens int64
	Budget int64
}

// Full reports that the day has spent its context budget.
func (c ContextPressure) Full() bool {
	return c.Tokens > 0 && c.Budget > 0 && c.Tokens >= c.Budget
}

// Signals is everything the decision reads. Assembled by the daemon once per
// tick per awake member.
type Signals struct {
	// AwayFor is how long since any client last reported above `away`. Zero
	// while the user is here.
	AwayFor time.Duration
	// AwayLimit is how long an absence has to last to count as one.
	AwayLimit time.Duration
	Cache     CacheState
	// Lead is how far ahead of the estimated expiry attn acts. It absorbs the
	// tick interval plus the time a nudge takes to reach the model.
	Lead time.Duration
	// Reachable is whether a prompt typed at this session would be read. A
	// session mid-turn or blocked on an approval is not: it would queue the
	// prompt behind work nobody asked to interrupt.
	Reachable bool
	// Settled is whether the session owes nobody an answer. A session holding a
	// question for the user is reachable but not settled, and the two halves
	// want opposite things from it: a heartbeat delivered there answers the
	// member's own question with filler and buries it, while the handoff ask is
	// precisely the answer an absence has for an open question.
	Settled bool
	// Context is how full the member's context is against its budget. Read
	// independently of everything above: a full context is a full context whether
	// the user is here, the cache is fresh, or the member is mid-thought.
	Context ContextPressure
	// HeartbeatEnabled and AutoSleepEnabled are the two halves' switches. Both
	// on by default; either off means that half does nothing at all, never that
	// the other half covers for it.
	HeartbeatEnabled bool
	AutoSleepEnabled bool
	// ContextHandoffEnabled is the third half's switch, default on like the
	// others.
	ContextHandoffEnabled bool
}

// Action is what to do about one awake member this tick.
type Action int

const (
	// ActionNone is the answer almost every tick, and the reason the subsystem
	// is silent on a quiet session: nothing is close enough to expiry to be
	// worth a request, so nothing is sent.
	ActionNone Action = iota
	// ActionHeartbeat nudges the session so its cache is read and its lifetime
	// starts over.
	ActionHeartbeat
	// ActionSleep prompts the member's handoff so the day ends.
	ActionSleep
	// ActionContextHandoff prompts the member's handoff because its context is
	// nearly full. The same turnover as ActionSleep and for the opposite reason:
	// sleep ends a day nobody is watching, this ends a day that ran out of room.
	ActionContextHandoff
)

func (a Action) String() string {
	switch a {
	case ActionHeartbeat:
		return "heartbeat"
	case ActionSleep:
		return "sleep"
	case ActionContextHandoff:
		return "context_handoff"
	default:
		return "none"
	}
}

// Decide answers one member's tick.
//
// Cache pressure is the gate on both halves, which is what keeps an idle system
// idle: a member whose cache is fresh is left alone whether the user is here or
// on holiday. Only once the cache is about to lapse is there a decision worth
// spending anything on — and then who is here decides which way it goes.
//
// Context fill is not gated on cache pressure and sits above it: it is the one
// condition that only gets worse, and waiting it out is how a day is lost.
//
// The three actions ask different things of the session they act on. Ending a
// day — for context or for an absence — is an answer to whatever the member was
// waiting for, so it only needs the session to take input. Warming a cache is
// not an answer to anything, so it waits for a session that owes nobody one: a
// heartbeat typed at a member holding a question for the user answers that
// question with filler.
func Decide(s Signals) Action {
	if !s.Reachable {
		return ActionNone
	}
	// A cache lapse costs a re-write; a full context costs the day — the harness
	// compacts it, and what the member would have written a letter about survives
	// only as the harness's summary of itself.
	if s.ContextHandoffEnabled && s.Context.Full() {
		return ActionContextHandoff
	}
	if s.Cache.Remaining() > s.Lead {
		return ActionNone
	}
	if s.AwayFor >= s.AwayLimit {
		if !s.AutoSleepEnabled {
			return ActionNone
		}
		return ActionSleep
	}
	if !s.HeartbeatEnabled || !s.Settled {
		return ActionNone
	}
	return ActionHeartbeat
}

// WakeLedger bounds how often a member is woken by something other than the
// user. Every autonomous wake is a fresh priming paid for, and a loop nobody is
// watching would pay for it all night; the limit is what makes an unattended
// crew safe to leave running.
//
// Arithmetic behind the default, list prices, not a measurement: a wake injects
// the priming measured in slice 2 (1.5-13KB, at most ~3.5k tokens) and the new
// day's first turn. At Opus-class $15/M input with the 1h-TTL cache write at 2x,
// the priming is 3.5k x $15/M x 2 = ~$0.11, call it ~$0.15 with the greeting.
// Eight wakes in twelve hours is ~$1.20 per member per absence — the price of
// noticing a runaway rather than sleeping through it. A number we will change:
// it is a setting, tunable without a release, never a constant.
type WakeLedger struct {
	Stamps []time.Time
	Limit  int
	Window time.Duration
}

// Within returns the stamps still inside the window as of now, newest last.
func (l WakeLedger) Within(now time.Time) []time.Time {
	cutoff := now.Add(-l.Window)
	kept := make([]time.Time, 0, len(l.Stamps))
	for _, at := range l.Stamps {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

// Allows reports whether one more autonomous wake fits, and names the limit,
// its value and the ask when it does not. A limit somebody can hit is a limit
// they must see: the caller prints this, it is never a silent no-op.
func (l WakeLedger) Allows(memberID string, now time.Time) ([]time.Time, error) {
	kept := l.Within(now)
	name := DisplayName(memberID)
	if l.Limit <= 0 {
		return kept, fmt.Errorf("autonomous wakes are turned off (crew.wake_limit=%d), so %s was not woken; wake it yourself from the sidebar, or raise the limit", l.Limit, name)
	}
	if len(kept) >= l.Limit {
		return kept, fmt.Errorf("%s has been woken %d times without the user in the last %s, and the limit is %d (crew.wake_limit=%d, crew.wake_limit_window_seconds=%d); nothing was woken. Wake it yourself from the sidebar, or raise the limit",
			name, len(kept), l.Window, l.Limit, l.Limit, int(l.Window/time.Second))
	}
	return append(kept, now), nil
}
