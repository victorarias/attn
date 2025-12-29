// app/src/shortcuts/registry.ts

export interface ShortcutDef {
  key: string;
  meta?: boolean;
  shift?: boolean;
  ctrl?: boolean;
  alt?: boolean;
}

export const SHORTCUTS = {
  // Session management (existing)
  'session.new': { key: 'n', meta: true },
  'session.newWorktree': { key: 'n', meta: true, shift: true },
  'session.close': { key: 'w', meta: true },
  'session.prev': { key: 'ArrowUp', meta: true },
  'session.next': { key: 'ArrowDown', meta: true },
  'session.goToDashboard': { key: 'd', meta: true },
  'session.jumpToWaiting': { key: 'j', meta: true },
  'session.toggleSidebar': { key: 'b', meta: true, shift: true },
  'session.openBranchPicker': { key: 'b', meta: true },
  'session.refreshPRs': { key: 'r', meta: true },
  'session.fork': { key: 'f', meta: true, shift: true },

  // Drawer
  'drawer.toggle': { key: 'k', meta: true },

  // Font scaling
  'ui.increaseFontSize': { key: '=', meta: true },
  'ui.decreaseFontSize': { key: '-', meta: true },
  'ui.resetFontSize': { key: '0', meta: true },

  // Quick Find (thumbs)
  'terminal.quickFind': { key: 'f', meta: true },

  // Terminal panel
  'terminal.open': { key: '`', meta: true },
  'terminal.collapse': { key: '~', shift: true },  // Shift+` produces ~ on US keyboards
  'terminal.new': { key: 't', meta: true },
  'terminal.close': { key: 'w', meta: true, shift: true },
  'terminal.prevTab': { key: '{', meta: true, shift: true },  // Shift+[ produces {
  'terminal.nextTab': { key: '}', meta: true, shift: true },  // Shift+] produces }
} as const;

export type ShortcutId = keyof typeof SHORTCUTS;

/**
 * Convert a ShortcutDef to a unique string key for conflict detection
 */
function shortcutToKey(def: ShortcutDef): string {
  const parts: string[] = [];
  if (def.meta) parts.push('meta');
  if (def.ctrl) parts.push('ctrl');
  if (def.alt) parts.push('alt');
  if (def.shift) parts.push('shift');
  parts.push(def.key.toLowerCase());
  return parts.join('+');
}

/**
 * Validate that no two shortcuts have the same key combination.
 * Throws an error at startup if conflicts are found.
 */
export function validateNoConflicts(): void {
  const seen = new Map<string, ShortcutId>();

  for (const [id, def] of Object.entries(SHORTCUTS)) {
    const key = shortcutToKey(def as ShortcutDef);
    const existing = seen.get(key);
    if (existing) {
      throw new Error(
        `Shortcut conflict: "${id}" and "${existing}" both use ${key}`
      );
    }
    seen.set(key, id as ShortcutId);
  }
}

/**
 * Check if a keyboard event matches a shortcut definition
 */
export function matchesShortcut(e: KeyboardEvent, def: ShortcutDef): boolean {
  const keyMatches = e.key.toLowerCase() === def.key.toLowerCase();
  const metaMatches = !!def.meta === (e.metaKey || e.ctrlKey);
  const shiftMatches = !!def.shift === e.shiftKey;
  const altMatches = !!def.alt === e.altKey;

  return keyMatches && metaMatches && shiftMatches && altMatches;
}

// Validate on module load
validateNoConflicts();
