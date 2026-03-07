# Review Loop UI Sketches

Date: 2026-03-07
Status: Draft
Owner: app/review-loop UX

## Goals

Design a review-loop UI that:

1. stays out of the way when unused
2. makes `input needed`, `running`, `done`, and `error` visually obvious
3. lets the user inspect reviewer output and state on demand
4. does not permanently consume terminal space
5. only appears when Claude is the configured/active agent path for the session
6. replaces the low-information sidebar pills with stronger color and layout cues

## Non-goals

1. showing the full review UI all the time
2. making the loop feel like a second terminal
3. treating the reviewer output log as the main interaction surface

## Core Interaction Model

The loop should have three layers:

1. passive signal
2. compact control surface
3. on-demand detail panel

Meaning:

1. the session row and terminal header should signal loop state even when the panel is closed
2. a compact header strip should let the user start/stop/open the detail surface
3. the detailed review panel should slide in only when requested or when input is required

## State Color System

These should become first-class visual tokens:

1. `running`: amber/orange
2. `awaiting_user`: yellow/gold
3. `completed`: green
4. `error`: red
5. `stopped`: muted gray

Recommendation:

1. use color fields, edge highlights, and soft backgrounds
2. do not rely on text pills as the primary cue
3. reserve red specifically for loop failure

## Availability Rule

The review-loop surface should only render when Claude is available/configured for the session.

Practical rule:

1. hide loop controls for non-Claude sessions
2. keep only passive status history in generic session data if needed later
3. avoid advertising loop controls where they cannot work

## Sketch A: Right Drawer

Recommended direction.

### Closed state

```text
+----------------------------------------------------------------------------------+
| Session Header                                                [Loop: Running ▸] |
| subtle orange underline across header                                            |
+----------------------------------------------------------------------------------+
|                                                                                  |
| terminal                                                                         |
|                                                                                  |
|                                                                                  |
+----------------------------------------------------------------------------------+
```

### Open state

```text
+---------------------------------------------------------------+------------------+
| Session Header                                 [Loop ▾]       | Review Loop      |
| orange header underline                                        | Running          |
+---------------------------------------------------------------+------------------+
|                                                               | Pass 1 of 2      |
| terminal                                                      | Model Sonnet 4.6 |
|                                                               |                  |
|                                                               | Summary          |
|                                                               | Critical issue   |
|                                                               | found in writable|
|                                                               | CTE handling     |
|                                                               |                  |
|                                                               | [Open log ▾]     |
|                                                               | assistant trace  |
|                                                               | tool events      |
|                                                               | result text      |
|                                                               |                  |
|                                                               | [Stop]           |
+---------------------------------------------------------------+------------------+
```

### Awaiting input state

```text
+---------------------------------------------------------------+------------------+
| Session Header                              [Loop: Input ▾]   | Review Loop      |
| warm yellow header glow                                           Needs Input    |
+---------------------------------------------------------------+------------------+
| terminal                                                      | Pass 1 of 2      |
|                                                               |                  |
|                                                               | Reviewer asks:   |
|                                                               | Should retry     |
|                                                               | exhaustion be    |
|                                                               | surfaced in UI?  |
|                                                               |                  |
|                                                               | [ answer box ]   |
|                                                               | [Send Answer]    |
|                                                               | [Show log]       |
+---------------------------------------------------------------+------------------+
```

### Why this is strong

1. hidden by default
2. easy place for question-answer interaction
3. easy place for expandable reviewer log
4. strong visual hierarchy
5. does not fight the terminal for horizontal space when closed

### Risks

1. right-side width must be carefully tuned on smaller laptops
2. needs responsive collapse behavior on narrow windows

## Sketch B: Bottom Sheet

Good fallback if the right drawer feels cramped.

### Closed state

```text
+----------------------------------------------------------------------------------+
| Session Header                                           [Loop: Running ▾]       |
| thin orange top edge on terminal                                                |
+----------------------------------------------------------------------------------+
|                                                                                  |
| terminal                                                                         |
|                                                                                  |
+----------------------------------------------------------------------------------+
```

