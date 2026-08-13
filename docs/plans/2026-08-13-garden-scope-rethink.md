# Garden scope rethink: what is a seed's "somewhere"?

Written for discussion 2026-08-13, after Victor flagged "we need to
reconsider the per-workspace seeds/garden" before slices 5–6 build on
the current answer. This doc grounds what shipped, shows the receipt
that made the doubt concrete, and prices the options.

## Ruling (2026-08-13)

Victor ruled **C**: location scoping goes; plots are the garden's only
grouping. D's directory claims stay on the shelf until flag-typing
actually stings. Slice 5 absorbs the change (it was already the plots
slice) and the panel's default flips to whole garden, scoped by plot on
request. The wider doubt behind the ruling — the workspace concept
bundles too many jobs — is captured in
[the desktops note](2026-08-13-desktops-note.md).

## What shipped (slices 1–4)

One garden at the home daemon; each seed carries a `workspace_id`
stamped at plant. Everything downstream scopes by that stamp:

```
plant  ── stamps ──►  workspace of the planting session
                      (--workspace overrides; none → empty stamp)

ready  ── infers ──►  interactive session: its workspace's seeds
                      delegate at a crown:  its plot's seeds
                      (--plot / --workspace / --all override)

panel  ── filters ──► the workspace being viewed, with a sticky
                      "Whole garden" toggle; dashboard = whole garden

empty stamp ───────►  surfaces only under --all
```

The design words behind it: "workspace is the default human scope; the
plot is the default delegate scope."

## The receipt

What a workspace actually is on the machine that matters — every
workspace row in production today:

| workspace          | directory                        |
|--------------------|----------------------------------|
| attn--reasearch    | ~/projects/victor/attn--reasearch |
| attn--sunny-meerkat| ~/projects/victor/attn--sunny-meerkat |
| attn--brisk-marmot | ~/projects/victor/attn--brisk-marmot |
| attn--fluffy-otter | ~/projects/victor/attn--fluffy-otter |
| attn--fluffy-otter (duplicate row) | same directory   |
| attn--crispy-capybara | ~/projects/victor/attn--crispy-capybara |
| attn--jolly-llama  | ~/projects/victor/attn--jolly-llama |

Seven workspaces; every one a worktree of the same project, one of them
twice. "This workspace's seeds" therefore answers *"which checkout was
I standing in when the idea arrived?"* — a question nobody asks. The
same idea planted from sunny-meerkat and from fluffy-otter lands in two
different scopes; a duplicate workspace row splits even one checkout's
seeds in two.

Two more sessions of the same rub, from the crew rewrite:

- Direction seeds — the crew plan, the garden itself — are about the
  whole system, not any checkout. Whatever plants them mis-stamps them.
- Crew members get their own cwd, not a workspace. A member planting
  from its home lands in the empty-stamp ghetto, visible only under
  `--all`.

## Options

What `ready` answers under each model, same three seeds — an attn idea
(planted from sunny-meerkat), an attn idea (from fluffy-otter), a
life admin seed:

```
A. workspace (today)   B. project           C. plots only
──────────────────     ──────────────       ──────────────
in fluffy-otter:       in any attn tree:    anywhere:
  attn idea #2 only      both attn ideas      all 3 (filter by
life admin: --all      life admin: its        plot when asked)
                         own project/none
```

### A. Keep the workspace stamp

Price of keeping it: the fragmentation above, forever, and it worsens
with every worktree. Fixing the duplicate row helps nobody — the grain
itself is wrong. Listed for completeness, not advocated.

### B. Stamp the project, not the checkout

Derive the scope from the repo identity (origin URL or main-checkout
path), so every attn worktree is one scope. Keeps the "seeds where I'm
standing" inference. Price: a new project-identity notion the daemon
does not have today; non-repo sessions still need a fallback; and the
grain is still *location*, when some seeds are about no location.

### C. Drop location scoping; plots are the only grouping

The garden becomes one space. Standing plots play the area role the
workspace was playing — "attn", "home", "writing" are crown seeds, and
`part-of` puts a seed in its area. `ready` and the panel default to the
whole garden; `--plot` narrows. The plan already ruled "plots are seeds
with children — one primitive," so this *deletes* a concept instead of
adding one, and the crew/cwd and direction-seed cases stop being
special. Price: whole-garden defaults get noisy as seeds grow into the
hundreds, and "what's relevant where I'm standing" stops being free —
you name the plot.

### D. C plus a directory claim on plots

C, with the inference restored: a standing plot may claim directories
("attn" claims `~/projects/victor/attn*`), and a session's cwd resolves
through the claims to a default plot for plant and ready. One small
mapping instead of a stamp; wrong claims are visible and editable on
the crown. Price: the mapping is one more thing to keep true, and
claims need a loud answer for "two plots claim this directory."

## My read

C is the honest core: the workspace stamp encodes where you stood, and
where you stood is not what a seed is about. D is C with the one thing
the stamp genuinely bought — zero-flag inference — bought back at the
cost of a small, visible mapping instead of an invisible wrong one.
I'd rule C now and treat D's directory claims as a later slice if the
flag-typing actually stings. B only wins if "project" is a concept attn
wants anyway for other reasons; nothing else on the roadmap asks for
it.

Migration is cheap whichever way: no production install has the garden
yet, so there is no user state to convert — the schema's `workspace_id`
field can retire or be repurposed before it ever holds real data.

## If we rule

- Slice 5 (plots and dispatch) absorbs the change: it was already the
  plot-building slice; it now also removes/repoints the workspace scope
  in `ready`, `ls`, the panel, and the launch priming ("your
  workspace's ready count" becomes "your plot's" or "the garden's").
- The panel's default view flips to whole garden; the toggle scopes to
  a plot instead of a workspace.
- The garden plan's "Every seed belongs somewhere" decision gets
  rewritten with a dated ruling pointing here.
