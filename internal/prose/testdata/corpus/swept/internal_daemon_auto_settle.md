Auto-settle closes a turn the user has already dealt with by steering the
agent back to work: an invisible arm delay ("did the steering take?"), then a
visible countdown ("does the user want to stop this?"). Timers live in the
daemon because a client-side one would race the broadcast that feeds it.

defaultAutoSettleArmSeconds is how long a session must hold `working`
before the countdown starts.

defaultAutoSettleCountdownSeconds is how long the visible countdown runs
before the turn is settled.

autoSettleHoldQuietWindow is the quiet time before a held countdown
resumes. Deliberately not a setting: it only has to span gaps in active
typing, and it resumes into a visible, cancellable countdown.

Floors keep the arm delay past the resolver's own settle latency
(HeartbeatSettleAfter, 5s), so no turn closes on an agent it is still
deciding about; ceilings are fat-finger guards.

autoSettleHeld: the user is interacting; the pending timer is a quiet check
that re-holds or resumes. No deadline rides the wire, `auto_settle_held` does.

autoSettleTimer is a session's pending auto-settle. firesAt exists because
time.Timer exposes no deadline accessor; resume is the phase a hold came from
and returns to, so activity during the arm delay cannot promote to counting.

visible reports whether clients can see this entry — only a countdown,
running or frozen — and so whether it owes a broadcast.

autoSettleConfig is the resolved policy. Read from settings on every arm so a
change takes effect on the next transition without invalidation bookkeeping.

resolveAutoSettleSeconds turns a stored setting into a duration. Validation
rejects out-of-range values at write time; this only has to be total.

syncAutoSettle runs from applyState on every committed transition, so the
rule is exhaustive: only `working` sustains a pending settle, anything else
cancels — which keeps a future state safe by default. No debounce:
internal/sessionstate already absorbs the flickers it knows about.

armAutoSettle starts the arm delay if the feature applies and nothing is
pending. Leaving an existing timer alone makes a re-reported `working`
harmless — restarting the delay each time would mean it never elapses.

turnOwed carries the shell/chief/pinned/muted exclusions too, so those are
out for the same reason they are out of the queue.

holdAutoSettle freezes a pending settle on user interaction. Any keystroke
counts — noteUserInput already drops attn's own automation and replay writes.
A hold is not a cancel: it expires on its own after the quiet window.

Already-held keeps a keystroke burst free: the quiet check reschedules
from the recorded keystroke time, so only the first key does work here.

startAutoSettleHeldLocked parks the session in the held phase with a quiet
check `window` away. Caller holds autoSettleMu.

startAutoSettleLocked replaces whatever is pending with a fresh timer in the
given phase. Caller holds autoSettleMu. The ready channel (same handshake as
nudge_countdown.go) blocks the closure until `timer` is published, so the fire
path's identity check reads a fully written value on a zero-length window.

stopAutoSettleLocked cancels and forgets a session's pending settle,
reporting whether the removed entry was visible (a rebroadcast is owed).
Caller holds autoSettleMu.

cancelAutoSettle drops a pending settle. Broadcasts only when a visible
countdown was cancelled, so the arming phase stays silent on the wire.

clearAutoSettleState drops a removed session's timer without broadcasting; the
removal's own sessions-updated follows.

stopAutoSettleTimers cancels every pending settle so no AfterFunc goroutine
outlives daemon teardown.

autoSettleFire advances or completes a pending settle. The identity check
against the map entry keeps a timer that lost a cancel/replace race from
acting; both phases re-check their preconditions rather than trust them.

A re-hold, or a hold of the invisible arm delay, fires every five seconds
while the user types — no broadcast for a picture that never moved.

runAutoSettle is the fire-time decision, separated so its outcome is a single
string a test hook can assert. `resume` is read only in the held phase.

Held across the whole decision so no state write lands between the check
and the settle — otherwise the timer could settle a turn a transition had
just opened. applyState takes the same lock around its store write.

The resolver projects `working` while a stop-time verdict is computed, and
that must not close the turn. recordClassifierStarted usually cancels the
timer; this closes the race where the callback already took it.

Re-checked here as well as in syncAutoSettle: catches a state that moved
without a committed transition (restart mid-window, a write outside applyState).

A phase that only moves the timer along can take the interaction hold as a
plain check: nothing commits on the far side of it.

Quiet again. The window restarts full: a frozen bar is drawn full,
so resuming anything less would drop the bar on release.

Test seam for the instant before the settle commits: the dangerous
interleaving (a keystroke arriving with the timer already out of the map)
is too narrow to hit by chance. Nil in production.

The activity hold is re-asked because it must be indivisible from the write
it guards (settleIfAutoSettleQuiet holds the activity lock across both):
the timer can fire in the microseconds around an activity report.

holdFromFire parks a session the fire path found the user interacting with,
with a quiet check exactly at the end of the window their last activity opened.

cancelAutoSettleByUser stops the countdown and does not re-arm (see
CancelCountdownMessage), reporting whether anything was pending.

Suppression makes the cancel stick: without it the next `working`
re-report re-arms. Cleared when the session leaves `working`.

No broadcast here: handleCancelCountdown makes exactly one after every
countdown on the session is called off.

decorateSessionWithAutoSettle stamps the broadcast clone with the countdown
deadline or the held flag, never both. Callers must not hold autoSettleMu.

turnOwed reads the decorated clone rather than re-deriving, so the timer and
the queue can never disagree about who is owed.

autoSettleSuppressedFor reports whether a user cancel is still standing for
this session.

clearAutoSettleSuppression lifts a standing cancel when the session leaves
`working` — the edge that makes the next steer a new decision.

cancelAllAutoSettle drops every pending settle. Used when the feature is
switched off: a countdown already on screen must stop, not run out.

armAutoSettleForRunningSessions arms every already-qualifying session when
the feature is turned on — the one moment there is no transition to react to.
