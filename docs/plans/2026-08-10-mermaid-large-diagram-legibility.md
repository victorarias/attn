# Large Mermaid diagram legibility

Status: implemented and verified after Victor selected intrinsic scrolling with
progressive diagram focus; ready for review.

## Implementation progress

- [x] Add reader-only oversized-diagram detection and intrinsic sizing.
- [x] Add the focused diagram surface with keyboard zoom, pan, and focus return.
- [x] Cover static/shared Markdown, reader interaction, and real Mermaid output.
- [x] Verify the behavior in an isolated packaged app profile.

## Recommendation

Progressively enhance only diagrams that would otherwise be materially
downscaled:

1. Keep a small diagram's current centered, fitted rendering exactly as it is.
2. Render an oversized diagram at its intrinsic SVG size inside a bounded,
   two-axis scroll viewport. Its labels are readable immediately, with native
   trackpad scrolling and arrow-key panning.
3. Add one visible **Focus diagram** action to oversized diagrams. Enter opens
   the same already-rendered SVG in a full-window reading surface; Escape returns
   focus to the originating diagram. The focused surface adds Fit, 100%, zoom
   in/out, and arrow-key panning.

This is prototype C. It combines A's immediate legibility and low idle cost with
B's overview controls, but moves the heavier controls into a deliberate reading
mode. The default does not make the user perform a zoom action just to read a
label.

The first implementation should use a fit-ratio threshold, not a diagram-type or
pixel-width allowlist. A starting product heuristic is `availableWidth /
viewBox.width < 0.8`: a default Mermaid 16px label is then being displayed below
about 12.8px. The threshold needs a short feel check before it becomes a durable
constant; it is not an accessibility standard.

## Current path and root cause

```text
workspace tile content
  -> WorkspaceDockTile.MarkdownBody
    -> MarkdownReader (annotation-enabled document reader)
      -> react-markdown
        -> rehypeRaw -> rehypeSanitize -> source anchors/transforms
        -> readerComponents.pre (Mermaid wrapper keeps source anchors)
          -> shared CodeRenderer
            -> MermaidDiagram
              -> lazy import mermaid 11.16.0
              -> initialize({ securityLevel: "strict", theme })
              -> mermaid.render(id, source)
              -> sanitized SVG string inserted into .markdown-mermaid
                -> .markdown-mermaid svg { max-width: 100%; height: auto }
```

