package pty

import "sync"

type ReplaySegment struct {
	Cols uint16
	Rows uint16
	Data []byte
}

// ReplayLog retains the newest PTY output as whole write segments. Segments
// are never sliced: every write enters at a safe parse boundary (the read
// loop only emits boundary-safe chunks), and attach replays the retained
// segments verbatim, so a partially-kept segment could open the restored
// terminal mid-escape-sequence or mid-rune.
type ReplayLog struct {
	mu        sync.RWMutex
	size      int
	total     int
	truncated bool
	items     []ReplaySegment
}

func NewReplayLog(size int) *ReplayLog {
	if size <= 0 {
		size = 1
	}
	return &ReplayLog{size: size}
}

func (r *ReplayLog) Write(data []byte, cols, rows uint16) {
	if len(data) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(data) >= r.size {
		// A single write larger than the whole log cannot be retained without
		// slicing it mid-stream, so drop history rather than keep a segment
		// with an unsafe start. Attach then derives a screen snapshot instead
		// of trusting raw replay. The PTY read loop bounds writes far below
		// any realistic log size, so this is a degenerate-input guard.
		r.items = nil
		r.total = 0
		r.truncated = true
		return
	}

	chunk := ReplaySegment{
		Cols: cols,
		Rows: rows,
		Data: append([]byte(nil), data...),
	}
	r.items = append(r.items, chunk)
	r.total += len(chunk.Data)
	r.trimLocked()
}

func (r *ReplayLog) Snapshot() ([]ReplaySegment, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.items) == 0 {
		return nil, r.truncated
	}

	out := make([]ReplaySegment, 0, len(r.items))
	for _, item := range r.items {
		out = append(out, ReplaySegment{
			Cols: item.Cols,
			Rows: item.Rows,
			Data: append([]byte(nil), item.Data...),
		})
	}

	return out, r.truncated
}

// trimLocked drops whole oldest segments until the log fits its capacity.
// It never slices a segment (see the ReplayLog doc comment).
func (r *ReplayLog) trimLocked() {
	for len(r.items) > 0 && r.total > r.size {
		r.total -= len(r.items[0].Data)
		r.items = r.items[1:]
		r.truncated = true
	}
}

// LimitReplaySegmentsTail keeps the newest whole segments that fit the byte
// limit. A partially-fitting oldest segment is dropped rather than sliced:
// every segment starts at a safe parse boundary (the read loop only emits
// boundary-safe writes), so whole-segment trimming guarantees the replayed
// tail never opens mid-escape-sequence or mid-rune.
func LimitReplaySegmentsTail(segments []ReplaySegment, limit int) ([]ReplaySegment, bool) {
	if len(segments) == 0 {
		return nil, false
	}
	if limit <= 0 {
		return nil, true
	}

	total := 0
	for _, segment := range segments {
		total += len(segment.Data)
	}
	if total <= limit {
		return cloneReplaySegments(segments), false
	}

	remaining := total
	startIndex := 0
	cloned := cloneReplaySegments(segments)
	for startIndex < len(cloned) && remaining > limit {
		remaining -= len(cloned[startIndex].Data)
		startIndex += 1
	}
	if startIndex >= len(cloned) {
		return nil, true
	}
	return cloned[startIndex:], true
}

func cloneReplaySegments(segments []ReplaySegment) []ReplaySegment {
	out := make([]ReplaySegment, 0, len(segments))
	for _, segment := range segments {
		out = append(out, ReplaySegment{
			Cols: segment.Cols,
			Rows: segment.Rows,
			Data: append([]byte(nil), segment.Data...),
		})
	}
	return out
}
