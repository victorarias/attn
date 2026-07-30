# Spike: can we annotate an agent's message in place, in the terminal?

Throwaway. Delete once the verdict is absorbed.

## The question

We want to annotate the agent's last message *in the terminal itself*, rather
than shipping it to a separate UI. The obvious way to do that is to anchor
annotations to terminal rows — and that is the trap. Rows are the rendered view:
they reflow on a width change, shift on a height change, and get repainted by
the agent's TUI. `TerminalBlockStore` already fights this and gives up outright
on a width change (`reanchorOnResize` → `all-stale`, store cleared).

So the design under test inverts it:

> **The transcript span is the anchor. The grid is a projection of it.**

Annotations live on offsets into the message's markdown (which is stable and
already content-hashable by the existing `MarkdownReader/anchoring` model). Grid
rows are re-derived on demand. Nothing row-shaped is ever stored.

That only works if two mappings hold up on real output:

1. **Forward** — markdown → the grid rows currently showing it (drives the
   in-place highlight).
2. **Reverse** — a user-selected row range → an *exact* quote of the agent's own
   markdown (this is the one the product actually depends on: it is what gets
   sent back to the agent).

## What was built

- `capture/` — talks to the daemon over the WebSocket, requests
  `get_screen_snapshot` (read-only: registers no subscriber, claims no PTY
  geometry), replays the returned ANSI through `internal/ghosttyvt` (the same VT
  core the worker uses, so the rows are exactly the rows the app shows), pairs it
  with the session's last assistant message from `internal/transcript`, and
  writes a fixture.
- `align.go` — the aligner. Both sides reduce to **word tokens carrying
  provenance** (markdown byte offsets on one side, row/column on the other) and
  are aligned as sequences via LCS.
- `replay/` — a **dead end**, kept so nobody pays for it twice. See below.
- Fixtures are gitignored: they contain real conversation text. Re-capture
  locally with `capture`.

Word-level alignment is what makes this work, and the reason is worth keeping:
every rendering transform Claude's TUI applies becomes irrelevant *by
construction*. Observed transforms, all handled without special-casing any of
them:

| Markdown | Rendered | Why alignment survives |
|---|---|---|
| `## Heading` | `  Heading` | `#` is stripped from both sides |
| `**bold**`, `` `code` `` | `bold`, `code` | emphasis chars stripped |
| soft wrap at `cols-2` | continuation rows indented 2 | a wrap is just a word boundary |
| ```` ```java ```` fence | the fence line vanishes entirely | unmatched token, skipped |
| `\| a \| b \|` table | Unicode box-drawing table | box chars → empty tokens, cell words align |
| first line | `⏺ ` prefix glyph | glyph → empty token |

## Verdict: the design holds, on Claude

`go test ./spikes/msg-grid-align -v`

Forward alignment, 6 Claude fixtures (100–307 cols, real sessions):

| fixture | recall in span | row inversions |
|---|---|---|
| claude-docs-review | 100.0% | 0 |
| claude-mcpless | 100.0% | 0 |
| claude-services-pilot | 100.0% | 0 |
| claude-thunk | 100.0% | 0 |
| claude-validator-gaps | 98.9% | 0 |
| claude-self-longmsg | no span (correctly) | — |

Reverse mapping — a selected row range → verbatim markdown quote, scored as
*exact* only when the quote's word sequence equals the rows' word sequence:

- single row: **100%** exact on four fixtures, 93.2% on the table-heavy one
- 2/3/5-row selections (what a real annotation spans): **97.5–100%** exact on
  four fixtures, 93.4% on the table-heavy one
- **zero inversions anywhere.** An inversion is the signature of a
  confidently-wrong match, and there were none.

Stability — the anchor is markdown offsets, so the test is whether those offsets
still project onto rows that quote back the same text:

| perturbation | result |
|---|---|
| height ±12 | 0 wrong (9 safe refusals where text left the shrunk viewport) |
| width −20, −60 | **0 wrong** |
| all rows shifted | 0 wrong |

**Width reflow — the case that defeats `TerminalBlockStore` entirely — costs
nothing here**, because no row index is ever persisted. That is the single most
important result in this spike.

The stability report prints a `ROWSΔ` column on purpose: a perturbation that
changed nothing proves nothing. Real reflow moved 8–23 rows, height changes
51–69. (`width +20` shows `ROWSΔ=0` — widening cannot reflow lines that already
fit — so that row of the table is vacuous and should be read as such.)

## Failure taxonomy — every failure was a *refusal*, not a wrong answer

`QuoteRows` refuses below 60% row confidence rather than returning a span it is
unsure of, mirroring the contract `extractBlock` already uses. What that caught:

1. **The user's own turns.** `❯ Ok, then I want you to propose…` and
   `let's go with design A` sit directly above and below the message and share
   enough common English words to pick up chance matches. Both refused.
