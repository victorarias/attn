import { describe, expect, it } from 'vitest';
import { generateWorktreeName } from './worktreeNames';

describe('generateWorktreeName', () => {
  it('produces a git-safe adjective-noun pair', () => {
    for (let i = 0; i < 200; i++) {
      expect(generateWorktreeName()).toMatch(/^[a-z]+-[a-z]+$/);
    }
  });

  it('never returns a name that is already taken', () => {
    const taken = new Set<string>();
    for (let i = 0; i < 300; i++) {
      const name = generateWorktreeName(taken);
      expect(taken.has(name)).toBe(false);
      taken.add(name);
    }
  });

  it('falls back to a numeric suffix when the draws keep colliding', () => {
    // A constant random source always draws the same pair, so the only way out
    // is the suffix path.
    const alwaysFirst = () => 0;
    const first = generateWorktreeName([], alwaysFirst);
    const second = generateWorktreeName([first], alwaysFirst);
    const third = generateWorktreeName([first, second], alwaysFirst);

    expect(second).toBe(`${first}-2`);
    expect(third).toBe(`${first}-3`);
  });

  it('spreads across the wordlists rather than clustering on one name', () => {
    const names = new Set<string>();
    for (let i = 0; i < 200; i++) {
      names.add(generateWorktreeName());
    }
    expect(names.size).toBeGreaterThan(100);
  });
});
