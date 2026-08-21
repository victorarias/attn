# The garden: seeds, plots, and reporting

The garden is where work lives. A **seed** is one unit of work — a short id
(`s-7k3f9m`), a title, a markdown body, and a state. Anything worth handing off,
parking, or attributing is a seed; in-session scratch is not. A **plot** is a
seed with children, and its children are parallel by default: only `blocks`
edges sequence them.

Two commands hold everything else:

- **`attn seed --help`**: the whole surface and the authority on syntax.
- **`attn seed guide`**: the craft — writing a seed's body, deliverable types
  and what "done" is for each, artifacts, handoffs and steering. Run it before
  writing a body or deciding where a seed belongs; it is the single source of
  truth for that judgment, so this reference does not repeat it.

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

## Only when the user asks

You may surface that something is worth planting. You never plant a seed on
your own initiative to park work you noticed.

## Tickets retired

`attn ticket`'s write verbs are signposts now: each names the garden command
that replaced it and exits nonzero. `attn ticket show` and `attn ticket list`
still read the archived board forever, because a done ticket has no garden
equivalent to point at.
