# Ghostty VT WASM

attn uses the `ghostty-web` 0.4.0 JavaScript wrapper with a patched Ghostty
terminal core.

- Ghostty source: `29d4aba03337d7e9d6a0c357969e8924240b84dd`
- ghostty-web WASM API patch: `9e4e126d89ac3537d2b2ebec075849851566de9f`
  (`v0.4.0`)
- Zig: `0.15.2`
- Build target: `wasm32-freestanding`
- Optimization: `ReleaseSmall`
- SHA-256: `e806cc51c1a76c3bef66c9ff179c6ccae09b021f6fe7b2bd91f373a5eb4237ea`

The Ghostty commit bundled by `ghostty-web` 0.4.0 corrupts page state when
OSC 8 hyperlinks exhaust capacity during repeated resize/reflow. The immediate
parent of `29d4aba` fails attn's captured production replay; `29d4aba` passes.
Applying only its `startHyperlink` migration from `adjustCapacity` to
`increaseCapacity` also passes.

Rebuild with:

```bash
app/scripts/build-ghostty-vt-wasm.sh
```

Ghostty and ghostty-web are MIT licensed; the corresponding license texts are
included beside the binary.
