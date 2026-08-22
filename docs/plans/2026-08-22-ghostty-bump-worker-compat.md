# Plan: bump ghostty, and tell sessions that predate the bump

## Goal

Move `ghostty-vt.pin` to upstream `main` on every target, native archives and
the browser module. Ship the compatibility handling in the same PR, so a
session that was open when the user updated says what happened rather than
quietly going wrong.

The compatibility half is keyed on "the terminal engine moved", so it covers
any future dep bump. Kitty is just what exposed it. The in-place worker
upgrade that makes bumps invisible is a follow-up in the same release (see
Follow-ups).

## Why this needs handling at all

A pty-worker outlives an install. The worker and the app each hold their own
libghostty-vt, and everything attn shows depends on the two agreeing. After a
pin bump they disagree.

```text
worker process (old ghostty)              app (new ghostty wasm)
  parses the PTY stream        ── bytes ──►  parses the same stream
  strips a kitty APC                         (never sees the APC)
  synthesizes SU/CUU/CUF  ───────────────►   replays them into ITS grid
                                             ↑ if the two ghosttys differ
                                               here, the pane is visibly wrong
```

Four things cross between them, and a bump skews all four. Only the first is
guarded today:

| what crosses | guarded? |
| --- | --- |
| snapshot bytes (worker encodes, app decodes) | yes, format tag |
| synthesized layout bytes replacing a kitty APC | no |
| grid-derived structure (placements, OSC 133 block rows) | no |
| plain VT bytes, parsed independently by both | no |

Two receipts that this reaches the screen: the 2026-08-16 skew incident, and
`changelog.d/cranky-platypus-kitty-disable.yaml`, which describes a placement
moving the cursor on one grid and not the other, and the pane coming back
corrupted.

**The format tag made drift worse for the unguarded rows.** A resync used to
force a snapshot re-push. The tag now rejects that re-push, so a drifted pane
has no self-heal and stays wrong until the session is reloaded.

## Decision: notice, not repair

On a build mismatch the session keeps running and keeps streaming text. The app
tells the user to reload the session, explains it is a one-off from a ghostty
upgrade, and says future upgrades handle themselves.

Rejected: dropping images and block markers for mismatched sessions. It is a
lot of machinery for a narrow case, and the notice already gives the user the
fix. Accepted cost, on the record: someone who ignores the notice and then runs
an image tool (`icat`, `chafa`, `timg`, matplotlib) in that session can still
get a corrupted pane.

Rejected: relaxing the tag so old snapshots decode. Upstream states outright
that snapshot format version 1 "does not yet carry a binary-compatibility
guarantee" (`include/ghostty/vt/snapshot.h`), so the tag stays fail-closed.

## The trigger is the ghostty locks, never the attn version

`scripts/snapshot-format.sh` derives the tag from `ghostty-vt-native.lock` +
`ghostty-vt-wasm.lock` + a salt. An ordinary attn release, daemon changes
included, leaves it byte-identical, so no running session is disturbed. It
moves only when we move ghostty. That is what keeps the notice rare, and why
the tag is the trigger rather than `WorkerVersion`.

## Architecture map

```diff
 worker connects to daemon
   ptyworker hello
     HelloResult{ worker_version, rpc_major, rpc_minor, session_id }
+                 snapshot_format          ← additive; absent on old workers
   daemon.registerWorker
+    compare against buildinfo.SnapshotFormat
+    mark the session stale, publish session.updated
   wireProjections → sessions snapshot re-push
     app renders the session
+      TerminalStaleBuildNotice on the pane
```

Absent means stale. Every worker alive at this bump omits the field, and those
are exactly the sessions the notice is for.

## Data model

```ts
// internal/ptyworker/protocol.go, worker to daemon, additive
type HelloResult = {
  // ... existing
  snapshot_format?: string  // buildinfo.SnapshotFormat; absent on old workers
}

// internal/protocol/schema/main.tsp, Session, daemon to app
terminal_build_stale?: boolean
// True when this session's pty-worker was built against a different
// libghostty-vt than this daemon. Judged at broadcast from the worker's
// reported format tag; never stored. Absent for every same-build session,
// which is all of them outside an upgrade window.
```

Derived at broadcast, not stored, matching `turn_owed` and `seed_id`.

## Boundaries

