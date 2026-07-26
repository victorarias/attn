# Vision: the agent queue

## End state (the why)

The app is called **attn**, for attention. Human attention is the thing it exists
to protect, and every version of the product is an answer to the same question:
where should you be looking right now? The color system was the first answer —
teach the sidebar to say something true about each agent, and let the user read
it. This is the second. It is not a feature bolted onto the app; it is the app's
own thesis, taken a step further.

You sit down with eight agents running. The sidebar hands you one. You read what
it did, think, steer it, and it hands you the next. Then the next. Then it tells
you there is nothing — every agent on the board is busy and none of them wants
you.

**Being handed the next thing, instead of choosing it, is the product.** The
empty list at the end is the reward; not having to hunt is the part that pays out
on every single interaction before it. Today the sidebar is a monitor: a wall of
colors you scan and interpret, and the only way to know whether you are needed is
to look at everything and decide. Better detection makes those colors true — it
does not stop you jumping between agents looking for work. That hunt is pure
overhead, and it is charged against the same budget as reading what an agent
actually did, steering it somewhere good, thinking, or resting. Human attention is
the scarcest thing in the system, and the product currently spends it on
bookkeeping.

Tomorrow the sidebar is a queue you drain. Agents become dynamic todos that put
themselves on your list when they need you and take themselves off when they
don't — a todo list that writes and clears itself, where the only work left on it
is work only you can do.

The unit is not the agent. It is **the turn you owe.** Every state we detect
already answers that question: waiting for input, waiting for approval, stuck,
crashed — your turn. Working, scheduled — its turn. The queue is just that
question, asked continuously and answered out loud.

The vocabulary is one word in two forms. An agent that owes you nothing is
**settled**, and that is the name of the band it sits in — working, snoozed, and
muted agents are all settled, differing only in why. Most agents settle
themselves: the moment one starts working, the turn is no longer yours. **Settle**
is also the verb you press, the single intentional act that takes a turn off your
plate. It covers both reasons you would ever want that — you are done with this
one, or the queue was wrong about it — because from your side those are the same
gesture, and nothing downstream needs to tell them apart.

## North-star principles

- **One question, asked well: whose turn is it?** Everything the queue shows is
  an answer to that. If a signal doesn't change whose turn it is, it doesn't
  belong in the queue.
- **Spend the user's attention on steering, never on choosing.** Picking the next
  agent is overhead drawn from the same budget as the thinking that produces good
  outcomes. Every design call should ask which side of that line it puts the
  effort on.
- **An empty queue must mean you can walk away.** This is the honesty test the
  whole feature lives or dies by. It forbids accumulating badges, historical
  tallies, and anything that enters the queue but can't be settled by a single
  action. How long a turn has been owed is fair game — that is a property of the
  live turn and it dies with it. A count of turns you have taken is not.
- **Moving on is the user's call, until they say otherwise.** The right moment to
  leave an agent varies — sometimes you want to watch it start, sometimes you're
  mid back-and-forth, sometimes you're done the instant you hit enter. So the
  primary mechanic is a shortcut you press when you're comfortable. Automating it
  is an accelerant layered on top, opted into separately.
- **One agent that wants you is one row.** Not one workspace, not one group with a
  count — the row you click is the turn you owe, and clicking it puts you in that
  agent. This is a real departure from the sidebar today, where the row is a
  workspace and agents are panes inside its layout, and it is the departure the
  whole feature rests on.
- **Predictable beats clever.** Most recently unsettled, first. Everything in the
  queue is blocked, so ranking by cause makes position unguessable — but recency is
  something you already hold in your head, and the thing that just came back to you
  is the thing you have context for. Position is knowable without being memorized.
- **Drain a workspace before leaving it.** Switching workspace is the expensive
  context switch, not switching agent. This is a rule about where moving on takes
  you, and nothing else: if another agent in the workspace you are already in wants
  you, that is the next one, and you leave only once none of it is your turn. The
  queue itself stays a flat list of agents with no workspace grouping in it, so
  moving on can legitimately land somewhere other than the top row.
- **The queue reorders; it never hides.** "Your turn" is a promotion out of the
  workspace tree, not a filter over it. Detection can fail in the direction the
  user cannot recover from — an agent that needs you and never enters the queue —
  and the only defence is that everything is still findable where it has always
  been.
