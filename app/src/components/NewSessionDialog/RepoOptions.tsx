import React, { useState, useEffect } from 'react';
import './RepoOptions.css';

interface RepoInfo {
  repo: string;
  currentBranch: string;
  currentCommitHash: string;
  currentCommitTime: string;
  defaultBranch: string;
  worktrees: Array<{ path: string; branch: string }>;
  branches: Array<{ name: string; commit_hash?: string; commit_time?: string }>;
  fetchedAt?: string;
}

interface RepoOptionsProps {
  repoInfo: RepoInfo;
  currentSessionBranch?: string;
  onSelectMainRepo: () => void;
  onSelectWorktree: (path: string) => void;
  onSelectBranch: (branch: string) => void;
  onCreateWorktree: (branchName: string, startingFrom: string) => Promise<void>;
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

export const RepoOptions: React.FC<RepoOptionsProps> = ({
  repoInfo,
  currentSessionBranch,
  onSelectMainRepo,
  onSelectWorktree,
  onSelectBranch,
  onCreateWorktree,
  onRefresh,
  onBack,
  refreshing = false,
}) => {
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [showNewWorktree, setShowNewWorktree] = useState(false);
  const [newWorktreeName, setNewWorktreeName] = useState('');
  const [startingBranch, setStartingBranch] = useState<'current' | 'default'>('current');

  // Calculate total items for navigation
  const mainRepoCount = 1;
  const worktreeCount = repoInfo.worktrees.length;
  const newWorktreeCount = 1; // "New worktree..." option
  const branchCount = repoInfo.branches.length;
  const totalItems = mainRepoCount + worktreeCount + newWorktreeCount + branchCount;

  // Handle keyboard navigation
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (showNewWorktree) {
        if (e.key === 'Escape') {
          setShowNewWorktree(false);
          setNewWorktreeName('');
          e.preventDefault();
        } else if (e.key === 'Enter' && newWorktreeName.trim()) {
          const startFrom = startingBranch === 'current'
            ? currentSessionBranch || repoInfo.currentBranch
            : `origin/${repoInfo.defaultBranch}`;
          onCreateWorktree(newWorktreeName.trim(), startFrom);
          e.preventDefault();
        } else if (e.key === 'Tab') {
          setStartingBranch(prev => prev === 'current' ? 'default' : 'current');
          e.preventDefault();
        }
        return;
      }

      switch (e.key) {
        case 'ArrowUp':
          setSelectedIndex(prev => Math.max(0, prev - 1));
          e.preventDefault();
          break;
        case 'ArrowDown':
          setSelectedIndex(prev => Math.min(totalItems - 1, prev + 1));
          e.preventDefault();
          break;
        case 'Enter':
          handleSelect(selectedIndex);
          e.preventDefault();
          break;
        case 'r':
        case 'R':
          onRefresh();
          e.preventDefault();
          break;
        case 'Escape':
          onBack();
          e.preventDefault();
          break;
        default:
          // Number shortcuts (1-9)
          if (/^[1-9]$/.test(e.key)) {
            const index = parseInt(e.key) - 1;
            if (index < totalItems) {
              handleSelect(index);
              e.preventDefault();
            }
          }
          break;
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [selectedIndex, totalItems, showNewWorktree, newWorktreeName, startingBranch]);

  const handleSelect = (index: number) => {
    let currentIndex = 0;

    // Main repository
    if (index === currentIndex) {
      onSelectMainRepo();
      return;
    }
    currentIndex += mainRepoCount;

    // Worktrees
    if (index < currentIndex + worktreeCount) {
      const worktreeIndex = index - currentIndex;
      onSelectWorktree(repoInfo.worktrees[worktreeIndex].path);
      return;
    }
    currentIndex += worktreeCount;

    // New worktree option
    if (index === currentIndex) {
      setShowNewWorktree(true);
      return;
    }
    currentIndex += newWorktreeCount;

    // Branches
    if (index < currentIndex + branchCount) {
      const branchIndex = index - currentIndex;
      onSelectBranch(repoInfo.branches[branchIndex].name);
      return;
    }
  };

  const renderItem = (
    itemIndex: number,
    icon: string,
    iconColor: string,
    name: string,
    detail?: string,
    shortcut?: number
  ) => {
    const isSelected = selectedIndex === itemIndex;
    return (
      <div className={`repo-option-item ${isSelected ? 'selected' : ''}`}>
        <span className={`repo-option-icon ${iconColor}`}>{icon}</span>
        <span className="repo-option-name">{name}</span>
        {detail && <span className="repo-option-detail">{detail}</span>}
        {shortcut && <span className="repo-option-shortcut">{shortcut}</span>}
      </div>
    );
  };

  let currentIndex = 0;
  const items: JSX.Element[] = [];

  // Main repository section
  items.push(
    <div key="main-header" className="repo-section-header">
      MAIN REPOSITORY
    </div>
  );
  const mainCommitInfo = `${repoInfo.currentCommitHash.substring(0, 7)} • ${formatTime(repoInfo.currentCommitTime)}`;
  items.push(
    <div key="main-item">
      {renderItem(
        currentIndex++,
        '●',
        'icon-green',
        repoInfo.currentBranch,
        mainCommitInfo,
        1
      )}
    </div>
  );

  // Worktrees section
  if (repoInfo.worktrees.length > 0) {
    items.push(
      <div key="worktrees-header" className="repo-section-header">
        WORKTREES
      </div>
    );
    repoInfo.worktrees.forEach((worktree, idx) => {
      items.push(
        <div key={`worktree-${idx}`}>
          {renderItem(
            currentIndex++,
            '◎',
            'icon-purple',
            worktree.branch,
            worktree.path,
            idx + 2
          )}
        </div>
      );
    });
  }

  // Branches section
  items.push(
    <div key="branches-header" className="repo-section-header">
      BRANCHES
    </div>
  );

  // New worktree option
  if (showNewWorktree) {
    items.push(
      <div key="new-worktree-form" className="new-worktree-form">
        <div className="new-worktree-input-row">
          <span className="repo-option-icon icon-blue">+</span>
          <input
            type="text"
            className="new-worktree-input"
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
              checked={startingBranch === 'current'}
              onChange={() => setStartingBranch('current')}
            />
            Start from {currentSessionBranch || repoInfo.currentBranch}
          </label>
          <label className={startingBranch === 'default' ? 'selected' : ''}>
            <input
              type="radio"
              name="startingBranch"
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
    );
  } else {
    items.push(
      <div key="new-worktree">
        {renderItem(
          currentIndex++,
          '+',
          'icon-blue',
          'New worktree...',
          undefined,
          repoInfo.worktrees.length + 2
        )}
      </div>
    );
  }

  // Available branches
  repoInfo.branches.forEach((branch, idx) => {
    const commitInfo = branch.commit_hash && branch.commit_time
      ? `${branch.commit_hash.substring(0, 7)} • ${formatTime(branch.commit_time)}`
      : undefined;
    items.push(
      <div key={`branch-${idx}`}>
        {renderItem(
          currentIndex++,
          '○',
          'icon-muted',
          branch.name,
          commitInfo,
          repoInfo.worktrees.length + idx + 3
        )}
      </div>
    );
  });

  return (
    <div className="repo-options">
      <div className="repo-options-content">
        {items}
      </div>
      <div className="repo-options-footer">
        <span>↑↓ Navigate</span>
        <span>Enter Select</span>
        <span>R Refresh{refreshing && ' ...'}</span>
        <span>Esc Back</span>
        {repoInfo.fetchedAt && (
          <span className="fetch-time">Updated {formatTime(repoInfo.fetchedAt)}</span>
        )}
      </div>
    </div>
  );
};
