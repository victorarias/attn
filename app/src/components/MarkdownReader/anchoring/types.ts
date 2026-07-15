/**
 * Anchoring core types — the W3C Web-Annotation-style anchor record and the
 * resolve/rebase result shapes.
 *
 * Everything in this module is pure data over strings: no DOM, no React.
 * All offsets are UTF-16 code units (JS string indices / DOM Range offsets).
 */

export interface AnchorRecord {
  /** data-block-id of the OWNING block (deepest stamped element containing the range). */
  blockId: string;
  /** Block's data-source-line at creation/last rebase (1-based raw-file line). */
  startLine: number;
  /** Block's data-source-line-end. */
  endLine: number;
  /** The selected RENDERED text (post prose-transforms). */
  exact: string;
  /** Up to 32 chars of rendered text before `exact`, same block. */
  prefix: string;
  /** Up to 32 chars of rendered text after `exact`, same block. */
  suffix: string;
  /** Offset of `exact` into the block's normalized rendered text. */
  start: number;
  /** Exclusive end offset; `end - start === exact.length`. */
  end: number;
  /** fnv1a32 hex of the raw file content the anchor was created/last rebased against. */
  contentHash: string;
}

/**
 * One stamped block's rendered text, extracted headlessly from the reader
 * pipeline. `text` is exactly what the DOM's text nodes will contain for the
 * block (chrome-skipped) — see extractBlocks.ts for the normalization rule.
 */
export interface BlockText {
  blockId: string;
  /** 1-based raw-file line range (from data-source-line / -end). */
  startLine: number;
  endLine: number;
  /** Normalized rendered text; offsets into it are UTF-16 code units. */
  text: string;
  /** Nesting depth among stamped elements (0 = top-level). */
  depth: number;
  /** blockId of the nearest stamped ancestor, or null for depth-0 blocks. */
  parentId: string | null;
  /** Offset of this block's text within the nearest stamped ancestor's text. */
  startInParent: number;
  /**
   * True for blocks whose text-space model diverges from what is painted
   * (mermaid code blocks render as an svg diagram). Resolve/rebase still work
   * in text space; the paint layer skips these in v1.
   */
  nonPaintable?: boolean;
}

export type OrphanReason =
  /** blockId no longer exists (resolve path). */
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

/**
 * Result of the convenience `resolveOrRebase` — the API the live-reload path
 * uses. On `rebased`, the caller must persist the re-baselined `anchor`.
 */
export type ResolveOrRebaseResult =
  | { state: 'exact'; blockId: string; start: number; end: number; anchor: AnchorRecord }
  | { state: 'rebased'; blockId: string; start: number; end: number; anchor: AnchorRecord }
  | { state: 'orphan'; reason: OrphanReason };
