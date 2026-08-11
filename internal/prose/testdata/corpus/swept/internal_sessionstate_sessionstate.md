Package sessionstate resolves what an agent session is doing from recorded
evidence. Every clause that can hold a state depends on evidence that either
refreshes or ages out, which is what makes a stuck state impossible. Pure —
no daemon, store, or IO imports — so the rules are table-tested directly.

Source names where an observation came from; the resolver treats sources
differently, so this is not merely diagnostic.

SourceHeartbeat is the agent's OSC 0 title glyph: a level, refreshed while
its turn runs.

SourceBracket is a turn/tool hook — the only signal that survives claude's
mid-tool-call title silence.

Claim is what an observation asserts — deliberately not a protocol state
name: a source reports what it saw, and only the resolver names a state.

ClaimParked: a yielded turn is waiting on its own background work. Only the
background-work clause consumes it.

ClaimStopFailed: the turn was cut off by an API error rather than asking
anything — distinct from ClaimNeedsInput.

ClaimTurnAborted: the user halted the turn. No agent announces it, so it is
read out of the transcript and leaves no answer to judge.

Evidence is everything the resolver may read about one session; the daemon
owns and mutates it, the resolver only reads. Levels (Heartbeat, TurnOpen,
ToolOpen, Process) hold until they change; edges (LastHarnessEvent,
LastClassifier) are one-shot facts that stay until superseded.

Heartbeat is the most recent title-glyph observation; its freshness bounds
how long a stale bracket may lie.

TurnEverOpened separates "settled" from "has not started yet" — a booting
agent paints title frames (codex flickers a busy one before its prompt).

PendingCron: the turn yielded with a scheduled wakeup. Evidence the turn is
over, not about whether the user is wanted.

Compacting: between PreCompact and PostCompact — work that paints no frames
and opens no turn, so nothing else here can see it. Measured at 26s.

ReviewerInLoop: something other than the user answers approvals. It does
not suppress the approval state, only how long it holds before being shown.

LastBusyAt is when the heartbeat last said the turn was running; zero means
never. Staleness is measured from here, not the latest heartbeat: claude
blips its not-busy glyph mid-turn, so any non-busy frame read as a settle
would flip a healthy open turn to idle.

PromptIdleAt is when the harness last confirmed the agent sitting at its
prompt. It carries no claim about why, but it is an independent witness the
agent is not working — the one thing a lost Stop hook hides.

ClassifyingSince is when a stop-time classification started, zero when none
runs: "settled and idle" vs "settled and still finding out".

LastMovement is when any evidence last changed; a frozen table means a
stuck session, distinct from any state it might be reported in.

Policy holds the timing constants: per-agent and measured, so an input rather
than package constants.

HeartbeatTTL is a precedence window, not a liveness one: it must be short,
or a busy frame suppresses the approval/question edges announced exactly
when the agent stops painting.

HeartbeatSettleAfter is how long busy frames must have stopped before their
absence reads as a settle — sized against the worst repaint gap.

StaleAfter is the heartbeat silence that closes a bracket whose closing hook
never arrived; it must exceed the longest mid-turn silence.

SettleGrace is how long past StaleAfter a stale bracket holds instead of
asserting idle, waiting for a late explanation.

ClassifierTimeout bounds how long a running classification may hold a
settle; a classifier that never returns must not freeze a color.

GuardianDwell is how long an approval holds before publishing when a
reviewer is in the loop; with no reviewer the dwell is zero.

Measured on claude 2.1.220 and codex 0.145.0 through a real PTY: claude
repaints its title ~1/s and goes silent up to ~3.5s inside a blocking tool
call; codex repaints ~10/s and never goes quiet mid-turn. Claude's TTL
carries ~55% margin over its repaint interval, codex's ~5x.

Measured: claude repaints every ~1.92s during a `/compact`, past the 1.5s
TTL; 5s clears that with margin for PTY read batching.

