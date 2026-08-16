package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

// The crew lifecycle's daemon half: the tick that watches awake members, the
// signals it reads, and the two things it can do about them.
//
// It is the presence system's second consumer. Session-activity generation asks
// presence one question — generate now? — and this asks a different one, so it
// reads the same tier and adds what that question needs: how long the user has
// been gone (client_presence.go), and how old a session's prompt cache is.
//
// Idle systems must be idle. Cache pressure gates every action in
// crew.Decide, so the ordinary tick on a quiet attended session reads the
// roster, finds nothing near expiry, and sends nothing at all.

const (
	// crewLifecycleKind is the tick's job kind on the durable queue — one
	// mechanism for every recurring duty, one durable record of when it fires.
	crewLifecycleKind = "crew_lifecycle_tick"
	// crewLifecycleInterval is how often the decision is remade. It must be well
	// under the heartbeat lead so a member cannot slip from "fine" to "lapsed"
	// between two ticks; the lead below is five of these.
	crewLifecycleInterval = 60 * time.Second
	crewLifecycleTimeout  = 60 * time.Second
)

// Prompt-cache lifetimes, per harness. ASSUMPTIONS, not measurements: no API
// reports a cache entry's remaining life, so what attn can know is when the
// session last talked to the model, and the rest is the vendor's documented
// policy. Both vendors refresh the entry for free on every read, which is what
// makes "time since the last request" the quantity to compare against.
//
//   - claude 1h: Anthropic prompt caching is 5 minutes by default with a 1-hour
//     option, and Claude Code takes the hour. Every crew member has run on
//     Claude Code since the simulation started on 2026-08-06.
//   - codex 30m: OpenAI, GPT-5.6 and later — "the 30-minute lifetime begins when
//     the prefix is written and refreshes whenever the prefix is reused".
//
// An unnamed harness gets claude's hour rather than the shortest assumption
// available, and the arithmetic is why: assuming too short heartbeats a member
// every few minutes all day (twelve an hour, ~$1.80/h at the plan's ~$0.15 a
// heartbeat), while assuming too long costs one lapsed cache (~$3.00) once. The
// cheap mistake is the long one. `crew.cache_ttl_seconds.<agent>` overrides.
const (
	crewCacheTTLClaude  = 3600
	crewCacheTTLCodex   = 1800
	crewCacheTTLDefault = 3600
)

// crewHeartbeatLeadDefault is how far ahead of the estimated expiry attn acts.
// It absorbs the tick interval plus a nudge's trip to the model and back, so it
// is a tripwire rather than a tuning: five ticks, where one would already do.
const crewHeartbeatLeadDefault = 300

// crewAwayDefault is how long an absence has to last before attn believes it.
//
// Receipt, measured 2026-08-14 over the production event log — 12.4 days of
// real use, 2026-08-02 to 08-14, 3,480 gaps between user-caused daemon facts:
// 82% under a minute, 96% under ten, 99.3% under an hour. The tail runs
// continuously up to 7,556s (2h06m) and then jumps to 18,468s (5h08m) — a clean
// break between the longest pause inside a working day and the shortest real
// absence. The measurement is coarse (a user typing into a terminal writes no
// fact), so real input gaps are at most these, never more.
//
// 9,000s sits past the whole continuum and well inside the break: only a real
// absence reaches it. Waiting that long costs about two heartbeats (~$0.30)
// before sleeping, against the ~$3.00 a lapsed cache would cost — the right
// side of the arithmetic, and the wrong direction to be generous in is the
// other one, because a member slept on a user who was only at lunch loses that
// day's live context.
const crewAwayDefault = 9000

// The wake limit's default and window. The arithmetic is on crew.WakeLedger.
const (
	crewWakeLimitDefault        = 8
	crewWakeLimitWindowDefault  = 43200
	crewWakeLimitMax            = 1000
	crewWakeLimitWindowMinSecs  = 60
	crewWakeLimitWindowMaxSecs  = 7 * 24 * 3600
	crewCacheTTLMinSeconds      = 60
	crewCacheTTLMaxSeconds      = 24 * 3600
	crewHeartbeatLeadMinSeconds = 30
	crewHeartbeatLeadMaxSeconds = 3600
	crewAwayMinSeconds          = 60
	crewAwayMaxSeconds          = 24 * 3600
)

