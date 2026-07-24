# Copilot mouse scrolling

## Why

Copilot sessions launched by attn currently receive `--no-mouse`, overriding
the user's Copilot configuration. When terminal mouse tracking is enabled,
attn also misreports passive pointer movement as a held left-button drag. Done
means attn leaves Copilot's mouse mode to the user and faithfully forwards its
mouse protocol without leaving selection stuck.

## Aligned on

- Respect the user's Copilot mouse configuration by passing neither
  `--mouse=on` nor `--no-mouse`.
- Harden attn's application-mouse forwarding: once a tracked press begins,
  observe release at document scope and synthesize a release from the last
  valid terminal cell on blur or pointer cancellation.
- Encode passive DECSET 1003 motion as "no button" rather than left drag after
  the actual button has been released.
- Preserve Option-drag as attn-owned text selection while ordinary mouse input
  remains owned by a mouse-tracking TUI.
- Cover both sides of the regression: Copilot receives no launch-time mouse
  override, a tracked drag released outside the terminal delivers exactly one
  release report, and later passive motion reports no held button.

## In scope / deferred

In scope: Copilot launch arguments, tracked-mouse lifecycle, automated tests,
and live verification in a non-production app. Deferred: changing the generic
alternate-screen/no-mouse wheel fallback used by other TUIs.
