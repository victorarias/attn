
import { ShortcutId } from './registry';
import { modifierTokens, shortcutTokens } from './formatShortcut';

export interface CheatsheetRow {
  label: string;
  combos: string[][];
  note?: string;
}

export interface CheatsheetCategory {
  title: string;
  rows: CheatsheetRow[];
}

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
        { label: 'Jump to workspace 1–9', combos: [[...modifierTokens('workspace.select1'), '1–9']] },
        { label: 'Go to dashboard (home)', combos: [fromId('session.goToDashboard')] },
        { label: 'Toggle grid view', combos: [fromId('view.toggleGrid')] },
        { label: 'Jump to next waiting session', combos: [fromId('session.jumpToWaiting')] },
        { label: 'Settle turn, go to next', combos: [fromId('session.settle')] },
        { label: 'Snooze this agent', combos: [fromId('session.snooze')] },
        { label: 'Stop the countdown, or keep the next turn', combos: [fromId('session.cancelCountdown')] },
        { label: 'Toggle sidebar', combos: [fromId('session.toggleSidebar')] },
        {
          label: 'Sessions list, live and closed',
          combos: [fromId('sessions.open')],
          note: 'Reopen a closed session from here.',
        },
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
          combos: [[...modifierTokens('terminal.focusLeft'), '←↑→↓']],
          note: 'Crosses into the next workspace at an edge.',
        },
        { label: 'Zoom active pane', combos: [fromId('terminal.toggleZoom')] },
        { label: 'Focus active pane', combos: [fromId('terminal.toggleMaximize')] },
        { label: 'Focus utility terminal', combos: [fromId('terminal.open')] },
      ],
    },
    {
      title: 'Markdown & Annotations',
      rows: [
        {
          label: 'Send annotations to session',
          combos: [fromId('markdown.sendAnnotations')],
          note: 'When a markdown tile has focus and annotations exist.',
        },
        {
          label: 'Send terminal annotations to session',
          combos: [fromId('terminal.sendAnnotations')],
          note: 'When the annotated pane has focus and marks are waiting.',
        },
      ],
    },
    {
      title: 'Review & Git',
      rows: [
        { label: 'PRs drawer', combos: [fromId('dock.attention')] },
        { label: 'Refresh PRs', combos: [fromId('session.refreshPRs')] },
      ],
    },
    {
      title: 'App',
      rows: [
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
