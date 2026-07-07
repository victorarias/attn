# Ghostty VT WASM

attn uses the `ghostty-web` 0.4.0 JavaScript wrapper with a patched Ghostty
terminal core.

- Ghostty source: `29d4aba03337d7e9d6a0c357969e8924240b84dd`
- ghostty-web WASM API patch: `9e4e126d89ac3537d2b2ebec075849851566de9f`
  (`v0.4.0`)
- Zig: `0.15.2`
- Build target: `wasm32-freestanding`
- Optimization: `ReleaseSmall`
- SHA-256: `7cf78364e94259234b7d09f7001c0b75f94ead6f59a80d58626ed8015eb1dc69`

The Ghostty commit bundled by `ghostty-web` 0.4.0 corrupts page state when
OSC 8 hyperlinks exhaust capacity during repeated resize/reflow. The immediate
parent of `29d4aba` fails attn's captured production replay; `29d4aba` passes.
Applying only its `startHyperlink` migration from `adjustCapacity` to
`increaseCapacity` also passes.

The compat patch also adds two per-cell OSC 8 hyperlink URI accessors not
present in the upstream WASM API: `ghostty_render_state_get_hyperlink_uri`
(active area) and `ghostty_terminal_get_scrollback_hyperlink_uri`
(scrollback), mirroring the existing grapheme accessors' signature and
buffer-truncation contract.

Rebuild with:

```bash
app/scripts/build-ghostty-vt-wasm.sh
```

Ghostty and ghostty-web are MIT licensed; the corresponding license texts are
included beside the binary.
