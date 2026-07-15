/**
 * domRange — map a resolved anchor's (start, end) text offsets to a live DOM
 * Range inside the block element carrying the matching data-block-id.
 *
 * The walk mirrors the extractBlocks normalization rule exactly: concatenate
 * every text node in the block's subtree in tree order, skipping subtrees the
 * React layer marks as chrome (`data-md-chrome` — alert titles, copy-button
 * chrome, blocked-image fallbacks), whose text has no hast counterpart.
 * Offsets are UTF-16 code units, the same units DOM Range offsets use, so no
 * conversion happens anywhere.
 *
 * Split text nodes (shiki spans, other highlights, prior splitText calls) are
 * tolerated by construction: the walk never assumes one text node per block.
 * A boundary landing exactly between two nodes attaches the START to the
 * later node at offset 0 and the END to the earlier node's end — this keeps
 * ranges tight (no zero-width tails inside neighbouring elements).
 */

const CHROME_ATTR = 'data-md-chrome';

function chromeSkippingTextWalker(root: Element): TreeWalker {
  return root.ownerDocument.createTreeWalker(
    root,
    NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT,
    {
      acceptNode(node) {
        if (node.nodeType === Node.ELEMENT_NODE) {
          // REJECT skips the whole chrome subtree; SKIP descends into
          // everything else without emitting the element itself.
          return (node as Element).hasAttribute(CHROME_ATTR)
            ? NodeFilter.FILTER_REJECT
            : NodeFilter.FILTER_SKIP;
        }
        return NodeFilter.FILTER_ACCEPT;
      },
    },
  );
}

/**
 * The block's rendered text as the anchor text-space sees it: every non-chrome
 * text node concatenated in tree order. Must equal the extractBlockTexts text
 * for the same block (the pipeline-parity fixture pins this).
 */
export function blockDomText(blockEl: Element): string {
  const walker = chromeSkippingTextWalker(blockEl);
  let text = '';
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    text += node.nodeValue ?? '';
  }
  return text;
}

/**
 * Map a DOM point (node, offset) — the shape Selection/Range boundaries come
 * in — to a UTF-16 offset into the block's rendered text (the inverse of
 * `resolveDomRange`). Handles both boundary shapes:
 *
 * - Text-node boundary: offset is a character index inside that node.
 * - Element boundary: offset is a child index; the point sits before the
 *   `offset`-th child (or at the element's end when offset === childCount,
 *   e.g. triple-click paragraph selections).
 *
 * Returns null when the node is outside `blockEl` or inside a chrome subtree
 * (chrome text has no counterpart in anchor text-space).
 */
export function domPointToOffset(blockEl: Element, node: Node, offset: number): number | null {
  if (node !== blockEl && !blockEl.contains(node)) {
    return null;
  }
  if (node.nodeType === Node.TEXT_NODE) {
    const walker = chromeSkippingTextWalker(blockEl);
    let acc = 0;
    for (let t = walker.nextNode(); t; t = walker.nextNode()) {
      const len = t.nodeValue?.length ?? 0;
      if (t === node) {
        return acc + Math.min(offset, len);
      }
      acc += len;
    }
    return null; // text node exists but the walker never emitted it: chrome
  }
  if (node.nodeType !== Node.ELEMENT_NODE) {
    return null;
  }
  const el = node as Element;
  if (el !== blockEl && el.closest(`[${CHROME_ATTR}]`)) {
    return null;
  }
  // The point sits before `anchor` (or at el's end when anchor is null).
  const anchor = el.childNodes[offset] ?? null;
  const walker = chromeSkippingTextWalker(blockEl);
  let acc = 0;
  for (let t = walker.nextNode(); t; t = walker.nextNode()) {
    const isAtOrAfterPoint = anchor
      ? anchor === t ||
        anchor.contains(t) ||
        (anchor.compareDocumentPosition(t) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0
      : !el.contains(t) && (el.compareDocumentPosition(t) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
    if (isAtOrAfterPoint) {
      return acc;
    }
    acc += t.nodeValue?.length ?? 0;
  }
  return acc; // point lies after every text node in the block
}

/**
 * Resolve `[start, end)` (UTF-16 offsets into the block's rendered text) to a
 * DOM Range within `blockEl` (the element matching `[data-block-id="..."]`;
 * scoping the querySelector is the caller's job). Returns null when the range
 * is degenerate or the DOM's accumulated text is shorter than `end` (DOM /
 * text-model disagreement — unpaintable, never throws).
 */
export function resolveDomRange(blockEl: Element, start: number, end: number): Range | null {
  if (start < 0 || end <= start) {
    return null;
  }
  const walker = chromeSkippingTextWalker(blockEl);
  const range = blockEl.ownerDocument.createRange();
  let acc = 0;
  let startSet = false;
  let endSet = false;

  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const len = node.nodeValue?.length ?? 0;
    if (len === 0) {
      continue;
    }
    // Start attaches to the LATER node when landing on a seam (start === acc + len).
    if (!startSet && start >= acc && start < acc + len) {
      range.setStart(node, start - acc);
      startSet = true;
    }
    // End attaches to the EARLIER node when landing on a seam (end === acc + len).
    if (startSet && !endSet && end > acc && end <= acc + len) {
      range.setEnd(node, end - acc);
      endSet = true;
      break;
    }
    acc += len;
  }

  return startSet && endSet ? range : null;
}
