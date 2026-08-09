/**
 * Anchor record and resolve/rebase result shapes. Pure data over strings — no
 * DOM, no React — and every offset is UTF-16 code units.
 */

export interface AnchorRecord {
  /** data-block-id of the OWNING (deepest containing) block. */
  blockId: string;
  /** Block's data-source-line at creation/last rebase (1-based raw-file line). */
  startLine: number;
  endLine: number;
  /** The selected RENDERED text, post prose-transforms. */
  exact: string;
  /** Up to 32 chars of rendered text on either side of `exact`, same block. */
  prefix: string;
  suffix: string;
  /** Offsets into the block's normalized rendered text; end is exclusive. */
  start: number;
  end: number;
  /** fnv1a32 hex of the raw file content last rebased against. */
  contentHash: string;
}

/**
 * One stamped block's rendered text: exactly what the DOM's text nodes hold for
 * the block, chrome skipped. Normalization rule lives in extractBlocks.ts.
 */
export interface BlockText {
  blockId: string;
  startLine: number;
  endLine: number;
  text: string;
  depth: number;
  parentId: string | null;
  startInParent: number;
  /** Text-space diverges from what is painted (mermaid); the paint layer skips it. */
  nonPaintable?: boolean;
}

export type OrphanReason =
  | 'block-missing'
  /** Hash matched but slice ≠ exact (contract violation) and rebase also failed. */
  | 'offset-mismatch'
  /** `exact` not found in any rebase tier. */
  | 'text-not-found'
  /** Multiple candidates, none above the confidence threshold. */
  | 'ambiguous';

export type ResolveResult =
  | { state: 'exact'; blockId: string; start: number; end: number }
  | { state: 'orphan'; reason: OrphanReason };

export type RebaseTier = 'same-block' | 'document' | 'normalized';

export type RebaseResult =
  | { state: 'rebased'; anchor: AnchorRecord; tier: RebaseTier }
  | { state: 'orphan'; reason: OrphanReason };

/** On `rebased`, the caller must persist the re-baselined `anchor`. */
export type ResolveOrRebaseResult =
  | { state: 'exact'; blockId: string; start: number; end: number; anchor: AnchorRecord }
  | { state: 'rebased'; blockId: string; start: number; end: number; anchor: AnchorRecord }
  | { state: 'orphan'; reason: OrphanReason };
