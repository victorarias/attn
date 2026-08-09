// An image that is the entire content of its line renders as a block widget, its
// source revealed when a selection touches the line (tableWidget's reveal rule).
//
// CM constraint shaping this file: decorations affecting vertical layout MUST come
// from a StateField via `EditorView.decorations.from(...)` — the view plugin runs
// after layout and may not introduce them. Hence its own field, as tableWidget has.

import { ensureSyntaxTree } from '@codemirror/language';
import { type EditorState, type Extension, type Range, StateField } from '@codemirror/state';
import { Decoration, type DecorationSet, EditorView, WidgetType } from '@codemirror/view';
import type { SyntaxNode } from '@lezer/common';

export interface ImageTarget {
  lineFrom: number;
  lineTo: number;
  alt: string;
  src: string;
}

export interface ImageWidgetOptions {
  // Resolve a raw notebook-relative src to a displayable one; absent, null, or a
  // rejection all render the broken placeholder.
  resolveSrc?: (src: string) => Promise<string | null>;
}

// Srcs the browser loads directly (explicit scheme or protocol-relative); anything
// else is notebook-relative and goes through options.resolveSrc.
const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

function isDirectSrc(src: string): boolean {
  return SCHEME.test(src) || src.startsWith('//');
}

// Pull { alt, src } out of an Image node. Lezer builds Image like Link, children
// being [LinkMark, ...alt..., LinkMark(']'), LinkMark('('), URL, Title?, LinkMark];
// there is no label node, so alt is the slice between the opening mark's end and
// the second-to-last LinkMark. Null for `![alt][ref]`, which has no URL child.
function parseImageNode(node: SyntaxNode, state: EditorState): { alt: string; src: string } | null {
  const url = node.getChild('URL');
  if (!url) return null;
  const marks: SyntaxNode[] = [];
  for (let child = node.firstChild; child && child.from < url.from; child = child.nextSibling) {
    if (child.name === 'LinkMark') marks.push(child);
  }
  if (marks.length < 3) return null; // need at least: opening ![, closing ], opening (
  const open = marks[0];
  const closeBracket = marks[marks.length - 2];
  return {
    alt: state.doc.sliceString(open.to, closeBracket.from),
    src: state.doc.sliceString(url.from, url.to),
  };
}

// Images qualifying for widget rendering: an Image that is the ENTIRE content of
// its line. Mid-paragraph images, trailing text, and two on one line stay raw.
export function imageTargets(state: EditorState): ImageTarget[] {
  const tree = ensureSyntaxTree(state, state.doc.length, 50);
  if (!tree) return [];
  const targets: ImageTarget[] = [];
  tree.iterate({
    enter: (node) => {
      if (node.name !== 'Image') return;
      if (node.to > state.doc.lineAt(node.from).to) return; // spans a line break
      const line = state.doc.lineAt(node.from);
      const before = state.doc.sliceString(line.from, node.from).trim();
      const after = state.doc.sliceString(node.to, line.to).trim();
      if (before || after) return;
      const parsed = parseImageNode(node.node, state);
      if (!parsed) return;
      targets.push({ lineFrom: line.from, lineTo: line.to, alt: parsed.alt, src: parsed.src });
    },
  });
  return targets;
}

class ImageWidget extends WidgetType {
  constructor(
    readonly target: ImageTarget,
    private readonly resolveSrc: ImageWidgetOptions['resolveSrc'],
    // Shared across widget instances, so a reveal toggle never re-fetches a src.
    private readonly cache: Map<string, Promise<string | null>>,
  ) {
    super();
  }

  eq(other: ImageWidget) {
    return this.target.alt === other.target.alt && this.target.src === other.target.src;
  }

  // Space reserved before the image loads, so the first layout doesn't jump.
  get estimatedHeight() {
    return 220;
  }

  // Clicks must reach the editor: placing the cursor is what reveals the source.
  ignoreEvent() {
    return false;
  }

