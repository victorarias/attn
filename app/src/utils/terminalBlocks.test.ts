import { describe, expect, it } from 'vitest';
import {
  blockViewportSpan,
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

  it('reports the completed block and why it closed', () => {
    const store = new TerminalBlockStore();
    expect(store.applyMarker({ kind: 'prompt-start' }, { row: 0, col: 0 })).toEqual({ completed: null, reason: null });
    expect(store.applyMarker({ kind: 'pre-exec', cmdline: 'echo hi' }, { row: 1, col: 0 })).toEqual({ completed: null, reason: null });

    // Normal close via command-end.
    const ended = store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: 2, col: 0 });
    expect(ended.reason).toBe('command-end');
    expect(ended.completed?.command).toBe('echo hi');
    expect(ended.completed?.endRow).toBe(2);

    // Self-heal close: a new prompt arrives while the previous command is open.
    store.applyMarker({ kind: 'prompt-start' }, { row: 3, col: 0 });
    store.applyMarker({ kind: 'pre-exec', cmdline: 'sleep' }, { row: 4, col: 0 });
    const healed = store.applyMarker({ kind: 'prompt-start' }, { row: 9, col: 0 });
    expect(healed.reason).toBe('self-heal');
    expect(healed.completed?.command).toBe('sleep');
    expect(healed.completed?.endRow).toBe(9);
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
