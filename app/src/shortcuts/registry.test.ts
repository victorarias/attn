// app/src/shortcuts/registry.test.ts
import { describe, it, expect } from 'vitest';
import {
  SHORTCUTS,
  ShortcutDef,
  Chord,
  matchesShortcut,
  bindingsConflict,
  combosConflict,
  isChord,
} from './registry';

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
  const isAllowedConflict = (a: string, b: string) => (
    [a, b].sort().join('|') === 'session.close|terminal.close'
  );

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
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'w', meta: true, shift: true };
        const event = new KeyboardEvent('keydown', {
          key: 'w',
          metaKey: true,
          shiftKey: false,
        });
        expect(matchesShortcut(event, def)).toBe(false);
      });
    });

    it('does not match when extra shift pressed', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'n', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: 'N',
          metaKey: true,
          shiftKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(false);
      });
    });

    it('does not match wrong key', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'n', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: 'm',
          metaKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(false);
      });
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

    it('matches cmd+alt+arrow shortcut on macOS', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: 'ArrowLeft', meta: true, alt: true };
        const event = new KeyboardEvent('keydown', {
          key: 'ArrowLeft',
          metaKey: true,
          altKey: true,
        });
        expect(matchesShortcut(event, def)).toBe(true);
      });
    });

    it('matches digit shortcuts by physical code when key text differs', () => {
      withNavigatorPlatform('MacIntel', () => {
        const def: ShortcutDef = { key: '2', code: 'Digit2', meta: true };
        const event = new KeyboardEvent('keydown', {
          key: '™',
          code: 'Digit2',
          metaKey: true,
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
          if (isAllowedConflict(existing, id)) {
            continue;
          }
          throw new Error(`Duplicate shortcut: "${id}" and "${existing}" both use ${key}`);
        }
        seen.set(key, id);
      }
    });

    it('has expected terminal shortcuts defined', () => {
      expect(SHORTCUTS['terminal.open']).toEqual({ key: '`', meta: true });
      expect(SHORTCUTS['terminal.collapse']).toEqual({ key: '~', shift: true });
      expect(SHORTCUTS['terminal.splitVertical']).toEqual({ key: 'd', meta: true });
      expect(SHORTCUTS['terminal.splitHorizontal']).toEqual({ key: 'd', meta: true, shift: true });
      expect(SHORTCUTS['terminal.toggleZoom']).toEqual({
        key: 'z',
        meta: true,
        shift: true,
        editableTarget: 'native',
      });
      expect(SHORTCUTS['terminal.toggleMaximize']).toEqual({ key: 'Enter', meta: true, shift: true });
      expect(SHORTCUTS['terminal.close']).toEqual({ key: 'w', meta: true });
      expect(SHORTCUTS['terminal.focusLeft']).toEqual({ key: 'ArrowLeft', meta: true, alt: true });
      expect(SHORTCUTS['terminal.focusRight']).toEqual({ key: 'ArrowRight', meta: true, alt: true });
      expect(SHORTCUTS['terminal.focusUp']).toEqual({ key: 'ArrowUp', meta: true, alt: true });
      expect(SHORTCUTS['terminal.focusDown']).toEqual({ key: 'ArrowDown', meta: true, alt: true });
    });

    it('has expected session shortcuts defined', () => {
      expect(SHORTCUTS['session.new']).toEqual({ key: 'n', meta: true });
      expect(SHORTCUTS['session.newHorizontal']).toEqual({ key: 'n', meta: true, shift: true });
      expect(SHORTCUTS['session.newWorkspace']).toEqual({ key: 't', meta: true });
      expect(SHORTCUTS['session.close']).toEqual({ key: 'w', meta: true });
      expect(SHORTCUTS['session.goToDashboard']).toEqual({ key: 'h', meta: true, shift: true });
      expect(SHORTCUTS['view.toggleGrid']).toEqual({ key: 'g', meta: true });
    });

    it('has expected workspace shortcuts defined', () => {
      expect(SHORTCUTS['workspace.select1']).toEqual({ key: '1', code: 'Digit1', meta: true });
      expect(SHORTCUTS['workspace.select9']).toEqual({ key: '9', code: 'Digit9', meta: true });
    });
  });

  describe('isChord', () => {
    it('distinguishes chords from combos', () => {
      expect(isChord({ key: 'k', meta: true })).toBe(false);
      expect(isChord({ leader: { key: 'k', meta: true }, then: { key: 'd' } })).toBe(true);
      expect(isChord(null)).toBe(false);
      expect(isChord(undefined)).toBe(false);
    });
  });

  describe('bindingsConflict', () => {
    const leaderK: Chord = { leader: { key: 'k', meta: true }, then: { key: 'd' } };

    it('treats combo vs combo as a same-keystroke collision', () => {
      expect(bindingsConflict({ key: 'g', meta: true }, { key: 'g', meta: true })).toBe(true);
      expect(bindingsConflict({ key: 'g', meta: true }, { key: 'g', meta: true, shift: true })).toBe(false);
    });

    it('lets chords share a leader as long as the follow key differs', () => {
      const sameLeaderDifferentThen: Chord = { leader: { key: 'k', meta: true }, then: { key: 'g' } };
      expect(bindingsConflict(leaderK, sameLeaderDifferentThen)).toBe(false);
    });

    it('conflicts when two chords share leader AND follow key', () => {
      const duplicate: Chord = { leader: { key: 'k', meta: true }, then: { key: 'd' } };
      expect(bindingsConflict(leaderK, duplicate)).toBe(true);
    });

    it('conflicts a chord with a plain combo equal to its leader (leader exclusivity)', () => {
      expect(bindingsConflict(leaderK, { key: 'k', meta: true })).toBe(true);
      expect(bindingsConflict({ key: 'k', meta: true }, leaderK)).toBe(true);
    });

    it('does not conflict a chord with a combo that only equals its follow key', () => {
      // A bare ⌘D combo collides with the leader (⌘K) check? No — different keystroke.
      expect(bindingsConflict(leaderK, { key: 'd' })).toBe(false);
    });
  });

  describe('combosConflict', () => {
    it('matches the matchesShortcut key-or-code equivalence', () => {
      expect(combosConflict({ key: '1', code: 'Digit1', meta: true }, { key: '&', code: 'Digit1', meta: true })).toBe(true);
      expect(combosConflict({ key: 'a' }, { key: 'b' })).toBe(false);
    });
  });
});
