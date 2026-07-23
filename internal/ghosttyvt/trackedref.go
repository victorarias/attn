//go:build darwin && arm64

// Tracked grid references for the OSC 133 block tracker (server-authoritative
// terminal, Phase 3a). A TrackedRef pins a grid cell so its SCREEN-space
// coordinate follows the cell across scrolling, scrollback pruning, and
// reflow — the primitive the worker-side block table (internal/pty) uses to
// carry block anchor rows through a serialize/restore round trip.
package ghosttyvt

/*
#include <ghostty/vt.h>

static GhosttyPoint ghosttyvt_point(GhosttyPointTag tag, uint16_t x, uint32_t y) {
	GhosttyPoint p;
	p.value._padding[0] = 0;
	p.value._padding[1] = 0;
	p.tag = tag;
	p.value.coordinate.x = x;
	p.value.coordinate.y = y;
	return p;
}
*/
import "C"

import "sync/atomic"

// liveTrackedRefs counts allocated-but-not-yet-freed TrackedRefs across the
// process. Block-table tests assert it returns to its baseline at teardown, so
// a missed Free on any block retirement path (cap eviction, alt-drop,
// self-heal replacement, session close) is a red test, not a native leak.
var liveTrackedRefs atomic.Int64

// LiveTrackedRefs returns the number of TrackedRefs not yet freed.
func LiveTrackedRefs() int { return int(liveTrackedRefs.Load()) }

// TrackedRef pins a grid cell that follows its content across scrolling,
// scrollback pruning, and resize/reflow. It is owned by the Terminal that
// created it; resolve and free while that Terminal is alive (or after — the
// handle stays freeable, it just reports no value).
type TrackedRef struct {
	ref C.GhosttyTrackedGridRef
}

// TrackCursor pins the current cursor cell as a tracked reference. Returns nil
// if the terminal is closed or the pin fails.
func (t *Terminal) TrackCursor() *TrackedRef {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	cx, cy := t.cursorXYLocked()
	p := C.ghosttyvt_point(C.GHOSTTY_POINT_TAG_ACTIVE, C.uint16_t(cx), C.uint32_t(cy))
	var ref C.GhosttyTrackedGridRef
	if rc := C.ghostty_terminal_grid_ref_track(t.term, p, &ref); rc != C.GHOSTTY_SUCCESS {
		return nil
	}
	liveTrackedRefs.Add(1)
	return &TrackedRef{ref: ref}
}

// ScreenPoint resolves the reference to full-screen coordinates (scrollback +
// active area, 0-indexed from the top of retained scrollback). ok is false when
// the tracked cell has been discarded (e.g. pruned past the scrollback cap).
// Callers must synchronize with Terminal writes externally.
func (r *TrackedRef) ScreenPoint() (x, y int, ok bool) {
	if r.ref == nil {
		return 0, 0, false
	}
	var out C.GhosttyPointCoordinate
	if rc := C.ghostty_tracked_grid_ref_point(r.ref, C.GHOSTTY_POINT_TAG_SCREEN, &out); rc != C.GHOSTTY_SUCCESS {
		return 0, 0, false
	}
	return int(out.x), int(out.y), true
}

// Free releases the native reference. Idempotent.
func (r *TrackedRef) Free() {
	if r.ref == nil {
		return
	}
	C.ghostty_tracked_grid_ref_free(r.ref)
	r.ref = nil
	liveTrackedRefs.Add(-1)
}
