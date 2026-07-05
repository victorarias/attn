import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// The app is macOS-only; pin platform detection so eventToBinding's Control
// rejection is exercised deterministically.
vi.mock('./platform', () => ({
  isMacLikePlatform: () => true,
  isAccelKeyPressed: (e: KeyboardEvent) => e.metaKey,
}));

import { SHORTCUTS, ShortcutId } from './registry';
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
  DEFAULT_DOCK,
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
    // dock.attention is ⌘⇧P by default.
    expect(findConflict({ key: 'p', meta: true, shift: true }, 'session.new')).toBe('dock.attention');
  });

  it('ignores the excluded id itself', () => {
    expect(findConflict({ key: 'p', meta: true, shift: true }, 'dock.attention')).toBeNull();
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

  it('evaluates the leader for a chord', () => {
    // Accelerator on the leader -> safe, even though the follow key is bare.
    expect(isRiskyBinding({ leader: { key: 'k', meta: true }, then: { key: 'd' } })).toBe(false);
    // Bare leader -> risky (would collide with typing before the chord arms).
    expect(isRiskyBinding({ leader: { key: 'k' }, then: { key: 'd' } })).toBe(true);
  });
});

describe('chord overrides', () => {
  it('resolves a chord override and round-trips it through parse/serialize', () => {
    const chord = { leader: { key: 'k', meta: true }, then: { key: 'd' } };
    const cfg = {
      version: 1 as const,
      overrides: { 'dock.attention': chord },
      dock: DEFAULT_DOCK,
    };
    const parsed = parseKeybindingsConfig(serializeKeybindingsConfig(cfg));
    expect(parsed.overrides['dock.attention']).toEqual(chord);
  });

  it('drops a malformed chord (missing follow step) so the id keeps its default', () => {
    const raw = JSON.stringify({
      overrides: { 'dock.attention': { leader: { key: 'k', meta: true } } },
    });
    expect('dock.attention' in parseKeybindingsConfig(raw).overrides).toBe(false);
  });

  it('finds a conflict for a chord whose leader equals an existing combo', () => {
    // ⌘G is view.toggleGrid. A chord with leader ⌘G conflicts with it.
    expect(findConflict({ leader: { key: 'g', meta: true }, then: { key: 'x' } }, 'dock.attention'))
      .toBe('view.toggleGrid');
  });

  it('lets a chord leader coexist with a different existing combo', () => {
    // ⌘K is ui.actionMenu (combo). A chord leader of ⌘J (jumpToWaiting) would
    // conflict; a leader on an unused accel like ⌘⌥J does not.
    expect(findConflict({ leader: { key: 'j', meta: true, alt: true }, then: { key: 'x' } }, 'dock.attention'))
      .toBeNull();
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
    const cfg = {
      version: 1 as const,
      overrides: { 'session.new': { key: 'm', meta: true } },
      dock: { collapsed: true, items: ['dock.attention', 'session.toggleSidebar'] as ShortcutId[] },
    };
    expect(parseKeybindingsConfig(serializeKeybindingsConfig(cfg))).toEqual(cfg);
  });
});

describe('dock config', () => {
  it('falls back to the default dock when absent or malformed', () => {
    expect(parseKeybindingsConfig(undefined).dock).toEqual(DEFAULT_DOCK);
    expect(parseKeybindingsConfig(JSON.stringify({ overrides: {} })).dock).toEqual(DEFAULT_DOCK);
    expect(parseKeybindingsConfig(JSON.stringify({ dock: 'nope' })).dock).toEqual(DEFAULT_DOCK);
    // items present but not an array -> default
    expect(parseKeybindingsConfig(JSON.stringify({ dock: { items: 5 } })).dock).toEqual(DEFAULT_DOCK);
  });

  it('keeps known ids in order, drops unknown ids, and dedups', () => {
    const raw = JSON.stringify({
      dock: { collapsed: true, items: ['dock.attention', 'bogus.id', 'dock.attention', 'session.new'] },
    });
    expect(parseKeybindingsConfig(raw).dock).toEqual({
      collapsed: true,
      items: ['dock.attention', 'session.new'],
    });
  });

  it('coerces collapsed to a boolean', () => {
    const raw = JSON.stringify({ dock: { collapsed: 'yes', items: [] } });
    expect(parseKeybindingsConfig(raw).dock.collapsed).toBe(false);
  });

  it('does not share the default dock array between parses (no cross-mutation)', () => {
    const a = parseKeybindingsConfig(undefined).dock.items;
    const b = parseKeybindingsConfig(undefined).dock.items;
    expect(a).not.toBe(b);
    expect(a).toEqual(b);
  });
});
