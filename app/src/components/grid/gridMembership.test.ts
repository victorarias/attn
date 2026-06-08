import { afterEach, describe, expect, it } from 'vitest';
import { persistExcludedGridSessions, readExcludedGridSessions } from './gridMembership';

const KEY = 'attn.grid.excluded';

describe('grid membership persistence', () => {
  afterEach(() => {
    window.localStorage.clear();
  });

  it('round-trips an excluded set', () => {
    persistExcludedGridSessions(new Set(['s1', 's2']));
    expect([...readExcludedGridSessions()].sort()).toEqual(['s1', 's2']);
  });

  it('defaults to empty when nothing is stored', () => {
    expect(readExcludedGridSessions().size).toBe(0);
  });

  it('defaults to empty on corrupt data', () => {
    window.localStorage.setItem(KEY, '{not json');
    expect(readExcludedGridSessions().size).toBe(0);
  });

  it('ignores a non-array payload', () => {
    window.localStorage.setItem(KEY, JSON.stringify({ s1: true }));
    expect(readExcludedGridSessions().size).toBe(0);
  });

  it('drops non-string entries', () => {
    window.localStorage.setItem(KEY, JSON.stringify(['s1', 42, null, 's2']));
    expect([...readExcludedGridSessions()].sort()).toEqual(['s1', 's2']);
  });
});
