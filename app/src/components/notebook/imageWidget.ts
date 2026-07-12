// Inline images (`![alt](src)`) that are the entire content of their own line render
// as a block image widget, with the raw markdown revealed when a selection touches
// the line — the same cursor-intersection reveal rule as tableWidget.ts (simpler than
// frontmatterCard's explicit-edit-mode toggle: an image needs no dedicated editing
// affordance beyond "click the line to see its source").
//
// CM constraint that shapes this file: decorations that affect vertical layout (block
// widgets) MUST come directly from a StateField via `EditorView.decorations.from(...)`
// — the view plugin that powers the inline preview runs after layout and is forbidden
// from introducing them. So this extension lives here, in its own field, mirroring
// tableWidget.ts and frontmatterCard.ts. (Inline, mid-paragraph images are left alone
// by this module — liveMarkdownPreview's ViewPlugin already hides their LinkMark/URL
// syntax the same way it does for a plain Link, so they read as bracket-free text.)

import { ensureSyntaxTree } from '@codemirror/language';
import { type EditorState, type Extension, type Range, StateField } from '@codemirror/state';
import { Decoration, type DecorationSet, EditorView, WidgetType } from '@codemirror/view';
import type { SyntaxNode } from '@lezer/common';
import { notebookLinkPath } from './brokenLinks';

export interface ImageTarget {
  lineFrom: number;
  lineTo: number;
  alt: string;
  src: string;
}

export interface ImageWidgetOptions {
  // Resolve a notebook-relative src (already stripped of #fragment/?query) to a
  // displayable src (typically a data: URI), or null when it can't be resolved.
  // Absent, a null resolution, or a rejection all render the broken placeholder.
  resolveSrc?: (src: string) => Promise<string | null>;
}

// Srcs the browser can load directly, with no daemon round-trip: an explicit URL
// scheme (http:, data:, …) or a protocol-relative URL. Everything else is treated as
// a notebook-relative path and goes through options.resolveSrc.
const SCHEME = /^[a-z][a-z0-9+.-]*:/i;

function isDirectSrc(src: string): boolean {
  return SCHEME.test(src) || src.startsWith('//');
}

// Pull { alt, src } out of an Image syntax node. Lezer's markdown grammar builds
// Image with the same finishLink() shape as Link, except the opening LinkMark spans
// the image's `![` (2 chars) instead of a link's `[` (1 char): children are
// [LinkMark(open), ...alt content..., LinkMark(close ']'), LinkMark(open '('), URL,
// Title?, LinkMark(close ')')]. There's no single "label" node, so alt is read as the
// doc slice between the opening mark's end and the closing ']' mark's start — the
// second-to-last LinkMark before the URL (the last is the '(' that precedes it).
// Returns null for a node that doesn't parse as a complete inline image (e.g.
// reference-style `![alt][ref]`, which has no URL child).
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

// The images that qualify for widget rendering: an Image node that is the ENTIRE
// content of its line (surrounding whitespace allowed). Inline images mid-paragraph,
// a line with trailing text after the image, and a line with more than one image
// (each sees the other's raw markdown as non-whitespace "before"/"after" text) all
// fail the check and stay raw. Pure over the parsed state, so it's unit-testable
// headlessly like brokenLinks.notebookLinkPaths.
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
    // Shared with every widget instance from the same imageWidget() extension, so
    // retyping or toggling the reveal doesn't re-fetch a src already resolved once.
    private readonly cache: Map<string, Promise<string | null>>,
  ) {
    super();
  }

  eq(other: ImageWidget) {
    return this.target.alt === other.target.alt && this.target.src === other.target.src;
  }

  // Rough space to reserve before the real image loads and CM re-measures, so the
  // first layout doesn't jump the scroll position.
  get estimatedHeight() {
    return 220;
  }

  // Clicks must reach the editor so clicking the widget's line places the cursor
  // there — which is what reveals the raw markdown (the same reveal rule tableWidget
  // uses).
  ignoreEvent() {
    return false;
  }

  toDOM(view: EditorView): HTMLElement {
    const { alt, src } = this.target;
    const container = document.createElement('div');
    container.className = 'cm-md-image';

    // A click reveals the raw markdown: move the cursor onto the widget's line (the
    // same gotoLine pattern tableWidget uses) rather than relying on CM's default
    // click-to-cursor resolution over a replaced block range, which isn't guaranteed
    // to land inside it.
    container.addEventListener('mousedown', (event) => event.preventDefault());
    container.addEventListener('click', (event) => {
      event.preventDefault();
      event.stopPropagation();
      view.dispatch({ selection: { anchor: this.target.lineFrom } });
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
      // A resolved src (e.g. a valid data: URI) can still fail to decode; fall back
      // to the broken placeholder rather than leaving a blank box.
      img.addEventListener('error', renderBroken);
      container.appendChild(img);
    };

    if (isDirectSrc(src)) {
      renderImg(src);
      return container;
    }

    const path = notebookLinkPath(src);
    if (!path || !this.resolveSrc) {
      renderBroken();
      return container;
    }

    let pending = this.cache.get(path);
    if (!pending) {
      pending = this.resolveSrc(path).catch(() => null);
      this.cache.set(path, pending);
    }
    pending.then((resolved) => {
      // The widget's DOM may already have been torn down (navigation, re-render, or
      // the line's raw markdown got revealed) by the time this resolves.
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
        // Mirrors tableField: if the tree isn't ready yet, keep the previous set
        // rather than flashing empty.
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
