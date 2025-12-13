// app/src/components/BranchPicker.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import type { Branch, DaemonSession } from '../hooks/useDaemonSocket';
import './BranchPicker.css';

interface BranchPickerProps {
  isOpen: boolean;
  onClose: () => void;
  session: DaemonSession | null;
  // Daemon operations
  onListBranches: (repo: string) => Promise<{ branches: Branch[] }>;
  onListRemoteBranches: (repo: string) => Promise<{ branches: Branch[] }>;
  onFetchRemotes: (repo: string) => Promise<{ success: boolean }>;
  onSwitchBranch: (repo: string, branch: string) => Promise<{ success: boolean }>;
  onCheckDirty: (repo: string) => Promise<{ dirty?: boolean }>;
  onStash: (repo: string, message: string) => Promise<{ success: boolean }>;
  onCommitWIP: (repo: string) => Promise<{ success: boolean }>;
  onCheckAttnStash: (repo: string, branch: string) => Promise<{ found?: boolean }>;
  onStashPop: (repo: string) => Promise<{ success: boolean }>;
  onGetDefaultBranch: (repo: string) => Promise<{ branch?: string }>;
}

type PickerState = 'loading' | 'picking' | 'dirty' | 'stash-restore' | 'error';

export function BranchPicker({
  isOpen,
  onClose,
  session,
  onListBranches,
  onListRemoteBranches,
  onFetchRemotes,
  onSwitchBranch,
  onCheckDirty,
  onStash,
  onCommitWIP,
  onCheckAttnStash,
  onStashPop,
  onGetDefaultBranch,
}: BranchPickerProps) {
  const [state, setState] = useState<PickerState>('loading');
  const [filterText, setFilterText] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [localBranches, setLocalBranches] = useState<Branch[]>([]);
  const [remoteBranches, setRemoteBranches] = useState<Branch[]>([]);
  const [previousBranch, setPreviousBranch] = useState<string | null>(null);
  const [targetBranch, setTargetBranch] = useState<string | null>(null);
  const [dirtyAction, setDirtyAction] = useState<'stash' | 'wip'>('stash');
  const [defaultBranch, setDefaultBranch] = useState<string>('main');
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);

  const repo = session?.main_repo || session?.directory || '';
  const currentBranch = session?.branch || '';

  // Load branches when opened
  useEffect(() => {
    if (!isOpen || !repo) return;

    setState('loading');
    setFilterText('');
    setSelectedIndex(0);
    setError(null);

    const loadData = async () => {
      try {
        // Fetch in parallel
        const [localResult, defaultResult] = await Promise.all([
          onListBranches(repo),
          onGetDefaultBranch(repo),
        ]);

        setLocalBranches(localResult.branches);
        setDefaultBranch(defaultResult.branch || 'main');

        // Fetch remotes in background
        onFetchRemotes(repo)
          .then(() => onListRemoteBranches(repo))
          .then((result) => setRemoteBranches(result.branches))
          .catch((err) => {
            console.warn('Failed to fetch remote branches:', err);
          });

        setState('picking');
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to load branches');
        setState('error');
      }
    };

    loadData();
    setTimeout(() => inputRef.current?.focus(), 50);
  }, [isOpen, repo, onListBranches, onFetchRemotes, onListRemoteBranches, onGetDefaultBranch]);

  // Build filtered branch list
  const filteredLocal = filterText
    ? localBranches.filter((b) => b.name.toLowerCase().includes(filterText.toLowerCase()))
    : localBranches;

  const filteredRemote = filterText
    ? remoteBranches.filter((b) => b.name.toLowerCase().includes(filterText.toLowerCase()))
    : remoteBranches;

  // Calculate all items with indices
  const allItems: { type: 'previous' | 'local' | 'remote'; name: string }[] = [];
  if (previousBranch && previousBranch !== currentBranch) {
    allItems.push({ type: 'previous', name: previousBranch });
  }
  filteredLocal.forEach((b) => allItems.push({ type: 'local', name: b.name }));
  filteredRemote.forEach((b) => allItems.push({ type: 'remote', name: b.name }));

  // Scroll selected into view
  useEffect(() => {
    if (!resultsRef.current) return;
    const selectedEl = resultsRef.current.querySelector(`[data-index="${selectedIndex}"]`);
    selectedEl?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [selectedIndex]);

  // Perform the actual switch
  const performSwitch = useCallback(async (branch: string) => {
    if (!repo) return;

    try {
      setState('loading');
      await onSwitchBranch(repo, branch);

      // Store previous branch
      if (currentBranch) {
        setPreviousBranch(currentBranch);
      }

      // Check for attn stash on the new branch
      const stashResult = await onCheckAttnStash(repo, branch);
      if (stashResult.found) {
        setTargetBranch(branch);
        setState('stash-restore');
        return;
      }

      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Switch failed');
      setState('error');
    }
  }, [repo, currentBranch, onSwitchBranch, onCheckAttnStash, onClose]);

  // Handle branch selection
  const selectBranch = useCallback(async (branch: string) => {
    if (!repo || branch === currentBranch) return;

    try {
      // Check dirty state
      const dirtyResult = await onCheckDirty(repo);
      if (dirtyResult.dirty) {
        setTargetBranch(branch);
        // Pre-select based on current branch
        const isOnDefault = currentBranch === defaultBranch;
        setDirtyAction(isOnDefault ? 'stash' : 'wip');
        setState('dirty');
        return;
      }

      // Not dirty, switch directly
      await performSwitch(branch);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Switch failed');
      setState('error');
    }
  }, [repo, currentBranch, defaultBranch, onCheckDirty, performSwitch]);

  // Handle dirty state confirmation
  const handleDirtyConfirm = useCallback(async () => {
    if (!repo || !targetBranch || !currentBranch) return;

    try {
      setState('loading');

      if (dirtyAction === 'stash') {
        // Use currentBranch (source) so we can find it when returning
        await onStash(repo, `attn: auto-stash from ${currentBranch}`);
      } else {
        await onCommitWIP(repo);
      }

      await performSwitch(targetBranch);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to handle dirty state');
      setState('error');
    }
  }, [repo, targetBranch, currentBranch, dirtyAction, onStash, onCommitWIP, performSwitch]);

  // Handle stash restore
  const handleStashPop = useCallback(async () => {
    if (!repo) return;

    try {
      setState('loading');
      await onStashPop(repo);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to pop stash');
      setState('error');
    }
  }, [repo, onStashPop, onClose]);

  // Keyboard handling
  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    e.stopPropagation();

    if (e.key === 'Escape') {
      e.preventDefault();
      if (state === 'dirty' || state === 'stash-restore') {
        setState('picking');
      } else {
        onClose();
      }
      return;
    }

    if (state === 'picking') {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) => Math.min(prev + 1, allItems.length - 1));
        return;
      }

      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
        return;
      }

      if (e.key === 'Enter') {
        e.preventDefault();
        const item = allItems[selectedIndex];
        if (item) {
          selectBranch(item.name);
        }
        return;
      }
    }

    if (state === 'dirty') {
      if (e.key === 'Enter') {
        e.preventDefault();
        handleDirtyConfirm();
        return;
      }

      if (e.key === '1' || e.key === 's') {
        e.preventDefault();
        setDirtyAction('stash');
        return;
      }

      if (e.key === '2' || e.key === 'w') {
        e.preventDefault();
        setDirtyAction('wip');
        return;
      }
    }

    if (state === 'stash-restore') {
      if (e.key === 'Enter' || e.key === 'y') {
        e.preventDefault();
        handleStashPop();
        return;
      }

      if (e.key === 'n') {
        e.preventDefault();
        onClose();
        return;
      }
    }
  }, [state, allItems, selectedIndex, selectBranch, handleDirtyConfirm, handleStashPop, onClose]);

  if (!isOpen) return null;

  return (
    <div className="branch-picker-overlay" onClick={onClose}>
      <div className="branch-picker" onClick={(e) => e.stopPropagation()}>
        {state === 'loading' && (
          <div className="branch-picker-loading">Loading branches...</div>
        )}

        {state === 'error' && (
          <div className="branch-picker-loading">
            <div style={{ color: 'var(--error)' }}>{error}</div>
            <div className="branch-picker-footer">
              <span>Press <kbd>Esc</kbd> to close</span>
            </div>
          </div>
        )}

        {state === 'picking' && (
          <>
            <div className="branch-picker-header">
              <div className="branch-picker-title">Switch Branch</div>
              <input
                ref={inputRef}
                type="text"
                className="branch-picker-input"
                placeholder="Filter branches..."
                value={filterText}
                onChange={(e) => {
                  setFilterText(e.target.value);
                  setSelectedIndex(0);
                }}
                onKeyDown={handleKeyDown}
              />
            </div>

            <div className="branch-picker-results" ref={resultsRef}>
              {allItems.length === 0 ? (
                <div className="branch-picker-empty">No branches found</div>
              ) : (
                <>
                  {/* Previous branch */}
                  {previousBranch && previousBranch !== currentBranch && (
                    <div className="branch-picker-section">
                      <div
                        className={`branch-picker-item previous ${selectedIndex === 0 ? 'selected' : ''}`}
                        data-index={0}
                        onClick={() => selectBranch(previousBranch)}
                        onMouseEnter={() => setSelectedIndex(0)}
                      >
                        <div className="branch-picker-icon previous">↩</div>
                        <div className="branch-picker-name">{previousBranch}</div>
                        <div className="branch-picker-hint">Previous</div>
                      </div>
                    </div>
                  )}

                  {/* Local branches */}
                  {filteredLocal.length > 0 && (
                    <div className="branch-picker-section">
                      <div className="branch-picker-section-header">Local</div>
                      {filteredLocal.map((branch, i) => {
                        const index = previousBranch && previousBranch !== currentBranch ? i + 1 : i;
                        return (
                          <div
                            key={branch.name}
                            className={`branch-picker-item ${selectedIndex === index ? 'selected' : ''}`}
                            data-index={index}
                            onClick={() => selectBranch(branch.name)}
                            onMouseEnter={() => setSelectedIndex(index)}
                          >
                            <div className="branch-picker-icon local">○</div>
                            <div className="branch-picker-name">{branch.name}</div>
                          </div>
                        );
                      })}
                    </div>
                  )}

                  {/* Remote branches */}
                  {filteredRemote.length > 0 && (
                    <div className="branch-picker-section">
                      <div className="branch-picker-section-header">Remote</div>
                      {filteredRemote.map((branch, i) => {
                        const baseIndex = (previousBranch && previousBranch !== currentBranch ? 1 : 0) + filteredLocal.length;
                        const index = baseIndex + i;
                        return (
                          <div
                            key={branch.name}
                            className={`branch-picker-item ${selectedIndex === index ? 'selected' : ''}`}
                            data-index={index}
                            onClick={() => selectBranch(branch.name)}
                            onMouseEnter={() => setSelectedIndex(index)}
                          >
                            <div className="branch-picker-icon remote">◇</div>
                            <div className="branch-picker-name">{branch.name}</div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </>
              )}
            </div>

            <div className="branch-picker-footer">
              <span><kbd>↑↓</kbd> navigate</span>
              <span><kbd>Enter</kbd> select</span>
              <span><kbd>Esc</kbd> cancel</span>
            </div>
          </>
        )}

        {state === 'dirty' && (
          <div className="dirty-dialog" onKeyDown={handleKeyDown} tabIndex={0} ref={(el) => el?.focus()}>
            <div className="dirty-dialog-title">Uncommitted changes detected</div>

            <div className="dirty-dialog-options">
              <label
                className={`dirty-dialog-option ${dirtyAction === 'stash' ? 'selected' : ''}`}
                onClick={() => setDirtyAction('stash')}
              >
                <input
                  type="radio"
                  name="dirtyAction"
                  checked={dirtyAction === 'stash'}
                  onChange={() => setDirtyAction('stash')}
                />
                <span>Stash changes</span>
              </label>

              <label
                className={`dirty-dialog-option ${dirtyAction === 'wip' ? 'selected' : ''}`}
                onClick={() => setDirtyAction('wip')}
              >
                <input
                  type="radio"
                  name="dirtyAction"
                  checked={dirtyAction === 'wip'}
                  onChange={() => setDirtyAction('wip')}
                />
                <span>Commit as WIP</span>
              </label>
            </div>

            <div className="dirty-dialog-actions">
              <button className="dirty-dialog-btn cancel" onClick={() => setState('picking')}>
                Cancel
              </button>
              <button className="dirty-dialog-btn confirm" onClick={handleDirtyConfirm}>
                Switch to {targetBranch}
              </button>
            </div>
          </div>
        )}

        {state === 'stash-restore' && (
          <div className="stash-dialog" onKeyDown={handleKeyDown} tabIndex={0} ref={(el) => el?.focus()}>
            <div className="stash-dialog-title">Found stashed changes</div>
            <div className="stash-dialog-description">
              You stashed changes when leaving this branch. Restore them?
            </div>

            <div className="stash-dialog-actions">
              <button className="dirty-dialog-btn cancel" onClick={onClose}>
                Keep stashed
              </button>
              <button className="dirty-dialog-btn confirm" onClick={handleStashPop}>
                Pop stash
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
