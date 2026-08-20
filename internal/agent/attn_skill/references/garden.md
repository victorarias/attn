# The garden: seeds, plots, and reporting

The garden is where work lives. A **seed** is one unit of work — a short id
(`s-7k3f9m`), a title, a markdown body, and a state. Anything worth handing off,
parking, or attributing is a seed; in-session scratch is not. A **plot** is a
seed with children, and its children are parallel by default: only `blocks`
edges sequence them.

Two commands hold everything else:

- **`attn seed --help`** — the whole surface and the authority on syntax.
- **`attn seed guide`** — the craft: writing a body, the deliverable types and
  what "done" is for each, where a seed belongs, edit versus replant, a seed
  whose tender is gone, artifacts, handoffs, and how to pick up further work.

Run `attn seed guide` before writing a body or deciding where a seed belongs.
It is the single source of truth for that judgment, so this reference does not
repeat it.

## The loop

- **`attn seed ready`** — what you can pick up right now: nothing open blocks
  it, nobody holds it, and it is not a crown (a plot's work is its children).
- **`attn seed tend <id>`** — claim it. One tender at a time, so the claim is
  how every other agent learns it is taken. The freshest handoff prints on the
  claim, so picking work up primes you.
- **`attn seed note <id> -m "…"`** — what happened and what you learned, for
  whoever tends it next. `--handoff` addresses it to your successor.
- **`attn seed harvest <id> -m "what got done"`** — close it as done.
  `attn seed wither` closes one nobody will pick up; `attn seed park` puts it
  down without giving up on it.

`attn seed plant "<title>" -m "<body>"` starts one and prints its id;
`--part-of <crown>` plants it into a plot. `attn seed plot -f <payload.json>`
plants a whole crown and its children in one move.

## Planting is an ordinary move

Plant the work you discover, without being asked. Fix what you found when it is
small and sits right where you already are; otherwise plant it, say in the body
what it fell out of, and stay on the work you were asked to do. Read
`attn seed ls` first so an existing thread gets a note rather than a duplicate,
and wither generously — a garden of stale seeds costs more than a lost one.

Before a session reports done, leave the garden true: note what was learned,
harvest what is finished, park or hand off what is not.
