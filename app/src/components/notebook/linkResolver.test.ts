import { describe, expect, it } from 'vitest';
import { headingSlug, noteDir, resolveNotebookLink } from './linkResolver';

describe('resolveNotebookLink', () => {
  it('resolves a bare relative link against the linking note\'s directory', () => {
    expect(resolveNotebookLink('foo.md', 'knowledge/areas')).toEqual({
      kind: 'note',
      path: 'knowledge/areas/foo.md',
      anchor: undefined,
    });
  });

  it('climbs a directory with ..', () => {
    expect(resolveNotebookLink('../bar.md', 'knowledge/areas')).toEqual({
      kind: 'note',
      path: 'knowledge/bar.md',
      anchor: undefined,
    });
  });

  it('clamps at the notebook root when .. climbs past it', () => {
    expect(resolveNotebookLink('../../../x.md', 'a')).toEqual({
      kind: 'note',
      path: 'x.md',
      anchor: undefined,
    });
  });

  it('treats a leading slash as root-relative, ignoring baseDir', () => {
    expect(resolveNotebookLink('/abs.md', 'knowledge/areas')).toEqual({
      kind: 'note',
      path: 'abs.md',
      anchor: undefined,
    });
  });

  it('decodes a fragment-only href', () => {
    expect(resolveNotebookLink('#Sec%20One', 'knowledge/areas')).toEqual({
      kind: 'fragment',
      anchor: 'Sec One',
    });
  });

  it('classifies a scheme URL as external', () => {
    expect(resolveNotebookLink('https://example.com', '').kind).toBe('external');
  });

  it('classifies a protocol-relative URL as external', () => {
    expect(resolveNotebookLink('//example.com/x', '').kind).toBe('external');
  });

  it('classifies mailto as external', () => {
    expect(resolveNotebookLink('mailto:someone@example.com', '').kind).toBe('external');
  });

  it('splits a query and anchor off a note link', () => {
    expect(resolveNotebookLink('foo.md?x=1#sec', 'knowledge/areas')).toEqual({
      kind: 'note',
      path: 'knowledge/areas/foo.md',
      anchor: 'sec',
    });
  });

  it('treats an empty href as external empty', () => {
    expect(resolveNotebookLink('', 'knowledge/areas')).toEqual({ kind: 'external', href: '' });
    expect(resolveNotebookLink('   ', 'knowledge/areas')).toEqual({ kind: 'external', href: '' });
  });
});

describe('noteDir', () => {
  it('returns the directory of a nested note path', () => {
    expect(noteDir('knowledge/areas/foo.md')).toBe('knowledge/areas');
  });

  it('returns empty for a root-level note path', () => {
    expect(noteDir('foo.md')).toBe('');
  });
});

describe('headingSlug', () => {
  it('lowercases and strips punctuation', () => {
    expect(headingSlug('My Heading!')).toBe('my-heading');
  });

  it('collapses multiple spaces into one hyphen', () => {
    expect(headingSlug('A   B')).toBe('a-b');
  });
});
