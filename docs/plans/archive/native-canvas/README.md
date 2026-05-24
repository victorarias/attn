# Archived Native Canvas Direction

These documents and their GPUI implementation explored an infinite-canvas
frontend for Attn. That product direction was abandoned on 2026-05-23.

Retained reference material:

- `native-gpui-canvas-ui.md`: feasibility spikes and automation work.
- `native-canvas-architecture.md`: layering and daemon-client reasoning.
- `2026-04-28-native-canvas-mvp.md`: former product scope.
- `2026-04-28-canvas-perf-spike.md`: rendering performance investigation.
- `../../../prototypes/archive/native-canvas/native-design-system-observatory.html`:
  the Observatory visual language explored for GPUI.

The code is retained under `native-ui/crates/attn-native-canvas-archive/`
with its matching protocol-v64 subset at
`native-ui/crates/attn-native-canvas-protocol-archive/`.
Its canvas-only automation scenario is retained at
`app/scripts/real-app-harness/archive/native-canvas/scenario-native-canvas.mjs`.

Do not treat canvas panels, panel geometry, or the protocol-v64 encoding of
`shell_as_session` as active architecture. The first-class shell-session need
is retained and must be represented through the current workspace-layout
protocol. Active native-client work is described in
`../../native-gpui-workspace-client-prototype.md`.
