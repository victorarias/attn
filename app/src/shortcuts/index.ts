// app/src/shortcuts/index.ts
export {
  SHORTCUTS,
  type ShortcutId,
  type ShortcutDef,
  matchesShortcut,
  shortcutToKey,
  isAllowedConflict,
} from './registry';
export { useShortcut, setShortcutCaptureSuspended } from './useShortcut';
export { formatShortcut, shortcutTokens, modifierTokens } from './formatShortcut';
export {
  buildCheatsheet,
  type CheatsheetRow,
  type CheatsheetCategory,
} from './cheatsheet';
export {
  type KeybindingsConfig,
  KEYBINDINGS_SETTING_KEY,
  EMPTY_KEYBINDINGS_CONFIG,
  resolveBinding,
  resolvedShortcutEntries,
  isUnbound,
  isCustomized,
  findConflict,
  eventToBinding,
  isRiskyBinding,
  setShortcutOverrides,
  parseKeybindingsConfig,
  serializeKeybindingsConfig,
} from './resolver';
export {
  type ShortcutCategory,
  type ShortcutMeta,
  SHORTCUT_META,
  SHORTCUT_CATEGORY_LABELS,
  SHORTCUT_CATEGORY_ORDER,
  isProtectedShortcut,
} from './metadata';
