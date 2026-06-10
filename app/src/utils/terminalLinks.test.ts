import { describe, expect, it } from 'vitest';
import {
  fragmentAtColumn,
  pathCandidatesForFragment,
  resolveDetectedPath,
  urlAtColumn,
} from './terminalLinks';

describe('urlAtColumn', () => {
  it('returns the uri and its column range', () => {
    const line = 'see https://example.test/page. then more';
    const hit = urlAtColumn(line, 10);
    expect(hit).toEqual({
      uri: 'https://example.test/page',
      startCol: 4,
      endCol: 29,
    });
  });

  it('misses columns outside the uri', () => {
    const line = 'see https://example.test/page';
    expect(urlAtColumn(line, 2)).toBeNull();
  });
});

describe('fragmentAtColumn', () => {
  it('finds the run of non-whitespace around the column', () => {
    expect(fragmentAtColumn('a (src/x.go:1) b', 5)).toEqual({ startCol: 2, endCol: 14 });
  });

  it('returns null on whitespace and past end of line', () => {
    expect(fragmentAtColumn('a b', 1)).toBeNull();
    expect(fragmentAtColumn('ab', 7)).toBeNull();
  });
});

describe('pathCandidatesForFragment', () => {
  it('parses line:col suffixes and prefers the stripped path', () => {
    const candidates = pathCandidatesForFragment('src/main.go:12:3', 4);
    expect(candidates[0]).toEqual({
      path: 'src/main.go',
      line: 12,
      column: 3,
      startCol: 4,
      endCol: 20,
    });
  });

  it('unwraps brackets and trailing punctuation', () => {
    const candidates = pathCandidatesForFragment('(internal/store/db.go:88),', 0);
    expect(candidates[0]?.path).toBe('internal/store/db.go');
    expect(candidates[0]?.line).toBe(88);
    expect(candidates[0]?.startCol).toBe(1);
    expect(candidates[0]?.endCol).toBe(1 + 'internal/store/db.go:88'.length);
  });

  it('accepts bare filenames with extensions and tilde paths', () => {
    expect(pathCandidatesForFragment('Makefile.am', 0)[0]?.path).toBe('Makefile.am');
    expect(pathCandidatesForFragment('~/notes.md', 0)[0]?.path).toBe('~/notes.md');
  });

  it('rejects prose words and urls', () => {
    expect(pathCandidatesForFragment('error:', 0)).toEqual([]);
    expect(pathCandidatesForFragment('https://example.test/x.go', 0)).toEqual([]);
    expect(pathCandidatesForFragment('word', 0)).toEqual([]);
  });

  it('keeps a colon-bearing path as a fallback candidate', () => {
    const candidates = pathCandidatesForFragment('a/b:9', 0);
    expect(candidates.map((candidate) => candidate.path)).toEqual(['a/b', 'a/b:9']);
  });
});

describe('resolveDetectedPath', () => {
  it('passes through absolute paths and normalizes dot segments', () => {
    expect(resolveDetectedPath('/a/./b/../c')).toBe('/a/c');
  });

  it('resolves relative paths against cwd', () => {
    expect(resolveDetectedPath('src/x.go', '/repo/')).toBe('/repo/src/x.go');
    expect(resolveDetectedPath('./src/x.go', '/repo')).toBe('/repo/src/x.go');
    expect(resolveDetectedPath('../x.go', '/repo/sub')).toBe('/repo/x.go');
  });

  it('expands ~ against home and refuses without it', () => {
    expect(resolveDetectedPath('~/notes.md', '/repo', '/Users/me')).toBe('/Users/me/notes.md');
    expect(resolveDetectedPath('~/notes.md', '/repo')).toBeNull();
    expect(resolveDetectedPath('~other/notes.md', '/repo', '/Users/me')).toBeNull();
  });

  it('refuses relative paths without cwd and escapes above root', () => {
    expect(resolveDetectedPath('src/x.go')).toBeNull();
    expect(resolveDetectedPath('../../../../x', '/a')).toBeNull();
  });
});
