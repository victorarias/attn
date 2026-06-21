import { EditorSelection, EditorState } from '@codemirror/state';
import { type DecorationSet } from '@codemirror/view';
import { describe, expect, it } from 'vitest';
import { frontmatterCardDecorations } from './frontmatterCard';
import { parseFrontmatter } from './frontmatter';

// Pull the card's ranges out of the decoration set so the cursor/focus gate can be
// asserted without mounting a view (the widget's toDOM never runs headlessly).
function ranges(set: DecorationSet): { from: number; to: number; block: boolean }[] {
  const out: { from: number; to: number; block: boolean }[] = [];
  const iter = set.iter();
  while (iter.value) {
    out.push({ from: iter.from, to: iter.to, block: iter.value.spec?.block === true });
    iter.next();
  }
  return out;
}

function stateAt(doc: string, cursor: number): EditorState {
  return EditorState.create({ doc, selection: EditorSelection.cursor(cursor) });
}

const NOTE = ['---', 'title: Context rail', 'type: area', '---', '# Body', 'text'].join('\n');

describe('frontmatterCardDecorations', () => {
  it('renders a block-replace card over the whole frontmatter range when unfocused', () => {
    const state = stateAt(NOTE, 0); // selection sits at 0 (inside the block) but unfocused
    const got = ranges(frontmatterCardDecorations(state, false));
    const fm = parseFrontmatter(NOTE)!;
    expect(got).toEqual([{ from: 0, to: fm.to, block: true }]);
  });

  it('keeps the card when focused but the cursor is in the body', () => {
    const bodyPos = parseFrontmatter(NOTE)!.to + 2; // a few chars into the body
    const got = ranges(frontmatterCardDecorations(stateAt(NOTE, bodyPos), true));
    expect(got).toHaveLength(1);
  });

  it('reveals raw YAML (no card) when focused with the cursor inside the block', () => {
    const got = ranges(frontmatterCardDecorations(stateAt(NOTE, 6), true)); // inside `title:`
    expect(got).toHaveLength(0);
  });

  it('renders nothing when the document has no frontmatter', () => {
    const got = ranges(frontmatterCardDecorations(stateAt('# Just a heading\n\nbody', 0), false));
    expect(got).toHaveLength(0);
  });

  it('renders nothing for a frontmatter-only note (no body line to keep the cursor on)', () => {
    const got = ranges(frontmatterCardDecorations(stateAt('---\ntype: area\n---\n', 0), false));
    expect(got).toHaveLength(0);
  });
});
