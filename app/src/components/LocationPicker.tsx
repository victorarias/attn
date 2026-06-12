import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useFilesystemSuggestions } from '../hooks/useFilesystemSuggestions';
import { PathInput } from './NewSessionDialog/PathInput';
import { RepoOptions } from './NewSessionDialog/RepoOptions';
import type { BrowseDirectoryResult, DaemonEndpoint, InspectPathResult, RecentLocation } from '../hooks/useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import { useSettings } from '../contexts/SettingsContext';
import {
  agentLabel,
  type AgentAvailability,
  getAgentCapabilities,
  hasAnyAvailableAgents,
  isAgentAvailable,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import {
  buildInitialPickerInput,
  expandDisplayPath,
  normalizePickerPath,
  pathBasename,
  toDisplayPath,
} from '../utils/locationPickerPaths';
import './LocationPicker.css';

interface BackendRepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: Array<{ path: string; branch: string }>;
}

interface RepoInfo {
  repo: string;
  currentBranch: string;
  currentCommitHash: string;
  currentCommitTime: string;
  defaultBranch: string;
  worktrees: Array<{ path: string; branch: string }>;
}

interface PickerTarget {
  id: string;
  endpointId?: string;
  name: string;
  connected: boolean;
  metaLabel: string;
  metaClassName?: string;
  projectsDirectory?: string;
  placeholder: string;
  daemonInstanceId?: string;
  agentsAvailable?: string[];
}

interface PathSelectableItem {
  kind: 'recent' | 'directory';
  key: string;
  path: string;
}

interface LocationPickerProps {
  isOpen: boolean;
  purpose?: 'workspace' | 'session';
  onClose: () => void;
  onSelect: (path: string, agent: SessionAgent, endpointId?: string, yoloMode?: boolean) => void;
  onGetRecentLocations?: (endpointId?: string) => Promise<{ locations: RecentLocation[]; home_path?: string }>;
  onBrowseDirectory?: (inputPath: string, endpointId?: string) => Promise<BrowseDirectoryResult>;
  onInspectPath?: (path: string, endpointId?: string) => Promise<InspectPathResult>;
  onGetRepoInfo?: (mainRepo: string, endpointId?: string) => Promise<{ success: boolean; info?: BackendRepoInfo; error?: string }>;
  onCreateWorktree?: (mainRepo: string, branch: string, path?: string, startingFrom?: string, endpointId?: string) => Promise<{ success: boolean; path?: string; error?: string }>;
  onCreateWorktreeSession?: (mainRepo: string, branch: string, startingFrom: string, endpointId: string | undefined, agent: SessionAgent, yoloMode: boolean) => void;
  onDeleteWorktree?: (path: string, endpointId?: string, options?: { force?: boolean }) => Promise<{ success: boolean; error?: string }>;
  onError?: (message: string) => void;
  projectsDirectory?: string;
  agentAvailability?: AgentAvailability;
  endpoints?: DaemonEndpoint[];
}

const MAX_RECENT_LOCATIONS = 10;
const SESSION_AGENT_KEY = 'new_session_agent';
const SESSION_YOLO_KEY = 'new_session_yolo';
const LOCAL_TARGET = '__local__';
const TERMINAL_AGENT: SessionAgent = 'shell';
const TARGET_SHORTCUT_KEYS = ['q', 'w', 'e', 'r', 'a', 's', 'd', 'f', 'g', 'z', 'x', 'c', 'v', 'b'];
const TARGET_SHORTCUT_CODES = ['KeyQ', 'KeyW', 'KeyE', 'KeyR', 'KeyA', 'KeyS', 'KeyD', 'KeyF', 'KeyG', 'KeyZ', 'KeyX', 'KeyC', 'KeyV', 'KeyB'];

const DEFAULT_AGENT_AVAILABILITY: AgentAvailability = {
  shell: true,
  codex: true,
  claude: true,
  copilot: true,
};
const FIXED_AGENT_ORDER: SessionAgent[] = [TERMINAL_AGENT, 'claude', 'codex', 'copilot'];

const normalizeAgent = (value?: string): SessionAgent | null => {
  if (!value) return null;
  const lower = value.toLowerCase().trim();
  return lower || null;
};

function pathStartsWith(candidate: string, input: string): boolean {
  const normalizedCandidate = candidate.toLowerCase();
  const normalizedInput = input.toLowerCase();
  return normalizedCandidate.startsWith(normalizedInput);
}

function parseBooleanSetting(value?: string): boolean | null {
  if (typeof value !== 'string') {
    return null;
  }
  const normalized = value.trim().toLowerCase();
  if (normalized === 'true') {
    return true;
  }
  if (normalized === 'false') {
    return false;
  }
  return null;
}

