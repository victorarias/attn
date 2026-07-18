# Writing a good ticket

A ticket's `description` is the brief — the literal prompt an agent receives when the
ticket is delegated, and the cold-start spec when the work is picked up later. It is
durable: it survives resume and reassignment. Write it for a reader with zero warm
context.

## The description is the brief

- **Outcome first.** State what "done" looks like — the stop condition — not a procedure.
  A title ("Migrate the store to X") is not a stop condition; "X is the only backend the
  daemon talks to, the old path is deleted, tests green" is.
- **Just-enough context.** The paths, the one non-obvious constraint, the why. Not a dump.
- **A verification contract.** How completion is known, and what evidence lands as an
  attachment for review. This is what makes "in review" mean something.
- **Scope + autonomy bounds.** What is explicitly deferred, and what is a real blocker vs.
  a call the worker can make. This is what makes "blocked" a signal and not noise.

## Durable description vs. live steering

- **The description is the stable contract** — still true after the ticket is reassigned
  to a different agent tomorrow.
- **The activity thread (comments, re-briefs) is the live steering.**
- Don't over-stuff the description trying to script the whole job; that belongs in steering.

## Deliverable types bend the shape

How much to prescribe, what "done" is, and who reviews all change with the kind of work:

| deliverable | what "done" is | attach | how much to prescribe | who reviews |
|---|---|---|---|---|
| feature / code | behavior exists, tests green, PR up | hand over the plan when it remains active | outcome + constraints, not the implementation | the user (engineering) |
| bug fix | root cause found *then* fixed, regression test | hand over durable diagnosis when needed | symptom + repro only — prescribing the fix invites symptom-patching | the user |
| research | a sourced answer feeding a decision | hand over the findings | frame the *question*, not a task | the answer is the deliverable |
| docs / prose | the durable point made, the old superseded | hand over the doc | audience + what it replaces + the one idea | the chief may review on the merits |
| refactor / migration | transform complete, behavior preserved | hand over before/after and invariants | here you *do* prescribe; list the behaviors that must survive | the user, lighter |
| prototype | a decision or a feel; throwaway | hand over the thing and learning when durable | the question being de-risked; tests optional | informal |

Deliverable type also predicts the terminal status, but evidence decides it. Use
**done** when the requested outcome has strong terminal evidence and no review or
decision remains — for example, Victor accepted it or the requested PR merged. Use
**in review** when implementation is finished but acceptance or another decision is
still pending. Research and prose may therefore go straight to done when the accepted
artifact is itself the proof; unreviewed code normally lands in review.

## Handing over artifacts

For a Markdown plan or design, use the canonical-source operation:

    "$ATTN_WRAPPER_PATH" ticket attach-plan \
      --file docs/plans/design.md \
      --scope services/catalog \
      --ticket <ticket-id> \
      --state ready_for_review \
      --comment "Decision context for the chief."

- Omit `--ticket` to use your own bound ticket; include it to target any known
  ticket, matching `ticket status --ticket` and `ticket comment`.
- `--authority auto` is the default. It keeps a committed plan in Git when the
  applicable scope has a repository documentation convention, attaching a Notebook
  reference with path, branch, and introducing commit. Otherwise it promotes the
  plan into the Notebook and retires its verified untracked staging source.
- When a new repository reference replaces an older copied attachment, a
  byte-identical legacy Notebook copy is retired and recorded on the ticket. A
  divergent copy is preserved for explicit reconciliation.
- In a monorepo, `--scope` must name the affected component so unrelated sibling
  documentation does not establish the convention.
- Explicit user or repository guidance wins. Record it with `--authority repository`
  or `--authority notebook` when needed. A tracked file is never deleted implicitly.
- `--state` uses the reporting vocabulary: `in_progress`, `needs_input`,
  `ready_for_review`, `completed`, or `failed`.
- `--comment` is short decision context. Put the full reasoning in the Markdown.
- The receipt names the authority. Edit the referenced Git file for repository-owned
  plans; edit the returned Notebook file for Notebook-owned plans.

Use `attn ticket attach` for other artifacts and deliberate snapshots. It accepts
repeatable `--file` flags, copies files into the ticket's Notebook directory, and
does not retire the sources. A same-name destination with different bytes is never
overwritten.

After attaching, edit only the canonical source. Report meaningful changes on the
ticket so participants know to re-read it.

## Creating a ticket

- **Delegation** mints a `working`, **bound** ticket — its description is the brief handed
  to the new agent (see the delegation reference).
- **`attn ticket new --title <t> [--description <d>] [--id <slug>]`** mints an **unbound
  `todo`** backlog ticket — no assignee, no session. Use it to capture work the user wants
  tracked without delegating it.
- **Only when the user asks.** You may surface that something is worth a ticket; you never
  file one on your own initiative.
- **Parking is cold-start.** A `todo` has no live session, so its description must be *more*
  self-sufficient than a delegation brief — assume the reader starts with zero context.
- **Slug.** Omit `--id` and the slug is derived from the title and made unique
  automatically. Pass `--id` to choose it; if that id is already taken, creation fails with
  guidance — pick a new name or append a number.

## Reading the board and commenting on another ticket

Most of your ticket interaction is your *own* bound ticket — reporting status, reading your
inbox (see the delegated-agent reference). These two commands reach **any** ticket:

- **`attn ticket list [--status <col>] [--all] [--json]`** reads the whole board: one line
  per ticket (id, column, assignee, title), newest first. `--json` adds each ticket's
  description (the brief). It needs no session — it is a global read, not scoped to you.
  This is primarily a **coordinator** move: it is how the chief sees every thread and finds
  a ticket's id. A worker rarely needs it — act on your own ticket and the ids you were
  handed.
- **`attn ticket comment <ticket-id> -m "<text>"`** posts a one-shot note onto a ticket by
  id, even one you aren't assigned to — the agent-to-agent note channel. Put the text behind
  `-m` (`--message`), not as a bare argument, so it can contain spaces and dashes and so
  `--session`/`--json` still parse. The comment informs that ticket's **participants** (its
  assignee, the chief who created it, anyone subscribed) but does **not** subscribe *you*:
  it is a way to chime in without joining the ticket's future activity. Use it to flag
  something to a sibling agent or annotate a thread you're not on; for your own bound
  ticket, prefer `attn ticket status … --comment` so the note also moves the board.
- **`attn ticket subscribe <ticket-id>` / `attn ticket unsubscribe <ticket-id>`** opt you
  into (or back out of) a ticket's notifications — the *standing*-interest counterpart to a
  one-shot comment. While subscribed you are a **participant**: future activity on the ticket
  nudges you and lands in your `attn ticket inbox`, and the first inbox after subscribing also
  delivers the ticket's history (subscribing does not skip the backlog). Subscribe when you
  need to follow a thread you don't own — a chief tracking a ticket it didn't create, or an
  agent whose work depends on another's. Unsubscribe is idempotent. (Commenting alone never
  subscribes you; this is the explicit opt-in.)
- **`attn ticket take <ticket-id> [--confirm]`** claims a ticket — you become its assignee.
  Use it to pick up an unassigned backlog ticket, or to hand work over to yourself. Taking a
  ticket **already assigned to someone else** requires `--confirm`, so you cannot silently
  take over a sibling's active work — without it the command refuses and names the current
  assignee. Taking does not skip the backlog: your first `attn ticket inbox` afterward
  delivers the ticket's history. The displaced assignee is notified of the attach.
