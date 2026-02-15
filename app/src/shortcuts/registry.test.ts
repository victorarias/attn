// app/src/shortcuts/registry.test.ts
import { describe, it, expect } from 'vitest';
import { SHORTCUTS, ShortcutDef, matchesShortcut } from './registry';

function withNavigatorPlatform<T>(platform: string, fn: () => T): T {
  const nav = window.navigator as Navigator & { platform?: string };
  const original = nav.platform;
  Object.defineProperty(nav, 'platform', { value: platform, configurable: true });
  try {
    return fn();
  } finally {
    Object.defineProperty(nav, 'platform', { value: original, configurable: true });
  }
}

describe('shortcut registry', () => {
  describe('matchesShortcut', () => {
    it('matches cmd+key shortcut on macOS', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'n', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: 'n',
          metaKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(true);
      });
    });

    it('matches ctrl as meta alternative on non-mac platforms', () => {
      withNavigatorPlatform('Win32', () => {
        const def: ShortcutDef = { key: 'n', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: 'n',
          ctrlKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(true);
      });
    });

    it('does not treat ctrl as meta on macOS', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'w', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: 'w',
          ctrlKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(false);
      });
    });

    it('matches cmd+shift+key shortcut on macOS', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'w', meta: true, shift: true };
        const event = new KeyboardEvent('keydown', {
          key: 'W',
          metaKey: true,
          shiftKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(true);
      });
    });

    it('matches shift-only shortcut (e.g., Shift+`)', () => {
      const def: ShortcutDef = { key: '~', shift: true };
      const event = new KeyboardEvent('keydown', {
        key: '~',
        shiftKey: true,
      });
      expect(matchesShortcut(event, def)).toBe(true);
    });

    it('does not match when meta required but not pressed', () => {
      const def: ShortcutDef = { key: 'n', meta: true };
      const event = new KeyboardEvent('keydown', {
        key: 'n',
        metaKey: false,
      });
      expect(matchesShortcut(event, def)).toBe(false);
    });

    it('does not match when shift required but not pressed', () => {
      const def: ShortcutDef = { key: 'w', meta: true, shift: true };
      const event = new KeyboardEvent('keydown', {
        key: 'w',
        metaKey: true,
        shiftKey: false,
      });
      expect(matchesShortcut(event, def)).toBe(false);
    });

    it('does not match when extra shift pressed', () => {
      const def: ShortcutDef = { key: 'n', meta: true };
      const event = new KeyboardEvent('keydown', {
        key: 'N',
        metaKey: true,
        shiftKey: true,
      });
      expect(matchesShortcut(event, def)).toBe(false);
    });

    it('does not match wrong key', () => {
      const def: ShortcutDef = { key: 'n', meta: true };
      const event = new KeyboardEvent('keydown', {
        key: 'm',
        metaKey: true,
      });
      expect(matchesShortcut(event, def)).toBe(false);
    });

    it('is case insensitive for key', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'N', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: 'n',
          metaKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(true);
      });
    });

    it('matches special characters like backtick', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: '`', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: '`',
          metaKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(true);
      });
    });

    it('matches shifted special characters (Shift+[ = {)', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: '{', meta: true, shift: true };
        const event = new KeyboardEvent('keydown', {
          key: '{',
          metaKey: true,
          shiftKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(true);
      });
    });
  });

  describe('SHORTCUTS registry', () => {
    it('has no duplicate key combinations', () => {
      const seen = new Map<string, string>();

      for (const [id, def] of Object.entries(SHORTCUTS)) {
        const parts: string[] = [];
        if ('meta' in def && def.meta) parts.push('meta');
        if ('ctrl' in def && def.ctrl) parts.push('ctrl');
        if ('alt' in def && def.alt) parts.push('alt');
        if ('shift' in def && def.shift) parts.push('shift');
        parts.push(def.key.toLowerCase());
        const key = parts.join('+');

        const existing = seen.get(key);
        if (existing) {
          throw new Error(`Duplicate shortcut: "${id}" and "${existing}" both use ${key}`);
        }
        seen.set(key, id);
      }
    });

    it('has expected terminal shortcuts defined', () => {
      expect(SHORTCUTS['terminal.open']).toEqual({ key: '`', meta: true });
      expect(SHORTCUTS['terminal.collapse']).toEqual({ key: '~', shift: true });
      expect(SHORTCUTS['terminal.new']).toEqual({ key: 't', meta: true });
      expect(SHORTCUTS['terminal.close']).toEqual({ key: 'w', meta: true, shift: true });
      expect(SHORTCUTS['terminal.prevTab']).toEqual({ key: '{', meta: true, shift: true });
      expect(SHORTCUTS['terminal.nextTab']).toEqual({ key: '}', meta: true, shift: true });
    });

    it('has expected session shortcuts defined', () => {
      expect(SHORTCUTS['session.new']).toEqual({ key: 'n', meta: true });
      expect(SHORTCUTS['session.close']).toEqual({ key: 'w', meta: true });
      expect(SHORTCUTS['session.goToDashboard']).toEqual({ key: 'd', meta: true });
    });
  });
});