// crewHeartbeatPrompt is the whole content of a heartbeat. Warming the cache is
// all it is for: reading the day's context is what refreshes the entry, so the
// cheapest turn that reads it is the right one, and asking for work would spend
// a day's attention on a timer nobody set.
const crewHeartbeatPrompt = "[attn] Keeping your context warm — no work is being asked for, and nothing has changed. Reply with one short line and go back to what you were doing."

// crewSleepPrompt ends the day. The member writes the letter, as always: attn
// decides when to ask, never what to say.
const crewSleepPrompt = "[attn] The user has been away long enough that your day should end rather than carry on warm. Close it now: write your letter to whoever wakes as you next — what you were doing, what is load-bearing, what you would pick up first — and file it with `attn handoff --sleep -m \"<your letter>\"`. Your session ends when it lands; nobody wakes behind it, and you will not be woken again until the user asks."

// crewSleepPromptGrace is how long attn waits for a prompted handoff before
// asking again. A member mid-thought may take minutes to close, and re-asking
// every tick would bury the first ask under copies of itself.
const crewSleepPromptGrace = 10 * time.Minute

// crewLifecycleMemo is what the tick remembers between fires, so a nudge that
// did not visibly land is not sent again on the next one. In memory on purpose:
// it bounds one episode, and a daemon that restarted has already lost the
// session it describes.
type crewLifecycleMemo struct {
	mu    sync.Mutex
	acted map[string]time.Time // session id -> when it was last heartbeated
	asked map[string]time.Time // session id -> when its handoff was last prompted
}

func newCrewLifecycleMemo() *crewLifecycleMemo {
	return &crewLifecycleMemo{
		acted: make(map[string]time.Time),
		asked: make(map[string]time.Time),
	}
}

// mayHeartbeat and mayAsk report whether enough time has passed since the last
// action of that kind on this session, and record the attempt when it has. The
// grace is what keeps a nudge nobody answered from being re-sent every tick for
// as long as the condition that provoked it holds.
func (m *crewLifecycleMemo) mayHeartbeat(sessionID string, now time.Time, grace time.Duration) bool {
	return m.mayAct(m.acted, sessionID, now, grace)
}

func (m *crewLifecycleMemo) mayAsk(sessionID string, now time.Time, grace time.Duration) bool {
	return m.mayAct(m.asked, sessionID, now, grace)
}

func (m *crewLifecycleMemo) mayAct(table map[string]time.Time, sessionID string, now time.Time, grace time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := table[sessionID]; ok && now.Sub(last) < grace {
		return false
	}
	table[sessionID] = now
	return true
}

func (m *crewLifecycleMemo) forget(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.acted, sessionID)
	delete(m.asked, sessionID)
}

// Settings.

// crewBoolSetting reads a default-ON switch: blank is on, only an explicit
// "false" turns it off.
func (d *Daemon) crewBoolSetting(name string) bool {
	if d.store == nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(d.store.GetSetting(name)), "false")
}

// crewSeconds reads a bounded duration setting, saying so when a stored value
// is outside its bounds rather than falling back silently — a limit somebody
// can hit is a limit they must see, and a typo'd setting that quietly reverts
// looks exactly like a feature that does not work.
func (d *Daemon) crewSeconds(name string, fallback, min, max int) time.Duration {
	seconds := fallback
	if d.store != nil {
		raw := strings.TrimSpace(d.store.GetSetting(name))
		if raw != "" {
			parsed := resolveBoundedIntSetting(raw, fallback, min, max)
			if parsed == fallback && raw != fmt.Sprint(fallback) {
				d.logf("crew: %s is %q, which is not a whole number of seconds between %d and %d; using %d", name, raw, min, max, fallback)
			}
			seconds = parsed
		}
	}
	return time.Duration(seconds) * time.Second
}

