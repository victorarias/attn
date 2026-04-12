# native-ui

## Learnings

After completing a meaningful spike or feature, add to this file any decisions that **seemed to work but caused rework** — not compiler errors, but choices that passed an early test, looked reasonable at the time, and then had to be undone or rewritten later. Compiler errors are self-correcting; these aren't.

---

### Div-based rendering cannot fill cell backgrounds to full row height

When implementing terminal cell backgrounds with GPUI's div/flex system, `.bg()` on a span is constrained to text layout height — not the full cell height. This looked correct at a glance (colors appeared) but caused visible artifacts with ANSI art and box-drawing characters. Attempts to fix it with `h_full()`, row height adjustments, and flex tweaks all failed and had to be reverted.

**Rule:** As soon as pixel-exact cell backgrounds are required, go straight to `Element` trait + `window.paint_quad(fill(...))`. Don't try to approximate it with div layout.
