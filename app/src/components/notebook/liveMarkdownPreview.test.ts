import { describe, expect, it } from 'vitest';
import { EditorState } from '@codemirror/state';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { buildDecorations } from './liveMarkdownPreview';

// buildDecorations works off an EditorState (no view), so the live-preview logic is
// testable headlessly — CodeMirror's view can't mount under happy-dom, but a state
// needs no DOM. Each test builds a state with the cursor placed deliberately, since
// the cursor's line reveals its raw markers (the Obsidian active-line behavior).
interface Deco {
  from: number;
  to: number;
  class?: string;
  // The `cls` of a widget-replace (bullet/checkbox), so widgets are distinguishable
  // from plain hides in the headless test (their spec has no `class`).
  widget?: string;
  attributes?: Record<string, string>;
}

function decosFor(doc: string, cursor: number, focused = true): Deco[] {
  const state = EditorState.create({
    doc,
    selection: { anchor: cursor },
    extensions: [markdown({ base: markdownLanguage })],
  });
  const out: Deco[] = [];
  const iter = buildDecorations(state, focused).iter();
  while (iter.value) {
    const spec = iter.value.spec as {
      class?: string;
      attributes?: Record<string, string>;
      widget?: { cls?: string };
    };
    out.push({
      from: iter.from,
      to: iter.to,
      class: spec.class,
      widget: spec.widget?.cls,
      attributes: spec.attributes,
    });
    iter.next();
  }
  return out;
}

// A hide decoration is a replace with no class AND no widget (Decoration.replace({})).
const isHide = (d: Deco) => d.class === undefined && d.widget === undefined;
const hasClass = (decos: Deco[], cls: string) => decos.some((d) => d.class === cls);
const hasWidget = (decos: Deco[], cls: string) => decos.some((d) => d.widget === cls);
const hideAt = (decos: Deco[], from: number, to: number) =>
  decos.some((d) => isHide(d) && d.from === from && d.to === to);

