# Glossary

Canonical domain language for attn. When code, prompts, plans, or guidance name
these concepts, use these words. The goal is one shared vocabulary so every agent
(and human) working on attn means the same thing.

This file is the source of truth for the terms below. If an implementation detail
drifts from a definition here, the definition wins — fix the code or fix this file
in the same change, deliberately.

---

## Workspace context

The per-workspace **editorial overlay**: `context.md`, one per workspace, managed
by `internal/store/workspace_context.go`. It is the workspace's own agents' (and
the user's) running statement of what currently matters here — Area, Current
Picture, Decisions, Constraints.

- **Authored by the workspace's agents and humans.** Agents provide editorial
  information into the workspace context as they work. This is the one place a
  working agent is expected to contribute durable shared state.
- **Ephemeral, not durable.** It is compacted on a size threshold and **erased**
  when the workspace is removed (`DELETE FROM workspace_contexts`). It was never
  meant to outlive the work.
- **Salience, not truth.** It marks where to point attention; it is the agents'
  unverified claim of what was important, not a record of what actually happened.

It is SQLite-canonical (the coordination layer), distinct from the Notebook, which
is filesystem-canonical.

## The Notebook

attn's durable, **profile-wide, filesystem-canonical** markdown layer — a dated
journal plus a PARA knowledge base. The
`.md` files on disk are the source of truth (unlike the workspace context, which is
SQLite-canonical), and the Notebook outlives any single session, workspace, or PR.

The write paths are the daemon (over the WebSocket protocol) and native file edits
on disk — both land in the same filesystem-canonical tree. It holds **the journal
and the knowledge base** — the journal a dated log, the knowledge base distilled,
timeless knowledge — alongside the machine-internal raw tier the keeper reads from.

## The journal

The durable, **curated, cross-workspace log of what was done in attn** —
`journal/<date>.md` in the Notebook. The user's lasting record for recall and
performance review: decisions made, things built and shipped, hard-won fixes,
dead-ends, what was learned. Importance is not recurrence; the most valuable
entries are singular.

Who writes the journal:

| Writer | What they contribute |
|--------|----------------------|
| **The keeper** | per-workspace narratives of the work done in each workspace |
| **The chief of staff** | a cross-workspace, chief-of-staff-altitude log (what moved across workspaces, what was delegated and decided) |
| **The human** | direct edits — corrections, additions, curation |

Other agents *can* write to the journal, but we do not ask them to and do not nudge
them. In practice they do not, and that is fine: automated capture (the keeper) is
what keeps the journal good. We do not try to prevent other writes — that is not
enforceable at this abstraction level — we simply say nothing about it.

The journal is **curated**: nothing machine-raw lands in it. Raw machine inputs
live in the raw tier and are consumed by the keeper, never pasted into the journal.

## Knowledge base

The Notebook's **distilled, durable knowledge subtree** — `knowledge/` — holding
notes worth keeping beyond a single PR: decisions, gotchas, and domain knowledge. It
is organized PARA-style into `knowledge/projects/`, `knowledge/areas/`,
`knowledge/resources/`, and `knowledge/archive/`, and indexed by `knowledge/index.md`
(each PARA dir also carries its own `index.md`). A note carries OKF frontmatter with a
`type` field (`type: note`), an open vocabulary rather than a closed set the store
validates.

Maintained by the **chief of staff** and the **user**, edited directly as files (over
the daemon/WS write path or as native file edits on disk) — there is no closed-kind
gate.

Distinct from the journal along one axis: the journal is a **dated log of what
happened**; a knowledge note is a **timeless statement of what is known**. The
knowledge base is not a task tracker — capture what is *known*, not what is *to do*.
Chief-authored knowledge is **grounded** with resolvable `sources:` (journal
anchors or URLs) rather than written from paraphrase alone; the
user's own notes in the same space are theirs to keep however they like.

## Note title

A note's title is its **first `# H1` heading** — the single canonical title. attn
does **not** read a frontmatter `title:` field (`Document.Title()` parses the body's
first level-1 ATX heading, skipping fenced code). When a note has no `# H1`, callers
fall back to the note's **filename**, which is its stable address (links point at the
path, so the filename is an ID, not a competing title). Frontmatter carries
*properties* (`type`, `summary`, `tags`, `sources`, dates) and is rendered as a
properties card in the editor — never a title. Journals (`# <date>`) and the chief
inbox (`# Chief inbox`) follow the same rule; they carry their title as the body H1
and write no frontmatter `title:`.

## The keeper

The single automated entity that **tends each workspace**. One persona, two duties:

1. **Keeps the workspace context tidy** — compacts/prunes `context.md` when it
   grows past threshold.
