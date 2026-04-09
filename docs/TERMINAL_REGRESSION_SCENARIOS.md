# Terminal Regression Scenarios

Status: Active scenario matrix for packaged-app PTY/UI work

This document is the regression surface for terminal, PTY, split-layout, and relaunch work in `attn`.

The goal is to stop treating terminal bugs as isolated one-offs. Each fix should map back to one or more scenarios here, and each new packaged-app harness automation should claim a specific scenario ID.

## Common Expectations

Every scenario should validate some combination of these invariants:

- the intended pane exists in the workspace model
- the intended pane has non-zero visible bounds in the packaged app
- the terminal inside that pane is `ready` and `visible`
- the pane shows meaningful rendered content, not just a prompt/footer/status strip
- for agent panes specifically, the visible content check must prove the agent output is actually rendered
- focus and typing reach the intended runtime without an extra click
- PTY geometry matches the pane that currently owns it
- replay does not leave the pane visually collapsed, monochrome, or stale
- resize/redraw activity stays targeted to the pane that changed
- render health shows no severe underfill or projected-layout mismatch
- rendered content uses the full visible pane area rather than collapsing into a small subset
- closing a session or app tears down the remote PTY/worker side instead of leaking runtimes that poison later runs

## Cross-Cutting Rules

- Split preservation rule:
  For any split scenario, both the source pane and the newly created pane must retain meaningful visible content after the split. Passing because only one pane looks healthy is not acceptable.

- Relaunch preservation rule:
  For any relaunch scenario, the same content-preservation requirement applies both before and after relaunch. A restored layout only passes if pre-existing panes still show meaningful content and new post-relaunch splits do too.

- Agent rendering rule:
  We should not accept a scenario as passing based only on workspace geometry, terminal readiness, or scrollback text. For agent panes, the harness must eventually prove that the agent's visible output is actually rendered on screen.

- Full-area rendering rule:
  Content should be rendered across the pane's usable visible area, not compressed into a footer band, narrow strip, or otherwise misleading subset of the pane.

## Scenario List

### Startup And First Paint

- `TR-001 Fresh Session First Paint`
  Open a new local session and verify the main pane renders at meaningful size on first paint, not a tiny bootstrap geometry.
  Assertions: main pane visible, terminal ready, no suspicious cols/rows, no severe render-health errors.
  Automation: planned

- `TR-002 Fresh Session Input Ready`
  Open a new local session and verify the first visible pane accepts typing immediately.
  Assertions: helper textarea focused for the active pane, typed text echoes without extra click.
  Automation: covered today by existing smoke flows

- `TR-003 Fresh Remote Session First Paint`
  Open a new remote session and verify the first paint geometry is authoritative, not stale attach replay.
  Assertions: main pane visible, post-attach geometry matches pane ownership, no tiny replay box.
  Automation: planned

### Split Lifecycle

- `TR-101 Split From Main Preserves Main Pane`
  Split from the main agent pane and verify the main pane remains rendered, visible, and writable after the workspace topology changes.
  Assertions: main pane still visible, new split pane visible, both panes retain meaningful visible content, render health sane for both panes.
  Automation: initial local Claude automation implemented via `real-app:scenario-tr101`, including visible-row width checks and pane-cropped native pixel assertions for the main pane

- `TR-102 Split From Utility Preserves Existing Utility Pane`
  Split from an existing utility terminal and verify both the older utility pane and the new utility pane remain writable.
  Assertions: old utility pane still receives input after focus return, new utility pane receives input immediately, and both panes retain meaningful visible content after the split.
  Automation: initial local Claude automation implemented via `real-app:scenario-tr102`; older shell-only repro still exists in `real-app:bridge-repro-shells`

- `TR-103 Split Layout Matches Workspace Model`
  Split one pane multiple times and verify rendered widths track the workspace model instead of decaying into tiny columns.
  Assertions: pane bounds match projected layout within tolerance, no severe projected-layout mismatch.
  Automation: covered by `real-app:bridge-remote-split-geometry`

- `TR-104 Split Does Not Disappear`
  Create a new split and verify the newly created pane is still present after focus churn and a short settle period.
  Assertions: workspace pane count increments, new pane has non-zero bounds, terminal ready/visible.
  Automation: planned

### Relaunch And Restore

- `TR-201 Relaunch Preserves Existing Split Session`
  Close and reopen the app while a session already has split panes, then revisit that session.
  Assertions: pre-existing panes remain visible, retain meaningful visible content, no pane restores into a tiny square, and render health stays sane after relaunch.
  Automation: planned

- `TR-202 Split From Main After Relaunch`
  Relaunch the app, return to an already-split session, then split from the main pane.
  Assertions: the source pane still has meaningful visible content, the new split pane appears, stays visible, accepts typing, and also renders meaningful content.
  Automation: covered by the new remote relaunch scenario below

