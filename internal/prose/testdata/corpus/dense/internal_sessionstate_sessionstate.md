Package sessionstate resolves what an agent session is doing from the evidence
collected about it.

Sources record what they saw, this package decides what that means, and a tick
re-runs the decision so evidence expires. Every clause that can hold a session
in a state depends on evidence that either refreshes or ages out, which is what
makes a stuck state impossible rather than merely unlikely — the failure of the
arrangement it replaced, where each source wrote a state name directly and a
session stuck whenever the source that would have moved it on never fired.

The package is pure — no daemon, store, or IO imports — so the rules are
table-tested directly instead of by standing up a daemon.

Source names where an observation came from. The resolver treats sources
differently, so this is not merely diagnostic.

SourceHeartbeat is the agent's own OSC 0 title glyph: a level, refreshed
while its turn runs.

SourceBracket is a hook opening or closing a turn or a tool call. The
primary level — the only signal that survives the multi-second title
silence claude produces in the middle of a blocking tool call.

SourceHarnessEvent is a one-shot harness announcement: an approval
request, Claude's Notification hook.

SourceProcess is the PTY process itself. A level with no expiry: an exited
process does not become un-exited.

Claim is what an observation asserts. Deliberately not a protocol state name:
a source reports what it saw, and only the resolver names a state. "The turn
is running" and "the session is working" are different statements, and
collapsing them is what let a heartbeat masquerade as an applied state.

ClaimSettled: the turn is over. It says nothing about why it ended — an
approval, a question, and a finished turn all settle.

ClaimNeedsInput: the agent is waiting on the user specifically, as opposed
to having simply finished.

ClaimParked: the stop-time judge read a yielded turn as waiting on its own
background work, not on the user. Only the background-work clause consumes
it — outside a yield the claim describes nothing.

ClaimStopFailed: the turn ended on an API error rather than on an answer.
Distinct from ClaimNeedsInput because the agent did not ask anything — it
was cut off — and a diagnosis that says so is worth more than one that
reports a question nobody asked.

ClaimTurnAborted: the user halted the turn. Distinct from every settle above
because nothing announces it — no agent fires a hook when its turn is
interrupted — so it is the one turn ending that must be read out of the
agent's transcript, and the one that leaves no answer to judge.

Evidence is everything the resolver may read about one session. The daemon
owns it and mutates it as observations arrive; the resolver only reads.

Levels (Heartbeat, TurnOpen, ToolOpen, Process) describe a condition
that holds until it changes. Edges (LastHarnessEvent, LastClassifier) are
one-shot facts that stay until superseded.

Heartbeat is the most recent title-glyph observation. Its freshness is
what bounds how long a stale bracket may lie.

TurnEverOpened: a turn has opened at least once in this session's life.
It is what separates "settled" from "has not started yet", which look
identical in every other field — a booting agent paints title frames, and
codex flickers a busy one before its first prompt is even ready.

BackgroundWork: the turn yielded with asynchronous work outstanding, so
it will auto-resume. Reported as a fact on the Stop payload.

PendingCron: the turn yielded with a scheduled wakeup that will resume it.
It is evidence that the turn is over — a wakeup is only ever learned from a
Stop — and not evidence about whether the user is wanted, so it names a
settle without suppressing one.

Compacting: the agent is rewriting its own context, between PreCompact and
PostCompact. It is work that paints no spinner frames and opens no turn, so
nothing else in this table can see it. Measured at 26s, which is long enough
that a compaction between turns reads as a session that finished.

ReviewerInLoop: something other than the user answers approval requests —
claude's permission classifier, codex's auto_review guardian. It does not
suppress an approval state; it decides how long that state must hold
before it is worth showing anyone.

LastBusyAt is when the heartbeat last said the turn was running. Staleness
is measured from here rather than from the latest heartbeat: claude blips
its not-busy glyph mid-turn (between tool calls, and while a foreground
tool is still running), so treating any non-busy frame as an immediate
settle would flip a healthy open turn to idle. Zero means the agent has
never reported being busy, which is not the same as having gone quiet.

PromptIdleAt is when the harness last confirmed the agent is sitting at its
prompt with nothing outstanding. Claude reports it via its Notification hook
60s after a settle nobody answered, once, cancelled if the user types first.

Not an Observation, because it carries no claim about *why*: it fires for a
finished turn exactly as for a question, so it cannot choose between idle and
waiting_input. What it is, is an independent witness that the agent is not
working — the one thing a lost Stop hook leaves attn unable to discover.

ClassifyingSince is when a stop-time classification started, zero when none
is running. It is the difference between "the turn settled and it is idle"
and "the turn settled and we are still finding out", which look identical
in every other field.

