The evidence table: what each source has said about a session, so the resolver
can weigh them instead of the last writer winning. Sources write here and
nowhere else, and the tick re-justifies state from live evidence every second,
so no state gets stuck when its sources go quiet.

evidenceTickInterval is how often every session's evidence is re-resolved.
The tick is what makes evidence expire when a source stops speaking.

updateIf mutates one session's evidence when admit says it is live, stamping
LastMovement. admit runs INSIDE the table's lock: checked outside, a writer
could pass liveness, lose to a removal, and recreate an orphan entry.

snapshot returns a copy of one session's evidence, or false when nothing has
been recorded — a copy, so the resolver never reads a table being mutated.

evidenceRecordGateHook (tests only) runs inside the table's lock, between the
live-row check and the write — the seam a removal must interleave into.

recordEvidence is the single write path into the evidence table. Every source
goes through it so the liveness gate cannot be forgotten at one call site.

recordPTYEvidence files an observation from the PTY layer; levels (heartbeat)
and edges (approval) land in different fields and age differently.

Codex announces an approval in its title — still a level: the title
holds, unrepainted, until the prompt is answered.

LastBusyAt only advances on a busy frame: staleness is measured from
the last time the turn was seen running, not the last time the agent
said anything.

recordBracketEvidence files a hook opening or closing a turn. The brackets
are the only signal that survives claude's multi-second title silence inside
a blocking tool call.

A stale verdict judged the previous turn; left in the table the
resolver would report it the moment this turn settles.

A turn cannot open while the previous one is blocked on a person, so
an edge still sitting here was answered.

Backstop for a lost PostCompact: compaction cannot still be running
while a turn is being worked.

These describe how the LAST turn yielded; left behind, outstanding
background work pins the session working with only silence to unpin it.

A question to the user is filed like an approval request — blocked on
a person — and retired the same way. The brackets alone cannot express
it: closing them resolves to idle and loses the question.

recordTranscriptEvidence files what the transcript watcher read. Copilot only:
it has no hooks, so its transcript is where its brackets come from, phrased as
states.

recordTurnAbortedEvidence files the transcript's record that the user halted
the turn. Brackets are closed too — an open one outlives the edge and resolves
as stuck mid-turn. abortedAt (agent-dated) and observedAt (read time) stay
separate so a late-read halt is outranked by later busy frames.

recordTurnBracketClosedEvidence closes the brackets and says nothing else: for
copilot abandoning a turn, where a halt would invent a user action.

recordStopFacts files what the Stop hook reported about why a turn yielded.

recordReviewerEvidence files who answers approval requests — a level, holding
until a different arrangement is reported. Two sources: the spawn route
(codex's only one) and claude's per-turn permission mode.

recordReviewerEvidenceFromPermissionMode files claude's reported mode. An
absent mode (older CLI) is not a report, and a mode from an agent whose mode
does not govern approvals must be ignored: codex sends `default` as filler,
which would retire the spawn-time fact on its first turn.

permissionModeGovernsApprovals: claude's mode decides who answers approvals;
the others state their arrangement at launch and never revise it.

recordNotificationEvidence files claude's Notification hook.
permission_prompt is a leading edge and becomes an approval claim.
idle_prompt deliberately becomes no claim: it fires for finished turns and
questions alike, so it can say "not working" but not what instead.

recordStopFailureEvidence files claude's StopFailure hook (turn ended on an
API error) as a harness edge, not a stop: nothing to classify, the session is
blocked on a person, and the agent going busy again retires it.

recordCompactionEvidence files claude's PreCompact/PostCompact pair as a
level; no other source reports compaction.

recordProcessEvidence files the PTY lifecycle. An exited process is terminal
and outranks every other clause.

recordClassifierStarted marks a classification as running, so a settle waits
for the verdict instead of publishing idle and correcting it seconds later.

Suspend auto-settle until the verdict lands; the fire path repeats this
check to close the race with a timer that already left the map.

recordClassifierFinished clears that mark. It must run on every exit from a
classification, verdict or not — one that applies nothing is exactly when the
session has to settle on its own.

No transition is guaranteed here (a background-working verdict can leave
`working` persisted), so re-evaluate auto-settle explicitly.

runEvidenceResolveLoop re-resolves every session on a tick and publishes the
verdict, including sessions whose sources have gone quiet.

The session is gone; so is any reason to keep resolving it.

resolverOwnedStates are the states the resolver decides; the rest describe
lifecycle, not agent activity (`recoverable` is the revive path's, and
resolving it would let a stale process observation stomp it). `launching` IS
owned: no source writes state directly, so excluding it strands the session.

publishResolution applies the resolver's verdict, or records why it did not:
Hold means evidence is still arriving, no-evidence means nothing recorded,
and an unowned current state means the resolver had no standing.

Only a hold is traced: it is bounded and looks stuck from outside, while the
other non-applications would log once per session per second forever. The
specific reason is traced, not the word "hold".

ReasonNoEvidence is the absence of a finding; publishing it would repaint
every session the table has not heard about yet. Stuck is the opposite.

An external driver owns its session's state through sequenced report_*
calls; without this veto the tick would overwrite a current report.

No transition: drop the dwell wait so a later one cannot inherit a clock
that started before an unrelated transition.

Recorded even without a move (an already-`unknown` session that goes
silent still needs its tooltip updated), broadcast only on delta.

Last gate on purpose: everything above decides what is true, this decides
whether it has been true long enough to show.

Below the dwell, not above: recording the reason for a transition still
serving its dwell publishes a self-contradicting pair (`working` alongside
`approval_open`), witnessed on a live session.

resolutionDetail is what `attn state explain` shows for a resolver row: the
winning clause's reason first, its own detail after.

traceResolutionSkip records a tick that changed nothing on purpose. Without
it a held session and an unresolved one look identical in the trace.

classifierClaim reads a verdict. Anything outside the three answers — the
`unknown` a failed classification publishes included — is no verdict at all.
