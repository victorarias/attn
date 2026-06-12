import { describe, expect, it } from 'vitest';
import {
  blockViewportSpan,
  blockViewportSpanAnchored,
  extractBlock,
  reanchorDelta,
  TerminalBlockStore,
  type BlockRowAccess,
} from './terminalBlocks';

function access(rows: string[]): BlockRowAccess {
  return {
    totalRows: () => rows.length,
    rowText: (row) => rows[row] ?? '',
  };
}

function completedBlock(rows: string[]) {
  const store = new TerminalBlockStore();
  const rowTextAt = (row: number) => rows[row] ?? '';
  store.applyMarker({ kind: 'prompt-start' }, { row: 0, col: 0 }, rowTextAt);
  store.applyMarker({ kind: 'input-start' }, { row: 0, col: 8 }, rowTextAt);
  store.applyMarker({ kind: 'pre-exec', cmdline: 'echo hello' }, { row: 1, col: 0 }, rowTextAt);
  store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: 3, col: 0 }, rowTextAt);
  return store;
}

const ROWS = ['prompt> echo hello', 'hello', 'world  ', '', 'prompt> '];

describe('TerminalBlockStore', () => {
  it('completes a block across the marker lifecycle', () => {
    const store = completedBlock(ROWS);
    expect(store.blocks()).toHaveLength(1);
    const block = store.blocks()[0];
    expect(block.command).toBe('echo hello');
    expect(block.exitCode).toBe(0);
    expect(block.promptRow).toBe(0);
    expect(block.outputStartRow).toBe(1);
    expect(block.endRow).toBe(3);
    expect(block.anchorText).toBe('prompt> echo hello');
  });

  it('hit-tests rows to the containing block', () => {
    const store = completedBlock(ROWS);
    const block = store.blocks()[0];
    expect(store.blockAt(0)).toBe(block);
    expect(store.blockAt(2)).toBe(block);
    expect(store.blockAt(3)).toBeNull();
  });

  it('ignores a command-end without a pre-exec (bare Enter at the prompt)', () => {
    const store = new TerminalBlockStore();
    store.applyMarker({ kind: 'prompt-start' }, { row: 0, col: 0 });
    store.applyMarker({ kind: 'input-start' }, { row: 0, col: 8 });
    store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: 1, col: 0 });
    expect(store.blocks()).toHaveLength(0);
  });

  it('does not merge two commands when a command-end is lost but the next prompt-start survives', () => {
    const store = new TerminalBlockStore();
    // make install
    store.applyMarker({ kind: 'prompt-start' }, { row: 0, col: 0 });
    store.applyMarker({ kind: 'pre-exec', cmdline: 'make install' }, { row: 1, col: 0 });
    // <-- make install's command-end (OSC 133;D) was lost (dropped chunk)
    // echo 1 (its prompt-start survived)
    store.applyMarker({ kind: 'prompt-start' }, { row: 40, col: 0 });
    store.applyMarker({ kind: 'pre-exec', cmdline: 'echo 1' }, { row: 41, col: 0 });
    store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: 43, col: 0 });

    const blocks = store.blocks();
    expect(blocks).toHaveLength(2);
    expect(blocks[0].command).toBe('make install');
    expect(blocks[0].promptRow).toBe(0);
    expect(blocks[0].endRow).toBe(40); // closed at the recovered prompt row
    expect(blocks[1].command).toBe('echo 1');
    expect(blocks[1].promptRow).toBe(40);
    expect(blocks[1].endRow).toBe(43);
  });

  it('does not merge when both the command-end AND the next prompt-start are lost', () => {
    // The make-install-then-echo-1 bug: fish emits `OSC 133;D` + `OSC 133;A`
    // back-to-back at a new prompt, so a single dropped chunk loses both. Only
    // echo 1's input-start/pre-exec/command-end survive.
    const store = new TerminalBlockStore();
    store.applyMarker({ kind: 'prompt-start' }, { row: 0, col: 0 });
    store.applyMarker({ kind: 'pre-exec', cmdline: 'make install' }, { row: 1, col: 0 });
    // <-- D (make install) + A (echo 1) both lost
    store.applyMarker({ kind: 'input-start' }, { row: 41, col: 2 });
    store.applyMarker({ kind: 'pre-exec', cmdline: 'echo 1' }, { row: 42, col: 0 });
    store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: 44, col: 0 });

    const blocks = store.blocks();
    expect(blocks).toHaveLength(2);
    expect(blocks[0].command).toBe('make install');
    expect(blocks[0].promptRow).toBe(0);
    expect(blocks[0].endRow).toBe(41); // closed when the orphan input-start arrived
    expect(blocks[1].command).toBe('echo 1');
    expect(blocks[1].promptRow).toBe(41);
    expect(blocks[1].endRow).toBe(44);
  });

  it('caps stored blocks and keeps the newest', () => {
    const store = new TerminalBlockStore();
    for (let i = 0; i < 250; i += 1) {
      const base = i * 3;
      store.applyMarker({ kind: 'prompt-start' }, { row: base, col: 0 });
      store.applyMarker({ kind: 'pre-exec', cmdline: `cmd-${i}` }, { row: base + 1, col: 0 });
      store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: base + 2, col: 0 });
    }
    expect(store.blocks()).toHaveLength(200);
    expect(store.blocks()[0].command).toBe('cmd-50');
    expect(store.blocks()[199].command).toBe('cmd-249');
  });
});