LastMovement is when any evidence last changed. A session whose evidence
has stopped moving entirely is stuck, which is a distinct condition from
any state it might be reported in.

Policy holds the timing constants. They are per-agent and measured, so they
are an input rather than package constants: a table test states the timing it
is testing instead of inheriting it.

HeartbeatTTL is how long a busy heartbeat keeps a session working on its
own, outranking every edge below it. It is a precedence window, not a
liveness one: it has to be short, because a busy frame that stays
authoritative too long suppresses the approval and question edges that are
announced precisely when the agent stops painting frames.

HeartbeatSettleAfter is how long busy frames must have stopped before their
absence is read as the turn having ended. It answers a different question
from HeartbeatTTL — "has it stopped for good" rather than "is it running
right now" — and so it must be sized against the agent's worst repaint gap
rather than its typical one.

StaleAfter is the heartbeat silence that closes a bracket whose closing
hook never arrived. It must exceed the longest silence the agent produces
mid-turn, or a slow tool call reads as a finished turn.

StuckAfter is total evidence silence — no source of any kind — after which
the session is reported stuck rather than left in whatever it last showed.

SettleGrace is how long past StaleAfter a stale bracket keeps its current
state instead of asserting idle, waiting for a late explanation.

ClassifierTimeout bounds how long a running classification may hold a
settle. Past it the session settles without the verdict, because a
classifier that never returns must not be able to freeze a color.

GuardianDwell is how long an approval must hold before it is published
when a reviewer is in the loop. It is not a delay on genuine approval
requests: with no reviewer the dwell is zero.

Measured on claude 2.1.220 and codex 0.145.0 driven through a real PTY.
Claude repaints its title about once a second and goes silent for up to ~3.5s
in the middle of a blocking foreground tool call; codex repaints about ten
times a second and never goes quiet mid-turn. Hence claude's TTL carries ~55%
margin over its repaint interval and codex's carries ~5x.

defaultHeartbeatSettleAfter is how long busy title frames must have stopped
before their absence settles a session with no bracket open. It is separate
from HeartbeatTTL because the TTL is sized to stay out of the way of approval
edges (pushing it down) and a settle must survive the agent's worst repaint
gap (pushing it up): claude repaints every ~1.92s during a `/compact`, past
the 1.5s TTL. Five seconds clears that with margin for PTY read batching, and
the latency only applies when the classifier declines to answer.

