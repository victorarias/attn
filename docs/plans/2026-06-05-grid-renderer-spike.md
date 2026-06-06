# Grid Renderer Spike — SPEC + Prototype Plan

**Status:** spike spec, ready to build
**Scope:** throwaway, DEV-ONLY prototype. No daemon. Plain browser via Vite. Fully deletable.
**Owner route:** `app/grid-proto/` (+ one line in `vite.config.ts`)

---

## 1. Goal + architectural thesis

**Goal.** Build a dev-only prototype that lets us *feel* the animations and *measure* the efficiency of attn "grid mode" (~25 live agent tiles in a 2×2..5×5 grid), comparing two renderer architectures head-to-head behind one harness, with synthetic data so it runs in a plain browser with no daemon.

**Thesis.** The load-bearing constraint for grid mode is **WebGL2 context count**, not draw count or bytes. WKWebView caps simultaneously-live WebGL contexts (~16; observed freeze, commit `5735c2a1`, `GhosttyTerminal.tsx:742-761`), and attn keeps every pane mounted, so today's *one-context-per-pane* design (`N panes = N contexts = N InputHandlers = N contentEditable surfaces`) physically cannot reach 25 tiles. The clean fix is a **unified single-context renderer**: one WebGL2 context, one 2048² glyph atlas, one vertex buffer, one `drawArrays` for the whole grid. This is clean *because the model is pure VT*: `ghostty-web`'s `GhosttyTerminal` is a pure state machine over WASM memory — it owns no canvas, GL context, or rAF (probe:ghostty-web-api). The app already proves this: `GhosttyWebGlRenderer.ts` consumes the model purely as a cell-data source (`getViewport()` + cursor/colors/graphemes) and does all GL itself. So N models can feed ONE renderer with zero VT-state entanglement, and the glyph atlas already keys on `(style,text)` only (color is tinted per-vertex, glyphs rasterized white at `GhosttyWebGlRenderer.ts:410-412,160-162`), so one atlas correctly serves all tiles. The prototype's job is to prove the unified renderer wins on **live context count** and **whole-grid animation smoothness** at 16–25 tiles against the faithful N-surface control — if it doesn't clearly win there, the effort is unjustified. That A/B is the go/no-go gate.

---

## 2. The renderer abstraction — `GridRenderer` contract

Both variants implement ONE interface so they are swappable behind ONE harness (`GridProto.tsx` + `GridCompositor`). The compositor owns the rAF loop, layout math, animation clocks, and input; a `GridRenderer` is a *dumb pure function of (tiles, layout, focus)* per frame. (Factoring borrowed from the GridCompositor candidate; see synthesis.)

```ts
// app/grid-proto/GridRenderer.ts
export interface TileModel {
  id: string;
  model: GhosttyTerminal;     // pure VT state machine (ghostty-web)
  cellWidth: number;          // logical px, canonical font (same for all tiles)
  cellHeight: number;
  baseline: number;
}

// Animated, per-frame placement of a tile in shared-canvas LOGICAL px.
export interface TileFrame {
  id: string;
  rect: { x: number; y: number; w: number; h: number }; // animated container-space rect
  scale: number;              // animated; 1.0 == native 1:1, thumbnail < 1
  alpha: number;              // animated; for fade-others during zoom
  attention: number;          // 0..1 pulse intensity (drives inset border quad)
  hidden: boolean;            // hide-tile control
}

export interface GridRenderStats {
  drawCalls: number;
  atlasUploads: number;       // glyph slots uploaded this frame
  atlasResets: number;        // resetAtlas() calls this frame
  liveContexts: number;       // successful, non-lost getContext('webgl2')
  cpuSubmitMs: number;        // vertex-assembly + submit wall time
}

export interface GridRenderer {
  mount(container: HTMLElement): void;        // owns/creates its canvas(es) under container
  setTiles(tiles: TileModel[]): void;         // model set changed (add/remove/resize)
  setLayout(rows: number, cols: number, container: { w: number; h: number }): void;
  setFocus(tileId: string | null): void;      // which tile is interactive
  zoomTo(tileId: string | null): void;        // begin grid<->fullscreen morph target
  // Pure per-frame draw. The compositor computes every TileFrame (animated rect/scale/
  // alpha/attention) and hands them in; the renderer only draws. Returns perf stats.
  frame(frames: TileFrame[], now: number): GridRenderStats;
  dispose(): void;            // deterministic GPU release (loseContext on real unmount)
}
```

