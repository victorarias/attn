// app/src/shortcuts/index.ts
export {
  SHORTCUTS,
  type ShortcutId,
  type ShortcutDef,
  type Combo,
  type Chord,
  type Binding,
  isChord,
  matchesShortcut,
  bindingsConflict,
  combosConflict,
  isAllowedConflict,
} from './registry';
export {
  LEADER_TIMEOUT_MS,
  type ChordCandidate,
  subscribeChord,
  getChordSnapshot,
  isLeaderPending,
  enterLeader,
  cancelLeader,
  resolvePendingThen,
} from './chordState';
export { matchChordLeader } from './chordDispatch';
export { useShortcut, setShortcutCaptureSuspended } from './useShortcut';
export { formatShortcut, shortcutTokens, modifierTokens } from './formatShortcut';
export {
  buildCheatsheet,
  type CheatsheetRow,
  type CheatsheetCategory,
} from './cheatsheet';
export {
  type KeybindingsConfig,
  type DockConfig,
  KEYBINDINGS_SETTING_KEY,
  EMPTY_KEYBINDINGS_CONFIG,
  DEFAULT_DOCK,
  DEFAULT_DOCK_ITEMS,
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
  dockShortcutLabel,
} from './metadata';