describe('blockViewportSpan', () => {
  const block = (promptRow: number, endRow: number) => ({
    id: 1, promptRow, endRow, command: 'x', anchorRow: promptRow, anchorText: '',
  });

  it('maps a fully visible block to viewport rows', () => {
    // Buffer rows 3..7 (endRow exclusive) with the viewport starting at buffer row 0.
    expect(blockViewportSpan(block(3, 8), 0, 24)).toEqual({
      startRow: 3, endRow: 7, visible: true, spansViewport: false,
    });
  });

  it('reports an over-tall block as spanning the viewport (the make-in-a-small-pane case)', () => {
    // 204-row block, 27-row viewport scrolled to its bottom: top is far off-screen.
    expect(blockViewportSpan(block(3, 207), 183, 27)).toEqual({
      startRow: -180, endRow: 23, visible: true, spansViewport: false,
    });
    // Scrolled into the middle: both edges off-screen → spans the whole viewport.
    expect(blockViewportSpan(block(3, 207), 100, 27)).toEqual({
      startRow: -97, endRow: 106, visible: true, spansViewport: true,
    });
  });

  it('reports a scrolled-away block as not visible', () => {
    expect(blockViewportSpan(block(3, 8), 50, 24)?.visible).toBe(false);
  });

  it('returns null for an incomplete block', () => {
    expect(blockViewportSpan({ id: 1, promptRow: 3, command: 'x', anchorRow: 3, anchorText: '' }, 0, 24)).toBeNull();
  });
});

describe('extractBlock', () => {
  it('extracts command and trailing-trimmed output rows', () => {
    const store = completedBlock(ROWS);
    expect(extractBlock(store.blocks()[0], access(ROWS))).toEqual({
      command: 'echo hello',
      output: 'hello\nworld',
    });
  });

  it('re-anchors when the buffer shifted under the block', () => {
    const store = completedBlock(ROWS);
    const shifted = ['noise-a', 'noise-b', ...ROWS];
    const block = store.blocks()[0];
    expect(reanchorDelta(block, access(shifted))).toBe(2);
    expect(extractBlock(block, access(shifted))).toEqual({
      command: 'echo hello',
      output: 'hello\nworld',
    });
  });

  it('refuses extraction when the anchor is gone', () => {
    const store = completedBlock(ROWS);
    const replaced = Array.from({ length: 200 }, (_, i) => `unrelated-${i}`);
    expect(extractBlock(store.blocks()[0], access(replaced))).toBeNull();
  });
});

// Builds a completed block at explicit rows with a chosen anchor line, so a
// store can be populated with blocks whose stored rows are deliberately stale
// relative to a given access buffer (the mixed pre/post-reflow case).
function blockAt(
  store: TerminalBlockStore,
  cmd: string,
  promptRow: number,
  outputStartRow: number,
  endRow: number,
  rowText: (row: number) => string,
) {
  store.applyMarker({ kind: 'prompt-start' }, { row: promptRow, col: 0 }, rowText);
  store.applyMarker({ kind: 'input-start' }, { row: promptRow, col: 0 }, rowText);
  store.applyMarker({ kind: 'pre-exec', cmdline: cmd }, { row: outputStartRow, col: 0 }, rowText);
  store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: endRow, col: 0 }, rowText);
}

