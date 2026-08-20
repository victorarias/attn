// app/src/hooks/useKeyboardShortcuts.ts
import { useEffect } from 'react';
import { useShortcut } from '../shortcuts/useShortcut';
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
  /** The garden's own gate. Its window frame is the garden at a different
   *  size, not a modal over it, so the key that promotes it must also bring it
   *  back — and `enabled` goes false in there, like every other app shortcut. */
  gardenShortcutEnabled?: boolean;
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
  // Registered only while the queue arrangement is on: with the band hidden the
  // keystroke has nothing visible to act on, and an invisible verb that silently
  // stamps state is worse than no verb.
  useShortcut('session.settle', onSettleTurn ?? (() => {}), enabled && !!onSettleTurn);
  // Same gate: snooze defers a turn, and there is no queue to defer out of while
  // the arrangement is off.
  useShortcut('session.snooze', onSnoozeTurn ?? (() => {}), enabled && !!onSnoozeTurn);
  // Delivered by a native menu item, not by the page's keydown listener — AppKit
  // eats ⌘. before the WebView sees it. The registration is identical either way:
  // `dispatch_native_shortcut` calls the same triggerShortcut(id), so an id with no
  // registered handler is still a no-op and the `enabled` gates still apply.
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

  // Markdown file opener (⌘P). Enabled like other surface shortcuts; a focused
  // notebook tile gets the keystroke handed back to it by the handler itself.
  useShortcut('file.open', onOpenFile ?? (() => {}), enabled && !!onOpenFile);

  // Notebook: dock a tile into the active workspace, or open the fullscreen modal.
  useShortcut('notebook.openTile', onOpenNotebookTile ?? (() => {}), enabled && !!onOpenNotebookTile);
  useShortcut('notebook.openFullscreen', onOpenNotebookFullscreen ?? (() => {}), enabled && !!onOpenNotebookFullscreen);

  // The garden: promote it into the window, or hand it back to the dock.
  useShortcut('board.open', onOpenGarden ?? (() => {}), (gardenShortcutEnabled ?? enabled) && !!onOpenGarden);

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
