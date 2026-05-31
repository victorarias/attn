// app/src/shortcuts/cheatsheet.test.ts
import { describe, it, expect } from 'vitest';
import { buildCheatsheet } from './cheatsheet';

describe('buildCheatsheet', () => {
  it('produces categories with non-empty, fully-rendered combos', () => {
    const categories = buildCheatsheet();
    expect(categories.length).toBeGreaterThan(0);

    for (const category of categories) {
      expect(category.title).toBeTruthy();
      expect(category.rows.length).toBeGreaterThan(0);
      for (const row of category.rows) {
        expect(row.label).toBeTruthy();
        expect(row.combos.length).toBeGreaterThan(0);
        for (const combo of row.combos) {
          expect(combo.length).toBeGreaterThan(0);
          // No empty/undefined keycaps leaked from a missing registry id.
          expect(combo.every((token) => typeof token === 'string' && token.length > 0)).toBe(true);
        }
      }
    }
  });

  it('reflects the current workspace bindings (⌘T new workspace, ⌘N new session)', () => {
    const rows = buildCheatsheet().flatMap((c) => c.rows);
    const newWorkspace = rows.find((r) => r.label === 'New workspace');
    const newSession = rows.find((r) => r.label === 'New session in this workspace');
    expect(newWorkspace?.combos[0]).toEqual(['⌘', 'T']);
    expect(newSession?.combos[0]).toEqual(['⌘', 'N']);
  });
});