Consulted only when a closing hook was lost. A minute is far past any
measured mid-turn silence (claude's worst ~3.5s).

Measured 90ms for claude's permission classifier, low seconds for codex's
auto_review; 60s is far above both — not waiting means a yellow flash on
every tool call of an unattended run.

Bounded on purpose: holding forever reproduces the stuck color it avoids,
and codex has no idle_prompt to unstick it.

Generous on purpose: overrunning costs one visible settle a late verdict
corrects; undershooting reintroduces flicker.

A shell pane's heartbeat is the foreground process group on the 1s
keepalive; 2.5s covers one missed poll plus worker RPC latency. A shell has
no approval edges, so this is sized for steadiness, not precedence.

PolicyFor returns the timing for an agent; an unmeasured one gets the
conservative end of each constant.

Reason names why the resolver reached a state; shown by `attn state explain`.

Hold means "keep whatever the session already shows" (State empty): a pure
resolver never reads the current state, and every holding clause is
time-bounded.

Resolve decides what a session is doing. Pure: same evidence and clock, same
answer. Clauses are ordered, first match wins, and the order encodes trust — a
fresh heartbeat outranks an open approval because an agent visibly running
cannot be blocked on the user.

Blocked on a person, announced exactly when the turn stops looking like it
runs, so it outranks every bracket below. Nothing announces the answer:
these edges retire only by the agent going busy past them.

A halted turn's closing bracket is never coming. It settles without the
classifier (no answer to judge) and sits above compaction/background work
but below the busy heartbeat, which is what contradicts it.

Work no other clause can see. This and the clause below expire on total
silence — a lost PostCompact must not pin the session green for good.

The turn yielded with work still running; the payload alone cannot say
whether anyone is waited on, so the stop-time verdict outranks every guess
below. A parked verdict is affirmative evidence for the silence that
follows, so it holds working WITHOUT decaying to unknown (unknown opens a
turn); it is still spent by the next busy frame. With no verdict, hold
working while judgment may land, let prompt-idle retire the yield, and
decay to stuck on total silence.

Outranks an open bracket (a second hook on a different trigger saying the
turn is over) but sits below approval: an unanswered approval is also
"parked at the prompt".

An open bracket says work is outstanding; the heartbeat decides whether to
believe it, or a lost closing hook holds the session working forever.

For an agent with hooks but no heartbeat, heartbeatSilentFor answers
"not silent" forever; without this check stuck is unreachable.

A finished turn and an unannounced approval look the same, so hold for
SettleGrace rather than assert idle into a late explanation; a verdict
that already landed ends the grace early.

Brackets closed and no busy frames: the turn is over. Needs a turn to have
happened, not merely a busy frame — a booting agent paints frames before
its prompt is ready.

A gap only longer than the TTL is a repaint gap, not a settle; without
HeartbeatSettleAfter every wide gap costs one owed turn.

A wakeup is only learned from a Stop, so one recorded here is a settle on
its own evidence. It needs no heartbeat: a session reporting hooks without
a title (headless, remote) would otherwise read as never having spoken.

Never took a turn and says it is not running: at its prompt — the only
thing that retires the `working` handed out at spawn. The evidence is the
agent's own not-busy title, not an absence of one.

Needs a turn to have opened first: a launched-and-left-alone agent is
silent because there is nothing to report, and nothing would ever
contradict a stuck verdict.

settled resolves a turn that is over, preferring the classifier's verdict and
holding (bounded by ClassifierTimeout) while one is computed: publishing idle
first and correcting on arrival flickers green-then-yellow. A registered
wakeup does not change the answer — suppressing the queue is a user control,
not something inferred from a schedule.

ClassifierVerdictPending reports whether a classification is running and
still worth waiting for; exported so outside consumers share the same bound.

harnessEdge reads an outstanding "blocked on a person" announcement. The edges
share one clause: a turn cannot be blocked on an approval and a question at
once, and the last arrival is the one outstanding.

A turn cut off by the API is blocked on a person as surely as a question
is (rate limit, bill, login); the detail carries which error.

turnAborted reports a recorded halt with nothing since to spend it. It shares
LastHarnessEvent with harnessEdge, which ignores the claim, so the two readers
of the slot cannot both fire.

parkedVerdict reports a stop-time verdict of "waiting on its own background
work", spent by the agent going busy past it — the resume it predicted.

classifierVerdict reads the stop-time verdict belonging to the current turn;
parked is read by the background-work clause alone. A verdict the agent has
gone busy past is dropped, or a turn settling mid-classification would take
the previous turn's answer.

DwellFor is how long a transition into state must hold before publishing: with
no reviewer in the loop the user IS the reviewer, so the dwell is zero.

supersededByBusy reports whether the agent painted a busy frame since o. The
edges this retires describe a moment the agent was not running and have no
announcement of their own to expire on.

fresh reports whether o makes claim and is recent enough to still be believed.

everTookATurn counts the classifier alongside the brackets: a daemon restarted
mid-turn leaves exactly that shape — judged, with no bracket to show for it.

evidenceStoppedMoving reports whether every source has gone quiet for d —
deliberately not the heartbeat's question, which stops routinely mid-turn.

promptIdleConfirmed guards on LastBusyAt, not the 60s the notification happens
to use, so nothing breaks if claude retunes it.

heartbeatSilentFor reads LastBusyAt, not the latest heartbeat (claude blips its
idle glyph mid-turn). An agent that never reported busy is not silent — one
with no harness signals must not have its brackets closed out from under it.
