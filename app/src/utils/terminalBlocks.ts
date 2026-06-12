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
// Per-click / per-extract re-anchor window. Drift between a stored row and the
// live buffer on these hot paths comes from scrollback trimming, which is small.
export const REANCHOR_SCAN_ROWS = 64;
// Re-anchor window used after a HEIGHT-only resize (width changes reflow the
// buffer non-uniformly and clear the store instead — see GhosttyTerminal's
// resizeModelAndReanchor). Height changes shift rows uniformly (rows move
// between scrollback and screen, plus any trim), which can exceed the per-click
// window; 512 covers it with margin. An anchor outside it tombstones the block.
export const RESIZE_REANCHOR_SCAN_ROWS = 512;

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
    // Self-heal against a lost command-end. If a command already ran in the
    // current block (outputStartRow is set) and a marker that begins a NEW
    // command context arrives, fish's `OSC 133;D` for the previous command
    // never reached us (e.g. a PTY output chunk was dropped). Close the open
    // block here so two commands don't silently merge into one.
    if (
      this.pending?.outputStartRow !== undefined
      && (marker.kind === 'prompt-start' || marker.kind === 'input-start' || marker.kind === 'pre-exec')
    ) {
      this.complete(this.pending, position.row, undefined, rowTextAt);
      this.pending = null;
    }

    switch (marker.kind) {
      case 'prompt-start':
        this.pending = { id: this.nextId, promptRow: position.row };
        this.nextId += 1;
        return;
      case 'input-start':
        // A surviving input-start with no prompt-start (its prompt-start was
        // lost) still anchors a block, just at the input row.
        if (!this.pending) this.pending = this.openPending(position.row);
        this.pending.inputStart = position;
        return;
      case 'pre-exec':
        if (!this.pending) this.pending = this.openPending(position.row);
        this.pending.outputStartRow = position.row;
        this.pending.command = marker.cmdline;
        return;
      case 'command-end': {
        const pending = this.pending;
        this.pending = null;
        // A block without a pre-exec marker never ran a command (e.g. a bare
        // Enter at the prompt) — nothing copyable.
        if (pending && pending.outputStartRow !== undefined) {
          this.complete(pending, position.row, marker.exitCode, rowTextAt);
        }
        return;
      }
      default:
        return;
    }
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
  ): void {
    if (pending.outputStartRow === undefined) return;
    const anchorRow = pending.inputStart?.row ?? pending.promptRow;
    this.completed.push({
      id: pending.id,
      promptRow: pending.promptRow,
      inputStart: pending.inputStart,
      outputStartRow: pending.outputStartRow,
      endRow,
      command: pending.command ?? '',
      exitCode,
      anchorRow,
      anchorText: (rowTextAt?.(anchorRow) ?? '').slice(0, ANCHOR_LENGTH),
    });
    if (this.completed.length > MAX_BLOCKS) {
      this.completed.splice(0, this.completed.length - MAX_BLOCKS);
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

  // Reflow-aware hit-test: re-anchor each block against the live buffer before
  // range-checking, so a stale stored row never matches (or mismatches) the
  // wrong buffer row. Blocks whose anchor is gone are skipped (never matched).
  blockAtAnchored(bufferRow: number, access: BlockRowAccess): TerminalBlock | null {
    for (let i = this.completed.length - 1; i >= 0; i -= 1) {
      const block = this.completed[i];
      if (block.endRow === undefined) continue;
      const delta = reanchorDelta(block, access);
      if (delta === null) continue;
      if (bufferRow >= block.promptRow + delta && bufferRow < block.endRow + delta) {
        return block;
      }
    }
    return null;
  }

  blockById(id: number): TerminalBlock | null {
    return this.completed.find((block) => block.id === id) ?? null;
  }

  // Re-anchor every completed block against the live buffer after a resize.
  // A block whose anchor matches at a non-zero delta is shifted in place so its
  // stored rows track the live buffer again; a block whose anchor is gone is
  // dropped (its content is unrecoverable). Returns 'all-stale' when the store
  // held blocks but every one was dropped, so the caller can clear the
  // selection; otherwise 'ok'.
  reanchorOnResize(
    access: BlockRowAccess,
    scanRows: number = RESIZE_REANCHOR_SCAN_ROWS,
  ): 'ok' | 'all-stale' {
    const hadBlocks = this.completed.length > 0;
    if (!hadBlocks) return 'ok';
    const survivors: TerminalBlock[] = [];
    for (const block of this.completed) {
      const delta = reanchorDelta(block, access, scanRows);
      if (delta === null) continue;
      if (delta !== 0) {
        block.promptRow += delta;
        block.anchorRow += delta;
        if (block.outputStartRow !== undefined) block.outputStartRow += delta;
        if (block.endRow !== undefined) block.endRow += delta;
        if (block.inputStart) block.inputStart = { ...block.inputStart, row: block.inputStart.row + delta };
      }
      survivors.push(block);
    }
    this.completed = survivors;
    return survivors.length === 0 && hadBlocks ? 'all-stale' : 'ok';
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

// Minimum characters an anchor comparison must cover to count as a match. A
// narrower pane clips rowText to the visible width, so the comparison runs on
// the overlapping prefix — but a tiny overlap (e.g. a 4-col pane) would match
// almost anything, so short overlaps refuse instead.
const MIN_ANCHOR_OVERLAP = 8;

// Width-tolerant anchor comparison. The anchor was captured at the pane width
// of completion time; the live row may be clipped to a narrower width (or the
// anchor itself may be the shorter one after re-widening). Compare the
// overlapping prefix, requiring MIN_ANCHOR_OVERLAP unless the anchor is
// genuinely shorter than that (a short command line is fully compared).
function anchorMatches(anchorText: string, rowText: string): boolean {
  const overlap = Math.min(anchorText.length, rowText.length);
  if (overlap < Math.min(anchorText.length, MIN_ANCHOR_OVERLAP)) return false;
  return rowText.slice(0, overlap) === anchorText.slice(0, overlap);
}

// Row offset between the block's recorded rows and the live buffer, found by
// matching the anchor text. null means the block's content is gone (trimmed
// or rewritten) and extraction must not proceed.
export function reanchorDelta(
  block: TerminalBlock,
  access: BlockRowAccess,
  scanRows: number = REANCHOR_SCAN_ROWS,
): number | null {
  if (!block.anchorText) return 0;
  const total = access.totalRows();
  const matches = (row: number) => (
    row >= 0 && row < total && anchorMatches(block.anchorText, access.rowText(row))
  );
  if (matches(block.anchorRow)) return 0;
  for (let delta = 1; delta <= scanRows; delta += 1) {
    if (matches(block.anchorRow - delta)) return -delta;
    if (matches(block.anchorRow + delta)) return delta;
  }
  return null;
}

// Reflow-aware viewport span: re-anchors the block against the live buffer
// (default per-click window) before mapping to viewport rows. Returns null when
// the block's anchor is gone, so the overlay clears the selection rather than
// drawing a box at stale coordinates. firstViewportBufferRow must be computed
// from the SAME live scrollback the access reads, so the delta and the mapping
// agree.
export function blockViewportSpanAnchored(
  block: TerminalBlock,
  access: BlockRowAccess,
  firstViewportBufferRow: number,
  viewportRows: number,
): BlockViewportSpan | null {
  if (block.endRow === undefined) return null;
  const delta = reanchorDelta(block, access);
  if (delta === null) return null;
  return blockViewportSpan(
    { ...block, promptRow: block.promptRow + delta, endRow: block.endRow + delta },
    firstViewportBufferRow,
    viewportRows,
  );
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