- **The sidebar has a spine, and the queue is one band of it.** The chief sits at
  the top, always, outside the queue — it is the console you drive the rest from,
  not a peer competing for a slot, and when it blocks on you it says so where it
  stands rather than being promoted into competition with the work it dispatched.
  Then what is your turn. Then the workspaces you pinned, present whether or not
  they want anything. Then the settled rest, quiet but reachable. Muted last, and
  only if you go looking. The bands are the one thing you hand-order, and you can
  drag them into the arrangement you want. Nothing inside the queue is
  hand-orderable: its order is the answer to a question, not a preference.
- **Explicit intent outranks inference.** Pin, mute, and snooze are the user saying
  what matters, and detection never overrides them. This is what makes the queue
  safe to trust: when it is wrong, the user can be right, permanently, in one
  gesture.
- **Looking is never acting.** Any agent can be opened and read at any time,
  whatever band it sits in, and doing so changes nothing about whose turn it is.
  Nothing ever leaves the queue because it was seen. A turn ends by being settled
  — steering it, approving it, snoozing it, or pressing settle — and "the user's
  eyes passed over this" is not any of those. Clearing by incidental viewing is
  the cheap mechanism that looks like it works and quietly loses things.
- **The user is the ground truth, and settle is how they say so.** The system can
  never conclude that it was wrong — if it could, it would be right. Only the user
  knows. Settle is that assertion, and it costs one keystroke, so being wrong is
  never expensive. It is worth noticing where it lands: a settle on a turn we
  could not explain is the only labelled detection failure we will ever get for
  free.
- **Handed an agent means handed the keyboard.** Arriving at a turn puts focus in
  that agent's terminal, every time, so the next thing you do is type the thing you
  came to say. A queue that hands you the right agent and then makes you click into
  it has spent your attention on exactly the kind of bookkeeping it exists to
  remove. This is not polish; it is the difference between a queue and a list.
- **The daemon owns the queue; the app is a sensor.** Whose turn it is, countdowns,
  deferrals, and promotions are daemon state, resolved from evidence. The app
  reports what only it can see — that the user typed, scrolled, clicked — as more
  evidence. Nothing about the queue lives only in a window.
- **Deferral is honoured; muting is absolute.** Snoozing says *not now* — a
  considered act that business as usual does not undo, though what the user could
  not have anticipated still breaks through: errors and states we cannot explain.
  Muting says *not ever*, and nothing breaks through it, not even a crash. The two
  are different verbs, not two lengths of the same one.
- **The empty state is never busywork.** When nothing wants you, the product does
  not manufacture a dashboard to occupy the space it just freed. It may eventually
  earn something real there — see below — but the bar is that it be worth your
  attention, not that the space be full.

## Scope & non-goals

**In scope.** Agent sessions only. The queue holds turns you owe an agent, and the
existing session states are its entire vocabulary. The sidebar's standing order —
chief on top, then your turn, then pinned workspaces, then the settled rest, then
muted. Settle as the one intentional discharging act, available on any turn.
Deferral (time-boxed snooze, broken only by errors and states we cannot explain),
muting as its absolute sibling, pinning as its inverse, a shortcut for moving on
to the next agent that wants you, unhurried exploration of settled agents, and a
designed empty state.

An agent whose run ended owes you a turn like any other. It is not running and it
is not going to do anything else, but its output is exactly the thing you need to
read, and it leaves the queue the same way everything does — by being settled, on
purpose. A finished run that silently settles itself is how you lose
forty minutes of work.

Shells are not in the queue. A workspace holding only shells can never owe you a
turn, so it has no queue presence at all — it lives in the settled band like any
other workspace nothing is asking of, and it can be pinned up if it matters. A long
build that just finished is genuinely something you were waiting on, and it is
deliberately not a turn: admitting it would make "your turn" mean two things, and
the session states are the vocabulary.

**What this costs, deliberately.** Today's sidebar is purely spatial — workspaces
sort by a rank the user owns and nothing else ever moves them, which is what makes
⌘1–9 and ⌘↑/⌘↓ into positional muscle memory. A queue that reorders under you
cannot keep that, and in queue mode the numbered shortcuts stop addressing
positions in the list. They keep working for the things that never move: the chief
and pinned entries. Navigating the queue itself is a separate gesture that moves
you through it without settling anything, so looking ahead never costs you your
place. Selection stays legible the way it already is — unfocused agents dim, and
the selected row can carry its own edge.

**Depends on, but is not.** The queue is only as good as the states beneath it,
and one gap in particular is load-bearing: an agent that has gone quiet — no tool
calls, no terminal output, nothing — for long enough is very likely stuck, and
today nothing concludes that. That is a hole in state detection, and it gets
fixed there. The queue inherits the fix; it must not grow its own private theory
of liveness on the side.

