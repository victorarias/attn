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
| feature / code | behavior exists, tests green, PR up | the diff / PR | outcome + constraints, not the implementation | the user (engineering) |
| bug fix | root cause found *then* fixed, regression test | repro + diagnosis | symptom + repro only — prescribing the fix invites symptom-patching | the user |
| research | a sourced answer feeding a decision | the findings | frame the *question*, not a task | the answer is the deliverable |
| docs / prose | the durable point made, the old superseded | the doc | audience + what it replaces + the one idea | the chief may review on the merits |
| refactor / migration | transform complete, behavior preserved | before/after + invariant checks | here you *do* prescribe; list the behaviors that must survive | the user, lighter |
| prototype | a decision or a feel; throwaway | the thing + what was learned | the question being de-risked; tests optional | informal |

Deliverable type also predicts the terminal status: research and prose often go straight
to **done** (the artifact is the proof); code lands in **in review** because someone else
validates it.

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