// crewCacheTTL is the assumed prompt-cache lifetime for one session's harness.
func (d *Daemon) crewCacheTTL(agent string) time.Duration {
	agent = strings.ToLower(strings.TrimSpace(agent))
	fallback := crewCacheTTLDefault
	switch agent {
	case "claude":
		fallback = crewCacheTTLClaude
	case "codex":
		fallback = crewCacheTTLCodex
	}
	if agent != "" && d.store != nil {
		if raw := strings.TrimSpace(d.store.GetSetting(SettingCrewCacheTTLPrefix + agent)); raw != "" {
			return d.crewSeconds(SettingCrewCacheTTLPrefix+agent, fallback, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
		}
	}
	return d.crewSeconds(SettingCrewCacheTTLSeconds, fallback, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
}

func (d *Daemon) crewHeartbeatLead() time.Duration {
	return d.crewSeconds(SettingCrewHeartbeatLeadSeconds, crewHeartbeatLeadDefault, crewHeartbeatLeadMinSeconds, crewHeartbeatLeadMaxSeconds)
}

func (d *Daemon) crewAwayLimit() time.Duration {
	return d.crewSeconds(SettingCrewAwaySeconds, crewAwayDefault, crewAwayMinSeconds, crewAwayMaxSeconds)
}

// crewWakeLedger is the limit as configured, without a member's stamps. Zero is
// a legitimate value — it turns autonomous wakes off — so it is not bounded
// away from below.
func (d *Daemon) crewWakeLedger() crew.WakeLedger {
	limit := crewWakeLimitDefault
	if d.store != nil {
		if raw := strings.TrimSpace(d.store.GetSetting(SettingCrewWakeLimit)); raw != "" {
			limit = resolveBoundedIntSetting(raw, crewWakeLimitDefault, 0, crewWakeLimitMax)
		}
	}
	return crew.WakeLedger{
		Limit:  limit,
		Window: d.crewSeconds(SettingCrewWakeLimitWindowSeconds, crewWakeLimitWindowDefault, crewWakeLimitWindowMinSecs, crewWakeLimitWindowMaxSecs),
	}
}

// Signals.

// crewCacheState estimates where a session's prompt cache stands. Time since
// the last request is what the vendors' own TTL is measured from, and the
// daemon's record of that is the session's last state transition: a session
// that is working is mid-request, so its entry is being read right now, and one
// that has settled last read it when it settled.
func (d *Daemon) crewCacheState(session *protocol.Session, now time.Time) crew.CacheState {
	state := crew.CacheState{TTL: d.crewCacheTTL(string(session.Agent))}
	switch session.State {
	case protocol.SessionStateWorking, protocol.SessionStateLaunching:
		return state
	}
	updated := protocol.Timestamp(session.StateUpdatedAt).Time()
	if updated.IsZero() || !now.After(updated) {
		return state
	}
	state.Age = now.Sub(updated)
	return state
}

// crewSessionReachable reports whether a prompt typed here would be read rather
// than queued behind work nobody asked to interrupt. The doorbell's own rule
// stops at approvals; this consumer also stays off a session mid-turn, because
// nothing here is urgent enough to land in whatever a turn has on screen — an
// in-flight question selector reads as `working` and would swallow the paste as
// its answer. A member mid-turn is talking to the model right now anyway, so its
// cache is the freshest in the roster and the tick has nothing to say to it.
func crewSessionReachable(session *protocol.Session) bool {
	return isNudgeDeliveryAllowed(string(session.State)) &&
		session.State != protocol.SessionStateWorking
}

// crewSessionSettled reports that the session owes nobody an answer. Only the
// heartbeat asks for this, and `waiting_input` is why: a member sitting on a
// question for the user is reachable, so the paste lands — in the composer, as
// the answer to the member's own question, which is then filler and gone.
//
// Delivering into a harness's modal selector has the same shape and this does
// not fix it: a modal can be open while the state says idle, and the state is
// as fresh as the last classification. That one belongs to the doorbell, where
// a fix would cover every nudge attn sends.
func crewSessionSettled(session *protocol.Session) bool {
	return session.State == protocol.SessionStateIdle
}

// The tick.

// registerCrewLifecycleCron arms the tick. Home-only, like every crew surface:
// an outpost holds no roster to watch.
func (d *Daemon) registerCrewLifecycleCron(runner *jobs.Runner) {
	if err := runner.RegisterCron(
		crewLifecycleKind,
		crewLifecycleInterval,
		d.crewLifecycleHandler,
		jobs.HandlerConfig{Timeout: crewLifecycleTimeout},
	); err != nil {
		d.logf("crew: register lifecycle tick: %v", err)
	}
}

func (d *Daemon) crewLifecycleHandler(_ context.Context, _ *jobs.Job) (any, error) {
	d.crewLifecycleTick(time.Now())
	return nil, nil
}

// crewLifecycleTick decides and acts for every awake member. It writes nothing
// and sends nothing when nothing is near expiry, which is the ordinary case and
// the reason this is safe to run every minute for as long as the daemon lives.
func (d *Daemon) crewLifecycleTick(now time.Time) {
	if d.store == nil {
		return
	}
	if err := d.requireHome(crew.Surface); err != nil {
		return
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for the lifecycle tick: %v", err)
		}
		return
	}
	awayFor := d.UserAwayFor(now)
	awayLimit := d.crewAwayLimit()
	lead := d.crewHeartbeatLead()
	heartbeat := d.crewBoolSetting(SettingCrewHeartbeatEnabled)
	autoSleep := d.crewBoolSetting(SettingCrewAutoSleepEnabled)
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		session := d.store.Get(member.BindingSession)
		if session == nil {
			continue
		}
		cache := d.crewCacheState(session, now)
		action := crew.Decide(crew.Signals{
			AwayFor:          awayFor,
			AwayLimit:        awayLimit,
			Cache:            cache,
			Lead:             lead,
			Reachable:        crewSessionReachable(session),
			Settled:          crewSessionSettled(session),
			HeartbeatEnabled: heartbeat,
			AutoSleepEnabled: autoSleep,
		})
		if action == crew.ActionNone {
			continue
		}
		d.actOnCrewMember(member, session.ID, action, cache, now)
	}
}

