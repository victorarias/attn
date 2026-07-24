import { describe, it, expect } from 'vitest';
import { finderBasename, scoreFile, rankFiles, type FileCandidate } from './rank';

function entry(path: string, extra: Partial<FileCandidate> = {}): FileCandidate {
  return { path, ...extra };
}

describe('finderBasename', () => {
  it('returns the last segment of a path', () => {
    expect(finderBasename('knowledge/index.md')).toBe('index.md');
    expect(finderBasename('a/b/c/deep.md')).toBe('deep.md');
  });
  it('returns the whole string when there is no slash', () => {
    expect(finderBasename('notes.md')).toBe('notes.md');
  });
});

describe('scoreFile', () => {
  it('scores an empty query as a uniform match (lists everything)', () => {
    expect(scoreFile(entry('knowledge/index.md'), '')).toBe(1);
    expect(scoreFile(entry('journal/2026-06-21.md'), '   ')).toBe(1);
  });

  it('matches a subsequence that spans path segments', () => {
    // k(nowledge)/(in)d(e)x — a scattered subsequence still matches.
    expect(scoreFile(entry('knowledge/index.md'), 'kidx')).toBeGreaterThan(0);
  });

  it('disqualifies a query that is not a subsequence', () => {
    expect(scoreFile(entry('knowledge/index.md'), 'zzz')).toBe(0);
    // Right characters, wrong order: 'x' never precedes 'i' in "index".
    expect(scoreFile(entry('index.md'), 'xidn')).toBe(0);
  });

  it('ranks a contiguous basename match above a scattered path match', () => {
    const basenameHit = scoreFile(entry('areas/index.md'), 'index');
    const scattered = scoreFile(entry('i/n/d/e/x-other.md'), 'index');
    expect(basenameHit).toBeGreaterThan(scattered);
  });

  it('matches against the title, not just the path', () => {
    const e = entry('journal/2026-06-21.md', { title: 'Shipping the tile finder' });
    expect(scoreFile(e, 'shipping')).toBeGreaterThan(0);
    // The query appears in neither the path nor the title.
    expect(scoreFile(e, 'database')).toBe(0);
  });
});

describe('rankFiles', () => {
  const files = [
    entry('knowledge/index.md', { updated: '2026-06-20' }),
    entry('knowledge/areas/tiles.md', { updated: '2026-06-21' }),
    entry('journal/2026-06-19.md', { updated: '2026-06-19' }),
    entry('notes.md'),
  ];

  it('drops non-matches and sorts best-first', () => {
    const ranked = rankFiles(files, 'tiles');
    expect(ranked[0].path).toBe('knowledge/areas/tiles.md');
    expect(ranked.every((e) => e.path.includes('t') || (e.title ?? '').includes('t'))).toBe(true);
  });

  it('lists everything for an empty query, most-recently-updated first', () => {
    const ranked = rankFiles(files, '');
    expect(ranked).toHaveLength(4);
    // tiles.md (06-21) is the newest; notes.md has no updated → sorts last.
    expect(ranked[0].path).toBe('knowledge/areas/tiles.md');
    expect(ranked[ranked.length - 1].path).toBe('notes.md');
  });

  it('caps the result list at the limit', () => {
    const many = Array.from({ length: 100 }, (_, i) => entry(`note-${i}.md`));
    expect(rankFiles(many, '', 10)).toHaveLength(10);
    expect(rankFiles(many, 'note', 5)).toHaveLength(5);
  });

  it('preserves the caller\'s own entry type through ranking', () => {
    // The generic keeps extra fields (here a Notebook note's size) visible to the
    // caller instead of narrowing rows to FileCandidate.
    const sized = [{ path: 'notes.md', size: 12 }];
    expect(rankFiles(sized, 'notes')[0].size).toBe(12);
  });
});
