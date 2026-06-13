// app/src/shortcuts/resolver.ts
// Layers user overrides on top of the built-in SHORTCUTS defaults.
//
// Defaults always live in code, so a corrupt or stale config can never orphan
// an action id — each id falls back to its default. Overrides are pure data
// persisted as one JSON settings blob (`keybindings_config`). This module is
// the ONLY place dispatch, formatting, and the editor read bindings from, so
// rebinds take effect everywhere at once.

import {
  SHORTCUTS,
  ShortcutDef,
  ShortcutId,
  Combo,
  Binding,
  bindingsConflict,
  isAllowedConflict,
  isChord,
} from './registry';
import { isMacLikePlatform } from './platform';

export interface DockConfig {
  /** When true the sidebar dock chips are hidden behind a "show dock" affordance. */
  collapsed: boolean;
  /** Ordered membership; each id renders one dock chip. */
  items: ShortcutId[];
}

export interface KeybindingsConfig {
  version: 1;
  // Absent id -> use default.
  // Binding   -> rebind to this combo or chord.
  // null      -> explicitly unbound.
  overrides: Partial<Record<ShortcutId, Binding | null>>;
  dock: DockConfig;
}

// Default dock membership, in render order. Mirrors the chips the sidebar showed
// before the dock became config-driven (panel toggles, the common terminal
// actions, then the sidebar toggle). Every entry must be a real ShortcutId.
export const DEFAULT_DOCK_ITEMS: ShortcutId[] = [
  'dock.diffDetail',
  'dock.reviewLoop',
  'dock.diff',
  'dock.attention',
  'terminal.splitVertical',
  'terminal.splitHorizontal',
  'session.newHorizontal',
  'terminal.toggleZoom',
  'session.toggleSidebar',
];

export const DEFAULT_DOCK: DockConfig = { collapsed: false, items: DEFAULT_DOCK_ITEMS };

export const EMPTY_KEYBINDINGS_CONFIG: KeybindingsConfig = {
  version: 1,
  overrides: {},
  dock: DEFAULT_DOCK,
};

export const KEYBINDINGS_SETTING_KEY = 'keybindings_config';

// --- Module-level resolved state (read by the global dispatch + formatting) ---

let overrides: Partial<Record<ShortcutId, Binding | null>> = {};

/** Replace the active override set (called when settings load or the user edits). */
export function setShortcutOverrides(next: Partial<Record<ShortcutId, Binding | null>>): void {
  overrides = { ...next };
}

export function getShortcutOverrides(): Partial<Record<ShortcutId, Binding | null>> {
  return overrides;
}

/** The effective binding for an id, or null when unbound. */
export function resolveBinding(id: ShortcutId): Binding | null {
  if (Object.prototype.hasOwnProperty.call(overrides, id)) {
    const ov = overrides[id];
    return ov ?? null;
  }
  return SHORTCUTS[id];
}

export function isUnbound(id: ShortcutId): boolean {
  return resolveBinding(id) === null;
}

export function isCustomized(id: ShortcutId): boolean {
  return Object.prototype.hasOwnProperty.call(overrides, id);
}

/** Bound shortcuts in registry order — the dispatch iteration source. */
export function resolvedShortcutEntries(): Array<[ShortcutId, Binding]> {
  const entries: Array<[ShortcutId, Binding]> = [];
  for (const id of Object.keys(SHORTCUTS) as ShortcutId[]) {
    const def = resolveBinding(id);
    if (def) entries.push([id, def]);
  }
  return entries;
}

/**
 * Find an already-bound shortcut that conflicts with `binding`, excluding
 * `excludeId` and any context-gated allowed-conflict partner. Returns the
 * conflicting id or null. Used by the editor for VSCode-style reassign.
 */
export function findConflict(binding: Binding, excludeId: ShortcutId): ShortcutId | null {
  for (const [id, d] of resolvedShortcutEntries()) {
    if (id === excludeId) continue;
    if (isAllowedConflict(excludeId, id)) continue;
    if (bindingsConflict(binding, d)) return id;
  }
  return null;
}

/** Modifier-only keys never form a binding on their own. */
const MODIFIER_KEYS = new Set([
  'Meta', 'Control', 'Shift', 'Alt', 'AltGraph', 'OS', 'Hyper', 'Super',
  'CapsLock', 'Fn', 'FnLock', 'NumLock', 'ScrollLock', 'Dead',
]);

export type CaptureResult =
  | { kind: 'binding'; def: ShortcutDef }
  | { kind: 'ignored' }
  | { kind: 'error'; message: string };

/**
 * Translate a keydown into a ShortcutDef for the editor's key-capture input.
 * Rejects Control on macOS because the matcher treats Control as the
 * accelerator only on non-Mac platforms — a Ctrl-only binding would never fire.
 */