- `internal/ptyworker` reports its own tag and knows nothing about the daemon's.
- `internal/daemon` owns the comparison and the verdict. One place.
- The app renders the verdict. It does not re-derive it, and it does not
  compare tags itself for this purpose.
- `classifyAttachRestore` keeps its own tag check for the snapshot decision.
  Two consumers of one tag, different questions; do not merge them.
- The comparison is worker against **its own daemon**, which is the right one
  for a hub too. The app replaying the bytes is the hub's, not the remote's,
  but a remote leg parks on any `SourceFingerprint` difference, so hub and
  remote daemon are always the same build and the comparison carries across.
- No CLI surface. The only session listing outside the app is `attn agent
  list`, an address book for messaging agents; a garbled pane is not something
  it can act on.

## The notice

The notice sits on the session pane. Copy, roughly:

> This session started before the last update and is running an older terminal.
> Reload it to bring it up to date.
> This is a one-off from a terminal-engine upgrade. Future updates will handle
> it automatically.

Points at the existing action: sidebar `…` → **Reload session**
(`SessionActionsPopover.tsx:94`, `data-testid="reload-session-action"`). Ships
with a small image showing where that is, in `app/src/assets/`.

Dismissible, and it clears on its own when the session is reloaded, since the
new worker reports a matching tag.

## Implementation steps

- [x] `HelloResult.snapshot_format`, populated from `buildinfo.SnapshotFormat`
- [x] Daemon compares on worker registration, derives `terminal_build_stale`
- [x] Protocol: `main.tsp` → `make generate-types` → `ProtocolVersion` 269 in
      `internal/protocol/constants.go` **and** `useDaemonSocket.ts`
- [x] `TerminalStaleBuildNotice` on the pane, dismissible, with the image
- [x] Bump `ghostty-vt.pin` to `da5ddcb` (upstream tip, 2026-08-22)
- [x] `make publish-ghostty-vt-wasm` and `make publish-native-vt`; both
      regenerated locks committed
- [x] Migrate the wasm ABI break (see below)
- [x] Re-measure the kitty parity corpus at the new pin, and re-take the
      tripwire receipts that cite a pin (see Decisions)
- [ ] Live verification: sessions open across an install of the branch, confirm
      the notice, confirm no `ghostty_model_fault`, confirm reload clears it
- [x] Changelog fragment

Keep the bump as its own commit so it can be reverted without losing the
compatibility work.

## What the bump actually moved

More than a pin edit. Recorded because the next bump will look like this too.

**The wasm ABI broke.** The typed allocator family
(`ghostty_wasm_alloc_u8_array` and friends) collapsed into
`ghostty_wasm_alloc`/`ghostty_wasm_free`, and
`ghostty_render_state_colors_get` folded into
`ghostty_render_state_get(state, GHOSTTY_RENDER_STATE_DATA_COLORS, out)`.
Contained to `app/src/ghostty/{abi,terminal}.ts` plus two repro scripts.

**Kitty scroll behavior moved.** A placement now scrolls one row where it
scrolled two. The parity corpus regenerated and the wasm side still agrees, so
both runtimes moved together. Two hand-written tripwires needed their receipts
re-taken; one of them, `kittyResyncPendingWrap`, no longer demonstrates its
divergence at all (see below).

**`abi.layout.test.ts` did not exist.** `abi.ts` claimed it asserted the struct
offsets at runtime. It now does, against the module's own `ghostty_type_json`,
and it found `COLORS_SIZE` was 782, the struct's content, where the ABI pads it
to 784. Pre-existing and harmless, since nothing reads the padded tail.

**All kitty accessors are gone from the wasm.** Expected: the client never
parses kitty, and ghostty hard-disables it on wasm. The old module exported the
accessors inertly; the new one does not export them at all. Nothing called them.

## Decisions

- **Tag, not `WorkerVersion`.** `WorkerVersion` differs on every attn release,
  so it would show the notice on all of them. The tag moves only with ghostty.
- **Absent tag reads as stale.** Same rule the snapshot path already uses, and
  it is what makes the notice reach the workers running today.
- **Re-measure the corpus, do not only re-run it.** Several tripwires
  cite measurements at the current pin. `kittyResyncScrollClamped` says
  outright that "a pin bump restoring proportional scrolling would ship it",
  and its 164-shape probe was taken at `d760ee9`.
