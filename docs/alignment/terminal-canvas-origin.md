# Terminal canvas origin

## Why

The terminal model can have the correct row and column geometry while its canvas is displaced inside the editable terminal host, leaving the bottom rows rendered outside the visible pane. This chunk is done when editable-flow content cannot move the canvas away from the pane origin and the behavior is proven in a packaged non-production app.

## Aligned on

- Keep the terminal's editable host and existing keyboard, composition, and IME behavior.
- Remove the canvas from editable document flow by anchoring it to the host's top-left corner; the renderer remains the authority for its explicit width and height.
- Add a regression that contaminates the editable flow ahead of the canvas and proves the canvas stays anchored.
- Verify the fix against the existing offset diagnostics and packaged-app scenario in a non-production profile.

## In scope / deferred

This chunk fixes and verifies the positional rendering bug and updates the user-facing changelog. Simplifying the detector, recovery behavior, and temporary terminal diagnostics is deferred until the fix is proven, so cleanup can be based on which safeguards still provide value.
