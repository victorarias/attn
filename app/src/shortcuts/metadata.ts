// app/src/shortcuts/metadata.ts
// Editor-facing metadata for every shortcut: a human label, a grouping
// category, and whether the binding is protected (rebindable, but never
// allowed to be left unbound so the user can't strand themselves).
//
// Bindings live in registry.ts (the single source of truth for key combos).
// This module is purely about how shortcuts are presented and governed in the
// shortcut editor. The `Record<ShortcutId, ...>` shape forces this map to stay
// exhaustive: adding a shortcut to the registry without metadata fails to
// compile.

import { ShortcutId } from './registry';

export type ShortcutCategory = 'sessions' | 'panes' | 'review' | 'app';

export interface ShortcutMeta {
  label: string;
  category: ShortcutCategory;
  /** Cannot be unbound (still rebindable). Guards the escape hatches. */
  protected?: boolean;
}

export const SHORTCUT_CATEGORY_LABELS: Record<ShortcutCategory, string> = {
  sessions: 'Workspaces & Sessions',
  panes: 'Panes & Terminals',
  review: 'Review & Git',
  app: 'App',
};

/** Render order for categories in the editor. */
export const SHORTCUT_CATEGORY_ORDER: ShortcutCategory[] = [
  'sessions',
  'panes',
  'review',
  'app',
];

export const SHORTCUT_META: Record<ShortcutId, ShortcutMeta> = {
  // Workspaces & Sessions
  'session.new': { label: 'New session in this workspace', category: 'sessions' },
  'session.newHorizontal': { label: 'New session, split sideways', category: 'sessions' },
  'session.newWorkspace': { label: 'New workspace', category: 'sessions' },
  'session.close': { label: 'Close session (or focused pane)', category: 'sessions' },
  'session.prev': { label: 'Previous session', category: 'sessions' },
  'session.next': { label: 'Next session', category: 'sessions' },
  'session.goToDashboard': { label: 'Go to dashboard (home)', category: 'sessions' },
  'view.toggleGrid': { label: 'Toggle grid view', category: 'sessions' },
  'session.jumpToWaiting': { label: 'Jump to next waiting session', category: 'sessions' },
  'session.toggleSidebar': { label: 'Toggle sidebar', category: 'sessions' },
  'workspace.select1': { label: 'Jump to workspace 1', category: 'sessions' },
  'workspace.select2': { label: 'Jump to workspace 2', category: 'sessions' },
  'workspace.select3': { label: 'Jump to workspace 3', category: 'sessions' },
  'workspace.select4': { label: 'Jump to workspace 4', category: 'sessions' },
  'workspace.select5': { label: 'Jump to workspace 5', category: 'sessions' },
  'workspace.select6': { label: 'Jump to workspace 6', category: 'sessions' },
  'workspace.select7': { label: 'Jump to workspace 7', category: 'sessions' },
  'workspace.select8': { label: 'Jump to workspace 8', category: 'sessions' },
  'workspace.select9': { label: 'Jump to workspace 9', category: 'sessions' },

  // Panes & Terminals
  'terminal.open': { label: 'Focus utility terminal', category: 'panes' },
  'terminal.collapse': { label: 'Collapse utility terminal', category: 'panes' },
  'terminal.splitVertical': { label: 'Split pane down', category: 'panes' },
  'terminal.splitHorizontal': { label: 'Split pane sideways', category: 'panes' },
  'terminal.toggleZoom': { label: 'Zoom active pane', category: 'panes' },
  'terminal.toggleMaximize': { label: 'Maximize active pane', category: 'panes' },
  'terminal.close': { label: 'Close focused pane', category: 'panes' },
  'terminal.focusLeft': { label: 'Move focus left', category: 'panes' },
  'terminal.focusRight': { label: 'Move focus right', category: 'panes' },
  'terminal.focusUp': { label: 'Move focus up', category: 'panes' },
  'terminal.focusDown': { label: 'Move focus down', category: 'panes' },
  'terminal.find': { label: 'Find in terminal', category: 'panes' },

  // Review & Git
  'dock.diff': { label: 'Diff panel', category: 'review' },
  'dock.diffDetail': { label: 'Diff detail', category: 'review' },
  'dock.reviewLoop': { label: 'Review loop', category: 'review' },
  'dock.attention': { label: 'PRs drawer', category: 'review' },
  'session.refreshPRs': { label: 'Refresh PRs', category: 'review' },

  // App
  'terminal.quickFind': { label: 'Quick Find', category: 'app' },
  'ui.actionMenu': { label: 'Action menu', category: 'app' },
  'ui.openSettings': { label: 'Settings', category: 'app', protected: true },
  'ui.showShortcuts': { label: 'Keyboard shortcuts', category: 'app', protected: true },
  'ui.increaseFontSize': { label: 'Increase font size', category: 'app' },
  'ui.decreaseFontSize': { label: 'Decrease font size', category: 'app' },
  'ui.resetFontSize': { label: 'Reset font size', category: 'app' },
  'app.quit': { label: 'Quit attn', category: 'app', protected: true },
};

export function isProtectedShortcut(id: ShortcutId): boolean {
  return SHORTCUT_META[id].protected === true;
}
