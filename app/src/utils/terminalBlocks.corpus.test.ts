// Proves the shared OSC 133 block-lifecycle corpus against the EXISTING
// client TerminalBlockStore. The corpus (internal/pty/testdata/) is the
// executable spec the Go worker block table (Phase 3a) is written against;
// this test is what makes it a spec rather than an opinion — any semantic the
// corpus claims must hold here first.
//
// The client store keeps its pending block private, so pending/nextId
// expectations are verified by flushing: sentinel markers complete the open
// block (or open a fresh one when none is pending), and the flushed block's
// fields expose what the pending state must have been — including the id the
// store allocates next, which the restore-seed contract relies on.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import type { Osc133Marker } from './terminalOsc133';
import { TerminalBlockStore, type TerminalBlock } from './terminalBlocks';

interface CorpusStep {
  marker: 'prompt-start' | 'input-start' | 'pre-exec' | 'command-end';
  row: number;
  col: number;
  cmdline?: string;
  exitCode?: number;
}

interface CorpusBlock {
  id: number;
  promptRow: number;
  inputRow?: number;
  inputCol?: number;
  outputStartRow?: number;
  endRow?: number;
  command?: string;
  exitCode?: number;
}

interface CorpusCase {
  name: string;
  steps?: CorpusStep[];
  generate?: { fullCycles: number; rowsPerCycle: number; commandPrefix: string };
  expect: {
    completed?: CorpusBlock[];
    completedCount?: number;
    firstId?: number;
    lastId?: number;
    firstCommand?: string;
    lastCommand?: string;
    firstPromptRow?: number;
    pending: CorpusBlock | null;
    nextId: number;
  };
}

const corpusPath = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../../internal/pty/testdata/osc133_block_corpus.json',
);
const corpus = JSON.parse(readFileSync(corpusPath, 'utf8')) as { cases: CorpusCase[] };

function toMarker(step: CorpusStep): Osc133Marker {
  switch (step.marker) {
    case 'prompt-start':
      return { kind: 'prompt-start' };
    case 'input-start':
      return { kind: 'input-start' };
    case 'pre-exec':
      return { kind: 'pre-exec', cmdline: step.cmdline };
    case 'command-end':
      return { kind: 'command-end', exitCode: step.exitCode };
  }
}

function expandSteps(c: CorpusCase): CorpusStep[] {
  if (c.steps) return c.steps;
  const g = c.generate;
  if (!g) throw new Error(`case ${c.name} has neither steps nor generate`);
  const steps: CorpusStep[] = [];
  for (let i = 0; i < g.fullCycles; i += 1) {
    const base = i * g.rowsPerCycle;
    steps.push({ marker: 'prompt-start', row: base, col: 0 });
    steps.push({ marker: 'pre-exec', row: base + 1, col: 0, cmdline: `${g.commandPrefix}${i}` });
    steps.push({ marker: 'command-end', row: base + 2, col: 0, exitCode: 0 });
  }
  return steps;
}

// Normalizes a store block to the corpus's client-independent shape (anchor
// fields are client-only and excluded by design).
function norm(block: TerminalBlock): CorpusBlock {
  const out: CorpusBlock = {
    id: block.id,
    promptRow: block.promptRow,
    outputStartRow: block.outputStartRow,
    endRow: block.endRow,
    command: block.command,
  };
  if (block.inputStart) {
    out.inputRow = block.inputStart.row;
    out.inputCol = block.inputStart.col;
  }
  if (block.exitCode !== undefined) out.exitCode = block.exitCode;
  return out;
}

const SENTINEL_ROW = 100000;
const SENTINEL_CMD = '__corpus_flush__';
// Mirrors the store's MAX_BLOCKS: a flush that completes a block at the cap
// evicts the oldest, so the count saturates instead of growing.
const CAP = 200;

describe('osc133 block corpus (executable spec for the Go worker block table)', () => {
  for (const c of corpus.cases) {
    it(c.name, () => {
      const store = new TerminalBlockStore();
      for (const step of expandSteps(c)) {
        store.applyMarker(toMarker(step), { row: step.row, col: step.col });
      }

      const blocks = store.blocks().map(norm);
      if (c.expect.completed) expect(blocks).toEqual(c.expect.completed);
      if (c.expect.completedCount !== undefined) expect(blocks).toHaveLength(c.expect.completedCount);
      if (c.expect.firstId !== undefined) expect(blocks[0]?.id).toBe(c.expect.firstId);
      if (c.expect.lastId !== undefined) expect(blocks.at(-1)?.id).toBe(c.expect.lastId);
      if (c.expect.firstCommand !== undefined) expect(blocks[0]?.command).toBe(c.expect.firstCommand);
      if (c.expect.lastCommand !== undefined) expect(blocks.at(-1)?.command).toBe(c.expect.lastCommand);
      if (c.expect.firstPromptRow !== undefined) expect(blocks[0]?.promptRow).toBe(c.expect.firstPromptRow);

      // Flush verification of pending + nextId (see file comment).
      const pending = c.expect.pending;
      const countBefore = store.blocks().length;
      if (pending && pending.outputStartRow !== undefined) {
        // Open block already has a running command: a single command-end
        // completes exactly it.
        store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: SENTINEL_ROW, col: 0 });
        const flushed = norm(store.blocks()[store.blocks().length - 1]);
        expect(store.blocks()).toHaveLength(Math.min(countBefore + 1, CAP));
        expect(flushed).toEqual({
          ...pending,
          command: pending.command ?? '',
          endRow: SENTINEL_ROW,
          exitCode: 0,
        });
      } else {
        // No open command: a sentinel pre-exec either attaches to the
        // commandless pending (exposing its id/rows) or opens a fresh block
        // whose id IS the store's nextId.
        store.applyMarker({ kind: 'pre-exec', cmdline: SENTINEL_CMD }, { row: SENTINEL_ROW, col: 0 });
        store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: SENTINEL_ROW + 1, col: 0 });
        const flushed = norm(store.blocks()[store.blocks().length - 1]);
        expect(store.blocks()).toHaveLength(Math.min(countBefore + 1, CAP));
        expect(flushed.command).toBe(SENTINEL_CMD);
        if (pending) {
          expect(flushed.id).toBe(pending.id);
          expect(flushed.promptRow).toBe(pending.promptRow);
          expect(flushed.inputRow).toBe(pending.inputRow);
          expect(flushed.inputCol).toBe(pending.inputCol);
        } else {
          expect(flushed.id).toBe(c.expect.nextId);
          expect(flushed.promptRow).toBe(SENTINEL_ROW);
        }
      }

      // Flushing a pending block does not consume a fresh id, so nextId is
      // still observable afterwards via a brand-new block.
      if (pending) {
        store.applyMarker({ kind: 'pre-exec', cmdline: SENTINEL_CMD }, { row: SENTINEL_ROW + 2, col: 0 });
        store.applyMarker({ kind: 'command-end', exitCode: 0 }, { row: SENTINEL_ROW + 3, col: 0 });
        const fresh = norm(store.blocks()[store.blocks().length - 1]);
        expect(fresh.id).toBe(c.expect.nextId);
      }
    });
  }
});
