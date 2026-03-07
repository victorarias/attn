# Review Loop UI Implementation Plan

Date: 2026-03-07
Status: In Progress
Owner: app/review-loop UX

## Implemented So Far

The following pieces are already built:

1. sidebar and dashboard use color-based review-loop state treatment instead of pills
2. review-loop controls are hidden for non-Claude sessions
3. a right-side drawer exists for review-loop details
4. the drawer shows:
- loop state
- current iteration state
- pass count
- latest summary
- files touched this round
- reviewer log
5. the drawer auto-opens when the selected session enters `awaiting_user`
6. a waiting-review switcher appears when multiple sessions are waiting for input

## Current Refinements Requested

The current implementation is functionally close but not yet in the desired final form.

Immediate changes requested:

1. remove the always-visible top trigger bar
2. replace it with a smaller button on the right side that opens the drawer
3. move the action buttons to the top of the drawer

This means the implementation below should now be read as:

1. backend and overview content are largely in place
2. the next work is visual and interaction refinement of the drawer trigger/header layout

## Chosen Direction

Implement Option 2 from the multi-session sketches:

1. the review panel stays hidden by default
2. it auto-opens when the selected session's loop needs input
3. if multiple sessions need input, the drawer gets a small waiting-review switcher
4. sidebar/session-row color remains the global discovery mechanism

This combines:

1. good global awareness
2. good local handling
3. minimal permanent terminal-space cost

## Core UX Rules

1. do not keep the review panel permanently visible
2. only show review-loop controls for Claude sessions
3. use color as the primary state signal, not sidebar pills
4. do not auto-switch the selected session when another review needs input
5. do auto-open the drawer when the selected session moves to `awaiting_user`
6. only show the waiting-review switcher when more than one session is currently `awaiting_user`

## What The User Should Always Be Able To See

The drawer should provide a complete overview of the review:

1. state of the current iteration
2. state of the overall loop
3. files changed
4. reviewer log

## Data Model Mapping

The backend already gives us most of the shape we need through `review_loop_run`.

### Loop state

Use:

1. `review_loop_run.status`
2. `iteration_count`
3. `iteration_limit`
4. `last_decision`
5. `last_result_summary`
6. `last_error`
7. `pending_interaction`

### Current iteration state

Use:

1. `review_loop_run.latest_iteration.status`
2. `review_loop_run.latest_iteration.iteration_number`
3. `review_loop_run.latest_iteration.summary`
4. `review_loop_run.latest_iteration.result_text`

If we later want richer live progress, we can add streamed iteration events, but this is enough for the first UI pass.

### Files changed

Yes, this is possible now.

Use:

1. `review_loop_run.latest_iteration.files_touched`

Important caveat:

1. this is "files the reviewer reports it touched in the latest iteration"
2. it is not yet a complete cross-iteration union
3. for v1, that is acceptable if labeled clearly

Recommended UI label:

1. `Files touched this round`

If later needed, we can aggregate all iteration file lists into:

1. `files_touched_union`

### Reviewer log

Use:

1. `review_loop_run.latest_iteration.assistant_trace_json`
2. `review_loop_run.latest_iteration.result_text`

This is enough for a first useful log viewer:

1. assistant prose
2. final result text

If we later want tool-by-tool streaming, we should add a dedicated structured log/event list to the iteration model.

## UI Structure

## 1. Passive Session Signal

Replace sidebar pills with stronger color treatments:

1. left color rail on session row
2. subtle session-row wash or edge glow
3. color mapping:
- `running` = orange
- `awaiting_user` = yellow
- `completed` = green
- `error` = red
- `stopped` = gray

The current selected session header should mirror the same color state.

## 2. Compact Trigger

Replace the current always-visible review-loop bar with a smaller trigger control anchored on the right side.

Suggested content:

1. `Loop Running`
2. `Needs Input`
3. `Done`
4. `Error`

Plus:

1. pass count, for example `1/2`
2. chevron to open/close drawer

Updated direction:

1. it should not read as a full-width top bar
2. it should feel like a right-edge affordance that summons the drawer
3. the drawer remains the main interaction surface

## 3. Right Drawer

The drawer becomes the full review surface.

Sections:

1. loop header
2. overview stats
3. files touched this round
4. pending question / answer form when needed
5. reviewer log toggle

### Header

Show:

1. overall loop state
2. current round
3. model
4. stop button
5. other primary actions near the top, not buried at the bottom

### Overview stats

Show:

1. loop state
2. iteration state
3. current round / total rounds
4. latest decision

### Files touched section

Show:

1. the file list from `latest_iteration.files_touched`
2. click behavior can later jump to diff or open file

### Reviewer log section

Collapsed by default.

Expanded view:

1. assistant trace
2. final result text
3. structured summary

## Multi-Session Input Queue

When more than one session is `awaiting_user`, the drawer should show a narrow switcher near the top.

Rules:

1. only include sessions currently waiting for input
2. do not include all sessions
3. selecting a chip switches the active session and updates the drawer

Suggested chip content:

1. session label
2. round number
3. compact state color

## Availability

The entire review-loop trigger/drawer should only be available when:

1. the selected session agent is Claude

If the user switches to a non-Claude session:

1. hide the trigger
2. close the drawer if it was open

## Error Design

Error needs to be visually louder than `stopped`.

Use:

1. red drawer state
2. red session-row rail
3. explicit error summary block
4. latest result text or raw error visible in the drawer

This is important because loop failure is materially different from a user stop.

## Implementation Slices

## Slice 1: Replace Pills With Color Signals

1. remove sidebar loop pills
2. add row color rail / wash
3. add red error treatment
4. add compact header trigger

## Slice 2: Drawer Shell

1. create right-side review drawer
2. hook open/close state to the selected session
3. auto-open on `awaiting_user` for the selected session
4. move the visible trigger from full-width top strip to compact right-side button
5. move drawer actions into the drawer header/top region

## Slice 3: Overview Content

1. loop state
2. iteration state
3. pass count
4. latest summary
5. files touched this round

## Slice 4: Reviewer Log

1. collapsed by default
2. expanded assistant trace
3. result text
4. error details if present

## Slice 5: Multi-Session Waiting Switcher

1. compute sessions with `review_loop_run.status === awaiting_user`
2. render top-of-drawer switcher only when count > 1
3. selecting one switches active session

## Open Questions

1. should the drawer remain pinned open per session after the user manually opens it?
2. should completed loops auto-collapse after a delay, or remain visible until dismissed?
3. do we want `files touched this round` only, or do we also want an `all touched files` rollup?

## Recommendation

Build in this exact order:

1. color-based session signals
2. compact trigger
3. drawer shell
4. overview content
5. reviewer log
6. multi-session waiting switcher

That gets the biggest UX win early without over-committing the first drawer version to a complex multi-session controller.