### Open state

```text
+----------------------------------------------------------------------------------+
| terminal                                                                         |
|                                                                                  |
|                                                                                  |
+----------------------------------------------------------------------------------+
| Review Loop  Running  Pass 1/2  Model Sonnet 4.6                    [Hide]       |
|----------------------------------------------------------------------------------|
| Summary: Critical writable CTE issue found and fixed.                            |
|----------------------------------------------------------------------------------|
| [Log ▾]                                                                          |
| assistant chunks                                                                 |
| tool events                                                                      |
| result text                                                                      |
|----------------------------------------------------------------------------------|
| [Stop]                                                                           |
+----------------------------------------------------------------------------------+
```

### Why this is good

1. preserves full session width
2. naturally supports log output
3. works well with long textual reviewer traces

### Why it is weaker than A

1. competes directly with utility terminal and bottom panels
2. consumes terminal height when open
3. input-needed state is less visually distinct

## Sketch C: Floating Peek Card

Best if you want the loop to feel lighter-weight.

### Closed state

```text
+----------------------------------------------------------------------------------+
| Session Header                                                                  |
+----------------------------------------------------------------------------------+
|                                                                                  |
| terminal                                                                         |
|                                                                                  |
|                                                   +--------------------------+   |
|                                                   | Loop Running  1/2   ▸   |   |
|                                                   +--------------------------+   |
+----------------------------------------------------------------------------------+
```

### Expanded state

```text
                                                   +------------------------------+
                                                   | Review Loop                  |
                                                   | Running  Pass 1/2            |
                                                   | Summary ...                  |
                                                   | [Show log] [Stop]            |
                                                   +------------------------------+
```

### Why this is attractive

1. highly unobtrusive
2. visually distinct from core terminal chrome
3. can animate in nicely

### Why it is weaker

1. bad fit for larger reviewer logs
2. awkward for multiline input-needed interaction
3. easier to miss than a full drawer

## Sidebar Direction

The current left-sidebar pills are too small and too text-dependent.

Recommendation:

1. replace pills with a session-row color treatment
2. use a left-edge color rail or full-row accent wash
3. optionally add a small icon only as secondary signal

Sketch:

```text
| amber rail | nolo-mcp session                     working
| gold rail  | nolo-mcp session                     needs input
| green rail | nolo-mcp session                     done
| red rail   | nolo-mcp session                     error
```

Better than pills because:

1. it is legible from peripheral vision
2. it uses existing session-row real estate
3. it aligns loop state with the broader session-state system

## Log Toggle

The reviewer log should not always be open.

Recommended behavior:

1. default collapsed
2. show a one-line summary and timestamp when collapsed
3. expand into a scrollable trace when requested
4. keep tool events visually distinct from assistant prose

Suggested structure:

```text
[Log ▸]
  hidden by default

[Log ▾]
  11:02 assistant: Found writable CTE bypass
  11:02 tool: Read internal/database/query.go
  11:03 tool: Bash go test ./internal/database/...
  11:04 assistant: Fix applied, rerunning tests
  11:05 result: converged
```

## Recommended Direction

Build Sketch A first:

1. compact loop trigger in the session header
2. right-side slide drawer
3. row/header color states replacing pills
4. expandable log inside the drawer

Why:

1. best balance of hidden-by-default and inspectable-on-demand
2. strongest place for `awaiting_user` interaction
3. easiest to grow later into richer reviewer output without redesign

## Implementation Order

1. add color-based loop state treatments to sidebar and session header
2. collapse the always-visible review-loop bar into a compact trigger row
3. add a right-side drawer that can open/close on demand
4. move start/stop/answer controls into the drawer
5. add latest-iteration summary block
6. add collapsible reviewer log section

## Open Questions

1. should the drawer auto-open on `awaiting_user`, or only pulse the trigger?
2. should the drawer remain pinned open per session once the user opens it?
3. should completed runs auto-collapse, or remain visible until dismissed?
