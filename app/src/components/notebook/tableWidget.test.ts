import { describe, expect, it } from 'vitest';
import { EditorState } from '@codemirror/state';
import { EditorView } from '@codemirror/view';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { parseTableData, markdownTables } from './tableWidget';

function stateFor(doc: string, selection: { anchor: number; head?: number }): EditorState {
  return EditorState.create({
    doc,
    selection,
    extensions: [markdown({ base: markdownLanguage }), markdownTables()],
  });
}

// The table field is private (only parseTableData/markdownTables are exported), so
// tests read its contribution to the standard decorations facet — resolvable from a
// bare EditorState since markdownTables()'s only decoration source is a StateField
// (Facet.from(field) needs no view). Counts block-replace ranges without depending on
// widget internals.
function replaceRangeCount(state: EditorState): number {
  let count = 0;
  for (const provider of state.facet(EditorView.decorations)) {
    const set = typeof provider === 'function' ? provider(state as unknown as EditorView) : provider;
    const iter = set.iter();
    while (iter.value) {
      count++;
      iter.next();
    }
  }
  return count;
}

describe('parseTableData', () => {
  it('parses header, alignment, rows, and fromLine', () => {
    const doc = '| a | b |\n| --- | :-: |\n| 1 | 2 |';
    const state = stateFor(doc, { anchor: 0 });
    const data = parseTableData(state, 0, doc.length);
    expect(data).toEqual({
      header: ['a', 'b'],
      align: [null, 'center'],
      rows: [['1', '2']],
      fromLine: 1,
    });
  });

  it('parses all four alignment forms', () => {
    const doc = '| a | b | c | d |\n| --- | :-- | --: | :-: |\n| 1 | 2 | 3 | 4 |';
    const state = stateFor(doc, { anchor: 0 });
    const data = parseTableData(state, 0, doc.length);
    expect(data?.align).toEqual([null, 'left', 'right', 'center']);
  });

  it('keeps inline markdown as raw cell text (v1 contract)', () => {
    const doc = '| a |\n| --- |\n| **x** |';
    const state = stateFor(doc, { anchor: 0 });
    const data = parseTableData(state, 0, doc.length);
    expect(data?.rows).toEqual([['**x**']]);
  });

  it('returns null for a range that is not a well-formed Table node', () => {
    const doc = 'just a paragraph, no table here';
    const state = stateFor(doc, { anchor: 0 });
    expect(parseTableData(state, 0, doc.length)).toBeNull();
  });
});

describe('markdownTables reveal gate', () => {
  const doc = '| a | b |\n| --- | :-: |\n| 1 | 2 |';

  it('renders one block-replace widget when the cursor is outside the table', () => {
    const state = stateFor(`${doc}\n\nafter`, { anchor: doc.length + 3 });
    expect(replaceRangeCount(state)).toBe(1);
  });

  it('reveals raw source (no decoration) when the cursor is on the header line', () => {
    const state = stateFor(`${doc}\n\nafter`, { anchor: 2 });
    expect(replaceRangeCount(state)).toBe(0);
  });

  it('reveals raw source when the cursor is on the delimiter line', () => {
    const delimiterPos = doc.indexOf('\n', doc.indexOf('\n') + 1) - 1;
    const state = stateFor(`${doc}\n\nafter`, { anchor: delimiterPos });
    expect(replaceRangeCount(state)).toBe(0);
  });

  it('reveals raw source when the cursor is on a body row', () => {
    const state = stateFor(`${doc}\n\nafter`, { anchor: doc.length - 2 });
    expect(replaceRangeCount(state)).toBe(0);
  });

  it('reveals when a selection range overlaps the table edge (anchor before, head inside)', () => {
    const state = stateFor(`${doc}\n\nafter`, { anchor: 0, head: 5 });
    expect(replaceRangeCount(state)).toBe(0);
  });

  it('reveals only the table the cursor is inside, with a second table intact', () => {
    const secondTable = '| c | d |\n| --- | --- |\n| 3 | 4 |';
    const full = `${doc}\n\n${secondTable}`;
    // Cursor on the first table's header line.
    const state = stateFor(full, { anchor: 2 });
    expect(replaceRangeCount(state)).toBe(1); // only the second table's widget remains
  });
});
