// app/src/shortcuts/formatShortcut.ts
// Render shortcut definitions as human-readable key tokens, so display
// surfaces (cheatsheet, footer hints, what's-new) stay in sync with the
// single source of truth in registry.ts instead of hardcoding strings.
//
// attn ships as a macOS app, so shortcuts always render with Mac glyphs
// (⌘ ⌥ ⇧). Cross-platform keystroke *matching* lives in registry.ts.

import { SHORTCUTS, ShortcutId, ShortcutDef } from './registry';

// Symbols for non-printable keys. Single-character keys are upper-cased; any
// other key name falls through unchanged.
const KEY_SYMBOLS: Record<string, string> = {
  ArrowLeft: '←',
  ArrowRight: '→',
  ArrowUp: '↑',
  ArrowDown: '↓',
  Enter: '⏎',
  Escape: 'Esc',
  ' ': 'Space',
};

function keyToken(key: string): string {
  if (KEY_SYMBOLS[key]) return KEY_SYMBOLS[key];
  return key.length === 1 ? key.toUpperCase() : key;
}

function resolve(idOrDef: ShortcutId | ShortcutDef): ShortcutDef {
  return typeof idOrDef === 'string' ? SHORTCUTS[idOrDef] : idOrDef;
}

/** Modifier glyphs only, in the order attn shows them (⌘ before ⌃/⌥/⇧). */
export function modifierTokens(idOrDef: ShortcutId | ShortcutDef): string[] {
  const def = resolve(idOrDef);
  const tokens: string[] = [];
  if (def.meta) tokens.push('⌘');
  if (def.ctrl) tokens.push('⌃');
  if (def.alt) tokens.push('⌥');
  if (def.shift) tokens.push('⇧');
  return tokens;
}

/** All tokens for a shortcut, e.g. ['⌘', '⇧', 'N']. One token per keycap. */
export function shortcutTokens(idOrDef: ShortcutId | ShortcutDef): string[] {
  const def = resolve(idOrDef);
  return [...modifierTokens(def), keyToken(def.key)];
}

/** Flat string form, e.g. '⌘⇧N'. Used for compact footer hints. */
export function formatShortcut(idOrDef: ShortcutId | ShortcutDef): string {
  return shortcutTokens(idOrDef).join('');
}
