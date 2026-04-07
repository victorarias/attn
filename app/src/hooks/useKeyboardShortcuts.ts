// app/src/hooks/useKeyboardShortcuts.ts
import { useEffect, useCallback } from 'react';
import { useShortcut } from '../shortcuts';
import { isAccelKeyPressed } from '../shortcuts/platform';

interface KeyboardShortcutsConfig {
  onNewSession: () => void;
  onNewWorktreeSession?: () => void;
  onCloseSession: () => void;
  onToggleDrawer: () => void;
  onGoToDashboard: () => void;
  onJumpToWaiting: () => void;
  onSelectSession: (index: number) => void;
  onPrevSession: () => void;
  onNextSession: () => void;
  onToggleSidebar?: () => void;
  onRefreshPRs?: () => void;
  onOpenBranchPicker?: () => void;
  onForkSession?: () => void;
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
  onNewWorktreeSession,
  onCloseSession,
  onToggleDrawer,
  onGoToDashboard,
  onJumpToWaiting,
  onSelectSession,
  onPrevSession,
  onNextSession,
  onToggleSidebar,
  onRefreshPRs,
  onOpenBranchPicker,
  onForkSession,
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
  useShortcut('session.newWorktree', onNewWorktreeSession ?? (() => {}), enabled && !!onNewWorktreeSession);
  useShortcut('session.close', onCloseSession, enabled);
  useShortcut('session.prev', onPrevSession, enabled);
  useShortcut('session.next', onNextSession, enabled);
  useShortcut('session.goToDashboard', onGoToDashboard, enabled);
  useShortcut('session.jumpToWaiting', onJumpToWaiting, enabled);
  useShortcut('session.toggleSidebar', onToggleSidebar ?? (() => {}), enabled && !!onToggleSidebar);
  useShortcut('session.openBranchPicker', onOpenBranchPicker ?? (() => {}), enabled && !!onOpenBranchPicker);
  useShortcut('session.refreshPRs', onRefreshPRs ?? (() => {}), enabled && !!onRefreshPRs);
  useShortcut('session.fork', onForkSession ?? (() => {}), enabled && !!onForkSession);
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

  // ⌘1-9 for session selection - kept as manual implementation since it needs special handling
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (!enabled) return;
      const isMeta = isAccelKeyPressed(e);

      // ⌘1-9 - Select session by index
      if (isMeta && e.key >= '1' && e.key <= '9') {
        e.preventDefault();
        const index = parseInt(e.key, 10) - 1;
        onSelectSession(index);
      }
    },
    [enabled, onSelectSession]
  );

  useEffect(() => {
    window.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.removeEventListener('keydown', handleKeyDown, true);
    };
  }, [handleKeyDown]);
}
