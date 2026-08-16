# Going first-party on libghostty-vt

Written 2026-08-15, after ghostty-org published prebuilt `ghostty-vt.wasm`
assets and a run of wasm-targeted optimization commits.

attn's browser terminal runs Ghostty already, so "faster than xterm.js" is
not the question. The question is what separates our vendored build from
what upstream now ships, and the answer turned out to be larger than a
version bump.

## Where we stand today

The frontend uses the `ghostty-web` npm package (0.4.0) for its JavaScript
terminal model, backed by a wasm binary we build ourselves from the shared
`ghostty-vt.pin` (`ab0b9da`, 2026-08-05). That build carries a vendored
compatibility adapter — `ghostty-web-v0.4.0-compat.zig` plus its patch —
because ghostty-web's ABI predates Ghostty's current C API. The native
worker builds from the same pin with a second carried patch,
`ghostty-vt-native.patch`, which adds `ghostty_terminal_serialize_vt` for
the server-authoritative restore path.

Two carried patches, a zig 0.16 dependency in the frontend build, and a
lock file per side.

`ghostty-web` is Coder's, not ghostty-org's. npm latest is 0.4.0, published
2026-06-28; the repository's last commit is 2026-07-02. We are pinned to a
third-party ABI that has stopped moving, and paying for the mismatch in
vendored zig.

## What upstream ships now

`main` is 535 commits past our pin. The tip release carries prebuilt
`ghostty-vt.wasm` (ReleaseFast, 1.1MB) and `ghostty-vt-small.wasm`
(ReleaseSmall, 780KB) built from `d760ee96`, alongside a
`libghostty-vt-source.tar.gz` scoped to the VT library.

Diffing the exports of our vendored binary against the prebuilt one:

- 26 symbols exist only in ours. Every one is a compat shim. Two of them,
  `ghostty_terminal_mode_set/get`, upstream **removed** on 2026-08-06 in
  favour of `GHOSTTY_TERMINAL_OPT_MODE` — so a pin bump alone is a
  breaking change for the adapter, not a recompile.
- Upstream adds `ghostty_snapshot_encode` / `ghostty_snapshot_decoder_*`,
  `ghostty_terminal_continuation_*`, and `ghostty_terminal_vt_write_until_ground`.

It also exposes first-party equivalents of everything the adapter was
written to provide: per-cell hyperlink URIs via `ghostty_grid_ref_hyperlink_uri`
and a `GHOSTTY_CELL_DATA_HAS_HYPERLINK` flag, key and mouse encoders, and
per-cell OSC 133 semantic content.

## The carried native patch is dead

`ghostty-vt-native.patch` exists because the plain terminal formatter
serializes only the active screen, losing the primary screen behind an
alt-screen app such as vim. Our 170-line patch reconstructs the ordering by
hand — palette, primary content, `?1049h`, alt content — and
`internal/ghosttyvt` then appends a corrective `CUP` because the dump emits
the cursor before tabstop resets move it.

Upstream's snapshot API replaces all of it. A snapshot is a versioned,
CRC32C-protected record stream (`GHOSTSNP`, format version 1) whose READY
marker follows enough state to render and resume, with older scrollback
following as prependable pages.

Driven through the prebuilt wasm, on a terminal carrying scrollback, SGR
and truecolor styling, an OSC 8 hyperlink, OSC 133 marks, wide CJK and ZWJ
emoji:

| case | result |
|---|---|
| primary screen, encode → decode → re-encode | byte-identical (15,082B) |
| alternate screen active, same round-trip | byte-identical (15,318B) |
| `?1049l` after restore vs. original | byte-identical |
| unfinished mid-OSC input, resumed after restore | byte-identical |

The third row is what the carried patch was written for, and upstream gets
it without the hand-ordered reconstruction. The fourth is something the VT
dump cannot express at all: continuation tracking is opt-in via
`GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES`, and with it a snapshot taken
while the parser sits mid-sequence resumes exactly.

**Discard `ghostty-vt-native.patch`.** Not adapt it.

Format version 1 carries no binary-compatibility guarantee yet. That is not
a risk for us: the hub already parks a remote leg on any `SourceFingerprint`
difference, so both ends of the wire are always the same build.

## Receipts

Same corpus (2.18 MiB of agent-shaped output — SGR runs, box drawing, wide
CJK, ZWJ emoji, ASCII bulk), same 120×40 grid, driven through
`ghostty_terminal_vt_write` and `ghostty_terminal_resize`, which the compat
adapter forwards unchanged. Median of 9 measured runs after 5 warmups.

| | IO | reflow |
|---|---|---|
| ours, `ab0b9da`, ReleaseSmall | 35.0 MB/s | 1,090 resizes/s |
| upstream, `d760ee9`, ReleaseSmall | 147.7 MB/s | 5,230 resizes/s |
| upstream, `d760ee9`, ReleaseFast | 154.1 MB/s | 6,694 resizes/s |

