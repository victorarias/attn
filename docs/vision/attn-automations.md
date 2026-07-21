# Vision: attn Automations — work ready before attention

Aligned 2026-07-15. This is the stable north star for the initiative; individual
implementation chunks should link back here and refine it only when the product
direction changes.

## End state (the why)

attn notices work that can be prepared and quietly gets it ready before it asks
for the user's attention. When a pull request requests their review, the configured
reviewer is already present on their computer: it has read the change, captured its
findings, explained what the PR does and why, and proposed a useful reading order.
It sits in an ordinary attn session they can inspect and steer. The chief already
knows about the work through the ticket behind that session. A later review-request
cycle returns to the same reviewer and its accumulated context.

The same primitive handles recurring maintenance. A scheduled prompt can ask an
agent to clear merged worktrees, prepare a briefing, or perform another bounded
piece of work under that agent harness's normal permissions. These are not hidden
cron jobs. Automated work looks and behaves like work the user started manually:
visible, durable, resumable, attributable, and interruptible.

The payoff is attention leverage. attn moves the mechanical discovery, dispatch,
and context-loading earlier, while leaving judgment and authority where they
already live: in the user's prompt and the agent harness they chose.

## North-star principles

- **Prepare work, do not merely remind.** A trigger should put useful work in
  motion. “A PR needs review” becomes a ready reviewer, not another notification
  the user must convert into a delegation.
- **Automated work is ordinary attn work.** Agent-producing automations create or
  continue normal tickets and sessions. They remain visible and steerable through
  the same surfaces as manual delegations.
- **Durable before immediate.** attn records the occurrence and run before it
  launches, resumes, or nudges anything. A daemon restart may delay delivery but
  must not lose it or deliver it twice.
- **The ticket carries awareness.** Automation-created tickets are owned by the
  chief role and their events are authored by an automation identity with explicit
  provenance. The existing ticket unread stream informs the chief; there is no
  parallel automation notification channel.
- **Continuity is explicit and generic.** An automation chooses a fresh worker per
  run, one worker per subject, or one singleton worker. PR review is one use of the
  model, not a special case in the engine.
- **The prompt authorizes; the harness enforces.** A definition pins a concrete
  launch specification and location strategy. Automation sessions always use the
  selected harness's automatic approval mode; this is an automation invariant,
  not a mutable definition option or inherited profile-wide default. attn does not
  silently fall back to another agent, model, approval mode, or directory.
- **Observed data is context, not authority.** Provider payloads such as PR titles,
  bodies, and authors stay structurally separate from the configured prompt. An
  external event cannot rewrite the automation's policy, `LaunchSpec`, or
  `LocationSpec`.
- **Policy describes lifecycle.** Catch-up, overlap, continuity, and eventual retry
  behavior are declarative. Providers observe facts; they do not launch agents or
  encode ticket behavior.
- **The profile owns execution.** Definitions are profile-owned and run whenever
  the profile daemon is alive, even when no chief session is open. The definition's
  `LaunchSpec` chooses how the agent runs and its `LocationSpec` chooses where the
  work is materialized.
- **Nudges are doorbells.** Delivery to an existing worker persists ticket activity
  first, then uses the ordinary content-free ticket nudge. Existing nudge safety
  rules remain intact; unread activity is the durable fallback.
- **Semantic integrations first.** Built-in providers observe schedules and GitHub
  review state directly. Automations do not drive attn by scripting its UI.

## Scope & non-goals

**In scope:** profile-owned automation definitions; enable, disable, edit, delete,
and Run now controls; a configurable prompt; a pinned `LaunchSpec`; a configurable
`LocationSpec`; schedule and GitHub review-request trigger providers; durable
occurrences and run history; explicit continuity, catch-up, and overlap policy;
automation provenance on tickets; delivery through fresh, live, or resumed
sessions; failure visibility; and a first-class Automations surface showing last
run, next run, current status, and the linked ticket/session.

The two proving cases are intentionally different:

- **PR pre-review** proves an observed event, `latest` catch-up, `per_subject`
  continuity, repeat occurrences, and ticket-native chief awareness.
- **Merged-worktree cleanup** proves a schedule, pinned harness authority, and a
  recurring prompt whose safety comes from the prompt plus harness rather than a
  bespoke cleanup engine.

**Non-goals:** a general workflow/DAG engine; a replacement for `cron`; arbitrary
shell hooks; a cloud or off-machine runner; a second permissions layer; a second
notification system; invisible headless agent work; team-shared automation
definitions in the first initiative; replaying every missed schedule occurrence;
or a public provider/plugin SDK in the first release. The internal provider seam
must be real, but public extensibility waits until a third concrete provider makes
its requirements clear.

## Stable domain language