**Out of scope, for now.** PRs and workflow runs. They also want your attention,
but their clearing rules are different in kind and folding them in early would
make "your turn" mean two things at once. The ⌘K attention drawer keeps them and
keeps its reason to exist. The queue should be built so they *could* fold in
later, without that being the first move.

**Explicitly not.** A task tracker, a history, an inbox, or a notification centre.
No unread counts, no read/unread state, no record of turns you already took. The
queue is a live picture of the present, and it forgets.

**Shipping posture.** Opt-in mode first, default later. The state-detection work
that makes this possible ships off by default; the queue earns the default once
detection is trusted in real use, not before. Automatic move-on is opted into
separately again, on top of the queue — it is an experiment about feel, and it
should be possible to live in the queue without ever turning it on.

## Big rocks (the arc)

- [x] **Trustworthy state detection.** The evidence-table resolver and
  harness-owned hook/heartbeat signals. Without near-perfect states the queue is
  a lie, and this is what makes "your turn" mechanically knowable.
- [ ] **The queue itself.** The sidebar rendered as *Your turn* / *Settled*, one
  row per agent that wants you, most recently unsettled first, behind a toggle. The single collapse of
  today's several competing notions of "needs attention" into one — including its
  earliest ancestor, the long run flagged for review. That flag was a first glimpse
  of this vision built as a thin slice: it never encoded the full state, and it
  clears by being seen, which this vision rejects. It should be absorbed into the
  queue and settled like anything else.
- [ ] **Settle.** One keystroke that takes a turn off your plate, on any agent, for
  either reason — done with it, or the queue was wrong. The act that makes the
  whole thing safe to trust, because it makes being wrong cheap.
- [ ] **The standing order.** Chief anchored on top and able to say it is blocked
  without leaving its slot, pinned workspaces held up, muted pushed out of sight —
  the user-controlled bands the queue lives between, and the guarantee that
  anything not in your face is still one click away.
- [ ] **Move on.** A shortcut that takes you from the agent you just steered to the
  next one that wants you — a sibling in the workspace you are already in when
  there is one, the top of the queue when there is not. The core verb of the whole
  product.
- [ ] **Automatic move-on.** The same thing on a timer, held off by user activity —
  a keystroke, a scroll, a click — reported by the app, with a visible countdown in
  the agent's terminal in the nudge animation's grammar, moved from the sidebar to
  the main screen. The same surface says so when you return to an agent you did not
  choose, fading on your first keystroke, so a swap that happened while you were
  away never reads as a glitch. Deliberately unplanned: it gets its own plan later,
  after the manual verb has been lived with, because whether a timer is wanted at
  all is something a few weeks of pressing the shortcut answers for free. This rock
  can still be dropped.
- [ ] **Deferral.** Snooze with real durations (30m, 1h, 8h, tomorrow, Saturday,
  Monday, custom), waking to the tail of the queue, broken early only by errors and
  unexplained states. And its sibling: indefinite backgrounding for agents that
  should never queue at all.
- [ ] **The empty state.** What the product says and shows when nothing wants you —
  starting as nothing, and eventually perhaps something.
- [ ] **Default-on.** Flip it once the queue has earned belief.

## Open questions

**Does automatic move-on feel good at all?** Genuinely unknown, and deliberately
not answered on paper or planned for in this arc — it gets its own plan later. The
manual shortcut ships first and gets lived with, and where it chafes is the
specification for the timer. If it never chafes, there is no timer. The shape when
it comes: a ~60s delay with the last ~15s visible as a countdown, held off by
activity the app reports. That the daemon cannot tell user input from anything else
crossing the PTY is why the app has to be the one to say so; it is a real signal to
build and maintain, and it should not be built until the feature it serves has
earned it.

**What the empty queue eventually becomes.** Draining the queue is the win, but a
product whose reward for succeeding is a blank panel has a hole in it. There is
something on the other side of "nothing wants you" — plausibly a version of grid
mode, many agents watchable at once, the shift from steering one at a time to
seeing the whole board move. That is a later chapter, and naming it now mostly
guards against filling the space with something worse in the meantime.

**Blindspot — what this feels like at rest.** The design reasoning here is about
motion: things entering, leaving, counting down. Much less is known about the
steady state of living inside this all day, especially the transition from a
spatial sidebar you've memorized to a list that reorders under you. Worth a
grounding pass on the actual sidebar interaction model — session switching,
workspace grouping, what muscle memory exists today — before the queue chunk
starts.

**Naming.** *Your turn* and *settled* are the two halves, and *settle* is the verb.
The feature itself is still not called "attention mode."
