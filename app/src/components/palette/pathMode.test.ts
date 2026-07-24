import { describe, it, expect } from 'vitest';
import { isPathQuery, toBrowseInput, descendQuery } from './pathMode';

describe('isPathQuery', () => {
  it('treats a leading slash, tilde, or dot-slash as a path', () => {
    expect(isPathQuery('/')).toBe(true);
    expect(isPathQuery('~')).toBe(true);
    expect(isPathQuery('~/projects')).toBe(true);
    expect(isPathQuery('./docs')).toBe(true);
    expect(isPathQuery('../sibling')).toBe(true);
  });

  it('leaves an ordinary fuzzy query alone even when it contains slashes', () => {
    // The whole point of requiring a leading slash: docs/plan is how people
    // narrow a fuzzy search, not a request to browse.
    expect(isPathQuery('docs/plan')).toBe(false);
    expect(isPathQuery('plan')).toBe(false);
    expect(isPathQuery('')).toBe(false);
  });
});

describe('toBrowseInput', () => {
  it('passes absolute and home-relative queries through for the daemon to expand', () => {
    expect(toBrowseInput('~/projects/att', '/repo')).toBe('~/projects/att');
    expect(toBrowseInput('/etc/', '/repo')).toBe('/etc/');
  });

  it('resolves a relative query against the root, since the daemon would use its own cwd', () => {
    expect(toBrowseInput('./docs/', '/repo')).toBe('/repo/docs/');
    expect(toBrowseInput('./docs/pl', '/repo')).toBe('/repo/docs/pl');
    expect(toBrowseInput('../other/', '/repo/app')).toBe('/repo/other/');
  });

  it('has nothing to resolve a relative query against without a root', () => {
    expect(toBrowseInput('./docs/', null)).toBeNull();
  });

  it('returns null for a query that is not a path at all', () => {
    expect(toBrowseInput('docs/plan', '/repo')).toBeNull();
  });

  it('never climbs above the filesystem root', () => {
    expect(toBrowseInput('../../../../', '/repo')).toBe('/');
  });
});

describe('descendQuery', () => {
  it('adds the trailing slash that means "list this directory"', () => {
    expect(descendQuery('~/projects/attn')).toBe('~/projects/attn/');
    expect(descendQuery('~/projects/attn/')).toBe('~/projects/attn/');
  });
});
