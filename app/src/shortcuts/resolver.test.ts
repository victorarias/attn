import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// The app is macOS-only; pin platform detection so eventToBinding's Control
// rejection is exercised deterministically.
vi.mock('./platform', () => ({
  isMacLikePlatform: () => true,
  isAccelKeyPressed: (e: KeyboardEvent) => e.metaKey,
}));

import { SHORTCUTS } from './registry';
import {
  setShortcutOverrides,
  resolveBinding,
  resolvedShortcutEntries,
  isUnbound,
  isCustomized,
  findConflict,
  eventToBinding,
  isRiskyBinding,
  parseKeybindingsConfig,
  serializeKeybindingsConfig,
} from './resolver';

beforeEach(() => setShortcutOverrides({}));
afterEach(() => setShortcutOverrides({}));

function key(init: KeyboardEventInit): KeyboardEvent {
  return new KeyboardEvent('keydown', init);
}

describe('resolveBinding', () => {
  it('returns the default when there is no override', () => {
    expect(resolveBinding('session.new')).toEqual(SHORTCUTS['session.new']);
  });

  it('returns the override when present', () => {
    setShortcutOverrides({ 'session.new': { key: 'm', meta: true } });
    expect(resolveBinding('session.new')).toEqual({ key: 'm', meta: true });
  });

  it('treats a null override as unbound', () => {
    setShortcutOverrides({ 'session.new': null });
    expect(resolveBinding('session.new')).toBeNull();
    expect(isUnbound('session.new')).toBe(true);
  });

  it('reports whether an id is customized', () => {
    expect(isCustomized('session.new')).toBe(false);
    setShortcutOverrides({ 'session.new': null });
    expect(isCustomized('session.new')).toBe(true);
  });
});

describe('resolvedShortcutEntries', () => {
  it('omits unbound shortcuts and preserves registry order', () => {
    setShortcutOverrides({ 'app.quit': null });
    const ids = resolvedShortcutEntries().map(([id]) => id);
    expect(ids).not.toContain('app.quit');
    // app.quit is the first registry entry; the next one should now lead.
    const registryIds = Object.keys(SHORTCUTS);
    expect(ids[0]).toBe(registryIds[1]);
  });
});

describe('findConflict', () => {
  it('finds a different shortcut using the same combo', () => {
    // dock.diff is ⌘⇧G by default.
    expect(findConflict({ key: 'g', meta: true, shift: true }, 'session.new')).toBe('dock.diff');
  });

  it('ignores the excluded id itself', () => {
    expect(findConflict({ key: 'g', meta: true, shift: true }, 'dock.diff')).toBeNull();
  });

  it('respects allowed-conflict pairs (session.close vs terminal.close on ⌘W)', () => {
    // Re-binding session.close to ⌘W must not flag terminal.close as a conflict.
    expect(findConflict({ key: 'w', meta: true }, 'session.close')).toBeNull();
  });

  it('returns null for a free combo', () => {
    expect(findConflict({ key: 'y', meta: true, alt: true, shift: true }, 'session.new')).toBeNull();
  });

  it('detects code-equivalent collisions even when the printed key differs', () => {
    // A localized layout where ⌘+the-1-key reports key '&' but code 'Digit1'.
    // matchesShortcut would fire workspace.select1, so findConflict must catch it.
    expect(findConflict({ key: '&', code: 'Digit1', meta: true }, 'session.new'))
      .toBe('workspace.select1');
  });
});

describe('eventToBinding', () => {
  it('builds a ShortcutDef from a keystroke', () => {
    const r = eventToBinding(key({ key: 'm', metaKey: true, shiftKey: true }));
    expect(r).toEqual({ kind: 'binding', def: { key: 'm', meta: true, shift: true } });
  });

  it('keeps code for digits and named keys', () => {
    expect(eventToBinding(key({ key: '1', code: 'Digit1', metaKey: true }))).toEqual({
      kind: 'binding', def: { key: '1', code: 'Digit1', meta: true },
    });
    expect(eventToBinding(key({ key: 'ArrowUp', code: 'ArrowUp', metaKey: true }))).toEqual({
      kind: 'binding', def: { key: 'ArrowUp', code: 'ArrowUp', meta: true },
    });
  });

  it('ignores modifier-only keystrokes', () => {
    expect(eventToBinding(key({ key: 'Meta', metaKey: true })).kind).toBe('ignored');
    expect(eventToBinding(key({ key: 'Shift', shiftKey: true })).kind).toBe('ignored');
  });

  it('rejects Control as a modifier on macOS', () => {
    const r = eventToBinding(key({ key: 'k', ctrlKey: true }));
    expect(r.kind).toBe('error');
  });
});

describe('isRiskyBinding', () => {
  it('flags bindings without an accelerator', () => {
    expect(isRiskyBinding({ key: 'a' })).toBe(true);
    expect(isRiskyBinding({ key: 'a', shift: true })).toBe(true);
    expect(isRiskyBinding({ key: 'a', meta: true })).toBe(false);
    expect(isRiskyBinding({ key: 'a', alt: true })).toBe(false);
  });
});

describe('parseKeybindingsConfig', () => {
  it('returns an empty config for missing/invalid input', () => {
    expect(parseKeybindingsConfig(undefined).overrides).toEqual({});
    expect(parseKeybindingsConfig('not json').overrides).toEqual({});
    expect(parseKeybindingsConfig('123').overrides).toEqual({});
  });

  it('keeps known ids and drops unknown ones', () => {
    const raw = JSON.stringify({
      version: 1,
      overrides: {
        'session.new': { key: 'm', meta: true },
        'bogus.id': { key: 'x', meta: true },
        'app.quit': null,
      },
    });
    const cfg = parseKeybindingsConfig(raw);
    expect(cfg.overrides['session.new']).toEqual({ key: 'm', meta: true });
    expect(cfg.overrides['app.quit']).toBeNull();
    expect('bogus.id' in cfg.overrides).toBe(false);
  });

  it('drops malformed overrides so the id falls back to its default', () => {
    const raw = JSON.stringify({ overrides: { 'session.new': { meta: true } } }); // no key
    const cfg = parseKeybindingsConfig(raw);
    expect('session.new' in cfg.overrides).toBe(false);
  });

  it('round-trips through serialize', () => {
    const cfg = { version: 1 as const, overrides: { 'session.new': { key: 'm', meta: true } } };
    expect(parseKeybindingsConfig(serializeKeybindingsConfig(cfg))).toEqual(cfg);
  });
});