At equal optimization level that is **4.2x IO and 4.8x reflow**.

The render read is the other half, and it does not move the same way. Today
one adapter call fills the whole viewport with resolved cells; upstream has
no such call, so a viewport read is one call per row plus per-cell decode.
Measured on a full-viewport repaint, each harness in its own process,
asserting both produce identical viewport text:

| | full viewport read |
|---|---|
| ours (1 bulk call + JS decode) | 203 µs |
| upstream small, naive per-cell | 317 µs |
| upstream fast, naive per-cell | 239 µs |

Roughly parity, with the naive binding losing ~18%. That is before caching
styles by `GHOSTTY_CELL_DATA_STYLE_ID`, before skipping unstyled rows via
`GHOSTTY_ROW_DATA_STYLED`, and before exploiting per-row dirty state — a
typical frame repaints a handful of rows, not forty.

Two traps worth recording, because both produced numbers off by 5–10x
before they were found. Allocating a `DataView` inside the cell loop costs
more than every wasm call in that loop. And three wasm instances plus a
shared cell-pool call site in one process go megamorphic: the same
measurement moved 9x between runs until each harness got its own process.

## The shape of the work

**Replace the dependency, not the call sites.** App code reaches
`ghostty-web` from nine non-test files for about thirty methods, and attn
already owns its renderer. A binding module under `app/src/ghostty/`
implementing that surface over the current C API keeps the first change to
one seam.

1. **Binding, prebuilt wasm, and the pin bump.** New binding module;
   `wasm.ts` loads the prebuilt binary; drop the compat adapter, its patch,
   and the wasm source build. The pin moves to `d760ee96` here rather than
   in step 2: upstream publishes a module only for the commit `tip` was
   last built from, so consuming the prebuilt binary IS the pin bump.
   Native prebuilts are republished for the new pin in the same change, so
   both runtimes land on one commit together. Existing model tests
   (`kittyWireRewrite.parity`, `ghosttyHyperlinks`, `kittyPlacements.store`,
   `ghosttyModelOpRing.replay`, `ghosttyVtWasm.resizeHang`) are the gate.

   Narrowed while implementing: `ghostty-web` stays a dependency for its
   key encoder alone, which `InputHandler` drives. Replacing the terminal
   model and the DOM input bridge in one change puts typing at risk for no
   gain; the encoder's exports all survive in the prebuilt module.
2. **Native patch removal.** Delete `ghostty-vt-native.patch` in favour of
   upstream's snapshot API, and republish native prebuilts under the new
   key. Verified at step 1's pin that the patch still applies, so this is a
   choice rather than forced work.
3. **Snapshot restore.** Replace `SerializeVT` with `ghostty_snapshot_encode`
   in `internal/ghosttyvt`, carry binary snapshot bytes in
   `attach_result.snapshot` (protocol bump), decode in the browser. The
   corrective `CUP` append goes away with it.
4. **Fast-path the render read**, using dirty rows and style-id caching.

Later, and deliberately not now: upstream exposes per-cell OSC 133 semantic
content and kitty graphics in the wasm build. Both are worker-authoritative
in attn by design. Revisit only with a reason.

### Sourcing the prebuilt binary

The `tip` release is rebuilt on every commit to main, so its assets cannot
be pinned by hash. Mirror the wasm the way native prebuilts already work:
download the upstream asset once, verify, republish to our own rolling
release keyed by the pin, and have the build download-and-verify against a
lock. `scripts/lib/libghostty-vt.sh` already implements exactly this shape
for the native archives.

Take the ReleaseFast binary. It loads from the bundle, and the whole point
is the speed.

## Verification

The apparatus for this already exists and is the right gate: the kitty
wire-rewrite parity corpus, the two production replay captures from
2026-08-05, and the resize-hang repro. The corpus is the pin tripwire — a
native/wasm behavioural skew is what it was built to catch.

It caught one at step 1, in the direction it was built for. Two of ghostty's
native kitty behaviours moved on this pin: a placement's scroll no longer
tracks the row count `r=` claims and stays inside the screen, and
retransmitting an image under a live placement id retires the placement
rather than re-pointing it. Both are improvements at the wire — the
SU-clamp case now carries its scroll instead of falling back to a snapshot
re-push — and worker/wire agreement held on all 164 shapes probed.

`kittyResyncScrollClamped` became unreachable as a result and was KEPT. Its
premise is a property of `SU` — ghostty clamps it to the scroll region, so
a larger scroll would be silently truncated and leave the client's history
permanently short — not a property of kitty. Upstream closed one route to
the condition; the condition still exists. The receipt on the constant
records the shapes probed against it.
