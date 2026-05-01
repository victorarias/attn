# native-ui

Architecture conventions and learnings for the GPUI native canvas client.

## Architecture

The native app follows **MVVM with a hexagonal edge**. Four directories under
`crates/attn-native-app/src/`, each with one responsibility:

| Directory   | Holds                                                                                  |
|-------------|----------------------------------------------------------------------------------------|
| `adapters/` | Everything that talks to the outside world (websocket, TCP sidecar).                   |
| `state/`    | Long-lived observable application state. GPUI entities. The "ViewModels".              |
| `views/`    | `Render` impls. Sidebar, canvas, terminal view, overlays.                              |
| `domain/`   | Pure logic. No `Window`, `Context`, or entity types. Unit-testable without a window.   |

Plus two top-level files:

- `app.rs` — the coordinator. Owns registries, wires adapters → state, hands state
  handles to views. Should stay slim (~150 lines once extractions land).
- `main.rs` — entrypoint. Window setup only.

### Decision rules

When adding code, ask one question:

1. **Does it talk to the network, filesystem, or another process?** → `adapters/`
2. **Is it stateful, observable, and survives re-renders?** → `state/`
3. **Does it produce pixels?** → `views/`
4. **Is it pure logic with no `Window`/`Context`/entity dependency?** → `domain/`
5. **Does it stitch the layers together?** → `app.rs`

### Dependency direction

Dependencies point inward, with two pragmatic exceptions for GPUI's grain:

- **Adapters** know nothing about views or state, with one exception:
  *action handlers* inside an adapter (e.g. `adapters/automation/actions.rs`)
  may dispatch into state entities, because they're the bridge between the
  outside-world request and the app's internal command surface. The wire
  layer of the adapter (server, protocol, manifest) stays pure. They emit
  events outward (`cx.emit(DaemonEvent::...)`) and expose command methods
  callers invoke.
- **State** entities may subscribe to adapter events (`cx.subscribe(&adapter, ...)`)
  and call adapter command methods. They never call into views. Domain
  state (sessions, workspaces) lives in state registries on `NativeApp`,
  not inside adapters — `DaemonClient` parses the wire and emits events,
  but the canonical "what sessions exist?" lives in `SessionRegistry`.
  Connection-meta state (`connected`, `error`) is the one thing adapters
  legitimately own about themselves.
- **Views** observe state and may hold adapter handles **only** for outbound
  commands (e.g. `TerminalView` sending `PtyInput`). Views never read cached
  state from an adapter — they read it from a state entity.
- **Domain** imports neither GPUI runtime types (`Window`, `Context`,
  `Entity`, `App`) nor any sibling module. It can use GPUI value types
  (`Pixels`, `Point`, `Bounds`).
- **`app.rs` does no logic.** It wires. If it's branching on data, the
  branch belongs in a state entity.

#### Pragmatic exception: state holding view entity handles

State entities may hold `Entity<View>` handles for views that encapsulate
per-instance view state. Example: `Panel` (state) holds
`Entity<TerminalView>` (view). This is the canonical GPUI pattern for
component-like views that bundle state + rendering (mirrors Zed's
`Editor`, `Pane`). Strict MVVM separation here would mean a parallel
`HashMap<session_id, Entity<TerminalView>>` in the view layer with
lookup-at-render-time plumbing — not worth it. The exception is narrow:
state holds the handle; state still does not call `.render(...)` or read
view-only fields. Views are observed normally.

### Why these conventions

- A reactive desktop UI in GPUI maps cleanly onto MVVM: entities are
  ViewModels, `Render` impls are Views, observation is data binding.
- The hexagonal `adapters/` boundary captures most of the value of
  ports-and-adapters without the ceremony — at this scale (~6k LOC), full
  hexagonal would be overkill.
- The daemon-broadcast-as-source-of-truth rule (no optimistic UI) is
  enforced by the dependency direction: adapters push, state holds, views
  observe.

## Design system — Observatory

Every color, radius, and spacing the GPUI views render comes from
`crates/attn-native-app/src/theme.rs`. The visual language is
documented in plates at
[`docs/prototypes/native-design-system-observatory.html`](../docs/prototypes/native-design-system-observatory.html)
— open it to see how every primitive composes.

The system is named *Observatory*. Agents are stars (Claude `α`, Codex
`β`, Shell `γ`); states are observation classifications (`transitus`,
`occultatum`, `retentum`, `inscriptum`, …); the canvas is the chart on
which they're plotted. The metaphor stays out of identifier names —
`theme::ink`, `theme::moon`, `theme::sodium` are about color roles, not
astronomy — but the visual language earns its character from it.

### Token vocabulary

