# The app's memory floor: where a gigabyte actually went

Written 2026-08-14, after Victor asked to bring the app from "1GB with
very few agents" toward a 500MB baseline, and then pushed back on the
first round of work: *"so far i can see you're trying to optimize gpu
rendering? but what if the problem is not there?"*

The pushback was right about the attention and wrong about the address.
The cost really was GPU surfaces — but not in the renderer's drawing
code, where the attention had been going. It was in **who holds a
surface, and for how long**. Three fixes, all of them about ownership,
none of them about how a frame is drawn.

## The measurement was broken first

Two defects had to be fixed before any number meant anything.

**`ps` RSS cannot see GPU memory at all.** Those pages are `owned
unmapped (graphics)`: charged to the process that owns them, mapped in
another. A run that dropped 482MB of graphics moved total RSS by −34MB.
The number that counts is the **physical footprint** — what Activity
Monitor prints — read per pid from `vmmap`.

**A process tree is not "the app".** The walk swept in a
daemon-spawned headless classifier (`claude --print`, 451MB) and
counted it as UI, inflating every reading by ~400MB. The app is the
Tauri process plus its WebKit children (`APP_PROCESS_CLASSES`), and
nothing else.

Both are fixed in `app/scripts/real-app-harness/perfMeasure.mjs`, with
unit tests over verbatim `vmmap` fixtures.

## The receipt

Same window (1710×1073), all sessions mounted, physical footprint in MB:

| | empty | 4 sessions | 8 sessions |
|---|---|---|---|
| before | 358.7 | 783.2 | 1055.6 |
| + closed dock panels drop their layer | 248.4 | 744.3 | 1017.6 |
| + WebGL depth buffer off | ~248 | 610.6 | 840.4 |
| + hidden panes release the drawing buffer | 249.6 | **472.0** | **696.9** |

Marginal cost per session: **87MB → 56MB**. At 8 sessions, −34%.

The empty app's graphics is window-area-proportional. Solving across
1710×1073 and 855×536 gave **7.07 window-sized layers + 14.3MB fixed** —
which is what said the floor was layers, not pixels drawn.

## The three fixes

**1. A closed dock panel held a compositing layer.** `RightDock` mounts
all five panels unconditionally and only toggles a class, while
`.side-panel` carried a permanent `will-change: transform, opacity`. So
an app with nothing open carried five full-height backing stores it
never drew: 74MB of the empty app's 212MB of GPU surface. The hint now
applies only under `.is-open`, which is the only time these move.
Empty-app histogram went `{15.2:2, 13.8:1, 16.0:1, 14.5:1}` → `{27.0:4}`.

**2. Every pane reserved a depth buffer it never read.** `depth` defaults
to `true` on a WebGL2 context, and it is a drawing-buffer-sized
allocation per pane. The renderer draws 2D text in draw order — it never
enables a depth or a stencil test. Turning it off removed one whole
23.2MB bucket per session from the histogram, which is how we know that
is what it was. `stencil` already defaults to `false`, so asking for
neither is one word of insurance, not the other half of the win.

**3. A hidden session's panes held a full window of drawing buffer.**
An inactive session's wrapper is `display:none`, but a canvas's drawing
buffer is sized by its width/height attributes, not by whether anything
can see it. Eight open sessions paid for eight windows and showed one.
`releaseDrawingBuffer()` shrinks the canvas to 1×1 while the GL context,
its program, and the glyph atlas stay alive; the reveal restores it and
repaints from the model, which never left.

Two details that are load-bearing:

- The trigger is `isActiveSession`, **not** the workspace's
  `sessionVisible`. The latter also goes false behind a modal, and most
  of attn's modals leave the terminal on screen — keying on it blanks a
  visible pane.
- The released state lives on a ref that outlives the renderer. A theme
  change or a model-fault recovery rebuilds every mounted pane's
  renderer, and a fresh renderer always owns a buffer, so without that
  the hidden panes silently re-take what they gave back.

The restore runs in a layout effect, so the repaint lands before the
browser paints the frame that reveals the pane. It does **not** rebuild
the GL context: WKWebView's live-context pool punishes that hard enough
to permanently break panes (see the font-size effect's comment in
`GhosttyTerminal.tsx`).

## What is WebKit's and not ours

The pane-sized surface count fell 16 → 10 at 8 panes, not 16 → 2. The
IOSurface pool is a **high-water mark that never shrinks on its own**:
released surfaces stay allocated but dead. Receipts —

- virtualizing panes releases nothing: 37 → 37 surfaces;
- closing every session releases nothing: 37 → 37, with zero sessions;
- only `notifyutil -p org.WebKit.lowMemory` collects them: 36 → 14,
  699.6 → 217.3MB, and it is a no-op when every pane is live (control).

So the win is that the surfaces are now *dead* rather than live, which
is what the footprint drop measures. The corpses are WebKit's to keep.

## Ruled out along the way

- **Session churn does not leak WebKit Malloc.** At-rest across rounds:
  107.8 → 105.7 → 104.3 → 104.3MB. Flat.
- **Deep scrollback is not a malloc driver.** 60,000 lines × 4 panes
  added ~22MB.
- **Modals are not part of the floor.** `SettingsModal` and
  `WorktreeCleanupPrompt` are placed unconditionally but both `return
  null` when closed.
- **The wrapper-layer hypothesis was wrong.** Surfaces were identical at
  8 live panes and 1; `.terminal-wrapper` is plain `display:none`, a
  virtualized pane renders an empty div, and `loseContext()` is called
  deterministically on unmount.

## What is left

- **A whole view still hides panes that keep their buffers.** The
  release keys on `isActiveSession`, so parking on Home or the grid
  leaves the active session's panes off-screen — the container is
  `display:none` without unmounting — and paying. It is the same shape as
  the modal case, on a different wall: invisible-by-view rather than
  invisible-by-session. The conservative trigger was chosen because
  `sessionVisible` also folds in the modal case, which must not release;
  the fix is a trigger that reads view visibility without it, and the
  reveal path (Home → session) needs the same no-blank-frame
  verification the switch path got.
- **The glyph atlas is per-pane** — a 1024²-growing-to-2048² canvas plus
  its own GPU texture for every mounted pane — while font, size, and
  theme are app-wide. A shared atlas is the obvious next cut, and it is
  a larger share of a hidden pane's cost now than it was before fix 3.
- **Constant chrome is `27.0 × 4` plus `24.0 × 3–4`** in the histogram,
  most likely WebKit's own root layers. Unattributed.
- **Production truth, for scale:** 1617.6MB across 32 processes, of
  which WebContent alone is 1126.4MB (~465MB graphics, much already
  compressed to swap; ~425MB WebKit Malloc). The app is one part of
  what a real day costs.

## How to measure this again

```bash
node scripts/real-app-harness/scenario-perf-baseline.mjs \
  --sessions 8 --stream 0 --window 1710x1073 --warm -1,3,0
```

`APP FOOTPRINT` is the number that counts. `paneSizedSurfaces` plus its
histogram names *which* surfaces exist, which is what turns a total into
a diagnosis. `--pressure` separates live surfaces from cached ones,
`--switch-probe` photographs the window after every session switch, and
`--dock-probe` measures a panel closed vs open and captures it open — so
a memory win that stopped something from rendering cannot pass as a win.
