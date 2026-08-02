package daemon

import (
	"context"
	"github.com/victorarias/attn/internal/bus"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// workflowBroadcastInterval is how often the coalescing loop flushes dirty runs.
// Coalescing collapses N rapid per-call upserts for one run into ONE broadcast
// of the FULL hydrated run. The daemon already drops slow WebSocket clients past
// a 256-message buffer; sending one self-contained snapshot per tick (rather
// than a frame per call) is self-healing against that drop, because any single
// surviving frame carries the complete current state.
const workflowBroadcastInterval = 75 * time.Millisecond

// markWorkflowRunDirty flags a run for re-broadcast on the next flush. Nil-safe
// and lazy-inits the dirty set so a directly-constructed test daemon
// (&Daemon{store: ...}) does not panic.
func (d *Daemon) markWorkflowRunDirty(runID string) {
	if d == nil || runID == "" {
		return
	}
	d.workflowBroadcastMu.Lock()
	defer d.workflowBroadcastMu.Unlock()
	if d.workflowDirty == nil {
		d.workflowDirty = make(map[string]bool)
	}
	d.workflowDirty[runID] = true
}

// flushWorkflowBroadcasts snapshots and clears the dirty set, then broadcasts one
// EventWorkflowRunUpdated per dirty run carrying the full hydrated run. It is
// called by the production ticker AND directly by tests for determinism (no
// reliance on the ticker's timing).
func (d *Daemon) flushWorkflowBroadcasts() {
	if d == nil {
		return
	}

	d.workflowBroadcastMu.Lock()
	if len(d.workflowDirty) == 0 {
		d.workflowBroadcastMu.Unlock()
		return
	}
	dirty := make([]string, 0, len(d.workflowDirty))
	for runID := range d.workflowDirty {
		dirty = append(dirty, runID)
	}
	d.workflowDirty = make(map[string]bool)
	d.workflowBroadcastMu.Unlock()

	for _, runID := range dirty {
		run, err := d.getWorkflowRunHydrated(runID)
		if err != nil {
			d.logf("workflow broadcast hydrate failed for run %s: %v", runID, err)
			continue
		}
		if run == nil {
			continue
		}
		d.publishFact(FactWorkflowRunUpdated, run.RunID, run)
	}
}

// broadcastWorkflowRunUpdated emits a single full-run snapshot to all WS clients.
// WorkflowRunUpdatedMessage is its own top-level event (not a WebSocketEvent
// field), so it ships via BroadcastValue. An optional in-process hook lets tests
// observe broadcasts deterministically without a live socket — the wsHub's
// WebSocketEvent-only broadcastListener cannot see this message type.
func (d *Daemon) projectWorkflowRunUpdated(ev bus.Event) {
	run, ok := decodeFact[*protocol.WorkflowRun](d, ev)
	if !ok || run == nil {
		return
	}
	msg := &protocol.WorkflowRunUpdatedMessage{
		Event: protocol.EventWorkflowRunUpdated,
		Run:   *run,
	}
	if d.workflowBroadcastHook != nil {
		d.workflowBroadcastHook(msg)
	}
	if d.wsHub != nil {
		d.wsHub.BroadcastValue(msg)
	}
}

// startWorkflowBroadcastLoop runs the coalescing ticker until ctx is done. It is
// started from the daemon lifecycle in production; tests drive
// flushWorkflowBroadcasts directly instead.
func (d *Daemon) startWorkflowBroadcastLoop(ctx context.Context) {
	if d == nil {
		return
	}
	ticker := time.NewTicker(workflowBroadcastInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.flushWorkflowBroadcasts()
		}
	}
}
