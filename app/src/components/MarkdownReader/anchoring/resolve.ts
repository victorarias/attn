/**
 * resolveAnchor — the hash-keyed re-anchor state machine's exact path.
 *
 * Hash unchanged (reopen, restart, re-render): stored offsets are exact by
 * construction — zero heuristics, zero search. Hash changed: this is rebase
 * territory; `resolveAnchor` delegates internally so misuse can't paint the
 * wrong text, but callers that need the re-baselined record to PERSIST must
 * use `resolveOrRebase` (the live-reload API), which distinguishes
 * `exact` from `rebased` and hands back the new record.
 */

import { extractBlockTexts } from './extractBlocks';
import { fnv1a32 } from './hash';
import { rebaseAnchor } from './rebase';
import type {
  AnchorRecord,
  BlockText,
  OrphanReason,
  ResolveOrRebaseResult,
  ResolveResult,
} from './types';

/**
 * Fast path only: verify the stored coordinates against `content` when the
 * hash matches. Contract-violation fallthrough (hash matched but block
 * missing or slice ≠ exact — should be impossible; means a pipeline change)
 * and hash-changed content both delegate to `rebaseAnchor` and surface its
 * coordinates as `exact`; the caller repaints correctly either way, but the
 * re-baselined record is NOT returned here — use `resolveOrRebase` when
 * persistence matters.
 */
export function resolveAnchor(
  content: string,
  anchor: AnchorRecord,
  blocks?: BlockText[],
): ResolveResult {
  const result = resolveOrRebase(content, anchor, blocks);
  if (result.state === 'orphan') {
    return result;
  }
  return { state: 'exact', blockId: result.blockId, start: result.start, end: result.end };
}

/**
 * The full state machine, as used by the live-reload path. On `rebased` the
 * caller must persist `anchor` (the re-baselined record) so fuzz never
 * compounds across edits.
 *
 * `blocks` (optional) must be `extractBlockTexts(content)` — callers resolving
 * many anchors against the same content should extract once and pass it in;
 * without it every call re-runs the full pipeline.
 */
export function resolveOrRebase(
  content: string,
  anchor: AnchorRecord,
  blocks?: BlockText[],
): ResolveOrRebaseResult {
  const hash = fnv1a32(content);
  const allBlocks = blocks ?? extractBlockTexts(content);

  if (hash === anchor.contentHash) {
    const block = allBlocks.find((b) => b.blockId === anchor.blockId);
    if (block && block.text.slice(anchor.start, anchor.end) === anchor.exact) {
      return {
        state: 'exact',
        blockId: anchor.blockId,
        start: anchor.start,
        end: anchor.end,
        anchor,
      };
    }
    // Hash contract violated (pipeline drift?) — do not lie; try recovery.
    console.warn(
      '[md-anchoring] hash matched but coordinates are stale — pipeline change?',
      anchor.blockId,
      anchor.exact,
    );
    const violation: OrphanReason = block ? 'offset-mismatch' : 'block-missing';
    const recovered = rebaseAnchor(anchor, content, allBlocks);
    if (recovered.state === 'orphan') {
      return { state: 'orphan', reason: violation };
    }
    return rebasedResult(recovered.anchor);
  }

  const rebased = rebaseAnchor(anchor, content, allBlocks);
  if (rebased.state === 'orphan') {
    return rebased;
  }
  return rebasedResult(rebased.anchor);
}

function rebasedResult(anchor: AnchorRecord): ResolveOrRebaseResult {
  return {
    state: 'rebased',
    blockId: anchor.blockId,
    start: anchor.start,
    end: anchor.end,
    anchor,
  };
}
