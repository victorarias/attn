// app/src/hooks/useKeyboardShortcuts.ts
import { useEffect } from 'react';
import { useShortcut } from '../shortcuts';
import { isAccelKeyPressed } from '../shortcuts/platform';

interface KeyboardShortcutsConfig {
  onNewSession: () => void;
  onNewSessionHorizontal?: () => void;
  onNewWorkspace?: () => void;
  onCloseSession: () => void;
  onToggleActionMenu: () => void;
  onGoToDashboard: () => void;
  onToggleGridMode?: () => void;
  onJumpToWaiting: () => void;
  onSelectWorkspaceByIndex: (index: number) => void;
  onPrevSession: () => void;
  onNextSession: () => void;
  onToggleSidebar?: () => void;
  onRefreshPRs?: () => void;
  onToggleAttentionPanel?: () => void;
  onOpenSettings?: () => void;
  onShowShortcuts?: () => void;
  onIncreaseFontSize?: () => void;
  onDecreaseFontSize?: () => void;
  onResetFontSize?: () => void;
  onOpenNotebookTile?: () => void;
  onOpenNotebookFullscreen?: () => void;
  onOpenBoard?: () => void;
  onQuit?: () => void;
  enabled: boolean;
}

export function useKeyboardShortcuts({
  onNewSession,
  onNewSessionHorizontal,
  onNewWorkspace,
  onCloseSession,
  onToggleActionMenu,
  onGoToDashboard,
  onToggleGridMode,
  onJumpToWaiting,
  onSelectWorkspaceByIndex,
  onPrevSession,
  onNextSession,
  onToggleSidebar,
  onRefreshPRs,
  onToggleAttentionPanel,
  onOpenSettings,
  onShowShortcuts,
  onIncreaseFontSize,
  onDecreaseFontSize,
  onResetFontSize,
  onOpenNotebookTile,
  onOpenNotebookFullscreen,
  onOpenBoard,
  onQuit,
  enabled,
}: KeyboardShortcutsConfig) {
  useShortcut('app.quit', onQuit ?? (() => {}), !!onQuit);

  // Session management
  useShortcut('session.new', onNewSession, enabled);
  useShortcut('session.newHorizontal', onNewSessionHorizontal ?? (() => {}), enabled && !!onNewSessionHorizontal);
  useShortcut('session.newWorkspace', onNewWorkspace ?? (() => {}), enabled && !!onNewWorkspace);
  useShortcut('session.close', onCloseSession, enabled);
  useShortcut('session.prev', onPrevSession, enabled);
  useShortcut('session.next', onNextSession, enabled);
  useShortcut('session.goToDashboard', onGoToDashboard, enabled);
  useShortcut('view.toggleGrid', onToggleGridMode ?? (() => {}), enabled && !!onToggleGridMode);
  useShortcut('session.jumpToWaiting', onJumpToWaiting, enabled);
  useShortcut('session.toggleSidebar', onToggleSidebar ?? (() => {}), enabled && !!onToggleSidebar);
  useShortcut('session.refreshPRs', onRefreshPRs ?? (() => {}), enabled && !!onRefreshPRs);
  useShortcut('workspace.select1', () => onSelectWorkspaceByIndex(0), enabled);
  useShortcut('workspace.select2', () => onSelectWorkspaceByIndex(1), enabled);
  useShortcut('workspace.select3', () => onSelectWorkspaceByIndex(2), enabled);
  useShortcut('workspace.select4', () => onSelectWorkspaceByIndex(3), enabled);
  useShortcut('workspace.select5', () => onSelectWorkspaceByIndex(4), enabled);
  useShortcut('workspace.select6', () => onSelectWorkspaceByIndex(5), enabled);
  useShortcut('workspace.select7', () => onSelectWorkspaceByIndex(6), enabled);
  useShortcut('workspace.select8', () => onSelectWorkspaceByIndex(7), enabled);
  useShortcut('workspace.select9', () => onSelectWorkspaceByIndex(8), enabled);
  useShortcut('dock.attention', onToggleAttentionPanel ?? (() => {}), enabled && !!onToggleAttentionPanel);

  // Action menu remains available while its own input is focused.
  useShortcut('ui.actionMenu', onToggleActionMenu, true);

  // Settings (always enabled)
  useShortcut('ui.openSettings', onOpenSettings ?? (() => {}), !!onOpenSettings);

  // Keyboard shortcuts cheatsheet (always enabled)
  useShortcut('ui.showShortcuts', onShowShortcuts ?? (() => {}), !!onShowShortcuts);

  // Font scaling (always enabled)
  useShortcut('ui.increaseFontSize', onIncreaseFontSize ?? (() => {}), !!onIncreaseFontSize);
  useShortcut('ui.decreaseFontSize', onDecreaseFontSize ?? (() => {}), !!onDecreaseFontSize);
  useShortcut('ui.resetFontSize', onResetFontSize ?? (() => {}), !!onResetFontSize);

  // Notebook: dock a tile into the active workspace, or open the fullscreen modal.
  useShortcut('notebook.openTile', onOpenNotebookTile ?? (() => {}), enabled && !!onOpenNotebookTile);
  useShortcut('notebook.openFullscreen', onOpenNotebookFullscreen ?? (() => {}), enabled && !!onOpenNotebookFullscreen);

  // Tickets board: open the fullscreen surface (Esc / the close button dismiss it).
  useShortcut('board.open', onOpenBoard ?? (() => {}), enabled && !!onOpenBoard);

  useEffect(() => {
    const preventWindowCloseShortcut = (e: KeyboardEvent) => {
      if (!isAccelKeyPressed(e) || e.shiftKey || e.altKey) {
        return;
      }
      if (e.key.toLowerCase() !== 'w') {
        return;
      }
      // Keep Cmd/Ctrl+W inside the app so shortcut handlers can decide
      // whether to close a pane, close a session, or do nothing.
      e.preventDefault();
    };

    window.addEventListener('keydown', preventWindowCloseShortcut, true);
    return () => {
      window.removeEventListener('keydown', preventWindowCloseShortcut, true);
    };
  }, []);

}
