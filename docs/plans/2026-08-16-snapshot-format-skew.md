# A snapshot that outlives the build that wrote it

Written 2026-08-16, after
[2026-08-16-snapshot-restore.md](2026-08-16-snapshot-restore.md) shipped and
broke every session that was open when it was installed.

## What happened

attn was installed over a running production app at 00:26. The two sessions
whose pty-workers had been spawned at 00:16 and 00:22 kept running — that is
the point of the worker being its own process — and opening either one
produced a red banner and a hot loop:

```
PTY attach result: policy=same_app_remount ghostty_snapshot_bytes=17386
                   replay_decision=use_ghostty_snapshot
→ ghostty_model_fault operation=restoreSnapshot
                      error="ghostty_snapshot_decoder_ready failed"
```

421 faults in six seconds, 421 attach round-trips behind them, each writing a
DOM snapshot into `ui-diagnostics.jsonl`. Sessions spawned after the install
were fine.

## Why

`AttachResult.GhosttySnapshot` is the same worker-RPC field it has always
been. What changed in #923 is what the bytes *mean*: the carried patch's VT
dump became ghostty's snapshot record stream. The field did not move, the
worker RPC minor did not move, and `IsCompatibleVersion` reports major 1 /
minor 1 on both sides — so a pre-install worker answers `attach` with a VT
dump, the new daemon labels it `use_ghostty_snapshot`, and the new wasm
decoder is handed bytes in a format it has never seen.

The loop is a second defect standing behind the first. `restoreSnapshot`
routes any throw to `recoverFromModelFault`, which bumps `rendererEpoch` to
replace the wasm model — the correct response to a broken model, and the wrong
one here. The model is fine; the payload is wrong. The new epoch remounts the
pane, the pane reattaches, the daemon serves the identical bytes, and it
throws again.

Neither is a one-off. The worker surviving an upgrade is a feature, and every
future release that moves the encoder does this again to every session a user
had open.

## The rule

**A snapshot names the format it was written in, and a decoder that does not
speak that format does not decode it.** Absence of a name is a format nobody
speaks — which is what makes the fix reach the workers already running out
there, this incident's included.

## Design

### The tag is derived, never typed

The identity is a 12-hex tag computed by `scripts/snapshot-format.sh` from the
files that decide the wire format:

- `ghostty-vt-native.lock` — the encoder's archive, keyed by pin.
- `ghostty-vt-wasm.lock` — the decoder's module, pinned by sha256.
- a salt constant in the script, for a change to attn's own encode/decode glue
  that moves neither artifact.

The pin alone would not have caught this incident: #923 changed the native
lock's `key=` (it dropped the carried patch) while `ghostty-vt.pin` stayed at
`d760ee96`. The locks are what move when the format moves.

The Makefile passes the tag to Go through `buildinfo.SnapshotFormat`, the same
ldflags path `SourceFingerprint` already uses; `vite.config.ts` runs the same
script and defines it for the frontend. One implementation, two consumers, so
a same-build worker and app always agree by construction — and a divergence
would show up as `scenario-snapshot-scrollback-restore` losing its scrollback.

Deriving from the locks rather than from `SourceFingerprint` is deliberate: a
tag that moved on every commit would cost every live session its scrollback on
every upgrade, for a format that did not change.

### The app decides, because the app owns the decoder

The tag rides with the payload — worker RPC → `AttachInfo` → the wire's
`AttachSnapshot.format` — and `classifyAttachRestore` treats a snapshot whose
format is not this build's as no snapshot at all.

That is one comparison, in the one place that owns the decoder, and it is
correct for a relayed remote attach without touching the hub: the chain
worker → remote daemon → local daemon → app can skew at any link, and only the
last link knows what it can read. Everything downstream of `hasSnapshot` is
already written for the snapshot-less case — no reset, the client's own
watermark as the dedup baseline, the live stream painting on — so the fallback
costs nothing new.

A daemon-side filter would additionally save shipping ~17 KB that the app
discards. Not worth a second comparison that can disagree with the first.

### A decode failure is not a model fault

`restoreSnapshot` catches a throw from `adoptSnapshot`, records it once, and
leaves the model alone. `adoptSnapshot` releases and throws before it swaps the
handle, on both failure paths, so the model it declines to replace is intact —
that ordering is what makes containment safe, and it is now load-bearing.

This is the net under any skew the tag fails to predict: a corrupt payload, a
truncated frame, a bug in the tag itself. The cost of a failure becomes one
missing restore rather than an unusable session.

The pane's `reset` is emitted before the restore, so a decode that fails after
the tag matched leaves the pane blank until the session next prints. That is
the residual, and it is bounded: the tag is what keeps the predictable skew
from reaching it.

## What the user gets

An upgrade with sessions open: those sessions keep running, attach without
their scrollback, and repopulate as the agent prints. Nothing faults, nothing
loops, no banner. Restarting a session restores the full behavior, because its
new worker is this build's.

No notice is shown. A session that comes back thin is quieter than a banner
about a format tag, and the condition ends by itself.

## Surfaces

- **Worker RPC** — `AttachResult.GhosttySnapshotFormat`, additive and
  skew-safe like the fields around it. Absent from an old worker; absent reads
  as incompatible.
- **Protocol** — `AttachSnapshot.format`, `ProtocolVersion` 252 → 253.
- **Backends** — the worker backend passes the tag through; the embedded
  backend is the daemon's own process and stamps `buildinfo.SnapshotFormat`.
- **Linux** — the tag is build plumbing, not cgo; the pure-Go stub emits no
  snapshot and reaches none of this.
- **CLI** — nothing. No command reads a snapshot.
- **Docs** — a line in AGENTS.md under the terminal contracts, and a changelog
  fragment.

## Verification

- `scenario-snapshot-scrollback-restore` still restores scrollback: the proof
  the two tag computations agree in a real bundle.
- A skewed attach degrades instead of faulting, driven by serving a snapshot
  under a foreign tag.
- Unit: `classifyAttachRestore` drops a foreign-tag and an absent-tag
  snapshot; `restoreSnapshot` on undecodable bytes records the rejection,
  raises no model fault, and leaves the model usable.
