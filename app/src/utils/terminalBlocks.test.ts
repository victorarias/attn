import { describe, expect, it } from 'vitest';
import {
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
