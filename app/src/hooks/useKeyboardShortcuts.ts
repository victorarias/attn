// app/src/hooks/useKeyboardShortcuts.ts
import { useEffect } from 'react';
import { useShortcut } from '../shortcuts';
import { isAccelKeyPressed } from '../shortcuts/platform';

interface KeyboardShortcutsConfig {
  onNewSession: () => void;
  onNewSessionHorizontal?: () => void;
  onNewWorkspace?: () => void;
  onCloseSession: () => void;
  onToggleDrawer: () => void;
  onGoToDashboard: () => void;
  onJumpToWaiting: () => void;
  onSelectWorkspaceByIndex: (index: number) => void;
  onPrevSession: () => void;
  onNextSession: () => void;
  onToggleSidebar?: () => void;
  onRefreshPRs?: () => void;
  onToggleDiffPanel?: () => void;
  onToggleReviewLoopPanel?: () => void;
  onToggleDiffDetailPanel?: () => void;
  onToggleAttentionPanel?: () => void;
  onQuickFind?: () => void;
  onOpenSettings?: () => void;
  onIncreaseFontSize?: () => void;
  onDecreaseFontSize?: () => void;
  onResetFontSize?: () => void;
  enabled: boolean;
}

export function useKeyboardShortcuts({
  onNewSession,
  onNewSessionHorizontal,
  onNewWorkspace,
  onCloseSession,
  onToggleDrawer,
  onGoToDashboard,
  onJumpToWaiting,
  onSelectWorkspaceByIndex,
  onPrevSession,
  onNextSession,
  onToggleSidebar,
  onRefreshPRs,
  onToggleDiffPanel,
  onToggleReviewLoopPanel,
  onToggleDiffDetailPanel,
  onToggleAttentionPanel,
  onQuickFind,
  onOpenSettings,
  onIncreaseFontSize,
  onDecreaseFontSize,
  onResetFontSize,
  enabled,
}: KeyboardShortcutsConfig) {
  // Session management
  useShortcut('session.new', onNewSession, enabled);
  useShortcut('session.newHorizontal', onNewSessionHorizontal ?? (() => {}), enabled && !!onNewSessionHorizontal);
  useShortcut('session.newWorkspace', onNewWorkspace ?? (() => {}), enabled && !!onNewWorkspace);
  useShortcut('session.close', onCloseSession, enabled);
  useShortcut('session.prev', onPrevSession, enabled);
  useShortcut('session.next', onNextSession, enabled);
  useShortcut('session.goToDashboard', onGoToDashboard, enabled);
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
  useShortcut('dock.diff', onToggleDiffPanel ?? (() => {}), enabled && !!onToggleDiffPanel);
  useShortcut('dock.reviewLoop', onToggleReviewLoopPanel ?? (() => {}), enabled && !!onToggleReviewLoopPanel);
  useShortcut('dock.diffDetail', onToggleDiffDetailPanel ?? (() => {}), enabled && !!onToggleDiffDetailPanel);
  useShortcut('dock.attention', onToggleAttentionPanel ?? (() => {}), enabled && !!onToggleAttentionPanel);

  // Quick Find (thumbs)
  useShortcut('terminal.quickFind', onQuickFind ?? (() => {}), enabled && !!onQuickFind);

  // Drawer
  useShortcut('drawer.toggle', onToggleDrawer, enabled);

  // Settings (always enabled)
  useShortcut('ui.openSettings', onOpenSettings ?? (() => {}), !!onOpenSettings);

  // Font scaling (always enabled)
  useShortcut('ui.increaseFontSize', onIncreaseFontSize ?? (() => {}), !!onIncreaseFontSize);
  useShortcut('ui.decreaseFontSize', onDecreaseFontSize ?? (() => {}), !!onDecreaseFontSize);
  useShortcut('ui.resetFontSize', onResetFontSize ?? (() => {}), !!onResetFontSize);

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
