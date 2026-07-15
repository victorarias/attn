/**
 * Anchoring core — pure (content, anchor) → resolution and
 * (anchor, newContent) → rebased | orphan functions. No DOM, no React;
 * everything here is testable in plain node vitest.
 *
 * DOM range resolution and the paint layer live in separate modules layered
 * on top of these (they consume BlockText offsets but never feed back in).
 */

export * from './types';
export { fnv1a32 } from './hash';
export { extractBlockTexts, ownerBlockFor, runReaderPipeline } from './extractBlocks';
export { buildAnchor, createAnchor, CONTEXT_CHARS } from './create';
export { resolveAnchor, resolveOrRebase } from './resolve';
export { rebaseAnchor } from './rebase';
export { resolveDomRange, blockDomText } from './domRange';
export {
  createHighlightPainter,
  supportsCustomHighlights,
  CustomHighlightPainter,
  MarkPainter,
  type HighlightKind,
  type HighlightPainter,
} from './painter';
