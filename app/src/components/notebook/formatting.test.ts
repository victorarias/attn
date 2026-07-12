import { describe, expect, it } from 'vitest';
import { EditorSelection, EditorState } from '@codemirror/state';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { toggleInlineFormat, type InlineMarkType } from './formatting';

function stateFor(doc: string, selection: { anchor: number; head?: number }): EditorState {
  return EditorState.create({
    doc,
    selection,
    extensions: [markdown({ base: markdownLanguage })],
  });
}

describe('toggleInlineFormat', () => {
  it('wraps a nonempty selection with strong markers, selecting the wrapped text', () => {
    const doc = 'hello world';
    const state = stateFor(doc, { anchor: 6, head: 11 }); // "world"
    const next = state.update(toggleInlineFormat(state, 'strong')!);
    expect(next.state.doc.toString()).toBe('hello **world**');
    const range = next.state.selection.main;
    expect(next.state.sliceDoc(range.from, range.to)).toBe('world');
  });

  it('unwraps when the cursor is inside a StrongEmphasis node', () => {
    const doc = '**world**';
    const state = stateFor(doc, { anchor: 4 }); // inside "world"
    const next = state.update(toggleInlineFormat(state, 'strong')!);
    expect(next.state.doc.toString()).toBe('world');
  });

  it('wraps the whole word under a bare cursor, keeping the cursor within it', () => {
    const doc = 'hello world';
    const state = stateFor(doc, { anchor: 8 }); // mid "world"
    const next = state.update(toggleInlineFormat(state, 'strong')!);
    expect(next.state.doc.toString()).toBe('hello **world**');
    const pos = next.state.selection.main.head;
    expect(pos).toBeGreaterThanOrEqual('hello **'.length);
    expect(pos).toBeLessThanOrEqual('hello **world'.length);
  });

  it('inserts an empty marker pair with a centered cursor when there is no word', () => {
    const doc = 'hello   world'; // cursor sits in the run of spaces (index 6)
    const state = stateFor(doc, { anchor: 6 });
    const next = state.update(toggleInlineFormat(state, 'strong')!);
    expect(next.state.doc.toString()).toBe(doc.slice(0, 6) + '****' + doc.slice(6));
    const head = next.state.selection.main.head;
    expect(next.state.sliceDoc(head - 2, head)).toBe('**');
    expect(next.state.sliceDoc(head, head + 2)).toBe('**');
  });

  it('emphasis inside bold wraps the word rather than stripping the bold', () => {
    const doc = '**bold**';
    const state = stateFor(doc, { anchor: 4 }); // inside "bold", within the StrongEmphasis
    const next = state.update(toggleInlineFormat(state, 'emphasis')!);
    expect(next.state.doc.toString()).toBe('***bold***');
  });

  it('unwraps inline code using the CodeMark node ranges, including double backticks', () => {
    const doc = 'see ``x`` here';
    const state = stateFor(doc, { anchor: 6 }); // inside the double-backtick span
    const next = state.update(toggleInlineFormat(state, 'code')!);
    expect(next.state.doc.toString()).toBe('see x here');
  });

  it('wraps two cursors on two different words independently', () => {
    const doc = 'alpha beta';
    const base = EditorState.create({
      doc,
      // Multi-range selections are collapsed to one by default; opt in explicitly.
      extensions: [markdown({ base: markdownLanguage }), EditorState.allowMultipleSelections.of(true)],
    });
    const state = base.update({
      selection: EditorSelection.create([EditorSelection.cursor(2), EditorSelection.cursor(8)]),
    }).state;
    const next = state.update(toggleInlineFormat(state, 'strong')!);
    expect(next.state.doc.toString()).toBe('**alpha** **beta**');
  });

  it('round-trips wrap then unwrap back to the original doc (strong, emphasis, code)', () => {
    const cases: Array<[InlineMarkType, string]> = [
      ['strong', 'hello world'],
      ['emphasis', 'hello world'],
      ['code', 'hello world'],
    ];
    for (const [type, doc] of cases) {
      const state = stateFor(doc, { anchor: 6, head: 11 });
      const wrapped = state.update(toggleInlineFormat(state, type)!);
      const unwrapped = wrapped.state.update(toggleInlineFormat(wrapped.state, type)!);
      expect(unwrapped.state.doc.toString()).toBe(doc);
    }
  });
});