describe('reanchorDelta scan window', () => {
  it('finds an anchor moved 200 rows with the wide resize window but not the default window', () => {
    const store = new TerminalBlockStore();
    const baseRows = ['prompt> make', 'building'];
    blockAt(store, 'make', 0, 1, 2, (row) => baseRows[row] ?? '');
    const block = store.blocks()[0];
    // Anchor 'prompt> make' now lives 200 rows later in the live buffer.
    const moved = [
      ...Array.from({ length: 200 }, (_, i) => `older-${i}`),
      'prompt> make',
      'building',
    ];
    expect(reanchorDelta(block, access(moved), 512)).toBe(200);
    expect(reanchorDelta(block, access(moved))).toBeNull(); // default 64-row window
  });
});

describe('TerminalBlockStore.reanchorOnResize', () => {
  it('remaps every block by a uniform height-only shift and keeps them', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> a', 'out-a', 'prompt> b', 'out-b'];
    blockAt(store, 'a', 0, 1, 2, (row) => liveRows[row] ?? '');
    blockAt(store, 'b', 2, 3, 4, (row) => liveRows[row] ?? '');
    // Resize promoted 2 rows out of scrollback: the same content sits +2 rows down.
    const shifted = ['x', 'y', ...liveRows];
    expect(store.reanchorOnResize(access(shifted))).toBe('ok');
    const [a, b] = store.blocks();
    expect(a.promptRow).toBe(2);
    expect(a.outputStartRow).toBe(3);
    expect(a.endRow).toBe(4);
    expect(a.anchorRow).toBe(2);
    expect(b.promptRow).toBe(4);
    expect(b.endRow).toBe(6);
  });

  it('drops a block whose anchor is gone but keeps the survivor', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> keep', 'out-keep', 'prompt> gone', 'out-gone'];
    blockAt(store, 'keep', 0, 1, 2, (row) => liveRows[row] ?? '');
    blockAt(store, 'gone', 2, 3, 4, (row) => liveRows[row] ?? '');
    // Only the first block's anchor survives the reflow.
    const after = ['prompt> keep', 'out-keep'];
    expect(store.reanchorOnResize(access(after))).toBe('ok');
    expect(store.blocks()).toHaveLength(1);
    expect(store.blocks()[0].command).toBe('keep');
    expect(store.blocks()[0].promptRow).toBe(0);
  });

  it('empties the store and reports all-stale when every anchor is gone', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> a', 'out-a', 'prompt> b', 'out-b'];
    blockAt(store, 'a', 0, 1, 2, (row) => liveRows[row] ?? '');
    blockAt(store, 'b', 2, 3, 4, (row) => liveRows[row] ?? '');
    const wiped = Array.from({ length: 50 }, (_, i) => `unrelated-${i}`);
    expect(store.reanchorOnResize(access(wiped))).toBe('all-stale');
    expect(store.blocks()).toHaveLength(0);
  });

  it('is a no-op confirmation when the no-reflow path kept rows stable (delta 0)', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> a', 'out-a', 'prompt> b', 'out-b'];
    blockAt(store, 'a', 0, 1, 2, (row) => liveRows[row] ?? '');
    blockAt(store, 'b', 2, 3, 4, (row) => liveRows[row] ?? '');
    expect(store.reanchorOnResize(access(liveRows))).toBe('ok');
    const [a, b] = store.blocks();
    expect(a.promptRow).toBe(0);
    expect(b.promptRow).toBe(2);
  });

  it('uses the wide window so a large height shift remaps instead of dropping', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> tall', 'out'];
    blockAt(store, 'tall', 0, 1, 2, (row) => liveRows[row] ?? '');
    const shifted = [...Array.from({ length: 200 }, (_, i) => `s-${i}`), 'prompt> tall', 'out'];
    expect(store.reanchorOnResize(access(shifted))).toBe('ok');
    expect(store.blocks()[0].promptRow).toBe(200);
    expect(store.blocks()[0].endRow).toBe(202);
  });

  it('matches anchors whose rows are clipped to a narrower width (width-tolerant prefix)', () => {
    // rowText() returns at most the current pane width, so a full-width
    // 64-char anchor can only be compared on the overlapping prefix — without
    // this, any width mismatch between capture and lookup drops every block.
    const store = new TerminalBlockStore();
    const wide = [
      'prompt> seq 1 200; echo RESIZE_TOKEN_LONG_ENOUGH_TO_EXCEED_NARROW_WIDTH',
      'output line',
    ];
    blockAt(store, 'seq', 0, 1, 2, (row) => wide[row] ?? '');
    // Same rows, clipped to a 30-column pane.
    const clipped = wide.map((line) => line.slice(0, 30));
    expect(store.reanchorOnResize(access(clipped))).toBe('ok');
    expect(store.blocks()).toHaveLength(1);
    expect(store.blocks()[0].promptRow).toBe(0);
  });

  it('refuses tiny anchor overlaps that would match almost anything', () => {
    const store = new TerminalBlockStore();
    const rows = ['prompt> unique-command-here', 'out'];
    blockAt(store, 'unique', 0, 1, 2, (row) => rows[row] ?? '');
    // A 3-column pane: 3-char overlap is below the minimum — refuse, don't
    // false-match the block onto any row starting with "pro".
    const tiny = ['pro', 'out'.slice(0, 3)];
    expect(store.reanchorOnResize(access(tiny))).toBe('all-stale');
    expect(store.blocks()).toHaveLength(0);
  });

  it('rebuilds in the new coordinate space after a clear (coherence)', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> old', 'out'];
    blockAt(store, 'old', 0, 1, 2, (row) => liveRows[row] ?? '');
    store.clear();
    expect(store.blocks()).toHaveLength(0);
    const fresh = ['noise', 'noise', 'prompt> new', 'out-new'];
    blockAt(store, 'new', 2, 3, 4, (row) => fresh[row] ?? '');
    expect(store.blocks()).toHaveLength(1);
    expect(store.blocks()[0].promptRow).toBe(2);
    expect(store.blocks()[0].command).toBe('new');
  });
});