2. **Narrates the workspace's work into the journal** — turns the workspace's
   sessions into the curated per-workspace
   journal narrative. On a workspace's final removal pass it also files that
   workspace's linked `knowledge/projects/<slug>/` folder (the one whose `index.md`
   carries `resource: attn:workspace/<id>`) under `knowledge/archive/` — a
   mechanical tidy-up that keeps the active `projects/` view focused; the chief
   keeps the higher-judgment promotion into `areas/`.

These two duties are **causally coupled**, which is why they are one entity: the
keeper can safely prune `context.md` *because* it has already preserved the story
in the journal. "Nothing is lost when the overlay is compacted or erased" is the
keeper's own promise to keep, not an implicit contract spread across separate
actors.

The keeper is realized as task kinds on the durable runner (`internal/tasks`):
`compact_context` (tidy duty), and `summarize_session` + `narrate_workspace`
(narrate duty). These are internal **mechanisms**, not separate personas — there
is no separate "janitor," "narrator," or "summarizer" in the domain model, only the
keeper performing its duties. (The narrate duty runs as a strong-tier agent reading
per-session digests produced by a cheaper summarize step; both are the keeper.)

## The chief of staff

The cross-workspace operator. The Notebook — not any single workspace's context —
is its durable home. The chief **journals**, from a chief-of-staff altitude: the
state of work across workspaces, what it delegated, what was decided — **not** a
step-by-step of what individual agents are doing inside a workspace.

The chief is **keeper-aware**: because the keeper already narrates each workspace's
own work into the journal, the chief does not duplicate per-workspace play-by-play.
It writes the cross-workspace layer the keeper cannot see.

## Turn, and the queue

A **turn** is the user owing an agent their attention. It **opens** when a session
reaches a state that wants the user, and it **closes** only when the user settles
it — steering, approving, snoozing, or pressing settle. Nothing else ever closes
one; in particular, looking at an agent is not acting on it. `turn_owed` is derived
at broadcast from the persisted `turn_opened_at`/`turn_settled_at` stamps and is
never stored. The predicates live in `internal/attention`.

**Auto-settle** closes a turn the user already dealt with by steering the agent
back to work: the session holds `working` through an invisible arm delay, then a
visible countdown, then the turn settles. It applies only to sessions the queue
includes — the exclusions below are also the exclusions here.

A **standing dismissal** is the user answering that settle, whether the countdown
is on screen or has not started yet: the session's next auto-settle does not run.
It is spent at the end of the `working` stretch it covers, so the turn after that
is a fresh decision, and it is off the wire as `auto_settle_dismiss_armed` while
it stands. Neither of those is settling — the turn stays owed either way.

The **queue** is the sidebar arrangement built on turns (queue mode; off by
default). Its standing order is the chief's anchored slot, the turns you owe
(oldest first), the settled rest, pinned agents, pinned workspaces, muted.

**Pinned** — the user saying "I'll come to this one myself". A **pinned agent**
(`sessions.pinned_at`) leaves the queue on its own and leaves its siblings in it; a
**pinned workspace** takes the whole workspace. Pinning is not settling: the turn
goes on accruing underneath, so unpinning surfaces whatever is outstanding at its
true age.

**Satellite** — a shell split out of an agent, recorded at spawn as
`sessions.parent_session_id`. It gets no sidebar row of its own: you reach it by
going to its agent, where it is a pane. Splitting a shell out of a shell inherits
the same agent, so the link always names an agent and never another shell.
Attachment is judged at read — the parent must still exist and still be in the same
workspace — so a satellite whose agent closed, or whose pane moved, gets its own
row back with no cascade. A satellite with no live parent is an **orphan**, and
orphans keep their rows.

## Agent conversation

An **agent conversation** is the provider-owned history currently hosted by one
attn session: its native id, transcript, and resume target. The attn session is
the stable container — its workspace, pane, ticket binding, PTY, and turns can
continue while the agent conversation changes. Codex `/new` is such a change: it
starts a new rollout inside the same running attn session.

Conversation-scoped projections move with that binding. A successor conversation
invalidates the prior transcript watcher, activity line, and activity cursor;
reload and other point-in-time readers resolve the newly bound native id. This is
different from a **conversation session** below, which names attn's headless-agent
runtime rather than a provider-owned history.

## Session activity, and presence

A session's **activity** is one short present-tense line saying what that agent is
doing right now — "running the frontend test suite", "waiting for the user to pick
a branch". It is written from the session's own transcript by a non-interactive
agent, stored on the session (`sessions.activity`, with the stamp it was generated
at), and rendered under the session's name on home. Off by default: it costs money
per session per refresh.

