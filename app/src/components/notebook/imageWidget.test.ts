import { describe, expect, it } from 'vitest';
import { EditorSelection, EditorState } from '@codemirror/state';
import { EditorView } from '@codemirror/view';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { imageTargets, imageWidget } from './imageWidget';

function stateFor(doc: string, selection?: { anchor: number; head?: number }): EditorState {
  return EditorState.create({
    doc,
    selection: selection && EditorSelection.create([EditorSelection.range(selection.anchor, selection.head ?? selection.anchor)]),
    extensions: [markdown({ base: markdownLanguage }), imageWidget()],
  });
}

// The image field is private (only imageTargets/imageWidget are exported), so this
// mirrors tableWidget.test.ts: read the field's contribution to the standard
// decorations facet, resolvable from a bare EditorState since imageWidget()'s only
// decoration source is a StateField.
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

describe('imageTargets', () => {
  it('finds an own-line image with its alt and src', () => {
    const doc = 'Some text.\n\n![a cat](assets/cat.png)\n\nMore text.';
    const targets = imageTargets(stateFor(doc));
    expect(targets).toHaveLength(1);
    expect(targets[0].alt).toBe('a cat');
    expect(targets[0].src).toBe('assets/cat.png');
    const line = doc.split('\n')[2];
    expect(targets[0].lineFrom).toBe(doc.indexOf(line));
    expect(targets[0].lineTo).toBe(doc.indexOf(line) + line.length);
  });

  it('allows surrounding whitespace on the image line', () => {
    const doc = '  ![a cat](assets/cat.png)  \n';
    expect(imageTargets(stateFor(doc))).toHaveLength(1);
  });

  it('does not return an inline image mid-paragraph', () => {
    const doc = 'Look at this ![a cat](assets/cat.png) right here.';
    expect(imageTargets(stateFor(doc))).toHaveLength(0);
  });

  it('does not return a line with text after the image', () => {
    const doc = '![a cat](assets/cat.png) — isn\'t she cute?';
    expect(imageTargets(stateFor(doc))).toHaveLength(0);
  });

  it('does not return either image on a line with two images', () => {
    const doc = '![one](a.png)![two](b.png)';
    expect(imageTargets(stateFor(doc))).toHaveLength(0);
  });
});

describe('imageWidget reveal gate', () => {
  const doc = 'Before.\n\n![a cat](assets/cat.png)\n\nAfter.';
  const imageLine = doc.split('\n')[2];
  const imageLineFrom = doc.indexOf(imageLine);

  it('renders one block-replace widget when the selection is elsewhere', () => {
    const state = stateFor(doc, { anchor: 0 });
    expect(replaceRangeCount(state)).toBe(1);
  });

  it('reveals raw source (no decoration) when the selection touches the image line', () => {
    const state = stateFor(doc, { anchor: imageLineFrom + 2 });
    expect(replaceRangeCount(state)).toBe(0);
  });

  it('reveals when a selection range overlaps the image line edge', () => {
    const state = stateFor(doc, { anchor: 0, head: imageLineFrom + 2 });
    expect(replaceRangeCount(state)).toBe(0);
  });
});
