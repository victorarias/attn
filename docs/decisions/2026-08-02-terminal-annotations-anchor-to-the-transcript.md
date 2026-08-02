# Terminal Annotations Anchor to the Transcript, Not to Rows

Date: 2026-08-02

## Decision

An annotation on an agent's message is anchored to **offsets in the message's
markdown**. The terminal rows it paints over are **re-derived on demand from the
live grid and never stored**.

A wash is painted only when the text currently sitting at the resolved rows still
quotes back the anchored text. When it does not, the annotation paints nothing
that frame. Refusing is a correct outcome; painting over words the agent did not
write is not.

This supersedes the obvious alternative — storing terminal rows and re-anchoring
them — which `TerminalBlockStore` already implements and which gives up outright
on a width change (`reanchorOnResize` → `all-stale`, store cleared).

Two consequences follow, and both are part of the decision rather than additions
to it:

- **The anchor names a message, not "the current one."** A session offers a
  *window* of recent assistant messages, each with a stable key, and an
  annotation carries the key it was made against. A new turn extends the window;
  it does not replace what the user already marked.
- **The annotations live in the daemon, not in the pane.** Every mutation is
  written through to `session_annotation_drafts` before it is anything else, and
  a mounting pane hydrates from there. The pane is a view of them, and views are
  allowed to be destroyed.

## Why

Rows are the rendered view, not the content. They reflow on a width change, shift
on a height change, and are repainted wholesale by the agent's TUI. Markdown
offsets change only when the message changes, which for a finished assistant turn
is never.

Inverting the relationship makes the hardest case free: a width reflow costs
nothing, because no row index was ever persisted to invalidate.

## Evidence

Two spikes, both deleted; this document is what they produced.

**Offline, six real Claude sessions (100–307 cols)** — markdown ↔ rendered-grid
alignment by word tokens carrying provenance, paired with LCS:

| property | result |
|---|---|
| forward alignment recall in span | 98.9–100% |
| row-order inversions | 0 everywhere |
| reverse quote exactness, 2/3/5-row selections | 97.5–100% (93.4% table-heavy) |
| height ±12 | 0 wrong (9 safe refusals) |
| width −20, −60 | 0 wrong |

**In-app, live panes, 2026-08-01** — annotations held fixed while the terminal
was perturbed (output appended, scrolled, split-narrowed, font-size changed),
under four re-alignment policies. `always` re-aligns every frame and is the
correctness baseline; `resize` re-aligns only on a geometry change and is the
cheapest; `idle` debounces 250ms after writes; `rederive` re-aligns every frame
but only searches near where the message was last found.

| run | projections | survived | refused | wrong |
|---|---|---|---|---|
| claude pane | 285 | 113 | 74 | 98 |
| codex pane | 619 | 589 | 20 | 10 |

Splitting those 108 by what the wash actually covered:

| policy | landed on unrelated text | right text, terminal-truncated |
|---|---|---|
| `resize` | 59 | 3 |
| `idle` | 34 | 4 |
| `always` | **0** | 4 |
| `rederive` | **0** | 4 |

Two things fall out of that split, and they are the whole verdict:

1. **Neither policy that re-derives ever misattributed.** Across 904 projections,
   `always` and `rederive` put a wash on text the agent did not write exactly zero
   times. Every such paint came from a policy that kept a stale mapping — worst
   case 13.2 seconds and 37 PTY writes old.
2. **The remaining 15 are the terminal truncating itself, not the projection
   failing.** They occur in the window between a resize and the TUI's repaint,
   where the grid holds rows cut to the new width: at the moment of one such
   paint, *every* row in the buffer was exactly 31 characters — the box borders,
   the status footer, and unrelated chrome included. The wash covered the agent's
   own sentence with its last word clipped, because that is what the terminal was
   displaying.

**Cost (Q2).** Alignment is not a constraint at pane scale. Worst full re-align
across both runs: **1ms**, over buffers of 24–113 rows and up to 283 grid tokens;
worst grid read 5ms. Bounding the search to a margin either side of the last
known span (`rederive`) kept 196 of 332 alignments local, but at these buffer
sizes the 60-row margin covers most of the buffer anyway — it saves roughly a
quarter, not orders of magnitude. It only becomes decisive against buffers in the
thousands.

**Gesture (Q3).** Whether a drag selects text or is swallowed by the TUI is not a
constant across agents:

| pane | mouse reporting | plain drag | alt-held drag |
|---|---|---|---|
| claude | on | no selection — the TUI owns it | selects |
| codex | off | selects | selects |
| shell (control) | off | selects | — |

Alt-held drag works everywhere and needs no new code: `GhosttyTerminal`'s
`onMouseDown` already tests `!event.altKey` before encoding a press to the PTY.

## Rules this imposes

1. **The containment check is a gate, not a metric.** Before painting, read the
   text now at the resolved rows and confirm it still contains (or is contained
   by) the anchored text, in word space. The spike painted anyway in order to
   count failures; the product must not. Under this gate all 108 observed wrong
   paints become refusals.
