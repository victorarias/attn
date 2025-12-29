import { useState, useEffect, useCallback, useRef } from 'react';
import './ForkDialog.css';

interface ForkDialogProps {
  isOpen: boolean;
  sessionLabel: string;
  sessionId: string;
  onClose: () => void;
  onFork: (name: string, createWorktree: boolean) => void;
}

export function ForkDialog({
  isOpen,
  sessionLabel,
  sessionId: _sessionId,
  onClose,
  onFork,
}: ForkDialogProps) {
  const [name, setName] = useState('');
  const [createWorktree, setCreateWorktree] = useState(true);
  const [isLoading, setIsLoading] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // Generate default name when dialog opens
  useEffect(() => {
    if (isOpen) {
      setName(`${sessionLabel}-fork-1`);
      setCreateWorktree(true);
      setIsLoading(false);
      // Focus and select input after render
      setTimeout(() => {
        inputRef.current?.focus();
        inputRef.current?.select();
      }, 0);
    }
  }, [isOpen, sessionLabel]);

  const handleSubmit = useCallback(() => {
    if (!name.trim() || isLoading) return;
    setIsLoading(true);
    onFork(name.trim(), createWorktree);
  }, [name, createWorktree, isLoading, onFork]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      handleSubmit();
    }
  }, [onClose, handleSubmit]);

  if (!isOpen) return null;

  return (
    <div className="fork-dialog-overlay" onClick={onClose}>
      <div
        className="fork-dialog"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <div className="fork-dialog-header">
          <h3>Fork Session</h3>
        </div>
        <div className="fork-dialog-body">
          <div className="fork-field">
            <label htmlFor="fork-name">Name</label>
            <input
              ref={inputRef}
              id="fork-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={isLoading}
            />
          </div>
          <div className="fork-field fork-checkbox">
            <input
              id="fork-worktree"
              type="checkbox"
              checked={createWorktree}
              onChange={(e) => setCreateWorktree(e.target.checked)}
              disabled={isLoading}
            />
            <label htmlFor="fork-worktree">Create git worktree</label>
          </div>
        </div>
        <div className="fork-dialog-footer">
          <span className="fork-shortcuts">
            <kbd>Enter</kbd> confirm · <kbd>Esc</kbd> cancel
          </span>
          <button
            className="fork-confirm-btn"
            onClick={handleSubmit}
            disabled={!name.trim() || isLoading}
          >
            {isLoading ? 'Creating...' : 'Fork'}
          </button>
        </div>
      </div>
    </div>
  );
}
