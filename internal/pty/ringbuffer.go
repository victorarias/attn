package pty

import "sync"

// RingBuffer stores the last N bytes written to it.
type RingBuffer struct {
	mu   sync.RWMutex
	buf  []byte
	size int
	pos  int
	full bool
}

func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = 1
	}
	return &RingBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write appends bytes to the ring, overwriting oldest bytes when full.
func (r *RingBuffer) Write(data []byte) {
	if len(data) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// If data is larger than the ring, only keep the tail.
	if len(data) >= r.size {
		copy(r.buf, data[len(data)-r.size:])
		r.pos = 0
		r.full = true
		return
	}

	remaining := len(data)
	off := 0
	for remaining > 0 {
		spaceToEnd := r.size - r.pos
		n := remaining
		if n > spaceToEnd {
			n = spaceToEnd
		}
		copy(r.buf[r.pos:r.pos+n], data[off:off+n])
		r.pos = (r.pos + n) % r.size
		off += n
		remaining -= n
		if r.pos == 0 {
			r.full = true
		}
	}
}

// Snapshot returns a copy of current bytes in chronological order.
// The second return value is true when old output has been truncated.
func (r *RingBuffer) Snapshot() ([]byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.full {
		if r.pos == 0 {
			return nil, false
		}
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out, false
	}

	out := make([]byte, r.size)
	copy(out, r.buf[r.pos:])
	copy(out[r.size-r.pos:], r.buf[:r.pos])
	return out, true
}
