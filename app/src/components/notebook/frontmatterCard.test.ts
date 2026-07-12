import { EditorSelection, EditorState } from '@codemirror/state';
import { type DecorationSet } from '@codemirror/view';
import { describe, expect, it } from 'vitest';
import { frontmatterCardDecorations } from './frontmatterCard';
import { FRONTMATTER_SCAN_LIMIT, parseFrontmatter } from './frontmatter';

// Pull the card's ranges out of the decoration set so the explicit-edit gate can be
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

const NOTE = ['---', 'type: area', 'tags: [a, b]', '---', '# Body', 'text'].join('\n');

describe('frontmatterCardDecorations', () => {
  it('renders a block-replace card when property editing is inactive', () => {
    const state = stateAt(NOTE, 0); // CM's initial selection sits inside the hidden block
    const got = ranges(frontmatterCardDecorations(state, false));
    const fm = parseFrontmatter(NOTE)!;
    expect(got).toEqual([{ from: 0, to: fm.to, block: true }]);
  });

  it('keeps the card when edit mode is active but the cursor is in the body', () => {
    const bodyPos = parseFrontmatter(NOTE)!.to + 2; // a few chars into the body
    const got = ranges(frontmatterCardDecorations(stateAt(NOTE, bodyPos), true));
    expect(got).toHaveLength(1);
  });

  it('reveals raw YAML when explicit edit mode is active inside the block', () => {
    const got = ranges(frontmatterCardDecorations(stateAt(NOTE, 6), true)); // inside `type:`
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

  it('still renders the card when the body (not the frontmatter) exceeds the scan limit', () => {
    const bigBody = '# Body\n' + 'x'.repeat(FRONTMATTER_SCAN_LIMIT * 2);
    const note = ['---', 'type: area', '---', bigBody].join('\n');
    const got = ranges(frontmatterCardDecorations(stateAt(note, 0), false));
    const fm = parseFrontmatter(note.slice(0, FRONTMATTER_SCAN_LIMIT))!;
    expect(got).toEqual([{ from: 0, to: fm.to, block: true }]);
  });

  it('treats an opening fence with no closing fence within the scan limit as no frontmatter', () => {
    const note = '---\n' + 'padding line\n'.repeat(400) + '---\n# Body\n';
    expect(note.indexOf('\n---\n', 4)).toBeGreaterThan(FRONTMATTER_SCAN_LIMIT);
    const got = ranges(frontmatterCardDecorations(stateAt(note, 0), false));
    expect(got).toHaveLength(0);
  });
});
