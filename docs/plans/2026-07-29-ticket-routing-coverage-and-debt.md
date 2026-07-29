# Ticket routing: one participation rule, one compensation stack, a routing model test

**Status: complete.** Everything below except D5 shipped alongside the
every-delegation-binds-a-ticket change. D5 was deliberately left alone; the
reasoning is recorded at the end.

## Context

Ticket notification routing is the machinery that decides **who hears about a
ticket, and how many times**. It spans five layers:

| layer | file | responsibility |
|---|---|---|
| participation rule | `internal/store/sqlite.go` (the `ticket_participants` view) | who counts as involved with a ticket |
| cursors | `internal/store/ticket_events.go` | per-`(identity, ticket)` "consumed through here" |
| observation | `internal/ticketnotify/notify.go` | consume / merge / count / decide delivery |
| identity composition | `internal/daemon/ticket_identity.go` | session ↔ durable-role identities |
| delivery | `internal/daemon/ticket_notify.go`, `nudge_countdown.go` | buffer windows, countdown, doorbell |

Participation has four sources: **assignment**, **non-comment event
authorship** (minus a carve-out: a `created` event on a role-owned ticket is
audit provenance, not participation), **explicit subscription**, and **durable
role ownership**. A durable role identity is `role:<role>` — its cursor
survives a change in which session fills the role.

Since every delegation binds a ticket (routed to assignee + creator + chief of
staff), one session routinely observes through **two** identities at once — a
chief that delegates holds both its session subscription and the
`role:chief_of_staff` identity on the same ticket. `ticketnotify.ConsumeAll`
merging those two queues by event `Seq` is what keeps delivery exactly-once.
That merge went from incidental to load-bearing, which is what prompted this
review.

---

## Part 1 — Testing

### The gap that was closed

The composition was tested only by example, and the newly load-bearing piece had
no test in the package that owns it. `ticketnotify.ConsumeAll` had zero tests in
`internal/ticketnotify`: the simulation harness only ever built single-identity
observers, so multi-identity observation — the entire reason `ConsumeAll`,
`UnreadAny`, and `NotifyAny` exist — was exercised only transitively from
`internal/daemon`, where two of the three call sites merely drained a queue as
setup.

The failure modes are asymmetric, and that asymmetry is the argument for going
past example tests:

- **Double delivery** is noisy and self-reporting — an agent gets nudged twice
  and someone notices.
- **Silent non-delivery** is the expensive one. A worker reports, no participant
  is routed, and the work stalls with no error in any log. Example tests
  structurally cannot find it, because they only cover cases someone already
  imagined.

### What landed

**T1 — model-based routing test.** `internal/ticketnotify/routing_model_test.go`.
The routing rule is restated once as a pure Go set model (participation,
cursors, self-author exclusion, cross-identity merge). A seeded 400-step
sequence of operations runs over a fixed cast — chief session, ordinary
delegator, worker, uninvolved bystander — mixing create, comment, status change,
assign, subscribe, unsubscribe, and chief-session rotation. After **every** step
it asserts that the real stack's participant set and pending queues equal the
model's, and it consumes through `ConsumeAll` at random points to check the
delivered multiset. Runs in about a second.

The model deliberately does **not** model event emission: it reloads the event
log from the store after each mutation and treats it as ground truth for "what
happened". A store change that emits a different event does not fail this test;
a store change that routes an event to the wrong identities does.

Named properties it pins: every participant except the author receives each
event exactly once; no non-participant receives anything; a one-shot commenter
is never enrolled; role transfer preserves the role's cursor without replaying
history to the new session's identity; reassignment delivers the full thread;
nothing is stranded after a full drain.

**T2 — inverse consistency** is folded in rather than written separately. The
old proposal was to assert `TicketParticipants` and `UnreadTicketEventsFor` are
exact inverses. With D1 they are inverses *by construction* — both read the same
view — so the model test checks both directions of it instead of asserting two
hand-written queries agree.

**T3 — multi-identity coverage in the owning package.**
`internal/ticketnotify/multi_identity_test.go` pins the contract directly:
overlap delivered once with **both** cursors advancing, the AuthorID split
(role's cursor, session's authorship) excluding self-authored events, role
identity surviving a session change, `NotifyAny` nudging the session rather than
the role, and `UnreadAny` behaving as a delivery predicate across identities.

### Deliberately not done

**More packaged-app scenarios.** They cost ~6 minutes each, are single-tenant,
drive real agents, and are flake-prone. The two that exist
(`real-app:scenario-ticket-lifecycle`, `real-app:scenario-ordinary-delegation-ticket`)
are the right number: they are the eyewitness that the wiring is real end to
end, not the safety net for routing logic. T1 moves that work down to where a
millisecond-scale test can do it.