// actOnCrewMember carries out one decision. Every path is rate-limited against
// the memo: a nudge that does not visibly land must not be re-sent every minute
// for as long as the condition holds.
func (d *Daemon) actOnCrewMember(member crew.Member, sessionID string, action crew.Action, cache crew.CacheState, now time.Time) {
	switch action {
	case crew.ActionHeartbeat:
		if !d.crewMemo().mayHeartbeat(sessionID, now, d.crewHeartbeatLead()) {
			return
		}
		if err := d.typeDoorbell(sessionID, crewHeartbeatPrompt); err != nil {
			d.logf("crew: %s's heartbeat did not reach session %s: %v", crew.DisplayName(member.ID), sessionID, err)
			return
		}
		d.logf("crew: warmed %s's context in session %s (cache estimated %s old against a %s assumption)",
			crew.DisplayName(member.ID), sessionID, cache.Age.Round(time.Second), cache.TTL)
	case crew.ActionSleep:
		if !d.crewMemo().mayAsk(sessionID, now, crewSleepPromptGrace) {
			return
		}
		if err := d.typeDoorbell(sessionID, crewSleepPrompt); err != nil {
			d.logf("crew: %s was not asked to close its day: %v", crew.DisplayName(member.ID), err)
			return
		}
		d.logf("crew: asked %s to close its day — the user has been away and the cache is %s from lapsing",
			crew.DisplayName(member.ID), cache.Remaining().Round(time.Second))
	}
}

// crewMemo lazily builds the tick's memory. Lazily because every daemon
// constructor would otherwise have to know about it, and the tick is the only
// caller.
func (d *Daemon) crewMemo() *crewLifecycleMemo {
	d.crewMemoOnce.Do(func() { d.crewLifecycleState = newCrewLifecycleMemo() })
	return d.crewLifecycleState
}

// The wake limit.

// chargeAutonomousWake books one wake nobody asked for against a member's
// allowance, refusing loudly past the limit. Called on every wake the user did
// not ask for — the nap that runs while they are away today, message-triggered
// wakes when crew addressing lands — and never on a wake from the sidebar or
// the CLI, which is the user asking.
//
// The stamps are written before the wake rather than after it, so a wake that
// fails still counts: a loop that fails eight times has spent eight primings
// exactly like a loop that succeeded.
func (d *Daemon) chargeAutonomousWake(memberID string, now time.Time) error {
	ledger := d.crewWakeLedger()
	var refusal error
	if _, err := d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		ledger.Stamps = parseWakeStamps(member.AutonomousWakes)
		kept, err := ledger.Allows(member.ID, now)
		if err != nil {
			refusal = err
			return false, nil
		}
		member.AutonomousWakes = formatWakeStamps(kept)
		return true, nil
	}); err != nil {
		return err
	}
	if refusal != nil {
		d.logf("crew: %v", refusal)
	}
	return refusal
}

func parseWakeStamps(raw []string) []time.Time {
	stamps := make([]time.Time, 0, len(raw))
	for _, value := range raw {
		at, err := time.Parse(time.RFC3339, value)
		if err != nil {
			// An unreadable stamp is not evidence a wake did not happen, so it is
			// dropped rather than counted: the alternative is a member locked out by
			// one bad row it cannot clear.
			continue
		}
		stamps = append(stamps, at)
	}
	return stamps
}

func formatWakeStamps(stamps []time.Time) []string {
	out := make([]string, 0, len(stamps))
	for _, at := range stamps {
		out = append(out, at.UTC().Format(time.RFC3339))
	}
	return out
}
