## Why

Agent switches can leave the app visually black and unusable. The existing terminal diagnostics have captured a matching `blank_after_resize` incident for the chief session, but they cannot show whether the rest of the React shell remained healthy or whether a native browser WebView covered it. This chunk is done when a future occurrence leaves a bounded, disk-backed trail that distinguishes those failure families and preserves fatal frontend errors.

## Aligned on

- Keep the current terminal diagnostics and correlate them with a separate app-shell stream rather than widening the high-volume terminal log.
- Record agent/workspace switches, delayed DOM health snapshots, JavaScript and React failures, event-loop stalls, visibility changes, and native browser-host geometry/lifecycle calls.
- Keep diagnostics production-safe: asynchronous writes, bounded files, low-frequency heartbeats, and no terminal contents or user text.
- Show a durable error fallback when React fails so a render exception is distinguishable from a compositor or terminal failure.

## In scope / deferred

In scope: app-wide disk diagnostics, switch probes, browser-host tracing, fatal error capture, focused tests, and live verification in a non-production profile.

Deferred: automatic reloads or renderer recovery beyond the existing terminal recovery path, and a root-cause fix for the black screen until the new evidence distinguishes the failing layer.