function yoloSettingKeys(targetId: string, daemonInstanceId?: string): string[] {
  if (targetId === LOCAL_TARGET) {
    return [`${SESSION_YOLO_KEY}_local`, SESSION_YOLO_KEY];
  }
  const keys: string[] = [];
  const daemonID = daemonInstanceId?.trim();
  if (daemonID) {
    keys.push(`${SESSION_YOLO_KEY}_daemon_${daemonID}`);
  }
  if (targetId.trim()) {
    keys.push(`${SESSION_YOLO_KEY}_endpoint_${targetId}`);
  }
  return keys;
}

function pickerAgentLabel(agent: SessionAgent): string {
  return agent === TERMINAL_AGENT ? 'Terminal' : agentLabel(agent);
}

function withoutTerminalAgent(availability: AgentAvailability): AgentAvailability {
  const { [TERMINAL_AGENT]: _terminal, ...rest } = availability;
  return rest;
}

function resolvePickerAgent(
  preferred: SessionAgent | undefined,
  availability: AgentAvailability,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  if (preferred === TERMINAL_AGENT && availability[TERMINAL_AGENT]) {
    return TERMINAL_AGENT;
  }
  const nonTerminalAvailability = withoutTerminalAgent(availability);
  if (hasAnyAvailableAgents(nonTerminalAvailability)) {
    return resolvePreferredAgent(preferred, nonTerminalAvailability, fallback);
  }
  return availability[TERMINAL_AGENT]
    ? TERMINAL_AGENT
    : resolvePreferredAgent(preferred, availability, fallback);
}

function initialInputPathForTarget(target: PickerTarget): string | undefined {
  if (target.projectsDirectory) {
    return target.projectsDirectory;
  }
  return target.endpointId ? '~' : undefined;
}

function toChooserRepoInfo(info: BackendRepoInfo): RepoInfo {
  return {
    repo: info.repo,
    currentBranch: info.current_branch,
    currentCommitHash: info.current_commit_hash,
    currentCommitTime: info.current_commit_time,
    defaultBranch: info.default_branch,
    worktrees: info.worktrees,
  };
}

type Mode = 'path-input' | 'repo-options';

