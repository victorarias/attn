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
- **Easy to read, nothing lost.** Plain words, short paragraphs, and a sketch
  wherever it says more than the sentences it replaces: see [showing.md](showing.md).

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

For a Markdown plan or design, use `attn ticket attach-plan` — it chooses one
canonical home for the document. `--authority auto` (the default) keeps a
committed plan in Git when the applicable scope has a repository documentation
convention, attaching a Notebook reference card; otherwise it promotes the plan
into the Notebook and retires its verified untracked staging source. In a
monorepo, `--scope` must name the affected component so unrelated sibling
documentation does not establish the convention. Explicit user or repository
guidance wins — record it with `--authority repository` or
`--authority notebook`. A tracked file is never deleted implicitly, and a
divergent legacy Notebook copy is preserved for explicit reconciliation.

Use `attn ticket attach` for other artifacts and deliberate snapshots; it copies
files into the ticket's Notebook directory and does not retire the sources.

The receipt names the authority. After attaching, edit only the canonical source
— the referenced Git file or the returned Notebook file — and report meaningful
changes on the ticket so participants know to re-read it. Keep `--comment` to
short decision context; the full reasoning belongs in the Markdown.

## Read before you write

`ticket status`, `ticket comment`, and `ticket attach` all deliver ticket activity you
had not read yet, printed above their own output.

- When the activity came from **another participant** — the user, the chief, a sibling
  agent — the command prints it, marks it read, and **does not run**, exiting 1. That is
  deliberate: their word may change what you were about to write. Read it, then **run the
  same command again** — the second attempt goes through. Never treat the refusal as the
  end of the road; an agent that stops there leaves its report unwritten.
- When it is only **attn's own bookkeeping** — the crash stamp, the "session was reloaded"
  flip, a reconciliation verdict — the command prints it and still runs. Those records
  describe what happened to your session while it was down; they are news to you, but
  nothing to answer.

## Creating a ticket

- **Delegation** mints a `working`, **bound** ticket — its description is the brief handed
  to the new agent (see the delegation reference).
- **`attn ticket new`** mints an **unbound `todo`** backlog ticket — no assignee, no
  session. Use it to capture work the user wants tracked without delegating it.
- **Only when the user asks.** You may surface that something is worth a ticket; you never
  file one on your own initiative.
- **Parking is cold-start.** A `todo` has no live session, so its description must be *more*
  self-sufficient than a delegation brief — assume the reader starts with zero context.

## Reading the board and commenting on another ticket

Most of your ticket interaction is your *own* bound ticket — reporting status, reading your
inbox (see the delegated-agent reference). Four commands reach **any** ticket; `attn ticket`
prints their syntax:

- **`ticket list`** reads the whole board; it needs no session. This is primarily a
  **coordinator** move: it is how the chief sees every thread and finds a ticket's id. A
  worker rarely needs it — act on your own ticket and the ids you were handed.
- **`ticket comment`** posts a one-shot note onto a ticket by id, even one you aren't
  assigned to — the agent-to-agent note channel. (Put the text behind `-m`, not as a bare
  argument, so it can contain spaces and dashes.) The comment informs that ticket's
  **participants** (its assignee, the session that delegated it, the chief of staff, anyone
  subscribed) but does **not** subscribe *you*: it is a way to chime in without joining the
  ticket's future activity. For your own bound ticket, prefer `ticket status … --comment`
  so the note also moves the board.
- **`ticket subscribe` / `ticket unsubscribe`** opt you into (or back out of) a ticket's
  notifications — the *standing*-interest counterpart to a one-shot comment. While
  subscribed you are a **participant**: future activity nudges you and lands in your
  inbox, and the first inbox after subscribing also delivers the ticket's history.
  Subscribe when you need to follow a thread you don't own — a chief tracking a ticket it
  didn't create, or an agent whose work depends on another's.
- **`ticket take`** claims a ticket — you become its assignee. Taking a ticket **already
  assigned to someone else** requires `--confirm`, so you cannot silently take over a
  sibling's active work; the displaced assignee is notified. Taking does not skip the
  backlog: your first `ticket inbox` afterward delivers the ticket's history.