2. **Chance matches widening the span.** Initially those neighbour rows pushed
   the detected span from `1-68` out to `0-71`. Fixed by bounding the span with
   *confident* rows only (`ConfidentRow = 0.6`), which also lifted recall
   98.9% ← 93.4% and multi-row exactness 93.4% ← 88.0%.
3. **Message not on screen at all.** Both non-prose fixtures resolved to no span
   and refused, which is correct — see the Codex note below.
4. **Table borders and `---` rules** resolve to no offsets (they carry no words).
   They are *inside* the message, so `SpanRows()` returns a contiguous range and
   the highlight must fill them rather than painting only resolved rows.
5. One row quoted 3 words narrow. Cosmetic; the prose was right.

## Codex is UNMEASURED — and this is the one real gap

No Codex fixture in this corpus has a prose message on screen:

- The one live Codex session was a delegated worker whose entire last assistant
  message is `{"verdict":"DONE"}` — 18 chars, nothing to annotate.
- 239 Codex rollouts on disk *do* have assistant messages over 800 chars, so
  Codex prose is common. What is missing is a **rendered grid** for any of them:
  those sessions are dead, and the daemon's `captures/` archive stopped in June.
- `replay/` was the attempt to recover one from the archive by pairing a capture
  with a transcript by search. It correctly refused: best score across 14 Codex
  captures was **51.5% on a 33-token message**, i.e. chance level for common
  English words. It is kept as a documented dead end. Do not raise its
  `-min-score` to make it "work".

**To close this, drive one fresh Codex session to produce a prose answer with
bullets/code and run `capture` against it.** The aligner is provider-agnostic by
construction, so the open questions are narrow and specific: does Codex render
assistant messages as prose in the primary screen at all, and does it need extra
glyphs added to `cutRunes`?

A separate finding from Codex that is *not* a measurement gap: **a session's
last assistant turn is often not annotatable prose** (a structured verdict, or
pure tool activity). The feature has to detect that and say so rather than
offering an empty annotation surface.

## What this implies for the design

1. **Alignment belongs in the daemon, not the frontend.** The daemon already
   owns both inputs — the worker's parsed terminal and the transcript — and
   resolving there means the frontend receives row spans plus markdown offsets
   and only has to paint. It also keeps the aligner in Go, testable without the
   app, exactly as it was tested here.
2. **`get_screen_snapshot` is viewport-only**, which is why the tallest fixture
   has its `⏺` row above row 0. The worker's `PlainText()` covers scrollback, so
   a daemon-side resolver sees the whole message; a read-only full-buffer request
   is needed for anything client-side. Not a product limitation — a limitation of
   the tooling used here, and it bounds these numbers to the visible portion.
3. **LCS is O(n·m).** Fine for a corpus this size (≤832 × ≤895 tokens), not fine
   against deep scrollback. Anchor on rare tokens first, or band the DP.
4. **Refusal is the right default and it already works.** Every failure mode
   found here degraded to "I won't quote that" rather than misquoting the agent.

## Status

Spike answered. Claude: the design holds, with numbers. Codex: unmeasured,
one fresh session away. Nothing here has been absorbed into production code yet.
