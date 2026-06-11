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

// Why a block closed. 'command-end' is the normal OSC 133;D path; 'self-heal'
// means a new prompt context arrived while the previous command was still open
// (its command-end was lost), so we closed it at the recovered prompt row.
export type BlockCloseReason = 'command-end' | 'self-heal';

export interface ApplyMarkerResult {
  // The block this marker just completed, if any — for diagnostics and so the
  // caller can react to a completion without re-scanning blocks().
  completed: TerminalBlock | null;
  reason: BlockCloseReason | null;
}

const NO_COMPLETION: ApplyMarkerResult = { completed: null, reason: null };

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

  applyMarker(marker: Osc133Marker, position: BlockPosition, rowTextAt?: (row: number) => string): ApplyMarkerResult {
    // Self-heal against a lost command-end. If a command already ran in the
    // current block (outputStartRow is set) and a marker that begins a NEW
    // command context arrives, fish's `OSC 133;D` for the previous command
    // never reached us (e.g. a PTY output chunk was dropped). Close the open
    // block here so two commands don't silently merge into one.
    let healed: TerminalBlock | null = null;
    if (
      this.pending?.outputStartRow !== undefined
      && (marker.kind === 'prompt-start' || marker.kind === 'input-start' || marker.kind === 'pre-exec')
    ) {
      healed = this.complete(this.pending, position.row, undefined, rowTextAt);
      this.pending = null;
    }

    switch (marker.kind) {
      case 'prompt-start':
        this.pending = { id: this.nextId, promptRow: position.row };
        this.nextId += 1;
        break;
      case 'input-start':
        // A surviving input-start with no prompt-start (its prompt-start was
        // lost) still anchors a block, just at the input row.
        if (!this.pending) this.pending = this.openPending(position.row);
        this.pending.inputStart = position;
        break;
      case 'pre-exec':
        if (!this.pending) this.pending = this.openPending(position.row);
        this.pending.outputStartRow = position.row;
        this.pending.command = marker.cmdline;
        break;
      case 'command-end': {
        const pending = this.pending;
        this.pending = null;
        // A block without a pre-exec marker never ran a command (e.g. a bare
        // Enter at the prompt) — nothing copyable.
        if (pending && pending.outputStartRow !== undefined) {
          const completed = this.complete(pending, position.row, marker.exitCode, rowTextAt);
          if (completed) return { completed, reason: 'command-end' };
        }
        break;
      }
      default:
        break;
    }
    return healed ? { completed: healed, reason: 'self-heal' } : NO_COMPLETION;
  }

  private openPending(promptRow: number): PendingBlock {
    const pending = { id: this.nextId, promptRow };
    this.nextId += 1;
    return pending;
  }

  // Pushes a completed block from a pending one with a known outputStartRow.
  private complete(
    pending: PendingBlock,
    endRow: number,
    exitCode: number | undefined,
    rowTextAt?: (row: number) => string,
  ): TerminalBlock | null {
    if (pending.outputStartRow === undefined) return null;
    const anchorRow = pending.inputStart?.row ?? pending.promptRow;
    const block: TerminalBlock = {
      id: pending.id,
      promptRow: pending.promptRow,
      inputStart: pending.inputStart,
      outputStartRow: pending.outputStartRow,
      endRow,
      command: pending.command ?? '',
      exitCode,
      anchorRow,
      anchorText: (rowTextAt?.(anchorRow) ?? '').slice(0, ANCHOR_LENGTH),
    };
    this.completed.push(block);
    if (this.completed.length > MAX_BLOCKS) {
      this.completed.splice(0, this.completed.length - MAX_BLOCKS);
    }
    return block;
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

// A completed block's position relative to the current viewport, in viewport
// rows. startRow may be negative and endRow may exceed the last viewport row
// when the block extends past the visible area — renderers and diagnostics
// must share this one mapping so what gets logged is what gets drawn.
export interface BlockViewportSpan {
  startRow: number; // viewport row of the block's prompt row
  endRow: number;   // viewport row of the block's last row (inclusive)
  visible: boolean; // intersects the viewport at all
  spansViewport: boolean; // covers every visible row (both edges off-screen or at the borders)
}

export function blockViewportSpan(
  block: TerminalBlock,
  firstViewportBufferRow: number,
  viewportRows: number,
): BlockViewportSpan | null {
  if (block.endRow === undefined) return null;
  const startRow = block.promptRow - firstViewportBufferRow;
  const endRow = block.endRow - 1 - firstViewportBufferRow;
  return {
    startRow,
    endRow,
    visible: endRow >= 0 && startRow < viewportRows,
    spansViewport: startRow <= 0 && endRow >= viewportRows - 1,
  };
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