| Term | Meaning |
|---|---|
| **Definition** | The profile-owned configuration: trigger, prompt, pinned `LaunchSpec`, `LocationSpec`, policy, enabled state, and revision. |
| **Subject** | The stable thing work is about, such as `host/repo#123` or one maintenance scope. |
| **Occurrence** | One legitimate trigger cycle, identified by an occurrence key so polling and daemon restarts cannot duplicate it. |
| **Run** | The durable processing record for an accepted occurrence, including a snapshot of the definition revision and its delivery outcome. |
| **Continuity binding** | The durable mapping from a continuity key to the ticket/session that should receive later runs. |

The occurrence key answers **“is this new work?”** The continuity key answers
**“which worker should receive it?”** Keeping them separate is what lets a later
review-request cycle be a new run while preserving the same reviewer conversation.

## Technical direction

The profile daemon owns a small orchestration spine:

```text
schedule adapter ─┐
                  ├─> automation engine ─> work delivery ─> ticket + session
GitHub adapter  ──┘          │                    │              │
                             └─ profile SQLite    └─ ticket event ┴─> chief/agent unread + nudge
```

### Trigger providers

A provider emits typed observations with a provider name, subject key,
occurrence key, observed time, and structured payload. It knows nothing about
tickets, sessions, agents, or continuity.

- The **schedule adapter** evaluates due definitions against a clock. Schedule
  occurrence keys derive from the intended scheduled instant, not the daemon's
  wake-up time, so restart reconciliation remains deterministic.
- The **GitHub adapter** sits beside the existing PR refresh and observes the
  transition into “review requested from me.” A durable provider cursor/edge
  ledger distinguishes a later request cycle for the same PR from a duplicate
  poll. It should consume the refreshed PR snapshot rather than add another
  GitHub polling loop.

V1 registers these two built-in adapters behind one internal provider seam. It
does not expose provider execution to plugins yet.

### Automation engine

An `internal/automation` module should own definition validation, occurrence
claiming, idempotency, catch-up, overlap, continuity-key derivation, run state,
and recovery. Its external interface should stay small: accept a batch of typed
observations and return durable run results. Provider-specific configuration and
ticket/session mechanics stay behind internal seams.

The canonical state belongs in the profile SQLite database. Definitions are
revisioned; each run snapshots the effective prompt, `LaunchSpec`, `LocationSpec`,
and policy so history remains explainable after an edit. Disabling a definition
stops new runs but does not kill an agent already working. Deleting or replacing a
definition must retain enough run history to explain tickets it already created.

The existing `internal/tasks` runner is prior art, not the automation engine. Its
crash recovery, atomic claiming, coalescing, retry backoff, injected clock, and
SQLite adapter are worth reusing as design lessons. Its record is intentionally
`kind + subject`, carries almost no payload, and assumes short daemon-owned work;
stretching it to hold definitions, occurrence history, immutable run snapshots,
and long-lived agent delivery would make both modules shallower.

### Work delivery

Agent delivery should be a second deep module with one conceptual interface:

```text
Deliver(WorkRequest) -> DeliveryResult
```

`WorkRequest` contains the snapshotted prompt, `LaunchSpec`, and `LocationSpec`,
run identity, subject and continuity keys, and automation provenance.
`DeliveryResult` returns the durable ticket/session link and whether delivery
created, continued, or resumed the worker. Callers should not know the mechanics.

Behind that interface, delivery:

1. creates a chief-owned ticket and visible delegation when no binding exists;
2. appends an automation-authored occurrence event when the worker already exists;
3. uses the existing ticket resume path when its session is no longer live;
4. uses the ticket notification path to give both the assigned worker and chief
   unread activity, with the existing nudge as best-effort immediacy; and
5. records the ticket/session link back on the run and continuity binding.

The ticket event is always the payload. A nudge never contains the prompt. Resume
should preserve the agent-native transcript and ticket identity when possible;
when reuse is unsafe or impossible, delivery returns an explicit failure or a
fresh linked fallback according to policy rather than silently losing continuity.

### Policy

| Policy | Values | Direction |
|---|---|---|
| **Continuity** | `fresh`, `per_subject`, `singleton` | Select a new worker, one worker per subject, or one worker for the definition. |
| **Catch-up** | `skip`, `latest` | Ignore downtime or accept the latest still-relevant occurrence. Never replay all missed occurrences. |
| **Overlap** | `coalesce`, `queue`, `parallel` | Default to coalescing. Parallel is valid only with fresh continuity. |

Several occurrences may produce one physical nudge; that is delivery debouncing,
not policy. The engine still records the accepted occurrences and decides whether
the worker sees only the latest demand or an ordered queue.

### Launch and location

There is no reusable named agent-profile entity in attn today. Automations pin a
`LaunchSpec` directly: the concrete driver, model, effort, and any required
executable selection. Validation should reuse driver capabilities already used by
delegation. If a pinned driver or model becomes unavailable, the run fails visibly;
it does not drift to a default agent. Approval is fixed to the driver's automatic
mode and is recorded in the immutable run snapshot. Codex and Claude implement
that product invariant differently, so the effective driver mode remains visible
for audit even though it is not configurable.

