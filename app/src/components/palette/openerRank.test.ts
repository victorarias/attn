import { describe, it, expect } from 'vitest';
import { mergeOpenerFiles, rankOpenerFiles, type OpenerFile } from './openerRank';

describe('mergeOpenerFiles', () => {
  it('labels recents inside the fuzzy root relative to it', () => {
    const merged = mergeOpenerFiles(
      [{ path: '/repo/docs/plan.md', lastAt: '2026-07-24T10:00:00Z' }],
      '/repo',
      [],
    );
    expect(merged).toEqual([
      { absPath: '/repo/docs/plan.md', label: 'docs/plan.md', recentAt: '2026-07-24T10:00:00Z' },
    ]);
  });

  it('keeps the absolute label for a recent outside the fuzzy root', () => {
    const merged = mergeOpenerFiles(
      [{ path: '/other/notes.md', lastAt: '2026-07-24T10:00:00Z' }],
      '/repo',
      [],
    );
    expect(merged[0].label).toBe('/other/notes.md');
  });

  it('lists an index file once even when it is also a recent', () => {
    const merged = mergeOpenerFiles(
      [{ path: '/repo/docs/plan.md', lastAt: '2026-07-24T10:00:00Z' }],
      '/repo',
      ['docs/plan.md', 'docs/other.md'],
    );
    expect(merged.map((file) => file.absPath)).toEqual([
      '/repo/docs/plan.md',
      '/repo/docs/other.md',
    ]);
    // The surviving entry is the recent one, so it keeps its recency bonus.
    expect(merged[0].recentAt).toBe('2026-07-24T10:00:00Z');
  });

  it('lists recents alone when there is no fuzzy root', () => {
    const merged = mergeOpenerFiles(
      [{ path: '/other/notes.md', lastAt: '2026-07-24T10:00:00Z' }],
      null,
      ['ignored.md'],
    );
    expect(merged.map((file) => file.absPath)).toEqual(['/other/notes.md']);
  });
});

describe('rankOpenerFiles', () => {
  const recent = (label: string, at: string): OpenerFile => ({ absPath: `/repo/${label}`, label, recentAt: at });
  const indexed = (label: string): OpenerFile => ({ absPath: `/repo/${label}`, label });

  it('shows only recents on an empty query, in the order given', () => {
    const files = [recent('b.md', '2026-07-24T09:00:00Z'), indexed('a.md'), recent('c.md', '2026-07-24T10:00:00Z')];
    expect(rankOpenerFiles(files, '').map((file) => file.label)).toEqual(['b.md', 'c.md']);
  });

  it('ranks recents and index entries in one list once typing starts', () => {
    const files = [indexed('docs/plan.md'), recent('notes/plan.md', '2026-07-24T10:00:00Z')];
    const ranked = rankOpenerFiles(files, 'plan');
    // Equal textual matches: the remembered one wins on the recency bonus, but
    // both stay in the same list.
    expect(ranked.map((file) => file.label)).toEqual(['notes/plan.md', 'docs/plan.md']);
  });

  it('still lets a clearly better match beat a recent', () => {
    const files = [recent('unrelated-scratch.md', '2026-07-24T10:00:00Z'), indexed('plan.md')];
    expect(rankOpenerFiles(files, 'plan')[0].label).toBe('plan.md');
  });

  it('drops entries the query does not subsequence-match', () => {
    const files = [indexed('plan.md'), indexed('readme.md')];
    expect(rankOpenerFiles(files, 'plan').map((file) => file.label)).toEqual(['plan.md']);
  });

  it('caps the list', () => {
    const files = Array.from({ length: 80 }, (_, i) => indexed(`plan-${i}.md`));
    expect(rankOpenerFiles(files, 'plan')).toHaveLength(50);
  });
});
