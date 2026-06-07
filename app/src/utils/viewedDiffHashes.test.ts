import { describe, it, expect, beforeEach } from 'vitest';
import {
  loadViewedDiffHashes,
  saveViewedDiffHashes,
  clearAllViewedDiffHashes,
} from './viewedDiffHashes';

describe('viewedDiffHashes', () => {
  beforeEach(() => {
    clearAllViewedDiffHashes();
  });

  it('round-trips a per-review hash map', () => {
    const hashes = new Map([
      ['src/a.ts', 'aaaa'],
      ['src/b.ts', 'bbbb'],
    ]);
    saveViewedDiffHashes('review-1', hashes);

    const restored = loadViewedDiffHashes('review-1');
    expect(restored.get('src/a.ts')).toBe('aaaa');
    expect(restored.get('src/b.ts')).toBe('bbbb');
    expect(restored.size).toBe(2);
  });

  it('isolates hashes by review id', () => {
    saveViewedDiffHashes('review-1', new Map([['f.ts', '1111']]));
    saveViewedDiffHashes('review-2', new Map([['f.ts', '2222']]));

    expect(loadViewedDiffHashes('review-1').get('f.ts')).toBe('1111');
    expect(loadViewedDiffHashes('review-2').get('f.ts')).toBe('2222');
  });

  it('returns an empty map for an unknown review', () => {
    expect(loadViewedDiffHashes('missing').size).toBe(0);
  });

  it('returns an empty map for malformed stored data', () => {
    localStorage.setItem('attn:viewedDiffHashes:bad', '{not json');
    expect(loadViewedDiffHashes('bad').size).toBe(0);
  });

  it('clears every stored entry', () => {
    saveViewedDiffHashes('review-1', new Map([['f.ts', '1111']]));
    saveViewedDiffHashes('review-2', new Map([['g.ts', '2222']]));
    clearAllViewedDiffHashes();
    expect(loadViewedDiffHashes('review-1').size).toBe(0);
    expect(loadViewedDiffHashes('review-2').size).toBe(0);
  });
});
