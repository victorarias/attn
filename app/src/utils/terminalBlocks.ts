// Command-block tracking built on OSC 133 markers.
//
// A block is one prompt → command → output cycle, stored as absolute buffer
// rows captured when the markers arrived. Rows can drift if the terminal
// trims scrollback, so each completed block keeps a text anchor (the command
// line's content at completion time); extraction re-anchors against the live
// buffer and refuses to extract rather than return wrong text.

import type { Osc133Marker } from './terminalOsc133';

export interface BlockPosition {
  row: number;
  col: number;
}

export interface TerminalBlock {
  id: number;
  promptRow: number;
  inputStart?: BlockPosition;
  outputStartRow?: number;
  // Exclusive: the row the cursor was on when the command finished (where the
  // next prompt renders).
  endRow?: number;
  command: string;
  exitCode?: number;
  anchorRow: number;
  anchorText: string;
}

export interface BlockRowAccess {
  totalRows(): number;
  rowText(bufferRow: number): string;
}

const MAX_BLOCKS = 200;
const ANCHOR_LENGTH = 64;
const REANCHOR_SCAN_ROWS = 64;

interface PendingBlock {
  id: number;
  promptRow: number;
  inputStart?: BlockPosition;
  outputStartRow?: number;
  command?: string;
}

export class TerminalBlockStore {
  private completed: TerminalBlock[] = [];
  private pending: PendingBlock | null = null;
  private nextId = 1;

  applyMarker(marker: Osc133Marker, position: BlockPosition, rowTextAt?: (row: number) => string): void {
    switch (marker.kind) {
      case 'prompt-start':
        this.pending = { id: this.nextId, promptRow: position.row };
        this.nextId += 1;
        return;
      case 'input-start':
        if (this.pending) this.pending.inputStart = position;
        return;
      case 'pre-exec':
        if (this.pending) {
          this.pending.outputStartRow = position.row;
          this.pending.command = marker.cmdline;
        }
        return;
      case 'command-end': {
        const pending = this.pending;
        this.pending = null;
        // A block without a pre-exec marker never ran a command (e.g. a bare
        // Enter at the prompt) — nothing copyable.
        if (!pending || pending.outputStartRow === undefined) return;
        const anchorRow = pending.inputStart?.row ?? pending.promptRow;
        this.completed.push({
          id: pending.id,
          promptRow: pending.promptRow,
          inputStart: pending.inputStart,
          outputStartRow: pending.outputStartRow,
          endRow: position.row,
          command: pending.command ?? '',
          exitCode: marker.exitCode,
          anchorRow,
          anchorText: (rowTextAt?.(anchorRow) ?? '').slice(0, ANCHOR_LENGTH),
        });
        if (this.completed.length > MAX_BLOCKS) {
          this.completed.splice(0, this.completed.length - MAX_BLOCKS);
        }
        return;
      }
      default:
        return;
    }
  }

  blocks(): readonly TerminalBlock[] {
    return this.completed;
  }

  hasBlocks(): boolean {
    return this.completed.length > 0;
  }

  blockAt(bufferRow: number): TerminalBlock | null {
    for (let i = this.completed.length - 1; i >= 0; i -= 1) {
      const block = this.completed[i];
      if (bufferRow >= block.promptRow && block.endRow !== undefined && bufferRow < block.endRow) {
        return block;
      }
    }
    return null;
  }

  blockById(id: number): TerminalBlock | null {
    return this.completed.find((block) => block.id === id) ?? null;
  }

  clear(): void {
    this.completed = [];
    this.pending = null;
  }
}

// Row offset between the block's recorded rows and the live buffer, found by
// matching the anchor text. null means the block's content is gone (trimmed
// or rewritten) and extraction must not proceed.
export function reanchorDelta(block: TerminalBlock, access: BlockRowAccess): number | null {
  if (!block.anchorText) return 0;
  const total = access.totalRows();
  const matches = (row: number) => (
    row >= 0 && row < total && access.rowText(row).slice(0, block.anchorText.length) === block.anchorText
  );
  if (matches(block.anchorRow)) return 0;
  for (let delta = 1; delta <= REANCHOR_SCAN_ROWS; delta += 1) {
    if (matches(block.anchorRow - delta)) return -delta;
    if (matches(block.anchorRow + delta)) return delta;
  }
  return null;
}

export interface ExtractedBlock {
  command: string;
  output: string;
}

export function extractBlock(block: TerminalBlock, access: BlockRowAccess): ExtractedBlock | null {
  if (block.outputStartRow === undefined || block.endRow === undefined) return null;
  const delta = reanchorDelta(block, access);
  if (delta === null) return null;
  const total = access.totalRows();
  const start = block.outputStartRow + delta;
  const end = Math.min(block.endRow + delta, total);
  const lines: string[] = [];
  for (let row = start; row < end; row += 1) {
    if (row < 0) continue;
    lines.push(access.rowText(row).replace(/\s+$/, ''));
  }
  while (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  return { command: block.command, output: lines.join('\n') };
}