Beside it sits the **activity cursor** (`sessions.activity_cursor`), the transcript
position the line was generated through. It is the load-bearing half: a session
whose transcript has not moved past its cursor has written nothing new, so its
existing line is still true and no run happens. That is what keeps blocked and
finished agents free however long home stays open.

The **presence tier** is how much of the user's attention attn currently has,
reduced across every connected client — the highest anyone reports wins. Clients
report facts (window visible, dashboard showing, seconds since input); the daemon
turns them into a tier:

- **watching** — the app is visible and showing home. The line is being read, so it
  refreshes fastest.
- **present** — input in the app recently, but home is not what is on screen.
- **away** — nobody can see it. A hard stop, not a slower rate: nothing is
  generated at all. Safe as a stop because leaving `away` is always an action that
  restores a higher tier, so the staleness it creates heals itself the moment it
  would matter.

Presence is a heartbeat, never a latch. A client repeats itself while nothing
changes, and a report the daemon has not heard renewed expires to `away` — so a
window that crashes while showing home cannot pin generation on with nobody
looking.

Distinct from `internal/daemon/presence.go`'s **last user activity**, which watches
UI-origin commands go past to answer "did the user act on the daemon". Reading a
screen produces no commands, so that proxy cannot see the case the tier exists for.

## Ticket

