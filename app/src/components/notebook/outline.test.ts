import { describe, expect, it } from 'vitest';
import { parseOutline } from './outline';

describe('parseOutline', () => {
  it('collects ATX headings with level, text, position, and line', () => {
    const md = ['# Title', '', 'Intro paragraph.', '', '## Section one', 'body', '### Detail'].join('\n');
    const out = parseOutline(md);
    expect(out).toEqual([
      { level: 1, text: 'Title', pos: 0, line: 1 },
      { level: 2, text: 'Section one', pos: md.indexOf('## Section one'), line: 5 },
      { level: 3, text: 'Detail', pos: md.indexOf('### Detail'), line: 7 },
    ]);
  });

  it('positions index into the source so the editor can scroll to them', () => {
    const md = 'preamble\n\n## Heading here\n';
    const [h] = parseOutline(md);
    expect(md.slice(h.pos)).toMatch(/^## Heading here/);
  });

  it('ignores # lines inside fenced code blocks (``` and ~~~)', () => {
    const md = [
      '# Real heading',
      '',
      '```bash',
      '# not a heading, just a shell comment',
      'echo hi',
      '```',
      '',
      '~~~',
      '## also code',
      '~~~',
      '',
      '## Real trailing heading',
    ].join('\n');
    expect(parseOutline(md).map((h) => h.text)).toEqual(['Real heading', 'Real trailing heading']);
  });

  it('strips a closing hash sequence and requires a space after the hashes', () => {
    const md = ['## Closed heading ##', '#hashtag is not a heading', '###### Deep'].join('\n');
    const out = parseOutline(md);
    expect(out.map((h) => [h.level, h.text])).toEqual([
      [2, 'Closed heading'],
      [6, 'Deep'],
    ]);
  });

  it('skips empty headings and tolerates an empty document', () => {
    expect(parseOutline('')).toEqual([]);
    expect(parseOutline('## \n#   \ntext')).toEqual([]);
  });

  it('allows up to three leading spaces but not four (indented code)', () => {
    const md = ['   ### Indented three', '    # Indented four is code'].join('\n');
    expect(parseOutline(md).map((h) => h.text)).toEqual(['Indented three']);
  });
});