- `TR-203 Split From Utility After Relaunch`
  Relaunch the app, return to an already-split session, then split from an existing utility pane.
  Assertions: the source pane still has meaningful visible content, the new split pane appears, stays visible, accepts typing, and also renders meaningful content.
  Automation: covered by the new remote relaunch scenario below

- `TR-204 Relaunch Restore Keeps Formatting`
  Relaunch the app and verify formatted output restores through daemon replay without flattening ANSI state into monochrome text.
  Assertions: replayed pane keeps color/styling, not just plain text.
  Automation: planned

- `TR-205 Close Splits After Relaunch Reuses Reclaimed Space`
  Start a session, create an initial split, relaunch the app, add more splits, then close the utility panes one by one.
  Assertions: after each close, the surviving main pane regains width and repaints meaningful visible content into the reclaimed space instead of keeping a stale narrow header/body layout.
  Automation: initial remote real-agent automation implemented via `real-app:scenario-tr205`

### Focus And Session Switching

- `TR-301 Utility Focus Survives Session Switch`
  With a utility pane active, switch to another session and back.
  Assertions: focus returns to the utility pane, typing lands in the utility runtime without extra click.
  Automation: planned

- `TR-302 Focus Survives Split Churn`
  Create and close splits while actively typing.
  Assertions: the intended active pane keeps focus ownership, helper textarea is not stolen by another pane.
  Automation: planned

### Resize And Redraw

- `TR-401 Window Resize Preserves Render Health`
  Resize the app window while a split session is visible.
  Assertions: affected panes redraw to meaningful geometry, content still uses the full visible area, no severe underfill, and no stale replay box.
  Automation: planned

- `TR-402 Split Open Or Close Triggers Targeted Redraw`
  Open or close a split and verify only panes whose container geometry changed bounce/redraw.
  Assertions: targeted PTY resize/redraw activity, unrelated panes do not reset, and when a split closes the surviving pane re-expands and repaints meaningful visible content instead of keeping a stale narrow header/body layout.
  Automation: initial remote real-agent close-path automation implemented via `real-app:scenario-tr402`

### Remote-Specific

- `TR-501 Remote Split Input Latency`
  Split a remote session and type into the utility pane immediately.
  Assertions: focus ready, typed echo, and command output stay within threshold.
  Automation: covered by `real-app:bridge-remote-split-input`

- `TR-502 Remote Relaunch Split Persistence`
  Create a remote split session, relaunch the packaged app, then split again from both the main pane and an existing utility pane.
  Assertions: pre-existing panes survive relaunch with meaningful visible content, new post-relaunch splits from both sources appear, accept typing, preserve content in both source and target panes, and shell echo after each split stays within the configured tolerance.
  Automation: initial remote real-agent automation implemented via `real-app:scenario-tr502`, including pre/post native paint stability checks for restored panes, a default shell echo threshold of `2500ms`, and pane/runtime trace artifacts when the delay regresses; dedicated visible agent-content assertions are still partial

- `TR-503 Remote Agent Pane Remains Visible After Split`
  Split a remote agent session and verify the agent pane still shows meaningful visible content rather than collapsing to an effectively empty bottom strip.
  Assertions: main pane visible content remains meaningful after split, not just status/prompt residue, and the content occupies the visible pane area rather than a small subset.
  Automation: planned

- `TR-504 Remote Session Cleanup Does Not Leak Workers`
  Create remote sessions and utility splits, then close the session or quit the app and verify the remote PTY/worker side is actually torn down.
  Assertions: closing the session or app does not leave orphaned remote shells, Codex/Claude workers, or stale PTY runtimes behind; repeated scenario runs do not degrade into `resource temporarily unavailable`, startup panics, or spawn failures caused by leaked processes from earlier runs.
  Automation: planned

### Render Efficiency

- `TR-601 Split Flow Transport Budget`
  Measure PTY decode, JSON parse, and write pipeline activity during split creation.
  Assertions: transport stays within an expected budget and does not explode during steady-state split usage.
  Automation: covered by `real-app:bridge-perf` and `real-app:bridge-pty-bench`

- `TR-602 Render Activity Health`
  During split open, relaunch restore, and resize, verify the active terminals keep rendering instead of stalling on stale buffers.
  Assertions: render count and write-parsed counters advance, recent PTY traffic is attributable, no long silent stalls for active panes.
  Automation: planned

## Immediate Automation Priority

The next scenarios to automate after `TR-502` should be:

1. `TR-101 Split From Main Preserves Main Pane`
2. `TR-201 Relaunch Preserves Existing Split Session`
3. `TR-503 Remote Agent Pane Remains Visible After Split`
4. `TR-504 Remote Session Cleanup Does Not Leak Workers`
5. `TR-204 Relaunch Restore Keeps Formatting`

## Review Rule

When a regression is reported, add or update the matching scenario here before changing PTY or terminal behavior. The harness script, artifact bundle, and fix should all point back to the same scenario ID.
