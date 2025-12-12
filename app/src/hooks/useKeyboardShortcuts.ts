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
  enabled,
}: KeyboardShortcutsConfig) {
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (!enabled) return;

      const isMeta = e.metaKey || e.ctrlKey;

      // ⌘⇧N - New worktree session (check shift first)
      if (isMeta && e.shiftKey && e.key.toLowerCase() === 'n' && onNewWorktreeSession) {
        e.preventDefault();
        onNewWorktreeSession();
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

      // ⌘D or Escape - Go to dashboard
      if ((isMeta && e.key === 'd') || e.key === 'Escape') {
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

      // ⌘B - Toggle sidebar
      if (isMeta && e.key === 'b' && onToggleSidebar) {
        e.preventDefault();
        onToggleSidebar();
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
    ]
  );

  useEffect(() => {
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [handleKeyDown]);
}
