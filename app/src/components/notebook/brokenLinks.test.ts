import { describe, expect, it } from 'vitest';
import { EditorState } from '@codemirror/state';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { brokenLinkDecorations, notebookLinkPath, notebookLinkPaths } from './brokenLinks';

// The pure helpers work off an EditorState (no view, no daemon), so the broken-link
// logic is testable headlessly — the async existence cache lives in the plugin, but
// "which paths to check" and "which links to flag" are pure over the parsed state.
function stateOf(doc: string): EditorState {
  return EditorState.create({ doc, extensions: [markdown({ base: markdownLanguage })] });
}

interface Mark {
  from: number;
  to: number;
  class?: string;
}

function marksFor(doc: string, baseDir: string, missing: (path: string) => boolean): Mark[] {
  const out: Mark[] = [];
  const iter = brokenLinkDecorations(stateOf(doc), baseDir, missing).iter();
  while (iter.value) {
    out.push({ from: iter.from, to: iter.to, class: (iter.value.spec as { class?: string }).class });
    iter.next();
  }
  return out;
}

describe('notebookLinkPath', () => {
  it('resolves a root-absolute note link to its (no-leading-slash) path', () => {
    expect(notebookLinkPath('/knowledge/areas/foo.md', '')).toBe('knowledge/areas/foo.md');
  });

  it('resolves a bare relative link inside a note in a subdirectory against that directory (behavior change: this used to require a leading slash and was otherwise treated as external)', () => {
    expect(notebookLinkPath('sibling.md', 'knowledge/areas')).toBe('knowledge/areas/sibling.md');
  });

  it('drops the #fragment and ?query tail (they address within a note)', () => {
    expect(notebookLinkPath('/knowledge/foo.md#section', '')).toBe('knowledge/foo.md');
    expect(notebookLinkPath('/knowledge/foo.md?v=2', '')).toBe('knowledge/foo.md');
  });

  it('never flags an external URL', () => {
    expect(notebookLinkPath('http://example.com', '')).toBeNull();
    expect(notebookLinkPath('https://example.com/x', '')).toBeNull();
    expect(notebookLinkPath('mailto:a@b.com', '')).toBeNull();
    expect(notebookLinkPath('file:///etc/hosts', '')).toBeNull();
    expect(notebookLinkPath('//cdn.example.com/x', '')).toBeNull();
  });

  it('never flags a pure in-document anchor or an empty href', () => {
    expect(notebookLinkPath('#section', '')).toBeNull();
    expect(notebookLinkPath('', '')).toBeNull();
    expect(notebookLinkPath('   ', '')).toBeNull();
  });
});

describe('notebookLinkPaths', () => {
  it('collects only the in-notebook link paths, deduped', () => {
    const doc = [
      'See [a](/knowledge/a.md) and [b](https://x.com) and [c](/knowledge/c.md).',
      'Also [again](/knowledge/a.md) and the [top](#intro).',
    ].join('\n');
    expect(notebookLinkPaths(stateOf(doc), '').sort()).toEqual(['knowledge/a.md', 'knowledge/c.md']);
  });

  it('returns nothing for a document with no in-notebook links', () => {
    expect(notebookLinkPaths(stateOf('Just [external](https://x.com) here.'), '')).toEqual([]);
  });
});

describe('brokenLinkDecorations', () => {
  it('flags exactly the links whose target is reported missing', () => {
    const doc = 'See [gone](/knowledge/gone.md) and [here](/knowledge/here.md).';
    const missing = (p: string) => p === 'knowledge/gone.md';
    const marks = marksFor(doc, '', missing);
    expect(marks).toHaveLength(1);
    expect(marks[0].class).toBe('cm-md-link-broken');
    // The mark spans the whole [text](url) link, not just its text.
    expect(doc.slice(marks[0].from, marks[0].to)).toBe('[gone](/knowledge/gone.md)');
  });

  it('existence-checks a bare-relative link against the linking note\'s own directory', () => {
    // A note at knowledge/areas/note.md linking bare `sibling.md` must check
    // knowledge/areas/sibling.md, not sibling.md at the root — this is the behavior
    // change PR8 makes.
    const doc = 'See [sib](sibling.md).';
    const missingAtRoot = (p: string) => p === 'sibling.md';
    expect(marksFor(doc, 'knowledge/areas', missingAtRoot)).toHaveLength(0);
    const missingAtSiblingDir = (p: string) => p === 'knowledge/areas/sibling.md';
    expect(marksFor(doc, 'knowledge/areas', missingAtSiblingDir)).toHaveLength(1);
  });

  it('flags nothing when every target exists (the predicate reports none missing)', () => {
    const doc = 'See [a](/knowledge/a.md) and [b](/knowledge/b.md).';
    expect(marksFor(doc, '', () => false)).toHaveLength(0);
  });

  it('never flags an external link even if the predicate would match its raw href', () => {
    const doc = 'Read [docs](https://example.com/x).';
    // notebookLinkPath returns null for the URL, so the predicate is never consulted.
    expect(marksFor(doc, '', () => true)).toHaveLength(0);
  });
});