---

## Part 2 — Debt

### D1 — The participation rule was hand-written in SQL three times

Three copies, three different SQL idioms, one rule — `ticket_id IN (… UNION …)`
for tickets-per-identity, `SELECT … UNION …` for identities-per-ticket, and
`EXISTS … OR EXISTS …` for one identity on one ticket. They agreed, but nothing
enforced it: adding a participation source meant finding all three and
translating the rule correctly into three dialects, with no test failing if one
was missed. The `NOT (kind = 'created' AND EXISTS …)` carve-out appearing
verbatim in all three was the tell.

**Fixed** by migration 82, which defines a `ticket_participants` view over
`(ticket_id, identity)` materializing the four branches once. All three queries
collapse to asking it a different question:

- participants for a ticket → `WHERE ticket_id = ?`
- tickets for an identity → `WHERE identity = ?`
- one identity on one ticket → both

Query plans were compared before and after. SQLite flattens the view and pushes
the predicate into each `UNION` arm, so all three call sites keep the plan they
had; the hot consume query is unchanged.

### D2 — Identity resolution ran in both directions, in three spellings

Session → identities lived in `ticket_read.go`, the inverse was inlined in
`notifyTicketObservers`, and a third role check computed the attention key —
three places that had to stay consistent, none adjacent to another.

**Fixed** by `internal/daemon/ticket_identity.go`, which defines the forward
mapping, its inverse, and the attention key together, over a single
`ticketRoleIdentitiesForSession`. `ticket_identity_test.go` pins the round trip:
every identity a session observes through must resolve back to that session, or
its events are computed and then delivered to nobody.

### D3 — `delegateOperation` was a hand-rolled saga

Eleven `return nil, d.rollbackDelegation(…)` sites, with the compensation set
growing as the function descended — the last failure points also had to
hand-list `unregisterSession` and `removeWorkspaceLayoutPaneForSession`. Adding
a failure point meant reading every site above it to copy the right
compensations forward, and getting it wrong leaks a workspace, a pane, or a
worktree with no error.

**Fixed** by `delegationRollback` in `internal/daemon/delegate.go`: each
acquisition pushes its own undo, and any later failure unwinds the stack newest
first. Failure sites no longer decide which compensations apply. `ticket_resume.go`
reassembles the same resources and now shares it, which retired the fourth copy
of the hand-listed pattern.

Two tests cover the deepest failure point (ticket creation, the only failure
past a live session) through a `delegationTicketCreateHook` seam: one asserts
worktree, workspace, pane, and ticket are all gone; a second delegates into the
source workspace, where no workspace is created and the session compensation is
therefore the only thing that can remove the spawned session. Each of the four
compensations was verified to be individually load-bearing by removing it and
watching a specific assertion fail.

### D4 — `ObserverChief` was exported production API but harness-only

`ticketnotify` exported `ObserverChief = "chief"` and `ChiefObserver()`, a flat
simulation string sitting in a production package's public surface while
production resolved the chief through
`store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)`. **Fixed**: both moved
into the package's test file.

### D5 — `delegatedByChief` still carries two behaviors — left alone

It gates durable chief-role **ownership** of the ticket (which drives the
delegated-from-chief sidebar badge) and **unmuting** a hidden target workspace.
Those are unrelated decisions and the name describes provenance rather than
either effect. It is down from three riders, it is documented at the
declaration, and splitting it now would churn the delegation path for no
behavior change. Worth splitting if it grows a third rider.

### D6 — `ConsumeAll` cursor advancement is not atomic across identities

`Consume` reads unread events and then writes each touched ticket's cursor
through separately-locked store calls, and `ConsumeAll` drains identities in
turn — so a crash between two identities' cursor writes leaves one advanced and
one not, and the next consume replays those events.

Not new, but its reachability changed: a chief that delegates now routinely
holds two identities on the same ticket. The cost is a duplicate delivery, never
a lost one, so this is **documented rather than fixed** — the doc comment on
`Consume` now records that the two-identity case is the normal path for a
delegating chief, and names the single transactional consume as the fix if it
ever bites.

---

## Verification

Beyond the Go suite: installed to an isolated profile and driven live. Migration
82 applied to a real database and the view was present; an ordinary delegation
produced exactly the three expected participants through the new view; the
worker's report reached the delegator and the chief exactly once each and a
second inbox was empty; a chief-initiated delegation — the two-identity overlap
— was also delivered exactly once; the delegated-from-chief decoration remained
ownership-derived (set for the chief's delegation, unset for the ordinary one);
and a worktree delegation exercised the full refactored saga end to end.