describe('liveMarkdownPreview decorations', () => {
  it('sizes a heading and hides its leading "# " off the active line', () => {
    const doc = '# Title\n\nbody';
    const decos = decosFor(doc, doc.length); // cursor on the "body" line

    // The whole heading line (0..7) is sized.
    expect(decos.some((d) => d.class === 'cm-md-h1' && d.from === 0 && d.to === 7)).toBe(true);
    // The "# " marker (including the trailing space) is hidden.
    expect(hideAt(decos, 0, 2)).toBe(true);
  });

  it('decorates syntax beyond CodeMirror\'s initial 3,000-character parse window', () => {
    const prefix = `${'ordinary paragraph text '.repeat(140)}\n\n`;
    const heading = '### Stage 2 remains rendered';
    const doc = `${prefix}${heading}\n\nbody`;
    const headingFrom = doc.indexOf(heading);
    expect(headingFrom).toBeGreaterThan(3000);

    const decos = decosFor(doc, doc.length, false);
    expect(
      decos.some(
        (d) => d.class === 'cm-md-h3' && d.from === headingFrom && d.to === headingFrom + heading.length,
      ),
    ).toBe(true);
    expect(hideAt(decos, headingFrom, headingFrom + 4)).toBe(true);
  });

  it('reveals the heading marker when the cursor is on the heading line', () => {
    const doc = '# Title\n\nbody';
    const decos = decosFor(doc, 1); // cursor inside the heading line

    // Sizing persists on the active line...
    expect(hasClass(decos, 'cm-md-h1')).toBe(true);
    // ...but the raw "# " marker is shown (not hidden) so it can be edited.
    expect(hideAt(decos, 0, 2)).toBe(false);
  });

  it('hides all markers when the editor is unfocused, even on the cursor line', () => {
    const doc = '# Title\n\nbody';
    // Cursor on the heading line, but unfocused → renders fully clean (a freshly
    // opened note reads like rendered markdown until the user clicks in).
    const decos = decosFor(doc, 1, false);

    expect(hasClass(decos, 'cm-md-h1')).toBe(true); // still sized
    expect(hideAt(decos, 0, 2)).toBe(true); // "# " hidden despite the cursor
  });

  it('styles bold / italic / code / strikethrough and hides their markers off the active line', () => {
    const doc = 'x **b** *i* `c` ~~s~~\nsecond';
    const decos = decosFor(doc, doc.length); // cursor on the second line

    expect(hasClass(decos, 'cm-md-strong')).toBe(true);
    expect(hasClass(decos, 'cm-md-em')).toBe(true);
    expect(hasClass(decos, 'cm-md-code')).toBe(true);
    expect(hasClass(decos, 'cm-md-strike')).toBe(true);
    // The surrounding **, *, `, ~~ markers are hidden.
    expect(decos.filter(isHide).length).toBeGreaterThan(0);
  });

  it('reveals inline markers when the cursor is on their line', () => {
    const doc = 'x **b** *i*\nsecond';
    const decos = decosFor(doc, 3); // cursor inside the styled first line

    // The styling still applies, but no markers are hidden on the active line.
    expect(hasClass(decos, 'cm-md-strong')).toBe(true);
    expect(decos.filter(isHide).length).toBe(0);
  });

  it('renders a link as its text, carrying the href, and hides the URL/brackets', () => {
    const doc = 'see [the note](/knowledge/areas/foo.md) here\nsecond';
    const decos = decosFor(doc, doc.length); // cursor on the second line

    const link = decos.find((d) => d.class === 'cm-md-link');
    expect(link).toBeDefined();
    expect(link?.attributes?.['data-href']).toBe('/knowledge/areas/foo.md');
    // The "](...)" tail and brackets are hidden, leaving just the link text.
    expect(decos.filter(isHide).length).toBeGreaterThan(0);
  });

  it('does not hide the ``` fence of a fenced code block as inline code', () => {
    const doc = '```\ncode\n```\n\nbody';
    const decos = decosFor(doc, doc.length); // cursor on "body", away from the fence

    // Fenced code is not inline code: no cm-md-code styling is applied to the fence,
    // and the backtick fence rows are not treated as inline-code markers to hide.
    expect(hasClass(decos, 'cm-md-code')).toBe(false);
  });

  it('replaces a bullet marker with a bullet widget off the active line', () => {
    const doc = '- item\n\nbody';
    const decos = decosFor(doc, doc.length); // cursor on "body"
    // The "-" marker (0..1) is replaced by the bullet widget.
    expect(decos.some((d) => d.widget === 'cm-md-bullet' && d.from === 0 && d.to === 1)).toBe(true);
  });

  it('reveals the raw bullet marker on the active line', () => {
    const doc = '- item\n\nbody';
    const decos = decosFor(doc, 2); // cursor inside the list line
    expect(hasWidget(decos, 'cm-md-bullet')).toBe(false);
  });

  it('leaves an ordered-list number as written', () => {
    const doc = '1. item\n\nbody';
    const decos = decosFor(doc, doc.length);
    // No bullet widget and no hide over the "1." marker — numbers are meaningful.
    expect(hasWidget(decos, 'cm-md-bullet')).toBe(false);
    expect(decos.some((d) => isHide(d) && d.from === 0)).toBe(false);
  });

  it('renders a task checkbox (hiding its bullet) and reflects checked state', () => {
    const unchecked = decosFor('- [ ] todo\n\nbody', '- [ ] todo\n\nbody'.length);
    // The "- " bullet+space is hidden; the "[ ]" marker becomes a checkbox widget.
    expect(hideAt(unchecked, 0, 2)).toBe(true);
    expect(hasWidget(unchecked, 'cm-md-bullet')).toBe(false);
    expect(unchecked.some((d) => d.widget === 'cm-md-checkbox' && d.from === 2 && d.to === 5)).toBe(true);

    const checked = decosFor('- [x] done\n\nbody', '- [x] done\n\nbody'.length);
    expect(checked.some((d) => d.widget === 'cm-md-checkbox' && d.from === 2 && d.to === 5)).toBe(true);
  });

  it('reveals the raw task marker on the active line', () => {
    const doc = '- [ ] todo\n\nbody';
    const decos = decosFor(doc, 3); // cursor inside the task line
    expect(hasWidget(decos, 'cm-md-checkbox')).toBe(false);
  });

  it('paints a fenced code block (lines + dimmed fences + language tag)', () => {
    const doc = '```ts\ncode\n```\n\nbody';
    const decos = decosFor(doc, doc.length); // cursor on "body"
    // Each of the three fenced rows (starts at 0, 6, 11) gets the code-panel line deco.
    expect(decos.filter((d) => d.class === 'cm-md-codeblock').map((d) => d.from)).toEqual([0, 6, 11]);
    // The fences are dimmed, the language tag styled, and it is NOT inline code.
    expect(hasClass(decos, 'cm-md-codefence')).toBe(true);
    expect(hasClass(decos, 'cm-md-codeinfo')).toBe(true);
    expect(hasClass(decos, 'cm-md-code')).toBe(false);
  });

  it('paints every line of a multi-line blockquote and hides its ">" marks off the active line', () => {
    const doc = '> line one\n> line two\n\nbody';
    const decos = decosFor(doc, doc.length); // cursor on "body"
    // Both quote lines (starts at 0, 11) get the blockquote line deco.
    expect(decos.filter((d) => d.class === 'cm-md-blockquote').map((d) => d.from)).toEqual([0, 11]);
    // Both "> " marks (including the following space) are hidden.
    expect(hideAt(decos, 0, 2)).toBe(true);
    expect(hideAt(decos, 11, 13)).toBe(true);
  });

  it('reveals the ">" mark on the active blockquote line while keeping the line styling', () => {
    const doc = '> line one\n> line two\n\nbody';
    const decos = decosFor(doc, 2); // cursor inside the first quote line
    expect(decos.filter((d) => d.class === 'cm-md-blockquote').map((d) => d.from)).toEqual([0, 11]);
    expect(hideAt(decos, 0, 2)).toBe(false); // revealed on its own line
    expect(hideAt(decos, 11, 13)).toBe(true); // still hidden on the other line
  });

  it('hides both marks of a nested blockquote off the active line', () => {
    const doc = '> > x\n\nbody';
    const decos = decosFor(doc, doc.length); // cursor on "body"
    expect(hideAt(decos, 0, 2)).toBe(true); // outer "> "
    expect(hideAt(decos, 2, 4)).toBe(true); // inner "> "
  });

  it('replaces a standalone "---" with the hr widget off the active line, and reveals it on its own line', () => {
    const doc = 'para one\n\n---\n\npara two';
    const decosOff = decosFor(doc, doc.length); // cursor on "para two"
    expect(decosOff.some((d) => d.widget === 'cm-md-hr' && d.from === 10 && d.to === 13)).toBe(true);

    const decosOn = decosFor(doc, 10); // cursor on the "---" line
    expect(hasWidget(decosOn, 'cm-md-hr')).toBe(false);
  });

  it('does not treat a Setext heading underline as a horizontal rule', () => {
    const doc = 'title\n---\n\nbody';
    const decos = decosFor(doc, doc.length); // cursor on "body"
    expect(hasWidget(decos, 'cm-md-hr')).toBe(false);
  });
});
