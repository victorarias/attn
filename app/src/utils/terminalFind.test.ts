import { describe, expect, it, vi } from 'vitest';
import {
  initialFocusedMatch,
  matchesInRowText,
  startFindScan,
  visibleMatches,
  type FindMatch,
  type FindRowAccess,
} from './terminalFind';

function access(rows: string[]): FindRowAccess {
  return {
    totalRows: () => rows.length,
    rowText: (row) => rows[row] ?? '',
  };
}

function scan(rows: string[], query: string, caseSensitive = false, chunkRows = 2): Promise<FindMatch[]> {
  return new Promise((resolve) => {
    startFindScan(access(rows), query, { caseSensitive, chunkRows }, () => {}, resolve);
  });
}

describe('matchesInRowText', () => {
  it('finds all occurrences case-insensitively by default', () => {
    expect(matchesInRowText('Foo foo FOO', 'foo', false, 3)).toEqual([
      { bufferRow: 3, startCol: 0, endCol: 3 },
      { bufferRow: 3, startCol: 4, endCol: 7 },
      { bufferRow: 3, startCol: 8, endCol: 11 },
    ]);
  });

  it('respects case sensitivity', () => {
    expect(matchesInRowText('Foo foo', 'Foo', true, 0)).toEqual([
      { bufferRow: 0, startCol: 0, endCol: 3 },
    ]);
  });

  it('returns nothing for an empty query', () => {
    expect(matchesInRowText('anything', '', false, 0)).toEqual([]);
  });
});

describe('startFindScan', () => {
  it('scans all rows across chunks and reports sorted matches', async () => {
    const rows = ['alpha', 'needle here', 'nothing', 'needle needle', 'tail'];
    const matches = await scan(rows, 'needle');
    expect(matches.map((match) => [match.bufferRow, match.startCol])).toEqual([
      [1, 0],
      [3, 0],
      [3, 7],
    ]);
  });

  it('stops producing results after cancel', async () => {
    const rows = Array.from({ length: 10 }, () => 'needle');
    const onDone = vi.fn();
    const handle = startFindScan(access(rows), 'needle', { caseSensitive: false, chunkRows: 2 }, () => {}, onDone);
    handle.cancel();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(onDone).not.toHaveBeenCalled();
  });

  it('completes with no matches for an empty query', async () => {
    expect(await scan(['a', 'b'], '')).toEqual([]);
  });
});

describe('focus and visibility helpers', () => {
  const matches: FindMatch[] = [
    { bufferRow: 2, startCol: 0, endCol: 3 },
    { bufferRow: 10, startCol: 1, endCol: 4 },
    { bufferRow: 10, startCol: 8, endCol: 11 },
    { bufferRow: 30, startCol: 0, endCol: 3 },
  ];

  it('focuses the last match initially', () => {
    expect(initialFocusedMatch(matches)).toBe(3);
  });

  it('selects only matches inside the viewport window', () => {
    expect(visibleMatches(matches, 10, 5)).toEqual(matches.slice(1, 3));
    expect(visibleMatches(matches, 0, 3)).toEqual(matches.slice(0, 1));
    expect(visibleMatches(matches, 11, 100)).toEqual(matches.slice(3));
    expect(visibleMatches([], 0, 10)).toEqual([]);
  });
});
