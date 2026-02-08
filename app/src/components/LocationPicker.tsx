import { useState, useEffect, useCallback } from 'react';
import { homeDir } from '@tauri-apps/api/path';
import { readDir } from '@tauri-apps/plugin-fs';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import { PathInput } from './NewSessionDialog/PathInput';
import { RepoOptions } from './NewSessionDialog/RepoOptions';
import type { RecentLocation } from '../hooks/useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import { useSettings } from '../contexts/SettingsContext';
import {
  type AgentAvailability,
  hasAnyAvailableAgents,
  isAgentAvailable,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import './LocationPicker.css';

interface RepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: Array<{ path: string; branch: string }>;
  branches: Array<{ name: string; commit_hash?: string; commit_time?: string }>;
  fetched_at?: string;
}

interface LocationPickerProps {
  isOpen: boolean;
  onClose: () => void;
  onSelect: (path: string, agent: SessionAgent, resumeEnabled?: boolean) => void;
  onGetRecentLocations?: () => Promise<{ locations: RecentLocation[] }>;
  onGetRepoInfo?: (mainRepo: string) => Promise<{ success: boolean; info?: RepoInfo; error?: string }>;
  onCreateWorktree?: (mainRepo: string, branch: string, path?: string, startingFrom?: string) => Promise<{ success: boolean; path?: string }>;
  onDeleteWorktree?: (path: string) => Promise<{ success: boolean; error?: string }>;
  onDeleteBranch?: (mainRepo: string, branch: string, force?: boolean) => Promise<{ success: boolean; error?: string }>;
  onError?: (message: string) => void;
  projectsDirectory?: string;
  agentAvailability?: AgentAvailability;
}

const MAX_RECENT_LOCATIONS = 10;
const SESSION_AGENT_KEY = 'new_session_agent';
const DEFAULT_AGENT_AVAILABILITY: AgentAvailability = {
  codex: true,
  claude: true,
  copilot: true,
};

const normalizeAgent = (value?: string): SessionAgent | null => {
  if (!value) return null;
  const lower = value.toLowerCase();
  if (lower === 'codex' || lower === 'claude' || lower === 'copilot') {
    return lower as SessionAgent;
  }
  return null;
};

type Mode = 'path-input' | 'repo-options';

interface State {
  mode: Mode;
  inputValue: string;
  selectedIndex: number;
  selectedRepo: string | null;
  repoInfo: RepoInfo | null;
  recentLocations: RecentLocation[];
  homePath: string;
  refreshing: boolean;
  // Tracks if user has intentionally selected since last Tab
  // (typing or arrow navigation = intentional, Tab auto-selects first child = not intentional)
  hasSelectedSinceTab: boolean;
  agent: SessionAgent;
  resumeEnabled: boolean;
}

