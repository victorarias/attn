package pty

// Worker-side OSC 133 command-block table — the production workerBlockTable.
//
// It mirrors the client's TerminalBlockStore (app/src/utils/terminalBlocks.ts):
// at most one pending block, self-heal when a command-end (133;D) is lost, a
// cap of maxBlocks with oldest-first eviction. The lifecycle logic is proven
// identical to the client store by the shared corpus
// (testdata/osc133_block_corpus.json) in blocktable_test.go.
//
// The difference from the client is positional: the client stores absolute
// buffer rows plus a text anchor and re-anchors on drift; the worker stores a
// tracked grid ref per marker (blockRef) that follows its cell across
// scrolling, pruning, and reflow, and resolves to a SCREEN-space row only at
// snapshot time. Command text and exit code are captured at parse time — they
// are unrecoverable from the grid later, which is the whole reason this table
// exists.
//
// Refs are native memory. Every ref a block holds must be freed on every
// retirement path (cap eviction, self-heal completion, bare-enter discard,
// session Close). The rails leak counter (ghosttyvt.LiveTrackedRefs) makes a
// missed Free a red test. A single marker's ref can be shared by two blocks
// (self-heal: the healing marker is both the old block's end and the new
// block's start), so refs are reference-counted; the native ref frees exactly
// once, when the last holder releases it.

const maxBlocks = 200

// sharedRef reference-counts one blockRef so a position pinned once can back
// more than one block with a single native ref and a single Free.
type sharedRef struct {
	ref blockRef
	rc  int
}

func newSharedRef(r blockRef) *sharedRef { return &sharedRef{ref: r} }

func (s *sharedRef) acquire() { s.rc++ }

func (s *sharedRef) release() {
	s.rc--
	if s.rc <= 0 {
		s.rc = 0
		if s.ref != nil {
			s.ref.Free()
			s.ref = nil
		}
	}
}

// freeIfUnheld frees the underlying ref only when nothing acquired it (an
// orphan marker whose position no block kept). No-op once acquired.
func (s *sharedRef) freeIfUnheld() {
	if s.rc == 0 && s.ref != nil {
		s.ref.Free()
		s.ref = nil
	}
}

func (s *sharedRef) point() (x, y int, ok bool) {
	if s == nil || s.ref == nil {
		return 0, 0, false
	}
	return s.ref.ScreenPoint()
}

// trackedBlock is one command block. hasCommand mirrors the client's
// "outputStartRow is set" — the marker that makes a bare Enter distinguishable
// from a real command and that arms self-heal. altScreen records whether the
// block was opened while the alternate screen was active; such blocks are
// excluded at snapshot (blocks are a primary-screen concept).
type trackedBlock struct {
	id         uint64
	promptRef  *sharedRef
	inputRef   *sharedRef
	outputRef  *sharedRef
	endRef     *sharedRef
	command    *string
	exitCode   *int32
	hasCommand bool
	altScreen  bool
}

func (b *trackedBlock) release() {
	// promptRef/inputRef (and self-heal end/prompt) can be the same *sharedRef;
	// releasing each acquire keeps the reference count balanced.
	for _, r := range []*sharedRef{b.promptRef, b.inputRef, b.outputRef, b.endRef} {
		if r != nil {
			r.release()
		}
	}
}

// blockTable is the production workerBlockTable. All methods run under
// replayMu (via blockFeeder), so it holds no lock of its own.
type blockTable struct {
	completed []*trackedBlock
	pending   *trackedBlock
	nextID    uint64
}

func newBlockTable() *blockTable {
	return &blockTable{nextID: 1}
}

