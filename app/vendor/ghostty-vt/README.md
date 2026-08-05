# Ghostty VT WASM

attn uses the `ghostty-web` 0.4.0 JavaScript wrapper with a WASM-only adapter
over Ghostty's current terminal C API.

- Ghostty source: `ghostty-vt.pin` (`ab0b9da9e88fcb4b0533a1854e84628f663930af`)
- ghostty-web JavaScript API: `0.4.0`
- Zig: `0.16.x` (`0.16.0` in `.tool-versions`)
- Build target: `wasm32-freestanding`
- Optimization: `ReleaseSmall`
- Provenance: `ghostty-vt.lock` (source inputs, Zig version, and output SHA-256)

## Shared source pin

The frontend WASM model and the native worker library both read
`ghostty-vt.pin`. A Ghostty source bump therefore moves the parser and terminal
storage implementation together; target-specific patches only expose the APIs
each embedder needs.

The previous WASM build stopped at `56237efe`. In `ReleaseSmall`, that revision
could reuse a freed page buffer without zeroing it. A scroll or reflow could
then expose stale cells carrying hyperlink flags without the corresponding
hyperlink map, and a later capacity growth trapped as `unreachable`. Ghostty
fixed the allocator invariant in `420de124`; the shared `ab0b9da` pin contains
that fix. Both self-contained production captures from 2026-08-05 replay
cleanly on this build, including the latest 101,873-byte restore plus 11 live
operations (256,617 bytes), repeated five times.

The shared pin also closes the old OSC 133 behavior skew. Marker bytes now
reach both the worker and browser models, and the generated kitty wire-rewrite
corpus proves their resulting grids match.

## Compatibility adapter

`ghostty-web@0.4.0` predates Ghostty's current C API. The vendored
`ghostty-web-v0.4.0-compat.zig` adapter preserves that JavaScript ABI while
delegating VT parsing, resize, render-state updates, scrollback, modes, and
query responses to the current core. The adjacent patch wires those exports
into the WASM build and leaves the native C ABI unchanged.

The adapter also exposes the two attn-specific per-cell OSC 8 URI accessors:
`ghostty_render_state_get_hyperlink_uri` and
`ghostty_terminal_get_scrollback_hyperlink_uri`.

Rebuild with:

```bash
app/scripts/build-ghostty-vt-wasm.sh
```

The build rewrites `ghostty-vt.lock`. Normal app builds and tests verify that
the shared pin, adapter, patch, build recipe, and vendored binary still match
that lock, so changing the pin cannot silently leave the browser on an older
core.

Ghostty and ghostty-web are MIT licensed; the corresponding license texts are
included beside the binary.
