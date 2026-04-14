import React, { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import './RepoOptions.css';

interface RepoInfo {
  repo: string;
  currentBranch: string;
  currentCommitHash: string;
  currentCommitTime: string;
  defaultBranch: string;
  worktrees: Array<{ path: string; branch: string }>;
}

interface RepoOptionsProps {
  repoInfo: RepoInfo;
  selectedPath?: string;
  onSelectedPathChange: (path: string) => void;
  onSelectMainRepo: () => void;
  onSelectWorktree: (path: string) => void;
  onCreateWorktree: (branchName: string, startingFrom: string) => Promise<void>;
  onDeleteWorktree?: (path: string) => Promise<void>;
  onError?: (message: string) => void;
  onRefresh: () => void;
  onBack: () => void;
  refreshing?: boolean;
}

const formatTime = (isoTime?: string) => {
  if (!isoTime) return '';
  const date = new Date(isoTime);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d ago`;
};

const baseName = (path: string) => {
  const parts = path.split('/').filter(Boolean);
  return parts[parts.length - 1] || path;
};

const tildify = (path: string): string => {
  const homeMatch = /^(\/(?:Users|home)\/[^/]+)/.exec(path);
  if (!homeMatch) return path;
  return '~' + path.slice(homeMatch[1].length);
};

type DestinationItem =
  | {
      kind: 'main-repo';
      path: string;
      branch: string;
      icon: string;
      iconColor: string;
      name: string;
      detail: string;
    }
  | {
      kind: 'worktree';
      path: string;
      branch: string;
      icon: string;
      iconColor: string;
      name: string;
      detail: string;
    };

export const RepoOptions: React.FC<RepoOptionsProps> = ({
  repoInfo,
  selectedPath,
  onSelectedPathChange,
  onSelectMainRepo,
  onSelectWorktree,
  onCreateWorktree,
  onDeleteWorktree,
  onError,
  onRefresh,
  onBack,
  refreshing = false,
}) => {
  const rootRef = useRef<HTMLDivElement>(null);
  const destinationListRef = useRef<HTMLDivElement>(null);
  const destinationItems = useMemo<DestinationItem[]>(
    () => [
      {
        kind: 'main-repo',
        path: repoInfo.repo,
        branch: repoInfo.currentBranch,
        icon: '●',
        iconColor: 'icon-green',
        name: repoInfo.currentBranch,
        detail: `${tildify(repoInfo.repo)} • ${repoInfo.currentCommitHash.substring(0, 7)} • ${formatTime(repoInfo.currentCommitTime)}`,
      },
      ...repoInfo.worktrees.map((worktree) => ({
        kind: 'worktree' as const,
        path: worktree.path,
        branch: worktree.branch,
        icon: '◎',
        iconColor: 'icon-purple',
        name: worktree.branch,
        detail: tildify(worktree.path),
      })),
    ],
    [repoInfo],
  );
  const createWorktreeIndex = destinationItems.length;
  const selectedDestinationIndex = useMemo(
    () => destinationItems.findIndex((item) => item.path === selectedPath),
    [destinationItems, selectedPath],
  );
  const committedDestinationIndex = selectedDestinationIndex >= 0 ? selectedDestinationIndex : 0;
  const selectedDestination = destinationItems[committedDestinationIndex];
  // Focus can temporarily move to the action row, but selectedPath remains the committed destination.
  const [focusIndex, setFocusIndex] = useState(committedDestinationIndex);
  const [showNewWorktree, setShowNewWorktree] = useState(false);
  const [newWorktreeName, setNewWorktreeName] = useState('');
  const [startingBranch, setStartingBranch] = useState<'current' | 'default'>('current');
  const [pendingDeletePath, setPendingDeletePath] = useState<string | null>(null);

  // Sub-state Escape handling via the stack so LIFO order is preserved.
  // pendingDeletePath and showNewWorktree are pushed above LocationPicker's handler.
  const cancelPendingDelete = useCallback(() => setPendingDeletePath(null), []);
  useEscapeStack(cancelPendingDelete, pendingDeletePath !== null);

  const cancelNewWorktree = useCallback(() => {
    setShowNewWorktree(false);
    setNewWorktreeName('');
    setFocusIndex(committedDestinationIndex);
  }, [committedDestinationIndex]);
  useEscapeStack(cancelNewWorktree, showNewWorktree);

  useEffect(() => {
    setPendingDeletePath(null);
    setFocusIndex((prev) => {
      if (showNewWorktree || prev === createWorktreeIndex) {
        return prev;
      }
      return committedDestinationIndex;
    });
  }, [committedDestinationIndex, createWorktreeIndex, showNewWorktree]);

  useEffect(() => {
    if (showNewWorktree) {
      return;
    }
    rootRef.current?.focus();
  }, [showNewWorktree]);

  useEffect(() => {
    if (pendingDeletePath) {
      rootRef.current?.focus();
    }
  }, [pendingDeletePath]);

  useEffect(() => {
    const activeIndex = pendingDeletePath
      ? destinationItems.findIndex((item) => item.path === pendingDeletePath)
      : committedDestinationIndex;
    if (activeIndex < 0) {
      return;
    }
    destinationListRef.current
      ?.querySelector<HTMLElement>(`[data-option-index="${activeIndex}"]`)
      ?.scrollIntoView({ block: 'nearest' });
  }, [committedDestinationIndex, destinationItems, pendingDeletePath]);

  const focusedDestination = focusIndex >= 0 && focusIndex < destinationItems.length
    ? destinationItems[focusIndex]
    : null;
  const canDeleteSelectedItem = Boolean(
    onDeleteWorktree &&
    focusedDestination &&
    focusedDestination.kind === 'worktree',
  );

  const commitDestination = useCallback((index: number) => {
    const clamped = Math.max(0, Math.min(index, destinationItems.length - 1));
    const item = destinationItems[clamped];
    setFocusIndex(clamped);
    if (item) {
      onSelectedPathChange(item.path);
    }
  }, [destinationItems, onSelectedPathChange]);

  const openNewWorktree = useCallback(() => {
    setPendingDeletePath(null);
    setFocusIndex(createWorktreeIndex);
    setShowNewWorktree(true);
  }, [createWorktreeIndex]);

  const beginDeleteWorktree = useCallback((path: string) => {
    setShowNewWorktree(false);
    setNewWorktreeName('');
    setPendingDeletePath(path);
  }, []);

  const handleActivate = useCallback((index: number) => {
    if (index < destinationItems.length) {
      const item = destinationItems[index];
      onSelectedPathChange(item.path);
      if (item.kind === 'main-repo') {
        onSelectMainRepo();
      } else {
        onSelectWorktree(item.path);
      }
      return;
    }
    openNewWorktree();
  }, [destinationItems, onSelectMainRepo, onSelectWorktree, onSelectedPathChange, openNewWorktree]);

  const executeDelete = useCallback(async () => {
    if (!pendingDeletePath || !onDeleteWorktree) {
      return;
    }

    const deletedIndex = destinationItems.findIndex((item) => item.path === pendingDeletePath);
    const survivingItems = destinationItems.filter((item) => item.path !== pendingDeletePath);
    const nextSelectedPath = survivingItems[Math.min(deletedIndex, survivingItems.length - 1)]?.path || repoInfo.repo;

    try {
      await onDeleteWorktree(pendingDeletePath);
      onSelectedPathChange(nextSelectedPath);
      setFocusIndex(Math.min(deletedIndex, survivingItems.length - 1));
    } catch (err) {
      console.error('Delete failed:', err);
      onError?.(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setPendingDeletePath(null);
    }
  }, [destinationItems, onDeleteWorktree, onError, onSelectedPathChange, pendingDeletePath, repoInfo.repo]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (pendingDeletePath) {
      e.stopPropagation();
      if (e.key === 'y' || e.key === 'Y') {
        void executeDelete();
        e.preventDefault();
      } else if (e.key === 'n' || e.key === 'N' || e.key === 'Escape') {
        setPendingDeletePath(null);
        e.preventDefault();
      } else {
        setPendingDeletePath(null);
      }
      return;
    }

    if (showNewWorktree) {
      e.stopPropagation();
      if (e.key === 'Escape') {
        setShowNewWorktree(false);
        setNewWorktreeName('');
        setFocusIndex(committedDestinationIndex);
        e.preventDefault();
      } else if (e.key === 'Enter' && newWorktreeName.trim()) {
        const startFrom = startingBranch === 'current'
          ? selectedDestination?.branch || repoInfo.currentBranch
          : `origin/${repoInfo.defaultBranch}`;
        void onCreateWorktree(newWorktreeName.trim(), startFrom);
        e.preventDefault();
      } else if (e.key === 'Tab') {
        setStartingBranch((prev) => prev === 'current' ? 'default' : 'current');
        e.preventDefault();
      }
      return;
    }

    const totalItems = destinationItems.length + 1;
    switch (e.key) {
      case 'ArrowUp':
        e.stopPropagation();
        if (focusIndex <= 0) {
          commitDestination(0);
        } else if (focusIndex <= destinationItems.length) {
          commitDestination(focusIndex - 1);
        }
        e.preventDefault();
        break;
      case 'ArrowDown':
        e.stopPropagation();
        if (focusIndex < destinationItems.length - 1) {
          commitDestination(focusIndex + 1);
        } else {
          setFocusIndex(Math.min(totalItems - 1, focusIndex + 1));
        }
        e.preventDefault();
        break;
      case 'Enter':
        e.stopPropagation();
        handleActivate(focusIndex);
        e.preventDefault();
        break;
      case 'd':
      case 'D':
        e.stopPropagation();
        if (canDeleteSelectedItem) {
          if (focusedDestination) {
            beginDeleteWorktree(focusedDestination.path);
          }
        }
        e.preventDefault();
        break;
      case 'r':
      case 'R':
        e.stopPropagation();
        onRefresh();
        e.preventDefault();
        break;
      case 'Escape':
        e.stopPropagation();
        onBack();
        e.preventDefault();
        break;
      default:
        if (/^[1-9]$/.test(e.key)) {
          e.stopPropagation();
          const index = parseInt(e.key, 10) - 1;
          if (index < destinationItems.length) {
            commitDestination(index);
          } else if (index === createWorktreeIndex) {
            openNewWorktree();
          }
          e.preventDefault();
        }
        break;
    }
  }, [
    canDeleteSelectedItem,
    beginDeleteWorktree,
    commitDestination,
    focusIndex,
    destinationItems,
    executeDelete,
    focusedDestination,
    handleActivate,
    newWorktreeName,
    onBack,
    onCreateWorktree,
    onRefresh,
    openNewWorktree,
    pendingDeletePath,
    repoInfo.currentBranch,
    repoInfo.defaultBranch,
    committedDestinationIndex,
    showNewWorktree,
    startingBranch,
  ]);

  const renderDestination = (itemIndex: number, item: DestinationItem) => {
    const isSelected = committedDestinationIndex === itemIndex;
    const isDeleting = pendingDeletePath === item.path;

    if (isDeleting) {
      return (
        <div className="repo-option-item selected delete-confirm">
          <span className="delete-prompt">Delete {baseName(item.path)}? (y/n)</span>
        </div>
      );
    }

    return (
      <div
        className={`repo-option-item ${isSelected ? 'selected' : ''}`}
        data-testid={`repo-option-${itemIndex}`}
        data-option-index={itemIndex}
        data-option-kind={item.kind}
        onClick={() => handleActivate(itemIndex)}
      >
        <span className={`repo-option-icon ${item.iconColor}`}>{item.icon}</span>
        <span className="repo-option-name">{item.name}</span>
        <span className="repo-option-detail">{item.detail}</span>
        <span className="repo-option-shortcut">{itemIndex + 1}</span>
      </div>
    );
  };

  return (
    <div
      ref={rootRef}
      className="repo-options"
      data-testid="repo-options"
      tabIndex={-1}
      onKeyDown={handleKeyDown}
    >
      <div className="repo-options-content">
        <div className="repo-options-destinations">
          <div className="repo-section-header">DESTINATIONS</div>
          <div
            ref={destinationListRef}
            className="repo-destination-list"
            data-testid="repo-destination-list"
          >
            {destinationItems.map((item, index) => (
              <div key={item.path}>
                {renderDestination(index, item)}
              </div>
            ))}
          </div>
        </div>

        <div className="repo-options-actions">
          <div className="repo-section-header">ACTIONS</div>
          {showNewWorktree ? (
            <div key="new-worktree-form" className="new-worktree-form" data-testid="repo-new-worktree-form">
              <div className="new-worktree-input-row">
                <span className="repo-option-icon icon-blue">+</span>
                <input
                  type="text"
                  className="new-worktree-input"
                  data-testid="repo-new-worktree-input"
                  placeholder="Branch name..."
                  value={newWorktreeName}
                  onChange={(e) => setNewWorktreeName(e.target.value)}
                  autoFocus
                />
              </div>
              <div className="new-worktree-radio-row">
                <label className={startingBranch === 'current' ? 'selected' : ''}>
                  <input
                    type="radio"
                    name="startingBranch"
                    value="current"
                    data-testid="repo-new-worktree-start-current"
                    checked={startingBranch === 'current'}
                    onChange={() => setStartingBranch('current')}
                  />
                  Start from {selectedDestination?.branch || repoInfo.currentBranch}
                </label>
                <label className={startingBranch === 'default' ? 'selected' : ''}>
                  <input
                    type="radio"
                    name="startingBranch"
                    value="default"
                    data-testid="repo-new-worktree-start-default"
                    checked={startingBranch === 'default'}
                    onChange={() => setStartingBranch('default')}
                  />
                  Start from origin/{repoInfo.defaultBranch}
                </label>
              </div>
              <div className="new-worktree-hint">
                Press Enter to create • Tab to toggle • Esc to cancel
              </div>
            </div>
          ) : (
            <div
              className={`repo-option-item ${focusIndex === createWorktreeIndex ? 'selected' : ''}`}
              data-testid={`repo-option-${createWorktreeIndex}`}
              data-option-index={createWorktreeIndex}
              data-option-kind="new-worktree"
              onClick={openNewWorktree}
            >
              <span className="repo-option-icon icon-blue">+</span>
              <span className="repo-option-name">Create worktree...</span>
              <span className="repo-option-detail">Create a new worktree and open it immediately</span>
              <span className="repo-option-shortcut">{createWorktreeIndex + 1}</span>
            </div>
          )}
        </div>
      </div>
      <div className="repo-options-footer">
        <span>↑↓ Navigate</span>
        <span>Enter Open</span>
        {canDeleteSelectedItem && <span>D Delete worktree</span>}
        <span>R Refresh{refreshing && ' ...'}</span>
        <span>Esc Back</span>
      </div>
    </div>
  );
};
