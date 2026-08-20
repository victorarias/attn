# One garden, one box that changes size

Why promoting the garden from the dock into the window is a resize rather
than a second surface, what that costs, and which decisions are still open.

The complaint this answers: the sidebar rail carried **two** garden buttons —
one that opened the dock panel, one that opened the fullscreen surface — with
no path between them. Open the dock panel, drill two plots deep, decide you
want the room: there was nothing to press. The duplication was the bug, not
only the missing gesture.

## The direction: promotion is a size change

`GardenFrame` renders `GardenPanel` **exactly once**, inside a fixed-position
element whose rectangle is either the dock slot's or the window's, and
animates between the two. Nothing unmounts, so the trail, the open seed, the
scroll offsets and the keyboard focus survive the promotion by construction
rather than by being copied between two instances — and so will whatever
panel state gets added next.

**The panel is not told which frame it is in.** It is told how wide it is:

```
const layout = panelWidth >= COLUMNS_MIN ? 'columns' : 'stack';
```

1160 is not a new number — it is the width at which the panel already lets a
second pane exist; see
[crown drilling](2026-08-20-garden-drilling-and-the-seed-reader.md). And
`panelWidth` comes from a `ResizeObserver` on the panel's own box, so it
is the *live* width during the flight, not the width it is heading for. The
Miller columns are therefore not switched on because a mode changed; they
arrive because the box got wide enough to hold them, in the last stretch of a
180ms flight, and the stack you were reading grows into them. That single
line is the argument of the whole change: promotion is a size change, and
everything else follows from the size.

Everything visible is borrowed. The easing and the 18px slide come from
`SidePanel.css`; the 12px gutter, the 8px radius and the app-background scrim
from the notebook's fullscreen surface. 180ms out (the reveal), 150ms back (a
dismissal), both inside the band, both skipped under
`prefers-reduced-motion` — CSS `transition: none` plus a JS guard that never
sets the flight class. One touch that carries meaning rather than decorates:
the shadow fades to **nothing** in the window. The dock panel floats above the
workspace, so it casts one; the window frame *is* the window, with nothing
behind it to float above.

### The dock reserves a place for a panel it does not paint

`RightDock` gained a first-class `detached` panel. The dock still reserves the
panel's width, so the panels beside it sit where they should, and lays out an
empty slot at the rectangle the panel would have filled — offset in the stack,
`clamp()`'d width and all. `GardenFrame` measures that slot, which is how a
surface rendered outside the dock finds where to be.

The slot is measured with `getBoundingClientRect` and that is only safe
because the slot itself carries no transform. The `.side-panel` it stands in
for does: it slides on `translateX` and would report where it is mid-slide
rather than where it belongs.

### Two views, one switch, and where it lives

The kanban board ([prototype](2026-08-20-garden-kanban-board-prototype.md))
was a second view inside the fullscreen surface, with the switch owned by the
surface so that neither view owned the other. The frame inherits that job. Two
things follow from the frame being a promotion rather than a separate entry:

- The switch is offered only in the window. Four columns of cards need the
  room, and the dock clamps at 560px.
- The promotion always lands on the **list**, whatever the reader last chose.
  The gesture says "this, bigger"; arriving on a different view would say the
  opposite. The board is one press away once you are there.

While the board is up, `GardenPanel` is not mounted, so the frame carries the
bottom of the Escape ladder itself. It arms when the board appears and the
board's own climb arms when the reader walks into a plot — later, so the climb
lands above it, the same way-of-arming rule as everywhere else here.

### Escape, one level at a time

Escape goes down exactly one rung of the ladder the panel already had:

```
clear the query  →  climb the trail  →  window → dock  →  close
```

Demotion is unconditional rather than return-to-origin: open straight into the
window from closed, and Escape still leaves you in the dock. One key, one
level, from anywhere.

