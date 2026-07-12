// ⌘B/⌘I/⌘E toggle bold/italic/inline-code marks in the Notebook's live markdown editor.
// "Already formatted" is detected from the markdown parser's syntax tree, not by
// scanning characters around the cursor, so toggling emphasis inside a bold run wraps
// (adds `*…*`) rather than mistaking the enclosing StrongEmphasis for a match — Emphasis
// and StrongEmphasis are distinct Lezer node types.

import { EditorSelection, type EditorState, type Extension, type TransactionSpec } from '@codemirror/state';
import { EditorView, keymap, type KeyBinding } from '@codemirror/view';
import { ensureSyntaxTree, syntaxTree } from '@codemirror/language';
import type { SyntaxNode } from '@lezer/common';

export type InlineMarkType = 'strong' | 'emphasis' | 'code';

interface MarkConfig {
  nodeName: 'StrongEmphasis' | 'Emphasis' | 'InlineCode';
  markNodeName: 'EmphasisMark' | 'CodeMark';
  marker: string;
}

const CONFIGS: Record<InlineMarkType, MarkConfig> = {
  strong: { nodeName: 'StrongEmphasis', markNodeName: 'EmphasisMark', marker: '**' },
  emphasis: { nodeName: 'Emphasis', markNodeName: 'EmphasisMark', marker: '*' },
  code: { nodeName: 'InlineCode', markNodeName: 'CodeMark', marker: '`' },
};

// Walk up from `node` looking for an ancestor named `nodeName` whose span fully
// contains [from, to]. Because Emphasis and StrongEmphasis are separate node types,
// a lookup for one never matches the other.
function findEnclosing(node: SyntaxNode, nodeName: string, from: number, to: number): SyntaxNode | null {
  let cur: SyntaxNode | null = node;
  while (cur) {
    if (cur.name === nodeName && cur.from <= from && cur.to >= to) return cur;
    cur = cur.parent;
  }
  return null;
}

// Map a position through two deletions (the open/close mark ranges), both expressed
// in the original document's coordinates and applied as one local change set.
function afterDeletions(
  pos: number,
  openFrom: number,
  openTo: number,
  closeFrom: number,
  closeTo: number,
): number {
  let result = pos;
  if (pos >= openTo) result -= openTo - openFrom;
  if (pos >= closeTo) result -= closeTo - closeFrom;
  return result;
}

// Toggle `type` across every selection range via changeByRange, so multi-cursor
// selections all flip together. Returns null when the selection is empty of ranges
// (never happens in practice — EditorSelection always has at least one — but keeps
// the contract honest for callers).
export function toggleInlineFormat(state: EditorState, type: InlineMarkType): TransactionSpec | null {
  const config = CONFIGS[type];
  if (state.selection.ranges.length === 0) return null;

  return state.changeByRange((range) => {
    // The tree is available synchronously for the selection's vicinity; fall back to
    // whatever's already parsed if the budget is exhausted rather than blocking.
    const tree = ensureSyntaxTree(state, range.to, 50) ?? syntaxTree(state);
    const resolved = tree.resolveInner(range.from, 1);
    const enclosing = findEnclosing(resolved, config.nodeName, range.from, range.to);
    const marks = enclosing?.getChildren(config.markNodeName) ?? [];

    if (enclosing && marks.length >= 2) {
      // Unwrap: delete the mark nodes using their actual ranges (inline code can be
      // fenced with more than one backtick), keep the text between them.
      const open = marks[0];
      const close = marks[marks.length - 1];
      const changes = [
        { from: open.from, to: open.to, insert: '' },
        { from: close.from, to: close.to, insert: '' },
      ];
      const map = (pos: number) => afterDeletions(pos, open.from, open.to, close.from, close.to);
      return { changes, range: EditorSelection.range(map(range.from), map(range.to)) };
    }

    if (!range.empty) {
      // Wrap a nonempty selection: markers land outside it, selection stays on the text.
      const changes = [
        { from: range.from, insert: config.marker },
        { from: range.to, insert: config.marker },
      ];
      return {
        changes,
        range: EditorSelection.range(
          range.from + config.marker.length,
          range.to + config.marker.length,
        ),
      };
    }

    const word = state.wordAt(range.head);
    if (word) {
      // Empty selection on a word: wrap the whole word, keep the cursor at its
      // original position (now shifted past the opening marker).
      const changes = [
        { from: word.from, insert: config.marker },
        { from: word.to, insert: config.marker },
      ];
      return { changes, range: EditorSelection.cursor(range.head + config.marker.length) };
    }

    // Empty selection, no word under it (whitespace or an empty line): insert the
    // marker pair and place the cursor between them.
    const pair = config.marker + config.marker;
    return {
      changes: { from: range.head, insert: pair },
      range: EditorSelection.cursor(range.head + config.marker.length),
    };
  });
}

function toggleCommand(type: InlineMarkType) {
  return (view: EditorView): boolean => {
    const spec = toggleInlineFormat(view.state, type);
    if (!spec) return false;
    view.dispatch(spec);
    return true;
  };
}

// Explicit Cmd- (not Mod-) bindings: CodeMirror resolves Mod- via navigator.platform
// sniffing, which is wrong on non-macOS browsers (e.g. Linux CI). This app is
// macOS-only, so Cmd is always the correct key.
export function formattingKeymap(): Extension {
  const bindings: KeyBinding[] = [
    { key: 'Cmd-b', run: toggleCommand('strong') },
    { key: 'Cmd-i', run: toggleCommand('emphasis') },
    { key: 'Cmd-e', run: toggleCommand('code') },
  ];
  return keymap.of(bindings);
}
