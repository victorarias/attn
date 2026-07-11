# Orphan agent pane lifecycle

## Why

Closing an agent must remove both the session and its workspace pane as one coherent lifecycle transition. Done means a closed pane cannot remain visible, be moved into another workspace, or reappear during later sidebar reconciliation.

## Aligned on

- Persist the pane-free layout before unregistering the session, so every later event observes the new authoritative layout.
- Reject cross-workspace moves of agent panes whose backing session no longer exists.
- Reconcile persisted agent panes against session records at startup, removing historical orphans while preserving docked tiles and valid pending spawns.
- Cover the close, move, and startup-repair paths with regression tests.

## In scope / deferred

This chunk fixes the daemon lifecycle invariant, repairs existing persisted violations on startup, and updates the user-facing changelog. Broader workspace/sidebar redesign is deferred.