// ApplyMarker applies one parsed marker whose position is pinned by ref.
func (bt *blockTable) ApplyMarker(m osc133Marker, ref blockRef, altScreen bool) {
	cur := newSharedRef(ref)

	// Self-heal against a lost command-end: if a command already ran in the
	// open block and a marker that begins a NEW command context arrives, the
	// previous 133;D never reached us. Close the open block at this marker's
	// position so two commands don't merge into one.
	if bt.pending != nil && bt.pending.hasCommand &&
		(m.Kind == osc133PromptStart || m.Kind == osc133InputStart || m.Kind == osc133PreExec) {
		bt.complete(bt.pending, cur, nil)
		bt.pending = nil
	}

	switch m.Kind {
	case osc133PromptStart:
		bt.pending = &trackedBlock{id: bt.nextID, promptRef: cur, altScreen: altScreen}
		cur.acquire()
		bt.nextID++
	case osc133InputStart:
		if bt.pending == nil {
			bt.pending = bt.openPending(cur, altScreen)
		}
		bt.pending.inputRef = cur
		cur.acquire()
	case osc133PreExec:
		if bt.pending == nil {
			bt.pending = bt.openPending(cur, altScreen)
		}
		bt.pending.outputRef = cur
		cur.acquire()
		bt.pending.command = m.Cmdline
		bt.pending.hasCommand = true
	case osc133CommandEnd:
		p := bt.pending
		bt.pending = nil
		switch {
		case p != nil && p.hasCommand:
			bt.complete(p, cur, m.ExitCode)
		case p != nil:
			// Bare Enter at the prompt: nothing copyable. The id is still
			// consumed (nextID already advanced), the block's refs are freed.
			p.release()
		}
	}

	// A marker whose position no block kept (orphan D, unknown subtype) must
	// not leak its native ref.
	cur.freeIfUnheld()
}

func (bt *blockTable) openPending(promptRef *sharedRef, altScreen bool) *trackedBlock {
	b := &trackedBlock{id: bt.nextID, promptRef: promptRef, altScreen: altScreen}
	promptRef.acquire()
	bt.nextID++
	return b
}

// complete pushes a pending block into completed with its end position, then
// enforces the cap oldest-first (freeing every evicted block's refs).
func (bt *blockTable) complete(p *trackedBlock, endRef *sharedRef, exitCode *int32) {
	p.endRef = endRef
	endRef.acquire()
	p.exitCode = exitCode
	bt.completed = append(bt.completed, p)
	if len(bt.completed) > maxBlocks {
		evicted := bt.completed[:len(bt.completed)-maxBlocks]
		bt.completed = append([]*trackedBlock(nil), bt.completed[len(bt.completed)-maxBlocks:]...)
		for _, e := range evicted {
			e.release()
		}
	}
}

// SnapshotBlocks resolves every serializable block to SCREEN-space rows. A
// completed block whose prompt or end ref no longer resolves is dropped
// (correct-or-absent). The pending block, if any, is included with Pending set
// so the client can re-arm it and continue the id sequence.
func (bt *blockTable) SnapshotBlocks() []AttachBlockData {
	out := make([]AttachBlockData, 0, len(bt.completed)+1)
	for _, b := range bt.completed {
		if d, ok := b.resolve(false); ok {
			out = append(out, d)
		}
	}
	if bt.pending != nil {
		if d, ok := bt.pending.resolve(true); ok {
			out = append(out, d)
		}
	}
	return out
}

// Close frees every held ref. The table is unusable afterwards.
func (bt *blockTable) Close() {
	for _, b := range bt.completed {
		b.release()
	}
	bt.completed = nil
	if bt.pending != nil {
		bt.pending.release()
		bt.pending = nil
	}
}

func (b *trackedBlock) resolve(pending bool) (AttachBlockData, bool) {
	// Blocks pinned while the alternate screen was active are not primary-screen
	// command blocks; exclude them from the restore payload.
	if b.altScreen {
		return AttachBlockData{}, false
	}
	_, promptY, ok := b.promptRef.point()
	if !ok {
		return AttachBlockData{}, false
	}
	d := AttachBlockData{ID: b.id, Pending: pending, PromptRow: int32(promptY)}
	if x, y, ok := b.inputRef.point(); ok {
		row, col := int32(y), int32(x)
		d.InputRow = &row
		d.InputCol = &col
	}
	if _, y, ok := b.outputRef.point(); ok {
		row := int32(y)
		d.OutputStartRow = &row
	}
	if !pending {
		_, y, ok := b.endRef.point()
		if !ok {
			// The end position is essential for a completed block; without it
			// the block's extent is unknown — drop rather than show a wrong row.
			return AttachBlockData{}, false
		}
		row := int32(y)
		d.EndRow = &row
	}
	if b.hasCommand {
		cmd := ""
		if b.command != nil {
			cmd = *b.command
		}
		d.Command = &cmd
	}
	d.ExitCode = b.exitCode
	return d, true
}
