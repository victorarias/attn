# native-ui

## Learnings

After completing a meaningful spike or feature, add to this file any decisions that **seemed to work but caused rework** — not compiler errors, but choices that passed an early test, looked reasonable at the time, and then had to be undone or rewritten later. Compiler errors are self-correcting; these aren't.

---

### Div-based rendering cannot fill cell backgrounds to full row height

When implementing terminal cell backgrounds with GPUI's div/flex system, `.bg()` on a span is constrained to text layout height — not the full cell height. This looked correct at a glance (colors appeared) but caused visible artifacts with ANSI art and box-drawing characters. Attempts to fix it with `h_full()`, row height adjustments, and flex tweaks all failed and had to be reverted.

**Rule:** As soon as pixel-exact cell backgrounds are required, go straight to `Element` trait + `window.paint_quad(fill(...))`. Don't try to approximate it with div layout.

---

### Panel frame scales with zoom; terminal surface inside does not

Panel width/height are world-space values. Screen size = `world_size * zoom`. Both position and size scale with zoom so the canvas feels like tldraw.

The exception is the `TerminalSurfaceElement` painted inside a panel: it renders at a fixed cell size regardless of zoom so terminal text stays readable. The panel frame scales; the terminal content inside does not.

Storing panel sizes as fixed screen pixels (only position scaling) makes panels overlap when zoomed out and stay the same size when zoomed in — it does not feel like a canvas.

---

### `Pixels.0` is private in GPUI 0.2 — use `f32::from(pixels)` or `pixels / pixels`

`pub struct Pixels(pub(crate) f32)` — the inner field is crate-private. To extract a float: `f32::from(p)` (via `impl From<Pixels> for f32`) or `p / px(1.0)` (via `impl Div<Pixels> for Pixels → f32`). The `pf()` helper wraps `f32::from(p)`.
