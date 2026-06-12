// app/src/shortcuts/cheatsheet.ts
// Presentation model for the keyboard shortcuts cheatsheet.
//
// Key bindings stay in registry.ts (single source of truth); this module only
// owns labels, grouping, and ordering. Rows reference registry ids and derive
// their keycaps via formatShortcut, except for a few rows that are clearer when
// collapsed (pane arrows, workspace numbers) and supply explicit tokens.

import { ShortcutId } from './registry';
import { shortcutTokens } from './formatShortcut';

export interface CheatsheetRow {
  label: string;
  // Each entry is one keycap-combo (array of tokens). Multiple combos render
  // separated by "/" (e.g. previous / next session).
  combos: string[][];
  note?: string;
}

export interface CheatsheetCategory {
  title: string;
  rows: CheatsheetRow[];
}

/** One combo derived from a registry id. */
function fromId(id: ShortcutId): string[] {
  return shortcutTokens(id);
}

export function buildCheatsheet(): CheatsheetCategory[] {
  return [
    {
      title: 'Workspaces & Sessions',
      rows: [
        { label: 'New session in this workspace', combos: [fromId('session.new')] },
        { label: 'New session, split sideways', combos: [fromId('session.newHorizontal')] },
        { label: 'New workspace', combos: [fromId('session.newWorkspace')] },
        { label: 'Close session (or focused pane)', combos: [fromId('session.close')] },
        {
          label: 'Previous / next session',
          combos: [fromId('session.prev'), fromId('session.next')],
        },
        { label: 'Jump to workspace 1–9', combos: [['⌘', '1–9']] },
        { label: 'Go to dashboard (home)', combos: [fromId('session.goToDashboard')] },
        { label: 'Toggle grid view', combos: [fromId('view.toggleGrid')] },
        { label: 'Jump to next waiting session', combos: [fromId('session.jumpToWaiting')] },
        { label: 'Toggle sidebar', combos: [fromId('session.toggleSidebar')] },
      ],
    },
    {
      title: 'Panes & Terminals',
      rows: [
        { label: 'Find in terminal', combos: [fromId('terminal.find')] },
        { label: 'Split pane down', combos: [fromId('terminal.splitVertical')] },
        { label: 'Split pane sideways', combos: [fromId('terminal.splitHorizontal')] },
        {
          label: 'Move focus between panes',
          combos: [['⌘', '⌥', '←↑→↓']],
          note: 'Crosses into the next workspace at an edge.',
        },
        { label: 'Zoom active pane', combos: [fromId('terminal.toggleZoom')] },
        { label: 'Maximize active pane', combos: [fromId('terminal.toggleMaximize')] },
        { label: 'Focus utility terminal', combos: [fromId('terminal.open')] },
      ],
    },
    {
      title: 'Review & Git',
      rows: [
        { label: 'Diff panel', combos: [fromId('dock.diff')] },
        { label: 'Diff detail', combos: [fromId('dock.diffDetail')] },
        { label: 'Review loop', combos: [fromId('dock.reviewLoop')] },
        { label: 'PRs drawer', combos: [fromId('dock.attention')] },
        { label: 'Refresh PRs', combos: [fromId('session.refreshPRs')] },
      ],
    },
    {
      title: 'App',
      rows: [
        { label: 'Quick Find', combos: [fromId('terminal.quickFind')] },
        { label: 'Action menu', combos: [fromId('ui.actionMenu')] },
        { label: 'Settings', combos: [fromId('ui.openSettings')] },
        {
          label: 'Font size up / down / reset',
          combos: [
            fromId('ui.increaseFontSize'),
            fromId('ui.decreaseFontSize'),
            fromId('ui.resetFontSize'),
          ],
        },
        { label: 'Keyboard shortcuts', combos: [fromId('ui.showShortcuts')] },
        { label: 'Quit attn', combos: [fromId('app.quit')] },
      ],
    },
  ];
}