The chief delegates a unit of work to a sub-agent, and that work is tracked as a
**ticket** bound to the delegated session (the session is the ticket's assignee).
The agent reports its own **work state** — in progress, needs input, ready for
review, completed, or failed — which moves the ticket across the board
(Todo · Working · Blocked · In Review · Done). Comments, status changes, and
artifact attachments accumulate on the ticket's activity thread. Current artifacts
are the files in the ticket's Notebook directory, and the chief watches
progress from the ticket view and board rather than polling the agent.

## The raw tier

Machine-internal capture under `.attn/raw/`, the keeper's **input**, never
user-facing and never part of the curated journal:

- `sessions/<wsID>/<sessionID>.md` — per-session digests (the summarize step's
  output), nested under the owning workspace; a session with no workspace lands in
  the reserved `sessions/_solo/<sessionID>.md` bucket.
- `context-snapshots/<wsID>.md` — the `context.md` snapshot taken synchronously at
  workspace removal (the deterministic data-safety floor, so the editorial overlay
  is never lost before the keeper can narrate it).

The raw tier is physically unreachable through the user-facing notebook APIs
(`CleanPath` rejects dotdir segments). Capture into it is deterministic and always
happens; the keeper's narration is best-effort on top of it, so nothing is lost if
narration never runs.

## App

An **app** is automation built on attn's platform: it declares what it does in a
manifest, consumes domain facts from the event bus, keeps its state in its own
document-store namespace, and runs inside attn's one shared supervised runtime.
Apps are written by agents, applied while attn runs, and are meant to be cheap
and numerous.

An app has exactly one name, and that name is its whole identity: registry key,
bus consumer `app:<name>`, document namespace `app/<name>`, directory
convention. Nothing stores those separately, so they cannot drift apart.

Two consequences of that single identity are worth stating outright, because
they are what the lifecycle verbs mean:

- An app's **enabled** state *is* its bus consumer's enabled bit. There is no
  second copy. Flipping it is the one act that both stops delivery and releases
  the event log's retention floor, and because the bit lives in the database,
  `attn bus disable app:<name>` kills an app whether or not the daemon is
  listening.
- **Removing** an app stops and deletes its bus consumer and deletes its
  registry row — and nothing else. Its version history, its invocation log, and
  every document under `app/<name>` survive. Deleting a user's data is a
  separate, explicit act, and uninstalling is not it.

A **version** is one built artifact, identified by its content hash and frozen
together with the declaration the manifest carried when it was built. Versions
are never rewritten: applying is an insert plus a pointer move, and rolling back
is the same pointer move to an older row. Re-applying byte-identical content
reuses the version that is already there, which is what keeps "which version
actually ran" answerable in the invocation log after a long editing session.

An app's **serving history** is the chain of versions it has served, and it is
what `attn app rollback <name>` walks. Each bare rollback goes one step further
back, until the oldest version on the chain, where it refuses rather than
wrapping. Applying a version — or naming one to roll onto — starts the history
again from there, so the way back from a fix is whatever was running when it was
applied. The history is not the version list: a version the walk went past is
still a version, still reachable by name.

**Applying** is how a directory becomes a version: parse the manifest, generate
the types the handlers are checked against, typecheck, bundle, hash, write the
artifact, insert the row, move the pointer. Apply never evaluates the app's
code — nothing is imported and nothing is run — so every way an apply can fail
happens before the pointer moves, with the previous version still serving. That
is what makes applying safe to do repeatedly while attn is running, and it is
why `attn app dev` can re-apply on every save.

## Plugin

A **plugin** integrates the outside world into attn: agent drivers, worktree
hooks. It runs as its own supervised process, dials the daemon, is installed
rarely, and is effectively part of the platform — a device driver.

App and plugin are deliberately different mechanisms, not two words for one. A
failing plugin takes an integration down; a failing app is disabled while
everything else keeps running. Different trust, different rate of change,
different blast radius.

## The retention floor, and the pin alarm

The event log is trimmed by age, but never past the lowest cursor any **enabled**
durable consumer still holds. That position is the **retention floor**, and the
consumer sitting on it is said to **pin** the log: nothing at or below its cursor
can be trimmed, because a durable consumer must not lose an unread fact. A
disabled consumer does not pin — releasing the floor is exactly what `attn bus
disable` is for.

Holding the floor is ordinary. Every log has a floor holder and it is usually
just the consumer that read least recently. What is not ordinary is holding it
without moving: a consumer that is enabled and not consuming grows the log for as
long as the condition lasts, and nothing ends that on its own.

The **pin alarm** is the tripwire that separates the two. Past it — an hour by
default, measured against every stall attn resolves by itself — the pin stops
being the system working and becomes an outage worth a warning notification.
Three surfaces report the same finding from the same predicate: the notification,
`attn bus status`, and the event bus settings page. It is announced once per
episode, and a new episode begins only after the consumer's cursor moves.

The alarm makes the condition visible; it never resolves it. No pinned event is
ever dropped and no consumer is ever disabled on its behalf.

## The document store

attn's **document store** is where an app keeps its own data. Three names
locate every record:

- **Namespace** — `owner/name`, e.g. `app/approval-gate`. A namespace is granted
  to exactly one author and is the isolation boundary: nothing an app does
  can read or write another namespace, and two namespaces may use the same
  collection name without meeting. `app/` is the owner segment apps are granted;
  `core/` is attn's own.
- **Collection** — a named set of documents inside a namespace, e.g. `requests`.
- **Document** — one JSON object with a caller-chosen **id**, unique within its
  collection. The body is stored byte for byte and nothing ever rewrites it, so
  an author never writes a migration.

Every document also carries a **revision**: a count of the writes to it, starting
at 1, reported by every read and by every write. Handing a revision back as a
write's **expectation** makes that write conditional — it lands only if the
document is still at that revision, and is otherwise refused with both revisions
named. That is what makes a read-modify-write safe between writers who cannot see
each other; without an expectation a write always lands, which is what a caller
setting a value rather than editing one wants. Expecting revision 0 means
expecting no document at all, and is how a write becomes create-only.

A collection carries a **declaration**: the fields it promises are queryable,
each with a type. Declaring a field does not constrain what a document may
contain — undeclared keys are stored and returned untouched — it only says what a
query may name, and it is what the store indexes that field by. The type decides
how stored values compare, so a body holding `"5"` in a `number` field sorts as
the number 5. `created_at` and `updated_at` are always queryable and are never
declared.

Physically a collection is its own table and a declared field an indexed column
computed from the body, so a declaration is built rather than merely recorded —
without any document being rewritten. A **live query** therefore ends, with an
error saying which, when its collection is removed or redeclared without a field
it uses: the collection can no longer answer the question that was asked.

A **query** is one JSON object: namespace, collection, filters, sort, limit, and
an optional **after cursor** — the id of the last document of the previous page.
The cursor is part of the query rather than a filter a caller writes, because the
visible order is (sort field, id) and a filter can only constrain one of those:
with documents tied on the sort field, `sort > value` skips the tied ones and
`sort >= value` returns the anchor again.

A **live query** is that same query left open. Every delivery is the whole
current result set, so a subscriber renders what it is handed and never
accumulates state across deliveries; the daemon re-runs the query when a write to
the collection says the answer may have moved. A skipped delivery is not a lost
update — the next one supersedes it.

## Recoverable

A session is **recoverable** when its runtime is gone but attn can still bring
its conversation back — the state the sidebar offers a Reload on, and the state
a pane revives itself from when it mounts. It is decided from two durable
things and nothing else: a launch intent, which says how to start a
replacement, and a restoration target that is still on disk, which says what
the replacement reopens — an agent-native resume id the agent's driver still
recognises, a conversation host's own session file, the launch intent's seed
(a source conversation or an initial prompt the host had not said yet), or a
plugin's persisted per-session handle.

Recoverability and activity are separate axes. A crash takes `idle`, `working`,
`waiting_input`, and `pending_approval` sessions down together and leaves them
equally resumable, so what a session was last seen doing is context for the user
and never the reason it survives or is reaped. A session that cannot be brought
back is **reaped**: the row and its pane go, rather than lingering as a Reload
that cannot work.
## The garden

The **garden** is where work lives: all seeds, and the space they live in. It
belongs to a home daemon, because one garden shared across a fleet is its whole
point — an outpost has none and passes its asks home.

A **seed** is the unit of work — one document with one short id, a title, a
markdown body, and a state. Anything worth handing off, parking, or attributing
is a seed; in-session scratch is not. **Planting** creates one, and costs a
single line that returns the id (`attn seed plant "what this is"`), because a
capture that costs ceremony does not happen.

A **plot** is a seed with children plus the intent to execute them — the whole
subtree, not just the parent. A plot has no id of its own: its root seed, the
**crown**, is how it is addressed. Picture a garden bed with a labeled plant
at its head: the bed is the plot, the head plant is the crown, and its label —
the crown's body — says what the bed grows. You never point at the bed; you
point at the labeled plant. A **packet** is a plot flagged as a template
with declared variables, so a proven shape can be planted again with its blanks
filled.

Seed ids are `s-` plus six Crockford base32 characters (`s-7k3f9m`) — short
enough to say out loud, with no character pair anyone confuses, and no `/`, so
qualifying one with its owning daemon at a federation boundary stays a matter of
prefixing rather than a re-identification.

A seed's life runs `planted` → `growing` → `harvested` or `withered`, with
`dormant` off to the side. **Tending** is the atomic claim: it sets the
**tender** — the session and crew member holding the seed — and starts it
growing in one move, and a seed has one tender at a time. **Parking** pauses a
seed deliberately (`dormant`) and lets go of the claim; tending it again picks
it back up. **Harvesting** closes it as done, with a reason, and **withering**
closes it as abandoned. **Replanting** reopens a closed seed — a closed seed
reopens before it moves again, which is why replant is the only verb a
harvested seed answers.

An **edge** is one typed relation between two seeds, stored on the seed it
points from. Two kinds carry meaning today: **blocks** (`a blocks b` — b waits
for a) and **part-of** (`b part-of c` — b is one of the crown c's children, and
a seed sits in at most one plot). `sown-from`, `discovered-from` and
`relates-to` are declared and inert. A cycle in either kind is refused when it
is created, naming both seeds and the edge to remove.

**Ready** is the answer to "what can I pick up right now": an open seed nothing
blocks, nobody holds, and nothing is part of — a crown's work is its children,
not the crown. It is computed when asked and never stored, so harvesting a
blocker frees its dependent at the next call, with nobody clearing anything.
`attn seed ready` answers for the whole garden unless told otherwise — a
delegation dispatched at a crown is the exception, scoped to its plot — and
every attn-launched agent starts knowing the garden's count. The garden is one
space: it has no workspace dimension at all, and plots are its only grouping
(ruled 2026-08-13).

**Dispatch-at-plot** aims a delegation at a crown (`attn delegate --plot
<crown>`). It is scope inference and nothing more: inside that session a
flag-free `ready` answers with the plot, and its launch guidance starts from
the crown — the plan in the crown's body, the plot's ready seeds, the freshest
handoff on each. It is not a fence and not an assignment. The delegate may tend
or plant anything (`--all` steps back out to the garden), several agents may
work one plot at once, and who holds what is always the per-seed tender.

**Stale** is a seed claiming attention it is not getting: open, with no log
movement — no note, no move, no edge — for a window (`attn seed ls --stale`,
default seven days). It is a query for a person's judgment, never a reaper:
nothing withers because a window passed.

"Nobody holds it" is one rule, shared by `ready` and by the claim `tend` makes,
so a seed offered by one is accepted by the other. A tender whose session the
daemon no longer knows has let go — which is how a successor picks up a seed
somebody tended and then ended on. A tender that names only a crew member
always holds: attn has no signal that a person in a terminal pane walked away.

A **note** is one entry on a seed's log: what happened and what was learned,
written for whoever tends that seed next. Notes are anchored to the work and
routed to nobody — a message with an addressee is a message, not a note — and
they are read where the tender already looks, in the seed's own `show`.

A **handoff** is a note kind: one written to your successor on this seed
(`attn seed note <id> -m "…" --handoff`). It is still a note on the log and
still routed to nobody, but the freshest one is put in front of whoever picks
the seed up — `attn seed show` renders it above the seed, and `attn seed tend`
prints it on the claim — so pickup primes without anybody being told to go
looking. This is continuity along the *seed*: a crew member's handoff, filed in
their home when they wrap, is continuity along the *member*, and the two are
independent.

Plan:
[docs/plans/2026-08-06-the-garden-vertical-slices.md](plans/2026-08-06-the-garden-vertical-slices.md).

## The crew

The **crew** is the roster of durable named identities. A **crew member** —
Keel, Alder, Trellis — is a charter, a handoff line, and an address; its
sessions are its **days**. A member belongs to a home daemon, for the same
reason the garden does: one roster across a fleet is its whole point.

**Display capitalizes, identity does not.** The id is lowercase and stays that
way wherever it addresses something — the home directory, `--member`, the
fields on the wire, the store. Wherever a person reads the member, it is
written as the name it is: you type `attn crew wake trellis` and Trellis is
who answers.

A member's **home** is plain markdown on disk at `~/.attn/crew/<name>/`: a
`CHARTER.md` saying what it cares about, and dated **handoffs** it writes to
its successor at the end of a day. Files are canonical and hand-editable —
which is what lets any agent be a member, claude or codex or something later.
The **registry** is the daemon's index over those homes (member id, charter
path, home dir, cwd, awareness dirs, active binding); it serves reads and is
never a second authority for the prose. A home the user adds by hand joins the
roster at the daemon's next start.

