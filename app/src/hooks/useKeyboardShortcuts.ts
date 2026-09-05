import { useEffect } from 'react';
import { useShortcut } from '../shortcuts/useShortcut';
import { isAccelKeyPressed, isMacLikePlatform } from '../shortcuts/platform';

interface KeyboardShortcutsConfig {
  onNewSession: () => void;
  onNewSessionHorizontal?: () => void;
  onNewWorkspace?: () => void;
  onCloseSession: () => void;
  onToggleActionMenu: () => void;
  onGoToDashboard: () => void;
  onToggleGridMode?: () => void;
  onJumpToWaiting: () => void;
  /** Undefined while the queue arrangement is off; the keystroke is then unbound. */
  onSettleTurn?: () => void;
  onSnoozeTurn?: () => void;
  onCancelCountdown?: () => void;
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
  onOpenFile?: () => void;
  onOpenNotebookTile?: () => void;
  onOpenNotebookFullscreen?: () => void;
  onOpenGarden?: () => void;
  /** Keep the Garden shortcut live fullscreen so the same key can switch frames. */
  gardenShortcutEnabled?: boolean;
  onOpenSessions?: () => void;
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
  onSettleTurn,
  onSnoozeTurn,
  onCancelCountdown,
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
  onOpenFile,
  onOpenNotebookTile,
  onOpenNotebookFullscreen,
  onOpenGarden,
  gardenShortcutEnabled,
  onOpenSessions,
  onQuit,
  enabled,
}: KeyboardShortcutsConfig) {
  useShortcut('app.quit', onQuit ?? (() => {}), !!onQuit);

  useShortcut('session.new', onNewSession, enabled);
  useShortcut('session.newHorizontal', onNewSessionHorizontal ?? (() => {}), enabled && !!onNewSessionHorizontal);
  useShortcut('session.newWorkspace', onNewWorkspace ?? (() => {}), enabled && !!onNewWorkspace);
  useShortcut('session.close', onCloseSession, enabled);
  useShortcut('session.prev', onPrevSession, enabled);
  useShortcut('session.next', onNextSession, enabled);
  useShortcut('session.goToDashboard', onGoToDashboard, enabled);
  useShortcut('view.toggleGrid', onToggleGridMode ?? (() => {}), enabled && !!onToggleGridMode);
  useShortcut('session.jumpToWaiting', onJumpToWaiting, enabled);
  useShortcut('session.settle', onSettleTurn ?? (() => {}), enabled && !!onSettleTurn);
  useShortcut('session.snooze', onSnoozeTurn ?? (() => {}), enabled && !!onSnoozeTurn);
  // Delivered by a native menu item, not the page's keydown listener: AppKit eats
  // ⌘. before the WebView sees it.
  useShortcut('session.cancelCountdown', onCancelCountdown ?? (() => {}), enabled && !!onCancelCountdown);
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

  useShortcut('ui.actionMenu', onToggleActionMenu, true);

  useShortcut('ui.openSettings', onOpenSettings ?? (() => {}), !!onOpenSettings);

  useShortcut('ui.showShortcuts', onShowShortcuts ?? (() => {}), !!onShowShortcuts);

  useShortcut('ui.increaseFontSize', onIncreaseFontSize ?? (() => {}), !!onIncreaseFontSize);
  useShortcut('ui.decreaseFontSize', onDecreaseFontSize ?? (() => {}), !!onDecreaseFontSize);
  useShortcut('ui.resetFontSize', onResetFontSize ?? (() => {}), !!onResetFontSize);

  useShortcut('file.open', onOpenFile ?? (() => {}), enabled && !!onOpenFile);

  useShortcut('notebook.openTile', onOpenNotebookTile ?? (() => {}), enabled && !!onOpenNotebookTile);
  useShortcut('notebook.openFullscreen', onOpenNotebookFullscreen ?? (() => {}), enabled && !!onOpenNotebookFullscreen);

  useShortcut('board.open', onOpenGarden ?? (() => {}), (gardenShortcutEnabled ?? enabled) && !!onOpenGarden);

  useShortcut('sessions.open', onOpenSessions ?? (() => {}), !!onOpenSessions);

  useEffect(() => {
    const preventWindowCloseShortcut = (e: KeyboardEvent) => {
      if (!isMacLikePlatform() || !isAccelKeyPressed(e) || e.shiftKey || e.altKey) {
        return;
      }
      if (e.key.toLowerCase() !== 'w') {
        return;
      }
      // Keep Cmd+W inside the app so shortcut handlers can decide
      // whether to close a pane, close a session, or do nothing.
      e.preventDefault();
    };

    window.addEventListener('keydown', preventWindowCloseShortcut, true);
    return () => {
      window.removeEventListener('keydown', preventWindowCloseShortcut, true);
    };
  }, []);

}
