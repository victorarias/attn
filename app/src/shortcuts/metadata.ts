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

export type ShortcutCategory = 'sessions' | 'panes' | 'markdown' | 'review' | 'app';

export interface ShortcutMeta {
  label: string;
  category: ShortcutCategory;
  /** Cannot be unbound (still rebindable). Guards the escape hatches. */
  protected?: boolean;
  /**
   * Terse text for the sidebar dock chip. Falls back to `label` when absent, so
   * any shortcut is dock-eligible while the default dock entries stay compact.
   */
  dockLabel?: string;
  /**
   * The handler is registered in `SessionTerminalWorkspace` gated by
   * `sessionVisible`, so the shortcut does nothing unless a terminal workspace
   * is on screen. This is an availability fact, NOT a focus claim — the key
   * still fires from the global window listener. Set only on the ids actually
   * gated this way; do not infer it from the `terminal.` id prefix (e.g.
   * `terminal.collapse` has no handler at all).
   */
  requiresTerminal?: boolean;
}

export const SHORTCUT_CATEGORY_LABELS: Record<ShortcutCategory, string> = {
  sessions: 'Workspaces & Sessions',
  panes: 'Panes & Terminals',
  markdown: 'Markdown & Annotations',
  review: 'Review & Git',
  app: 'App',
};

/** Render order for categories in the editor. */
export const SHORTCUT_CATEGORY_ORDER: ShortcutCategory[] = [
  'sessions',
  'panes',
  'markdown',
  'review',
  'app',
];

export const SHORTCUT_META: Record<ShortcutId, ShortcutMeta> = {
  // Workspaces & Sessions
  'session.new': { label: 'New session in this workspace', category: 'sessions' },
  'session.newHorizontal': { label: 'New session, split sideways', category: 'sessions', dockLabel: 'session h' },
  'session.newWorkspace': { label: 'New workspace', category: 'sessions' },
  'session.close': { label: 'Close session (or focused pane)', category: 'sessions' },
  'session.prev': { label: 'Previous session', category: 'sessions' },
  'session.next': { label: 'Next session', category: 'sessions' },
  'session.goToDashboard': { label: 'Go to dashboard (home)', category: 'sessions' },
  'view.toggleGrid': { label: 'Toggle grid view', category: 'sessions' },
  'session.jumpToWaiting': { label: 'Jump to next waiting session', category: 'sessions' },
  'session.toggleSidebar': { label: 'Toggle sidebar', category: 'sessions', dockLabel: 'sidebar' },
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
  'terminal.open': { label: 'Focus utility terminal', category: 'panes', requiresTerminal: true },
  'terminal.collapse': { label: 'Collapse utility terminal', category: 'panes' },
  'terminal.splitVertical': { label: 'Split pane down', category: 'panes', dockLabel: 'split v', requiresTerminal: true },
  'terminal.splitHorizontal': { label: 'Split pane sideways', category: 'panes', dockLabel: 'split h', requiresTerminal: true },
  'terminal.toggleZoom': { label: 'Zoom active pane', category: 'panes', dockLabel: 'zoom', requiresTerminal: true },
  'terminal.toggleMaximize': { label: 'Maximize active pane', category: 'panes', requiresTerminal: true },
  'terminal.close': { label: 'Close focused pane', category: 'panes', requiresTerminal: true },
  'terminal.focusLeft': { label: 'Move focus left', category: 'panes', requiresTerminal: true },
  'terminal.focusRight': { label: 'Move focus right', category: 'panes', requiresTerminal: true },
  'terminal.focusUp': { label: 'Move focus up', category: 'panes', requiresTerminal: true },
  'terminal.focusDown': { label: 'Move focus down', category: 'panes', requiresTerminal: true },
  'terminal.find': { label: 'Find in terminal', category: 'panes', requiresTerminal: true },

  // Markdown & Annotations
  'markdown.sendAnnotations': { label: 'Send annotations to session', category: 'markdown', dockLabel: 'send notes' },

  // Review & Git
  'dock.attention': { label: 'PRs drawer', category: 'review', dockLabel: 'PRs' },
  'session.refreshPRs': { label: 'Refresh PRs', category: 'review' },

  // App
  'ui.actionMenu': { label: 'Action menu', category: 'app' },
  'ui.openSettings': { label: 'Settings', category: 'app', protected: true },
  'ui.showShortcuts': { label: 'Keyboard shortcuts', category: 'app', protected: true },
  'ui.increaseFontSize': { label: 'Increase font size', category: 'app' },
  'ui.decreaseFontSize': { label: 'Decrease font size', category: 'app' },
  'ui.resetFontSize': { label: 'Reset font size', category: 'app' },
  'notebook.openTile': { label: 'Open Editor tile', category: 'app' },
  'notebook.openFullscreen': { label: 'Open Notebook fullscreen', category: 'app' },
  'board.open': { label: 'Open Tickets board', category: 'app' },
  'app.quit': { label: 'Quit attn', category: 'app', protected: true },
};

export function isProtectedShortcut(id: ShortcutId): boolean {
  return SHORTCUT_META[id].protected === true;
}

/** Terse text shown on a dock chip; falls back to the full editor label. */
export function dockShortcutLabel(id: ShortcutId): string {
  return SHORTCUT_META[id].dockLabel ?? SHORTCUT_META[id].label;
}
