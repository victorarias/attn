# Muted Sessions UX Plan

Date: 2026-04-07
Status: Done
Owner: app/session attention UX

## Summary

Add a first-class `mute session` workflow for sessions the user wants to keep alive but remove from active attention surfaces.

This is explicitly not a state rewrite. A muted session preserves its real runtime state, but it is removed from the normal attention model and parked in a collapsed `Muted Sessions` section.

The core user problem is:

1. some sessions remain alive and legitimately `waiting_input`
2. the user does not want to close them yet
3. the user also does not want them to keep pulling visual attention for the rest of the day

## Chosen UX Model

Use `mute` rather than `mark as read`.

Why:

1. `mark as read` implies the issue has been handled
2. changing `waiting_input` to `idle` would collapse "done" and "deferred" into the same signal
3. `mute` better matches the intended user action: keep it, hide it, come back later

This also fits the current backend model, which already includes a session-level `muted` field in the session schema and attention adapter path.

## Product Rules

### Meaning of mute

Muting a session means:

1. the session stays alive
2. the session keeps its true underlying state
3. the session is removed from active attention surfaces
4. the session is parked in a dedicated muted area until manually unmuted

Unmuting a session means:

1. remove the muted flag
2. return the session to the normal visible list
3. show it with whatever its current real state is at that moment

If a muted session is still `waiting_input` when unmuted, it returns as yellow.

### Attention behavior

Muted sessions should not:

1. count toward the global attention count
2. appear in the attention drawer
3. appear in `Waiting for input` session groups
4. participate in jump-to-waiting behavior

Muted sessions should:

1. remain visible in a separate muted inventory area
2. preserve their underlying state for eventual restoration

### Persistence

Muted state should persist across app restarts and daemon restarts.

Muted sessions should remain muted even if their underlying state changes while parked.

## Sidebar UX

### Row action

Add a `Mute` action to session rows in the normal session list.

Placement:

1. alongside existing row actions on hover
2. available from the sessions sidebar, not only from the dashboard

Initial scope:

1. allow mute for all normal sessions
2. primary motivation is `waiting_input`, but the interaction need also applies to other sessions the user wants out of view temporarily

### Muted section

Add a dedicated `Muted Sessions` section at the bottom of the left sidebar.

Rules:

1. it is collapsed by default
2. collapsed header shows count only
3. example label: `Muted Sessions (4)`
4. no per-state summary appears in the collapsed header

When expanded:

1. show the full muted session rows
2. each row includes label, branch, and endpoint/location metadata consistent with the normal sidebar treatment
3. rows are visually subdued relative to active sessions
4. each row exposes an `Unmute` action

### Visual treatment

Muted sessions should feel parked, not active.

Recommended treatment:

1. muted section header uses dim text and low-contrast styling
2. muted rows use subdued text and no attention glow
3. real state dots may still appear, but muted styling must dominate
4. muted rows should not look like urgent work even if their underlying state is `waiting_input`

### Ordering

The muted section lives below the normal visible session list.

Within the muted section, preserve stable ordering rather than trying to surface urgency. The whole point is that these rows are intentionally deprioritized.

## Dashboard UX

### Muted summary group

Add a collapsed muted summary group to the Sessions card on the dashboard.

Rules:

1. place it after the normal session groups
2. show count only
3. example label: `Muted Sessions (4)`
4. do not expand muted sessions inline on the dashboard

### Click behavior

Clicking the dashboard muted group should:

1. switch to the sessions view
2. ensure the sidebar is visible
3. expand the `Muted Sessions` section in the sidebar

The dashboard remains a summary surface. Session management happens in the sidebar.

## Interaction Details

### Mute flow

When the user mutes a session:

1. the session animates out of the normal list
2. it moves into the muted section
3. the attention count updates immediately
4. the attention drawer no longer includes it
5. a toast appears: `Session muted. Undo`

### Unmute flow

When the user unmutes a session:

1. it leaves the muted section
2. it returns to the normal session list based on its current real state
3. if it is still `waiting_input`, it reappears in the relevant yellow attention surfaces

### Undo

Reuse the existing short-lived undo toast pattern already used for mute-related actions elsewhere in the app.

## Data Model And Implementation Direction

The important product rule is to preserve real state and treat mute as an attention-layer override.

Implementation direction:

1. reuse the existing session `muted` field rather than inventing a fake `read` or `idle` state
2. filter muted sessions out of attention calculations
3. surface muted sessions in a dedicated sidebar/dashboard summary path
4. keep session state transitions independent from mute transitions

This avoids conflating:

1. `idle`: truly done
2. `waiting_input`: needs input
3. `muted`: intentionally parked by the user

## Non-goals

This plan does not include:

1. automatic unmute behavior
2. time-based snoozing
3. per-state counts inside the muted group header
4. inline dashboard expansion of muted sessions
5. rewriting session runtime state to simulate read/done behavior

## Suggested Implementation Phases

### Phase 1: Functional mute support

1. expose session mute/unmute controls in the frontend
2. remove muted sessions from attention counts and drawer surfaces
3. add sidebar `Muted Sessions` section with collapsed-by-default behavior
4. add mute undo toast

### Phase 2: Dashboard integration

1. add dashboard muted summary group
2. route dashboard click into sessions view with muted section expanded

### Phase 3: Polish

1. refine muted row styling and transitions
2. verify keyboard navigation and accessibility behavior
3. add tests for mute persistence and attention-count exclusion

## Acceptance Criteria

The feature is correct when:

1. muting a `waiting_input` session removes it from yellow attention surfaces without closing it
2. the sidebar shows a collapsed `Muted Sessions (N)` section at the bottom
3. expanding the muted section reveals full session rows with metadata and unmute actions
4. the dashboard shows a collapsed muted summary group with count only
5. clicking the dashboard muted summary navigates to the sessions view and expands the sidebar muted section
6. unmuting restores the session to the normal list in its true current state
7. muted sessions remain muted across restart
