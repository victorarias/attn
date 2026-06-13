// app/src/shortcuts/formatShortcut.ts
// Render shortcut definitions as human-readable key tokens, so display
// surfaces (cheatsheet, footer hints, what's-new) stay in sync with the
// single source of truth in registry.ts instead of hardcoding strings.
//
// attn ships as a macOS app, so shortcuts always render with Mac glyphs
// (⌘ ⌥ ⇧). Cross-platform keystroke *matching* lives in registry.ts.

import { ShortcutId, Binding, Combo, isChord } from './registry';
import { resolveBinding } from './resolver';

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

// Resolve a string id through the override-aware resolver so display surfaces
// reflect rebinds; a passed Binding is used verbatim. Returns null when the id
// resolves to "unbound".
function resolve(idOrBinding: ShortcutId | Binding): Binding | null {
  return typeof idOrBinding === 'string' ? resolveBinding(idOrBinding) : idOrBinding;
}

/** Tokens for a single combo, e.g. ['⌘', '⇧', 'N']. */
function comboTokens(combo: Combo): string[] {
  const tokens: string[] = [];
  if (combo.meta) tokens.push('⌘');
  if (combo.ctrl) tokens.push('⌃');
  if (combo.alt) tokens.push('⌥');
  if (combo.shift) tokens.push('⇧');
  tokens.push(keyToken(combo.key));
  return tokens;
}

/** Modifier glyphs only, in the order attn shows them (⌘ before ⌃/⌥/⇧). For a
 * chord this is the leader's modifiers. */
export function modifierTokens(idOrBinding: ShortcutId | Binding): string[] {
  const binding = resolve(idOrBinding);
  if (!binding) return [];
  const combo = isChord(binding) ? binding.leader : binding;
  const tokens: string[] = [];
  if (combo.meta) tokens.push('⌘');
  if (combo.ctrl) tokens.push('⌃');
  if (combo.alt) tokens.push('⌥');
  if (combo.shift) tokens.push('⇧');
  return tokens;
}

/**
 * Flat keycap tokens. For a combo: ['⌘', '⇧', 'N']. For a chord the steps are
 * joined by a literal 'then' token (['⌘', 'K', 'then', 'D']) so keycap
 * renderers show the sequence. Empty when unbound.
 */
export function shortcutTokens(idOrBinding: ShortcutId | Binding): string[] {
  const binding = resolve(idOrBinding);
  if (!binding) return [];
  if (isChord(binding)) {
    return [...comboTokens(binding.leader), 'then', ...comboTokens(binding.then)];
  }
  return comboTokens(binding);
}

/**
 * Flat string form. Combo: '⌘⇧N'. Chord: '⌘K then D'. Empty when unbound.
 */
export function formatShortcut(idOrBinding: ShortcutId | Binding): string {
  const binding = resolve(idOrBinding);
  if (!binding) return '';
  if (isChord(binding)) {
    return `${comboTokens(binding.leader).join('')} then ${comboTokens(binding.then).join('')}`;
  }
  return comboTokens(binding).join('');
}