**Identity is the invocation, never the files.** A session is a member because
it was launched as one — the daemon stamps a **binding** at launch
(`attn <agent> --member <name>`), and that binding is what `attn agent list`
and `attn agent peek` report. Reading a charter confers nothing. One member has
one active binding: **two agents with the same identity never run at once**, and
waking a member whose day is live names that day rather than starting a second.
Parallelism means another member, never a second copy. A binding naming a
session the daemon no longer knows has let go on its own, the same liveness
rule a seed's tender follows.

To **wake** a member is to start its day: `attn crew wake <name>`, or one click
on its row in the sidebar, where every member is drawn awake or asleep. The
daemon binds the member, then launches a session in its recorded cwd — its own
home when none is recorded — reaching its **awareness dirs**, the directories
its charter is about. Every wake runs on the same pinned model, hardcoded
rather than configurable: a member subtly wrong takes a read of its prose to
notice. The launch carries **priming**: what a member is, where its charter is
to be read, the freshest letter left for it inline, and how a day is closed.
Skills retire into verbs and the verbs are taught, because an agent never told
how to handoff cannot file one.

A member's day ends with a **handoff**: `attn handoff -m "<letter>"`, the
member's own letter to its successor. attn names the file and files it into the
member's `handoffs/`; the line is **append-only**, so a name already taken is
refused and a correction is a new letter rather than an edit. Only the session
living the day can file one — the binding says whose day is closing.