export function LocationPicker({
  isOpen,
  onClose,
  onSelect,
  onGetRecentLocations,
  onGetRepoInfo,
  onCreateWorktree,
  onDeleteWorktree,
  onDeleteBranch,
  onError,
  projectsDirectory,
  agentAvailability,
}: LocationPickerProps) {
  const { settings, setSetting } = useSettings();
  const effectiveAgentAvailability = agentAvailability || DEFAULT_AGENT_AVAILABILITY;
  const hasAvailableAgents = hasAnyAvailableAgents(effectiveAgentAvailability);
  const [state, setState] = useState<State>({
    mode: 'path-input',
    inputValue: '',
    selectedIndex: -1, // Start with nothing selected
    selectedRepo: null,
    repoInfo: null,
    recentLocations: [],
    homePath: '/Users',
    refreshing: false,
    hasSelectedSinceTab: false, // Start false - user hasn't navigated yet
    agent: 'claude',
    resumeEnabled: false,
  });

  const { suggestions: fsSuggestions, currentDir } = useFilesystemSuggestions(state.inputValue);

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setState(prev => ({ ...prev, homePath: dir.replace(/\/$/, '') }));
    }).catch(() => {});
  }, []);

  // Fetch recent locations when picker opens
  useEffect(() => {
    if (isOpen && onGetRecentLocations) {
      onGetRecentLocations()
        .then((result) => {
          setState(prev => ({ ...prev, recentLocations: result.locations }));
        })
        .catch((err) => {
          console.error('[LocationPicker] Failed to fetch recent locations:', err);
          setState(prev => ({ ...prev, recentLocations: [] }));
        });
    }
  }, [isOpen, onGetRecentLocations]);

  // Reset state when picker opens
  useEffect(() => {
    if (isOpen) {
      // Contract projectsDirectory to use ~ for home path
      let initialValue = '';
      if (projectsDirectory) {
        if (projectsDirectory.startsWith(state.homePath + '/')) {
          initialValue = '~' + projectsDirectory.slice(state.homePath.length);
        } else if (projectsDirectory === state.homePath) {
          initialValue = '~';
        } else {
          initialValue = projectsDirectory;
        }
        // Ensure trailing slash for directory browsing
        if (!initialValue.endsWith('/')) {
          initialValue += '/';
        }
      }
      setState(prev => ({
        ...prev,
        mode: 'path-input',
        inputValue: initialValue,
        selectedIndex: -1, // Nothing selected initially
        selectedRepo: null,
        repoInfo: null,
        refreshing: false,
        hasSelectedSinceTab: false, // User hasn't navigated yet
        resumeEnabled: false,
      }));
    }
  }, [isOpen, projectsDirectory, state.homePath]);

  const savedAgent = normalizeAgent(settings[SESSION_AGENT_KEY]);
  useEffect(() => {
    if (!savedAgent) return;
    const resolvedSavedAgent = resolvePreferredAgent(savedAgent, effectiveAgentAvailability, 'codex');
    setState(prev => (prev.agent === resolvedSavedAgent ? prev : { ...prev, agent: resolvedSavedAgent }));
  }, [effectiveAgentAvailability, savedAgent]);

  useEffect(() => {
    const resolvedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
    if (resolvedAgent === state.agent) {
      return;
    }
    setState(prev => ({ ...prev, agent: resolvedAgent }));
  }, [effectiveAgentAvailability, state.agent]);

  // Filter recent locations based on input
  // Expand ~ to home path so filtering works with stored full paths
  const expandedInput = state.inputValue.startsWith('~')
    ? state.inputValue.replace('~', state.homePath)
    : state.inputValue;
  const filteredRecent = expandedInput
    ? state.recentLocations.filter(
        (loc) =>
          loc.label.toLowerCase().includes(expandedInput.toLowerCase()) ||
          loc.path.toLowerCase().includes(expandedInput.toLowerCase())
      )
    : state.recentLocations;

  // Contract full path to use ~ for home directory
  const contractPath = (path: string) => {
    if (path.startsWith(state.homePath + '/')) {
      return '~' + path.slice(state.homePath.length);
    } else if (path === state.homePath) {
      return '~';
    }
    return path;
  };

  // Calculate ghost text from selected item (recent or filesystem suggestion)
  // When nothing selected (-1), use first available suggestion for Tab autocomplete
  const getSelectedPath = () => {
    if (state.selectedIndex === -1) {
      // Nothing selected - use first suggestion for autocomplete
      return fsSuggestions[0]?.path || filteredRecent[0]?.path || '';
    }
    if (state.selectedIndex < filteredRecent.length) {
      return filteredRecent[state.selectedIndex]?.path || '';
    }
    const fsIndex = state.selectedIndex - filteredRecent.length;
    return fsSuggestions[fsIndex]?.path || '';
  };
  // Contract ghostText to use ~ so it matches input value format
  const ghostText = contractPath(getSelectedPath());

  // Reset selection when input changes (user is typing, not navigating)
  useEffect(() => {
    setState(prev => ({ ...prev, selectedIndex: -1 }));
  }, [state.inputValue]);

  // Scroll selected item into view
  useEffect(() => {
    if (state.selectedIndex >= 0) {
      const el = document.querySelector(`[data-index="${state.selectedIndex}"]`);
      el?.scrollIntoView({ block: 'nearest' });
    }
  }, [state.selectedIndex]);

  const handleSelect = useCallback(
    async (rawPath: string) => {
      if (!hasAvailableAgents) {
        onError?.('No supported agent CLI found in PATH (codex, claude, copilot).');
        return;
      }
      // Expand ~ to home path and remove trailing slash
      const path = (rawPath.startsWith('~')
        ? rawPath.replace('~', state.homePath)
        : rawPath
      ).replace(/\/$/, '');
      const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');

      console.log('[LocationPicker] handleSelect:', path);

      // Check if path is a git repo by checking for .git
      try {
        const entries = await readDir(path);
        const isGitRepo = entries.some(e => e.name === '.git');

        if (isGitRepo && onGetRepoInfo) {
          // Fetch repo info and enter repo-options mode
          console.log('[LocationPicker] Entering repo-options mode');
          const result = await onGetRepoInfo(path);

          if (result.success && result.info) {
            setState(prev => ({
              ...prev,
              mode: 'repo-options',
              selectedRepo: path,
              repoInfo: result.info || null,
            }));
          } else {
            // Failed to get repo info, just select the path
            onSelect(path, selectedAgent, state.resumeEnabled);
            onClose();
          }
          return;
        }
      } catch (e) {
        // Not a readable directory, proceed with selection
        console.log('[LocationPicker] readDir error:', e);
      }

      // Not a git repo, just select it
      onSelect(path, selectedAgent, state.resumeEnabled);
      onClose();
    },
    [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onGetRepoInfo, onSelect, state.agent, state.homePath, state.resumeEnabled]
  );

  const handleAgentChange = useCallback((agent: SessionAgent) => {
    if (!isAgentAvailable(effectiveAgentAvailability, agent)) return;
    setState(prev => ({ ...prev, agent }));
    setSetting(SESSION_AGENT_KEY, agent);
  }, [effectiveAgentAvailability, setSetting]);

  const handleResumeToggle = useCallback(() => {
    setState(prev => ({
      ...prev,
      resumeEnabled: !prev.resumeEnabled,
    }));
  }, []);

  const handlePathInputChange = useCallback((value: string) => {
    setState(prev => ({ ...prev, inputValue: value, hasSelectedSinceTab: true }));
  }, []);

  // Tab completion handler - sets hasSelectedSinceTab to false since
  // the auto-selection of first child is not an intentional selection
  const handleTabComplete = useCallback((value: string) => {
    setState(prev => ({ ...prev, inputValue: value, hasSelectedSinceTab: false }));
  }, []);

  // PathInput already sends the raw path - handleSelect does the expansion
  const handlePathInputSelect = useCallback((path: string) => {
    handleSelect(path);
  }, [handleSelect]);

  // RepoOptions callbacks
  const handleSelectMainRepo = useCallback(() => {
    if (!hasAvailableAgents) {
      onError?.('No supported agent CLI found in PATH (codex, claude, copilot).');
      return;
    }
    if (state.selectedRepo) {
      const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
      onSelect(state.selectedRepo, selectedAgent, state.resumeEnabled);
      onClose();
    }
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onSelect, state.agent, state.resumeEnabled, state.selectedRepo]);

  const handleSelectWorktree = useCallback((path: string) => {
    if (!hasAvailableAgents) {
      onError?.('No supported agent CLI found in PATH (codex, claude, copilot).');
      return;
    }
    const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
    onSelect(path, selectedAgent, state.resumeEnabled);
    onClose();
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onSelect, state.agent, state.resumeEnabled]);

  const handleSelectBranch = useCallback((_branch: string) => {
    if (!hasAvailableAgents) {
      onError?.('No supported agent CLI found in PATH (codex, claude, copilot).');
      return;
    }
    if (state.selectedRepo) {
      const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
      onSelect(state.selectedRepo, selectedAgent, state.resumeEnabled);
      onClose();
    }
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onSelect, state.agent, state.resumeEnabled, state.selectedRepo]);

  const handleCreateWorktree = useCallback(async (branchName: string, startingFrom: string) => {
    if (!hasAvailableAgents) {
      onError?.('No supported agent CLI found in PATH (codex, claude, copilot).');
      return;
    }
    if (!state.selectedRepo || !onCreateWorktree) return;

    try {
      const result = await onCreateWorktree(state.selectedRepo, branchName, undefined, startingFrom);
      if (result.success && result.path) {
        const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
        onSelect(result.path, selectedAgent);
        onClose();
      }
    } catch (err) {
      console.error('[LocationPicker] Failed to create worktree:', err);
    }
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onCreateWorktree, onError, onSelect, state.agent, state.selectedRepo]);

  const handleRefresh = useCallback(async () => {
    if (!state.selectedRepo || !onGetRepoInfo || state.refreshing) return;

    setState(prev => ({ ...prev, refreshing: true }));
    try {
      const result = await onGetRepoInfo(state.selectedRepo);
      if (result.success && result.info) {
        setState(prev => ({
          ...prev,
          repoInfo: result.info || null,
          refreshing: false,
        }));
      }
    } catch (err) {
      console.error('[LocationPicker] Failed to refresh repo info:', err);
    } finally {
      setState(prev => ({ ...prev, refreshing: false }));
    }
  }, [state.selectedRepo, state.refreshing, onGetRepoInfo]);

  const handleBack = useCallback(() => {
    setState(prev => ({
      ...prev,
      mode: 'path-input',
      selectedRepo: null,
      repoInfo: null,
    }));
  }, []);

  // Global keyboard handler for navigation
  useEffect(() => {
    if (!isOpen) return;

    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (e.altKey && !e.metaKey && !e.ctrlKey) {
        if (e.code === 'Digit1') {
          if (!isAgentAvailable(effectiveAgentAvailability, 'codex')) return;
          e.preventDefault();
          handleAgentChange('codex');
          return;
        }
        if (e.code === 'Digit2') {
          if (!isAgentAvailable(effectiveAgentAvailability, 'claude')) return;
          e.preventDefault();
          handleAgentChange('claude');
          return;
        }
        if (e.code === 'Digit3') {
          if (!isAgentAvailable(effectiveAgentAvailability, 'copilot')) return;
          e.preventDefault();
          handleAgentChange('copilot');
          return;
        }
        if (e.code === 'Digit4') {
          e.preventDefault();
          setState(prev => ({
            ...prev,
            resumeEnabled: !prev.resumeEnabled,
          }));
          return;
        }
      }

      if (e.key === 'Escape') {
        e.preventDefault();
        if (state.mode === 'repo-options') {
          handleBack();
        } else {
          onClose();
        }
        return;
      }

      // Arrow keys and Enter only in path-input mode
      if (state.mode !== 'path-input') return;

      const totalItems = filteredRecent.length + fsSuggestions.length;

      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setState(prev => ({
          ...prev,
          selectedIndex: Math.min(prev.selectedIndex + 1, totalItems - 1),
          hasSelectedSinceTab: true, // Arrow navigation is intentional selection
        }));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setState(prev => ({
          ...prev,
          selectedIndex: Math.max(prev.selectedIndex - 1, 0),
          hasSelectedSinceTab: true, // Arrow navigation is intentional selection
        }));
      }
      // Note: Enter is handled by PathInput component directly
    };

    window.addEventListener('keydown', handleGlobalKeyDown);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown);
  }, [effectiveAgentAvailability, fsSuggestions, filteredRecent, handleAgentChange, handleBack, isOpen, onClose, state.mode, state.selectedIndex]);

  // Transform RepoInfo from snake_case to camelCase for RepoOptions
  const transformedRepoInfo = state.repoInfo ? {
    repo: state.repoInfo.repo,
    currentBranch: state.repoInfo.current_branch,
    currentCommitHash: state.repoInfo.current_commit_hash,
    currentCommitTime: state.repoInfo.current_commit_time,
    defaultBranch: state.repoInfo.default_branch,
    worktrees: state.repoInfo.worktrees,
    branches: state.repoInfo.branches,
    fetchedAt: state.repoInfo.fetched_at,
  } : null;

  if (!isOpen) return null;

  return (
    <div className="location-picker-overlay" onClick={onClose}>
      <div className="location-picker" onClick={(e) => e.stopPropagation()}>
        <div className="picker-agent-bar">
          <div className="picker-agent-label">SESSION AGENT</div>
          <div className="picker-agent-controls">
            <div className="agent-toggle" role="radiogroup" aria-label="Session agent">
              <button
                type="button"
                className={`agent-option ${state.agent === 'codex' ? 'active' : ''}`}
                onClick={() => handleAgentChange('codex')}
                role="radio"
                aria-checked={state.agent === 'codex'}
                disabled={!effectiveAgentAvailability.codex}
                title={!effectiveAgentAvailability.codex ? 'Codex CLI not found in PATH' : undefined}
              >
                <span className="agent-option-name">Codex</span>
                <kbd className="agent-shortcut">⌥1</kbd>
              </button>
              <button
                type="button"
                className={`agent-option ${state.agent === 'claude' ? 'active' : ''}`}
                onClick={() => handleAgentChange('claude')}
                role="radio"
                aria-checked={state.agent === 'claude'}
                disabled={!effectiveAgentAvailability.claude}
                title={!effectiveAgentAvailability.claude ? 'Claude CLI not found in PATH' : undefined}
              >
                <span className="agent-option-name">Claude</span>
                <kbd className="agent-shortcut">⌥2</kbd>
              </button>
              <button
                type="button"
                className={`agent-option ${state.agent === 'copilot' ? 'active' : ''}`}
                onClick={() => handleAgentChange('copilot')}
                role="radio"
                aria-checked={state.agent === 'copilot'}
                disabled={!effectiveAgentAvailability.copilot}
                title={!effectiveAgentAvailability.copilot ? 'Copilot CLI not found in PATH' : undefined}
              >
                <span className="agent-option-name">Copilot</span>
                <kbd className="agent-shortcut">⌥3</kbd>
              </button>
            </div>
            <button
              type="button"
              className={`resume-toggle ${state.resumeEnabled ? 'active' : ''}`}
              onClick={handleResumeToggle}
              aria-pressed={state.resumeEnabled}
            >
              <span className="resume-toggle-label">Resume</span>
              <kbd className="agent-shortcut">⌥4</kbd>
            </button>
          </div>
        </div>
        {!hasAvailableAgents && (
          <div className="picker-agent-warning">No supported agent CLIs found in PATH.</div>
        )}
        {state.mode === 'path-input' ? (
          <>
            <div className="picker-header">
              <div className="picker-title">New Session Location</div>
              <PathInput
                value={state.inputValue}
                onChange={handlePathInputChange}
                onTabComplete={handleTabComplete}
                onSelect={handlePathInputSelect}
                ghostText={ghostText}
                hasSelectedSinceTab={state.hasSelectedSinceTab}
                placeholder="Type path (e.g., ~/projects) or search..."
              />
              {currentDir && (
                <div className="picker-breadcrumb">
                  <span className="picker-breadcrumb-label">Browsing:</span>
                  <span className="picker-breadcrumb-path">{currentDir}</span>
                </div>
              )}
            </div>

            <div className="picker-results">
              {/* Recent locations section */}
              {filteredRecent.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">RECENT</div>
                  {filteredRecent.slice(0, MAX_RECENT_LOCATIONS).map((loc, index) => (
                    <div
                      key={loc.path}
                      className={`picker-item ${index === state.selectedIndex ? 'selected' : ''}`}
                      data-index={index}
                      onClick={() => handleSelect(loc.path)}
                      onMouseEnter={() => setState(prev => ({ ...prev, selectedIndex: index }))}
                    >
                      <div className="picker-icon">🕐</div>
                      <div className="picker-info">
                        <div className="picker-name">{loc.label}</div>
                        <div className="picker-path">{loc.path.replace(state.homePath, '~')}</div>
                      </div>
                    </div>
                  ))}
                </div>
              )}

              {/* Filesystem suggestions */}
              {fsSuggestions.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">DIRECTORIES</div>
                  {fsSuggestions.map((item: { name: string; path: string }, index: number) => {
                    const globalIndex = filteredRecent.length + index;
                    return (
                      <div
                        key={item.path}
                        className={`picker-item ${globalIndex === state.selectedIndex ? 'selected' : ''}`}
                        data-index={globalIndex}
                        onClick={() => handleSelect(item.path)}
                        onMouseEnter={() => setState(prev => ({ ...prev, selectedIndex: globalIndex }))}
                      >
                        <div className="picker-icon">📁</div>
                        <div className="picker-info">
                          <div className="picker-name">{item.name}</div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}

              {/* Empty state */}
              {fsSuggestions.length === 0 && filteredRecent.length === 0 && (
                <div className="picker-empty">
                  {state.inputValue
                    ? 'No matches. Press Enter to use path directly.'
                    : 'Type a path to browse directories'}
                </div>
              )}
            </div>

            <div className="picker-footer">
              <span className="shortcut"><kbd>↑↓</kbd> navigate</span>
              <span className="shortcut"><kbd>Tab</kbd> autocomplete</span>
              <span className="shortcut"><kbd>Enter</kbd> select</span>
              <span className="shortcut"><kbd>Esc</kbd> cancel</span>
            </div>
          </>
        ) : state.mode === 'repo-options' && transformedRepoInfo ? (
          <RepoOptions
            repoInfo={transformedRepoInfo}
            onSelectMainRepo={handleSelectMainRepo}
            onSelectWorktree={handleSelectWorktree}
            onSelectBranch={handleSelectBranch}
            onCreateWorktree={handleCreateWorktree}
            onDeleteWorktree={onDeleteWorktree ? async (path) => {
              await onDeleteWorktree(path);
              // Refresh repo info after delete
              if (state.selectedRepo && onGetRepoInfo) {
                const result = await onGetRepoInfo(state.selectedRepo);
                if (result.success && result.info) {
                  setState(prev => ({ ...prev, repoInfo: result.info || null }));
                }
              }
            } : undefined}
            onDeleteBranch={onDeleteBranch ? async (branch) => {
              if (state.selectedRepo) {
                await onDeleteBranch(state.selectedRepo, branch, true);
                // Refresh repo info after delete
                if (onGetRepoInfo) {
                  const result = await onGetRepoInfo(state.selectedRepo);
                  if (result.success && result.info) {
                    setState(prev => ({ ...prev, repoInfo: result.info || null }));
                  }
                }
              }
            } : undefined}
            onError={onError}
            onRefresh={handleRefresh}
            onBack={handleBack}
            refreshing={state.refreshing}
          />
        ) : null}
      </div>
    </div>
  );
}