export function LocationPicker({
  isOpen,
  purpose = 'session',
  onClose,
  onSelect,
  onGetRecentLocations,
  onBrowseDirectory,
  onInspectPath,
  onGetRepoInfo,
  onCreateWorktree,
  onCreateWorktreeSession,
  onDeleteWorktree,
  onError,
  projectsDirectory,
  agentAvailability,
  endpoints = [],
}: LocationPickerProps) {
  const { settings, setSetting } = useSettings();
  const localAgentAvailability = agentAvailability || DEFAULT_AGENT_AVAILABILITY;
  const noAgentsMessage = 'No supported agent CLI found in PATH.';
  const copy = purpose === 'workspace'
    ? {
        agentAria: 'Initial workspace session agent',
        targetAria: 'Workspace target',
        title: 'New Workspace Location',
      }
    : {
        agentAria: 'Session agent',
        targetAria: 'Session target',
        title: 'New Session Location',
      };

  const [mode, setMode] = useState<Mode>('path-input');
  const [inputValue, setInputValue] = useState('');
  const [highlightedItemKey, setHighlightedItemKey] = useState<string | null>(null);
  const [selectedPath, setSelectedPath] = useState('');
  const [recentLocations, setRecentLocations] = useState<RecentLocation[]>([]);
  const [homePath, setHomePath] = useState('');
  const [agent, setAgent] = useState<SessionAgent>('claude');
  const [targetId, setTargetId] = useState(LOCAL_TARGET);
  const [yoloMode, setYoloMode] = useState(false);
  const [repoRootPath, setRepoRootPath] = useState<string | null>(null);
  const [repoInfo, setRepoInfo] = useState<RepoInfo | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [pickerOperation, setPickerOperation] = useState<string | null>(null);
  const [hasSelectedSinceTab, setHasSelectedSinceTab] = useState(true);
  const [autoHighlight, setAutoHighlight] = useState(false);
  const autoHighlightDoneRef = useRef(false);
  const requestGenerationRef = useRef(0);

  const agentCapabilities = useMemo(() => getAgentCapabilities(settings), [settings]);
  const availableEndpoints = useMemo(
    () => endpoints.filter((endpoint) => endpoint.enabled !== false),
    [endpoints],
  );
  const selectableTargets = useMemo<PickerTarget[]>(
    () => [
      {
        id: LOCAL_TARGET,
        name: 'Local',
        connected: true,
        metaLabel: 'this machine',
        projectsDirectory,
        placeholder: 'Type path (e.g., ~/projects) or search...',
      },
      ...availableEndpoints.map((endpoint) => ({
        id: endpoint.id,
        endpointId: endpoint.id,
        name: endpoint.name,
        connected: endpoint.status === 'connected',
        metaLabel: endpoint.status,
        metaClassName: `status-${endpoint.status}`,
        projectsDirectory: endpoint.capabilities?.projects_directory,
        placeholder: `Type path on ${endpoint.name} (e.g., ~/projects/repo)`,
        daemonInstanceId: endpoint.capabilities?.daemon_instance_id,
        agentsAvailable: endpoint.capabilities?.agents_available,
      })),
    ],
    [availableEndpoints, projectsDirectory],
  );
  const selectedTarget = useMemo(
    () => selectableTargets.find((target) => target.id === targetId) || selectableTargets[0],
    [selectableTargets, targetId],
  );
  const selectedEndpointId = selectedTarget?.endpointId;
  const effectiveAgentAvailability = useMemo<AgentAvailability>(() => {
    const withTerminal = (availability: AgentAvailability): AgentAvailability => ({
      ...availability,
      [TERMINAL_AGENT]: true,
    });
    if (!selectedEndpointId || !selectedTarget.agentsAvailable) {
      return withTerminal(localAgentAvailability);
    }
    const endpointAvailability = selectedTarget.agentsAvailable.reduce<AgentAvailability>((availability, candidate) => {
      const normalized = normalizeAgent(candidate);
      if (normalized) {
        availability[normalized] = true;
      }
      return availability;
    }, {});
    return withTerminal(endpointAvailability);
  }, [localAgentAvailability, selectedEndpointId, selectedTarget.agentsAvailable]);
  const hasAvailableAgents = hasAnyAvailableAgents(effectiveAgentAvailability);
  const yoloKeys = useMemo(
    () => yoloSettingKeys(selectedTarget.id, selectedTarget.daemonInstanceId),
    [selectedTarget.daemonInstanceId, selectedTarget.id],
  );
  const yoloSettingKey = yoloKeys[0];
  const savedYoloMode = useMemo(() => {
    for (const key of yoloKeys) {
      const parsed = parseBooleanSetting(settings[key]);
      if (parsed != null) {
        return parsed;
      }
    }
    return false;
  }, [settings, yoloKeys]);

  const targetShortcutByID = useMemo(() => {
    const shortcuts = new Map<string, string>();
    selectableTargets.forEach((target, index) => {
      const shortcutKey = TARGET_SHORTCUT_KEYS[index];
      if (shortcutKey) {
        shortcuts.set(target.id, shortcutKey);
      }
    });
    return shortcuts;
  }, [selectableTargets]);
  const targetShortcutIDByCode = useMemo(() => {
    const shortcuts = new Map<string, string>();
    selectableTargets.forEach((target, index) => {
      const shortcutCode = TARGET_SHORTCUT_CODES[index];
      if (shortcutCode) {
        shortcuts.set(shortcutCode, target.id);
      }
    });
    return shortcuts;
  }, [selectableTargets]);

  const handleHomePathChange = useCallback((nextHomePath: string) => {
    setHomePath((prev) => (prev === nextHomePath ? prev : nextHomePath));
  }, []);

  const { suggestions: fsSuggestions, currentDir } = useFilesystemSuggestions(
    inputValue,
    selectedEndpointId,
    onBrowseDirectory,
    homePath,
    handleHomePathChange,
    isOpen,
  );

  const orderedAgentList = useMemo(() => {
    const ordered: SessionAgent[] = [];
    const seen = new Set<SessionAgent>();
    const push = (candidate: SessionAgent) => {
      if (seen.has(candidate)) return;
      seen.add(candidate);
      ordered.push(candidate);
    };
    for (const candidate of FIXED_AGENT_ORDER) {
      push(candidate);
    }
    const dynamicAgents = Object.keys(effectiveAgentAvailability) as SessionAgent[];
    dynamicAgents.sort((a, b) => a.localeCompare(b));
    for (const candidate of dynamicAgents) {
      push(candidate);
    }
    return ordered;
  }, [effectiveAgentAvailability]);
  const agentShortcutByName = useMemo(() => {
    const shortcuts = new Map<SessionAgent, number>();
    orderedAgentList.filter((candidate) => candidate !== TERMINAL_AGENT).forEach((candidate, index) => {
      shortcuts.set(candidate, index + 1);
    });
    return shortcuts;
  }, [orderedAgentList]);

  const savedAgent = normalizeAgent(settings[SESSION_AGENT_KEY]);
  const yoloSupported = Boolean(agentCapabilities[agent]?.yolo);

  const invalidateRequestGeneration = useCallback(() => {
    requestGenerationRef.current += 1;
  }, []);

  const beginRequestGeneration = useCallback(() => {
    const nextGeneration = requestGenerationRef.current + 1;
    requestGenerationRef.current = nextGeneration;
    return nextGeneration;
  }, []);

  const isRequestCurrent = useCallback(
    (requestGeneration: number) => requestGenerationRef.current === requestGeneration,
    [],
  );

  useEffect(() => {
    if (!selectedTarget) {
      return;
    }
    setTargetId((prev) => (prev === selectedTarget.id ? prev : selectedTarget.id));
  }, [selectedTarget]);

  useEffect(() => {
    if (!savedAgent) return;
    const resolvedSavedAgent = resolvePickerAgent(savedAgent, effectiveAgentAvailability, 'codex');
    setAgent((prev) => (prev === resolvedSavedAgent ? prev : resolvedSavedAgent));
  }, [effectiveAgentAvailability, savedAgent]);

  useEffect(() => {
    const resolvedAgent = resolvePickerAgent(agent, effectiveAgentAvailability, 'codex');
    if (resolvedAgent !== agent) {
      setAgent(resolvedAgent);
    }
  }, [agent, effectiveAgentAvailability]);

  useEffect(() => {
    setYoloMode((prev) => (prev === savedYoloMode ? prev : savedYoloMode));
  }, [savedYoloMode]);

  useEffect(() => {
    if (!yoloSupported) {
      setYoloMode(false);
    }
  }, [yoloSupported]);

  useEffect(() => {
    if (!isOpen) {
      invalidateRequestGeneration();
      return;
    }
    setMode('path-input');
    setInputValue(buildInitialPickerInput(initialInputPathForTarget(selectedTarget), homePath));
    setHighlightedItemKey(null);
    setAutoHighlight(false);
    autoHighlightDoneRef.current = false;
    setSelectedPath('');
    setRepoRootPath(null);
    setRepoInfo(null);
    setRefreshing(false);
    setPickerOperation(null);
  }, [isOpen]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!isOpen) {
      return;
    }

    let cancelled = false;
    if (!onGetRecentLocations) {
      setRecentLocations([]);
      return;
    }

    onGetRecentLocations(selectedEndpointId)
      .then((result) => {
        if (cancelled) {
          return;
        }
        setRecentLocations(result.locations);
        const nextHomePath = result.home_path;
        if (nextHomePath) {
          setHomePath((prev) => (prev === nextHomePath ? prev : nextHomePath));
        }
      })
      .catch((err) => {
        if (cancelled) {
          return;
        }
        console.error('[LocationPicker] Failed to fetch recent locations:', err);
        setRecentLocations([]);
      });

    return () => {
      cancelled = true;
    };
  }, [isOpen, onGetRecentLocations, selectedEndpointId]);

  const expandedInput = expandDisplayPath(inputValue, homePath);
  const filteredRecent = useMemo(
    () => (expandedInput
      ? recentLocations.filter(
          (loc) => loc.path.toLowerCase().includes(expandedInput.toLowerCase()),
        )
      : recentLocations),
    [expandedInput, recentLocations],
  );
  const visibleRecent = useMemo(
    () => filteredRecent.slice(0, MAX_RECENT_LOCATIONS).map((loc) => ({
      ...loc,
      name: pathBasename(loc.path),
      selectionPath: homePath ? toDisplayPath(loc.path, homePath) : loc.path,
    })),
    [filteredRecent, homePath],
  );
  const selectableItems = useMemo<PathSelectableItem[]>(
    () => [
      ...visibleRecent.map((loc) => ({
        kind: 'recent' as const,
        key: `recent:${loc.path}`,
        path: loc.selectionPath,
      })),
      ...fsSuggestions.map((item) => ({
        kind: 'directory' as const,
        key: `directory:${item.path}`,
        path: item.path,
      })),
    ],
    [fsSuggestions, visibleRecent],
  );
  const highlightedIndex = useMemo(
    () => (highlightedItemKey ? selectableItems.findIndex((item) => item.key === highlightedItemKey) : -1),
    [highlightedItemKey, selectableItems],
  );
  const highlightedItem = highlightedIndex >= 0 ? selectableItems[highlightedIndex] : null;
  const completionCandidate = useMemo(() => {
    const trimmedInput = inputValue.trim();
    if (!trimmedInput) {
      return '';
    }
    return selectableItems.find((item) => pathStartsWith(item.path, trimmedInput))?.path || '';
  }, [inputValue, selectableItems]);
  const ghostText = completionCandidate !== inputValue && pathStartsWith(completionCandidate, inputValue)
    ? completionCandidate
    : '';
  const tabCompletionValue = highlightedItem?.path || ghostText;

  useEffect(() => {
    if (highlightedItemKey && highlightedIndex < 0) {
      setHighlightedItemKey(null);
    }
  }, [highlightedIndex, highlightedItemKey]);

  // Pre-highlight the top recent location when it loads so a bare Enter opens
  // it. One-shot per open: typing or tab-completing consumes it, and Esc still
  // closes the picker while the automatic highlight is in place.
  useEffect(() => {
    if (!isOpen || mode !== 'path-input' || autoHighlightDoneRef.current || visibleRecent.length === 0) {
      return;
    }
    autoHighlightDoneRef.current = true;
    setAutoHighlight(true);
    setHighlightedItemKey(`recent:${visibleRecent[0].path}`);
  }, [isOpen, mode, visibleRecent]);

  useEffect(() => {
    if (highlightedIndex >= 0) {
      document.querySelector(`[data-index="${highlightedIndex}"]`)?.scrollIntoView({ block: 'nearest' });
    }
  }, [highlightedIndex]);

  const launchSelection = useCallback((path: string) => {
    const selectedAgent = resolvePickerAgent(agent, effectiveAgentAvailability, 'codex');
    onSelect(path, selectedAgent, selectedEndpointId, yoloMode && yoloSupported);
    onClose();
  }, [agent, effectiveAgentAvailability, onClose, onSelect, selectedEndpointId, yoloMode, yoloSupported]);

  const updateInputValue = useCallback((nextPath: string) => {
    if (nextPath === inputValue) {
      return;
    }
    invalidateRequestGeneration();
    autoHighlightDoneRef.current = true;
    setAutoHighlight(false);
    setInputValue(nextPath);
    setHighlightedItemKey(null);
    setSelectedPath('');
    setHasSelectedSinceTab(true);
    setPickerOperation(null);
  }, [inputValue, invalidateRequestGeneration]);

  const handleTabComplete = useCallback((nextPath: string) => {
    if (nextPath === inputValue) {
      return;
    }
    invalidateRequestGeneration();
    autoHighlightDoneRef.current = true;
    setAutoHighlight(false);
    setInputValue(nextPath);
    setHighlightedItemKey(null);
    setSelectedPath('');
    setHasSelectedSinceTab(false);
    setPickerOperation(null);
  }, [inputValue, invalidateRequestGeneration]);

  const handlePathSelect = useCallback((path: string) => {
    setSelectedPath(path);
  }, []);

  const setSelectedPathFromPhysical = useCallback((path: string) => {
    setSelectedPath((prev) => (prev === path ? prev : path));
  }, []);

  const handleSelectPath = useCallback(async (rawPath: string) => {
    if (!onInspectPath) {
      onError?.('Path inspection is not available.');
      return;
    }

    const sanitizedPath = normalizePickerPath(rawPath, homePath);
    if (!sanitizedPath) {
      return;
    }
    const requestGeneration = beginRequestGeneration();
    setPickerOperation('Inspecting path');

    try {
      const inspected = await onInspectPath(sanitizedPath, selectedEndpointId);
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      const inspection = inspected.inspection;
      if (!inspection?.exists || !inspection.is_directory) {
        setPickerOperation(null);
        onError?.(`Directory not found: ${sanitizedPath}`);
        return;
      }

      const inspectedHomePath = inspection.home_path;
      if (inspectedHomePath) {
        setHomePath((prev) => (prev === inspectedHomePath ? prev : inspectedHomePath));
      }
      const resolvedPath = inspection.resolved_path;
      setSelectedPathFromPhysical(resolvedPath);
      const repoRoot = inspection.repo_root;
      if (repoRoot && onGetRepoInfo) {
        setPickerOperation('Loading repo options');
        const result = await onGetRepoInfo(repoRoot, selectedEndpointId);
        if (!isRequestCurrent(requestGeneration)) {
          return;
        }
        if (result.success && result.info) {
          setMode('repo-options');
          setRepoRootPath(repoRoot);
          setRepoInfo(toChooserRepoInfo(result.info));
          setPickerOperation(null);
          return;
        }
        onError?.(result.error || 'Failed to load repo options');
        setPickerOperation(null);
        launchSelection(resolvedPath);
        return;
      }

      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      setPickerOperation(null);
      launchSelection(resolvedPath);
    } catch (err) {
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      console.log('[LocationPicker] inspect path error:', err);
      setPickerOperation(null);
      onError?.(err instanceof Error ? err.message : String(err));
    }
  }, [
    beginRequestGeneration,
    homePath,
    isRequestCurrent,
    launchSelection,
    onError,
    onGetRepoInfo,
    onInspectPath,
    selectedEndpointId,
    setSelectedPathFromPhysical,
  ]);

  const handleAgentChange = useCallback((nextAgent: SessionAgent) => {
    if (!isAgentAvailable(effectiveAgentAvailability, nextAgent)) {
      return;
    }
    setAgent(nextAgent);
    if (nextAgent === TERMINAL_AGENT) {
      return;
    }
    setSetting(SESSION_AGENT_KEY, nextAgent);
  }, [effectiveAgentAvailability, setSetting]);

  const handleTargetChange = useCallback((nextTargetId: string) => {
    if (nextTargetId === selectedTarget.id) {
      if (!yoloSupported || !yoloSettingKey) {
        return;
      }
      const nextYoloMode = !yoloMode;
      setSetting(yoloSettingKey, String(nextYoloMode));
      setYoloMode(nextYoloMode);
      return;
    }

    const nextTarget = selectableTargets.find((target) => target.id === nextTargetId);
    if (!nextTarget || !nextTarget.connected) {
      return;
    }

    setTargetId(nextTargetId);
    invalidateRequestGeneration();
    setMode('path-input');
    setInputValue(buildInitialPickerInput(initialInputPathForTarget(nextTarget), homePath));
    setHighlightedItemKey(null);
    setAutoHighlight(false);
    autoHighlightDoneRef.current = false;
    setSelectedPath('');
    setRepoRootPath(null);
    setRepoInfo(null);
    setRecentLocations([]);
    setRefreshing(false);
    setPickerOperation(null);
  }, [homePath, invalidateRequestGeneration, selectableTargets, selectedTarget.id, setSetting, yoloMode, yoloSettingKey, yoloSupported]);

  const handlePathInputSubmit = useCallback(() => {
    const pathToOpen = highlightedItem?.path || inputValue;
    if (!pathToOpen.trim()) {
      return;
    }
    void handleSelectPath(pathToOpen);
  }, [handleSelectPath, highlightedItem, inputValue]);

  const handleSelectMainRepo = useCallback(() => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (repoRootPath) {
      launchSelection(repoRootPath);
    }
  }, [hasAvailableAgents, launchSelection, onError, repoRootPath]);

  const handleSelectWorktree = useCallback((path: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    launchSelection(path);
  }, [hasAvailableAgents, launchSelection, onError]);

  const handleCreateWorktree = useCallback(async (branchName: string, startingFrom: string) => {
    if (!hasAvailableAgents) {
      onError?.(noAgentsMessage);
      return;
    }
    if (!repoRootPath || !onCreateWorktree) {
      return;
    }

    if (onCreateWorktreeSession) {
      const selectedAgent = resolvePickerAgent(agent, effectiveAgentAvailability, 'codex');
      onCreateWorktreeSession(
        repoRootPath,
        branchName,
        startingFrom,
        selectedEndpointId,
        selectedAgent,
        yoloMode && yoloSupported,
      );
      onClose();
      return;
    }

    const requestGeneration = beginRequestGeneration();
    try {
      const result = await onCreateWorktree(repoRootPath, branchName, undefined, startingFrom, selectedEndpointId);
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      if (result.success && result.path) {
        setSelectedPathFromPhysical(result.path);
        launchSelection(result.path);
      } else {
        onError?.(result.error || 'Failed to create worktree');
      }
    } catch (err) {
      if (!isRequestCurrent(requestGeneration)) {
        return;
      }
      console.error('[LocationPicker] Failed to create worktree:', err);
      onError?.(err instanceof Error ? err.message : 'Failed to create worktree');
    }
  }, [
    beginRequestGeneration,
    hasAvailableAgents,
    isRequestCurrent,
    launchSelection,
    onClose,
    onCreateWorktree,
    onCreateWorktreeSession,
    onError,
    repoRootPath,
    agent,
    effectiveAgentAvailability,
    selectedEndpointId,
    setSelectedPathFromPhysical,
    yoloMode,
    yoloSupported,
  ]);

  const handleRefresh = useCallback(async () => {
    if (!repoRootPath || !onGetRepoInfo || refreshing) {
      return;
    }

    const requestGeneration = requestGenerationRef.current;
    setRefreshing(true);
    try {
      const result = await onGetRepoInfo(repoRootPath, selectedEndpointId);
      if (isRequestCurrent(requestGeneration) && result.success && result.info) {
        setRepoInfo(toChooserRepoInfo(result.info));
      }
    } catch (err) {
      console.error('[LocationPicker] Failed to refresh repo info:', err);
    } finally {
      if (isRequestCurrent(requestGeneration)) {
        setRefreshing(false);
      }
    }
  }, [isRequestCurrent, onGetRepoInfo, refreshing, repoRootPath, selectedEndpointId]);

  const handleBack = useCallback(() => {
    invalidateRequestGeneration();
    setMode('path-input');
    setRepoRootPath(null);
    setRepoInfo(null);
    setRefreshing(false);
    setPickerOperation(null);
  }, [invalidateRequestGeneration]);

  const movePathSelection = useCallback((direction: 'up' | 'down') => {
    const totalItems = selectableItems.length;
    if (totalItems === 0) {
      return;
    }

    const nextIndex = direction === 'down'
      ? (highlightedIndex < 0 ? 0 : (highlightedIndex + 1) % totalItems)
      : (highlightedIndex <= 0 ? totalItems - 1 : highlightedIndex - 1);
    const nextItem = selectableItems[nextIndex];
    if (nextItem) {
      setAutoHighlight(false);
      setHighlightedItemKey(nextItem.key);
    }
  }, [highlightedIndex, selectableItems]);

  const handleClosePicker = useCallback(() => {
    invalidateRequestGeneration();
    onClose();
  }, [invalidateRequestGeneration, onClose]);

  const handleEscape = useCallback(() => {
    if (mode === 'repo-options') {
      // RepoOptions pushes its own sub-state handlers (pendingDeletePath, showNewWorktree)
      // above this one in the escape stack. This branch fires only when those are inactive.
      handleBack();
    } else if (highlightedItemKey && !autoHighlight) {
      setHighlightedItemKey(null);
    } else {
      handleClosePicker();
    }
  }, [mode, handleBack, highlightedItemKey, autoHighlight, handleClosePicker]);
  useEscapeStack(handleEscape, isOpen);

  const handleDialogKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.altKey && !e.metaKey && !e.ctrlKey) {
      if (e.code === 'KeyT') {
        e.preventDefault();
        handleAgentChange(TERMINAL_AGENT);
        return;
      }

      const shortcutTargetId = targetShortcutIDByCode.get(e.code);
      if (shortcutTargetId) {
        e.preventDefault();
        handleTargetChange(shortcutTargetId);
        return;
      }

      const digitMatch = /^Digit([1-9])$/.exec(e.code);
      if (digitMatch) {
        const idx = Number(digitMatch[1]) - 1;
        const nextAgent = orderedAgentList.filter((candidate) => candidate !== TERMINAL_AGENT)[idx];
        if (nextAgent) {
          e.preventDefault();
          handleAgentChange(nextAgent);
        }
        return;
      }
    }

    if (mode !== 'path-input') {
      return;
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      movePathSelection('down');
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      movePathSelection('up');
    }
  }, [
    handleAgentChange,
    handleTargetChange,
    mode,
    movePathSelection,
    orderedAgentList,
    targetShortcutIDByCode,
  ]);

  if (!isOpen || !selectedTarget) {
    return null;
  }

  return (
    <div className="location-picker-overlay" data-testid="location-picker-overlay" onClick={handleClosePicker}>
      <div
        className="location-picker"
        data-testid="location-picker"
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleDialogKeyDown}
      >
        <div className="picker-agent-bar">
          <div className="picker-agent-label">SESSION AGENT</div>
          <div className="picker-agent-controls">
            <div className="agent-toggle" role="radiogroup" aria-label={copy.agentAria}>
              {orderedAgentList.map((candidate) => {
                const available = isAgentAvailable(effectiveAgentAvailability, candidate);
                const shortcutNumber = agentShortcutByName.get(candidate);
                const shortcut = candidate === TERMINAL_AGENT
                  ? '⌥T'
                  : shortcutNumber && shortcutNumber <= 9 ? `⌥${shortcutNumber}` : null;
                const label = pickerAgentLabel(candidate);
                return (
                  <button
                    key={candidate}
                    type="button"
                    className={`agent-option ${agent === candidate ? 'active' : ''}`}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => handleAgentChange(candidate)}
                    role="radio"
                    aria-checked={agent === candidate}
                    disabled={!available}
                    title={!available ? `${label} is not available on ${selectedTarget.name}` : undefined}
                  >
                    <span className="agent-option-name">{label}</span>
                    {available && shortcut && <kbd className="agent-shortcut">{shortcut}</kbd>}
                  </button>
                );
              })}
            </div>
          </div>
        </div>
        <div className="picker-endpoint-bar">
          <div className="picker-endpoint-leading">
            <div className="picker-endpoint-label">SESSION TARGET</div>
          </div>
          <div className="picker-endpoint-controls" role="radiogroup" aria-label={copy.targetAria}>
            {selectableTargets.map((target) => {
              const shortcutKey = targetShortcutByID.get(target.id);
              const active = target.id === selectedTarget.id;
              return (
                <button
                  key={target.id}
                  type="button"
                  className={`endpoint-option ${active ? 'active' : ''} ${active && yoloMode ? 'yolo-active' : ''}`}
                  data-testid={target.id === LOCAL_TARGET ? 'location-picker-target-local' : `location-picker-target-${target.id}`}
                  data-endpoint-id={target.endpointId}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => handleTargetChange(target.id)}
                  role="radio"
                  aria-checked={active}
                  disabled={!target.connected}
                  title={!target.connected ? `${target.name} is ${target.metaLabel}` : undefined}
                >
                  <span className="endpoint-option-name">{target.name}</span>
                  {active && yoloMode && (
                    <span className="endpoint-option-badge">YOLO</span>
                  )}
                  <div className="endpoint-option-footer">
                    <span className={`endpoint-option-meta ${target.metaClassName || ''}`.trim()}>{target.metaLabel}</span>
                    {target.connected && shortcutKey && <kbd className="agent-shortcut endpoint-shortcut">{`⌥${shortcutKey.toUpperCase()}`}</kbd>}
                  </div>
                </button>
              );
            })}
          </div>
        </div>
        {!hasAvailableAgents && (
          <div className="picker-agent-warning">{noAgentsMessage}</div>
        )}
        {mode === 'path-input' ? (
          <>
            <div className="picker-header">
              <div className="picker-header-top">
                <div className="picker-title" data-testid="location-picker-title">
                  {copy.title}
                </div>
                <div
                  className={`picker-endpoint-hint ${!yoloSupported ? 'disabled' : ''}`}
                  title={!yoloSupported ? `${agentLabel(agent)} does not support yolo mode` : undefined}
                >
                  {yoloSupported ? 'select the same target again for YOLO' : 'YOLO unavailable'}
                </div>
              </div>
              <PathInput
                value={inputValue}
                onChange={updateInputValue}
                onTabComplete={handleTabComplete}
                onSelect={handlePathSelect}
                onSubmit={handlePathInputSubmit}
                ghostText={ghostText}
                completionValue={tabCompletionValue}
                hasSelectedSinceTab={hasSelectedSinceTab}
                placeholder={selectedTarget.placeholder}
              />
              {currentDir && (
                <div className="picker-breadcrumb" data-testid="location-picker-breadcrumb">
                  <span className="picker-breadcrumb-label">Browsing:</span>
                  <span className="picker-breadcrumb-path" data-testid="location-picker-breadcrumb-path">{currentDir}</span>
                </div>
              )}
              {pickerOperation && (
                <div className="picker-operation" role="status" aria-label={pickerOperation}>
                  <span className="picker-operation-pulse" aria-hidden="true" />
                  <span>{pickerOperation}</span>
                  {selectedPath && <span className="picker-operation-path">{toDisplayPath(selectedPath, homePath)}</span>}
                </div>
              )}
            </div>

            <div className="picker-results">
              {visibleRecent.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">RECENT</div>
                  {visibleRecent.map((loc, index) => (
                    <div
                      key={loc.path}
                      className={`picker-item ${index === highlightedIndex ? 'selected' : ''}`}
                      data-testid={`location-picker-item-${index}`}
                      data-index={index}
                      data-kind="recent"
                      data-path={loc.selectionPath}
                      onMouseDown={(e) => e.preventDefault()}
                      onClick={() => void handleSelectPath(loc.selectionPath)}
                    >
                      <div className="picker-icon">🕐</div>
                      <div className="picker-info">
                        <div className="picker-name">{loc.name}</div>
                        <div className="picker-path">{loc.selectionPath}</div>
                      </div>
                    </div>
                  ))}
                </div>
              )}

              {fsSuggestions.length > 0 && (
                <div className="picker-section">
                  <div className="picker-section-title">DIRECTORIES</div>
                  {fsSuggestions.map((item, index) => {
                    const globalIndex = visibleRecent.length + index;
                    return (
                      <div
                        key={item.path}
                        className={`picker-item ${globalIndex === highlightedIndex ? 'selected' : ''}`}
                        data-testid={`location-picker-item-${globalIndex}`}
                        data-index={globalIndex}
                        data-kind="directory"
                        data-path={item.path}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => void handleSelectPath(item.path)}
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

              {fsSuggestions.length === 0 && visibleRecent.length === 0 && (
                <div className="picker-empty" data-testid="location-picker-empty">
                  {inputValue
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
        ) : repoInfo ? (
          <RepoOptions
            repoInfo={repoInfo}
            selectedPath={selectedPath || repoRootPath || undefined}
            onSelectedPathChange={(path) => {
              setSelectedPathFromPhysical(path);
            }}
            onSelectMainRepo={handleSelectMainRepo}
            onSelectWorktree={handleSelectWorktree}
            onCreateWorktree={handleCreateWorktree}
            onDeleteWorktree={onDeleteWorktree ? async (path, options) => {
              const requestGeneration = requestGenerationRef.current;
              const deleteResult = options
                ? await onDeleteWorktree(path, selectedEndpointId, options)
                : await onDeleteWorktree(path, selectedEndpointId);
              if (!deleteResult.success) {
                throw new Error(deleteResult.error || 'Failed to delete worktree');
              }
              if (repoRootPath && onGetRepoInfo) {
                const result = await onGetRepoInfo(repoRootPath, selectedEndpointId);
                if (isRequestCurrent(requestGeneration) && result.success && result.info) {
                  setRepoInfo(toChooserRepoInfo(result.info));
                } else if (isRequestCurrent(requestGeneration) && !result.success) {
                  throw new Error(result.error || 'Failed to refresh repo options');
                }
              }
            } : undefined}
            onError={onError}
            onRefresh={handleRefresh}
            onBack={handleBack}
            refreshing={refreshing}
          />
        ) : null}
      </div>
    </div>
  );
}
