// app/src/contexts/KeybindingsContext.tsx
// Bridges persisted shortcut overrides (one JSON settings blob) to the
// imperative resolver used by dispatch/formatting, and exposes mutations to the
// shortcut editor. Lives inside SettingsProvider (it reads/writes via settings).

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  ReactNode,
} from 'react';
import { useSettings } from './SettingsContext';
import { ShortcutDef, ShortcutId } from '../shortcuts/registry';
import { isProtectedShortcut } from '../shortcuts/metadata';
import {
  KeybindingsConfig,
  DockConfig,
  DEFAULT_DOCK,
  KEYBINDINGS_SETTING_KEY,
  parseKeybindingsConfig,
  serializeKeybindingsConfig,
  setShortcutOverrides,
  resolveBinding,
  findConflict,
} from '../shortcuts/resolver';

// def -> rebind, null -> unbind, undefined -> reset to default (drop override).
export type OverrideChange = ShortcutDef | null | undefined;

interface KeybindingsContextValue {
  config: KeybindingsConfig;
  dock: DockConfig;
  resolve: (id: ShortcutId) => ShortcutDef | null;
  isProtected: (id: ShortcutId) => boolean;
  isCustomized: (id: ShortcutId) => boolean;
  findConflict: (def: ShortcutDef, excludeId: ShortcutId) => ShortcutId | null;
  /** Apply one or more override changes in a single persisted write (atomic). */
  applyOverrides: (changes: Partial<Record<ShortcutId, OverrideChange>>) => void;
  isInDock: (id: ShortcutId) => boolean;
  /** Add (append) or remove a shortcut from the dock membership list. */
  setInDock: (id: ShortcutId, inDock: boolean) => void;
  /** Move a dock item one slot earlier (-1) or later (+1); no-op at the ends. */
  moveDockItem: (id: ShortcutId, direction: -1 | 1) => void;
  setDockCollapsed: (collapsed: boolean) => void;
  restoreDefaults: () => void;
}

const KeybindingsContext = createContext<KeybindingsContextValue | null>(null);

export function KeybindingsProvider({ children }: { children: ReactNode }) {
  const { settings, setSetting } = useSettings();
  const raw = settings[KEYBINDINGS_SETTING_KEY];

  const [config, setConfig] = useState<KeybindingsConfig>(() => {
    const parsed = parseKeybindingsConfig(raw);
    setShortcutOverrides(parsed.overrides);
    return parsed;
  });
  const configRef = useRef(config);
  configRef.current = config;
  // Last value we either received from or wrote to settings, so echoes of our
  // own writes don't clobber newer local state.
  const lastSyncedRef = useRef<string>(serializeKeybindingsConfig(config));

  // Adopt external changes (other windows, fresh load).
  useEffect(() => {
    const parsed = parseKeybindingsConfig(raw);
    const serialized = serializeKeybindingsConfig(parsed);
    if (serialized === lastSyncedRef.current) return;
    lastSyncedRef.current = serialized;
    configRef.current = parsed;
    setShortcutOverrides(parsed.overrides);
    setConfig(parsed);
  }, [raw]);

  const commit = useCallback((next: KeybindingsConfig) => {
    const serialized = serializeKeybindingsConfig(next);
    lastSyncedRef.current = serialized;
    configRef.current = next;
    setShortcutOverrides(next.overrides);
    setConfig(next);
    setSetting(KEYBINDINGS_SETTING_KEY, serialized);
  }, [setSetting]);

  const applyOverrides = useCallback((changes: Partial<Record<ShortcutId, OverrideChange>>) => {
    const overrides = { ...configRef.current.overrides };
    for (const [id, change] of Object.entries(changes) as Array<[ShortcutId, OverrideChange]>) {
      // Never leave a protected shortcut unbound.
      if (change === null && isProtectedShortcut(id)) continue;
      if (change === undefined) {
        delete overrides[id];
      } else {
        overrides[id] = change;
      }
    }
    commit({ ...configRef.current, overrides });
  }, [commit]);

  const setInDock = useCallback((id: ShortcutId, inDock: boolean) => {
    const items = configRef.current.dock.items;
    const present = items.includes(id);
    if (inDock === present) return;
    const nextItems = inDock ? [...items, id] : items.filter((x) => x !== id);
    commit({ ...configRef.current, dock: { ...configRef.current.dock, items: nextItems } });
  }, [commit]);

  const moveDockItem = useCallback((id: ShortcutId, direction: -1 | 1) => {
    const items = [...configRef.current.dock.items];
    const from = items.indexOf(id);
    if (from === -1) return;
    const to = from + direction;
    if (to < 0 || to >= items.length) return;
    [items[from], items[to]] = [items[to], items[from]];
    commit({ ...configRef.current, dock: { ...configRef.current.dock, items } });
  }, [commit]);

  const setDockCollapsed = useCallback((collapsed: boolean) => {
    if (configRef.current.dock.collapsed === collapsed) return;
    commit({ ...configRef.current, dock: { ...configRef.current.dock, collapsed } });
  }, [commit]);

  const restoreDefaults = useCallback(() => {
    commit({ version: 1, overrides: {}, dock: { ...DEFAULT_DOCK, items: [...DEFAULT_DOCK.items] } });
  }, [commit]);

  const value = useMemo<KeybindingsContextValue>(() => ({
    config,
    dock: config.dock,
    resolve: resolveBinding,
    isProtected: isProtectedShortcut,
    isCustomized: (id: ShortcutId) =>
      Object.prototype.hasOwnProperty.call(config.overrides, id),
    findConflict,
    applyOverrides,
    isInDock: (id: ShortcutId) => config.dock.items.includes(id),
    setInDock,
    moveDockItem,
    setDockCollapsed,
    restoreDefaults,
  }), [config, applyOverrides, setInDock, moveDockItem, setDockCollapsed, restoreDefaults]);

  return (
    <KeybindingsContext.Provider value={value}>
      {children}
    </KeybindingsContext.Provider>
  );
}

export function useKeybindings(): KeybindingsContextValue {
  const ctx = useContext(KeybindingsContext);
  if (!ctx) {
    throw new Error('useKeybindings must be used within a KeybindingsProvider');
  }
  return ctx;
}
