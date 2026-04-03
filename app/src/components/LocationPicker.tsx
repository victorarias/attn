import { useState, useEffect, useCallback, useMemo } from 'react';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import { PathInput } from './NewSessionDialog/PathInput';
import { RepoOptions } from './NewSessionDialog/RepoOptions';
import type { BrowseDirectoryResult, DaemonEndpoint, InspectPathResult, RecentLocation } from '../hooks/useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import { useSettings } from '../contexts/SettingsContext';
import {
  agentLabel,
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
  onSelect: (path: string, agent: SessionAgent, endpointId?: string) => void;
  onGetRecentLocations?: (endpointId?: string) => Promise<{ locations: RecentLocation[]; home_path?: string }>;
  onBrowseDirectory?: (inputPath: string, endpointId?: string) => Promise<BrowseDirectoryResult>;
  onInspectPath?: (path: string, endpointId?: string) => Promise<InspectPathResult>;
  onGetRepoInfo?: (mainRepo: string, endpointId?: string) => Promise<{ success: boolean; info?: RepoInfo; error?: string }>;
  onCreateWorktree?: (mainRepo: string, branch: string, path?: string, startingFrom?: string, endpointId?: string) => Promise<{ success: boolean; path?: string }>;
  onDeleteWorktree?: (path: string, endpointId?: string) => Promise<{ success: boolean; error?: string }>;
  onDeleteBranch?: (mainRepo: string, branch: string, force?: boolean, endpointId?: string) => Promise<{ success: boolean; error?: string }>;
  onError?: (message: string) => void;
  projectsDirectory?: string;
  agentAvailability?: AgentAvailability;
  endpoints?: DaemonEndpoint[];
}

const MAX_RECENT_LOCATIONS = 10;
const SESSION_AGENT_KEY = 'new_session_agent';
const LOCAL_TARGET = '__local__';
const TARGET_SHORTCUT_KEYS = ['q', 'w', 'e', 'r', 't', 'a', 's', 'd', 'f', 'g', 'z', 'x', 'c', 'v', 'b'];
const TARGET_SHORTCUT_CODES = ['KeyQ', 'KeyW', 'KeyE', 'KeyR', 'KeyT', 'KeyA', 'KeyS', 'KeyD', 'KeyF', 'KeyG', 'KeyZ', 'KeyX', 'KeyC', 'KeyV', 'KeyB'];

const DEFAULT_AGENT_AVAILABILITY: AgentAvailability = {
  codex: true,
  claude: true,
  copilot: true,
  pi: false,
};
const FIXED_AGENT_ORDER: SessionAgent[] = ['claude', 'codex', 'copilot', 'pi'];

const normalizeAgent = (value?: string): SessionAgent | null => {
  if (!value) return null;
  const lower = value.toLowerCase().trim();
  return lower || null;
};

function toDisplayPath(path: string, homePath: string): string {
  if (!path) {
    return '';
  }
  if (path.startsWith(homePath + '/')) {
    return '~' + path.slice(homePath.length);
  }
  if (path === homePath) {
    return '~';
  }
  return path;
}

function buildInitialInput(path: string | undefined, homePath: string): string {
  if (!path) {
    return '';
  }
  const displayPath = homePath ? toDisplayPath(path, homePath) : path;
  return displayPath.endsWith('/') ? displayPath : `${displayPath}/`;
}

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
  hasSelectedSinceTab: boolean;
  agent: SessionAgent;
  endpointId: string;
}