Notes:
- `frame()` does NOT own rAF. The **compositor** owns rAF and calls `renderer.frame(...)` only when work is needed (any animation active OR any model dirty); a fully idle grid issues zero draws.
- `mount/dispose` own the canvas lifecycle. `dispose()` MUST call `WEBGL_lose_context.loseContext()` on real unmount (deterministic release per `5735c2a1`) — but never on a re-init that reuses the canvas (corpse-context hazard, `GhosttyTerminal.tsx:750-755`).
- The renderer never touches `GhosttyTerminal` geometry (no `resize()`); rendering is a pure read. PTY geometry authority is untouched (AGENTS #7).

---

## 3. Variants

### Variant A — Baseline: N ghostty surfaces in a CSS grid (the control)

Exact reproduction of today's per-pane architecture at grid scale, in the SAME harness, fed by the SAME synthetic feed, so the only independent variable is renderer architecture. Expected to hit the WKWebView cap (~16) and freeze/drop contexts past it — **that failure is the measurement**, the go/no-go gate.

- A CSS grid container: `display:grid; grid-template-columns:repeat(N,1fr); grid-template-rows:repeat(M,1fr); gap:2px; width:100%; height:100%`.
- Each cell is a `<BaselineTile>` reproducing ONE production pane: a `<div contentEditable tabIndex=0 role="textbox">` wrapping its OWN `<canvas>`, its OWN `new WebGlTerminalRenderer(canvas, FONT_SIZE, FONT_FAMILY, THEME)` (the **UNMODIFIED existing class** — do NOT use the grid renderer), its OWN `InputHandler(ghostty, container, data=>model.write(data), ...)`, its OWN `GhosttyTerminal` from a shared `Ghostty.load()`. Copy create/render/dispose + the unmount `loseContext` effect from `GhosttyTerminal.tsx` (630-680, 742-761) verbatim so context lifecycle matches production.
- Each tile owns its render-on-dirty loop calling `renderer.render(model)` — today's per-pane path. N loops, N contexts, N InputHandlers, N atlases.
- Sizing: each tile `renderer.resize(cols,rows)` from `fitDimensions(tileW, tileH)` of the measured CSS cell (`ResizeObserver` per tile, like production). Canvas at native `cell*dpr` — the per-pane coupling we measure against.
- **Baseline zoom analog (document it, do not strawman):** baseline has no native grid↔fullscreen morph. Use `transform: scale()` on the focused pane wrapper (cheap, raster-blurry — the realistic "naive" approach) as the like-for-like animation comparison. Note it in the HUD so "smoothness" compares honestly.

This variant implements `GridRenderer` only thinly (it really manages N sub-renderers); it reports `liveContexts = N`.

### Variant B — RECOMMENDED: Unified single-context renderer (transform-in-vertex, one batch)

The spine. One context, one atlas, one buffer, one draw for the whole grid. Smallest delta from working code: the **vertex shader is UNCHANGED** (pure pixel→NDC, `GhosttyWebGlRenderer.ts:147-148`), and `getGlyph`/atlas/color helpers are reused because the atlas already keys on `(style,text)` and tints per-vertex. New code is: `resizeCanvas` (decouple canvas size from one model), `walkTile` (the `render()` inner loop with scale+translate **baked into `a_position`** on the CPU — transform option A), and a thin dirty-gated per-tile vertex cache.

Core shape (grounds the synthesis):
- **ONE** WebGL2 context, **ONE** 2048² atlas, **ONE** vertex buffer. Canvas sized to the grid **container × dpr** (never grid-at-native), so the 16384/side cap is never approached.
- **Atlas:** rasterize every glyph ONCE at canonical `fontSize*dpr`; per-tile `scale` shrinks via the baked transform; NEVER re-key per tile-scale. All tiles share one font/size/dpr (enforced invariant — see Risk 4). Defer the `atlasGeneration`-bump retry to the WHOLE-grid pass boundary, not per-tile mid-grid.
- **Draw:** accumulate every tile's transform-baked quads (bg fill, glyph, cursor, selection, block-element, underline/strike) into ONE preallocated `Float32Array` via typed-array `.set()` from per-tile caches; one `bufferData(subarray)` + one `drawArrays`. `u_resolution` = canvas backing size, set once.
- **Dirty-gating:** per tile, `model.update()` (NONE/PARTIAL/FULL). A tile that is model-clean AND not animating (rect/scale/attention/focus unchanged, hashed) reuses its cached `Float32Array`; only dirty/animating tiles re-walk (consume `getViewport()` IMMEDIATELY — it's a recycled mutable pool overwritten on the next call, probe:ghostty-web-api). Per-tile sync-output gating preserved (Risk 6).
- **CRITICAL — typed-array hot path (NOT `render()` verbatim):** `pushQuad` today does `vertices.push(x,y,u0,v0,...rgba,...)` into a `number[]` then `new Float32Array(vertices)` per call (`GhosttyWebGlRenderer.ts:367,519-529`). Benchmarked at ~57ms/frame for 25 animating tiles = hard jank. The walk MUST write floats directly into a per-tile preallocated `Float32Array` via an index cursor (`out[p++]=...`), then `mega.set(tileCache.subarray(0,len), off)`; upload one `bufferData(mega.subarray(0,used))`. Same pattern measured ~1.5ms/frame. This single decision is the difference between the headline zoom looking smooth and stuttering. (Resolves the verdicts' load-bearing contradiction.)

### Variant C (optional) — RTT-per-tile freeze (held as a lever, build only if profiling demands)

Render each static tile once into an FBO texture (inside the SAME single context — FBOs are not extra contexts), then the compositor samples pixels instead of re-walking cells. The one thing transform-in-vertex provably *cannot* do: animate a static tile (zoom-out/reflow of 25 tiles) at ZERO content cost. Also structurally clips glyph overhang (FBO bounds), the principled fix if bleed is visible and the width-clamp is unacceptable.

Build C **only if** the cpuSubmitMs HUD shows the typed-array path stuttering on a 25-tile reflow. Keep it as a documented `GridRenderer` variant the harness can switch to, not v1. Its cost (N native-res textures ≈ 245MB → ~35MB with thumbnail clamp + resolution promotion on focus, mandatory `contextrestored` FBO rebuild) is more machinery than the A/B comparison needs, and its weakness — bilinear-blurry text on fullscreen *upscale* — is exactly the axis the brief cares about, where B wins.

**Decision: Build B as v1 (the recommendation). Build A as the control (required for the gate). Hold C as the lever.**

---

## 4. File list + responsibilities + run command

Everything lives under `app/grid-proto/` (sibling MPA entry, mirrors `test-harness/`), plus one line in `vite.config.ts`. Fully deletable (probe:harness-route-wiring).

| File | Responsibility |
|---|---|
| `app/grid-proto/index.html` | Unified-proto page. Mount `#grid-proto-root`, `<script type="module" src="./main.tsx">`. |
| `app/grid-proto/main.tsx` | `createRoot(...).render(<GridProto/>)`. **NO `StrictMode`** (canvas double-mount). |
| `app/grid-proto/GridProto.tsx` | Host harness: controls toolbar, perf HUD, instantiates `GridCompositor` with the selected `GridRenderer`, owns the scripted A/B sequence. |
| `app/grid-proto/GridCompositor.ts` | Owns the rAF loop, layout/grid math, animation clocks (zoom/pulse/reflow easing), focus state, and `GridInputController`. Computes every `TileFrame` per frame and calls `renderer.frame(...)`. Renderer-agnostic. |
| `app/grid-proto/GridRenderer.ts` | The `GridRenderer` interface + shared types (§2). |
| `app/grid-proto/UnifiedGridRenderer.ts` | **Variant B.** Single context/atlas/buffer, `walkTile` (transform-baked, typed-array hot path), dirty-gated per-tile cache, single draw. Reuses `getGlyph`/color/block-element logic adapted to write into preallocated Float32Array. |
| `app/grid-proto/BaselineGridRenderer.tsx` | **Variant A.** Manages N `WebGlTerminalRenderer` (unmodified) over N canvases in a CSS grid; reports `liveContexts=N`. (Renders its own DOM, so `.tsx`.) |
| `app/grid-proto/RttGridRenderer.ts` | **Variant C (optional).** FBO-per-tile + composite. Stub interface in v1; flesh out only if HUD demands. |
| `app/grid-proto/syntheticAgentFeed.ts` | Self-contained synthetic ANSI generator (§5). No daemon. |
| `app/grid-proto/GridInputController.ts` | One transparent grid-hit div for hit-testing + one roving contentEditable overlay + one `InputHandler` on the focused tile (§ interaction). |
| `app/grid-proto/perfHud.tsx` | FPS / frame-ms / draw calls / live context count / atlas uploads readout (§7). |

**`vite.config.ts` — one line** in `build.rollupOptions.input`:
```ts
input: {
  main: resolve(__dirname, "index.html"),
  "test-harness": resolve(__dirname, "test-harness/index.html"),
  "grid-proto": resolve(__dirname, "grid-proto/index.html"), // add — dev MPA entry
},
```
(Dev serves the HTML by path regardless; adding to `input` keeps `vite build`/`tsc` covering it.)

**Run (pure web, no daemon/Tauri):**
```bash
cd /Users/victor/projects/victor/attn--feat-grid/app
VITE_MOCK_PTY=1 npx vite --port 1421
# open http://localhost:1421/grid-proto/
```
> The HEADLINE context-cap number must be confirmed inside the actual Tauri WKWebView (the cap is a WKWebView property; desktop Chrome grants 25 contexts and HIDES the freeze). Pure-browser Vite is fine for functional iteration; host the route in the dev install (`make dev`) for the go/no-go context measurement, or accept the context claim as asserted-from-`5735c2a1`.

**Wiring sketch — `GridProto.tsx`:**
```tsx
const RENDERERS = {
  unified: () => new UnifiedGridRenderer(FONT_SIZE, FONT_FAMILY, THEME),
  baseline: () => new BaselineGridRenderer(FONT_SIZE, FONT_FAMILY, THEME),
  // rtt: () => new RttGridRenderer(...),   // optional lever
};

export function GridProto() {
  const [variant, setVariant] = useState<'unified'|'baseline'>('unified');
  const [dims, setDims] = useState({ rows: 5, cols: 5 });     // 2x2..5x5
  const [tileCount, setTileCount] = useState(25);
  const hostRef = useRef<HTMLDivElement>(null);
  const compRef = useRef<GridCompositor>();

  useEffect(() => {
    let comp: GridCompositor;
    (async () => {
      const ghostty = await Ghostty.load(ghosttyWasmUrl);
      const renderer = RENDERERS[variant]();
      comp = new GridCompositor(renderer, ghostty, hostRef.current!);
      compRef.current = comp;
      comp.setLayout(dims.rows, dims.cols);
      comp.spawnTiles(tileCount, startSyntheticAgent);   // feed wired here
      comp.start();                                       // begins rAF
    })();
    return () => comp?.dispose();   // loseContext on real unmount
  }, [variant]);  // re-mount renderer when variant flips (clean A/B)
  // ...toolbar + HUD bound to comp.stats
}
```

---

## 5. Synthetic data feed

Self-contained ANSI generator. No fixtures exist in-repo (probe:pty-feed confirmed: no `.cast`/`.jsonl` corpora), so synthetic is the right call: deterministic-enough, infinitely scalable, cheap (chunks <100B; the real budget is contexts, not bytes). Drives **~25 tiles** with spinners, colored 256-color logs, progress bars, and an occasional waiting-for-input prompt. Jittered 90–170ms per-tile intervals so 25 tiles don't update in lockstep.

Skip attn entirely (lightest seam): `Ghostty.load()` once, `createTerminal(cols,rows,cfg)` per tile, `startSyntheticAgent(s => model.write(s))` on the jittered interval, drain `hasResponse()/readResponse()` after each write (no real PTY).

```ts
// app/grid-proto/syntheticAgentFeed.ts — throwaway
type Sink = (text: string) => void;
const ESC='\x1b', FG=(n:number)=>`${ESC}[38;5;${n}m`, RESET=`${ESC}[0m`;
const HIDE=`${ESC}[?25l`, SHOW=`${ESC}[?25h`, CR='\r', EOL=`${ESC}[K`;
const SPIN=['⠋','⠙','⠹','⠸','⠼','⠴','⠦','⠧','⠇','⠏'];
const COLORS=[39,213,220,78,203,117];
const pick=<T,>(a:T[])=>a[(Math.random()*a.length)|0];
const TASKS=['Reading files','Running tests','Editing src/app.ts','Searching repo','Compiling'];
const LOG=['ok   parsed module','warn deprecated API','info cache hit','pass 124 assertions','edit +18 -4'];

export function startSyntheticAgent(sink: Sink) {
  let frame=0, line=0, barPct=0, task=pick(TASKS);
  type Phase='spin'|'bar'|'logs'|'prompt';
  let phase:Phase='spin', left=8+((Math.random()*12)|0);
  sink(`${ESC}[2J${ESC}[H${HIDE}${FG(245)}attn synthetic agent${RESET}\r\n`);
  const tick=()=>{
    frame++;
    if(phase==='spin') sink(`${CR}${EOL}${FG(pick(COLORS))}${SPIN[frame%SPIN.length]}${RESET} ${task}…`);
    else if(phase==='bar'){ barPct=Math.min(100,barPct+3+((Math.random()*6)|0));
      const w=24,f=Math.round(barPct/100*w); sink(`${CR}${EOL}${FG(78)}[${'█'.repeat(f)}${'░'.repeat(w-f)}]${RESET} ${barPct}%`);
      if(barPct>=100){left=0;barPct=0;} }
    else if(phase==='logs'){ line++; sink(`\r\n${FG(244)}${String(line).padStart(4)}${RESET} ${FG(pick(COLORS))}${pick(LOG)}${RESET}`); }
    else { sink(`\r\n${FG(220)}? ${RESET}Apply changes? ${FG(245)}(y/n)${RESET} ${HIDE}`); left=6; }   // waiting_input
    if(--left<=0){ const r=Math.random();
      phase = r<0.12?'prompt': r<0.4?'bar': r<0.7?'logs':'spin';
      left = phase==='logs'?3+((Math.random()*5)|0): phase==='bar'?12+((Math.random()*10)|0):6+((Math.random()*10)|0);
      task=pick(TASKS); if(phase!=='spin') sink('\r\n'); }
  };
  const h=setInterval(tick, 90+((Math.random()*80)|0));
  return ()=>{ clearInterval(h); sink(`${SHOW}${RESET}`); };
}
```

**Stress modes (for the risks, not the comfortable case):**
- `--torture` feed variant: floods distinct CJK/emoji wide glyphs across all 25 tiles to deliberately overflow the 2048² atlas and exercise `resetAtlas`/retry (Risk 2/atlas). Surface `atlasResets` in the HUD.
- At least two tiles render wide CJK + italic flush against right/bottom edges to make glyph overhang bleed visible (Risk 1).

**Optional recorded-replay hook:** `GridCompositor.spawnTiles` takes a `feed: (sink)=>stop` param; swap `startSyntheticAgent` for a `replayCast(bytes)` that pumps a captured byte log on its recorded timestamps. No corpus ships; this is a seam, not a feature.

---

## 6. Animations to demo + how each is driven in the rAF loop

All motion lives in `GridCompositor` as per-tile `TileFrame` re-bakes; the renderer just draws what it's handed. ONE rAF loop. Drawing only when (any animation active) OR (any model dirty).

- **Grid ↔ fullscreen zoom morph.** `zoomTo(tileId)` sets a target; each frame the compositor lerps the focused tile's `rect`+`scale` from its grid-cell to the full container over ~220ms (ease-out-cubic), and fades others' `alpha`. The renderer re-bakes the focused (and fading) tiles' quads with the animated transform. Crisp endpoint = `scale→dpr` (today's 1:1 path). **Geometry-stable handoff:** resize the focused model to fullscreen cols/rows BEFORE the morph starts (one reflow hidden behind the still-thumbnail tile), then animate scale into already-final geometry — turns end-of-morph from a re-layout pop into a pure scale lerp (Risk 5).
- **Attention pulse.** An animated inset border quad with `alpha = 0.15 + k*sin(t)`, emitted in the SAME batch — no extra pass, no FBO. Driven by `TileFrame.attention` ramped by the compositor; a tile entering a `prompt`/waiting phase raises attention.
- **Reflow on dimension change.** Changing N×M (controls) lerps each tile's `rect` to its new grid-slot target over ~220ms; the renderer re-bakes moving tiles. Worst case (25-tile reflow re-baking static geometry every frame) is the trigger to pull Variant C — gate on `cpuSubmitMs`, not assumption (Risk 2/re-walk).

**Interaction (single shared canvas) — driven each frame:** observer tiles get ZERO input. ONE persistent transparent grid-hit div resolves which tile via `clientX/Y` vs each tile's animated rect; the single contentEditable overlay + single `InputHandler` move to the focused tile. `cellFromPointer` is rebased to the tile's rect + scale-aware divisor (`cellWidth*scale/dpr`), reusing `GhosttyTerminal.tsx:763-779`. The overlay follows the animated focused-tile rect each frame so mid-morph alignment holds — but input is attached ONLY at rest (scale==dpr, integer rect); during the morph the focused tile is treated as non-interactive and clicks are swallowed/queued (Risk 3). Honors AGENTS #3/#6/#7.

---

## 7. Perf HUD

On-screen overlay (`perfHud.tsx`), identical metrics on both routes, also written to `$APPLOCALDATA/debug/grid-proto-<variant>.jsonl` (AGENTS disk-logging) so an agent can read results. From `GridRenderStats` + a frame-time ring buffer:

- **FPS** and **frame ms** (p50 / p95 / max from a rolling rAF-dt buffer; count of frames >32ms = dropped) + long-task count.
- **Draw calls** per frame (unified target: 1; baseline: N; RTT: K+1).
- **LIVE WebGL context count** — count successful `getContext('webgl2')` not `isContextLost()`. **The headline number.** Unified = 1 at any tile count; baseline = N, dropping past ~16. Also log every `webglcontextlost` (baseline emits them past the cap; unified should emit zero in steady state). Assert exactly one `getContext` in unified so a refactor can't silently reintroduce per-tile contexts.
- **Atlas uploads** (glyph slots uploaded this frame) and **atlas resets** (`resetAtlas` calls) — makes thrash visible under the torture feed.
- **cpuSubmitMs** (vertex-assembly + submit, summed across tiles) — assert <~8ms with all 25 animating; this is the lever-trigger for Variant C.
- Optional: peak JS heap (`performance.memory`) and a coarse VRAM proxy (1 atlas vs N) to show the multiplication.

**Scripted A/B sequence (same on both routes):** (a) idle 5s, (b) zoom-in on tile 12 over 220ms, (c) hold fullscreen 2s, (d) zoom-out, (e) reflow N×M→(N+1)×(M+1). Report dt p50/p95/max + dropped frames per phase.

---

## 8. Controls

Toolbar in `GridProto.tsx`:
- **Variant switch** — Unified / Baseline (/ RTT if built). Remounts the renderer for a clean A/B (run each route ALONE; never both at once — they'd fight for contexts).
- **Grid dimensions** — 2×2, 3×3, 4×4, 5×5 (drives `setLayout` + reflow).
- **Tile count** — 4 / 9 / 16 / 25 (sweep; the cliff is 16→25 where baseline crosses the cap).
- **Scaling-quality toggle** — LINEAR vs LINEAR_MIPMAP_LINEAR (mip chain on the atlas) for observer-tile minification quality.
- **Hide-tile** — set `TileFrame.hidden` on a chosen tile (excludes it from the batch / unmounts its surface).
- **Run scripted sequence** — fire the A/B animation/resize script and dump the HUD JSONL.
- **Torture feed** — toggle the CJK/emoji-flood feed.

**Fairness:** same font, theme, tile count, cols/rows, feed, jitter sequence, machine, display/dpr, and scripted sequence on both routes. Representative geometry: ~120×32 cols/rows per tile; 25 (5×5) is the target ceiling for the sweep.

---

## 9. Ordered build checklist (phased)

**Phase 0 — Harness + feed (no renderer yet).**
1. `app/grid-proto/{index.html, main.tsx, GridProto.tsx}` + the `vite.config.ts` line. Confirm `http://localhost:1421/grid-proto/` renders a blank shell.
2. `syntheticAgentFeed.ts`. Verify one `<canvas>` + one `WebGlTerminalRenderer` + one model + feed shows live spinner/log/bar/prompt output. (Proves the feed + model path before grid work.)
3. `GridRenderer.ts` interface + `perfHud.tsx` skeleton (FPS + context count).

**Phase 1 — Baseline (the control, the gate).**
4. `BaselineGridRenderer.tsx`: N unmodified `WebGlTerminalRenderer` in a CSS grid, per-tile `ResizeObserver`, per-tile `InputHandler`, verbatim unmount `loseContext`.
5. Wire HUD: confirm `liveContexts == N`, sweep 4/9/16/25, record where it freezes/drops a context. Add the `transform:scale` baseline zoom analog.

**Phase 2 — Unified spine (the recommendation).**
6. `GridCompositor.ts`: rAF loop, layout/grid math, per-tile `TileFrame` computation, dirty/animation gating.
7. `UnifiedGridRenderer.ts`: `resizeCanvas` (container×dpr), `walkTile` with **transform baked into `a_position`** and the **typed-array hot path** (preallocated per-tile Float32Array, `mega.set(...)`, one `bufferData`/`drawArrays`). Reuse `getGlyph`/color/block-element. Assert one context.
8. Dirty-gating: per-tile content hash + animation-transform hash; clean+static tiles reuse cached buffer. Defer atlas-gen retry to whole-grid pass boundary. Preserve per-tile sync-output gating.

**Phase 3 — Animation + interaction.**
9. Zoom morph (geometry-stable handoff), attention pulse (inset border quad in-batch), reflow lerp — all as `TileFrame` re-bakes in the compositor.
10. `GridInputController.ts`: grid-hit div + roving contentEditable overlay + single `InputHandler`; scale-aware `cellFromPointer`; input attached only at rest.

**Phase 4 — Measure + decide.**
11. Run the scripted A/B sequence on both routes (Tauri WKWebView for the headline context number). Dump HUD JSONL. Run the torture feed; eyeball glyph bleed at 5×5 (mip on/off).
12. **Gate:** if unified clearly wins on live context count + animation smoothness at 16–25 tiles, proceed. If `cpuSubmitMs` stutters on 25-tile reflow → build Variant C (`RttGridRenderer.ts`) as the FREEZE lever and re-measure. If text is muddy at 5×5 → enable mip chain (Risk + scaling).

---

## Consolidated risks & mitigations (from verification verdicts)

| # | Risk | Verdict | Mitigation (do cheap ones in v1) |
|---|---|---|---|
| 1 | **Glyph bleed on shared canvas.** `getGlyph` pads width +4 and lets `measureText` exceed the cell (`:421`); no per-canvas clip, so wide/italic/CJK glyphs at a tile's right/bottom edge bleed into the neighbor. | partial / open | Build the one-draw version FIRST, eyeball wide/italic/CJK at edges. If it bleeds: clamp glyph quad width to `cell*cellW` (keeps 1 draw, rare right-edge clip). Escalate to per-tile `gl.scissor` (N draws, still 1 context) or RTT (FBO clips) only if visibly unacceptable. Decide empirically; do not pre-build scissor. |
| 2 | **Per-frame vertex-assembly cost / animating-tile re-walk.** "Reuse `render()` verbatim" is the `number[]`+spread+`new Float32Array` path — ~57ms/frame for 25 animating tiles (40fps, visible stutter). Transform-in-vertex also re-bakes an animating tile every frame even when content is static. | partial (cost) / refuted-if-verbatim | **DELETE the `vertices.push(...rgba)` path.** Write into a preallocated per-tile `Float32Array` (`out[p++]=...`), `mega.set(...)`, one `bufferData(subarray)`. Measured ~1.5ms/frame. Per-tile cache keyed on content hash + transform hash; fade patches only the alpha float. Assert `cpuSubmitMs<~8ms` with all 25 animating. If a 25-tile reflow still stutters → pull Variant C (RTT freeze). |
| 3 | **Single-canvas input.** One canvas can't host hit-test AND handoff without redesign: coord math collapses to grid space, focus is ambiguous, selection bleeds, N InputHandlers fight; mid-morph hit-test is wrong with the fixed `cellWidth` divisor. Observer-tile link-click is removed. | partial | Split roles: ONE grid-hit div for hit-testing only + ONE focus surface owning ONE `InputHandler`. Observer tiles input-inert. Make `cellFromPointer` scale-aware (`floor((x-rect.x)/(cellWidth*scale/dpr))`). **Attach input only at rest** (scale==dpr, integer rect); treat the focused tile as non-interactive during the morph, swallow/queue clicks. Snap rest rect to integer device px. Decide explicitly whether observer tiles keep link-hover (lightweight hover hit-test on the grid-hit div if yes). Regression-assert identical cell coords pre/post-grid for a known click against a mouse-tracking model. |
| 4 | **Atlas: shared correctness + thrash.** Keys are `(style,text)` only (good, color tinted per-vertex) BUT rasterization bakes construction-time `fontSize/fontFamily/dpr` — a shared atlas serves wrong-size glyphs unless homogeneity is enforced. "Bounded" is false for adversarial CJK/emoji: union across 25 viewports can overflow 2048², and a 2nd same-frame reset makes the one-shot retry draw STALE UVs (garbage glyphs) — a correctness bug, not just perf. | partial | **Enforce font/size/dpr as grid-level constants** (assert/clamp per-tile); rasterize at canonical size, shrink via baked transform. **Make the retry guard safe:** on a 2nd same-frame reset, DROP the offending glyph quads (push blank) and mark grid dirty — convert "garbage glyphs" to "one incomplete frame." For the prototype it's bounded (small glyph set); confirm by logging `resetAtlas` under a long run + the torture feed. If it fires: bump to 4096² (one line, under `MAX_TEXTURE_SIZE`) or array-texture pages / LRU eviction. |
| 5 | **Zoom-morph crispness + handoff seam.** Atlas side is clean (rasterized once; `scale→dpr` = today's 1:1). But continuous morph at fractional scale with LINEAR-only minify is soft; "fullscreen = today's pixel-perfect path" mischaracterizes today's path (today reflows to MORE cols at same cell size, never magnifies). Geometry seam: thumbnail cols/rows ≠ fullscreen cols/rows → end-of-morph re-layout pop + possible dropped first keystroke. | partial / refuted (scaling) | For observer tiles, **mip chain** the atlas (`generateMipmap` + `LINEAR_MIPMAP_LINEAR`, +2–4px inter-glyph padding) so 3×–5× minify is anti-aliased not shimmery — make this a v1 toggle, validate 2×2 vs 5×5 with screenshots. For crisp fullscreen, **resize the model to fullscreen cols/rows BEFORE the morph** (reflow up front, hidden), animate scale into final geometry, do InputHandler dispose+reparent+recreate synchronously at morph completion. The prototype must actually exercise this seam (`model.resize()` on focus), not hide it behind a fixed `createTerminal(cols,rows)`. |
| 6 | **rAF loop vs synchronized-output tearing.** Current renderer is event-driven (draws at the `\x1b[?2026l` boundary, commit `aa1f3683`), NOT rAF-driven. A naive "rAF draws when any model dirty" samples a model mid-2026-block, reintroducing the tearing the codebase already fixed. | partial (hidden regression) | Carry per-tile `SynchronizedOutputState` into the tile descriptor; the rAF loop decides WHEN to composite, but a tile's content snapshot is taken on its OWN sync boundary (or the 1000ms fallback) — SKIP re-baking a tile whose 2026 block is open. Preserve the per-tile `aa1f3683` guarantee. |
| 7 | **Single-context blast radius.** One context = one point of failure for all 25 tiles. Today's per-pane handler just tells the user to reopen — unacceptable when it blanks the whole grid. | confirmed (the win) / must-handle | `webglcontextlost`→`preventDefault`; implement `webglcontextrestored`→rebuild program+buffer+atlas (+FBOs if RTT), clear caches, re-render from MODELS with `force=true` (NEVER replay PTY bytes — AGENTS #7). Deterministic `loseContext()` on grid UNMOUNT only, never on font/theme re-init that reuses the canvas. Context-loss/restore must NOT emit `pty_resize` or change geometry. |
| 8 | **WKWebView vs Chrome for the headline number.** The context cap is a WKWebView property; Chrome grants 25 and HIDES the freeze. | open | Pure-browser Vite is fine for functional iteration, but run the go/no-go context measurement inside the Tauri WKWebView (dev install). Otherwise the context-count claim is asserted-from-`5735c2a1`, not re-measured. |
| 9 | **DPR / multi-display.** `dpr` captured once and baked into atlas + sizing; moving between displays of different dpr is unhandled (pre-existing). | open (accepted) | Single-display is fine for the measurement; confirm the test machine's dpr is the one baked. Note multi-display is out of scope for the throwaway. |

**Net:** the unified single-context renderer (Variant B) is the cheapest core that solves the real constraint (context count), structured as renderer-as-pure-function + compositor-owns-motion, with RTT (Variant C) held as the one optimization the core provably cannot do. The baseline (Variant A) is the go/no-go gate, not a formality.

---

## Spike results (2026-06-05) — BUILT, gate PASSED

Prototype built under `app/grid-proto/`, runs at `http://localhost:4399/grid-proto/`
(`VITE_MOCK_PTY=1 npx vite --port 4399` from `app/`, no daemon). Variants A + B both live; C (RTT)
intentionally not built — not needed. Measured headless (Chromium + SwiftShader, 25 tiles @ 80×24, 5×5):

| Metric | Unified (B) | Baseline (A) |
|---|---|---|
| **Live WebGL contexts** | **1** | **16** (requested 25 → browser capped; ~9 tiles render dead-white) |
| Draw calls / frame | **1** | 6–8 |
| cpuSubmitMs (25 animating) | **0.9–1.3 ms** | n/a |
| Frame p95 | ~29–37 ms | **84–100 ms** |
| Dropped (>32ms) | 3–10 | 44–59 |
| Atlas resets under torture (CJK/emoji ×25) | **0** | n/a |

**Findings vs the spec's risks:**
- **Risk 2 (hot path) — decisive.** Typed-array path holds cpuSubmitMs at ~1ms for 25 animating tiles
  (the feared `number[]`+spread path was ~57ms). This is the whole game.
- **Risk 4 (atlas thrash) — not observed.** Torture feed (CJK + emoji ×25) produced **0 atlas resets**;
  the union glyph set fits 2048². Bump to 4096² only if real transcripts overflow.
- **Context cap — demonstrated, not just asserted.** Baseline visibly breaks (dead-white tiles) at the
  cap even in Chrome; unified is flat at 1.
- **Animations** — zoom morph (crisp focused tile), attention pulse (orange border on `prompt`/waiting
  tiles), reflow-on-dimension-change — all run in one rAF loop, 1 draw call.

**Caveats / not yet done:**
- The ~36fps unified figure is a **SwiftShader (software GL)** headless artifact; the load-bearing
  number is cpuSubmitMs (~1ms). Real GPU (Metal/WKWebView) expected at 60fps — confirm on-device.
- **Headline WKWebView context number** (open Q#8) still needs a read from the Tauri dev install.
- **Typing handoff** (roving InputHandler on the focused tile, Risk 3) **not built** — prototype is
  observer-only + click-to-zoom. Main gap before spike → feature.
- Glyph bleed (Risk 1) not visibly problematic at 5×5; revisit width-clamp if it shows on-device.