| Module            | Holds                                                          |
|-------------------|----------------------------------------------------------------|
| `theme::ink`      | Six surface inks, deepest (`void`) to lightest (`firm`).       |
| `theme::moon`     | Five text values, primary (`moonstone`) to disabled (`cinder`).|
| `theme::sodium`   | The single accent — `vapor`, `deep`, plus `soft` / `glow` / `hush` translucent tints. |
| `theme::star`     | Three agent identities — `claude`, `codex`, `shell`.            |
| `theme::state`    | Seven observation classifications — working, halted, etc.       |
| `theme::surface`  | Composite tokens for row states (selected, danger, pending).   |
| `theme::line`     | Four hairline opacities — `weak` (.05) → `bold` (.28), moonstone-tinted alpha. |
| `theme::radius`   | Three radii — `R0` (4px), `R1` (7px), `R2` (12px).              |
| `theme::space`    | Seven-step spacing ramp — `S0` (4px) through `S6` (56px).       |
| `theme::motion`   | Duration tokens in seconds — `TIGHT`/`NORMAL`/`RELAXED` for UI; `BREATH_LIVE`/`BREATH_HALTED`/`AURORA` for ambient. |

### Rules

1. **Color is meaning.** Hue carries either *identity* (which agent) or
   *status* (what state) — never decoration.
2. **One accent.** Sodium-vapor orange (`theme::sodium::vapor`). If
   sodium appears, it is the most important pixel on screen.
3. **Three radii, no more.** `R0` / `R1` / `R2`. Anything else is a bug.
4. **Six surface inks.** Cool blue-black. Never pure black, never
   tinted toward purple.
5. **Identity and status are independent.** A working Codex panel
   shows the Codex blue (identity) plus the working green (status).
   They never share a hue.

Raw `rgb(0xNNNNNN)` and `rgba(0xNNNNNNNN)` literals are forbidden in
`views/`, `app.rs`, and elsewhere outside `theme.rs`. The module's
tests guard the load-bearing invariants:

- idle visual weight ≡ dim metadata (`state::idle` ≡ `moon::ash`);
- the sodium tints share the vapor hue (`soft` / `glow` / `hush` only
  vary in alpha — there is no "second orange");
- the hairline scale is monotone (`line::weak` < `mild` < `firm` < `bold`);
- halted breath is faster than live breath (the urgent voice pulses
  faster than the working one);
- agent star hues are pairwise distinct;
- state hues are pairwise distinct.

### Working in views

```rust
use crate::theme;

div()
    .bg(theme::ink::nocturne())
    .border_color(theme::line::firm())  // hairline (translucent)
    .text_color(theme::moon::moonstone())
    .rounded(px(theme::radius::R1))
```

`theme::ink::firm()` and `theme::line::firm()` are *both* legitimate
border colors — they read differently. `ink::firm` is opaque cool
blue-black (a structural region edge); `line::firm` is moonstone alpha
that blends onto whatever ink it sits on (a hairline that feels of the
warm palette). Use `line::*` for in-card dividers, faint chrome edges,
and anything that should read as "barely there"; use `ink::firm` when
you need the harder, region-defining contrast.

For status indicators driven by a protocol enum, call the resolver
helper rather than re-`match`ing in the view:

```rust
fn status_badge(status: WorkspaceStatus) -> impl IntoElement {
    div().w(px(8.)).h(px(8.)).rounded_full()
        .bg(theme::workspace_status_color(status))
}
```

For agent identity from a wire-level identifier:

```rust
let star_color = theme::star::for_agent_id(&agent_id);  // "claude" → peach, etc
```

### Adding a token

1. Add a `pub const FOO_HEX: u32 = 0x...;` to the right submodule of
   `theme.rs`.
2. Add a `pub fn foo() -> Rgba { rgb(FOO_HEX) }` next to it.
3. Document what it means and where it gets used.

That's it. No coordinator file, no enum to extend, no match arms to keep
in sync. When a submodule grows past ~30 tokens, split `theme.rs` into a
`theme/` directory with one file per submodule — the public API stays
the same.

### When tokens don't fit

If a view needs a color that doesn't map cleanly to an existing token,
pause and ask: *is this a missing primitive, or am I about to invent
decoration?*

- **Missing primitive** — add a token (e.g. a new `surface::*` for a
  composite row state, a new `state::*` if the protocol grows a
  classification). Update the visual plate so the prototype stays
  authoritative.
- **Decoration** — find an existing token. If you need a color to
  "pop", you almost always want `sodium::vapor`, and the rule is that
  it's already the most important pixel — putting it on something
  ordinary devalues every other appearance of it.

### Migration notes

The migration from ad-hoc `rgb(0xNNNNNN)` literals is complete in
`views/canvas.rs`, `views/sidebar.rs`, `views/terminal_view.rs`,
`views/fps_overlay.rs`, and `app.rs`. New views should never reintroduce
raw hex.

The terminal's 16-entry ANSI palette in `views/terminal_view.rs` is
intentionally **not** themed — external tools paint with ANSI escapes
expecting standard colors, and overriding them would break user
expectations. Only the chrome (panel bg, default fg, cursor) follows
the design system.

## Learnings

After completing a meaningful spike or feature, add to this file any decisions
that **seemed to work but caused rework** — not compiler errors, but choices
that passed an early test, looked reasonable at the time, and then had to be
undone or rewritten later. Compiler errors are self-correcting; these aren't.

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