export function LocationPicker({
  isOpen,
  onClose,
  onSelect,
  onGetRecentLocations,
  onBrowseDirectory,
  onInspectPath,
  onGetRepoInfo,
  onCreateWorktree,
  onDeleteWorktree,
  onDeleteBranch,
  onError,
  projectsDirectory,
  agentAvailability,
  endpoints = [],
}: LocationPickerProps) {
  const { settings, setSetting } = useSettings();
  const effectiveAgentAvailability = agentAvailability || DEFAULT_AGENT_AVAILABILITY;
  const hasAvailableAgents = hasAnyAvailableAgents(effectiveAgentAvailability);
  const noAgentsMessage = 'No supported agent CLI found in PATH.';
  const [state, setState] = useState<State>({
    mode: 'path-input',
    inputValue: '',
    selectedIndex: -1,
    selectedRepo: null,
    repoInfo: null,
    recentLocations: [],
    homePath: '',
    refreshing: false,
    hasSelectedSinceTab: false,
    agent: 'claude',
    endpointId: LOCAL_TARGET,
  });

  const isLocalTarget = state.endpointId === LOCAL_TARGET;
  const { suggestions: fsSuggestions, currentDir, homePath: suggestedHomePath } = useFilesystemSuggestions(
    state.inputValue,
    isLocalTarget ? undefined : state.endpointId,
    onBrowseDirectory,
  );
  const availableEndpoints = useMemo(
    () => endpoints.filter((endpoint) => endpoint.enabled !== false),
    [endpoints],
  );
  const selectedEndpoint = useMemo(
    () => availableEndpoints.find((endpoint) => endpoint.id === state.endpointId),
    [availableEndpoints, state.endpointId],
  );
  const selectedEndpointId = selectedEndpoint?.id;
  const selectedProjectsDirectory = isLocalTarget
    ? projectsDirectory
    : selectedEndpoint?.capabilities?.projects_directory;
  const selectableTargets = useMemo(
    () => [
      { id: LOCAL_TARGET, connected: true },
      ...availableEndpoints.map((endpoint) => ({
        id: endpoint.id,
        connected: endpoint.status === 'connected',
      })),
    ],
    [availableEndpoints],
  );
  const targetShortcutByID = useMemo(() => {
    const shortcuts = new Map<string, string>();
    selectableTargets.forEach((target, index) => {
      const shortcutKey = TARGET_SHORTCUT_KEYS[index];
      if (!shortcutKey) {
        return;
      }
      shortcuts.set(target.id, shortcutKey);
    });
    return shortcuts;
  }, [selectableTargets]);
  const targetShortcutIDByCode = useMemo(() => {
    const shortcuts = new Map<string, string>();
    selectableTargets.forEach((target, index) => {
      const shortcutCode = TARGET_SHORTCUT_CODES[index];
      if (!shortcutCode) {
        return;
      }
      shortcuts.set(shortcutCode, target.id);
    });
    return shortcuts;
  }, [selectableTargets]);

  useEffect(() => {
    if (isOpen && onGetRecentLocations) {
      onGetRecentLocations(selectedEndpointId)
        .then((result) => {
          setState((prev) => ({
            ...prev,
            recentLocations: result.locations,
            homePath: result.home_path || prev.homePath,
          }));
        })
        .catch((err) => {
          console.error('[LocationPicker] Failed to fetch recent locations:', err);
          setState((prev) => ({ ...prev, recentLocations: [] }));
        });
    } else if (isOpen) {
      setState((prev) => ({ ...prev, recentLocations: [] }));
    }
  }, [isOpen, onGetRecentLocations, selectedEndpointId]);

  useEffect(() => {
    if (!suggestedHomePath) {
      return;
    }
    setState((prev) => (prev.homePath === suggestedHomePath ? prev : { ...prev, homePath: suggestedHomePath }));
  }, [suggestedHomePath]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const defaultPath = selectedProjectsDirectory || (!isLocalTarget ? '~' : undefined);
    const initialValue = buildInitialInput(defaultPath, state.homePath);
    setState((prev) => ({
      ...prev,
      mode: 'path-input',
      inputValue: initialValue,
      selectedIndex: -1,
      selectedRepo: null,
      repoInfo: null,
      refreshing: false,
      hasSelectedSinceTab: false,
    }));
  }, [isLocalTarget, isOpen, selectedProjectsDirectory]);

  const orderedAgentList = useMemo(() => {
    const ordered: SessionAgent[] = [];
    const seen = new Set<SessionAgent>();
    const push = (agent: SessionAgent) => {
      if (seen.has(agent)) return;
      seen.add(agent);
      ordered.push(agent);
    };
    for (const agent of FIXED_AGENT_ORDER) {
      push(agent);
    }
    const dynamicAgents = Object.keys(effectiveAgentAvailability) as SessionAgent[];
    dynamicAgents.sort((a, b) => a.localeCompare(b));
    for (const agent of dynamicAgents) {
      push(agent);
    }
    return ordered;
  }, [effectiveAgentAvailability]);

  const agentShortcutByName = useMemo(() => {
    const shortcuts = new Map<SessionAgent, number>();
    orderedAgentList.forEach((agent, index) => {
      shortcuts.set(agent, index + 1);
    });
    return shortcuts;
  }, [orderedAgentList]);

  const savedAgent = normalizeAgent(settings[SESSION_AGENT_KEY]);

  useEffect(() => {
    if (!savedAgent) return;
    const resolvedSavedAgent = resolvePreferredAgent(savedAgent, effectiveAgentAvailability, 'codex');
    setState((prev) => (prev.agent === resolvedSavedAgent ? prev : { ...prev, agent: resolvedSavedAgent }));
  }, [effectiveAgentAvailability, savedAgent]);

  useEffect(() => {
    const resolvedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
    if (resolvedAgent === state.agent) {
      return;
    }
    setState((prev) => ({ ...prev, agent: resolvedAgent }));
  }, [effectiveAgentAvailability, state.agent]);

  const expandedInput = state.inputValue.startsWith('~')
    ? (state.homePath ? state.inputValue.replace('~', state.homePath) : state.inputValue)
    : state.inputValue;

  const filteredRecent = expandedInput
    ? state.recentLocations.filter(
        (loc) =>
          loc.label.toLowerCase().includes(expandedInput.toLowerCase()) ||
          loc.path.toLowerCase().includes(expandedInput.toLowerCase()),
      )
    : state.recentLocations;

  const visibleRecent = filteredRecent;
  const visibleSuggestions = fsSuggestions;

  const getSelectedPath = () => {
    if (state.selectedIndex === -1) {
      return visibleSuggestions[0]?.path || visibleRecent[0]?.path || '';
    }
    if (state.selectedIndex < visibleRecent.length) {
      return visibleRecent[state.selectedIndex]?.path || '';
    }
    const fsIndex = state.selectedIndex - visibleRecent.length;
    return visibleSuggestions[fsIndex]?.path || '';
  };
  const selectedPath = getSelectedPath();
  const ghostText = state.homePath ? toDisplayPath(selectedPath, state.homePath) : selectedPath;

  useEffect(() => {
    setState((prev) => ({ ...prev, selectedIndex: -1 }));
  }, [state.inputValue]);

  useEffect(() => {
    if (state.selectedIndex >= 0) {
      const el = document.querySelector(`[data-index="${state.selectedIndex}"]`);
      el?.scrollIntoView({ block: 'nearest' });
    }
  }, [state.selectedIndex]);

  const handleSelect = useCallback(async (rawPath: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (!onInspectPath) {
      onError?.('Path inspection is not available.');
      return;
    }
    const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');

    try {
      const inspected = await onInspectPath(rawPath.replace(/\/$/, ''), selectedEndpointId);
      const inspection = inspected.inspection;
      if (!inspection?.exists || !inspection.is_directory) {
        onError?.(`Directory not found: ${rawPath.replace(/\/$/, '')}`);
        return;
      }

      const path = inspection.resolved_path;
      const repoRoot = inspection.repo_root;
      if (repoRoot && onGetRepoInfo) {
        const result = await onGetRepoInfo(repoRoot, selectedEndpointId);
        if (result.success && result.info) {
          setState((prev) => ({
            ...prev,
            mode: 'repo-options',
            selectedRepo: repoRoot,
            repoInfo: result.info || null,
          }));
        } else {
          onSelect(path, selectedAgent, selectedEndpointId);
          onClose();
        }
        return;
      }

      onSelect(path, selectedAgent, selectedEndpointId);
      onClose();
    } catch (err) {
      console.log('[LocationPicker] inspect path error:', err);
      onError?.(err instanceof Error ? err.message : String(err));
      return;
    }
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onGetRepoInfo, onInspectPath, onSelect, selectedEndpointId, state.agent]);

  const handleAgentChange = useCallback((agent: SessionAgent) => {
    if (!isAgentAvailable(effectiveAgentAvailability, agent)) return;
    setState((prev) => ({ ...prev, agent }));
    setSetting(SESSION_AGENT_KEY, agent);
  }, [effectiveAgentAvailability, setSetting]);

  const handleEndpointChange = useCallback((endpointId: string) => {
    const nextEndpoint = endpointId === LOCAL_TARGET
      ? null
      : availableEndpoints.find((endpoint) => endpoint.id === endpointId);
    const nextProjectsDirectory = endpointId === LOCAL_TARGET
      ? projectsDirectory
      : nextEndpoint?.capabilities?.projects_directory;
    const defaultPath = nextProjectsDirectory || (endpointId === LOCAL_TARGET ? undefined : '~');
    setState((prev) => ({
      ...prev,
      endpointId,
      mode: 'path-input',
      inputValue: buildInitialInput(defaultPath, prev.homePath),
      selectedIndex: -1,
      selectedRepo: null,
      repoInfo: null,
      recentLocations: [],
      hasSelectedSinceTab: false,
    }));
  }, [availableEndpoints, projectsDirectory]);

  const handlePathInputChange = useCallback((value: string) => {
    setState((prev) => ({ ...prev, inputValue: value, hasSelectedSinceTab: true }));
  }, []);

  const handleTabComplete = useCallback((value: string) => {
    setState((prev) => ({ ...prev, inputValue: value, hasSelectedSinceTab: false }));
  }, []);

  const handlePathInputSelect = useCallback((path: string) => {
    handleSelect(path);
  }, [handleSelect]);

  const handleSelectMainRepo = useCallback(() => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (state.selectedRepo) {
      const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
      onSelect(state.selectedRepo, selectedAgent, selectedEndpointId);
      onClose();
    }
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onSelect, selectedEndpointId, state.agent, state.selectedRepo]);

  const handleSelectWorktree = useCallback((path: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
    onSelect(path, selectedAgent, selectedEndpointId);
    onClose();
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onSelect, selectedEndpointId, state.agent]);

  const handleSelectBranch = useCallback((_branch: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (state.selectedRepo) {
      const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
      onSelect(state.selectedRepo, selectedAgent, selectedEndpointId);
      onClose();
    }
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onError, onSelect, selectedEndpointId, state.agent, state.selectedRepo]);

  const handleCreateWorktree = useCallback(async (branchName: string, startingFrom: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (!state.selectedRepo || !onCreateWorktree) return;

    try {
      const result = await onCreateWorktree(state.selectedRepo, branchName, undefined, startingFrom, selectedEndpointId);
      if (result.success && result.path) {
        const selectedAgent = resolvePreferredAgent(state.agent, effectiveAgentAvailability, 'codex');
        onSelect(result.path, selectedAgent, selectedEndpointId);
        onClose();
      }
    } catch (err) {
      console.error('[LocationPicker] Failed to create worktree:', err);
    }
  }, [effectiveAgentAvailability, hasAvailableAgents, onClose, onCreateWorktree, onError, onSelect, selectedEndpointId, state.agent, state.selectedRepo]);

  const handleRefresh = useCallback(async () => {
    if (!state.selectedRepo || !onGetRepoInfo || state.refreshing) return;

    setState((prev) => ({ ...prev, refreshing: true }));
    try {
      const result = await onGetRepoInfo(state.selectedRepo, selectedEndpointId);
      if (result.success && result.info) {
        setState((prev) => ({
          ...prev,
          repoInfo: result.info || null,
          refreshing: false,
        }));
      }
    } catch (err) {
      console.error('[LocationPicker] Failed to refresh repo info:', err);
    } finally {
      setState((prev) => ({ ...prev, refreshing: false }));
    }
  }, [onGetRepoInfo, state.refreshing, state.selectedRepo]);

  const handleBack = useCallback(() => {
    setState((prev) => ({
      ...prev,
      mode: 'path-input',
      selectedRepo: null,
      repoInfo: null,
    }));
  }, []);

  useEffect(() => {
    if (!isOpen) return;

    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (e.altKey && !e.metaKey && !e.ctrlKey) {
        const targetID = targetShortcutIDByCode.get(e.code);
        if (targetID) {
          const target = selectableTargets.find((candidate) => candidate.id === targetID);
          e.preventDefault();
          e.stopPropagation();
          if (!target || !target.connected) {
            return;
          }
          handleEndpointChange(target.id);
          return;
        }

        const digitMatch = /^Digit([1-9])$/.exec(e.code);
        if (digitMatch) {
          const idx = Number(digitMatch[1]) - 1;
          if (idx < orderedAgentList.length) {
            const targetAgent = orderedAgentList[idx];
            e.preventDefault();
            e.stopPropagation();
            if (!isAgentAvailable(effectiveAgentAvailability, targetAgent)) {
              return;
            }
            handleAgentChange(targetAgent);
            return;
          }
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

      if (state.mode !== 'path-input') return;

      const totalItems = visibleRecent.length + visibleSuggestions.length;
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setState((prev) => ({
          ...prev,
          selectedIndex: Math.min(prev.selectedIndex + 1, totalItems - 1),
          hasSelectedSinceTab: true,
        }));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setState((prev) => ({
          ...prev,
          selectedIndex: Math.max(prev.selectedIndex - 1, 0),
          hasSelectedSinceTab: true,
        }));
      }
    };

    window.addEventListener('keydown', handleGlobalKeyDown, true);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown, true);
  }, [effectiveAgentAvailability, handleAgentChange, handleBack, handleEndpointChange, isOpen, onClose, orderedAgentList, selectableTargets, state.mode, targetShortcutIDByCode, visibleRecent.length, visibleSuggestions.length]);

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
    <div className="location-picker-overlay" data-testid="location-picker-overlay" onClick={onClose}>
      <div className="location-picker" data-testid="location-picker" onClick={(e) => e.stopPropagation()}>
        <div className="picker-agent-bar">
          <div className="picker-agent-label">SESSION AGENT</div>
          <div className="picker-agent-controls">
            <div className="agent-toggle" role="radiogroup" aria-label="Session agent">
              {orderedAgentList.map((agent) => {
                const available = isAgentAvailable(effectiveAgentAvailability, agent);
                const shortcutNumber = agentShortcutByName.get(agent);
                const shortcut = shortcutNumber && shortcutNumber <= 9 ? `⌥${shortcutNumber}` : null;
                return (
                  <button
                    key={agent}
                    type="button"
                    className={`agent-option ${state.agent === agent ? 'active' : ''}`}
                    onClick={() => handleAgentChange(agent)}
                    role="radio"
                    aria-checked={state.agent === agent}
                    disabled={!available}
                    title={!available ? `${agentLabel(agent)} CLI not found in PATH` : undefined}
                  >
                    <span className="agent-option-name">{agentLabel(agent)}</span>
                    {available && shortcut && <kbd className="agent-shortcut">{shortcut}</kbd>}
                  </button>
                );
              })}
            </div>
          </div>
        </div>
        <div className="picker-endpoint-bar">
          <div className="picker-agent-label">SESSION TARGET</div>
          <div className="picker-endpoint-controls" role="radiogroup" aria-label="Session target">
            <button
              type="button"
              className={`endpoint-option ${isLocalTarget ? 'active' : ''}`}
              data-testid="location-picker-target-local"
              onClick={() => handleEndpointChange(LOCAL_TARGET)}
              role="radio"
              aria-checked={isLocalTarget}
            >
              <span className="endpoint-option-name">Local</span>
              <div className="endpoint-option-footer">
                <span className="endpoint-option-meta">this machine</span>
                <kbd className="agent-shortcut endpoint-shortcut">{`⌥${targetShortcutByID.get(LOCAL_TARGET)?.toUpperCase()}`}</kbd>
              </div>
            </button>
            {availableEndpoints.map((endpoint) => {
              const connected = endpoint.status === 'connected';
              const shortcutKey = targetShortcutByID.get(endpoint.id);
              return (
                <button
                  key={endpoint.id}
                  type="button"
                  className={`endpoint-option ${state.endpointId === endpoint.id ? 'active' : ''}`}
                  data-testid={`location-picker-target-${endpoint.id}`}
                  data-endpoint-id={endpoint.id}
                  onClick={() => handleEndpointChange(endpoint.id)}
                  role="radio"
                  aria-checked={state.endpointId === endpoint.id}
                  disabled={!connected}
                  title={!connected ? `${endpoint.name} is ${endpoint.status}` : undefined}
                >
                  <span className="endpoint-option-name">{endpoint.name}</span>
                  <div className="endpoint-option-footer">
                    <span className={`endpoint-option-meta status-${endpoint.status}`}>{endpoint.status}</span>
                    {connected && shortcutKey && <kbd className="agent-shortcut endpoint-shortcut">{`⌥${shortcutKey.toUpperCase()}`}</kbd>}
                  </div>
                </button>
              );
            })}
          </div>
        </div>
        {!hasAvailableAgents && (
          <div className="picker-agent-warning">{noAgentsMessage}</div>
        )}
        {state.mode === 'path-input' ? (
          <>
            <div className="picker-header">
              <div className="picker-title" data-testid="location-picker-title">
                New Session Location
              </div>
              <PathInput
                value={state.inputValue}
                onChange={handlePathInputChange}
                onTabComplete={handleTabComplete}
                onSelect={handlePathInputSelect}
                ghostText={ghostText}
                hasSelectedSinceTab={state.hasSelectedSinceTab}
                placeholder={isLocalTarget ? 'Type path (e.g., ~/projects) or search...' : `Type path on ${selectedEndpoint?.name || 'remote host'} (e.g., ~/projects/repo)`}
              />
              {currentDir && (
                <div className="picker-breadcrumb" data-testid="location-picker-breadcrumb">
                  <span className="picker-breadcrumb-label">Browsing:</span>
                  <span className="picker-breadcrumb-path" data-testid="location-picker-breadcrumb-path">{currentDir}</span>
                </div>
              )}
            </div>

            <div className="picker-results">
              {visibleRecent.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">RECENT</div>
                  {visibleRecent.slice(0, MAX_RECENT_LOCATIONS).map((loc, index) => (
                    <div
                      key={loc.path}
                      className={`picker-item ${index === state.selectedIndex ? 'selected' : ''}`}
                      data-testid={`location-picker-item-${index}`}
                      data-index={index}
                      data-kind="recent"
                      data-path={loc.path}
                      onClick={() => handleSelect(loc.path)}
                      onMouseEnter={() => setState((prev) => ({ ...prev, selectedIndex: index }))}
                    >
                      <div className="picker-icon">🕐</div>
                      <div className="picker-info">
                        <div className="picker-name">{loc.label}</div>
                        <div className="picker-path">{state.homePath ? toDisplayPath(loc.path, state.homePath) : loc.path}</div>
                      </div>
                    </div>
                  ))}
                </div>
              )}

              {visibleSuggestions.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">DIRECTORIES</div>
                  {visibleSuggestions.map((item, index) => {
                    const globalIndex = visibleRecent.length + index;
                    return (
                      <div
                        key={item.path}
                        className={`picker-item ${globalIndex === state.selectedIndex ? 'selected' : ''}`}
                        data-testid={`location-picker-item-${globalIndex}`}
                        data-index={globalIndex}
                        data-kind="directory"
                        data-path={item.path}
                        onClick={() => handleSelect(item.path)}
                        onMouseEnter={() => setState((prev) => ({ ...prev, selectedIndex: globalIndex }))}
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

              {visibleSuggestions.length === 0 && visibleRecent.length === 0 && (
                <div className="picker-empty" data-testid="location-picker-empty">
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
              await onDeleteWorktree(path, selectedEndpointId);
              if (state.selectedRepo && onGetRepoInfo) {
                const result = await onGetRepoInfo(state.selectedRepo, selectedEndpointId);
                if (result.success && result.info) {
                  setState((prev) => ({ ...prev, repoInfo: result.info || null }));
                }
              }
            } : undefined}
            onDeleteBranch={onDeleteBranch ? async (branch) => {
              if (state.selectedRepo) {
                await onDeleteBranch(state.selectedRepo, branch, true, selectedEndpointId);
                if (onGetRepoInfo) {
                  const result = await onGetRepoInfo(state.selectedRepo, selectedEndpointId);
                  if (result.success && result.info) {
                    setState((prev) => ({ ...prev, repoInfo: result.info || null }));
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
