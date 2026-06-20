// Live-preview decorations for CodeMirror 6 — the Obsidian-style "read and type in
// the same surface" behavior. The document stays raw, canonical markdown (the
// external-sync invariant); these decorations only change how it RENDERS:
//   - heading lines are sized and their leading `#`s hidden
//   - **bold**, *italic*, `code`, ~~strike~~ render styled with their markers hidden
//   - [text](url) shows just the text, mod-click follows the link
// On the line the cursor is on, the raw markers are REVEALED so you can edit them —
// that line reads exactly as the file does on disk. Everything is derived from the
// Lezer markdown syntax tree, so it tracks the parser rather than re-implementing it.

import { syntaxTree } from '@codemirror/language';
import { type EditorState, type Extension, type Range } from '@codemirror/state';
import {
  Decoration,
  type DecorationSet,
  EditorView,
  ViewPlugin,
  type ViewUpdate,
} from '@codemirror/view';

export interface LiveMarkdownOptions {
  // Invoked when the user mod-clicks (⌘/Ctrl) a rendered link. Receives the raw href.
  onFollowLink?: (href: string) => void;
}

const HEADING_LEVEL: Record<string, number> = {
  ATXHeading1: 1,
  ATXHeading2: 2,
  ATXHeading3: 3,
  ATXHeading4: 4,
  ATXHeading5: 5,
  ATXHeading6: 6,
};

// Mark decorations (styling). Replace decorations (hide a marker range) are created
// inline because they carry no shared identity.
const STRONG = Decoration.mark({ class: 'cm-md-strong' });
const EMPHASIS = Decoration.mark({ class: 'cm-md-em' });
const INLINE_CODE = Decoration.mark({ class: 'cm-md-code' });
const STRIKE = Decoration.mark({ class: 'cm-md-strike' });
const HEADING_MARK = [1, 2, 3, 4, 5, 6].map((n) => Decoration.mark({ class: `cm-md-h${n}` }));
const HIDE = Decoration.replace({});

function linkMark(href: string): Decoration {
  return Decoration.mark({
    class: 'cm-md-link',
    attributes: { 'data-href': href },
  });
}

// Build the set of line numbers any selection range touches. A node rendered on one
// of these lines keeps its raw markers visible (the active-line reveal). Derived
// from the EditorState (not a view) so the decoration logic is unit-testable
// headlessly — building a state needs no DOM, mounting a view does.
function activeLines(state: EditorState): Set<number> {
  const lines = new Set<number>();
  for (const range of state.selection.ranges) {
    const first = state.doc.lineAt(range.from).number;
    const last = state.doc.lineAt(range.to).number;
    for (let n = first; n <= last; n += 1) lines.add(n);
  }
  return lines;
}

