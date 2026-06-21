import { describe, expect, it } from 'vitest';
import { parseFrontmatter } from './frontmatter';

describe('parseFrontmatter', () => {
  it('parses scalars, flow lists, and computes the body boundary', () => {
    const doc = ['---', 'type: area', 'title: Context rail', 'tags: [a, b, c]', '---', '# Body', 'text'].join('\n');
    const fm = parseFrontmatter(doc);
    expect(fm).not.toBeNull();
    expect(fm!.fields).toEqual({ type: 'area', title: 'Context rail', tags: ['a', 'b', 'c'] });
    expect(fm!.from).toBe(0);
    // `to` lands at the start of the body, so slicing from it begins at the body.
    expect(doc.slice(fm!.to)).toBe('# Body\ntext');
  });

  it('parses a block list (key: then indented - items)', () => {
    const doc = ['---', 'sources:', '  - /knowledge/a.md', '  - https://example.com', '---', 'body'].join('\n');
    const fm = parseFrontmatter(doc);
    expect(fm!.fields.sources).toEqual(['/knowledge/a.md', 'https://example.com']);
  });

  it('strips surrounding quotes from scalars and list items', () => {
    const doc = ['---', 'title: "Quoted title"', "summary: 'single'", 'tags: ["x", "y"]', '---', ''].join('\n');
    const fm = parseFrontmatter(doc);
    expect(fm!.fields.title).toBe('Quoted title');
    expect(fm!.fields.summary).toBe('single');
    expect(fm!.fields.tags).toEqual(['x', 'y']);
  });

  it('returns null when the document does not open with a fence', () => {
    expect(parseFrontmatter('# Just a heading\n\nbody')).toBeNull();
    // A `---` that is not on line 1 is a horizontal rule, not frontmatter.
    expect(parseFrontmatter('intro\n\n---\ntype: x\n---\n')).toBeNull();
  });

  it('returns null for an unterminated block', () => {
    expect(parseFrontmatter('---\ntype: area\nno closing fence\n')).toBeNull();
  });

  it('ignores blank lines and comments inside the block', () => {
    const doc = ['---', '# a yaml comment', 'type: area', '', 'title: T', '---', 'body'].join('\n');
    const fm = parseFrontmatter(doc);
    expect(fm!.fields).toEqual({ type: 'area', title: 'T' });
  });

  it('accepts the ... closing fence and clamps to with no trailing newline', () => {
    const doc = '---\ntype: area\n...';
    const fm = parseFrontmatter(doc);
    expect(fm!.fields.type).toBe('area');
    expect(fm!.to).toBe(doc.length); // nothing after the close
    expect(doc.slice(fm!.to)).toBe('');
  });
});
