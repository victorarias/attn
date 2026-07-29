import React, { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { generateWorktreeName, isBranchAlreadyExistsError } from './worktreeNames';
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
  onDeleteWorktree?: (path: string, options?: { force?: boolean }) => Promise<void>;
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

type DeleteFailure = {
  path: string;
  message: string;
  forceable: boolean;
};

type ForceableError = Error & {
  forceable?: boolean;
};

// Creating a worktree is the overwhelmingly common reason to open this step, so
// the create form is always expanded, pre-named, and focused first — a new
// worktree off the latest origin/<default> costs a single Enter.
//
// The exception is arriving with a specific worktree already selected: that only
// happens when the typed path resolved to that worktree, which is an explicit
// "open this one" and must keep Enter meaning "open", not "create".
type FocusZone = 'create' | 'destinations';

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
  const inputRef = useRef<HTMLInputElement>(null);
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
  const selectedDestinationIndex = useMemo(
    () => destinationItems.findIndex((item) => item.path === selectedPath),
    [destinationItems, selectedPath],
  );
  const committedDestinationIndex = selectedDestinationIndex >= 0 ? selectedDestinationIndex : 0;
  const selectedDestination = destinationItems[committedDestinationIndex];
  // Names that would make `git worktree add` fail, so a generated name rarely
  // produces a create the daemon has to reject. This is necessarily incomplete
  // — `RepoInfo` has no visibility into a local branch with no worktree — so
  // `attemptCreateWorktree` below also rerolls and retries on the specific
  // "branch already exists" failure as a backstop.
  const takenBranchNames = useMemo(
    () => [repoInfo.currentBranch, ...repoInfo.worktrees.map((worktree) => worktree.branch)],
    [repoInfo],
  );

  const [focusZone, setFocusZone] = useState<FocusZone>(
    committedDestinationIndex > 0 ? 'destinations' : 'create',
  );
  const [focusIndex, setFocusIndex] = useState(committedDestinationIndex);
  const [newWorktreeName, setNewWorktreeName] = useState(() => generateWorktreeName(takenBranchNames));
  // Default to origin/<defaultBranch> so a new worktree starts fresh from the
  // latest upstream main (fetched first daemon-side), not from whatever local
  // branch the selected destination happens to be sitting on — which can be
  // arbitrarily stale. Users can still opt into "current" via the radio/Tab.
  const [startingBranch, setStartingBranch] = useState<'current' | 'default'>('default');
  const [pendingDeletePath, setPendingDeletePath] = useState<string | null>(null);
  const [deleteFailure, setDeleteFailure] = useState<DeleteFailure | null>(null);
  const [creatingWorktree, setCreatingWorktree] = useState(false);
  const [deletingPath, setDeletingPath] = useState<string | null>(null);
  const isBusy = creatingWorktree || deletingPath !== null;

  // Sub-state Escape handling via the stack so LIFO order is preserved.
  // pendingDeletePath is pushed above LocationPicker's handler.
  const cancelPendingDelete = useCallback(() => {
    setPendingDeletePath(null);
    setDeleteFailure(null);
  }, []);
  useEscapeStack(cancelPendingDelete, pendingDeletePath !== null || deleteFailure !== null);

  useEffect(() => {
    setPendingDeletePath(null);
    setDeleteFailure(null);
    setFocusIndex(committedDestinationIndex);
  }, [committedDestinationIndex]);

  useEffect(() => {
    if (pendingDeletePath) {
      rootRef.current?.focus();
      return;
    }
    if (focusZone === 'create') {
      const input = inputRef.current;
      if (input) {
        input.focus();
        input.select();
      }
      return;
    }
    rootRef.current?.focus();
  }, [creatingWorktree, focusZone, pendingDeletePath]);

  useEffect(() => {
    const activeDeletePath = pendingDeletePath || deleteFailure?.path || null;
    const activeIndex = activeDeletePath
      ? destinationItems.findIndex((item) => item.path === activeDeletePath)
      : committedDestinationIndex;
    if (activeIndex < 0) {
      return;
    }
    destinationListRef.current
      ?.querySelector<HTMLElement>(`[data-option-index="${activeIndex}"]`)
      ?.scrollIntoView({ block: 'nearest' });
  }, [committedDestinationIndex, deleteFailure, destinationItems, pendingDeletePath]);

  const focusedDestination = focusZone === 'destinations' && focusIndex >= 0 && focusIndex < destinationItems.length
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
    setFocusZone('destinations');
    setFocusIndex(clamped);
    if (item) {
      onSelectedPathChange(item.path);
    }
  }, [destinationItems, onSelectedPathChange]);

  const focusCreateForm = useCallback(() => {
    if (isBusy) {
      return;
    }
    setPendingDeletePath(null);
    setDeleteFailure(null);
    setFocusZone('create');
  }, [isBusy]);

  const rerollWorktreeName = useCallback(() => {
    setNewWorktreeName(generateWorktreeName(takenBranchNames));
  }, [takenBranchNames]);

  // `takenBranchNames` can only carry what `RepoInfo` knows about — the current
  // branch and branches with an attached worktree — so a generated name can
  // still collide with an ordinary local branch that has no worktree, or one
  // another client created in the meantime. Rather than surface that raw git
  // failure, reroll and retry automatically: the user asked for "a" fresh
  // worktree, not that specific generated name.
  const MAX_CREATE_ATTEMPTS = 5;
  const attemptCreateWorktree = useCallback(
    (branchName: string, startFrom: string, attempt: number, avoid: Set<string>): Promise<void> =>
      onCreateWorktree(branchName, startFrom).catch((err) => {
        const message = err instanceof Error ? err.message : String(err);
        if (isBranchAlreadyExistsError(message) && attempt + 1 < MAX_CREATE_ATTEMPTS) {
          avoid.add(branchName);
          const nextName = generateWorktreeName([...takenBranchNames, ...avoid]);
          setNewWorktreeName(nextName);
          return attemptCreateWorktree(nextName, startFrom, attempt + 1, avoid);
        }
        throw err;
      }),
    [onCreateWorktree, takenBranchNames],
  );

  const submitCreateWorktree = useCallback(() => {
    const branchName = newWorktreeName.trim();
    if (!branchName || creatingWorktree) {
      return;
    }
    const startFrom = startingBranch === 'current'
      ? selectedDestination?.branch || repoInfo.currentBranch
      : `origin/${repoInfo.defaultBranch}`;
    setCreatingWorktree(true);
    void attemptCreateWorktree(branchName, startFrom, 0, new Set())
      .catch((err) => {
        console.error('Create worktree failed:', err);
        onError?.(err instanceof Error ? err.message : 'Create worktree failed');
      })
      .finally(() => {
        setCreatingWorktree(false);
      });
  }, [
    attemptCreateWorktree,
    creatingWorktree,
    newWorktreeName,
    onError,
    repoInfo.currentBranch,
    repoInfo.defaultBranch,
    selectedDestination,
    startingBranch,
  ]);

  const beginDeleteWorktree = useCallback((path: string) => {
    if (isBusy) {
      return;
    }
    setDeleteFailure(null);
    setPendingDeletePath(path);
  }, [isBusy]);

  const handleActivate = useCallback((index: number) => {
    if (isBusy) {
      return;
    }
    const item = destinationItems[index];
    if (!item) {
      return;
    }
    setFocusZone('destinations');
    setFocusIndex(index);
    onSelectedPathChange(item.path);
    if (item.kind === 'main-repo') {
      onSelectMainRepo();
    } else {
      onSelectWorktree(item.path);
    }
  }, [destinationItems, isBusy, onSelectMainRepo, onSelectWorktree, onSelectedPathChange]);

  const executeDelete = useCallback(async (force = false) => {
    const targetPath = force ? deleteFailure?.path : pendingDeletePath;
    if (!targetPath || !onDeleteWorktree || deletingPath) {
      return;
    }

    const pathToDelete = targetPath;
    const deletedIndex = destinationItems.findIndex((item) => item.path === pathToDelete);
    const survivingItems = destinationItems.filter((item) => item.path !== pathToDelete);
    const nextSelectedPath = survivingItems[Math.min(deletedIndex, survivingItems.length - 1)]?.path || repoInfo.repo;

    try {
      setDeletingPath(pathToDelete);
      setPendingDeletePath(null);
      setDeleteFailure(null);
      await onDeleteWorktree(pathToDelete, force ? { force: true } : undefined);
      onSelectedPathChange(nextSelectedPath);
      setFocusIndex(Math.min(deletedIndex, survivingItems.length - 1));
    } catch (err) {
      console.error('Delete failed:', err);
      const message = err instanceof Error ? err.message : 'Delete failed';
      if (!force && (err as ForceableError)?.forceable) {
        setDeleteFailure({ path: pathToDelete, message, forceable: true });
      } else {
        onError?.(message);
        if (force) {
          setDeleteFailure({ path: pathToDelete, message, forceable: false });
        }
      }
    } finally {
      setDeletingPath(null);
      setPendingDeletePath(null);
    }
  }, [deleteFailure, deletingPath, destinationItems, onDeleteWorktree, onError, onSelectedPathChange, pendingDeletePath, repoInfo.repo]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (isBusy) {
      e.stopPropagation();
      e.preventDefault();
      return;
    }

    if (deleteFailure) {
      e.stopPropagation();
      if ((e.key === 'y' || e.key === 'Y') && deleteFailure.forceable) {
        void executeDelete(true);
        e.preventDefault();
      } else if (e.key === 'n' || e.key === 'N' || e.key === 'Escape') {
        setDeleteFailure(null);
        e.preventDefault();
      } else {
        setDeleteFailure(null);
      }
      return;
    }

    if (pendingDeletePath) {
      e.stopPropagation();
      if (e.key === 'y' || e.key === 'Y') {
        void executeDelete(false);
        e.preventDefault();
      } else if (e.key === 'n' || e.key === 'N' || e.key === 'Escape') {
        setPendingDeletePath(null);
        e.preventDefault();
      } else {
        setPendingDeletePath(null);
      }
      return;
    }

    // The create form owns a text input, so bare letters must keep reaching it.
    // Only the keys listed here are intercepted while this zone has focus.
    if (focusZone === 'create') {
      e.stopPropagation();
      if (e.key === 'Enter') {
        submitCreateWorktree();
        e.preventDefault();
      } else if (e.key === 'Tab') {
        setStartingBranch((prev) => prev === 'current' ? 'default' : 'current');
        e.preventDefault();
      } else if (e.key === 'ArrowDown') {
        commitDestination(committedDestinationIndex);
        e.preventDefault();
      } else if ((e.key === 'r' || e.key === 'R') && (e.ctrlKey || e.metaKey)) {
        rerollWorktreeName();
        e.preventDefault();
      } else if (e.key === 'Escape') {
        onBack();
        e.preventDefault();
      }
      return;
    }

    switch (e.key) {
      case 'ArrowUp':
        e.stopPropagation();
        if (focusIndex <= 0) {
          focusCreateForm();
        } else {
          commitDestination(focusIndex - 1);
        }
        e.preventDefault();
        break;
      case 'ArrowDown':
        e.stopPropagation();
        commitDestination(Math.min(destinationItems.length - 1, focusIndex + 1));
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
        if (canDeleteSelectedItem && focusedDestination) {
          beginDeleteWorktree(focusedDestination.path);
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
          }
          e.preventDefault();
        }
        break;
    }
  }, [
    beginDeleteWorktree,
    canDeleteSelectedItem,
    commitDestination,
    committedDestinationIndex,
    deleteFailure,
    destinationItems,
    executeDelete,
    focusCreateForm,
    focusIndex,
    focusZone,
    focusedDestination,
    handleActivate,
    isBusy,
    onBack,
    onRefresh,
    pendingDeletePath,
    rerollWorktreeName,
    submitCreateWorktree,
  ]);

  const renderDestination = (itemIndex: number, item: DestinationItem) => {
    const isSelected = focusZone === 'destinations' && committedDestinationIndex === itemIndex;
    const isDeleting = pendingDeletePath === item.path;
    const failedDelete = deleteFailure?.path === item.path ? deleteFailure : null;
    const isDeleteRunning = deletingPath === item.path;

    if (isDeleteRunning) {
      return (
        <div className="repo-option-item selected operation-running" role="status" aria-label={`Deleting ${baseName(item.path)}`}>
          <span className="repo-option-icon operation-spinner" aria-hidden="true" />
          <span className="repo-option-name">Deleting {baseName(item.path)}</span>
          <span className="repo-option-detail">Removing worktree and refreshing repo options</span>
        </div>
      );
    }

    if (isDeleting) {
      return (
        <div className="repo-option-item selected delete-confirm">
          <span className="delete-prompt">Delete {baseName(item.path)}? (y/n)</span>
        </div>
      );
    }

    if (failedDelete) {
      return (
        <div className="repo-option-item selected delete-confirm delete-failed">
          <span className="delete-prompt">
            Delete failed: {failedDelete.message}
          </span>
          <span className="delete-prompt-detail">
            {failedDelete.forceable ? 'Force delete local worktree and branch? (y/n)' : 'Press Esc to dismiss'}
          </span>
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
        {refreshing && (
          <div className="repo-operation-banner" role="status" aria-label="Refreshing repo options">
            <span className="repo-operation-dot" aria-hidden="true" />
            <span>Refreshing repo options</span>
          </div>
        )}

        <div className="repo-options-actions">
          <div className="repo-section-header">NEW WORKTREE</div>
          <div
            key="new-worktree-form"
            className={`new-worktree-form ${focusZone === 'create' ? 'focused' : ''}`}
            data-testid="repo-new-worktree-form"
            onClick={focusCreateForm}
          >
            <div className="new-worktree-input-row">
              <span className="repo-option-icon icon-blue">+</span>
              <input
                ref={inputRef}
                type="text"
                className="new-worktree-input"
                data-testid="repo-new-worktree-input"
                placeholder="Branch name..."
                value={newWorktreeName}
                onChange={(e) => setNewWorktreeName(e.target.value)}
                onFocus={() => setFocusZone('create')}
                disabled={creatingWorktree}
                autoCorrect="off"
                autoCapitalize="off"
                spellCheck={false}
              />
              <button
                type="button"
                className="new-worktree-reroll"
                data-testid="repo-new-worktree-reroll"
                title="Pick another name (⌃R)"
                aria-label="Pick another name"
                onClick={rerollWorktreeName}
                disabled={creatingWorktree}
              >
                ⟳
              </button>
            </div>
            <div className="new-worktree-radio-row">
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
            </div>
            <div className={`new-worktree-hint ${creatingWorktree ? 'operation-running' : ''}`}>
              {creatingWorktree ? (
                <>
                  <span className="repo-operation-dot" aria-hidden="true" />
                  Creating worktree...
                </>
              ) : (
                'Enter to create • Tab to toggle • ⌃R for another name • ↓ to open an existing one'
              )}
            </div>
          </div>
        </div>

        <div className="repo-options-destinations">
          <div className="repo-section-header">OPEN EXISTING</div>
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
      </div>
      <div className="repo-options-footer">
        {focusZone === 'create' ? (
          <>
            <span>Enter Create worktree</span>
            <span>↓ Open existing</span>
            <span>Esc Back</span>
          </>
        ) : (
          <>
            <span>↑↓ Navigate</span>
            <span>Enter Open</span>
            {canDeleteSelectedItem && <span>D Delete worktree</span>}
            <span>R Refresh{refreshing && ' ...'}</span>
            <span>Esc Back</span>
          </>
        )}
      </div>
    </div>
  );
};
