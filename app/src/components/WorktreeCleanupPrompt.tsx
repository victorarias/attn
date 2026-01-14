// app/src/components/WorktreeCleanupPrompt.tsx
import { useCallback, useEffect, useRef } from 'react';
import type { KeyboardEvent } from 'react';
import './WorktreeCleanupPrompt.css';

interface WorktreeCleanupPromptProps {
  isVisible: boolean;
  worktreePath: string;
  branchName?: string;
  onKeep: () => void;
  onDelete: () => void;
  onAlwaysKeep: () => void;
}

export function WorktreeCleanupPrompt({
  isVisible,
  worktreePath,
  branchName,
  onKeep,
  onDelete,
  onAlwaysKeep,
}: WorktreeCleanupPromptProps) {
  const keepRef = useRef<HTMLButtonElement>(null);
  const deleteRef = useRef<HTMLButtonElement>(null);
  const alwaysRef = useRef<HTMLButtonElement>(null);

  const handleKeep = useCallback(() => {
    onKeep();
  }, [onKeep]);

  const handleDelete = useCallback(() => {
    onDelete();
  }, [onDelete]);

  const handleAlwaysKeep = useCallback(() => {
    onAlwaysKeep();
  }, [onAlwaysKeep]);

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      const buttons = [keepRef.current, deleteRef.current, alwaysRef.current].filter(
        Boolean
      ) as HTMLButtonElement[];

      if (event.key === 'Escape') {
        event.preventDefault();
        onKeep();
        return;
      }

      if (event.key === 'Tab') {
        if (buttons.length === 0) return;
        const active = document.activeElement as HTMLButtonElement | null;
        const currentIndex = Math.max(0, buttons.indexOf(active || buttons[0]));
        const delta = event.shiftKey ? -1 : 1;
        const nextIndex = (currentIndex + delta + buttons.length) % buttons.length;
        buttons[nextIndex]?.focus();
        event.preventDefault();
        return;
      }

      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;

      if (buttons.length === 0) return;
      const active = document.activeElement as HTMLButtonElement | null;
      const currentIndex = Math.max(0, buttons.indexOf(active || buttons[0]));
      const delta = event.key === 'ArrowRight' ? 1 : -1;
      const nextIndex = (currentIndex + delta + buttons.length) % buttons.length;
      buttons[nextIndex]?.focus();
      event.preventDefault();
    },
    [onKeep]
  );

  useEffect(() => {
    if (!isVisible) return;
    const raf = requestAnimationFrame(() => keepRef.current?.focus());
    return () => cancelAnimationFrame(raf);
  }, [isVisible]);

  if (!isVisible) return null;

  const displayName = branchName || worktreePath.split('/').pop() || 'worktree';

  return (
    <div className="worktree-cleanup-prompt" role="presentation">
      <div
        className="cleanup-content"
        role="dialog"
        aria-modal="true"
        aria-labelledby="worktree-cleanup-title"
        aria-describedby="worktree-cleanup-message"
        onKeyDown={handleKeyDown}
      >
        <div className="cleanup-title" id="worktree-cleanup-title">
          Session closed
        </div>
        <div className="cleanup-message" id="worktree-cleanup-message">
          Keep worktree <span className="cleanup-branch">{displayName}</span> for later?
        </div>
        <div className="cleanup-actions">
          <button ref={keepRef} className="cleanup-btn keep" onClick={handleKeep}>
            Keep
          </button>
          <button ref={deleteRef} className="cleanup-btn delete" onClick={handleDelete}>
            Delete
          </button>
          <button ref={alwaysRef} className="cleanup-btn always" onClick={handleAlwaysKeep}>
            Always keep
          </button>
        </div>
      </div>
    </div>
  );
}
