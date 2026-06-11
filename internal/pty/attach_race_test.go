package pty

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// TestAttachSnapshotSeqConsistency proves — deterministically — that a frontend
// re-attach can lose PTY output at the replay/live boundary.
//
// On attach the daemon hands the frontend a replay payload (raw scrollback /
// replay segments) plus a LastSeq watermark; the frontend applies the replay
// and then keeps only live chunks with seq >= LastSeq, assuming everything with
// a smaller seq is already in the replay. info() captures the replay payload
// and reads LastSeq (seqCounter) at DIFFERENT times. A PTY write landing in
// that window is in neither: not yet in the replay payload, and deduped out of
// the live stream because its seq < LastSeq. It vanishes.
//
// fish emits `OSC 133;D` (close command) + `OSC 133;A` (open next prompt)
// back-to-back at each new prompt, so a single lost chunk here silently merges
// two command blocks — the make-install-then-echo-1 bug.
//
// infoSnapshotHook drives the race deterministically: it injects two PTY writes
// into the window after the payload is captured. The first injected chunk then
// has seq < LastSeq but is absent from the payload — exactly the lost chunk.
func TestAttachSnapshotSeqConsistency(t *testing.T) {
	const cols, rows = 80, 24
	defer func() { infoSnapshotHook = nil }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	s := &Session{
		id:          "race",
		cols:        cols,
		rows:        rows,
		ptmx:        r,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() returns an error, never panics
		scrollback:  NewRingBuffer(64 << 20),
		replayLog:   NewReplayLog(64 << 20),
		screen:      newVirtualScreen(cols, rows),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	go s.readLoop(nil, func(string, ...any) {})

	// Authoritative stream: every fanned chunk, in order, no dedup.
	mirror := &streamMirror{}
	s.addSubscriber("mirror", mirror.send, nil)

	write := func(line string) int {
		n, werr := w.Write([]byte(line))
		if werr != nil {
			t.Fatalf("pipe write: %v", werr)
		}
		return n
	}
	waitMirror := func(want int) {
		deadline := time.Now().Add(2 * time.Second)
		for mirror.len() < want {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for mirror to reach %d bytes (have %d)", want, mirror.len())
			}
			time.Sleep(100 * time.Microsecond)
		}
	}

	// Seed the "replay history": several commands' worth of output, all fully
	// fanned (and thus in the replay payload) before we attach.
	seeded := 0
	for i := range 50 {
		seeded += write(fmt.Sprintf("SEED%05d|prior-command-output-line\n", i))
	}
	waitMirror(seeded)

	// Arm the race: when info() reaches the post-payload window, inject two PTY
	// writes (think: fish's `OSC 133;D` + `OSC 133;A` at a new prompt) and wait
	// until both are fanned so LastSeq is read AHEAD of the captured payload.
	lostChunk := "GAP_LOST|command-end+next-prompt\n"
	keptChunk := "GAP_KEPT|following-output\n"
	// Inject as two distinct fanned chunks (two seqs): write, wait for it to be
	// fanned, then write the next. Back-to-back writes would coalesce into one
	// read/seq and not advance the watermark past the lost chunk.
	injectOneChunk := func(line string) {
		start := s.seqCounter.Load()
		write(line)
		deadline := time.Now().Add(2 * time.Second)
		for s.seqCounter.Load() <= start {
			if time.Now().After(deadline) {
				t.Errorf("injected write %q was not fanned in time", firstLine([]byte(line)))
				return
			}
			time.Sleep(50 * time.Microsecond)
		}
	}
	var once sync.Once
	infoSnapshotHook = func() {
		once.Do(func() {
			injectOneChunk(lostChunk)
			injectOneChunk(keptChunk)
		})
	}

	// Model Manager.Attach exactly: register the live subscriber, then read the
	// replay payload + watermark (the hook fires inside info()).
	probe := &recordingSink{}
	s.addSubscriber("probe", probe.send, nil)
	info := s.info()

	segLen := 0
	for _, seg := range info.ReplaySegments {
		segLen += len(seg.Data)
	}
	if segLen != seeded {
		t.Fatalf("replay payload (%d bytes) should be exactly the seeded history (%d bytes)", segLen, seeded)
	}

	// The bytes the frontend would actually apply after the replay: live chunks
	// with seq >= LastSeq.
	var applied []byte
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		applied = probe.appliedAfter(info.LastSeq, 8)
		if len(applied) > 0 {
			break
		}
		time.Sleep(100 * time.Microsecond)
	}
	s.removeSubscriber("probe")
	if len(applied) == 0 {
		t.Fatal("probe never received an applied live chunk")
	}

	waitMirror(segLen + len(applied))
	ok, have := mirror.matchAt(segLen, applied)
	if !have {
		t.Fatal("mirror did not cover the reconstruction boundary")
	}
	if !ok {
		// reconstruction = replay payload ++ applied-live has a hole/overlap:
		// the user loses (or double-sees) output across the re-attach.
		t.Fatalf("re-attach lost output: replay ended at %d bytes, LastSeq=%d, but the first applied live bytes are %q, not the next bytes in the stream %q",
			segLen, info.LastSeq, firstLine(applied), firstLine(mirror.slice(segLen, len(applied))))
	}
}

func firstLine(b []byte) string {
	line, _, _ := bytes.Cut(b, []byte{'\n'})
	return string(line)
}

// recordingSink captures the live chunks a single attached subscriber receives.
type recordingSink struct {
	mu     sync.Mutex
	chunks []recordedChunk
}

type recordedChunk struct {
	seq  uint32
	data []byte
}

func (r *recordingSink) send(data []byte, seq uint32) bool {
	r.mu.Lock()
	r.chunks = append(r.chunks, recordedChunk{seq: seq, data: append([]byte(nil), data...)})
	r.mu.Unlock()
	return true
}

// appliedAfter returns the concatenated bytes of the first maxChunks live
// chunks the frontend would APPLY: those with seq >= lastSeq (the rest are
// deduped as already-in-replay).
func (r *recordingSink) appliedAfter(lastSeq uint32, maxChunks int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []byte
	n := 0
	for _, c := range r.chunks {
		if c.seq >= lastSeq {
			out = append(out, c.data...)
			n++
			if n >= maxChunks {
				break
			}
		}
	}
	return out
}

// streamMirror is the authoritative ordered byte stream (every fanned chunk,
// no dedup) — the ground truth of what the user should see.
type streamMirror struct {
	mu  sync.Mutex
	buf []byte
}

func (m *streamMirror) send(data []byte, _ uint32) bool {
	m.mu.Lock()
	m.buf = append(m.buf, data...)
	m.mu.Unlock()
	return true
}

func (m *streamMirror) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.buf)
}

// matchAt reports whether p appears in the authoritative stream starting at
// offset. have=false means the mirror has not yet reached offset+len(p).
func (m *streamMirror) matchAt(offset int, p []byte) (ok bool, have bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if offset+len(p) > len(m.buf) {
		return false, false
	}
	return bytes.Equal(m.buf[offset:offset+len(p)], p), true
}

func (m *streamMirror) slice(offset, length int) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := min(offset+length, len(m.buf))
	offset = min(offset, len(m.buf))
	return append([]byte(nil), m.buf[offset:end]...)
}
