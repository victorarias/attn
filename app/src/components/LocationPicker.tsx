// app/src/components/LocationPicker.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import { homeDir } from '@tauri-apps/api/path';
import { readDir } from '@tauri-apps/plugin-fs';
import { useLocationHistory } from '../hooks/useLocationHistory';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import type { DaemonWorktree } from '../hooks/useDaemonSocket';
import './LocationPicker.css';

interface LocationPickerProps {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (path: string) => void;
  worktrees?: DaemonWorktree[];
  onListWorktrees?: (mainRepo: string) => void;
  onCreateWorktree?: (mainRepo: string, branch: string) => Promise<{ success: boolean; path?: string }>;
  worktreeFlowMode?: boolean;
  projectsDirectory?: string;
}

export function LocationPicker({ isOpen, onClose, onSelect, worktrees, onListWorktrees, onCreateWorktree, worktreeFlowMode, projectsDirectory }: LocationPickerProps) {
  const [inputValue, setInputValue] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [homePath, setHomePath] = useState('/Users');
  const [worktreeMode, setWorktreeMode] = useState(false);
  const [selectedRepo, setSelectedRepo] = useState<string | null>(null);
  const [newBranchName, setNewBranchName] = useState('');
  const [showNewBranch, setShowNewBranch] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const { getRecentLocations, addToHistory } = useLocationHistory();
  const { suggestions: fsSuggestions, loading, currentDir } = useFilesystemSuggestions(inputValue);

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setHomePath(dir.replace(/\/$/, ''));
    }).catch(() => {});
  }, []);

  const recentLocations = getRecentLocations();

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

  // Focus input when opened
  useEffect(() => {
    if (isOpen) {
      setSelectedIndex(0);
      setWorktreeMode(false);
      setSelectedRepo(null);
      setNewBranchName('');
      setShowNewBranch(false);

      // If worktreeFlowMode and projectsDirectory set, pre-populate and browse
      if (worktreeFlowMode && projectsDirectory) {
        setInputValue(projectsDirectory.replace(homePath, '~'));
      } else {
        setInputValue('');
      }

      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen, worktreeFlowMode, projectsDirectory, homePath]);

  const handleSelect = useCallback(
    async (path: string) => {
      // Check if path is a git repo by checking for .git
      try {
        const entries = await readDir(path);
        const isGitRepo = entries.some(e => e.name === '.git');

        if (isGitRepo && onListWorktrees) {
          // Enter worktree mode
          setSelectedRepo(path);
          setWorktreeMode(true);
          onListWorktrees(path);
          return;
        }
      } catch (e) {
        // Not a readable directory, proceed with selection
      }

      // Not a git repo, just select it
      addToHistory(path);
      onSelect(path);
      onClose();
    },
    [addToHistory, onSelect, onClose, onListWorktrees]
  );

  const handleWorktreeSelect = useCallback(
    (worktreePath: string) => {
      addToHistory(worktreePath);
      onSelect(worktreePath);
      onClose();
    },
    [addToHistory, onSelect, onClose]
  );

  const handleMainBranchSelect = useCallback(() => {
    if (selectedRepo) {
      addToHistory(selectedRepo);
      onSelect(selectedRepo);
      onClose();
    }
  }, [selectedRepo, addToHistory, onSelect, onClose]);

  const handleNewBranch = useCallback(async () => {
    if (!selectedRepo || !newBranchName || !onCreateWorktree || creating) return;

    setCreating(true);
    setCreateError(null);
    try {
      const result = await onCreateWorktree(selectedRepo, newBranchName);
      if (result.success && result.path) {
        addToHistory(result.path);
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
  }, [selectedRepo, newBranchName, onCreateWorktree, addToHistory, onSelect, onClose, creating]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      // Worktree mode keyboard handling
      if (worktreeMode) {
        if (e.key === 'Escape') {
          e.preventDefault();
          setWorktreeMode(false);
          setSelectedRepo(null);
          setShowNewBranch(false);
          setNewBranchName('');
          return;
        }

        if (e.key === 'n') {
          e.preventDefault();
          setShowNewBranch(true);
          return;
        }

        // Number shortcuts for quick selection
        const num = parseInt(e.key);
        if (num >= 1 && num <= 9) {
          e.preventDefault();
          if (num === 1) {
            handleMainBranchSelect();
          } else {
            const worktreeIndex = num - 2;
            if (worktrees && worktrees[worktreeIndex]) {
              handleWorktreeSelect(worktrees[worktreeIndex].path);
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
    [worktreeMode, worktrees, handleMainBranchSelect, handleWorktreeSelect, allSuggestions, selectedIndex, inputValue, handleSelect, onClose, homePath, fsSuggestions.length, totalSuggestions]
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

        <div className="picker-results">
          {worktreeMode && selectedRepo ? (
            <>
              <div className="picker-section">
                <div className="picker-section-title">Select Branch</div>

                {/* Main branch option */}
                <div
                  className={`picker-item ${selectedIndex === 0 ? 'selected' : ''}`}
                  onClick={handleMainBranchSelect}
                  onMouseEnter={() => setSelectedIndex(0)}
                >
                  <div className="picker-shortcut">1</div>
                  <div className="picker-icon">🌿</div>
                  <div className="picker-info">
                    <div className="picker-name">Main branch</div>
                    <div className="picker-path">Use repository root</div>
                  </div>
                </div>

                {/* Worktree options */}
                {worktrees?.map((wt, index) => (
                  <div
                    key={wt.path}
                    className={`picker-item ${selectedIndex === index + 1 ? 'selected' : ''}`}
                    onClick={() => handleWorktreeSelect(wt.path)}
                    onMouseEnter={() => setSelectedIndex(index + 1)}
                  >
                    <div className="picker-shortcut">{index + 2}</div>
                    <div className="picker-icon">⎇</div>
                    <div className="picker-info">
                      <div className="picker-name">{wt.branch}</div>
                      <div className="picker-path">{wt.path}</div>
                    </div>
                  </div>
                ))}
              </div>

              {/* New branch section */}
              {showNewBranch ? (
                <div className="picker-section">
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
                  className="picker-item picker-new-branch-trigger"
                  onClick={() => setShowNewBranch(true)}
                >
                  <div className="picker-shortcut">n</div>
                  <div className="picker-icon">+</div>
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
          {worktreeMode ? (
            <>
              <span className="shortcut"><kbd>1-9</kbd> quick select</span>
              <span className="shortcut"><kbd>n</kbd> new branch</span>
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