`LocationSpec` is separate because workspace placement and repository
materialization are different concerns. PR review uses a fresh detached worktree
for every session, checked out at the occurrence's snapshotted head SHA. By default,
attn maintains a profile-owned repository cache and creates session worktrees below
the profile data directory. A definition may map a repository identity such as
`host/owner/repo` to an existing local clone; this is machine-local configuration in
the automation definition, never a source-code constant. The override supplies Git
objects and repository-specific caches, while the resulting review worktree remains
automation-owned and unique to the session.

GitHub review definitions consider every accessible repository by default. Their
trigger filter (`all`, include, exclude) is independent from the repository-source
map: one decides whether a PR launches work, and the other decides where the exact
revision is materialized. An invalid explicit source override fails visibly instead
of silently falling back to the managed cache.

The configured prompt is the user's instruction. The selected Codex, Claude, or
plugin harness remains responsible for filesystem, network, approval, and tool
behavior. Trigger payload is supplied as clearly delimited structured context so
an untrusted PR cannot modify the configured instruction.

### Product surface and protocol

Automations deserve a profile-level surface rather than being hidden in Settings.
The list should make enabled state, trigger summary, pinned launch, last/next run,
current result, and failures scannable. Editing exposes the prompt and the three
policy groups directly. Run now enters through the same engine as provider events
and creates a normal occurrence with manual provenance.

The foundation is CLI/API first so the engine can be exercised without waiting for
the editor. This is sequencing, not an API-only product direction: the first-class
Automations surface remains part of the initiative once the durable behavior is
proven.

Daemon mutations should follow attn's request/result protocol pattern; state
changes broadcast a compact automations-updated event and the UI re-reads canonical
state. Run detail links directly to its ticket/session rather than recreating agent
output in an automation log viewer.

## Big rocks (the arc)

- [ ] **Foundation vertical slice** — durable definitions and runs, CLI-driven
      manual Run now, `LaunchSpec`/`LocationSpec` validation, fixed automatic
      approval, and fresh delivery into an automation-authored, chief-owned ticket
      and visible session.
- [ ] **Scheduled prompts** — daemon-owned schedule adapter, deterministic due-time
      occurrences, restart reconciliation, and `skip`/`latest` catch-up.
- [ ] **PR pre-review** — reuse the existing GitHub refresh, detect review-request
      cycles, launch a configured reviewer, and link runs by PR subject.
- [ ] **Continuity and nudges** — `per_subject`/`singleton` bindings, live delivery,
      ticket resume, transcript preservation, and implicit ticket doorbells.
- [ ] **Policy depth** — overlap behavior, bounded failure/retry semantics, disable
      and edit behavior, and safe recovery from partial delivery.
- [ ] **Automations surface** — create/edit/enable/disable, Run now, run history,
      failure recovery, and direct navigation to resulting work.
- [ ] **Second proving case** — ship merged-worktree cleanup as a scheduled prompt
      and use what it reveals to refine schedule and harness configuration.
- [ ] **Provider seam hardening** — stabilize the internal interface from two real
      adapters; consider public plugin extensibility only with another concrete use.

## Recommended first chunk

Prove the engine-to-delivery spine before adding time or GitHub:

- persist one profile-owned definition and its immutable run snapshot;
- accept a CLI-driven manual Run now occurrence through the automation engine;
- validate and use its pinned `LaunchSpec` and explicit initial location;
- create an automation-authored, chief-owned ticket and visible agent through the
  delivery module;
- make the run inspectable and idempotent, including its ticket/session link; and
- verify module behavior with an injected clock/store, daemon restart recovery, and
  a live non-production app run.

Defer scheduled evaluation, GitHub observations, continuity reuse, and the polished
editor. This chunk earns the two core module seams and leaves a complete path for
the next provider rather than a disconnected schema foundation.

## Open questions

Known unknowns:

- What bounded retry policy distinguishes transient provider/delivery failure from
  an agent run that legitimately failed? The first slice should prefer a visible
  failure and manual retry over silently launching duplicate agents.
- What exact GitHub evidence identifies a new review-request cycle when the daemon
  was offline? The `latest` policy only needs to recover current demand, but live
  remove-and-readd cycles must remain distinct.
- When should an archived ticket, missing worktree, dirty review worktree, or missing
  agent transcript break continuity and create a fresh linked worker?
- How should schedule editing handle time zones, daylight-saving transitions, and a
  due occurrence computed under the previous revision?
- Which prompt-template fields are universal, and which remain provider-specific
  structured context?
- How much run history remains after definition deletion, and what is the eventual
  retention policy?

Blindspots to ground before their chunks:

- **Cross-provider rate and backpressure behavior.** GitHub polling, many due
  schedules, and agent-launch limits can interact in ways the two initial examples
  do not expose. Run a blindspot pass before introducing global concurrency limits.
- **Public provider isolation.** If trigger providers later come from plugins, their
  trust, lifecycle, versioning, and payload-validation requirements need a dedicated
  ground pass rather than leaking through the internal V1 seam.
