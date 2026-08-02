# Ghostty VT WASM

attn uses the `ghostty-web` 0.4.0 JavaScript wrapper with a patched Ghostty
terminal core.

- Ghostty source: `56237efeefaf7082a82854eba1fbaa93868925e8`
- ghostty-web WASM API patch: `9e4e126d89ac3537d2b2ebec075849851566de9f`
  (`v0.4.0`)
- Zig: `0.15.2`
- Build target: `wasm32-freestanding`
- Optimization: `ReleaseSmall`
- SHA-256: `6c4f21f514be21b13ff0911817458c69f26d22fb41469c10b68c322627266e85`

## Why this pin

**June 2026 — OSC 8 capacity corruption.** The Ghostty commit bundled by
`ghostty-web` 0.4.0 corrupts page state when OSC 8 hyperlinks exhaust capacity
during repeated resize/reflow. The immediate parent of `29d4aba` fails attn's
captured production replay (`app/src/utils/ghosttyHyperlinks.test.ts`'s
territory — a replay of real terminal output, distinct from the resize-hang
repro below); `29d4aba` passes. Applying only its `startHyperlink` migration
from `adjustCapacity` to `increaseCapacity` also passes. That is why the pin
moved to `29d4aba`.

**August 2026 — the same bug one function over, and the pinning mistake that
exposed it.** `29d4aba` is a *mid-PR* commit inside ghostty PR #10337, and it
introduced a second hyperlink-capacity defect of its own: `cursorSetHyperlink`
grew a hand-rolled capacity-doubling loop (the author's own
`// FIXME: This SUCKS`) that can spin forever. In attn that is a frozen pane at
100% CPU with no trap and therefore no automatic recovery. Deterministic
repro: `app/scripts/repro-ghostty-vt-resize-hang.mjs` (exit 0 fixed, 1 hang,
2 trap), guarded in CI by
`app/src/utils/ghosttyVtWasm.resizeHang.test.ts`. A bisect over ghostty history
found `29d4aba` to be the only commit that reproduces it; its direct child
`25b7cc9f2cc28071d9d07f3a96ab86c811f1d1e1` ("terminal: hyperlink state uses
increaseCapacity on screen") fixes it by replacing that loop with
`increaseCapacity(.string_bytes)` — the same capacity family as the June fix.

The pin therefore moves forward to `56237efee`, PR #10337 **as merged to
ghostty main**, which contains that fix plus the rest of the PR's PageList
overflow detection and protection. The lesson is the general one: pin
main-line merge commits, not mid-PR commits — a mid-PR commit is a state the
author never intended anyone to ship, and pinning one is what made this bug
reachable.

## Compat patch

On top of ghostty-web's own WASM API patch, `ghostty-web-v0.4.0-compat.patch`
adds two per-cell OSC 8 hyperlink URI accessors not present in that API:
`ghostty_render_state_get_hyperlink_uri`
(active area) and `ghostty_terminal_get_scrollback_hyperlink_uri`
(scrollback), mirroring the existing grapheme accessors' signature and
buffer-truncation contract.

Rebuild with:

```bash
app/scripts/build-ghostty-vt-wasm.sh
```

Ghostty and ghostty-web are MIT licensed; the corresponding license texts are
included beside the binary.