The relevant code is deliberately shared. [`CodeRenderer`](../../app/src/components/Markdown/index.tsx#L14)
routes `language-mermaid` fences to [`MermaidDiagram`](../../app/src/components/Markdown/MermaidDiagram.tsx#L55),
and `MarkdownReader` reuses it in its component map rather than forking Mermaid
rendering ([`MarkdownReader/index.tsx`](../../app/src/components/MarkdownReader/index.tsx#L116)).
The tile host enables the full reader and annotations in
[`WorkspaceDockTile.tsx`](../../app/src/components/SessionTerminalWorkspace/WorkspaceDockTile.tsx#L602).

There are two independent constraints:

- **Diagram generation.** Mermaid lays out the source into an intrinsic SVG
  coordinate space. The number of components or participants, label lengths,
  layout direction, and Mermaid configuration determine that `viewBox`. attn
  cannot make a 3,293px-wide generated graph compact without changing the
  source or Mermaid's layout decisions.
- **Presentation.** Mermaid 11.16.0 defaults both `flowchart.useMaxWidth` and
  `sequence.useMaxWidth` to `true`. Its sizing helper emits `width="100%"` plus
  an inline `max-width: <intrinsic width>px`, with the intrinsic bounds retained
  in `viewBox`. [Mermaid documents](https://mermaid.js.org/config/schema-docs/config-defs-base-diagram-config.html#usemaxwidth)
  that `useMaxWidth` scales to available space. attn then independently applies
  `max-width: 100%; height: auto` to every generated SVG
  ([`Markdown.css`](../../app/src/components/Markdown/Markdown.css#L1)).

The wrapper's `overflow-x: auto` looks like an overflow escape hatch, but the SVG
is shrunk before it can overflow. In the actual failing examples, Playwright
measured `scrollWidth === clientWidth` for both diagrams.

### Measured failing examples

These receipts use the repository's pinned Mermaid 11.16.0, the actual first
component and sequence diagrams from
`docs/plans/2026-08-10-live-terminal-annotation-refresh.md`, attn's current SVG
CSS, and a representative 736px Markdown card content width.

| Diagram | Mermaid viewBox | Rendered width | Scale | Declared label | Effective label |
| --- | ---: | ---: | ---: | ---: | ---: |
| Component flow | 3292.8 × 560 | 736px | 22.4% | 16px | ~3.6px |
| Before sequence | 1961 × 1157 | 736px | 37.5% | 16px | ~6.0px |

“Effective label” is the declared SVG font size multiplied by the rendered-width
to viewBox-width ratio. `getComputedStyle` still reports 16px because the shrink
happens through the SVG viewport; the visible glyphs are scaled with the rest of
the graph.

The reader makes this especially visible because its document wrapper tops out
at 832px and adds responsive card padding
([`MarkdownReader.css`](../../app/src/components/MarkdownReader/MarkdownReader.css#L52)).
Annotation mode can also share the tile with a sidebar
([`MarkdownReader.css`](../../app/src/components/MarkdownReader/MarkdownReader.css#L620)).

## Prototypes

All three are self-contained HTML files with no network access or dependencies.
Each uses the same realistic component and sequence content from the failing
plan, a resizable attn-style split, and in-memory state only.

Open them directly:

```bash
open docs/prototypes/mermaid-intrinsic-scroll.html
open docs/prototypes/mermaid-fit-and-zoom.html
open docs/prototypes/mermaid-progressive-focus.html
```

### A — intrinsic-size scroll

[`mermaid-intrinsic-scroll.html`](../prototypes/mermaid-intrinsic-scroll.html)

- Drag the split divider narrower and wider.
- Click a diagram, then use arrows, Shift+arrows, Page Up/Down, Home, or End.
- Trackpad/mouse scrolling stays native. Center is the only explicit action.

This is the smallest fix and the cheapest at runtime. It makes every label
readable on first paint, but a 3,293px component graph is hard to overview and
the user can lose their position while panning.

### B — bounded fit with zoom controls

[`mermaid-fit-and-zoom.html`](../prototypes/mermaid-fit-and-zoom.html)

- Observe the fitted overview, then choose 100% or press `1`.
- Use `+`/`-` to zoom and `0` to return to Fit.
- Resize the split while in Fit, then choose a manual zoom and resize again.

The overview and controls are discoverable, and Fit responds to container
resizes. It preserves the current unreadable first impression: in the mock-up,
the component graph starts at 44%; the actual failing graph is 22%. Every diagram
also gains persistent toolbar chrome and local scale state.

### C — progressive readable focus

[`mermaid-progressive-focus.html`](../prototypes/mermaid-progressive-focus.html)

- Inline, use native scrolling or arrows at readable 100%.
- Press Enter or choose **Focus diagram**. Double-click is a pointer shortcut,
  not the only way in.
- In focus, use arrows, `+`/`-`, `0` for Fit, and `1` for 100%; Escape returns to
  the exact inline diagram.
- To open the mock-up directly in component focus while serving the repository,
  append `?focus=component`.

This adds complexity only where it buys something. Inline reading remains
immediate, the full-window surface makes long panning easier, and small diagrams
need no new focus target or chrome.

## Comparison

| Criterion | A — intrinsic scroll | B — fit + zoom | Expanded-only view | C — progressive readable focus |
| --- | --- | --- | --- | --- |
| Legibility | Immediate at 1:1 | Poor until zoomed | Poor inline; strong after activation | Immediate inline and strong in focus |
| Keyboard flow | Focus + arrows; no mode | Focus + `+/-/0/1` | Enter, then controls, Escape | Arrows inline; Enter; controls; Escape restores |
| Discoverability | Scrollbars/badge only | Best: controls always visible | Focus action visible, but benefit deferred | Large badge + one clear Focus action |
| Pointer/trackpad | Native two-axis pan | Native pan after zoom; buttons for scale | Comfortable in expanded canvas | Native inline pan; larger focused canvas |
| Resize behavior | Intrinsic content is stable | Fit must recompute; manual zoom needs a rule | Expanded view isolates tile resize | Inline stays stable; focused Fit is event-driven |
| Accessibility | Focusable region needs a name and pan instructions | Many controls on every graph | Requires dialog focus trap and return focus | Same dialog work, only for oversized graphs |
| Implementation | Lowest | Medium: per-diagram zoom + observer | Medium: portal/focus/one-SVG ownership | Highest, but composes two small mechanisms |
| Idle/runtime cost | Native overflow; effectively zero | Event-driven ResizeObserver + state | Zero while closed | Native overflow; observer only for oversize detection; zero while closed |

Expanded-only is worth naming separately even though C is the runnable focused
prototype. It reuses the image-lightbox mental model, but it does not satisfy the
objective on first paint: the reader still encounters 3.6px or 6px labels before
discovering the action.

## Target interaction

```text
small or comfortably fitted
  -> current centered SVG, no new controls or tab stop

fit ratio below threshold
  -> bounded .markdown-mermaid viewport
     -> SVG at intrinsic size (inline max-width override)
     -> focusable scroll region; native trackpad + arrow pan
     -> "Large diagram" status + "Focus diagram" button
        -> full-window dialog (one SVG instance in the DOM)
           -> 100% initial scale
           -> Fit / 100% / +/- and arrow pan
           -> Escape or Return closes, restores trigger focus and inline scroll
```

The focused surface is “Diagram focus,” not workspace “Focus Mode.” The latter
already means maximizing the active leaf with `⌘⇧Enter`; a Markdown tile is
already a first-class focused leaf and can itself enter that mode
([`SessionTerminalWorkspace/index.tsx`](../../app/src/components/SessionTerminalWorkspace/index.tsx#L386),
[`registry.ts`](../../app/src/shortcuts/registry.ts#L40)). Diagram focus is nested
inside that tile and closes through the app's LIFO Escape stack, matching
[`ImageLightbox`](../../app/src/components/MarkdownReader/ImageLightbox.tsx#L10)
and [`useEscapeStack`](../../app/src/hooks/useEscapeStack.ts#L3).

Plain Enter, arrows, `+`, `-`, `0`, and `1` are local handlers while the diagram
viewport owns focus. Do not add global shortcut-registry entries for them.
`⌘+`, `⌘-`, and `⌘0` remain app font-size controls, and `⌘⇧Enter` remains
workspace Focus Mode. No proposed key is claimed by the packaged app's adjusted
`Menu::default` accelerators
([`src-tauri/src/lib.rs`](../../app/src-tauri/src/lib.rs#L531)).

## Implemented seam

Keep generation and presentation separate:

```text
MarkdownReader
  -> shared CodeRenderer
    -> MermaidDiagram               existing generation owner
       -> useMermaidPresentation    new, event-driven measurement/state
          -> inline viewport        intrinsic-size overflow when oversized
          -> DiagramFocusView       portal + focus trap + zoom/pan when open
```

State is local and ephemeral:

```ts
type MermaidSize = {
  viewBoxWidth: number;
  viewBoxHeight: number;
  availableWidth: number;
  oversized: boolean;
};

type DiagramFocusState = null | {
  scaleMode: 'actual' | 'fit' | 'manual';
  scale: number;
  inlineScrollLeft: number;
  inlineScrollTop: number;
};
```

Implementation:

1. Add a reader-only presentation context beside the existing diagram layout
   callback context in `app/src/components/Markdown/index.tsx`. Plain shared
   `Markdown` surfaces remain static. `MarkdownReaderBody` opts into interactive
   presentation around its existing `ReactMarkdown`; there is still one
   `CodeRenderer` and one Mermaid generation path.
2. In `MermaidDiagram.tsx`, retain the rendered SVG string and read the inserted
   SVG's `viewBox` after render. A `ResizeObserver` on the wrapper updates only
   `availableWidth` and the threshold classification. Resize must never call
   `mermaid.render`.
3. Add oversized wrapper classes in `Markdown.css`. Mermaid's emitted inline
   `max-width` has higher precedence than an ordinary stylesheet rule, so the
   oversized rule must deliberately override both `width: 100%` and inline
   `max-width` (or set those two safe presentation properties on the SVG node).
4. Add `DiagramFocusView.tsx`: portal to `document.body`, existing
   `focus-trap-react`, `useEscapeStack`, visible Return action, and restored
   trigger focus. Opening focus updates only the diagram component, not
   `MarkdownReaderBody`, preserving the reader's content-keyed memo gate and DOM
   state ([`MarkdownReader/index.tsx`](../../app/src/components/MarkdownReader/index.tsx#L307)).
5. Keep a single SVG instance in the DOM. When focused, render the retained
   sanitized SVG string in the portal and replace inline content with a sized
   placeholder; on close, restore it inline. Do not clone two visible copies:
   Mermaid SVGs contain internal IDs referenced by markers, masks, and gradients.
6. Mark all controls and placeholder text `data-md-chrome="1"`. Mermaid blocks
   are already deliberately non-paintable to annotations because source text is
   replaced by SVG DOM
   ([`extractBlocks.ts`](../../app/src/components/MarkdownReader/anchoring/extractBlocks.ts#L94));
   interaction chrome must not become document text.

No daemon, protocol, persistence, plugin, SDK, or Linux surface applies. This is
an app-only rendering change with a changelog fragment.

## Library and security constraints

- The real-browser harness confirms `viewBox` sizing for flowchart and sequence
  output from pinned Mermaid 11.16.0. Author directives with
  `useMaxWidth: false` remain a useful compatibility fixture, but are not a
  build blocker because the presentation reads the generated `viewBox` rather
  than relying on Mermaid's width attribute.
- The real component diagram exercises SVG markers and confirms their fragment
  references survive the inline-to-portal ownership transfer. HTML labels /
  `foreignObject` and gradients remain useful compatibility fixtures for later
  Mermaid upgrades.
- Decide whether 100% or a lower readable floor is the best initial focused
  scale on a typical 14-inch display. Do not infer “readable” from the SVG's
  declared font size; the viewBox ratio is the relevant scale.
- Mermaid source remains untrusted. Keep `securityLevel: 'strict'`; do not add
  source-node click handling, evaluate source callbacks, copy author HTML into
  React controls, or use source text as `innerHTML`. Wrapper keyboard/pointer
  handlers should act only on the trusted viewport and buttons. Mermaid's
  [security-level contract](https://mermaid.js.org/config/usage.html#securitylevel)
  remains the generation boundary.
- Reusing the retained, strict-rendered SVG string avoids a second Mermaid render
  and its global `initialize` state. It also keeps opening focus free of parsing
  cost.

## Verification receipts

### Component and browser tests

- `pnpm --dir app test`: 237 files; 2,679 passed and 20 skipped. Focused unit
  coverage proves small/static diagrams stay unchanged, oversized reader
  diagrams pan from the keyboard, the portal owns the sole SVG instance, Escape
  restores exact focus, sibling DOM state survives, and controls remain
  annotation chrome.
- `pnpm --dir app exec playwright test e2e/mermaid-diagram.spec.ts`: 2 passed.
  The browser renders actual Mermaid 11.16.0 output for one small diagram and
  the realistic wide component and sequence diagrams; both large SVGs render at
  their intrinsic width with overflow, marker references survive focus, Fit /
  100% / zoom work, and Escape restores the originating viewport.
- `pnpm --dir app exec tsc --noEmit` and `pnpm --dir app run build`: passed.

### Packaged app

The isolated `mermaid-focus` packaged profile passed its bundled preflight at
protocol 221. Opening the original failing
`2026-08-10-live-terminal-annotation-refresh.md` in a real Markdown tile proved
that the oversized component graph exposes the progressive affordance, opens at
readable 100% in the full-window surface, and gives keyboard focus to its canvas.
The profile was removed after verification.

The local keyboard handlers are deliberately unmodified by app-global commands:
they ignore Meta-modified input, so `⌘+`, `⌘-`, and `⌘0` remain app font-size
controls and `⌘⇧Enter` remains workspace Focus Mode. The implementation adds no
timer or animation; ResizeObservers run only on layout changes, and focused Fit
observes only while that mode is selected.

The three runnable prototypes remain as comparison artifacts. They passed
Playwright checks for intrinsic 15px labels and keyboard pan, Fit -> 100% -> Fit,
and focus open -> zoom -> pan -> Escape with origin-focus restoration.