Filing runs the **nap**: attn closes that session and immediately starts the
member's next day, primed by the letter that was just filed. The nap is a
replacement rather than a resume, because carrying a transcript — or a
compaction summary — into the new day is exactly what the member's letter is
there instead of. The successor keeps the closed day's launch settings, and the
binding moves from one session to the other in a single write, so the member is
never momentarily unbound. A nap that cannot run leaves the letter filed and the
day running: a member is never torn down with its letter unfiled.

That state has its own way out. Writing the letter and turning the day over are
one motion but two acts, and only the second can fail, so `attn handoff --retry`
runs the turnover again against the letter already filed — no second file, no
overwrite, append-only untouched. The registry records which letter the current
day filed so the two refusals stay apart: filing again after a failed turnover
names the retry, and a retry with nothing filed names the verb that writes a
letter.

Filing does not always start the next day. Whether it naps or **sleeps** is
attn's call, made from whether the user is around: a day that closes while
nobody is there does not start another one, because the point of a fresh day is
somebody to spend it with. A sleeping member is bound to nothing and shows in
the sidebar one click from a new day. `attn handoff --sleep` and `--nap` decide
it for one handoff rather than letting attn read presence.

Between those two, the **crew lifecycle** is what watches an awake member and
decides when either should happen. It reads two things: how long the user has
been away, and how close the member's session is to losing its prompt cache —
an **estimate**, since no API reports a cache entry's life, so it is time since
the session last talked to the model against an assumed TTL for its harness.
Cache pressure gates everything, which is what keeps the subsystem silent: a
member whose cache is fresh is left alone whoever is around. Once it is close,
who is here decides — the user present means a **heartbeat**, a nudge that reads
the day's context so its lifetime starts over; the user gone means the member is
asked to close its day. Wakes attn starts on its own are bounded per member by
the **wake limit**, so an unattended night has a ceiling and every refusal names
it.

Tending is not a crew privilege: workers and errand sessions tend seeds too,
under any free-string name. Where a tender's name happens to match a registered
member it resolves to that member's id, so the claim compares addresses rather
than spellings — but the registry is never a requirement to tend. The two
handoffs stay apart on purpose: a **seed handoff** is the work item's thread,
written by whoever tends it; a **crew handoff** is the member's day-line.

Plan:
[docs/plans/2026-08-11-the-crew-primitive.md](plans/2026-08-11-the-crew-primitive.md).

