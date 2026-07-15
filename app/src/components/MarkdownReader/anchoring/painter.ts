/**
 * Paint layer — turns resolved DOM Ranges into visible highlights.
 *
 * Preferred implementation is the CSS Custom Highlight API
 * (`CSS.highlights`): zero DOM mutation, so the React-rendered tree is never
 * touched (the whole point vs the 750ms live-reload — DOM-owned state like
 * open <details> must survive paints). Styling lives in MarkdownReader.css
 * under `::highlight(attn-md-comment)` / `::highlight(attn-md-deletion)`.
 *
 * MarkPainter is the fallback for engines without the API (jsdom/happy-dom in
 * unit tests — which is exactly why the fallback is the jsdom-testable one —
 * and insurance for old WKWebView; the plan targets Safari ≥ 17.2 which has
 * Custom Highlights). It wraps each covered text-node segment in
 * `<span class="md-mark md-mark-<kind>" data-md-mark="<id>">`; unpaint
 * replaces spans with their children and `normalize()`s parents so the DOM
 * returns to its pre-paint text-node shape.
 *
 * Painter instances are per-reader-root. `clearAll()` must run before repaint
 * on every content change: stale Ranges reference detached nodes.
 *
 * Known cosmetic gap: a Range that passes THROUGH inline chrome (blocked-image
 * span mid-paragraph) is painted contiguously by the Custom Highlight API,
 * chrome text included — acceptable visually. MarkPainter must NOT wrap that
 * chrome text (it would mutate chrome), so its walker rejects chrome subtrees.
 */

export type HighlightKind = 'comment' | 'deletion' | 'focus';

export interface HighlightPainter {
  /** Idempotent per id: painting an existing id replaces its range. */
  paint(id: string, range: Range, kind: HighlightKind): void;
  clear(id: string): void;
  clearAll(): void;
  /** Which strategy this painter uses (surfaced by the annotations bridge state). */
  readonly mode: 'custom-highlight' | 'mark';
}

/** Registry names — referenced by the ::highlight() rules in MarkdownReader.css. */
const HIGHLIGHT_NAMES: Record<HighlightKind, string> = {
  comment: 'attn-md-comment',
  deletion: 'attn-md-deletion',
  // Transient sidebar-focus glow, painted OVER an annotation's range for a
  // couple of seconds — a separate registry entry so it stacks with the
  // annotation's own comment/deletion paint.
  focus: 'attn-md-focus',
};

const KINDS: HighlightKind[] = ['comment', 'deletion', 'focus'];

export function supportsCustomHighlights(): boolean {
  return typeof CSS !== 'undefined' && 'highlights' in CSS && CSS.highlights != null;
}

/** Feature-detects once per call; both classes stay exported for direct tests. */
export function createHighlightPainter(root: HTMLElement): HighlightPainter {
  return supportsCustomHighlights() ? new CustomHighlightPainter() : new MarkPainter(root);
}

/**
 * CSS.highlights is a per-DOCUMENT registry, but painter instances are
 * per-reader-root and multiple markdown tiles can be open at once. Every live
 * painter therefore registers here, and each mutation rebuilds the two shared
 * registry entries from the UNION of all live painters' ranges — one painter
 * repainting (or clearing) can never wipe another tile's highlights.
 */
const livePainters = new Set<CustomHighlightPainter>();

/** Test hook: drop painters leaked by previous tests from the shared union. */
export function __resetCustomHighlightPaintersForTests(): void {
  livePainters.clear();
}

function rebuildSharedRegistry(): void {
  for (const kind of KINDS) {
    const ranges: Range[] = [];
    for (const painter of livePainters) {
      painter.collectRanges(kind, ranges);
    }
    const name = HIGHLIGHT_NAMES[kind];
    if (ranges.length === 0) {
      CSS.highlights.delete(name);
    } else {
      CSS.highlights.set(name, new Highlight(...ranges));
    }
  }
}

/**
 * CSS Custom Highlight API painter. Two shared registry entries (one per
 * kind), each rebuilt on every mutation from all live painters' entries —
 * Highlight objects are cheap containers of Ranges.
 */
export class CustomHighlightPainter implements HighlightPainter {
  readonly mode = 'custom-highlight' as const;
  private readonly entries = new Map<string, { range: Range; kind: HighlightKind }>();

  paint(id: string, range: Range, kind: HighlightKind): void {
    this.entries.set(id, { range, kind });
    livePainters.add(this);
    rebuildSharedRegistry();
  }

  clear(id: string): void {
    if (this.entries.delete(id)) {
      if (this.entries.size === 0) {
        livePainters.delete(this);
      }
      rebuildSharedRegistry();
    }
  }

  clearAll(): void {
    this.entries.clear();
    livePainters.delete(this);
    rebuildSharedRegistry();
  }

  /** Internal (union rebuild): append this painter's ranges of `kind` to `out`. */
  collectRanges(kind: HighlightKind, out: Range[]): void {
    for (const entry of this.entries.values()) {
      if (entry.kind === kind) {
        out.push(entry.range);
      }
    }
  }
}

