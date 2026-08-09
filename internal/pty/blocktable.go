package pty

// Worker-side OSC 133 command-block table — the production workerBlockTable.
// Lifecycle mirrors the client's TerminalBlockStore, proven identical by the
// shared corpus (testdata/osc133_block_corpus.json). Positions are tracked grid
// refs resolved to SCREEN-space rows at snapshot time; command text and exit
// code are captured at parse time — unrecoverable from the grid later. Refs
// are native memory: every retirement path must free them, and one marker's
// ref can back two blocks (self-heal), hence the reference counting.

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

// trackedBlock is one command block. hasCommand distinguishes a bare Enter
// from a real command and arms self-heal; altScreen blocks drop at snapshot.
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
	// Fields can share a *sharedRef (self-heal); releasing each acquire keeps
	// the count balanced.
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

	// Self-heal a lost command-end: a marker beginning a NEW command context
	// while a command already ran means the previous 133;D never arrived —
	// close the open block here so two commands don't merge.
	if bt.pending != nil && bt.pending.hasCommand &&
		(m.Kind == osc133PromptStart || m.Kind == osc133InputStart || m.Kind == osc133PreExec) {
		bt.complete(bt.pending, cur, nil)
		bt.pending = nil
	}

	switch m.Kind {
	case osc133PromptStart:
		// A redrawn prompt replaces the open block; retire the displaced one
		// or its refs leak.
		if bt.pending != nil {
			bt.pending.release()
		}
		bt.pending = &trackedBlock{id: bt.nextID, promptRef: cur, altScreen: altScreen}
		cur.acquire()
		bt.nextID++
	case osc133InputStart:
		if bt.pending == nil {
			bt.pending = bt.openPending(cur, altScreen)
		}
		// A repeated input-start re-pins; release the ref it replaces.
		if bt.pending.inputRef != nil {
			bt.pending.inputRef.release()
		}
		bt.pending.inputRef = cur
		cur.acquire()
	case osc133PreExec:
		if bt.pending == nil {
			bt.pending = bt.openPending(cur, altScreen)
		}
		// No release-on-replace guard: a repeated pre-exec always trips
		// self-heal above and arrives with a fresh pending block.
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
			// Bare Enter at the prompt: nothing copyable; free the refs.
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

// SnapshotBlocks resolves every serializable block to SCREEN-space rows,
// dropping blocks whose essential refs no longer resolve (correct-or-absent).
// The pending block carries Pending so the client can re-arm it.
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
			// The end position is essential; drop rather than show a wrong row.
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