## Home daemon

A daemon that is **its own home** — standalone, complete, owning its garden,
its crew, and every other piece of user-level shared state. Every fresh
install starts as a home daemon, and the user's app talks to one. "Home" is
not a rank or a different binary: it is the default state of any daemon that
nobody has enrolled, and the one that may have enrolled others.

The ownership rule underneath: **every piece of state has exactly one owner
daemon; everyone else is a client of it.** Sessions are owned by the daemon
where they run — including on outposts; the garden and the crew are owned by
a home daemon, because one board and one roster are their whole point.

The central server (closed, operated, optional) connects **home daemons to
each other** — federation, a different relationship from home↔outpost
ownership. Outposts never meet the server; a home represents its whole
fleet. Plan:
[docs/plans/2026-08-10-home-garden-crew-arc.md](plans/2026-08-10-home-garden-crew-arc.md).

## Outpost

A daemon **enrolled to a home**: it keeps owning its own sessions, but
garden and crew asks pass to its home over the **uplink** (the generic
outpost-asks-home intent channel). An outpost holds no garden state at all —
not a copy, not a cache; reads pass through like writes.

**Enrollment** is the recorded, mutual act that makes an outpost: the
outpost persists its home's daemon id, and only a connection presenting that
identity acts as home. Every daemon has exactly one home; a second home
dialing an already-enrolled outpost is a loud re-home decision, never silent
adoption.

The record lives beside the daemon's own `daemon-id` file in its data dir; a
home writes it on the remote when it syncs one. `attn enrollment` shows it,
and `attn enrollment leave` is the way out — it makes the daemon a home
again, and is what has to happen on an outpost before a different home may
take it.

Until the uplink is built, outposts are **fenced**: garden and crew surfaces
refuse on an outpost with an error naming the home and the plan tracking the
gap. Everything outposts do today — sessions, PTY, PR flows, local
tickets — is unaffected.

