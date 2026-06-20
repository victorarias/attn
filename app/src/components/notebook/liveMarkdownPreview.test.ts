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
    const spec = iter.value.spec as { class?: string; attributes?: Record<string, string> };
    out.push({ from: iter.from, to: iter.to, class: spec.class, attributes: spec.attributes });
    iter.next();
  }
  return out;
}

// A hide decoration is a replace with no class (Decoration.replace({})).
const isHide = (d: Deco) => d.class === undefined;
const hasClass = (decos: Deco[], cls: string) => decos.some((d) => d.class === cls);
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
});
