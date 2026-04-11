package pty

import "sync"

type ReplaySegment struct {
	Cols uint16
	Rows uint16
	Data []byte
}

type ReplayLog struct {
	mu    sync.RWMutex
	size  int
	total int
	items []ReplaySegment
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
		r.items = []ReplaySegment{{
			Cols: cols,
			Rows: rows,
			Data: append([]byte(nil), data[len(data)-r.size:]...),
		}}
		r.total = r.size
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
		return nil, false
	}

	out := make([]ReplaySegment, 0, len(r.items))
	for _, item := range r.items {
		out = append(out, ReplaySegment{
			Cols: item.Cols,
			Rows: item.Rows,
			Data: append([]byte(nil), item.Data...),
		})
	}

	truncated := r.total >= r.size
	return out, truncated
}

func (r *ReplayLog) trimLocked() {
	for len(r.items) > 0 && r.total > r.size {
		excess := r.total - r.size
		headLen := len(r.items[0].Data)
		if headLen <= excess {
			r.total -= headLen
			r.items = r.items[1:]
			continue
		}
		r.items[0].Data = append([]byte(nil), r.items[0].Data[excess:]...)
		r.total -= excess
		break
	}
}

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

	remainingTrim := total - limit
	startIndex := 0
	cloned := cloneReplaySegments(segments)
	for startIndex < len(cloned) && remainingTrim > 0 {
		headLen := len(cloned[startIndex].Data)
		if headLen <= remainingTrim {
			remainingTrim -= headLen
			startIndex += 1
			continue
		}
		cloned[startIndex].Data = append([]byte(nil), cloned[startIndex].Data[remainingTrim:]...)
		remainingTrim = 0
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