2. **Re-derive; never cache a row.** Cache the *alignment* if it helps, but the
   mapping from offsets to rows is recomputed, and rule 1 re-validates it against
   the live grid every time regardless.
3. **A stale mapping is the only thing that misattributes.** Any invalidation
   scheme that lets an alignment outlive the writes that moved the text will paint
   wrong words. Re-align on every projection unless a measurement on real buffer
   sizes says otherwise.
4. **A geometry change invalidates the search window, not just the alignment.** A
   span measured at the old width names rows that hold different text after a
   reflow; seeding a bounded search from it finds the message shifted and clips
   its head. Force one whole-buffer search after a resize.
5. **Bound the alignment window only once the message's location is known**, and
   widen back to the whole buffer when the bounded result lands against a window
   edge — an edge hit means the message probably continues outside it.
6. **Tokenize both sides identically**, stripping markdown syntax and TUI chrome
   (`*_`` ~#|>``, box-drawing, `⏺✻❯⎿·▪●○`). Every rendering transform the TUI
   applies then becomes irrelevant by construction: a heading loses its `#` on
   both sides, a soft wrap is just a word boundary, a code fence is an unmatched
   token that is skipped.
7. **Bound the span with confident rows only** (≥60% of a row's words aligned).
   The user's own turns sit directly above and below the message and share enough
   common English to pick up chance matches; letting those set the boundary lights
   up text the agent never wrote.
8. **Drop *alignments* on a terminal reset** (alt-screen enter/exit, session
   switch, attach restore) — never the annotations. The buffer they were
   resolved against is gone; the markdown they address is not. An annotation
   that stops painting is a frame with nothing to paint, not a deletion. The
   same rule covers a message aging out of the window: it keeps its quote and
   its place in the panel with nowhere to paint.
9. **Nothing may be lost to something the user did not do.** A new turn, a
   reflow, a pane virtualized away, an app quit, a crash — none of these are the
   user removing an annotation, so none of them may remove one. Concretely:
   persist on every mutation with no debounce, hydrate before the first paint,
   and let only an explicit remove or a send clear anything.
10. **Ordering is the daemon's, by generation.** A save carries a monotonically
   increasing generation and is stored only if it beats both the stored
   generation and the tombstone a send raises. A refused save is a normal
   outcome the client answers by re-hydrating — not an error to show anyone.
   Shared with markdown drafts in `internal/store/annotation_drafts.go`, because
   two implementations of an ordering rule is one too many.
11. **Alt-drag is the selection gesture on a mouse-reporting TUI.** Whether it is
   discoverable enough on its own, or needs an explicit annotate mode beside it,
   is an open UX question — not a technical one.

## The window's budgets, and their receipts

The annotatable window is bounded three ways. The numbers come from measuring
the 120 most recent Claude Code transcripts on the development machine (2,327
assistant prose blocks), then setting each limit far past what any of them
reached, so only something broken touches one:

| limit | value | measured | headroom |
|---|---|---|---|
| `annotatableMessageMaxChars` | 64 KB | largest block 18,713 chars (p99 3,949) | 3.4x |
| `annotatableWindowMessages` | 32 | — | — |
| `annotatableWindowMaxChars` | 256 KB | heaviest 32-block tail 21,282 chars (p90 13,486) | 12x |

Two rules go with them:

- **An oversize message is dropped whole, never truncated.** Cutting a message
  keeps it in the window while silently re-pointing every offset past the cut,
  which turns a stored annotation into a quote of the wrong words. Dropping it
  costs the user the ability to annotate one enormous message; truncating would
  cost them correctness on all the others.
- **Every drop is visible.** `truncated` rides the wire, and the daemon logs
  which budget was hit, its value, and the actual value that hit it.

## Known limits

- **Column arithmetic is code-unit based**, which equals cell columns only on rows
  with no wide (CJK/emoji) characters. That skews sub-row wash bounds on such
  rows; it does not affect which rows resolve.
- **Alignment could move daemon-side.** The daemon owns both inputs — the worker's
  parsed terminal (with full scrollback, unlike the viewport-only
  `get_screen_snapshot`) and the transcript — so resolving there would send the
  frontend row spans plus offsets and leave it only to paint. The 1ms measurement
  means this is an option, not a requirement.

## Where the UX is settled

`docs/prototypes/terminal-annotation-sketch.html`: highlight a range, get an emoji
popup, one option opens a comment box, saved annotations collect in a draggable
floating panel with a single "Send all". The panel floats specifically so it
cannot resize the terminal.

The prototype renders rows as DOM text and wraps annotations in `<mark>`. The real
terminal is a WebGL canvas with no DOM text and no element to wrap, so the sketch
proves the interaction and nothing about the mechanism. The mechanism is a
`background` `WebGlOverlay` — the same idiom `renderSurface` already uses for the
selection wash, the hover-link underline, and block selection.
