# Arming a dismissal of the next auto-settle

## Why

`Cmd+.` stops the auto-settle countdown a user can see. But the moment a user
knows they want the turn kept is when they steer the agent — before the 30s arm
delay has elapsed and before anything is on screen. Today the app unregisters
the shortcut whenever nothing visible is counting down, so that press is
swallowed and the user has to wait for the countdown to appear before they can
answer it.

Done means the same key, pressed early, arms a standing "the next auto-settle
does not happen" for the session, and the pane header says so with the key that
undoes it.

## Aligned on

- **A pre-emptive press targets the focused session only.** Cancelling every
  visible countdown is honest because each countdown pill announces its own key;
  an armed dismissal has no pill until it exists, so arming every on-screen pane
  would be a silent broad action. When countdowns *are* visible the existing
  behavior stands unchanged: the press cancels all of them.
- **An arm covers the next working stretch, then clears.** It is placed in any
  state, survives repeated non-`working` state reports (the resolver re-reports
  the same state, and today's cancel-suppression clears on any of them),
  suppresses auto-settle for the upcoming `working` stretch, and is dropped when
  that stretch ends. That is literally "the next auto-settle".
- **Pressing the key while armed and nothing is pending disarms.** A way in gets
  its way out; the chip is also clickable.
- **The armed state is a static chip in the pane header**, in the countdown
  pill's own slot, with no track or bar because no time is running: `◆ Turn kept
  ⌘. undo`. Nothing new in the sidebar.
- **Auto-settle only.** The chip means one thing: this turn will not auto-close.
  The ticket nudge keeps its existing rule — the key still calls off a nudge
  countdown the user can see, and a pre-emptive press does not pre-suppress
  doorbells for tickets already unread.
- **Nothing to dismiss means nothing to arm.** With auto-settle switched off, or
  on a session auto-settle never applies to, the press is a no-op rather than a
  chip that promises something that was never going to happen.

## In scope / deferred

In scope: the daemon's standing-dismissal state and its lifecycle, the protocol
flag that carries it, the shortcut's target selection when nothing is counting
down, the pane-header chip, tests, and live verification.

Deferred: a per-session "never auto-settle this one" switch (sticky across
turns), any sidebar surface for an armed session whose tile is off-screen, and
extending the pre-emptive press to ticket nudges.