// `focused` gates the active-line reveal: an unfocused editor renders as fully clean
// markdown (nothing "active"), so a freshly-opened note reads like a rendered doc
// even though CM always keeps a selection at position 0. Once focused, the cursor's
// line reveals its raw markers for editing.
export function buildDecorations(state: EditorState, focused = true): DecorationSet {
  const decos: Range<Decoration>[] = [];
  const { doc } = state;
  const active = focused ? activeLines(state) : new Set<number>();
  const tree = syntaxTree(state);

  const onActiveLine = (pos: number) => active.has(doc.lineAt(pos).number);

  tree.iterate({
    enter: (node) => {
      const name = node.name;

      // ---- block: ATX headings ----
      const level = HEADING_LEVEL[name];
      if (level) {
        // Size the whole heading line; sizing persists even on the active line.
        decos.push(HEADING_MARK[level - 1].range(node.from, node.to));
        return;
      }

      // ---- inline styling spans ----
      if (name === 'StrongEmphasis') {
        decos.push(STRONG.range(node.from, node.to));
        return;
      }
      if (name === 'Emphasis') {
        decos.push(EMPHASIS.range(node.from, node.to));
        return;
      }
      if (name === 'InlineCode') {
        decos.push(INLINE_CODE.range(node.from, node.to));
        return;
      }
      if (name === 'Strikethrough') {
        decos.push(STRIKE.range(node.from, node.to));
        return;
      }
      if (name === 'Link') {
        const url = node.node.getChild('URL');
        const href = url ? doc.sliceString(url.from, url.to) : '';
        if (href) decos.push(linkMark(href).range(node.from, node.to));
        return;
      }

      // ---- markers we hide off the active line ----
      if (name === 'HeaderMark') {
        // Only ATX leading `#`s (a Setext underline is also a HeaderMark — leave it).
        if (doc.sliceString(node.from, node.from + 1) !== '#') return;
        if (onActiveLine(node.from)) return;
        // Swallow the single space after the `#`s so the title sits flush.
        let to = node.to;
        if (doc.sliceString(to, to + 1) === ' ') to += 1;
        decos.push(HIDE.range(node.from, to));
        return;
      }
      if (name === 'EmphasisMark') {
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        return;
      }
      if (name === 'StrikethroughMark') {
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        return;
      }
      if (name === 'CodeMark') {
        // Inline-code backticks only — never the ``` fences of a code block.
        if (node.node.parent?.name !== 'InlineCode') return;
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        return;
      }
      if (name === 'LinkMark') {
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
        return;
      }
      if (name === 'URL') {
        // The (url) tail of a [text](url) link — hidden; bare/autolink URLs stay.
        if (node.node.parent?.name !== 'Link') return;
        if (!onActiveLine(node.from)) decos.push(HIDE.range(node.from, node.to));
      }
    },
  });

  // CM requires decorations sorted by position (and side); let Decoration.set sort.
  return Decoration.set(decos, true);
}

const baseTheme = EditorView.baseTheme({
  '.cm-md-h1': { fontSize: '1.7em', fontWeight: '650', lineHeight: '1.3' },
  '.cm-md-h2': { fontSize: '1.4em', fontWeight: '630', lineHeight: '1.3' },
  '.cm-md-h3': { fontSize: '1.2em', fontWeight: '600' },
  '.cm-md-h4': { fontSize: '1.08em', fontWeight: '600' },
  '.cm-md-h5': { fontSize: '1em', fontWeight: '600' },
  '.cm-md-h6': { fontSize: '1em', fontWeight: '600', opacity: '0.85' },
  '.cm-md-strong': { fontWeight: '700' },
  '.cm-md-em': { fontStyle: 'italic' },
  '.cm-md-strike': { textDecoration: 'line-through', opacity: '0.75' },
  '.cm-md-code': {
    fontFamily:
      "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, Monaco, Consolas, monospace",
    fontSize: '0.92em',
    padding: '0.5px 4px',
    borderRadius: '4px',
    background: 'var(--color-bg-elevated, rgba(127,127,127,0.16))',
  },
  '.cm-md-link': { color: 'var(--accent, #ff6b35)', cursor: 'pointer' },
});

export function liveMarkdownPreview(options: LiveMarkdownOptions = {}): Extension {
  const plugin = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      constructor(view: EditorView) {
        this.decorations = buildDecorations(view.state, view.hasFocus);
      }
      update(update: ViewUpdate) {
        // Markers reveal/hide as the cursor moves and as focus changes (an unfocused
        // editor renders fully clean), so rebuild on selection and focus changes too,
        // not only document/viewport changes.
        if (update.docChanged || update.viewportChanged || update.selectionSet || update.focusChanged) {
          this.decorations = buildDecorations(update.state, update.view.hasFocus);
        }
      }
    },
    { decorations: (v) => v.decorations },
  );

  const followLinks = EditorView.domEventHandlers({
    mousedown: (event, view) => {
      if (!(event.metaKey || event.ctrlKey)) return false;
      const target = (event.target as HTMLElement | null)?.closest('.cm-md-link');
      const href = target?.getAttribute('data-href');
      if (!href) return false;
      event.preventDefault();
      options.onFollowLink?.(href);
      // Drop the selection CM would otherwise place at the click point.
      view.dispatch({ selection: { anchor: view.state.selection.main.head } });
      return true;
    },
  });

  return [plugin, baseTheme, followLinks];
}
