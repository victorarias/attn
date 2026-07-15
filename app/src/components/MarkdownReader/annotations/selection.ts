/**
 * Selection → pending-annotation mapping (the exceptSelectors + anchoring
 * equivalent of plannotator's web-highlighter setup).
 *
 * `evaluateSelection` is pure over its inputs (root element, a Selection-like
 * object, content, extracted blocks) so tests can drive it with synthetic
 * ranges instead of faking real mouse geometry.
 */

import { createAnchor, domPointToOffset } from '../anchoring';
import type { AnchorRecord, BlockText } from '../anchoring';

/**
 * Attn chrome a selection may not start or end in. `[data-md-no-annotate]`
 * is the future-proof hook (PR6's session picker adds the attribute).
 */
export const ANNOTATION_EXCEPT_SELECTORS = [
  '.workspace-dock-tile-header',
  '.md-annotations-sidebar',
  '.md-selection-toolbar',
  '.md-annotation-popover',
  '.md-quick-label-picker',
  'button',
  '.md-frontmatter',
  '[data-md-chrome]',
  '[data-md-no-annotate]',
];

const EXCEPT_SELECTOR = ANNOTATION_EXCEPT_SELECTORS.join(', ');

/** The subset of Selection the evaluator reads — mockable in jsdom tests. */
export interface SelectionLike {
  isCollapsed: boolean;
  rangeCount: number;
  anchorNode: Node | null;
  focusNode: Node | null;
  toString(): string;
  getRangeAt(index: number): Range;
}

export interface PendingSelection {
  anchor: AnchorRecord;
  /** The anchored (possibly clamped) text — what the toolbar/popover quote shows. */
  selectionText: string;
  /** True when a cross-block selection was clamped to its first block. */
  clamped: boolean;
  blockId: string;
  /** Owning block renders as a code block (toolbar switches to top-right mode). */
  isCodeBlock: boolean;
  /** Selection range bounding rect at creation (toolbar positioning). */
  rect: DOMRect | null;
}

function elementOf(node: Node): Element | null {
  return node.nodeType === Node.ELEMENT_NODE ? (node as Element) : node.parentElement;
}

function isInExceptedChrome(node: Node | null): boolean {
  const el = node ? elementOf(node) : null;
  return el ? el.closest(EXCEPT_SELECTOR) !== null : true;
}

function owningBlockElement(node: Node): Element | null {
  return elementOf(node)?.closest('[data-block-id]') ?? null;
}

/**
 * Validate a selection and map it to an anchor. Returns null (no pending
 * annotation) when the selection is collapsed/whitespace-only, escapes the
 * reader root, touches excepted chrome, or cannot be anchored (no owning
 * block, non-paintable block, empty after clamping).
 *
 * Cross-block selections are clamped to the FIRST block (v1 single-block
 * contract of the anchoring core); `clamped` reports it so UI can hint.
 */
export function evaluateSelection(
  root: HTMLElement,
  selection: SelectionLike | null,
  content: string,
  blocks: BlockText[],
): PendingSelection | null {
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
    return null;
  }
  if (selection.toString().trim() === '') {
    return null;
  }
  const { anchorNode, focusNode } = selection;
  if (!anchorNode || !focusNode || !root.contains(anchorNode) || !root.contains(focusNode)) {
    return null;
  }
  if (isInExceptedChrome(anchorNode) || isInExceptedChrome(focusNode)) {
    return null;
  }

  const range = selection.getRangeAt(0);
  const blockEl = owningBlockElement(range.startContainer);
  const blockId = blockEl?.getAttribute('data-block-id');
  if (!blockEl || !blockId) {
    return null;
  }
  const block = blocks.find((b) => b.blockId === blockId);
  if (!block || block.nonPaintable) {
    return null; // mermaid & co: text-space diverges from the painted DOM
  }

  const start = domPointToOffset(blockEl, range.startContainer, range.startOffset);
  if (start === null) {
    return null;
  }
  // End boundary: same block → exact offset; anywhere else (a later sibling
  // block, or a stamped ancestor when the selection spills out) → clamp to
  // the first block's end.
  const endBlockEl = owningBlockElement(range.endContainer);
  let end: number | null;
  let clamped = false;
  if (endBlockEl === blockEl || (endBlockEl && blockEl.contains(endBlockEl))) {
    // Same block, or a nested stamped block inside it: the offset is still
    // expressible in the owning block's text-space via the DOM walk.
    end = domPointToOffset(blockEl, range.endContainer, range.endOffset);
  } else {
    end = block.text.length;
    clamped = true;
  }
  if (end === null) {
    return null;
  }

  // Trim whitespace edges so the anchored quote matches what a user perceives
  // as selected (and so createAnchor's non-empty contract holds).
  const slice = block.text.slice(start, end);
  const leading = slice.length - slice.replace(/^\s+/, '').length;
  const trailing = slice.length - slice.replace(/\s+$/, '').length;
  const trimmedStart = start + leading;
  const trimmedEnd = end - trailing;
  if (trimmedEnd <= trimmedStart) {
    return null;
  }

  const anchor = createAnchor(content, blockId, trimmedStart, trimmedEnd, blocks);
  if (!anchor) {
    return null;
  }

  let rect: DOMRect | null = null;
  try {
    rect = range.getBoundingClientRect();
  } catch {
    rect = null; // test DOMs without layout
  }

  return {
    anchor,
    selectionText: anchor.exact,
    clamped,
    blockId: anchor.blockId,
    isCodeBlock: blockEl.tagName === 'PRE' || blockEl.closest('.md-codeblock') !== null,
    rect,
  };
}
