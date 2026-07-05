import { describe, it, expect } from 'vitest';
import { hashContent } from './reviewHash';

describe('hashContent', () => {
  it('returns consistent hash for same content', () => {
    expect(hashContent('hello')).toBe(hashContent('hello'));
  });

  it('returns different hash for different content', () => {
    expect(hashContent('hello')).not.toBe(hashContent('world'));
  });

  it('handles empty string', () => {
    expect(hashContent('')).toBe('0');
  });

  it('is sensitive to small changes (single character)', () => {
    expect(hashContent('line 1\nline 2')).not.toBe(hashContent('line 1\nline 3'));
  });
});
