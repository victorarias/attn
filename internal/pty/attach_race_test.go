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
// re-attach can lose PTY output at the restore/live boundary.
//
// On attach the daemon hands the frontend a restore payload (the serialized
// ghostty snapshot) plus a LastSeq watermark; the frontend applies the restore
// and then keeps only live chunks with seq > LastSeq, assuming everything up to
// LastSeq is already restored. If info() captured the payload and read LastSeq
// at DIFFERENT times, a PTY write landing in that window is in neither: not yet
// in the payload, and deduped out of the live stream because its seq <= LastSeq.
// It vanishes. info() serializes the snapshot and reads lastReplaySeq under a
// single replayMu critical section to close that window; this test proves the
// watermark bounds exactly the covered history regardless of how the payload is
// represented — LastSeq names the last chunk baked into the payload, so the
// authoritative stream and the applied-live stream meet with no hole or overlap.
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

	gt := newTestGhostty(t, cols, rows)
	s := &Session{
		id:          "race",
		cols:        cols,
		rows:        rows,
		ptmx:        r,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() returns an error, never panics
		screen:      newVirtualScreen(cols, rows),
		ghostty:     gt,
		blockFeed:   newBlockFeeder(gt),
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

	// The payload covers exactly the seeded history (all of it was fanned before
	// the attach), so the restore/live boundary sits at `seeded` bytes in the
	// authoritative stream. This holds independently of the payload's
	// representation (ghostty snapshot); what is under test is that LastSeq names
	// the last chunk baked into the payload.
	boundary := seeded

	// The bytes the frontend would actually apply after the restore: live chunks
	// with seq > LastSeq (matching planLivePtyOutput's stale rule).
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

	waitMirror(boundary + len(applied))
	ok, have := mirror.matchAt(boundary, applied)
	if !have {
		t.Fatal("mirror did not cover the reconstruction boundary")
	}
	if !ok {
		// reconstruction = restore payload ++ applied-live has a hole/overlap:
		// the user loses (or double-sees) output across the re-attach.
		t.Fatalf("re-attach lost output: payload covered %d bytes, LastSeq=%d, but the first applied live bytes are %q, not the next bytes in the stream %q",
			boundary, info.LastSeq, firstLine(applied), firstLine(mirror.slice(boundary, len(applied))))
	}
}

// TestScreenSnapshotSeqConsistency proves the same replay-boundary loss for
// the snapshot observer path (Manager.Snapshot): grid/read-only consumers seed
// a tile from screenSnapshot() and then dedupe the live firehose against its
// LastSeq. The read loop allocates a chunk's sequence number BEFORE applying
// the chunk to replay/screen state, so a snapshot taken in that gap must
// report the watermark of the last APPLIED chunk (lastReplaySeq), not
// seqCounter. Reporting seqCounter would claim coverage of the in-flight chunk
// while the snapshot screen lacks its bytes — the observer then drops that
// live chunk and the bytes vanish, the same class of loss as the attach race
// above.
//
// readLoopSeqGapHook drives the race deterministically: it takes the observer
// snapshot inside the gap for a known marker chunk.
func TestScreenSnapshotSeqConsistency(t *testing.T) {
	const cols, rows = 80, 24
	defer func() { readLoopSeqGapHook = nil }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })

	gt := newTestGhostty(t, cols, rows)
	s := &Session{
		id:          "snapshot-race",
		cols:        cols,
		rows:        rows,
		ptmx:        r,
		cmd:         &exec.Cmd{}, // unstarted: readLoop's Wait() returns an error, never panics
		screen:      newVirtualScreen(cols, rows),
		ghostty:     gt,
		blockFeed:   newBlockFeeder(gt),
		subscribers: make(map[string]*sessionSubscriber),
		running:     true,
		exited:      make(chan struct{}),
		startedAt:   time.Now(),
	}
	go s.readLoop(nil, func(string, ...any) {})

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

	// Seed applied history so the gap snapshot has a real screen and a non-zero
	// watermark to report.
	seeded := write("SEED|earlier-output\r\n")
	waitMirror(seeded)

	// Arm the race: when the marker chunk's seq is allocated but its bytes have
	// not yet reached the screen, take the observer snapshot inside the gap.
	var (
		once    sync.Once
		gapInfo AttachInfo
		gapSeq  uint32
	)
	readLoopSeqGapHook = func() {
		once.Do(func() {
			gapSeq = s.seqCounter.Load()
			gapInfo = s.screenSnapshot()
		})
	}
	written := write("MARKER|in-flight-chunk\r\n")
	waitMirror(seeded + written)

	if gapSeq == 0 {
		t.Fatal("readLoopSeqGapHook never fired")
	}
	if bytes.Contains(gapInfo.ScreenSnapshot, []byte("MARKER")) {
		t.Fatal("gap snapshot already contains the in-flight chunk; the seam fired too late to exercise the race")
	}
	// The core invariant: a snapshot whose screen lacks the chunk's bytes must
	// not claim the chunk. LastSeq >= gapSeq would make an observer drop the
	// live chunk carrying bytes the snapshot does not have.
	if gapInfo.LastSeq >= gapSeq {
		t.Fatalf("snapshot taken before chunk %d reached the screen reports LastSeq=%d — observers deduping seq <= LastSeq would lose the chunk's bytes", gapSeq, gapInfo.LastSeq)
	}

	// Once applied, the pair is consistent again: the watermark covers the
	// marker chunk and the screen contains its bytes.
	settled := s.screenSnapshot()
	if settled.LastSeq != gapSeq {
		t.Fatalf("settled snapshot LastSeq = %d, want %d (the applied marker chunk)", settled.LastSeq, gapSeq)
	}
	if !bytes.Contains(settled.ScreenSnapshot, []byte("MARKER")) {
		t.Fatalf("settled snapshot screen should contain the applied marker chunk; got %q", settled.ScreenSnapshot)
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
// chunks the frontend would APPLY: those with seq > lastSeq (the rest are
// deduped as already-in-replay). This must mirror the real client rule in
// planLivePtyOutput, or the test validates a contract no client implements.
func (r *recordingSink) appliedAfter(lastSeq uint32, maxChunks int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []byte
	n := 0
	for _, c := range r.chunks {
		if c.seq > lastSeq {
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