  toDOM(view: EditorView): HTMLElement {
    const { alt, src } = this.target;
    const container = document.createElement('div');
    container.className = 'cm-md-image';

    // Move the cursor onto the widget's line explicitly; CM's click-to-cursor over
    // a replaced block range may not land inside it.
    //
    // eq() is position-blind, so an edit above does not recreate this DOM — which
    // means it outlives the lineFrom it was built with. Read the position via
    // posAtDOM at click time, or a stale handler jumps to where the image WAS.
    container.addEventListener('mousedown', (event) => event.preventDefault());
    container.addEventListener('click', (event) => {
      event.preventDefault();
      event.stopPropagation();
      const pos = view.posAtDOM(container);
      if (pos >= 0) view.dispatch({ selection: { anchor: pos } });
      view.focus();
    });

    const renderBroken = () => {
      container.className = 'cm-md-image-broken';
      container.replaceChildren();
      const label = document.createElement('span');
      label.className = 'cm-md-image-broken-label';
      label.textContent = alt || src;
      const hint = document.createElement('span');
      hint.className = 'cm-md-image-broken-hint';
      hint.textContent = 'image not found';
      container.append(label, hint);
    };

    const renderImg = (resolvedSrc: string) => {
      container.className = 'cm-md-image';
      container.replaceChildren();
      const img = document.createElement('img');
      img.alt = alt;
      img.src = resolvedSrc;
      // A resolved src can still fail to decode; show broken, not a blank box.
      img.addEventListener('error', renderBroken);
      container.appendChild(img);
    };

    if (isDirectSrc(src)) {
      renderImg(src);
      return container;
    }

    // Hand the raw src to resolveSrc, the ONE place that resolves it: resolving
    // here too would double-apply and mis-clamp a `..` before baseDir is known.
    if (!this.resolveSrc) {
      renderBroken();
      return container;
    }

    let pending = this.cache.get(src);
    if (!pending) {
      pending = this.resolveSrc(src).catch(() => null);
      this.cache.set(src, pending);
    }
    pending.then((resolved) => {
      // The widget's DOM may already be torn down by the time this resolves.
      if (!container.isConnected) return;
      if (resolved) renderImg(resolved);
      else renderBroken();
    });

    return container;
  }
}

function imageDecorations(
  state: EditorState,
  resolveSrc: ImageWidgetOptions['resolveSrc'],
  cache: Map<string, Promise<string | null>>,
): DecorationSet {
  const ranges: Range<Decoration>[] = [];
  for (const target of imageTargets(state)) {
    const revealed = state.selection.ranges.some(
      (range) => range.from <= target.lineTo && range.to >= target.lineFrom,
    );
    if (revealed) continue;
    ranges.push(
      Decoration.replace({ block: true, widget: new ImageWidget(target, resolveSrc, cache) }).range(
        target.lineFrom,
        target.lineTo,
      ),
    );
  }
  return Decoration.set(ranges);
}

const imageTheme = EditorView.baseTheme({
  '.cm-md-image': {
    display: 'block',
    margin: '4px 0',
  },
  '.cm-md-image img': {
    display: 'block',
    maxWidth: '100%',
    maxHeight: '480px',
    borderRadius: '6px',
  },
  '.cm-md-image-broken': {
    display: 'flex',
    flexDirection: 'column',
    gap: '2px',
    margin: '4px 0',
    padding: '10px 14px',
    borderRadius: '8px',
    border: '1px dashed var(--color-border, rgba(127,127,127,0.35))',
    background: 'var(--color-bg-elevated, rgba(127,127,127,0.08))',
    fontFamily: 'var(--font-sans, system-ui), sans-serif',
  },
  '.cm-md-image-broken-label': {
    fontSize: '0.85em',
    color: 'var(--color-text-secondary, #b8b8b8)',
  },
  '.cm-md-image-broken-hint': {
    fontSize: '0.72em',
    color: 'var(--color-text-dimmed, #666)',
  },
});

export function imageWidget(options: ImageWidgetOptions = {}): Extension {
  const cache = new Map<string, Promise<string | null>>();

  const imageField = StateField.define<DecorationSet>({
    create: (state) => imageDecorations(state, options.resolveSrc, cache),
    update(value, tr) {
      if (tr.docChanged || tr.selection) {
        // As tableField does: an unready tree keeps the previous set, not empty.
        const tree = ensureSyntaxTree(tr.state, tr.state.doc.length, 50);
        if (!tree) return value.map(tr.changes);
        return imageDecorations(tr.state, options.resolveSrc, cache);
      }
      return value.map(tr.changes);
    },
    provide: (field) => EditorView.decorations.from(field),
  });

  return [imageField, imageTheme];
}
