import { describe, expect, it } from 'vitest';
import { filterBlockOutputLines, lineSegments } from './terminalBlockFilter';

describe('filterBlockOutputLines', () => {
  const output = [
    'On branch master',
    'Your branch is up to date.',
    '',
    'nothing to commit, working tree clean',
  ].join('\n');

  it('keeps only lines containing the query, with match ranges', () => {
    const lines = filterBlockOutputLines(output, 'branch', false);
    expect(lines.map((line) => line.lineOffset)).toEqual([0, 1]);
    expect(lines[0].ranges).toEqual([{ startCol: 3, endCol: 9 }]);
    expect(lines[1].ranges).toEqual([{ startCol: 5, endCol: 11 }]);
  });

  it('is case-insensitive unless asked otherwise', () => {
    expect(filterBlockOutputLines(output, 'BRANCH', false)).toHaveLength(2);
    expect(filterBlockOutputLines(output, 'BRANCH', true)).toHaveLength(0);
  });

  it('reports every occurrence within a line', () => {
    const lines = filterBlockOutputLines('foo bar foo', 'foo', false);
    expect(lines[0].ranges).toEqual([
      { startCol: 0, endCol: 3 },
      { startCol: 8, endCol: 11 },
    ]);
  });

  it('returns nothing for an empty query or empty output', () => {
    expect(filterBlockOutputLines(output, '', false)).toEqual([]);
    expect(filterBlockOutputLines('', 'x', false)).toEqual([]);
  });
});

describe('lineSegments', () => {
  it('splits a line into plain and highlighted segments', () => {
    const [line] = filterBlockOutputLines('foo bar foo', 'foo', false);
    expect(lineSegments(line)).toEqual([
      { text: 'foo', match: true },
      { text: ' bar ', match: false },
      { text: 'foo', match: true },
    ]);
  });

  it('keeps leading and trailing plain text', () => {
    const [line] = filterBlockOutputLines('xx needle yy', 'needle', false);
    expect(lineSegments(line)).toEqual([
      { text: 'xx ', match: false },
      { text: 'needle', match: true },
      { text: ' yy', match: false },
    ]);
  });
});
