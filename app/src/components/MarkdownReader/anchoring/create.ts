/**
 * createAnchor — build an AnchorRecord from a range in a block's rendered
 * text. Pure; the DOM-selection → (blockId, start, end) mapping is the UI
 * layer's job (PR5).
 */

import { extractBlockTexts, ownerBlockFor } from './extractBlocks';
import { fnv1a32 } from './hash';
import type { AnchorRecord, BlockText } from './types';

/** Context window size for prefix/suffix (chars of rendered text). */
export const CONTEXT_CHARS = 32;

/**
 * Build a record from already-extracted blocks. Internal building block for
 * `createAnchor` and rebase re-baselining (which must not fuzz-compound:
 * every rebased record is rebuilt from scratch against the new content).
 */
export function buildAnchor(
  blocks: BlockText[],
  contentHash: string,
  blockId: string,
  start: number,
  end: number,
): AnchorRecord | null {
  if (!blocks.some((b) => b.blockId === blockId)) {
    return null;
  }
  const owner = ownerBlockFor(blocks, blockId, start, end);
  const { block } = owner;
  if (
    owner.start < 0 ||
    owner.end > block.text.length ||
    owner.end <= owner.start
  ) {
    return null;
  }
  const exact = block.text.slice(owner.start, owner.end);
  if (exact.trim() === '') {
    return null;
  }
  return {
    blockId: block.blockId,
    startLine: block.startLine,
    endLine: block.endLine,
    exact,
    prefix: block.text.slice(Math.max(0, owner.start - CONTEXT_CHARS), owner.start),
    suffix: block.text.slice(owner.end, owner.end + CONTEXT_CHARS),
    start: owner.start,
    end: owner.end,
    contentHash,
  };
}

/**
 * Create an anchor for `[start, end)` of `blockId`'s rendered text in
 * `content`. Returns null when the block doesn't exist, the range is out of
 * bounds, or the slice is empty/whitespace-only. The owning block is the
 * deepest stamped element containing the range (a range given against a `ul`
 * but contained in one `li` is re-attributed to the `li`); a range that only
 * exists spanning two sibling stamped blocks cannot be expressed and yields
 * null by the bounds check (single-block contract).
 *
 * `blocks` (optional) must be `extractBlockTexts(content)` — pass it when the
 * caller already ran the pipeline for this content.
 */
export function createAnchor(
  content: string,
  blockId: string,
  start: number,
  end: number,
  blocks?: BlockText[],
): AnchorRecord | null {
  return buildAnchor(blocks ?? extractBlockTexts(content), fnv1a32(content), blockId, start, end);
}
