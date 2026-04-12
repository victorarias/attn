# native-ui

## Learnings

After completing a meaningful spike or feature, add to this file any decisions that **seemed to work but caused rework** — not compiler errors, but choices that passed an early test, looked reasonable at the time, and then had to be undone or rewritten later. Compiler errors are self-correcting; these aren't.

---

### Div-based rendering cannot fill cell backgrounds to full row height

When implementing terminal cell backgrounds with GPUI's div/flex system, `.bg()` on a span is constrained to text layout height — not the full cell height. This looked correct at a glance (colors appeared) but caused visible artifacts with ANSI art and box-drawing characters. Attempts to fix it with `h_full()`, row height adjustments, and flex tweaks all failed and had to be reverted.

**Rule:** As soon as pixel-exact cell backgrounds are required, go straight to `Element` trait + `window.paint_quad(fill(...))`. Don't try to approximate it with div layout.

---

### Panel frame and terminal content both scale with zoom; cols/rows do not

Panel width/height are world-space values. Screen size = `world_size * zoom`. Both position and size scale with zoom so the canvas feels like tldraw.

Terminal rendering (font size, row height) also scales with zoom: `text_size = BASE_SIZE * zoom`, `line_height = ROW_HEIGHT * zoom`. This makes the whole panel — frame and content — shrink and grow together, which is the correct tldraw feel.

**What does NOT change with zoom:** terminal cols/rows. Those are computed from world-space panel dimensions (`world_w / CHAR_WIDTH`, `(world_h - TITLE_HEIGHT) / ROW_HEIGHT`) and only change when the user drag-resizes the panel, triggering a `PtyResize` to the daemon.

The two concerns are separate:
- **Rendering zoom** → `TerminalView.zoom` field, updated every canvas frame, scales font and row height
- **Logical size (cols/rows)** → derived from world-space panel dimensions, zoom-invariant, drives `PtyResize`

Storing panel sizes as fixed screen pixels (only position scaling) makes panels overlap when zoomed out and stay the same size when zoomed in — it does not feel like a canvas.

---

### Focus routing: read `focus_handle` from the entity, don't store it separately in the canvas

When the canvas needs to focus a specific `TerminalView` on a mouse click, the correct pattern is:

```rust
panel.view.read(cx).focus_handle.clone().focus(window);
```

Do not try to cache focus handles separately in the canvas — they can drift. Reading directly from the entity at click time is always current.

Also: focusing in a `needs_focus_panel` flag + `render()` application (same pattern as spike 2's `needs_focus`) avoids calling `focus(window)` from inside `spawn_panel` where `window` is not available.

---

### `Pixels.0` is private in GPUI 0.2 — use `f32::from(pixels)` or `pixels / pixels`

`pub struct Pixels(pub(crate) f32)` — the inner field is crate-private. To extract a float: `f32::from(p)` (via `impl From<Pixels> for f32`) or `p / px(1.0)` (via `impl Div<Pixels> for Pixels → f32`). The `pf()` helper wraps `f32::from(p)`.
