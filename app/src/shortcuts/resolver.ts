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
  bindingsConflict,
  isAllowedConflict,
} from './registry';
import { isMacLikePlatform } from './platform';

export interface KeybindingsConfig {
  version: 1;
  // Absent id  -> use default.
  // ShortcutDef -> rebind to this combo.
  // null        -> explicitly unbound.
  overrides: Partial<Record<ShortcutId, ShortcutDef | null>>;
}

export const EMPTY_KEYBINDINGS_CONFIG: KeybindingsConfig = { version: 1, overrides: {} };

export const KEYBINDINGS_SETTING_KEY = 'keybindings_config';

// --- Module-level resolved state (read by the global dispatch + formatting) ---

let overrides: Partial<Record<ShortcutId, ShortcutDef | null>> = {};

/** Replace the active override set (called when settings load or the user edits). */
export function setShortcutOverrides(next: Partial<Record<ShortcutId, ShortcutDef | null>>): void {
  overrides = { ...next };
}

export function getShortcutOverrides(): Partial<Record<ShortcutId, ShortcutDef | null>> {
  return overrides;
}

/** The effective binding for an id, or null when unbound. */
export function resolveBinding(id: ShortcutId): ShortcutDef | null {
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
export function resolvedShortcutEntries(): Array<[ShortcutId, ShortcutDef]> {
  const entries: Array<[ShortcutId, ShortcutDef]> = [];
  for (const id of Object.keys(SHORTCUTS) as ShortcutId[]) {
    const def = resolveBinding(id);
    if (def) entries.push([id, def]);
  }
  return entries;
}

/**
 * Find an already-bound shortcut that uses the same combo as `def`, excluding
 * `excludeId` and any context-gated allowed-conflict partner. Returns the
 * conflicting id or null. Used by the editor for VSCode-style reassign.
 */
export function findConflict(def: ShortcutDef, excludeId: ShortcutId): ShortcutId | null {
  for (const [id, d] of resolvedShortcutEntries()) {
    if (id === excludeId) continue;
    if (isAllowedConflict(excludeId, id)) continue;
    if (bindingsConflict(def, d)) return id;
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
 * terminal and text inputs. The editor surfaces this as a soft warning.
 */
export function isRiskyBinding(def: ShortcutDef): boolean {
  return !def.meta && !def.ctrl && !def.alt;
}

// --- Config (de)serialization with tolerant sanitization ---

function sanitizeDef(value: unknown): ShortcutDef | null {
  if (!value || typeof value !== 'object') return null;
  const v = value as Record<string, unknown>;
  if (typeof v.key !== 'string' || v.key.length === 0) return null;
  const def: ShortcutDef = { key: v.key };
  if (typeof v.code === 'string') def.code = v.code;
  if (v.meta === true) def.meta = true;
  if (v.ctrl === true) def.ctrl = true;
  if (v.alt === true) def.alt = true;
  if (v.shift === true) def.shift = true;
  if (v.editableTarget === 'native') def.editableTarget = 'native';
  return def;
}

/**
 * Parse a persisted blob into a config, dropping anything unrecognized so a bad
 * value can never crash dispatch (the affected id just keeps its default).
 */
export function parseKeybindingsConfig(raw: string | undefined | null): KeybindingsConfig {
  if (!raw) return { version: 1, overrides: {} };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { version: 1, overrides: {} };
  }
  if (!parsed || typeof parsed !== 'object') return { version: 1, overrides: {} };

  const rawOverrides = (parsed as Record<string, unknown>).overrides;
  const overridesOut: Partial<Record<ShortcutId, ShortcutDef | null>> = {};
  if (rawOverrides && typeof rawOverrides === 'object') {
    for (const [id, value] of Object.entries(rawOverrides as Record<string, unknown>)) {
      if (!Object.prototype.hasOwnProperty.call(SHORTCUTS, id)) continue; // unknown id
      if (value === null) {
        overridesOut[id as ShortcutId] = null;
        continue;
      }
      const def = sanitizeDef(value);
      if (def) overridesOut[id as ShortcutId] = def;
      // malformed override -> drop, so the id falls back to its default
    }
  }

  return { version: 1, overrides: overridesOut };
}

export function serializeKeybindingsConfig(config: KeybindingsConfig): string {
  return JSON.stringify(config);
}
