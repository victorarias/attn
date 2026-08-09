/**
 * Paint layer — turns resolved DOM Ranges into visible highlights.
 *
 * Preferred: the CSS Custom Highlight API, which mutates no DOM, so DOM-owned
 * state such as an open <details> survives a paint (styling is in
 * MarkdownReader.css under `::highlight(attn-md-*)`). MarkPainter is the
 * fallback for engines without it: it wraps covered text-node segments in
 * `<span class="md-mark">` and unpaint restores the text-node shape. A range
 * passing THROUGH inline chrome must never be wrapped, so its walker rejects
 * chrome subtrees. Painters are per-reader-root, and `clearAll()` must run
 * before repaint on every content change — stale Ranges hold detached nodes.
 */

export type HighlightKind = 'comment' | 'deletion' | 'focus';

export interface HighlightPainter {
  /** Idempotent per id: painting an existing id replaces its range. */
  paint(id: string, range: Range, kind: HighlightKind): void;
  clear(id: string): void;
  clearAll(): void;
  readonly mode: 'custom-highlight' | 'mark';
}

/** Registry names — referenced by the ::highlight() rules in MarkdownReader.css. */
const HIGHLIGHT_NAMES: Record<HighlightKind, string> = {
  comment: 'attn-md-comment',
  deletion: 'attn-md-deletion',
  // Separate entry so the transient sidebar glow stacks over comment/deletion.
  focus: 'attn-md-focus',
};

const KINDS: HighlightKind[] = ['comment', 'deletion', 'focus'];

export function supportsCustomHighlights(): boolean {
  return typeof CSS !== 'undefined' && 'highlights' in CSS && CSS.highlights != null;
}

export function createHighlightPainter(root: HTMLElement): HighlightPainter {
  return supportsCustomHighlights() ? new CustomHighlightPainter() : new MarkPainter(root);
}

/**
 * CSS.highlights is per-DOCUMENT while painters are per-reader-root, so each
 * mutation rebuilds the shared entries from the UNION of live painters — one
 * tile clearing must never wipe another tile's highlights.
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

/** CSS Custom Highlight API painter; Highlight objects are cheap Range bags. */
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
 * DOM-mutating fallback: `range.surroundContents` cannot span element
 * boundaries, so boundary text nodes are `splitText` at the edges and every
 * covered text node is wrapped in a mark span.
 */
export class MarkPainter implements HighlightPainter {
  readonly mode = 'mark' as const;

  constructor(private readonly root: HTMLElement) {}

  paint(id: string, range: Range, kind: HighlightKind): void {
    if (range.collapsed) {
      this.clear(id);
      return;
    }
    // Wrap NEW spans before dropping stale ones: clearing first would
    // normalize() text nodes together and invalidate the caller's Range.
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

/** No CSS.escape: shaky in test DOMs, and our ids are attribute-safe. */
function idSelector(id: string): string {
  return `[${MARK_ATTR}="${id.replace(/["\\]/g, '\\$&')}"]`;
}

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
 * Split the boundary text nodes at the range edges and return every text node
 * fully covered afterwards. Mutates the DOM but never reorders or removes.
 */
function splitAndCollectRangeTextNodes(range: Range): Text[] {
  let startNode = range.startContainer;
  let startOffset = range.startOffset;
  let endNode = range.endContainer;
  let endOffset = range.endOffset;

  // Split the END first: splitting a shared start node shifts the end offset.
  if (endNode.nodeType === Node.TEXT_NODE && endOffset < (endNode.nodeValue?.length ?? 0)) {
    (endNode as Text).splitText(endOffset);
  }
  if (startNode.nodeType === Node.TEXT_NODE && startOffset > 0) {
    const after = (startNode as Text).splitText(startOffset);
    if (endNode === startNode) {
      endNode = after;
      endOffset = after.nodeValue?.length ?? 0;
    }
    startNode = after;
    startOffset = 0;
  }

  // Walk under the common ancestor ELEMENT: a TreeWalker never emits its own
  // root, so a text-node ancestor (single-node range) widens to its parent.
  // Chrome subtrees are REJECTed — a range may pass through inline chrome, and
  // chrome text has no counterpart in anchor text-space.
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
