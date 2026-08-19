# Tickets retired

Work lives in the garden. Read [garden.md](garden.md).

Every `attn ticket` write verb is a signpost: run it and it names the garden
command that replaced it, then exits nonzero. Nothing creates a ticket any
more — a delegation binds a seed, and unbound backlog tickets were converted to
seeds at the cutover.

The two read verbs stay forever, because a done ticket has no garden equivalent
to point at:

- `attn ticket list [--all]` — the archived board.
- `attn ticket show <ticket-id>` — one ticket's full record.
