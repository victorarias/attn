// app/src/components/LocationPicker.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import { homeDir } from '@tauri-apps/api/path';
import { readDir } from '@tauri-apps/plugin-fs';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import type { DaemonWorktree, RecentLocation, Branch } from '../hooks/useDaemonSocket';
import './LocationPicker.css';

interface LocationPickerProps {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (path: string) => void;
  worktrees?: DaemonWorktree[];
  onListWorktrees?: (mainRepo: string) => void;
  onCreateWorktree?: (mainRepo: string, branch: string) => Promise<{ success: boolean; path?: string }>;
  onDeleteWorktree?: (path: string) => Promise<{ success: boolean }>;
  worktreeFlowMode?: boolean;
  projectsDirectory?: string;
  onGetRecentLocations?: () => Promise<{ locations: RecentLocation[] }>;
  // Branch operations
  onListBranches?: (mainRepo: string) => Promise<{ branches: Branch[] }>;
  onDeleteBranch?: (mainRepo: string, branch: string, force?: boolean) => Promise<{ success: boolean }>;
  onSwitchBranch?: (mainRepo: string, branch: string) => Promise<{ success: boolean }>;
  onCreateWorktreeFromBranch?: (mainRepo: string, branch: string) => Promise<{ success: boolean; path?: string }>;
}

