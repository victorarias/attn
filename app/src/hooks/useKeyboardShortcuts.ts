// app/src/hooks/useKeyboardShortcuts.ts
import { useEffect, useCallback } from 'react';

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
  onIncreaseFontSize,
  onDecreaseFontSize,
  onResetFontSize,
  enabled,
}: KeyboardShortcutsConfig) {
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      // Font size shortcuts work even when disabled (e.g., in modals)
      const isMeta = e.metaKey || e.ctrlKey;

      // ⌘+ or ⌘= - Increase font size
      if (isMeta && (e.key === '+' || e.key === '=') && onIncreaseFontSize) {
        e.preventDefault();
        onIncreaseFontSize();
        return;
      }

      // ⌘- - Decrease font size
      if (isMeta && e.key === '-' && onDecreaseFontSize) {
        e.preventDefault();
        onDecreaseFontSize();
        return;
      }

      // ⌘0 - Reset font size
      if (isMeta && e.key === '0' && onResetFontSize) {
        e.preventDefault();
        onResetFontSize();
        return;
      }

      if (!enabled) return;

      // ⌘⇧N - New worktree session (check shift first)
      if (isMeta && e.shiftKey && e.key.toLowerCase() === 'n' && onNewWorktreeSession) {
        e.preventDefault();
        onNewWorktreeSession();
        return;
      }

      // ⌘⇧B - Toggle sidebar
      if (isMeta && e.shiftKey && e.key.toLowerCase() === 'b' && onToggleSidebar) {
        e.preventDefault();
        onToggleSidebar();
        return;
      }

      // ⌘N - New session
      if (isMeta && !e.shiftKey && e.key === 'n') {
        e.preventDefault();
        onNewSession();
        return;
      }

      // ⌘W - Close session
      if (isMeta && e.key === 'w') {
        e.preventDefault();
        onCloseSession();
        return;
      }

      // ⌘K or ⌘. - Toggle drawer
      if (isMeta && (e.key === 'k' || e.key === '.')) {
        e.preventDefault();
        onToggleDrawer();
        return;
      }

      // ⌘D - Go to dashboard
      if (isMeta && e.key === 'd') {
        e.preventDefault();
        onGoToDashboard();
        return;
      }

      // ⌘J - Jump to next waiting session
      if (isMeta && e.key === 'j') {
        e.preventDefault();
        onJumpToWaiting();
        return;
      }

      // ⌘1-9 - Select session by index
      if (isMeta && e.key >= '1' && e.key <= '9') {
        e.preventDefault();
        const index = parseInt(e.key, 10) - 1;
        onSelectSession(index);
        return;
      }

      // ⌘↑ - Previous session
      if (isMeta && e.key === 'ArrowUp') {
        e.preventDefault();
        onPrevSession();
        return;
      }

      // ⌘↓ - Next session
      if (isMeta && e.key === 'ArrowDown') {
        e.preventDefault();
        onNextSession();
        return;
      }

      // ⌘B - Open branch picker
      if (isMeta && !e.shiftKey && e.key === 'b' && onOpenBranchPicker) {
        e.preventDefault();
        onOpenBranchPicker();
        return;
      }

      // ⌘R - Refresh PRs
      if (isMeta && e.key === 'r' && onRefreshPRs) {
        e.preventDefault();
        onRefreshPRs();
        return;
      }
    },
    [
      enabled,
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
      onIncreaseFontSize,
      onDecreaseFontSize,
      onResetFontSize,
    ]
  );

  useEffect(() => {
    // Use capture phase to get events before xterm
    window.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.removeEventListener('keydown', handleKeyDown, true);
    };
  }, [handleKeyDown]);
}
