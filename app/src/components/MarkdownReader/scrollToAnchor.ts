/**
 * Smooth-scroll an in-document `#fragment` link inside the reader's scroll
 * container (never `window`) — plannotator's `scrollToAnchor` behavior.
 *
 * The dock tile's scroll container is `.workspace-dock-tile-body`; a sticky
 * bar INSIDE the scroll container (none in the tile today — the tile header
 * is a sibling) contributes a top offset per plannotator's formula.
 */

const SCROLL_CONTAINER_SELECTOR = '.workspace-dock-tile-body';

function findScrollContainer(start: HTMLElement): HTMLElement | null {
  const tileBody = start.closest<HTMLElement>(SCROLL_CONTAINER_SELECTOR);
  if (tileBody) {
    return tileBody;
  }
  // Fallback: nearest scrollable ancestor.
  let node: HTMLElement | null = start.parentElement;
  while (node) {
    const overflowY = getComputedStyle(node).overflowY;
    if (overflowY === 'auto' || overflowY === 'scroll') {
      return node;
    }
    node = node.parentElement;
  }
  return null;
}

/** Returns true when the hash resolved to a target inside `root` and scrolled. */
export function scrollToAnchor(root: HTMLElement | null, hash: string, stickyBar?: HTMLElement | null): boolean {
  if (!root || !hash.startsWith('#')) {
    return false;
  }
  let id: string;
  try {
    id = decodeURIComponent(hash.slice(1));
  } catch {
    id = hash.slice(1);
  }
  if (!id) {
    return false;
  }
  // Resolve WITHIN root: multiple markdown tiles can render the same document
  // (per-document sluggers dedup ids per tile only), so a document-wide
  // getElementById would find the FIRST tile's heading and leave later tiles'
  // fragment links dead. The attribute selector handles ids starting with
  // digits/unicode that a bare `#id` selector would reject.
  const target = root.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`);
  if (!target) {
    return false;
  }
  const viewport = findScrollContainer(root);
  if (!viewport) {
    return false;
  }
  const headerOffset = stickyBar
    ? stickyBar.getBoundingClientRect().height + (parseFloat(getComputedStyle(stickyBar).top || '0') || 0)
    : 0;
  const top = viewport.scrollTop
    + (target.getBoundingClientRect().top - viewport.getBoundingClientRect().top)
    - headerOffset;
  const reduceMotion = typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  viewport.scrollTo({ top: Math.max(0, top), behavior: reduceMotion ? 'auto' : 'smooth' });
  return true;
}
