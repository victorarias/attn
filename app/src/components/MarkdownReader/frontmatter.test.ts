import { describe, expect, it } from 'vitest';
import { extractFrontmatter } from './frontmatter';

const NONE = { entries: [], lineCount: 0 };

describe('extractFrontmatter', () => {
  it('returns no entries for documents without frontmatter', () => {
    expect(extractFrontmatter('# Title\n\nBody')).toEqual({ entries: [], lineCount: 0 });
  });

  it('returns no entries when the fence never closes (leading hr, not frontmatter)', () => {
    expect(extractFrontmatter('---\nstill text')).toEqual({ entries: [], lineCount: 0 });
  });

  it('parses scalars, quoted strings, numbers, and booleans as strings', () => {
    const { entries, lineCount } = extractFrontmatter(
      '---\ntitle: "My Plan"\nstatus: draft\npriority: 2\ndone: false\n---\n# Body\n',
    );
    expect(entries).toEqual([
      { key: 'title', value: 'My Plan' },
      { key: 'status', value: 'draft' },
      { key: 'priority', value: '2' },
      { key: 'done', value: 'false' },
    ]);
    expect(lineCount).toBe(6);
  });

  it('parses inline arrays and dash lists', () => {
    const { entries } = extractFrontmatter(
      "---\ntags: [alpha, 'beta', \"gamma\"]\nowners:\n  - victor\n  - attn\n---\nBody",
    );
    expect(entries).toEqual([
      { key: 'tags', value: ['alpha', 'beta', 'gamma'] },
      { key: 'owners', value: ['victor', 'attn'] },
    ]);
  });

  it('skips nested objects, comments, and blank lines', () => {
    const { entries } = extractFrontmatter(
      '---\n# a comment\ntitle: ok\nnested:\n  inner: value\n\ndate: 2026-07-14\n---\nBody',
    );
    expect(entries).toEqual([
      { key: 'title', value: 'ok' },
      { key: 'date', value: '2026-07-14' },
    ]);
  });

  it('counts delimiter lines in lineCount (the strip-side lineOffset)', () => {
    // ---            line 1
    // title: x       line 2
    // ---            line 3
    const { lineCount } = extractFrontmatter('---\ntitle: x\n---\nFirst paragraph\n');
    expect(lineCount).toBe(3);
  });

  it('rejects the YAML document-end marker, matching remark-frontmatter', () => {
    // remark-frontmatter only closes on `---`; with `...` it renders the block
    // as prose, so extracting a card here would show the frontmatter twice.
    expect(extractFrontmatter('---\ntitle: x\n...\nBody\n')).toEqual(NONE);
  });

  it('rejects indented fences, matching remark-frontmatter', () => {
    expect(extractFrontmatter(' ---\ntitle: x\n ---\nBody\n')).toEqual(NONE);
    expect(extractFrontmatter('---\ntitle: x\n  ---\nBody\n')).toEqual(NONE);
  });

  it('accepts fences with trailing whitespace and CRLF line endings', () => {
    const { entries, lineCount } = extractFrontmatter('--- \r\ntitle: x\r\n---\t\r\nBody\r\n');
    expect(entries).toEqual([{ key: 'title', value: 'x' }]);
    expect(lineCount).toBe(3);
  });
});