export function eventToBinding(e: KeyboardEvent): CaptureResult {
  if (!e.key || MODIFIER_KEYS.has(e.key)) return { kind: 'ignored' };

  if (isMacLikePlatform() && e.ctrlKey && !e.metaKey) {
    return {
      kind: 'error',
      message: 'Control isn’t available as a shortcut modifier on macOS. Use ⌘, ⌥, or ⇧.',
    };
  }

  const def: ShortcutDef = { key: e.key };
  if (e.metaKey) def.meta = true;
  if (e.altKey) def.alt = true;
  if (e.shiftKey) def.shift = true;
  // Keep code for keys whose `key` is layout/locale dependent (digits, named keys).
  if (/^Digit\d$/.test(e.code) || e.key.length !== 1) def.code = e.code;

  return { kind: 'binding', def };
}

/**
 * A binding with no accelerator (no ⌘/⌃/⌥) collides with ordinary typing in the
 * terminal and text inputs. The editor surfaces this as a soft warning. For a
 * chord the leader is what must claim a keystroke up front, so the leader is
 * what's evaluated.
 */
export function isRiskyBinding(binding: Binding): boolean {
  const combo = isChord(binding) ? binding.leader : binding;
  return !combo.meta && !combo.ctrl && !combo.alt;
}

// --- Config (de)serialization with tolerant sanitization ---

function sanitizeCombo(value: unknown): Combo | null {
  if (!value || typeof value !== 'object') return null;
  const v = value as Record<string, unknown>;
  if (typeof v.key !== 'string' || v.key.length === 0) return null;
  const def: Combo = { key: v.key };
  if (typeof v.code === 'string') def.code = v.code;
  if (v.meta === true) def.meta = true;
  if (v.ctrl === true) def.ctrl = true;
  if (v.alt === true) def.alt = true;
  if (v.shift === true) def.shift = true;
  if (v.editableTarget === 'native') def.editableTarget = 'native';
  return def;
}

/**
 * Sanitize a persisted binding: a `{leader, then}` shape becomes a Chord (both
 * steps must sanitize, else the whole chord is dropped so the id falls back to
 * its default), otherwise a single Combo.
 */
function sanitizeBinding(value: unknown): Binding | null {
  if (value && typeof value === 'object' && ('leader' in value || 'then' in value)) {
    const v = value as Record<string, unknown>;
    const leader = sanitizeCombo(v.leader);
    const then = sanitizeCombo(v.then);
    if (!leader || !then) return null;
    return { leader, then };
  }
  return sanitizeCombo(value);
}

/** A fresh empty config (own copy of the default dock so callers can't mutate it). */
function emptyConfig(): KeybindingsConfig {
  return { version: 1, overrides: {}, dock: defaultDock() };
}

function defaultDock(): DockConfig {
  return { collapsed: false, items: [...DEFAULT_DOCK_ITEMS] };
}

/**
 * Sanitize a persisted dock blob: keep only real, deduped ShortcutIds in order,
 * coerce `collapsed` to a boolean. A missing/malformed dock falls back to the
 * default so the sidebar always has a usable dock.
 */
function sanitizeDock(value: unknown): DockConfig {
  if (!value || typeof value !== 'object') return defaultDock();
  const v = value as Record<string, unknown>;
  if (!Array.isArray(v.items)) return defaultDock();
  const seen = new Set<string>();
  const items: ShortcutId[] = [];
  for (const id of v.items) {
    if (typeof id !== 'string') continue;
    if (!Object.prototype.hasOwnProperty.call(SHORTCUTS, id)) continue; // unknown id
    if (seen.has(id)) continue; // dedup
    seen.add(id);
    items.push(id as ShortcutId);
  }
  return { collapsed: v.collapsed === true, items };
}

/**
 * Parse a persisted blob into a config, dropping anything unrecognized so a bad
 * value can never crash dispatch (the affected id just keeps its default).
 */
export function parseKeybindingsConfig(raw: string | undefined | null): KeybindingsConfig {
  if (!raw) return emptyConfig();
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return emptyConfig();
  }
  if (!parsed || typeof parsed !== 'object') return emptyConfig();

  const rawOverrides = (parsed as Record<string, unknown>).overrides;
  const overridesOut: Partial<Record<ShortcutId, Binding | null>> = {};
  if (rawOverrides && typeof rawOverrides === 'object') {
    for (const [id, value] of Object.entries(rawOverrides as Record<string, unknown>)) {
      if (!Object.prototype.hasOwnProperty.call(SHORTCUTS, id)) continue; // unknown id
      if (value === null) {
        overridesOut[id as ShortcutId] = null;
        continue;
      }
      const binding = sanitizeBinding(value);
      if (binding) overridesOut[id as ShortcutId] = binding;
      // malformed override -> drop, so the id falls back to its default
    }
  }

  return {
    version: 1,
    overrides: overridesOut,
    dock: sanitizeDock((parsed as Record<string, unknown>).dock),
  };
}

export function serializeKeybindingsConfig(config: KeybindingsConfig): string {
  return JSON.stringify(config);
}