export function LocationPicker({
  isOpen,
  onClose,
  onSelect,
  worktrees,
  onListWorktrees,
  onCreateWorktree,
  onDeleteWorktree,
  worktreeFlowMode,
  projectsDirectory,
  onGetRecentLocations,
  onListBranches,
  onDeleteBranch,
  onSwitchBranch,
  onCreateWorktreeFromBranch,
}: LocationPickerProps) {
  const [inputValue, setInputValue] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [homePath, setHomePath] = useState('/Users');
  const [worktreeMode, setWorktreeMode] = useState(false);
  const [selectedRepo, setSelectedRepo] = useState<string | null>(null);
  const [newBranchName, setNewBranchName] = useState('');
  const [showNewBranch, setShowNewBranch] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [deleteConfirmIndex, setDeleteConfirmIndex] = useState<number | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [recentLocations, setRecentLocations] = useState<RecentLocation[]>([]);
  // Branch state
  const [branches, setBranches] = useState<Branch[]>([]);
  const [branchActionMode, setBranchActionMode] = useState(false);
  const [selectedBranch, setSelectedBranch] = useState<string | null>(null);
  const [branchActionError, setBranchActionError] = useState<string | null>(null);
  const [branchActionLoading, setBranchActionLoading] = useState(false);
  const [deleteBranchConfirm, setDeleteBranchConfirm] = useState(false);
  const [forceDelete, setForceDelete] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const resultsRef = useRef<HTMLDivElement>(null);
  const newBranchRef = useRef<HTMLDivElement>(null);
  const { suggestions: fsSuggestions, loading, currentDir } = useFilesystemSuggestions(inputValue);

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setHomePath(dir.replace(/\/$/, ''));
    }).catch(() => {});
  }, []);

  // Fetch recent locations from daemon when picker opens
  useEffect(() => {
    if (isOpen && onGetRecentLocations) {
      onGetRecentLocations()
        .then((result) => {
          setRecentLocations(result.locations);
        })
        .catch((err) => {
          console.error('[LocationPicker] Failed to fetch recent locations:', err);
          setRecentLocations([]);
        });
    }
  }, [isOpen, onGetRecentLocations]);

  // Filter recent locations based on input
  const filteredRecent = inputValue
    ? recentLocations.filter(
        (loc) =>
          loc.label.toLowerCase().includes(inputValue.toLowerCase()) ||
          loc.path.toLowerCase().includes(inputValue.toLowerCase())
      )
    : recentLocations;

  // Combine suggestions: filesystem first, then recent
  const allSuggestions = [
    ...fsSuggestions.map(s => ({ type: 'dir' as const, ...s })),
    ...filteredRecent.slice(0, 10).map(loc => ({
      type: 'recent' as const,
      name: loc.label,
      path: loc.path
    })),
  ];

  const totalSuggestions = allSuggestions.length;

  // Reset selection when suggestions change
  useEffect(() => {
    setSelectedIndex(0);
  }, [inputValue]);

  // Scroll selected item into view
  useEffect(() => {
    if (!resultsRef.current) return;
    const selectedEl = resultsRef.current.querySelector(`[data-index="${selectedIndex}"]`);
    if (selectedEl) {
      selectedEl.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    }
  }, [selectedIndex]);

  // Scroll new branch input into view when it appears
  useEffect(() => {
    if (showNewBranch) {
      // Small delay to ensure DOM is updated
      setTimeout(() => {
        newBranchRef.current?.scrollIntoView({ block: 'end', behavior: 'smooth' });
      }, 50);
    }
  }, [showNewBranch]);

  // Focus input when opened
  useEffect(() => {
    if (isOpen) {
      setSelectedIndex(0);
      setWorktreeMode(false);
      setSelectedRepo(null);
      setNewBranchName('');
      setShowNewBranch(false);
      setDeleteConfirmIndex(null);
      setDeleting(false);
      setDeleteError(null);
      // Reset branch state
      setBranches([]);
      setBranchActionMode(false);
      setSelectedBranch(null);
      setBranchActionError(null);
      setBranchActionLoading(false);
      setDeleteBranchConfirm(false);
      setForceDelete(false);

      // If worktreeFlowMode and projectsDirectory set, pre-populate and browse
      if (worktreeFlowMode && projectsDirectory) {
        setInputValue(projectsDirectory.replace(homePath, '~'));
      } else {
        setInputValue('');
      }

      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen, worktreeFlowMode, projectsDirectory, homePath]);

  // Global Escape key handler (in case input doesn't have focus)
  useEffect(() => {
    if (!isOpen) return;

    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        if (branchActionMode) {
          setBranchActionMode(false);
          setSelectedBranch(null);
          setBranchActionError(null);
          setDeleteBranchConfirm(false);
          setForceDelete(false);
          setSelectedIndex(0);
        } else if (worktreeMode) {
          setWorktreeMode(false);
          setSelectedRepo(null);
          setShowNewBranch(false);
          setNewBranchName('');
          setBranches([]);
        } else {
          onClose();
        }
      }
    };

    window.addEventListener('keydown', handleGlobalKeyDown);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown);
  }, [isOpen, worktreeMode, branchActionMode, onClose]);

  const handleSelect = useCallback(
    async (path: string) => {
      console.log('[LocationPicker] handleSelect:', path);
      // Check if path is a git repo by checking for .git
      try {
        const entries = await readDir(path);
        console.log('[LocationPicker] entries:', entries.map(e => e.name));
        const isGitRepo = entries.some(e => e.name === '.git');
        console.log('[LocationPicker] isGitRepo:', isGitRepo, 'onListWorktrees:', !!onListWorktrees);

        if (isGitRepo && onListWorktrees) {
          // Enter worktree mode
          console.log('[LocationPicker] Entering worktree mode');
          setSelectedRepo(path);
          setWorktreeMode(true);
          onListWorktrees(path);
          // Also fetch available branches
          if (onListBranches) {
            onListBranches(path)
              .then((result) => setBranches(result.branches))
              .catch((err) => console.error('[LocationPicker] Failed to fetch branches:', err));
          }
          return;
        }
      } catch (e) {
        // Not a readable directory, proceed with selection
        console.log('[LocationPicker] readDir error:', e);
      }

      // Not a git repo, just select it
      // Note: Location is automatically tracked by daemon when session registers
      onSelect(path);
      onClose();
    },
    [onSelect, onClose, onListWorktrees, onListBranches]
  );

  const handleWorktreeSelect = useCallback(
    (worktreePath: string) => {
      // Note: Location is automatically tracked by daemon when session registers
      onSelect(worktreePath);
      onClose();
    },
    [onSelect, onClose]
  );

  const handleMainBranchSelect = useCallback(() => {
    if (selectedRepo) {
      // Note: Location is automatically tracked by daemon when session registers
      onSelect(selectedRepo);
      onClose();
    }
  }, [selectedRepo, onSelect, onClose]);

  const handleNewBranch = useCallback(async () => {
    if (!selectedRepo || !newBranchName || !onCreateWorktree || creating) return;

    setCreating(true);
    setCreateError(null);
    try {
      const result = await onCreateWorktree(selectedRepo, newBranchName);
      if (result.success && result.path) {
        // Note: Location is automatically tracked by daemon when session registers
        onSelect(result.path);
        onClose();
      } else {
        setCreateError('Failed to create worktree');
      }
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : 'Failed to create worktree');
    } finally {
      setCreating(false);
    }
  }, [selectedRepo, newBranchName, onCreateWorktree, onSelect, onClose, creating]);

  const handleDeleteWorktree = useCallback(async () => {
    if (deleteConfirmIndex === null || !worktrees || !onDeleteWorktree || deleting) return;

    const worktreeIndex = deleteConfirmIndex - 1; // Index 0 is main branch
    const worktree = worktrees[worktreeIndex];
    if (!worktree) return;

    setDeleting(true);
    setDeleteError(null);
    try {
      const result = await onDeleteWorktree(worktree.path);
      if (result.success) {
        // Note: Deleted paths are automatically filtered by daemon on next fetch
        setDeleteConfirmIndex(null);
        // Refresh worktree list
        if (selectedRepo && onListWorktrees) {
          onListWorktrees(selectedRepo);
        }
      } else {
        setDeleteError('Failed to delete worktree');
      }
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : 'Failed to delete worktree');
    } finally {
      setDeleting(false);
    }
  }, [deleteConfirmIndex, worktrees, onDeleteWorktree, deleting, selectedRepo, onListWorktrees]);

  // Handler when user selects a branch (opens sub-menu)
  const handleBranchSelect = useCallback((branchName: string) => {
    setSelectedBranch(branchName);
    setBranchActionMode(true);
    setBranchActionError(null);
    setSelectedIndex(0);
  }, []);

  // Handler for "Open as worktree" action
  const handleOpenAsWorktree = useCallback(async () => {
    if (!selectedRepo || !selectedBranch || !onCreateWorktreeFromBranch || branchActionLoading) return;

    setBranchActionLoading(true);
    setBranchActionError(null);
    try {
      const result = await onCreateWorktreeFromBranch(selectedRepo, selectedBranch);
      if (result.success && result.path) {
        onSelect(result.path);
        onClose();
      } else {
        setBranchActionError('Failed to create worktree');
      }
    } catch (err) {
      setBranchActionError(err instanceof Error ? err.message : 'Failed to create worktree');
    } finally {
      setBranchActionLoading(false);
    }
  }, [selectedRepo, selectedBranch, onCreateWorktreeFromBranch, branchActionLoading, onSelect, onClose]);

  // Handler for "Switch main repo" action
  const handleSwitchMainRepo = useCallback(async () => {
    if (!selectedRepo || !selectedBranch || !onSwitchBranch || branchActionLoading) return;

    setBranchActionLoading(true);
    setBranchActionError(null);
    try {
      const result = await onSwitchBranch(selectedRepo, selectedBranch);
      if (result.success) {
        onSelect(selectedRepo);
        onClose();
      } else {
        setBranchActionError('Failed to switch branch');
      }
    } catch (err) {
      setBranchActionError(err instanceof Error ? err.message : 'Failed to switch branch');
    } finally {
      setBranchActionLoading(false);
    }
  }, [selectedRepo, selectedBranch, onSwitchBranch, branchActionLoading, onSelect, onClose]);

  // Handler for deleting a branch
  const handleDeleteBranch = useCallback(async () => {
    if (!selectedRepo || !selectedBranch || !onDeleteBranch || branchActionLoading) return;

    setBranchActionLoading(true);
    setBranchActionError(null);
    try {
      const result = await onDeleteBranch(selectedRepo, selectedBranch, forceDelete);
      if (result.success) {
        // Refresh branch list and go back to worktree mode
        if (onListBranches) {
          const branchResult = await onListBranches(selectedRepo);
          setBranches(branchResult.branches);
        }
        setBranchActionMode(false);
        setSelectedBranch(null);
        setDeleteBranchConfirm(false);
        setForceDelete(false);
      } else {
        setBranchActionError('Failed to delete branch');
      }
    } catch (err) {
      const errorMsg = err instanceof Error ? err.message : 'Failed to delete branch';
      // If error mentions "not fully merged", suggest force delete
      if (errorMsg.includes('not fully merged') || errorMsg.includes('not merged')) {
        setBranchActionError('Branch not fully merged. Use force delete (f)?');
        setForceDelete(true);
      } else {
        setBranchActionError(errorMsg);
      }
    } finally {
      setBranchActionLoading(false);
    }
  }, [selectedRepo, selectedBranch, onDeleteBranch, branchActionLoading, forceDelete, onListBranches]);

  // Total items in worktree mode: 1 (main) + worktrees + branches + 1 (new branch)
  const worktreeCount = worktrees?.length || 0;
  const branchCount = branches.length;
  const worktreeItemCount = 1 + worktreeCount + branchCount + 1;
  // Index boundaries
  const worktreeStartIndex = 1;
  const branchStartIndex = 1 + worktreeCount;
  const newBranchIndex = 1 + worktreeCount + branchCount;

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      // Stop propagation for all keys to prevent global shortcuts from firing
      e.stopPropagation();

      // Branch action mode keyboard handling (sub-menu)
      if (branchActionMode) {
        if (e.key === 'Escape') {
          e.preventDefault();
          setBranchActionMode(false);
          setSelectedBranch(null);
          setBranchActionError(null);
          setDeleteBranchConfirm(false);
          setForceDelete(false);
          return;
        }

        // Delete branch confirmation
        if (deleteBranchConfirm) {
          if (e.key === 'n') {
            e.preventDefault();
            setDeleteBranchConfirm(false);
            setForceDelete(false);
            setBranchActionError(null);
            return;
          }
          if ((e.key === 'Enter' || e.key === 'y' || (forceDelete && e.key === 'f')) && !branchActionLoading) {
            e.preventDefault();
            handleDeleteBranch();
            return;
          }
          return;
        }

        if (e.key === 'ArrowDown') {
          e.preventDefault();
          setSelectedIndex((prev) => Math.min(prev + 1, 2)); // 3 options: 0, 1, 2
          return;
        }

        if (e.key === 'ArrowUp') {
          e.preventDefault();
          setSelectedIndex((prev) => Math.max(prev - 1, 0));
          return;
        }

        if (e.key === 'Enter' && !branchActionLoading) {
          e.preventDefault();
          if (selectedIndex === 0) handleOpenAsWorktree();
          else if (selectedIndex === 1) handleSwitchMainRepo();
          else if (selectedIndex === 2) setDeleteBranchConfirm(true);
          return;
        }

        // Number shortcuts
        if (e.key === '1' && !branchActionLoading) {
          e.preventDefault();
          handleOpenAsWorktree();
          return;
        }
        if (e.key === '2' && !branchActionLoading) {
          e.preventDefault();
          handleSwitchMainRepo();
          return;
        }
        if (e.key === 'd' && !branchActionLoading) {
          e.preventDefault();
          setDeleteBranchConfirm(true);
          return;
        }
        return;
      }

      // Worktree mode keyboard handling
      if (worktreeMode) {
        // Delete confirmation mode handling
        if (deleteConfirmIndex !== null) {
          if (e.key === 'Escape' || e.key === 'n') {
            e.preventDefault();
            setDeleteConfirmIndex(null);
            setDeleteError(null);
            return;
          }
          if ((e.key === 'Enter' || e.key === 'y') && !deleting) {
            e.preventDefault();
            handleDeleteWorktree();
            return;
          }
          return; // Block other keys during confirmation
        }

        if (e.key === 'Escape') {
          e.preventDefault();
          setWorktreeMode(false);
          setSelectedRepo(null);
          setShowNewBranch(false);
          setNewBranchName('');
          setBranches([]);
          return;
        }

        // 'd' to delete selected worktree (not main branch, not branches, not new branch)
        if (e.key === 'd' && !showNewBranch && selectedIndex >= worktreeStartIndex && selectedIndex < branchStartIndex && onDeleteWorktree) {
          e.preventDefault();
          setDeleteConfirmIndex(selectedIndex);
          return;
        }

        if (e.key === 'n' && !showNewBranch) {
          e.preventDefault();
          setShowNewBranch(true);
          return;
        }

        // Arrow key navigation
        if (e.key === 'ArrowDown') {
          e.preventDefault();
          setSelectedIndex((prev) => Math.min(prev + 1, worktreeItemCount - 1));
          return;
        }

        if (e.key === 'ArrowUp') {
          e.preventDefault();
          setSelectedIndex((prev) => Math.max(prev - 1, 0));
          return;
        }

        // Enter to select
        if (e.key === 'Enter' && !showNewBranch) {
          e.preventDefault();
          if (selectedIndex === 0) {
            handleMainBranchSelect();
          } else if (selectedIndex === newBranchIndex) {
            setShowNewBranch(true);
          } else if (selectedIndex >= branchStartIndex) {
            // Branch selected - open action sub-menu
            const branchIndex = selectedIndex - branchStartIndex;
            if (branches[branchIndex]) {
              handleBranchSelect(branches[branchIndex].name);
            }
          } else {
            // Worktree selected
            const worktreeIndex = selectedIndex - worktreeStartIndex;
            if (worktrees && worktrees[worktreeIndex]) {
              handleWorktreeSelect(worktrees[worktreeIndex].path);
            }
          }
          return;
        }

        // Number shortcuts for quick selection (1-9)
        const num = parseInt(e.key);
        if (num >= 1 && num <= 9) {
          e.preventDefault();
          const targetIndex = num - 1;
          if (targetIndex === 0) {
            handleMainBranchSelect();
          } else if (targetIndex < branchStartIndex) {
            const worktreeIndex = targetIndex - worktreeStartIndex;
            if (worktrees && worktrees[worktreeIndex]) {
              handleWorktreeSelect(worktrees[worktreeIndex].path);
            }
          } else if (targetIndex < newBranchIndex) {
            const branchIndex = targetIndex - branchStartIndex;
            if (branches[branchIndex]) {
              handleBranchSelect(branches[branchIndex].name);
            }
          }
          return;
        }
        return;
      }

      // Normal mode keyboard handling
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
        return;
      }

      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setSelectedIndex((prev) => Math.min(prev + 1, totalSuggestions - 1));
        return;
      }

      if (e.key === 'ArrowUp') {
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
        return;
      }

      // Tab autocompletes the selected directory suggestion
      if (e.key === 'Tab' && fsSuggestions.length > 0) {
        e.preventDefault();
        const selected = allSuggestions[selectedIndex];
        if (selected && selected.type === 'dir') {
          setInputValue(selected.path.replace(homePath, '~'));
        }
        return;
      }

      if (e.key === 'Enter') {
        e.preventDefault();
        const selected = allSuggestions[selectedIndex];
        if (selected) {
          if (selected.type === 'dir') {
            // For directories, expand to input for further navigation or select
            const expanded = selected.path;
            // If user presses Enter on a dir, select it
            handleSelect(expanded);
          } else {
            handleSelect(selected.path);
          }
        } else if (inputValue.startsWith('/') || inputValue.startsWith('~')) {
          // Direct path input
          const path = inputValue.startsWith('~')
            ? inputValue.replace('~', homePath)
            : inputValue;
          handleSelect(path.replace(/\/$/, '')); // Remove trailing slash
        }
        return;
      }
    },
    [worktreeMode, branchActionMode, worktrees, branches, worktreeItemCount, worktreeStartIndex, branchStartIndex, newBranchIndex, showNewBranch, handleMainBranchSelect, handleWorktreeSelect, handleBranchSelect, handleDeleteWorktree, handleOpenAsWorktree, handleSwitchMainRepo, handleDeleteBranch, deleteConfirmIndex, deleteBranchConfirm, forceDelete, deleting, branchActionLoading, onDeleteWorktree, allSuggestions, selectedIndex, inputValue, handleSelect, onClose, homePath, fsSuggestions.length, totalSuggestions]
  );

  if (!isOpen) return null;

  return (
    <div className="location-picker-overlay" onClick={onClose}>
      <div className="location-picker" onClick={(e) => e.stopPropagation()}>
        <div className="picker-header">
          <div className="picker-title">New Session Location</div>
          <div className="picker-input-wrap">
            <input
              ref={inputRef}
              type="text"
              className="picker-input"
              placeholder="Type path (e.g., ~/projects) or search..."
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleKeyDown}
            />
            {loading && <div className="picker-loading" />}
          </div>
          {currentDir && (
            <div className="picker-breadcrumb">
              <span className="picker-breadcrumb-label">Browsing:</span>
              <span className="picker-breadcrumb-path">{currentDir}</span>
            </div>
          )}
        </div>

        <div className="picker-results" ref={resultsRef}>
          {/* Branch action sub-menu */}
          {branchActionMode && selectedBranch ? (
            <>
              <div className="picker-section">
                <div className="picker-section-title">{selectedBranch}</div>

                {deleteBranchConfirm ? (
                  <div className="picker-item delete-confirm">
                    <div className="picker-icon branch delete-icon">×</div>
                    <div className="picker-info">
                      <div className="picker-name delete-prompt">
                        {branchActionLoading ? 'Deleting...' : forceDelete ? 'Force delete branch?' : 'Delete branch?'}
                      </div>
                      {branchActionError ? (
                        <div className="picker-path delete-error">{branchActionError}</div>
                      ) : (
                        <div className="picker-path">
                          {forceDelete ? '[f] force · [n] cancel' : '[Enter/y] confirm · [n] cancel'}
                        </div>
                      )}
                    </div>
                  </div>
                ) : (
                  <>
                    {/* Open as worktree option */}
                    <div
                      className={`picker-item ${selectedIndex === 0 ? 'selected' : ''}`}
                      data-index={0}
                      onClick={handleOpenAsWorktree}
                      onMouseEnter={() => setSelectedIndex(0)}
                    >
                      <div className="picker-shortcut">1</div>
                      <div className="picker-icon worktree">◎</div>
                      <div className="picker-info">
                        <div className="picker-name">Open as worktree</div>
                        <div className="picker-path">Creates a new worktree directory</div>
                      </div>
                    </div>

                    {/* Switch main repo option */}
                    <div
                      className={`picker-item ${selectedIndex === 1 ? 'selected' : ''}`}
                      data-index={1}
                      onClick={handleSwitchMainRepo}
                      onMouseEnter={() => setSelectedIndex(1)}
                    >
                      <div className="picker-shortcut">2</div>
                      <div className="picker-icon main">●</div>
                      <div className="picker-info">
                        <div className="picker-name">Switch main repo</div>
                        <div className="picker-path">Checkout in {selectedRepo}</div>
                      </div>
                    </div>

                    {/* Delete branch option */}
                    <div
                      className={`picker-item ${selectedIndex === 2 ? 'selected' : ''}`}
                      data-index={2}
                      onClick={() => setDeleteBranchConfirm(true)}
                      onMouseEnter={() => setSelectedIndex(2)}
                    >
                      <div className="picker-shortcut">d</div>
                      <div className="picker-icon delete">×</div>
                      <div className="picker-info">
                        <div className="picker-name">Delete branch</div>
                      </div>
                    </div>
                  </>
                )}
              </div>

              {branchActionError && !deleteBranchConfirm && (
                <div className="picker-error">{branchActionError}</div>
              )}
            </>
          ) : worktreeMode && selectedRepo ? (
            <>
              {/* ACTIVE section: main + worktrees */}
              <div className="picker-section">
                <div className="picker-section-header">ACTIVE</div>

                {/* Main branch option */}
                <div
                  className={`picker-item ${selectedIndex === 0 ? 'selected' : ''}`}
                  data-index={0}
                  onClick={handleMainBranchSelect}
                  onMouseEnter={() => setSelectedIndex(0)}
                >
                  <div className="picker-shortcut">1</div>
                  <div className="picker-icon main">●</div>
                  <div className="picker-info">
                    <div className="picker-name">main</div>
                    <div className="picker-path">{selectedRepo}</div>
                  </div>
                </div>

                {/* Worktree options */}
                {worktrees?.map((wt, index) => {
                  const itemIndex = worktreeStartIndex + index;
                  const isConfirmingDelete = deleteConfirmIndex === itemIndex;
                  return (
                    <div
                      key={wt.path}
                      className={`picker-item ${selectedIndex === itemIndex ? 'selected' : ''} ${isConfirmingDelete ? 'delete-confirm' : ''}`}
                      data-index={itemIndex}
                      onClick={() => isConfirmingDelete ? undefined : handleWorktreeSelect(wt.path)}
                      onMouseEnter={() => setSelectedIndex(itemIndex)}
                    >
                      {isConfirmingDelete ? (
                        <>
                          <div className="picker-icon worktree delete-icon">×</div>
                          <div className="picker-info">
                            <div className="picker-name delete-prompt">
                              {deleting ? 'Deleting...' : `Delete ${wt.branch}?`}
                            </div>
                            {deleteError ? (
                              <div className="picker-path delete-error">{deleteError}</div>
                            ) : (
                              <div className="picker-path">[Enter/y] confirm · [Esc/n] cancel</div>
                            )}
                          </div>
                        </>
                      ) : (
                        <>
                          <div className="picker-shortcut">{itemIndex + 1}</div>
                          <div className="picker-icon worktree">◎</div>
                          <div className="picker-info">
                            <div className="picker-name">{wt.branch}</div>
                            <div className="picker-path">{wt.path}</div>
                          </div>
                        </>
                      )}
                    </div>
                  );
                })}
              </div>

              {/* AVAILABLE section: branches not checked out */}
              {branches.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-header">AVAILABLE</div>
                  {branches.map((branch, index) => {
                    const itemIndex = branchStartIndex + index;
                    return (
                      <div
                        key={branch.name}
                        className={`picker-item ${selectedIndex === itemIndex ? 'selected' : ''}`}
                        data-index={itemIndex}
                        onClick={() => handleBranchSelect(branch.name)}
                        onMouseEnter={() => setSelectedIndex(itemIndex)}
                      >
                        <div className="picker-shortcut">{itemIndex + 1 <= 9 ? itemIndex + 1 : ''}</div>
                        <div className="picker-icon branch">○</div>
                        <div className="picker-info">
                          <div className="picker-name">{branch.name}</div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}

              {/* New branch section */}
              {showNewBranch ? (
                <div className="picker-section" ref={newBranchRef}>
                  <div className="picker-section-title">New Branch</div>
                  <div className="picker-new-branch">
                    <input
                      type="text"
                      className={`picker-branch-input ${createError ? 'error' : ''}`}
                      placeholder="feature/my-branch"
                      value={newBranchName}
                      onChange={(e) => {
                        setNewBranchName(e.target.value);
                        setCreateError(null);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && newBranchName && !creating) {
                          handleNewBranch();
                        }
                        if (e.key === 'Escape') {
                          setShowNewBranch(false);
                          setCreateError(null);
                        }
                      }}
                      disabled={creating}
                      autoFocus
                    />
                    {creating && <div className="picker-creating">Creating...</div>}
                    {createError && <div className="picker-error">{createError}</div>}
                  </div>
                </div>
              ) : (
                <div
                  className={`picker-item picker-new-branch-trigger ${selectedIndex === newBranchIndex ? 'selected' : ''}`}
                  data-index={newBranchIndex}
                  onClick={() => setShowNewBranch(true)}
                  onMouseEnter={() => setSelectedIndex(newBranchIndex)}
                >
                  <div className="picker-shortcut">n</div>
                  <div className="picker-icon new">+</div>
                  <div className="picker-info">
                    <div className="picker-name">New branch...</div>
                  </div>
                </div>
              )}
            </>
          ) : (
            <>
              {/* Filesystem suggestions */}
              {fsSuggestions.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">Directories</div>
                  {fsSuggestions.map((item, index) => (
                    <div
                      key={item.path}
                      className={`picker-item ${index === selectedIndex ? 'selected' : ''}`}
                      data-index={index}
                      onClick={() => handleSelect(item.path)}
                      onMouseEnter={() => setSelectedIndex(index)}
                    >
                      <div className="picker-icon">📁</div>
                      <div className="picker-info">
                        <div className="picker-name">{item.name}</div>
                      </div>
                    </div>
                  ))}
                </div>
              )}

              {/* Recent locations */}
              {filteredRecent.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">Recent</div>
                  {filteredRecent.slice(0, 10).map((loc, index) => {
                    const globalIndex = fsSuggestions.length + index;
                    return (
                      <div
                        key={loc.path}
                        className={`picker-item ${globalIndex === selectedIndex ? 'selected' : ''}`}
                        data-index={globalIndex}
                        onClick={() => handleSelect(loc.path)}
                        onMouseEnter={() => setSelectedIndex(globalIndex)}
                      >
                        <div className="picker-icon">🕐</div>
                        <div className="picker-info">
                          <div className="picker-name">{loc.label}</div>
                          <div className="picker-path">{loc.path.replace(homePath, '~')}</div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}

              {/* Empty state */}
              {fsSuggestions.length === 0 && filteredRecent.length === 0 && (
                <div className="picker-empty">
                  {inputValue
                    ? 'No matches. Press Enter to use path directly.'
                    : 'Type a path to browse directories'}
                </div>
              )}
            </>
          )}
        </div>

        <div className="picker-footer">
          {branchActionMode ? (
            <>
              <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
              <span className="shortcut"><kbd>1</kbd> worktree</span>
              <span className="shortcut"><kbd>2</kbd> switch</span>
              <span className="shortcut"><kbd>d</kbd> delete</span>
              <span className="shortcut"><kbd>Esc</kbd> back</span>
            </>
          ) : worktreeMode ? (
            <>
              <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
              <span className="shortcut"><kbd>Enter</kbd> select</span>
              <span className="shortcut"><kbd>n</kbd> new</span>
              <span className="shortcut"><kbd>d</kbd> delete</span>
              <span className="shortcut"><kbd>Esc</kbd> back</span>
            </>
          ) : (
            <>
              <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
              <span className="shortcut"><kbd>Tab</kbd> autocomplete</span>
              <span className="shortcut"><kbd>Enter</kbd> select</span>
              <span className="shortcut"><kbd>Esc</kbd> cancel</span>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
