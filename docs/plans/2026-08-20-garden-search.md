# Garden search and filters

The garden grows without bound. Every seed ever planted stays in it,
harvested ones included, and the panel is a flat list of whatever the
snapshot pushed. Past a few dozen seeds the only way to reach one is to
remember where it is.

This is search and filtering in the garden panel: one line of type that
does both.

## The direction: one line, one state

Search is a line under the header — always there, never summoned. The
garden opens with focus in it, because opening the garden is already the
ask. The `/` glyph on the left is the affordance and the shortcut in the
same character; `⌘F` works too.

Filters are operators typed on that same line:
`is:open|closed|any|ready|blocked|dormant` and `tender:<name>`. That is
the whole language, on purpose — anything past it is a report, and a
report is a CLI thing.

The rule that holds it together: **the query is the only filter state.**
The `N closed` toggle in the header writes `is:any` into the query. The
`+2 in the whole garden` hint writes the widen. The no-results moves
write their own tokens. Nothing keeps a flag beside the text, so the line
always says exactly what you are looking at, and there is never a second
place to check.

Three decisions follow from it.

**Browsing is a tree; searching is a list.** Text or a `tender:` flattens
the scope's entire subtree into one ranked list. A search that only
looked one level down would answer "not here" about a seed that is very
much here.

**A lens is not a search.** A bare `is:` value re-filters the level you
are standing on rather than flattening it. Asking to see closed work is
not asking to see every seed in every plot at once, and the same
predicate — `satisfiesLens` — decides both the browsing list and the
search, so `is:closed` means one thing in the panel and not two.

**Scope is visible and escapable, and widening is not a move.** Inside a
plot, search covers the plot; if the rest of the garden holds matches,
the line says so and offers `⌥↵`. Widening changes what the *query*
looks at, not where you are: the trail stays, its plot segments recede,
and the `Garden` crumb takes the weight — one line answering "where am I
standing?" and "what am I searching?" without the two contradicting each
other. `⌥↵` swings both ways and always names the count on the other
side. Clearing the search puts you back in the plot you never left.

Ranking is id, then title (earlier hit first), then tender, then
body-only — and a body-only hit is the only one that spends a third line
on a snippet, because it is the only case where the row does not already
show why it matched. Ties keep snapshot order, so a query never
reshuffles equally good answers.

## Two alternatives that lost

**A summoned palette (`⌘K` over the panel).** It looked right — attn
already has palettes — and it lost on what happens *after* the match. In
a palette the result list is not the panel's list, so acting on a result
means dismissing the palette first, and drilling from a result is a
two-step. Search in a garden of a few hundred seeds is not an occasional
command, it is the primary way to reach a seed; making it a modal thing
you leave taxes the common path to protect the rare one. It also hides
the count, and the count is what tells you whether to keep typing.

**Filter chips beside the field** (`open | ready | closed | mine`). Lost
on two counts. First, it makes filters and search two mechanisms with two
states, and then someone has to decide what a pressed `closed` chip means
while the query says `is:open` — there is no good answer, only a
precedence rule to memorise. Second, chips are chrome: a permanent row of
boxes above a dense list, spending vertical space on affordances that are
mostly off. The operator line gets more power (`is:ready tender:hazel`
composes; chips do not) in pixels the query already occupies. The chips
*are* still there, in effect — they appear as plain value names while you
are mid-way through typing `is:`, and they disappear the moment the query
answers for itself.

Asking the daemon was never seriously on the table. The snapshot already
carries every seed's body, so a round trip would buy nothing and cost
search a loading state.

## What client-side costs, with receipts

Matching runs over the pushed snapshot. Two corpora:
`measured` mirrors a real seeded garden (bodies averaging 171 characters);
`heavy` is a garden of delegation briefs (bodies averaging ~1.5 KB with a
10% tail at 8 KB), which is what a plot planted from real briefs looks
like. Script: `app/src/components/gardenSearch.bench.ts` — it carries the
invocation for both engines and the reason the steady-state figures are a
minimum of three rounds.

Milliseconds, worst query of six (`reconnect daemon`). `first` is a
snapshot the process has never seen, indexed and scanned once;
`keystroke` is the steady state of typing into it.

| corpus | text | first (JSC) | keystroke (JSC) | keystroke (V8) |
|---|---|---|---|---|
| 107 seeds, measured | 26 KB | 0.40 | 0.02 | 0.03 |
| 1000 seeds, measured | 245 KB | 0.24 | 0.25 | 0.25 |
| 1000 seeds, heavy | 2065 KB | 0.74 | 0.86 | 0.86 |
| 5000 seeds, heavy | 10282 KB | 3.44 | 6.06 | 5.77 |

JavaScriptCore is the engine that counts — that is what WKWebView runs.
Warm, it lands within noise of V8 on this workload, which is the useful
finding: the numbers node prints are the numbers the app gets. The 107
row's `first` is the highest in its column because it is the only
genuinely JIT-cold measurement in the run; every later block is cold data
through warm code, which is also what the app sees after the first
search. Measured on an M-series Mac, macOS 25.5.

Read it as: **at the snapshot cap, client-side is not the problem.** A
thousand seeds of full delegation briefs costs under a millisecond a
keystroke, inside one 120 Hz frame with room to spare. Five thousand
heavy seeds would want a different answer at 6 ms — but nothing can push
five thousand seeds today: `gardenSnapshotLimit` is `docstore.MaxLimit`,
a thousand.

One thing that is not free: the lowercased text is built once per
snapshot (`buildIndex`), not once per keystroke. Over a thousand heavy
seeds the lowercasing is 0.6 ms and the scan that answers the query is
0.9 ms, so folding the first into the second would nearly double every
keystroke for nothing.

### The real ceiling is the push, not the match

Every garden fact re-pushes the whole garden (`wireProjections`,
`garden.*` → `garden_seeds_updated`). Measured against a live daemon with
a WebSocket client that presents the profile's client token, timing
`initial_state` after the hello and one re-push after planting a seed:

| garden | `initial_state` | one re-push after a plant |
|---|---|---|
| 107 seeds | 67 KB of seeds, 12 ms after hello | 68 KB, ~104 ms |
| 1000 seeds | 1816 KB of seeds, 129 ms after hello | 1814 KB, ~368 ms |

So the client-side assumption does not break because matching gets slow.
It breaks because a thousand-seed garden ships 1.8 MB down a socket whose
client buffer is 256 messages, every time anybody plants, tends, notes,
or harvests anything. **That is the thing to fix first, and it is not a
search project** — it is a cursor'd delta feed for the garden, daemon
plus protocol. Once that exists, a daemon-side index is a small addition
on top of it. Until then, searching client-side is strictly cheaper than
what the panel already pays to exist.

## What it costs to build

- **Daemon: nothing. Protocol: nothing.** The snapshot already carries
  `title`, `body`, `id`, `tender_member`, `status`, and `edges`. That is
  every field the matcher reads.
- **Frontend:** `gardenSearch.ts` is ~300 lines of pure functions and is
  the whole of the logic; the panel gains a search row, a highlight
  helper, a provenance line, and a designed no-results block. All of it
  is testable without a daemon.

## Known gaps

- **Row virtualization.** A query that matches 477 rows renders 477 rows.
  That is a list problem the panel already has, and it is the first thing
  to hit if the garden grows and search stays.
- **The hint counts run a second pass.** `+N closed` and `+N in the whole
  garden` each re-run a full search to get their number. At today's sizes
  that is free; at 5000 heavy seeds it doubles the keystroke cost.