defaultStaleAfter is the heartbeat silence after which an open bracket stops
being believed. It is consulted only when a closing hook was lost, so the
trade is "how long a lost hook shows the wrong color" against "how sure we
are the turn ended". A minute is far past any mid-turn silence either agent
produces (claude's worst measured is ~3.5s inside a tool call); a lost hook
showing green that long is the cheaper failure, with stuck at 90s behind it.

guardianDwell is the round trip a guardian needs to answer in the user's
place. Measured: 90ms for claude's permission classifier, low seconds for
codex's auto_review. 60s is deliberately far above both — the cost of waiting
is a late notification, the cost of not waiting is a yellow flash on every
tool call of an unattended run.

defaultSettleGrace is the window past StaleAfter in which a stale bracket
holds rather than asserting idle: the instant a bracket stops being believed
is a bad moment to assert anything, because a late explanation is still
possible. Bounded on purpose — holding forever would reproduce the stuck
color it exists to avoid, and codex has no idle_prompt to unstick it.

defaultClassifierTimeout is generous on purpose. Overrunning it costs one
visible settle that a late verdict then corrects; undershooting it
reintroduces the flicker the gate exists to prevent.

shellHeartbeatTTL: a shell pane's heartbeat is the foreground process
group, polled once a second and re-emitted on the same 1s keepalive the
title observers use. 2.5s carries margin for one missed poll plus worker
RPC latency. The short agent TTLs exist to stay out of the way of
approval edges; a shell has no such edges, so its TTL is sized for
steadiness instead.

PolicyFor returns the timing for an agent. An agent with no measured numbers
gets the conservative end of each: a TTL short enough not to hold a session
working on a stale glyph, and a stale window long enough not to close a
bracket early.

Reason names why the resolver reached a state. It is what `attn state explain`
shows, and it is the difference between "the color is wrong" and a diagnosis.

Detail carries the winning observation's detail so a diagnosis does not
require re-reading the evidence table.

Hold means "keep whatever the session already shows"; State is empty when it
is set. A pure resolver cannot express "unchanged" any other way — it never
reads the current state — and every clause that holds is time-bounded.

Resolve decides what a session is doing. Pure: same evidence and same clock
always give the same answer.

The clauses are ordered, first match wins, and the order encodes trust rather
than recency. A fresh heartbeat outranks an open approval because the agent
cannot be blocked on the user while its turn is visibly running — that
combination means the approval was already answered and its closing edge was
lost.

A process that exited is terminal. Nothing below can outrank it, and no
amount of stale evidence should keep a dead session colored as alive.

The clause that rescues a lost turn-open hook: the agent is visibly
running, whatever the brackets say.

The harness said in so many words that the agent is blocked on a person.
That outranks every bracket below, because it is announced precisely when
the turn stops looking like it is running.

Nothing announces the answer — neither agent has a hook for it — so these
edges are retired by the agent going busy past them, in the clearing switch
in the daemon's recorder.

The user halted the turn. No agent reports this — the closing bracket is not
late, it is never coming — so without this clause the turn stays open until
the stale window retires it, and a halted agent reads as working for the
whole minute that takes.

It settles without consulting the classifier: an aborted turn left a
truncated fragment and no answer, and a verdict drawn from that reports a
question the agent never finished asking. It sits above compaction and
background work because an abort ends everything the turn had running, and
below the busy heartbeat because the agent visibly running again is the one
thing that contradicts it.

Compaction is work no other clause can see: it opens no turn and no tool
call, and its title frames have gaps wide enough to read as a finished turn.

This and the clause below both claim something is running on the strength of
a fact from a hook, so both expire on total silence — a lost PostCompact or
a task that resumed nothing must not pin the session green for good.

The turn yielded with work still running. The fact alone cannot say whether
anyone is waited on — "I'll continue when the build finishes" and "done, I
left the server running" yield identical payloads — so the stop is judged
with the yield in view, and the verdict outranks every guess below it.

A waiting/idle verdict means the judge read the ending as the user's turn:
the process still running is a leftover, not a reason the turn will resume,
so it settles (and rings) like any other ending. A parked verdict is the
opposite answer — the turn waits on its own work and will resume without
anyone — and it is affirmative evidence for the silence that follows: an
agent sitting out a background wait paints no frames and fires its
prompt-idle notification exactly as a finished one does, so neither total
silence nor the confirmation says anything the verdict has not already
answered. Parked therefore holds working without decaying to unknown —
`unknown` opens a turn, and ringing the user because a build is slow is the
failure this clause exists to remove. The hold is still bounded: the next
busy frame spends the verdict, and the next turn clears the yield.

With no verdict — the judge failed, declined, or is still out — the ladder
is the old one: hold working while the judgment may yet land, let the
harness's prompt-idle confirmation retire the yield (the safety valve that
keeps a wrongly-held session from staying green forever), and decay to
stuck on total silence.

The harness says the agent is parked at its prompt. That outranks an open
bracket, because a bracket closes on a hook that may never come and this is
a second hook, on a different trigger, saying the same turn is over. It sits
below the approval clause because an unanswered approval is also "parked at
the prompt", and approval is the more useful thing to say.

An open bracket says work is outstanding. Whether to believe it is exactly
what the heartbeat is for: a bracket whose closing hook was lost would
otherwise hold the session working for the rest of its life.

A bracket only outranks stuck while something is still arriving. For an
agent with hooks but no heartbeat, heartbeatSilentFor answers "not silent"
forever — it has nothing to have gone quiet from — so without this check
the stale test below cannot retire the bracket and stuck is unreachable.

The bracket is stale, which means the agent stopped painting frames — what
a finished turn and an approval nobody has announced yet both look like.
So hold for SettleGrace rather than assert idle into a late explanation,
and let a verdict that already landed end the grace early.

Brackets closed and no busy frames: the turn is over. This is what makes a
settle independent of any particular source — the classifier is allowed to
decline, and when it does nothing else contradicts the working state.

It needs a turn to have happened, not merely a busy frame: a booting agent
paints frames before its prompt is ready (codex flickers a busy one), and
settling on those reports a turn finished before the agent has taken one.

The latest frame still says busy and has only gone quiet for longer than
the TTL: a repaint gap, not a settle. An agent whose turn is over stops
painting busy frames for good, so waiting out HeartbeatSettleAfter tells
the two apart. Without it, every gap wider than the TTL settles and
un-settles the session — one owed turn per gap.

A wakeup is only ever learned from a Stop, so reaching here with one recorded
means a turn ended and no bracket reopened one — a settle on its own
evidence. It matters because the clause above needs a heartbeat and this does
not: a session reporting hooks without a title (headless, remote) would
otherwise resolve as though it had never spoken. It sits below the heartbeat
so a wakeup that already fired is described by the agent that is visibly
running rather than by the schedule that started it.

An agent that has never taken a turn and says it is not running is sitting at
its prompt. Saying so is the only thing that retires the `working` a session
is handed at spawn, since every clause above requires a turn to have opened.
The evidence is the agent's own title, not an absence of one: claude paints a
not-busy glyph once its prompt is ready, codex after its boot flicker.

