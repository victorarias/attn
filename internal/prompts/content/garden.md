attn keeps work as seeds in the garden. A seed is one unit of work: a short id like `s-7k3f9m`, a slug like `mermaid-rendered-grid` (the title's first key words), a title, a markdown body, a state. The id is for commands and for other agents; every verb prints it beside the slug. To the user, say the slug: `mermaid-rendered-grid` (`s-7k3f9m`) on first mention, then the slug alone. A person should never have to decode an id.

Write every seed body as a work prompt for an agent starting without this conversation, including work you plan to do yourself. State the task and outcome, starting context and constraints, and how to verify completion.

A plot is a seed with children: its body holds the shared plan, and each child has its own work prompt. Keep shared decisions in the parent; tell each child which parent section, sibling result or artifact to read and why. Related bodies are not included automatically in a delegation. Children are parallel unless a `blocks` edge orders them. Read `attn seed guide` before writing a plot. Any seed can be a plot. Seed packets are templates for plots; the attn skill explains them.

Garden words have Jira-style equivalents: seed = ticket, ready = todo, plot = epic, harvested = done. Use the Garden word by default. When the user uses one of those Jira words, mirror it for that concept for the rest of the exchange; do not correct them, and do not switch the other concepts unless they do.

Track work in seeds, not in markdown TODO lists or your own todo tool. Plant a seed for any work that outlives this turn: a bug you found, a follow-up you are not doing now, a piece you split off. Plant work before you start it, so the claim and the log exist while you work. Under a plot, plant with `--part-of <plot>` so it stays with its plan. If you discover work while tending another seed, add `--discovered-from <seed>` so its origin is on record. Before your turn ends, plant what is still undone. Harvest a seed when the outcome and required verification in its body are complete.

A delegated session reports to one seed: either the seed planted for its brief or the seed targeted by `attn delegate --plot`. `attn seed ready` without flags shows that seed's plot. When the session delegates more work, `attn delegate` plants the new seed under its reporting seed; the child delegate reports to the new seed's log. Every other garden verb uses the seed id you provide.

The loop:

    attn seed ready                  what you can pick up now: open, not parked, not blocked, nobody holding it
                                     inside your plot when you report to one. A plot itself is never ready; only its children can be
    attn seed ready --all            the same across the whole garden; use it to look past your plot
    attn seed show <id>              body, state, tender, edges, children, freshest handoff
    attn seed tend <id>              claim it; one tender at a time, a held seed refuses you by name
    attn seed note <id> -m "…"       what happened and what you learned, tending it or not; --handoff addresses the next tender
                                     --ring tells watchers to look
    attn seed harvest <id> -m "…"    done; the reason is required and fits in 400 characters, the long version goes in a note
    attn seed wither <id> [-m "…"]   abandoned, nobody will pick it up
    attn seed park <id>              put down, claim released; tend it again to resume
    attn seed replant <id>           a harvested or withered seed back to planted
    attn seed plant "<title>" -m "…" [--part-of <plot>] [--discovered-from <seed>]    a new seed; prints the id

`attn seed tend`, `attn seed park`, `attn seed harvest`, `attn seed wither` and `attn seed replant` all check who holds the seed. If a live session or crew member holds it, the command refuses it by naming the holder. `--force` performs the move anyway, and the log records who forced it. A seed whose session ended is not held. `--member <name>` on any of these commands acts as a crew member instead of this session, and a member's claim never expires.

Plans:

    attn seed plot -f <file.json>    a whole plot in one move; - reads stdin. The file is
                                     {"title": …, "body": …, "children": [{"title": …, "body": …, "blocks": ["<sibling-slug>"]}]}
                                     A slug is the sibling's title without its stop words, lowercased and dash-joined; writing the sibling's title works too. `attn seed guide` has a full example
    attn seed link <a> blocks <b>    b waits until a closes; unlink removes the edge
    attn seed link <a> part-of <b>   a joins b's plot; a seed sits in one plot at a time
    attn seed link <a> discovered-from <b>    a was discovered while working on b; the link records that origin but never orders or blocks anything
    attn seed ls [--flat]            everything planted and who holds it, children nested under their plot; --flat for one list
    attn seed edit <id> -m "…"       replace the body; say what changed in a note

Keeping up:

When attn sends an update notification, run the suggested command to read it. Reading acknowledges the update and maintains awareness; it does not authorize or require acting on the update. Only act or interrupt the user when attention is genuinely needed.

    attn seed notes <id>             the whole log, newest first
    attn seed watch <id>             ring this session when the seed or anything in its plot moves; unwatch stops it
    attn seed attach <id> --path <file> (--move | --copy)    bring a local file into durable seed ownership; Move is recommended
    attn seed detach <id> --path <filename> --to <destination>    move an owned file back out without overwriting
    attn seed attach <id> --path <file> --repo <repo> | --notebook <doc-id> | --url <url>    keep a link to a document elsewhere
    attn seed export <id> [--out <path>]    the seed and its log as one markdown file
    attn seed set-resume <id> --resume-session-id <id> --cwd <path> --agent <name>    make an ended conversation resumable from the seed; --clear forgets it

Use attn agent msg for attn-managed sessions; keep Claude's SendMessage for follow-ups to the native subagents this session spawned.

`attn seed --help` has every flag. `attn seed guide` has how to write a body worth handing to somebody else.
