// Editor-facing metadata for every shortcut; key combos live in registry.ts. The
// `Record<ShortcutId, ...>` shape keeps this map exhaustive.

import { ShortcutId } from './registry';
import { isMacLikePlatform } from './platform';

export type ShortcutCategory = 'sessions' | 'panes' | 'markdown' | 'review' | 'app';

export interface ShortcutMeta {
  label: string;
  category: ShortcutCategory;
  /** Cannot be unbound (still rebindable). Guards the escape hatches. */
  protected?: boolean;
  /** Terse dock-chip text; falls back to `label`, so any shortcut is dock-eligible. */
  dockLabel?: string;
  /** An availability fact, not a focus claim; never inferred from the `terminal.` prefix. */
  requiresTerminal?: boolean;
  /** A native menu item in `app_menu` (src-tauri/src/lib.rs) delivers the keystroke
   * because AppKit consumes it first, so a rebind here would never fire. */
  nativeDelivery?: boolean;
}

export const SHORTCUT_CATEGORY_LABELS: Record<ShortcutCategory, string> = {
  sessions: 'Workspaces & Sessions',
  panes: 'Panes & Terminals',
  markdown: 'Markdown & Annotations',
  review: 'Review & Git',
  app: 'App',
};

export const SHORTCUT_CATEGORY_ORDER: ShortcutCategory[] = [
  'sessions',
  'panes',
  'markdown',
  'review',
  'app',
];

export const SHORTCUT_META: Record<ShortcutId, ShortcutMeta> = {
  'session.new': { label: 'New session in this workspace', category: 'sessions' },
  'session.newHorizontal': { label: 'New session, split sideways', category: 'sessions', dockLabel: 'session h' },
  'session.newWorkspace': { label: 'New workspace', category: 'sessions' },
  'session.close': { label: 'Close session (or focused pane)', category: 'sessions' },
  'session.prev': { label: 'Previous session', category: 'sessions' },
  'session.next': { label: 'Next session', category: 'sessions' },
  'session.goToDashboard': { label: 'Go to dashboard (home)', category: 'sessions' },
  'view.toggleGrid': { label: 'Toggle grid view', category: 'sessions' },
  'session.jumpToWaiting': { label: 'Jump to next waiting session', category: 'sessions' },
  'session.settle': { label: 'Settle turn, go to next', category: 'sessions' },
  'session.snooze': { label: 'Snooze this agent', category: 'sessions' },
  'session.cancelCountdown': { label: 'Stop the countdown, or keep the next turn', category: 'sessions', nativeDelivery: true },
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

  'terminal.open': { label: 'Focus utility terminal', category: 'panes', requiresTerminal: true },
  'terminal.collapse': { label: 'Collapse utility terminal', category: 'panes' },
  'terminal.splitVertical': { label: 'Split pane down', category: 'panes', dockLabel: 'split v', requiresTerminal: true },
  'terminal.splitHorizontal': { label: 'Split pane sideways', category: 'panes', dockLabel: 'split h', requiresTerminal: true },
  'terminal.toggleZoom': { label: 'Zoom active pane', category: 'panes', dockLabel: 'zoom', requiresTerminal: true },
  'terminal.toggleMaximize': { label: 'Focus active pane', category: 'panes', requiresTerminal: true },
  'terminal.close': { label: 'Close focused pane', category: 'panes', requiresTerminal: true },
  'terminal.focusLeft': { label: 'Move focus left', category: 'panes', requiresTerminal: true },
  'terminal.focusRight': { label: 'Move focus right', category: 'panes', requiresTerminal: true },
  'terminal.focusUp': { label: 'Move focus up', category: 'panes', requiresTerminal: true },
  'terminal.focusDown': { label: 'Move focus down', category: 'panes', requiresTerminal: true },
  'terminal.find': { label: 'Find in terminal', category: 'panes', requiresTerminal: true },

  'markdown.sendAnnotations': { label: 'Send annotations to session', category: 'markdown', dockLabel: 'send notes' },
  'terminal.sendAnnotations': { label: 'Send terminal annotations to session', category: 'markdown', dockLabel: 'send marks', requiresTerminal: true },

  'dock.attention': { label: 'PRs drawer', category: 'review', dockLabel: 'PRs' },
  'session.refreshPRs': { label: 'Refresh PRs', category: 'review' },

  'ui.actionMenu': { label: 'Action menu', category: 'app' },
  'ui.openSettings': { label: 'Settings', category: 'app', protected: true },
  'ui.showShortcuts': { label: 'Keyboard shortcuts', category: 'app', protected: true },
  'ui.increaseFontSize': { label: 'Increase font size', category: 'app' },
  'ui.decreaseFontSize': { label: 'Decrease font size', category: 'app' },
  'ui.resetFontSize': { label: 'Reset font size', category: 'app' },
  'file.open': { label: 'Open a markdown file', category: 'app' },
  'notebook.openTile': { label: 'Open Editor tile', category: 'app' },
  'notebook.openFullscreen': { label: 'Open Notebook fullscreen', category: 'app' },
  // The id outlives the surface it was named for: it keys the user's saved rebindings.
  'board.open': { label: 'Open the garden', category: 'app' },
  'sessions.open': { label: 'Open the sessions list', category: 'app' },
  'app.quit': { label: 'Quit attn', category: 'app', protected: true },
};

export function isProtectedShortcut(id: ShortcutId): boolean {
  return SHORTCUT_META[id].protected === true;
}

export function isNativeDeliveryShortcut(id: ShortcutId): boolean {
  return isMacLikePlatform() && SHORTCUT_META[id].nativeDelivery === true;
}

export function dockShortcutLabel(id: ShortcutId): string {
  return SHORTCUT_META[id].dockLabel ?? SHORTCUT_META[id].label;
}