The floor rung is registered inside `GardenPanel`, not in `GardenFrame`, and
that is the piece to get wrong. `useEscapeStack` is LIFO, and React runs child
effects before parent ones — so the same handler registered in the frame would
land *above* `climbOne` and eat the climb. Most of the time the ordering comes
for free, because each rung pushes when its own condition turns on and you
have to type before you can clear, walk in before you can climb. Source order
only decides the case where two rungs arm in the *same* commit, which is what
reopening the garden onto a trail it already had does. That case has its own
test.

`Cmd+Shift+T` toggles both ways. That needed one carve-out in the shortcut
gate, which silences *every* app shortcut while a fullscreen surface is up:
the garden's own survives inside its own frame, because the frame is the
garden at a different size, not a modal over it. Whether `notebookOpen`
deserves the same treatment is a separate question.

## Two alternatives that lost

**Lift the state, mount two panels, crossfade.** `store/gardenWalk.ts` already
did the hard half of this — the trail and the scroll memory are shared — so
mounting a second `GardenPanel` in the window and cross-fading would have been
cheap, and would have preserved place. It loses because the crossfade has a
tell: two copies at different widths reflow their rows at different moments,
so the dissolve shows two lists rather than one object moving, and one object
moving is precisely the thing being said. It also re-opens continuity as a
promise to keep for every *future* piece of panel state — focus, text
selection, an in-flight document fetch — where a single mount keeps it for
free.

**Promote it into a workspace tile, maximized.** The app already knows how to
promote a tile, so this speaks an existing language rather than inventing one.
It loses because the garden is *global* and a workspace tile is not: promoting
it binds the garden to one workspace and makes "which workspace is my garden
in?" a real question with no good answer. Escape would also inherit
tile-maximize semantics instead of the garden's own ladder.

(A third, briefly: drag the dock's left edge wider. Pointer-only, no keyboard
story, no named end state, and it never actually becomes the window. Though
with layout derived from width, it now nearly works for free.)

## What it cost

- **`GardenSurface` is gone**, and with it its hardcoded `layout="columns"`.
- **`layout` stops being a prop.** The panel derives it from its own measured
  width, which deletes a way for the frame and the panel to disagree.
- **`RightDock` grew the `detached` concept** and `SidePanel.css` the
  `.side-panel-slot` rule.
- **One rung on the escape ladder**, registered in the panel.
- **The rail collapses to one garden button.** It means *the garden, docked*;
  the shortcut and the palette entry mean *the garden, in whichever frame it is
  not in*. The window frame covers the rail, so coming back from it is Escape,
  the shortcut, or the header control — never the rail.
- **The shortcut gate grew one carve-out.**

## Settled

- The header scrolls away with the list, so the pointer affordance only exists
  at scroll top. Good enough — the shortcut works anywhere.
- Escape demotes one level, never returning to origin.
- The dock panel stays. The window is a promotion, not a replacement.
- What the window frame is *for* is the Miller-columns walk — the trail beside
  what you are reading. The old 960px measure cap is gone; columns own the
  measure.

## Still open

1. **Where should the crossing happen?** Columns arrive at 1160px, which on a
   1670pt window is the last stretch of the flight: the stack grows, then the
   columns land almost at rest. The alternative is crossing earlier — a lower
   threshold, or keying it to a fraction of the trip — so the columns settle
   while the box is still moving. Late was chosen because a relayout mid-flight
   reads as a second event.
2. **Should a wide dock get columns too?** Layout follows width, so a dock ever
   resizable past 1160px would go to columns on its own. Today it clamps at
   560, so the question does not arise — but the rule is now "wide enough
   means columns", everywhere, and that is a stance.
3. **The reader's offset across the crossing.** The stack scrolls one page
   (document *and* children in a single scroller); columns split them into a
   reader pane and a list column with separate scrollers and separate memory
   keys. The document's offset carries across — both renderers key it the same
   way — but the children list arrives at its own top. A new pane starting at
   its top reads as correct, so it was left there. Bridging the two keys is
   small: they are the same string with a `col:` prefix.
4. **The old reveal animation is gone.** The fullscreen surface used to fade
   and scale 0.99 → 1 on its own. Now nothing appears; something grows.