Nothing has moved at all, which is its own diagnosis: a stuck session is
otherwise indistinguishable from a correctly-quiet one. It needs a turn to
have opened first — a launched-and-left-alone agent is silent because there
is nothing to report, and nothing would ever contradict a stuck verdict.

settled resolves a turn that is over, preferring the classifier's verdict and
falling back to the reason that got us here.

When no verdict has landed but one is being computed, it holds. The classifier
is what separates idle from waiting_input, and it takes seconds: publishing
idle first and correcting it on arrival turns every question-ending turn into
a visible green-then-yellow flicker. Holding is bounded by ClassifierTimeout,
so a classifier that dies still settles the session.

A registered wakeup does not change the answer. This table exists to say
whether the agent needs the user, and a cron does not answer that either way,
so it names why the session settled and nothing more. Suppressing the queue is
a user control — a pinned workspace — not something inferred from a schedule.

ClassifierVerdictPending reports whether a classification is running and
still worth waiting for. Consumers outside the resolver use the same bounded
definition when they must distinguish confirmed work from a working state the
resolver is temporarily holding while the verdict arrives.

harnessEdge reads the harness's own announcement that the agent is blocked on
a person, if one is still outstanding.

The two edges differ only in what they say and what answers them, so they
share a clause rather than being ordered against each other: a turn cannot be
blocked on an approval and on a question at the same time, and whichever
arrived last is the one still outstanding.

A turn cut off by the API is blocked on a person as surely as a question
is: the error is a rate limit to wait out, a bill to pay, or a login to
renew, and the agent will not produce anything until one of those
happens. Reported as waiting on the user because that is what it is —
the detail carries which error, which is the part worth reading.

Retired like the others, by the agent going busy again. That is the right
edge whether the user fixed the cause or simply re-prompted.

turnAborted reports whether the harness recorded the user halting the turn,
with nothing since to spend that.

It shares LastHarnessEvent with the edges above rather than taking a field of
its own: an abort is a one-shot harness announcement like the others, it
retires on the same terms — the agent going busy past it, or the next turn
opening — and it cannot coexist with them, because a turn the user halted is
not also blocked on an approval. harnessEdge ignores the claim, so the two
readers of the slot cannot both fire.

parkedVerdict reports whether the stop-time judge read the current yield as
the turn waiting on its own background work rather than on the user. Spent
the same way every verdict is: by the agent going busy past it — which for a
parked turn is precisely the resume it predicted.

classifierVerdict reads the stop-time verdict, if one belongs to the current
turn. It answers only the two claims that name a state; parked is read by the
background-work clause alone, because outside a yield it describes nothing.

A verdict describes the turn it was computed for and nothing else. The turn
bracket clears it when the next turn opens, but a turn may also start without
its hook — surviving that is the whole reason the heartbeat is here — so this
also drops a verdict the agent has since gone busy past. Otherwise a turn that
settles while its own classification is still running takes the previous
turn's answer instead of holding for its own.

DwellFor is how long a transition into state must hold before it is published.

It is keyed on who is being asked first. With a guardian in the loop, an
approval request is addressed to the guardian, and showing it to the user
immediately produces a flash of attention-demanding color on every tool call
of an unattended run. With no guardian the user *is* the reviewer, so the
dwell is zero and a genuine request is not delayed by a millisecond.

supersededByBusy reports whether the agent has painted a busy frame since o
was observed.

Both edges this retires — an unanswered approval and a stop-time verdict —
describe a moment the agent was *not* running. A later busy frame is proof it
moved on, and neither edge has an announcement of its own to expire on.

fresh reports whether o makes claim and is recent enough to still be believed.

heartbeatSilentFor reports whether the agent has stopped saying it is busy for
longer than d.

It reads LastBusyAt, not the latest heartbeat: claude blips its idle glyph
mid-turn, so only the absence of busy frames for a full window counts. An agent
that has never reported being busy is not silent — one with no harness signals
must not have its brackets closed out from under it.
everTookATurn reports whether this session has been seen doing anything at all.

The classifier counts alongside the brackets: a verdict only exists because a
turn ended, and it outlives the bracket that produced it. A daemon restarted
mid-turn leaves exactly that shape — judged, with no bracket to show for it.

Deliberately not the heartbeat's question: that one asks whether the agent is
painting frames, which stops routinely mid-turn, while this asks whether
anything at all has been heard from. A frozen table means a lost session, not
whatever state it last showed.

promptIdleConfirmed reports whether the harness has confirmed the agent is
sitting at its prompt, and nothing has happened since to spend that.

LastBusyAt is the guard, not the 60s the notification happens to use: if the
agent painted a spinner after the confirmation, a new turn started and the
confirmation is spent. Nothing here breaks if claude retunes the timer.
