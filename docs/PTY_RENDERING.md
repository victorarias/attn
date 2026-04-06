# PTY Geometry, Attach Replay, and Terminal Rendering

Status: Active guidance
Applies to: `internal/daemon/web`, `internal/daemon/ws_pty.go`, `internal/pty/`, app terminal work, and any future browser/mobile terminal clients

## Why This Exists

Terminal rendering bugs in this repo have repeatedly come from mixing two different models:

1. **Authoritative PTY geometry**
2. **Convenience attach replay**

Those are not interchangeable.

The PTY has exactly one real geometry at a time. Replayed scrollback or a replayed visible-frame snapshot can help a client show something quickly, but replay is not the same thing as asking the running process to rerender for a new screen size.

This note records the rules we want to preserve so we do not relearn them through regressions.

## Core Model

- A PTY has one global `cols/rows` at a time.
- The most recently active interactive client owns PTY geometry.
- For the embedded mobile web client, **opening a session is the ownership claim**. Keyboard open is not the ownership event.
- `pty_resize` is the geometry authority mechanism.
- `attach_session` is a subscription plus optional replay mechanism.
- `attach_session` replay is **provisional context**, not an authoritative redraw primitive.

## Non-Negotiable Rules

1. Do not use `attach_session` as a generic redraw repair tool.
2. Do not use keyboard open/close as a reason to replay attach payloads.
3. Do not treat a locally resized terminal as authoritative until the PTY has been resized and the process has had a chance to rerender.
4. Do not assume replayed scrollback or a replayed screen snapshot is valid at the client's current geometry.
5. Do not assume desktop mobile emulation proves real-phone keyboard and viewport behavior.

## Why Replay Is Dangerous

The daemon stores PTY output history as raw bytes and may also provide a visible-frame snapshot:

- `scrollback` is accumulated PTY output, not a screen model.
- `screen_snapshot` is a captured visible frame, tied to the geometry where it was observed.

Consequences:

- Replaying scrollback produced during a narrow or transient geometry will reproduce that narrow wrapping later.
- Replaying a visible-frame snapshot captured before the final viewport settles can omit the bottom rows for the eventual client size.
- Reattaching during keyboard animation or viewport churn can replay unstable content and make the terminal look squeezed, truncated, or stale.

## Preferred Lifecycle

### Session Open

1. Client computes the best current geometry it can display.
2. If this client is taking ownership, it sends `pty_resize`.
3. The daemon applies that geometry to the PTY.
4. The running process rerenders.
5. The client renders live PTY output for that geometry.
6. Attach replay may be shown only as a temporary bridge while waiting for authoritative output, and only if its geometry is known to be compatible.

### Keyboard Open / Close

1. Treat keyboard open/close as a **viewport event**, not an attach/replay event.
2. Wait for the viewport to settle enough to compute meaningful geometry.
3. Decide whether PTY geometry actually changed.
4. If geometry changed, send `pty_resize`.
5. Prefer live post-resize PTY output as the authoritative redraw path.
6. If no PTY geometry change happened, prefer a local redraw path that does not reattach/replay old daemon payloads.

## Mobile Viewport Guidance

- Mobile viewport and keyboard transitions are asynchronous.
- `visualViewport` changes can arrive in bursts.
- Pixel height changes are not automatically proof that rows changed meaningfully.
- Temporary keyboard geometries can produce transient PTY redraws that should not be treated as a stable steady state.
- Local `fitTerminal()` is a display calculation, not proof that the remote PTY is now correct.

## Instrumentation Is Part Of The Model

For PTY and terminal rendering work in this repo, structured instrumentation is not optional polish. It is part of the debugging model.

Why:

- real-phone keyboard and viewport behavior cannot be reproduced reliably from desktop emulation alone
- the interesting failures often happen between browser focus, viewport, layout commit, terminal fit, and PTY resize
- a screenshot or a verbal report is usually not enough to tell which layer was wrong

### Current Embedded Web Mechanism

The embedded web client can post structured JSON snapshots to the daemon:

- endpoint: `POST /web-instrumentation`
- daemon log prefix: `web instrumentation:`
- handler: `internal/daemon/web_instrumentation.go`

The current payload captures the cross-layer state we actually needed in practice:

- keyboard mode / transition state
- committed app viewport
- live `visualViewport` width, height, offsets, and scale
- `window.innerWidth/innerHeight` and scroll position
- active element
- mobile input geometry and styling
- terminal geometry and attach state
- last resize and last viewport-settle markers

This exists because viewport and focus bugs were otherwise easy to misdiagnose.

### Standing Rule

Do not remove this instrumentation just because a bug seems fixed.

Treat it like other purpose-built observability:

- extend it when a new PTY or viewport bug needs more evidence
- keep payloads compact and structured
- prefer daemon-side logging over browser-console-only evidence for real-device issues
- use it to prove or disprove a theory before changing terminal behavior

### What To Instrument

For PTY/mobile terminal bugs, instrument transitions, not just steady state:

- attach start / attach result
- ownership claim / `pty_resize`
- keyboard open request
- keyboard close request
- native blur / native close detection
- viewport signal bursts
- viewport settled
- any timeout or fallback path

### Main App Direction

The same idea should exist for the main Tauri app.

The transport does not need to match the web client exactly, but the schema and intent should:

- collect the same cross-layer state
- correlate terminal geometry, PTY messages, focus, and viewport/layout state
- write it somewhere durable enough to inspect after the fact

Recommended direction:

- reuse the same logical event names and payload shape where practical
- route main-app instrumentation into daemon logs or another persistent app log, not only DevTools console output
- treat web and main-app instrumentation as two frontends for the same diagnostic model

## Anti-Patterns

Do not do these:

- Locally call `fitTerminal()` and then replay attach payloads as if they now match.
- Reattach after keyboard open just to "make something visible again."
- Reuse `attach_session` as a post-resize repaint primitive.
- Replay scrollback generated under a different resize epoch and expect it not to look compressed.
- Let an attach snapshot captured at one geometry stand in for a stable render at another geometry.
- Accept a narrow automated test that proves "something became visible" as proof that the full mobile lifecycle is correct.

## Verification Expectations

Every significant terminal/mobile rendering change should be checked against these behaviors:

- Open session: bottom prompt/input area is visible immediately.
- Open session: scrolling works before keyboard interaction.
- Open keyboard: prompt remains visible after viewport change.
- Type immediately after keyboard open: typed input visibly echoes.
- Close keyboard: text is not squeezed or rewrapped incorrectly.
- Refresh and reopen: behavior remains stable, not just self-healing after a second attach.
- Multi-client attach: the most recently active client owns geometry and passive viewers tolerate the winner's size.

Automate what we can, but use a real phone for:

- trusted keyboard focus behavior
- `visualViewport` timing
- touch-scroll vs keyboard interactions
- Safari/Chrome mobile repaint quirks

When real-device behavior disagrees with desktop emulation, prefer adding or extending instrumentation before trying another behavioral fix.

## Protocol Implications

The existing protocol already exposes some useful geometry:

- `attach_result.cols` / `attach_result.rows` for PTY size
- `attach_result.screen_cols` / `attach_result.screen_rows` for screen snapshot size

That is not enough to decide whether replay is safe.

### What We Actually Need To Know

The important question is not only:

- "What geometry is this client asking for?"

It is also:

- "What geometry produced this replay payload?"

### Recommended Direction

If we evolve the protocol, prefer something like:

- `attach_session` may optionally include requested `cols/rows` and ownership intent.
- `attach_result` should indicate the geometry or resize epoch associated with replayable payloads.
- The client should know whether replay is authoritative, provisional, or incompatible with its requested geometry.

Useful fields could include:

- requested geometry on attach
- replay geometry for scrollback/snapshot
- replay compatibility flags
- a resize generation / geometry epoch identifier

### Better Than Geometry Alone

Geometry tags help, but they only tell us whether replay is probably compatible.

A stronger design is a **resize epoch** model:

- PTY resize increments a geometry epoch.
- Replay payloads are tagged with the epoch that produced them.
- Clients avoid treating replay from a different epoch as authoritative for the current screen.

That would let the client make a clear choice:

- use replay because it matches current geometry/epoch
- or ignore replay and wait for live post-resize output

## Working Rule For Future Changes

If a proposed fix solves a terminal rendering problem by "reattaching" or "replaying again," stop and prove why replay is valid for the target geometry before implementing it.

If you cannot prove that, it is probably the wrong fix.