- **`kittyResyncPendingWrap` stays, and the cost is now on the record.** At
  `d760ee9` a placement on a pending wrap left the two cursors on different
  rows. Re-probed at `da5ddcb` over 336 shapes: the placement's own cursor move
  describes the wrap and every one agrees. But the guard fires on the column,
  not on a measured divergence, so it resynced on 336 of 336 — it is free only
  because no emitter in the A4 sweep leaves the cursor there. Kept, because
  nothing exposes the pending-wrap bit and 336 agreeing shapes are not a proof
  the wire describes it in general. Dropping it is a decision about trusting the
  description; it does not belong inside a bump.
- **Animation is not wired up.** Upstream ships kitty animation storage but
  exports no `animationTick` in the C API, so animated images sit on their
  current frame. Do not advertise animation support after this bump.

## Which commit to pin

Not a ghostty release. Checked, and it does not work:

- The latest tag, `v1.3.1`, is from 2026-03-13. Our pin is 2026-08-14, five
  months newer, so a release pin is a large downgrade that drops the snapshot
  API the restore path is built on.
- No tag publishes assets. `gh release list` returns exactly one release,
  `tip`, and `ghostty-vt.wasm` exists only there. Pinning to a tag means
  building the browser module ourselves again, which
  `docs/plans/2026-08-15-first-party-libghostty-vt.md` removed on purpose.

So the pin is whatever `tip` was last built from at the moment we mirror.
`scripts/publish-ghostty-vt-wasm.sh` already reads tip's target commit and
refuses when the pin disagrees, so the procedure is: read tip's commit, write
it to the pin, publish both halves in that sitting.

The consequence, stated plainly because it is permanent: attn pins a ghostty
nightly, never a release. That is inherent to mirroring `tip`, whose assets are
overwritten on every commit to main. It also means the bump commit should land
right after the mirror, not days later.

## Live verification

Run on a throwaway `plumtack` profile on 2026-08-22. A session was spawned on
the pre-bump build, the branch was installed over the same profile, and the
pre-existing worker kept running across the install.

The daemon's own attach log is the receipt — the two builds report different
format tags, which is the trigger the whole design rests on:

```
20:53:48  9688d6e8  same_app_remount   snapshot_format=199ae0429d61   (pre-bump worker)
20:45:40  aec100cd  same_app_remount   snapshot_format=7768a6bd7179   (post-bump worker)
21:12:07  8970d3ed  relaunch_restore   snapshot_format=7768a6bd7179   (after Reload session)
```

What it showed:

- the pre-bump session reported `terminal_build_stale: true` and rendered the
  notice; a session spawned after the install omitted the field and rendered
  nothing;
- the old worker (pid 57008) survived the install and was replaced only by the
  reload;
- Reload session cleared the flag, removed the notice from the DOM, and the
  pane came back with its full transcript;
- `grep -c model_fault ~/.attn-plumtack/daemon.log` is `0` — a refused snapshot
  never reaches model-fault recovery.

Recording: [clip.gif](https://raw.githubusercontent.com/victorarias/attn-pr-evidence/0874c6a3b457e0903039d2191c0f5da3d09e4d9a/whimsy-lemur/clip.gif)
([mp4](https://raw.githubusercontent.com/victorarias/attn-pr-evidence/0874c6a3b457e0903039d2191c0f5da3d09e4d9a/whimsy-lemur/clip.mp4)).

## Follow-ups

- **In-place worker upgrade.** A worker swaps its own binary while keeping the
  PTY handle and the agent child, dumping its screen as replayable VT
  (format-independent, any ghostty reads it) and replaying it into the new
  model. That closes all four rows of the table above, plain-VT drift
  included, and makes every later bump invisible. **Must land in the same
  release as this PR.** The notice promises "future updates handle it
  automatically", and that is only true if the release carrying the notice
  also carries the swap.
- **RIS wipes the kitty storage limit.** `ESC c` resets `total_limit` to
  ghostty's 320MB default (`Screen.reset`, fixed upstream in `bcbc93a6b`), so
  `ATTN_KITTY_STORAGE_LIMIT=0` stops holding, with nothing said. Verified
  against the pinned library: after a RIS the support query answers `OK` and a
  transmission places an image. Independent bug, not a gate on this PR. The
  upstream fix arrives with the bump. Decide separately whether to also
  re-assert on RIS the way `terminalGraphemeMode.ts` re-asserts mode 2027.