describe('TerminalBlockStore.blockAtAnchored', () => {
  it('finds the containing block after a +N shift', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> a', 'out-a', 'tail'];
    blockAt(store, 'a', 0, 1, 2, (row) => liveRows[row] ?? '');
    const shifted = ['x', 'y', 'prompt> a', 'out-a', 'tail'];
    // Block spans live buffer rows 2..3 (endRow 4 exclusive) after the +2 shift.
    expect(store.blockAtAnchored(2, access(shifted))?.command).toBe('a');
    expect(store.blockAtAnchored(3, access(shifted))?.command).toBe('a');
    // Stale stored rows 0..1 must NOT match against the shifted buffer.
    expect(store.blockAtAnchored(0, access(shifted))).toBeNull();
  });

  it('returns null when the anchor is gone', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> a', 'out-a'];
    blockAt(store, 'a', 0, 1, 2, (row) => liveRows[row] ?? '');
    const wiped = Array.from({ length: 50 }, (_, i) => `unrelated-${i}`);
    expect(store.blockAtAnchored(0, access(wiped))).toBeNull();
    expect(store.blockAtAnchored(1, access(wiped))).toBeNull();
  });
});

describe('blockViewportSpanAnchored', () => {
  it('returns the correctly-shifted span after a buffer shift', () => {
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> a', 'out-a'];
    blockAt(store, 'a', 0, 1, 2, (row) => liveRows[row] ?? '');
    const block = store.blocks()[0];
    const shifted = ['x', 'y', 'prompt> a', 'out-a'];
    // Live block now at rows 2..3; viewport starts at live row 2.
    expect(blockViewportSpanAnchored(block, access(shifted), 2, 24)).toEqual({
      startRow: 0, endRow: 1, visible: true, spansViewport: false,
    });
  });

  it('returns null (not a wrong box) when the anchor is gone — the "completely off" regression', () => {
    // Live-evidence shape: a tall `make` block stored with endRow far beyond the
    // post-reflow buffer. With its anchor gone, the overlay must draw nothing.
    const store = new TerminalBlockStore();
    const liveRows = ['prompt> make', 'building'];
    blockAt(store, 'make', 0, 3, 720, (row) => liveRows[row] ?? '');
    const block = store.blocks()[0];
    expect(block.endRow).toBe(720);
    const reflowed = Array.from({ length: 394 }, (_, i) => `reflowed-${i}`);
    expect(blockViewportSpanAnchored(block, access(reflowed), 341, 53)).toBeNull();
  });
});
