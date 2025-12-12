// app/src/components/WorktreeCleanupPrompt.tsx
import { useCallback } from 'react';
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
  const handleKeep = useCallback(() => {
    onKeep();
  }, [onKeep]);

  const handleDelete = useCallback(() => {
    onDelete();
  }, [onDelete]);

  const handleAlwaysKeep = useCallback(() => {
    onAlwaysKeep();
  }, [onAlwaysKeep]);

  if (!isVisible) return null;

  const displayName = branchName || worktreePath.split('/').pop() || 'worktree';

  return (
    <div className="worktree-cleanup-prompt">
      <div className="cleanup-content">
        <div className="cleanup-title">Session closed</div>
        <div className="cleanup-message">
          Keep worktree <span className="cleanup-branch">{displayName}</span> for later?
        </div>
        <div className="cleanup-actions">
          <button className="cleanup-btn keep" onClick={handleKeep}>
            Keep
          </button>
          <button className="cleanup-btn delete" onClick={handleDelete}>
            Delete
          </button>
          <button className="cleanup-btn always" onClick={handleAlwaysKeep}>
            Always keep
          </button>
        </div>
      </div>
    </div>
  );
}
