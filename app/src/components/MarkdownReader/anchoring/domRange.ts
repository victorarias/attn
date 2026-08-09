/**
 * Map an anchor's (start, end) text offsets to a live DOM Range inside the
 * element carrying the matching data-block-id.
 *
 * The walk mirrors extractBlocks' normalization exactly: every text node in
 * tree order, skipping `data-md-chrome` subtrees, in UTF-16 units. Split text
 * nodes are tolerated by construction, and a boundary landing on a seam
 * attaches START to the later node and END to the earlier one.
 */

const CHROME_ATTR = 'data-md-chrome';

function chromeSkippingTextWalker(root: Element): TreeWalker {
  return root.ownerDocument.createTreeWalker(
    root,
    NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT,
    {
      acceptNode(node) {
        if (node.nodeType === Node.ELEMENT_NODE) {
          // REJECT drops the whole chrome subtree; SKIP descends without emitting.
          return (node as Element).hasAttribute(CHROME_ATTR)
            ? NodeFilter.FILTER_REJECT
            : NodeFilter.FILTER_SKIP;
        }
        return NodeFilter.FILTER_ACCEPT;
      },
    },
  );
}

/** Non-chrome text in tree order; must equal extractBlockTexts' text (pinned). */
export function blockDomText(blockEl: Element): string {
  const walker = chromeSkippingTextWalker(blockEl);
  let text = '';
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    text += node.nodeValue ?? '';
  }
  return text;
}

/**
 * Inverse of `resolveDomRange`, over both Range boundary shapes: a text-node
 * offset is a character index; an element offset is a child index, the point
 * sitting before that child (end of element when it equals childCount). Null
 * when the node is outside `blockEl` or inside chrome.
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
 * Resolve `[start, end)` to a DOM Range within `blockEl`. Null — never a throw
 * — when the range is degenerate or the DOM's text is shorter than `end`.
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
    // Start attaches to the LATER node on a seam (start === acc + len).
    if (!startSet && start >= acc && start < acc + len) {
      range.setStart(node, start - acc);
      startSet = true;
    }
    // End attaches to the EARLIER node on a seam (end === acc + len).
    if (startSet && !endSet && end > acc && end <= acc + len) {
      range.setEnd(node, end - acc);
      endSet = true;
      break;
    }
    acc += len;
  }

  return startSet && endSet ? range : null;
}
