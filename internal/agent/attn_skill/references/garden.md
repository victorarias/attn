# The garden: seeds, plots, and reporting

The garden is where work lives. A **seed** is one unit of work — a short id
(`s-7k3f9m`), a title, a markdown body, and a state. Anything worth handing off,
parking, or attributing is a seed; in-session scratch is not. A **plot** is a
seed with children, and its children are parallel by default: only `blocks`
edges sequence them.

`attn seed --help` is the whole surface and the authority on syntax. This
reference is the rules and the judgment.

## The loop

- **`attn seed ready`** — what you can pick up right now: nothing open blocks
  it, nobody holds it, and it is not a crown (a plot's work is its children).
- **`attn seed tend <id>`** — claim it. One tender at a time, so the claim is
  how every other agent learns it is taken. The freshest handoff prints on the
  claim, so picking work up primes you.
- **`attn seed note <id> -m "…"`** — what happened and what you learned, for
  whoever tends it next.
- **`attn seed harvest <id> -m "what got done"`** — close it as done.
  `attn seed wither` closes one nobody will pick up; `attn seed park` puts it
  down without giving up on it.

`attn seed plant "<title>" -m "<body>"` starts one and prints its id.
`attn seed plot -f <payload.json>` plants a whole crown and its children in one
move.

## Rings and watches

Lifecycle moves ring the sessions with a stake in the seed. Notes stay quiet
unless you add `--ring`: ring when somebody needs to look, and let ordinary
progress accumulate silently on the log. `attn seed watch <id>` gives this
session a stake; watching a crown covers everything in its plot. `attn seed
unwatch <id>` is the way out.

A bell carries only the seed and what moved, so read it with `attn seed show`;
`show` or `notes` resets the bell for the next meaningful move.

## Writing a seed's body

A seed's body is the brief — the literal prompt a delegate receives when it is
dispatched at the seed, and the cold-start spec when somebody picks it up
months later. Write it for a reader with zero warm context.

- **Outcome first.** State what "done" looks like — the stop condition — not a
  procedure. A title ("Migrate the store to X") is not a stop condition; "X is
  the only backend the daemon talks to, the old path is deleted, tests green"
  is.
- **Just-enough context.** The paths, the one non-obvious constraint, the why.
  Not a dump.
- **A verification contract.** How completion is known, and what evidence lands
  as an attachment. This is what makes "ready for review" mean something.
- **Scope + autonomy bounds.** What is explicitly deferred, and what is a real
  blocker versus a call the tender can make.
- **Easy to read, nothing lost.** Plain words, short paragraphs, and a sketch
  wherever it says more than the sentences it replaces: see
  [showing.md](showing.md).

The body is the stable contract — still true when a different agent tends the
seed tomorrow. The **log** is the live thread. Don't over-stuff the body trying
to script the whole job; that belongs in notes and steering.

A planted seed nobody is tending is colder than a delegation brief: there is no
live session to ask. Its body has to be *more* self-sufficient, not less.

## Deliverable types bend the shape

How much to prescribe, what "done" is, and who reviews all change with the kind
of work:

| deliverable | what "done" is | attach | how much to prescribe | who reviews |
|---|---|---|---|---|
| feature / code | behavior exists, tests green, PR up | the plan while it remains active | outcome + constraints, not the implementation | the user (engineering) |
| bug fix | root cause found *then* fixed, regression test | durable diagnosis when needed | symptom + repro only — prescribing the fix invites symptom-patching | the user |
| research | a sourced answer feeding a decision | the findings | frame the *question*, not a task | the answer is the deliverable |
| docs / prose | the durable point made, the old superseded | the doc | audience + what it replaces + the one idea | the chief may review on the merits |
| refactor / migration | transform complete, behavior preserved | before/after and invariants | here you *do* prescribe; list the behaviors that must survive | the user, lighter |
| prototype | a decision or a feel; throwaway | the thing and the learning when durable | the question being de-risked; tests optional | informal |

Evidence decides when to harvest, not the deliverable type. Harvest when the
requested outcome has strong terminal evidence and no review or decision
remains — the user accepted it, the requested PR merged. When implementation is
finished but acceptance or another decision is still pending, say so in a note
and leave the seed open.

## Artifacts

A document is **associated** with a seed, never moved into it:

    attn seed attach <id> --path <file.md> [--repo <repository>]
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md>

Where the document lives does not change. A committed plan stays canonical in
Git; an untracked staging file belongs in the Notebook (see
[notebook.md](notebook.md)). The seed's current artifacts are every attach that
has not been detached, and `attn seed show` renders the set.

Edit only the canonical source, and note a meaningful edit, rename, or deletion
on the seed so whoever reads it next knows to re-read the document.

## Handoffs and steering

`attn seed note <id> -m "…" --handoff` addresses a note to your successor on
this seed: `show` renders the freshest one first and `tend` prints it on the
claim, so it is read before any work. Leave one whenever you park a seed or
stop mid-thread.

To steer whoever is tending a seed right now, message them by seed id:
`attn agent msg <seed-id> -m "…"` delivers to its tender, and an untended seed
refuses by name and points at the log. See
[converse-and-observe.md](converse-and-observe.md).

## Only when the user asks

You may surface that something is worth planting. You never plant a seed on
your own initiative to park work you noticed.

## Tickets retired

`attn ticket`'s write verbs are signposts now: each names the garden command
that replaced it and exits nonzero. `attn ticket show` and `attn ticket list`
still read the archived board forever, because a done ticket has no garden
equivalent to point at.