The transport vocabulary is older than these words and stays: **hub** (the
code's name for the dialing side), **endpoint** (a stored SSH target), and
**remote** (the dialed machine) describe the plumbing; home and outpost
describe the ownership relationship carried over it.

## Conversation session

A **conversation session** is an attn session whose agent runs headless in a
process attn spawns, instead of a terminal program driven through a PTY. It is a
session in every other respect — it has a workspace, a pane, a state, turns, and
a ticket binding — so nothing that reasons about sessions has to know which kind
it is looking at.

What differs is the surface. A PTY session's surface is a byte stream and a
terminal grid; a conversation session's is an **envelope** stream going out and
**verbs** coming in. There is no grid and no scrollback.

The process running the agent is its **host**. The daemon owns a host's lifetime
exactly as it owns a PTY worker's: it signals the host to tear down, and kills
its process group as the backstop, so nothing the agent started outlives the
session.

An **envelope** is one message from a host: a session id, a monotonic sequence
number, a `kind`, and a body. Kinds fall in two families, and the split is what
lets an agent's own vocabulary grow without the daemon changing:

- **Declarations** are what the daemon understands and acts on — `session_ready`,
  `run_started`, `run_settled`, `tool_started`, `tool_finished`. These are the
  host telling attn something true about the session.
- **Renderings** are what the app draws — `message_start`, `message_delta`,
  `message_end`, `queue_update`, `tool_detail`, `conversation_page`, `notice`,
  `model_changed`. The daemon forwards them opaquely and holds no opinion about
  them, with one exception: it reads `model_changed` to remember which model the
  session should relaunch on.

A **state declaration** is the subset of declarations that carries the attn state
it puts the session in — the run boundaries — so the daemon reads state off the
host rather than inferring it from the kind. That is what makes a conversation
session move through `working` and `idle` like any other agent, and it is the
path a future `pending_approval` travels on too. Tool boundaries are declarations
without a state: they say what the agent did, not where the session now is.

A **tool call** appears in the transcript as a card: the tool's name, one line
naming what it was pointed at, and how it ended. What the call actually read,
wrote or printed is **detail**, and it stays in the host until someone opens the
card and the app asks for it. That is deliberate — a transcript that inlines
every tool's output is one nobody can scroll, and most of them are never
looked at.

A **run** is one prompt and everything the agent does in response to it, from
`run_started` to `run_settled`. A run is what a turn is opened and settled
around, the same way a PTY agent's stop is.

Three verbs put a message in front of the agent, and which one you use is a
statement about *when* it should be read:

- a **prompt** opens a run, and is what the composer sends when nothing is
  running;
- a **steer** cuts in at the agent's next turn boundary — the interruption, and
  what every attn doorbell (a ticket nudge, a chief nudge, a Present notice)
  becomes for a conversation session, since there is no PTY to type into;
- a **follow-up** waits for the run to finish and is read before it settles, so
  the agent never stops with one unread.

A steer or a follow-up sent to a session with no run open starts one. That is
what makes a doorbell safe to ring at any moment: nothing is dropped for having
arrived at the wrong time. What has been sent and not yet read is the session's
**queue**, which the host reports as it fills and drains — queued, then seen. The
queue can be cleared, which drops everything in it at once; what the strip shows
is always the host's last word about it, never a local guess.

A **conversation snapshot** is what a client needs to draw a conversation it has
not been watching: the newest stretch of the transcript, whether a run is open,
and the queue. It is the conversation's answer to the terminal's restore — one
authority — so what it carries is what the client draws, and two windows on the
same conversation show the same thing by construction. The host holds the
transcript it snapshots, not the session file: a message pi has not finished is
not on disk yet, and a snapshot rebuilt from disk would stop one paragraph short
of the truth.

A snapshot is only a **window** onto a long conversation, and everything older is
**scroll-back** the host still holds and serves a **page** at a time, on request,
as the reader scrolls up. A snapshot also names its **epoch** — the host process
that built it — which is what lets a client tell "the same conversation, redrawn"
from "a different host rebuilt this": the first is spliced onto the scroll-back
the client has already paged in, the second replaces it. Without that
distinction, one window opening a long conversation would shorten what every
other window is showing.

Scroll-back a client is holding can outlive what the host still keeps. The host
bounds its own transcript, and a conversation that talks long enough for the
host to drop its oldest items leaves a window that paged those items in still
drawing them — quietly showing more than a window opened fresh would, until the
next page request comes back empty. It is bounded and it is inherent to paging
something that is broadcast: the client's copy is the client's, and the host
never reaches back into it.

A conversation whose start the host has dropped says so. Once there is no page
left to ask for and items are known to be gone, the transcript is headed by a
row naming how many — the same honesty a compaction row gives, for the same
reason: a history that appears to begin mid-thought is indistinguishable from
one that did.

A conversation can be **resumed**: a new session picks up an existing
conversation instead of starting empty. The old conversation is copied into the
new session's own storage rather than continued in place, so the session it came
from is untouched and two sessions never write to one history. That is distinct
from reloading a `recoverable` session, which returns to its own conversation.

A **notice** is a row in the transcript that explains a silence the agent is not
responsible for — a compaction, a retry. It settles in place rather than
stacking: the row that says a thing is happening becomes the row that says it
finished.

A conversation session is **recoverable** when its host is gone but the
conversation is not: the history is a session file under attn's data dir, and
reloading the session starts a replacement host that reopens it. That is the same
`recoverable` state and the same Reload every other session has, and it covers
the daemon restarting as well as the host dying on its own. A session whose
reopened history does not end with the agent speaking comes back
**`waiting_input`** rather than `idle` — both open a turn and both take a nudge,
so the difference is not what attn does next but what it tells the user: the
agent stopped without finishing, and something is owed to it.

A **launch prompt** is the first message a session is opened with rather than
typed into — a delegation brief, most often. It belongs to the session, not to
the process that first received it: the daemon stores it and hands the same one
to every replacement host, and the host decides whether to say it by looking at
the history it just reopened. An empty history means it was never said, which is
true on a first launch and on a relaunch after a crash so early the agent had not
spoken yet; anything else means it was, and it is never said twice. Only
conversation sessions store one — a PTY agent's relaunch resumes its transcript,
so replaying the brief there would set it to work on something it had already
finished.

An agent becomes a conversation agent by its plugin driver registering the
`conversation` capability. Everything else about launching it — argv, env, cwd —
comes back from the same `driver.spawn` call a PTY-backed agent uses. It never
has to declare the PTY agents' `resume`: that capability describes an agent
resumed from an argv flag, and a host is resumed from its own session file.

## nisse

**nisse** is attn's own agent. It is the first conversation agent: pi's SDK runs
the loop and the model, and everything around that loop — the host process and
its lifetime, the envelope stream, the verbs, the delegation, the pane you read
it in — is attn's. That is why it carries a name of attn's rather than pi's.

A nisse is the Scandinavian household spirit that keeps a house going while
everyone sleeps and works tirelessly for as long as you leave out its porridge.
attn is the house, nisse lives in it, and your attention is the porridge.

The word is only ever the agent. Say **host** for the process a conversation
agent runs in — that machinery is agent-agnostic and would run a second
conversation agent unchanged — and say **pi** for the engine underneath. On the
wire and in the CLI the agent is `nisse` (`attn delegate --agent nisse`); its
launch environment is the `ATTN_NISSE_*` block; the plugin that registers it is
`plugins/attn-pi`, which also registers the PTY-backed `pi` agent.
