# Terminal overflow diagnostics cleanup

## Why

The terminal diagnostics currently treat model-size overflow and DOM-position drift as the same incident even though only model-size overflow can be repaired by `fit()`. With the canvas structurally anchored, continuing to force container and canvas rectangle reads every sweep adds complexity and can still trigger futile repair attempts.

## Aligned on

- Remove DOM-position and DOM-rectangle signals from the production render probe and geometry snapshot.
- Keep the low-frequency overflow watchdog for stale PTY geometry, covering both rows and columns from model dimensions and renderer cell metrics.
- Only attempt `fit()` for overflow that a geometry change can actually repair.
- Keep the packaged offset soak and the direct editable-flow regression as verification coverage.

## In scope / deferred

This chunk simplifies terminal overflow diagnostics, repair tests, and comments. The separate blank/underdraw detector, lifecycle ring buffer, and packaged terminal scenarios remain because they cover different failure modes.