const MARK_ATTR = 'data-md-mark';

/**
 * DOM-mutating fallback. `range.surroundContents` cannot span element
 * boundaries, so this implements the standard split: boundary text nodes are
 * `splitText` at the range edges, then every covered text node is wrapped in
 * a mark span. The visual "cover whitespace at edges without shifting layout"
 * trick (padding: 0 2px; margin: 0 -2px) lives on `.md-mark` in
 * MarkdownReader.css.
 */
export class MarkPainter implements HighlightPainter {
  readonly mode = 'mark' as const;

  constructor(private readonly root: HTMLElement) {}

  paint(id: string, range: Range, kind: HighlightKind): void {
    if (range.collapsed) {
      this.clear(id);
      return;
    }
    // Wrap the NEW spans first, then drop the stale ones: clearing first
    // would normalize() text nodes back together and invalidate the caller's
    // Range mid-paint (repaint of an id is exactly that scenario).
    const doc = this.root.ownerDocument;
    const fresh = new Set<Element>();
    for (const textNode of splitAndCollectRangeTextNodes(range)) {
      const span = doc.createElement('span');
      span.className = `md-mark md-mark-${kind}`;
      span.setAttribute(MARK_ATTR, id);
      textNode.parentNode?.replaceChild(span, textNode);
      span.appendChild(textNode);
      fresh.add(span);
    }
    for (const span of [...this.root.querySelectorAll(idSelector(id))]) {
      if (!fresh.has(span)) {
        unwrap(span);
      }
    }
  }

  clear(id: string): void {
    for (const span of [...this.root.querySelectorAll(idSelector(id))]) {
      unwrap(span);
    }
  }

  clearAll(): void {
    for (const span of [...this.root.querySelectorAll(`[${MARK_ATTR}]`)]) {
      unwrap(span);
    }
  }
}

/**
 * CSS.escape is universal in real engines but shaky in test DOMs; the ids we
 * generate are attribute-safe, so a quote-escaped literal is enough.
 */
function idSelector(id: string): string {
  return `[${MARK_ATTR}="${id.replace(/["\\]/g, '\\$&')}"]`;
}

/** Replace `el` with its children and merge the sibling text nodes back. */
function unwrap(el: Element): void {
  const parent = el.parentNode;
  if (!parent) {
    return;
  }
  while (el.firstChild) {
    parent.insertBefore(el.firstChild, el);
  }
  parent.removeChild(el);
  parent.normalize();
}

/**
 * Split the range's boundary text nodes at the range edges, then return every
 * text node fully covered by the (post-split) range, in document order.
 * Mutates the DOM (splitText) but never reorders or removes content.
 */
function splitAndCollectRangeTextNodes(range: Range): Text[] {
  let startNode = range.startContainer;
  let startOffset = range.startOffset;
  let endNode = range.endContainer;
  let endOffset = range.endOffset;

  // Split the END first: splitting the start of a shared node would shift the
  // end offset out from under us.
  if (endNode.nodeType === Node.TEXT_NODE && endOffset < (endNode.nodeValue?.length ?? 0)) {
    (endNode as Text).splitText(endOffset);
    // endNode now holds exactly the covered tail boundary.
  }
  if (startNode.nodeType === Node.TEXT_NODE && startOffset > 0) {
    const after = (startNode as Text).splitText(startOffset);
    if (endNode === startNode) {
      // Shared boundary node: the covered segment is entirely in `after`.
      endNode = after;
      endOffset = after.nodeValue?.length ?? 0;
    }
    startNode = after;
    startOffset = 0;
  }

  // Walk text nodes under the common ancestor ELEMENT (a TreeWalker never
  // emits its own root, so a text-node ancestor — single-node range — must be
  // widened to its parent), toggled on at the (post-split) start node and off
  // after the end node. Chrome subtrees are REJECTed: resolveDomRange never
  // places a BOUNDARY inside chrome, but a range can pass THROUGH inline
  // chrome (a blocked-image span between prose), and its text must never be
  // wrapped — chrome has no counterpart in anchor text-space.
  const ancestor = range.commonAncestorContainer;
  const rootNode = ancestor.nodeType === Node.ELEMENT_NODE ? ancestor : ancestor.parentNode;
  if (!rootNode) {
    return [];
  }
  const doc = rootNode.ownerDocument ?? (rootNode as Document);
  const walker = doc.createTreeWalker(rootNode, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
    acceptNode(node) {
      if (node.nodeType === Node.ELEMENT_NODE) {
        return (node as Element).hasAttribute('data-md-chrome')
          ? NodeFilter.FILTER_REJECT
          : NodeFilter.FILTER_SKIP;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  const covered: Text[] = [];
  let inRange = false;

  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (node === startNode) {
      inRange = true;
    }
    if (inRange && (node.nodeValue?.length ?? 0) > 0) {
      covered.push(node as Text);
    }
    if (node === endNode) {
      break;
    }
  }
  return covered;
}
