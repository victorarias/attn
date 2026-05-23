// app/src/components/SettingsModal.tsx
import { useState, useCallback, useEffect, useMemo } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { open } from '@tauri-apps/plugin-dialog';
import {
  DaemonEndpoint,
  DaemonPlugin,
  DaemonPluginIssue,
  DaemonSettings,
  PluginListResult,
} from '../hooks/useDaemonSocket';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import type { ThemePreference } from '../hooks/useTheme';
import {
  AGENT_CAPABILITY_ORDER,
  agentCapabilityLabel,
  agentLabel,
  getAgentAvailability,
  getAgentCapabilities,
  getAgentExecutableSettings,
  hasAnyAvailableAgents,
  isAgentAvailable,
  orderedAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import {
  buildCustomReviewLoopPresetID,
  parseSavedReviewLoopPresets,
  REVIEW_LOOP_SETTINGS_CUSTOM_PRESETS,
  type ReviewLoopPreset,
  serializeSavedReviewLoopPresets,
} from '../utils/reviewLoopPresets';
import { BUILD_PROFILE } from '../utils/buildProfile';
import './SettingsModal.css';

interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  mutedRepos: string[];
  connectedHosts: string[];
  onUnmuteRepo: (repo: string) => void;
  mutedAuthors: string[];
  onUnmuteAuthor: (author: string) => void;
  settings: DaemonSettings;
  endpoints: DaemonEndpoint[];
  plugins: DaemonPlugin[];
  pluginIssues: DaemonPluginIssue[];
  onAddEndpoint: (name: string, sshTarget: string, profile?: string) => Promise<{ success: boolean }>;
  onUpdateEndpoint: (endpointId: string, updates: { name?: string; ssh_target?: string; enabled?: boolean; profile?: string }) => Promise<{ success: boolean }>;
  onRemoveEndpoint: (endpointId: string) => Promise<{ success: boolean }>;
  onSetEndpointRemoteWeb: (endpointId: string, enabled: boolean) => Promise<{ success: boolean }>;
  onListPlugins: () => Promise<PluginListResult>;
  onInstallPlugin: (path: string) => Promise<{ success: boolean; name?: string }>;
  onRemovePlugin: (name: string) => Promise<{ success: boolean; name?: string }>;
  onSetPluginPriority: (name: string, priority: number) => Promise<{ success: boolean; name?: string }>;
  onSetSetting: (key: string, value: string) => void;
  themePreference: ThemePreference;
  onSetTheme: (theme: ThemePreference) => void;
}

type SettingsSectionID = 'general' | 'connectivity' | 'plugins' | 'agents' | 'review' | 'hygiene';

interface SettingsNavItem {
  id: SettingsSectionID;
  label: string;
  title: string;
  description: string;
  count: number;
  keywords: string;
}

interface SettingsNavGroup {
  label: string;
  items: SettingsNavItem[];
}

export function SettingsModal({
  isOpen,
  onClose,
  mutedRepos,
  connectedHosts,
  onUnmuteRepo,
  mutedAuthors,
  onUnmuteAuthor,
  settings,
  endpoints,
  plugins,
  pluginIssues,
  onAddEndpoint,
  onUpdateEndpoint,
  onRemoveEndpoint,
  onSetEndpointRemoteWeb,
  onListPlugins,
  onInstallPlugin,
  onRemovePlugin,
  onSetPluginPriority,
  onSetSetting,
  themePreference,
  onSetTheme,
}: SettingsModalProps) {
  const [projectsDir, setProjectsDir] = useState(settings.projects_directory || '');
  const [agentExecutables, setAgentExecutables] = useState<Record<SessionAgent, string>>({});
  const [editorExecutable, setEditorExecutable] = useState(settings.editor_executable || '');
  const [defaultAgent, setDefaultAgent] = useState<SessionAgent>('claude');
  const [reviewLoopPresets, setReviewLoopPresets] = useState<ReviewLoopPreset[]>([]);
  const [reviewLoopPresetName, setReviewLoopPresetName] = useState('');
  const [reviewLoopPrompt, setReviewLoopPrompt] = useState('');
  const [reviewLoopIterations, setReviewLoopIterations] = useState(3);
  const [selectedReviewLoopPresetID, setSelectedReviewLoopPresetID] = useState('');
  const [reviewLoopModel, setReviewLoopModel] = useState(settings.review_loop_model || '');
  const [reviewerModel, setReviewerModel] = useState(settings.reviewer_model || '');
  const [newEndpointName, setNewEndpointName] = useState('');
  const [newEndpointTarget, setNewEndpointTarget] = useState('');
  const [newEndpointProfile, setNewEndpointProfile] = useState(BUILD_PROFILE);
  const [editingEndpointID, setEditingEndpointID] = useState<string | null>(null);
  const [editingEndpointName, setEditingEndpointName] = useState('');
  const [editingEndpointTarget, setEditingEndpointTarget] = useState('');
  const [editingEndpointProfile, setEditingEndpointProfile] = useState('');
  const [endpointError, setEndpointError] = useState<string | null>(null);
  const [endpointActionID, setEndpointActionID] = useState<string | null>(null);
  const [pluginSourcePath, setPluginSourcePath] = useState('');
  const [pluginPriorityDrafts, setPluginPriorityDrafts] = useState<Record<string, string>>({});
  const [pluginError, setPluginError] = useState<string | null>(null);
  const [pluginActionName, setPluginActionName] = useState<string | null>(null);
  const [pluginsLoading, setPluginsLoading] = useState(false);
  const [selectedSection, setSelectedSection] = useState<SettingsSectionID>('connectivity');
  const [settingsSearch, setSettingsSearch] = useState('');
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  // Sync with settings when modal opens
  const actualProjectsDir = settings.projects_directory || '';
  const tailscaleEnabled = (settings.tailscale_enabled || 'false') === 'true';
  const tailscaleStatus = settings.tailscale_status || 'disabled';
  const tailscaleURL = settings.tailscale_url || '';
  const tailscaleDomain = settings.tailscale_domain || '';
  const tailscaleAuthURL = settings.tailscale_auth_url || '';
  const tailscaleError = settings.tailscale_error || '';
  const actualAgentExecutables = useMemo(
    () => getAgentExecutableSettings(settings),
    [settings],
  );
  const actualAgentCapabilities = useMemo(
    () => getAgentCapabilities(settings),
    [settings],
  );
  const actualEditorExecutable = settings.editor_executable || '';
  const actualDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
  const actualReviewLoopPresets = useMemo(
    () => parseSavedReviewLoopPresets(settings[REVIEW_LOOP_SETTINGS_CUSTOM_PRESETS]),
    [settings],
  );
  const actualReviewLoopModel = settings.review_loop_model || '';
  const actualReviewerModel = settings.reviewer_model || '';
  const resolvedDefaultAgent = resolvePreferredAgent(actualDefaultAgent, agentAvailability, 'codex');
  const orderedAgentList = useMemo(
    () => orderedAgents(agentAvailability, resolvedDefaultAgent, 'codex'),
    [agentAvailability, resolvedDefaultAgent],
  );
  const agentCapabilityOrder = useMemo(
    () => AGENT_CAPABILITY_ORDER.map((cap) => cap as string),
    [],
  );
  const rawPtyBackendMode = (settings.pty_backend_mode || 'unknown').toLowerCase();
  const ptyBackendMode = rawPtyBackendMode === 'worker' || rawPtyBackendMode === 'embedded'
    ? rawPtyBackendMode
    : 'unknown';
  const ptyBackendLabel =
    ptyBackendMode === 'worker'
      ? 'External worker sidecar'
      : ptyBackendMode === 'embedded'
        ? 'Embedded in daemon'
        : 'Unknown';
  const ptyBackendHint =
    ptyBackendMode === 'worker'
      ? 'Sessions run in per-session worker processes and can survive daemon restarts.'
      : ptyBackendMode === 'embedded'
      ? 'Sessions run inside the daemon process and stop if the daemon restarts.'
      : 'Backend mode is not currently reported by the daemon.';
  const endpointActionInFlight = endpointActionID !== null;

  useEffect(() => {
    if (!isOpen) return;
    setProjectsDir(actualProjectsDir);
    setAgentExecutables(actualAgentExecutables);
    setEditorExecutable(actualEditorExecutable);
    setDefaultAgent(resolvedDefaultAgent);
    setReviewLoopPresets(actualReviewLoopPresets);
    setSelectedReviewLoopPresetID(actualReviewLoopPresets[0]?.id || '');
    setReviewLoopPresetName(actualReviewLoopPresets[0]?.name || '');
    setReviewLoopPrompt(actualReviewLoopPresets[0]?.prompt || '');
    setReviewLoopIterations(actualReviewLoopPresets[0]?.iterationLimit || 3);
    setReviewLoopModel(actualReviewLoopModel);
    setReviewerModel(actualReviewerModel);
    setNewEndpointName('');
    setNewEndpointTarget('');
    setEditingEndpointID(null);
    setEditingEndpointName('');
    setEditingEndpointTarget('');
    setEndpointError(null);
    setEndpointActionID(null);
    setPluginSourcePath('');
    setPluginError(null);
    setPluginActionName(null);
  }, [isOpen, actualProjectsDir, actualAgentExecutables, actualEditorExecutable, resolvedDefaultAgent, actualReviewLoopPresets, actualReviewLoopModel, actualReviewerModel]);

  useEffect(() => {
    if (!isOpen) return;
    const selected = actualReviewLoopPresets.find((preset) => preset.id === selectedReviewLoopPresetID);
    if (!selected && actualReviewLoopPresets.length > 0) {
      const first = actualReviewLoopPresets[0];
      setSelectedReviewLoopPresetID(first.id);
      setReviewLoopPresetName(first.name);
      setReviewLoopPrompt(first.prompt);
      setReviewLoopIterations(first.iterationLimit);
    }
    if (!selected && actualReviewLoopPresets.length === 0) {
      setSelectedReviewLoopPresetID('');
      setReviewLoopPresetName('');
      setReviewLoopPrompt('');
      setReviewLoopIterations(3);
    }
  }, [actualReviewLoopPresets, isOpen, selectedReviewLoopPresetID]);

  useEscapeStack(onClose, isOpen);

  const handleBrowse = useCallback(async () => {
    const selected = await open({
      directory: true,
      multiple: false,
      title: 'Select Projects Directory',
    });
    if (selected && typeof selected === 'string') {
      setProjectsDir(selected);
      onSetSetting('projects_directory', selected);
    }
  }, [onSetSetting]);

  const handleInputChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setProjectsDir(e.target.value);
  }, []);

  const handleToggleTailscale = useCallback(() => {
    onSetSetting('tailscale_enabled', tailscaleEnabled ? 'false' : 'true');
  }, [onSetSetting, tailscaleEnabled]);

  const handleInputBlur = useCallback(() => {
    if (projectsDir !== actualProjectsDir) {
      onSetSetting('projects_directory', projectsDir);
    }
  }, [projectsDir, actualProjectsDir, onSetSetting]);

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      if (projectsDir !== actualProjectsDir) {
        onSetSetting('projects_directory', projectsDir);
      }
    }
  }, [projectsDir, actualProjectsDir, onSetSetting]);

  const handleExecutableChange = useCallback((agent: SessionAgent, value: string) => {
    setAgentExecutables((prev) => ({ ...prev, [agent]: value }));
  }, []);

  const commitExecutable = useCallback((agent: SessionAgent) => {
    const nextValue = agentExecutables[agent] || '';
    const currentValue = actualAgentExecutables[agent] || '';
    if (nextValue !== currentValue) {
      onSetSetting(`${agent}_executable`, nextValue);
    }
  }, [actualAgentExecutables, agentExecutables, onSetSetting]);

  const handleEditorChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setEditorExecutable(e.target.value);
  }, []);

  const handleEditorBlur = useCallback(() => {
    if (editorExecutable !== actualEditorExecutable) {
      onSetSetting('editor_executable', editorExecutable);
    }
  }, [editorExecutable, actualEditorExecutable, onSetSetting]);

  const handleEditorKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      if (editorExecutable !== actualEditorExecutable) {
        onSetSetting('editor_executable', editorExecutable);
      }
    }
  }, [editorExecutable, actualEditorExecutable, onSetSetting]);

  const handleDefaultAgentChange = useCallback((agent: SessionAgent) => {
    if (!isAgentAvailable(agentAvailability, agent)) return;
    setDefaultAgent(agent);
    if (agent !== actualDefaultAgent) {
      onSetSetting('new_session_agent', agent);
    }
  }, [actualDefaultAgent, agentAvailability, onSetSetting]);

  const persistReviewLoopPresets = useCallback((nextPresets: ReviewLoopPreset[]) => {
    setReviewLoopPresets(nextPresets);
    onSetSetting(REVIEW_LOOP_SETTINGS_CUSTOM_PRESETS, serializeSavedReviewLoopPresets(nextPresets));
  }, [onSetSetting]);

  const handleSelectReviewLoopPreset = useCallback((presetId: string) => {
    setSelectedReviewLoopPresetID(presetId);
    const selected = reviewLoopPresets.find((preset) => preset.id === presetId);
    setReviewLoopPresetName(selected?.name || '');
    setReviewLoopPrompt(selected?.prompt || '');
    setReviewLoopIterations(selected?.iterationLimit || 3);
  }, [reviewLoopPresets]);

  const handleSaveReviewLoopPreset = useCallback(() => {
    const name = reviewLoopPresetName.trim();
    const prompt = reviewLoopPrompt.trim();
    if (!name || !prompt || reviewLoopIterations <= 0) return;
    const id = selectedReviewLoopPresetID || buildCustomReviewLoopPresetID(name);
    const nextPreset: ReviewLoopPreset = {
      id,
      name,
      prompt,
      iterationLimit: reviewLoopIterations,
      builtin: false,
    };
    const nextPresets = [...reviewLoopPresets.filter((preset) => preset.id !== id), nextPreset]
      .sort((a, b) => a.name.localeCompare(b.name));
    persistReviewLoopPresets(nextPresets);
    setSelectedReviewLoopPresetID(id);
  }, [
    persistReviewLoopPresets,
    reviewLoopIterations,
    reviewLoopPresetName,
    reviewLoopPresets,
    reviewLoopPrompt,
    selectedReviewLoopPresetID,
  ]);

  const handleDeleteReviewLoopPreset = useCallback(() => {
    if (!selectedReviewLoopPresetID) return;
    const nextPresets = reviewLoopPresets.filter((preset) => preset.id !== selectedReviewLoopPresetID);
    persistReviewLoopPresets(nextPresets);
    const nextSelected = nextPresets[0];
    setSelectedReviewLoopPresetID(nextSelected?.id || '');
    setReviewLoopPresetName(nextSelected?.name || '');
    setReviewLoopPrompt(nextSelected?.prompt || '');
    setReviewLoopIterations(nextSelected?.iterationLimit || 3);
  }, [persistReviewLoopPresets, reviewLoopPresets, selectedReviewLoopPresetID]);

  const handleNewReviewLoopPreset = useCallback(() => {
    setSelectedReviewLoopPresetID('');
    setReviewLoopPresetName('');
    setReviewLoopPrompt('');
    setReviewLoopIterations(3);
  }, []);

  const commitReviewLoopModel = useCallback(() => {
    if (reviewLoopModel !== actualReviewLoopModel) {
      onSetSetting('review_loop_model', reviewLoopModel);
    }
  }, [actualReviewLoopModel, onSetSetting, reviewLoopModel]);

  const commitReviewerModel = useCallback(() => {
    if (reviewerModel !== actualReviewerModel) {
      onSetSetting('reviewer_model', reviewerModel);
    }
  }, [actualReviewerModel, onSetSetting, reviewerModel]);

  const handleAddEndpoint = useCallback(async () => {
    const name = newEndpointName.trim();
    const sshTarget = newEndpointTarget.trim();
    const profile = newEndpointProfile.trim();
    if (!name || !sshTarget) {
      setEndpointError('Endpoint name and SSH target are required.');
      return;
    }
    setEndpointError(null);
    setEndpointActionID('new');
    try {
      await onAddEndpoint(name, sshTarget, profile);
      setNewEndpointName('');
      setNewEndpointTarget('');
      setNewEndpointProfile(BUILD_PROFILE);
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to add endpoint');
    } finally {
      setEndpointActionID(null);
    }
  }, [newEndpointName, newEndpointProfile, newEndpointTarget, onAddEndpoint]);

  const beginEditEndpoint = useCallback((endpoint: DaemonEndpoint) => {
    setEndpointError(null);
    setEditingEndpointID(endpoint.id);
    setEditingEndpointName(endpoint.name);
    setEditingEndpointTarget(endpoint.ssh_target);
    setEditingEndpointProfile(endpoint.profile || '');
  }, []);

  const cancelEditEndpoint = useCallback(() => {
    setEditingEndpointID(null);
    setEditingEndpointName('');
    setEditingEndpointTarget('');
    setEditingEndpointProfile('');
  }, []);

  const handleSaveEndpoint = useCallback(async (endpointId: string) => {
    const name = editingEndpointName.trim();
    const sshTarget = editingEndpointTarget.trim();
    const profile = editingEndpointProfile.trim();
    if (!name || !sshTarget) {
      setEndpointError('Endpoint name and SSH target are required.');
      return;
    }
    setEndpointError(null);
    setEndpointActionID(endpointId);
    try {
      await onUpdateEndpoint(endpointId, { name, ssh_target: sshTarget, profile });
      cancelEditEndpoint();
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to update endpoint');
    } finally {
      setEndpointActionID(null);
    }
  }, [cancelEditEndpoint, editingEndpointName, editingEndpointProfile, editingEndpointTarget, onUpdateEndpoint]);

  const handleToggleEndpoint = useCallback(async (endpoint: DaemonEndpoint) => {
    setEndpointError(null);
    setEndpointActionID(endpoint.id);
    try {
      await onUpdateEndpoint(endpoint.id, { enabled: endpoint.enabled === false });
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to update endpoint');
    } finally {
      setEndpointActionID(null);
    }
  }, [onUpdateEndpoint]);

  const handleRebootstrapEndpoint = useCallback(async (endpoint: DaemonEndpoint) => {
    if (endpoint.enabled === false) {
      return;
    }
    setEndpointError(null);
    setEndpointActionID(endpoint.id);
    try {
      await onUpdateEndpoint(endpoint.id, { enabled: false });
      await onUpdateEndpoint(endpoint.id, { enabled: true });
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to re-bootstrap endpoint');
    } finally {
      setEndpointActionID(null);
    }
  }, [onUpdateEndpoint]);

  const handleRemoveEndpoint = useCallback(async (endpointId: string) => {
    setEndpointError(null);
    setEndpointActionID(endpointId);
    try {
      await onRemoveEndpoint(endpointId);
      if (editingEndpointID === endpointId) {
        cancelEditEndpoint();
      }
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to remove endpoint');
    } finally {
      setEndpointActionID(null);
    }
  }, [cancelEditEndpoint, editingEndpointID, onRemoveEndpoint]);

  const handleSetEndpointRemoteWeb = useCallback(async (endpointId: string, enabled: boolean) => {
    setEndpointError(null);
    setEndpointActionID(endpointId);
    try {
      await onSetEndpointRemoteWeb(endpointId, enabled);
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to update remote web access');
    } finally {
      setEndpointActionID(null);
    }
  }, [onSetEndpointRemoteWeb]);

  const refreshPlugins = useCallback(async () => {
    setPluginsLoading(true);
    setPluginError(null);
    try {
      const result = await onListPlugins();
      setPluginPriorityDrafts(
        Object.fromEntries(result.plugins.map((plugin) => [plugin.name, String(plugin.priority)])),
      );
    } catch (error) {
      setPluginError(error instanceof Error ? error.message : 'Failed to load plugins');
    } finally {
      setPluginsLoading(false);
    }
  }, [onListPlugins]);

  useEffect(() => {
    if (!isOpen) return;
    void refreshPlugins();
  }, [isOpen, refreshPlugins]);

  const handleBrowsePluginPath = useCallback(async () => {
    const selected = await open({
      directory: true,
      multiple: false,
      title: 'Select Plugin Directory',
    });
    if (selected && typeof selected === 'string') {
      setPluginSourcePath(selected);
    }
  }, []);

  const handleInstallPlugin = useCallback(async () => {
    const path = pluginSourcePath.trim();
    if (path === '') {
      setPluginError('Plugin directory is required');
      return;
    }
    setPluginError(null);
    setPluginActionName('install');
    try {
      await onInstallPlugin(path);
      setPluginSourcePath('');
      await refreshPlugins();
    } catch (error) {
      setPluginError(error instanceof Error ? error.message : 'Failed to install plugin');
    } finally {
      setPluginActionName(null);
    }
  }, [onInstallPlugin, pluginSourcePath, refreshPlugins]);

  const handleRemovePlugin = useCallback(async (name: string) => {
    setPluginError(null);
    setPluginActionName(name);
    try {
      await onRemovePlugin(name);
      await refreshPlugins();
    } catch (error) {
      setPluginError(error instanceof Error ? error.message : 'Failed to remove plugin');
    } finally {
      setPluginActionName(null);
    }
  }, [onRemovePlugin, refreshPlugins]);

  const handlePluginPriorityChange = useCallback((name: string, value: string) => {
    setPluginPriorityDrafts((prev) => ({ ...prev, [name]: value }));
  }, []);

  const handleSavePluginPriority = useCallback(async (name: string) => {
    const raw = (pluginPriorityDrafts[name] ?? '').trim();
    const priority = Number(raw);
    if (!Number.isInteger(priority)) {
      setPluginError('Plugin priority must be an integer');
      return;
    }
    setPluginError(null);
    setPluginActionName(name);
    try {
      await onSetPluginPriority(name, priority);
      await refreshPlugins();
    } catch (error) {
      setPluginError(error instanceof Error ? error.message : 'Failed to update plugin priority');
    } finally {
      setPluginActionName(null);
    }
  }, [onSetPluginPriority, pluginPriorityDrafts, refreshPlugins]);

  const connectedEndpointCount = endpoints.filter((endpoint) => endpoint.status === 'connected').length;
  const activePluginCount = plugins.filter((plugin) => plugin.connected || plugin.running).length;
  const availableAgentCount = orderedAgentList.filter((agent) => isAgentAvailable(agentAvailability, agent)).length;
  const mutedItemCount = mutedRepos.length + mutedAuthors.length;
  const pluginProblemCount = pluginIssues.length + plugins.filter((plugin) => plugin.health_status === 'unhealthy').length;
  const hasProjectsDirChange = projectsDir !== actualProjectsDir;
  const hasReviewModelChange = reviewLoopModel !== actualReviewLoopModel || reviewerModel !== actualReviewerModel;

  const settingsNavGroups = useMemo<SettingsNavGroup[]>(() => [
    {
      label: 'General',
      items: [
        {
          id: 'general',
          label: 'Appearance and projects',
          title: 'Appearance and project roots',
          description: 'Theme selection and the directory attn uses when opening repositories and worktrees.',
          count: 2,
          keywords: 'theme appearance dark light system projects directory worktrees roots',
        },
      ],
    },
    {
      label: 'Connectivity',
      items: [
        {
          id: 'connectivity',
          label: 'Mobile, hosts, remotes',
          title: 'Mobile web, hosts, and remote endpoints',
          description: 'Controls for mobile browser access, GitHub host detection, and remote attn peers.',
          count: Math.max(3, endpoints.length + connectedHosts.length + 1),
          keywords: 'tailscale mobile web github hosts ssh remote endpoint daemon',
        },
      ],
    },
    {
      label: 'Extensions',
      items: [
        {
          id: 'plugins',
          label: 'Plugins',
          title: 'Plugins',
          description: 'Install user-owned plugins and tune provider dispatch priority.',
          count: Math.max(1, plugins.length + pluginIssues.length),
          keywords: 'plugins extensions providers priority install healthcheck',
        },
      ],
    },
    {
      label: 'Agents',
      items: [
        {
          id: 'agents',
          label: 'Executables and defaults',
          title: 'Agent runtime',
          description: 'Agent executable paths, the default session agent, capability reporting, and PTY runtime mode.',
          count: orderedAgentList.length + 3,
          keywords: 'agents executables claude codex cursor default capabilities pty backend editor',
        },
      ],
    },
    {
      label: 'Review',
      items: [
        {
          id: 'review',
          label: 'Loop prompts and models',
          title: 'Review loop prompts and models',
          description: 'Saved review-loop prompt presets and model overrides for review automation.',
          count: Math.max(2, reviewLoopPresets.length + 2),
          keywords: 'review loop prompt preset model reviewer iterations',
        },
      ],
    },
    {
      label: 'Hygiene',
      items: [
        {
          id: 'hygiene',
          label: 'Muted repos and authors',
          title: 'Muted repositories and authors',
          description: 'Repositories and authors hidden from the attention queue.',
          count: mutedItemCount,
          keywords: 'muted repositories repos authors hide unmute hygiene',
        },
      ],
    },
  ], [
    connectedHosts.length,
    endpoints.length,
    mutedItemCount,
    orderedAgentList.length,
    pluginIssues.length,
    plugins.length,
    reviewLoopPresets.length,
  ]);

  const filteredNavGroups = useMemo(() => {
    const query = settingsSearch.trim().toLowerCase();
    if (query === '') return settingsNavGroups;
    return settingsNavGroups
      .map((group) => ({
        ...group,
        items: group.items.filter((item) => (
          `${item.label} ${item.title} ${item.description} ${item.keywords}`.toLowerCase().includes(query)
        )),
      }))
      .filter((group) => group.items.length > 0);
  }, [settingsNavGroups, settingsSearch]);

  const flatNavItems = useMemo(
    () => settingsNavGroups.flatMap((group) => group.items),
    [settingsNavGroups],
  );

  const selectedNavItem = flatNavItems.find((item) => item.id === selectedSection) || flatNavItems[0];

  const renderSectionStatusPills = () => {
    switch (selectedSection) {
      case 'connectivity':
        return (
          <>
            <span className={`settings-pill ${tailscaleEnabled && tailscaleStatus !== 'error' ? 'good' : ''}`}>
              {tailscaleEnabled ? tailscaleStatus : 'mobile off'}
            </span>
            <span className={`settings-pill ${endpoints.length === 0 || connectedEndpointCount === endpoints.length ? 'good' : 'warn'}`}>
              {connectedEndpointCount}/{endpoints.length} remotes
            </span>
          </>
        );
      case 'plugins':
        return (
          <>
            <span className={`settings-pill ${pluginProblemCount === 0 ? 'good' : 'bad'}`}>
              {pluginProblemCount === 0 ? 'healthy' : `${pluginProblemCount} issue${pluginProblemCount === 1 ? '' : 's'}`}
            </span>
            <span className="settings-pill">{activePluginCount}/{plugins.length} running</span>
          </>
        );
      case 'agents':
        return (
          <>
            <span className={`settings-pill ${hasAvailableAgents ? 'good' : 'bad'}`}>
              {availableAgentCount}/{orderedAgentList.length} available
            </span>
            <span className="settings-pill">{ptyBackendMode}</span>
          </>
        );
      case 'review':
        return (
          <>
            <span className="settings-pill">{reviewLoopPresets.length} saved prompts</span>
            <span className={`settings-pill ${hasReviewModelChange ? 'warn' : 'good'}`}>
              {hasReviewModelChange ? 'unsaved edits' : 'models saved'}
            </span>
          </>
        );
      case 'hygiene':
        return (
          <>
            <span className="settings-pill">{mutedRepos.length} repos</span>
            <span className="settings-pill">{mutedAuthors.length} authors</span>
          </>
        );
      case 'general':
      default:
        return (
          <>
            <span className="settings-pill">{themePreference}</span>
            <span className={`settings-pill ${hasProjectsDirChange ? 'warn' : 'good'}`}>
              {hasProjectsDirChange ? 'project path edited' : 'project path saved'}
            </span>
          </>
        );
    }
  };

  const renderGeneralSettings = () => (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Appearance</div>
          <h3>Theme</h3>
          <p className="settings-description">
            Choose how attn renders the application chrome.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-segmented" role="radiogroup" aria-label="Theme preference">
            <button
              type="button"
              className={`settings-segmented-option ${themePreference === 'dark' ? 'active' : ''}`}
              onClick={() => onSetTheme('dark')}
              aria-checked={themePreference === 'dark'}
            >
              Dark
            </button>
            <button
              type="button"
              className={`settings-segmented-option ${themePreference === 'light' ? 'active' : ''}`}
              onClick={() => onSetTheme('light')}
              aria-checked={themePreference === 'light'}
            >
              Light
            </button>
            <button
              type="button"
              className={`settings-segmented-option ${themePreference === 'system' ? 'active' : ''}`}
              onClick={() => onSetTheme('system')}
              aria-checked={themePreference === 'system'}
            >
              System
            </button>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Projects</div>
          <h3>Projects Directory</h3>
          <p className="settings-description">
            Directory where Git repositories are cloned and opened in worktrees.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-inline-form projects-dir-input">
            <input
              data-testid="settings-projects-directory-input"
              type="text"
              value={projectsDir}
              onChange={handleInputChange}
              onBlur={handleInputBlur}
              onKeyDown={handleKeyDown}
              placeholder="/Users/you/projects"
              className="settings-input"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <button className="settings-action" onClick={handleBrowse}>
              Browse
            </button>
          </div>
        </div>
      </section>
    </>
  );

  const renderConnectivitySettings = () => (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Mobile</div>
          <h3>Mobile Web Client</h3>
          <p className="settings-description">
            Expose this daemon through the existing Tailscale device identity for mobile browser access.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Tailscale Serve</p>
              <p className="settings-row-copy">
                {tailscaleURL || tailscaleDomain || 'Uses the host Tailscale client and does not register a second tailnet device for attn.'}
              </p>
            </div>
            <button className="settings-action" onClick={handleToggleTailscale}>
              {tailscaleEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>
          <div className="settings-row-card compact">
            <div>
              <p className="settings-row-title">Sign-in status</p>
              <p className="settings-row-copy">Status: {tailscaleStatus}</p>
            </div>
            <span className={`settings-pill ${tailscaleEnabled && tailscaleStatus !== 'error' ? 'good' : ''}`}>
              {tailscaleEnabled ? tailscaleStatus : 'disabled'}
            </span>
          </div>
          <div className="settings-hint">
            This uses the host Tailscale client and does not register a second tailnet device for attn.
          </div>
          {tailscaleDomain && (
            <div className="settings-meta-row">
              <span className="settings-meta-label">Device DNS</span>
              <code>{tailscaleDomain}</code>
            </div>
          )}
          {tailscaleURL && (
            <div className="settings-meta-row">
              <span className="settings-meta-label">Web URL</span>
              <a href={tailscaleURL} target="_blank" rel="noreferrer">{tailscaleURL}</a>
            </div>
          )}
          {tailscaleAuthURL && (
            <div className="settings-warning">
              Sign this machine into Tailscale:{' '}
              <a href={tailscaleAuthURL} target="_blank" rel="noreferrer">{tailscaleAuthURL}</a>
            </div>
          )}
          {tailscaleError && (
            <div className="settings-warning">{tailscaleError}</div>
          )}
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">GitHub</div>
          <h3>GitHub Hosts</h3>
          <p className="settings-description">
            Authenticated hosts used by PR actions, review lookup, and repository metadata.
          </p>
        </div>
        <div className="settings-block-body">
          {connectedHosts.length === 0 ? (
            <p className="settings-empty">No authenticated hosts detected.</p>
          ) : (
            <div className="settings-token-list">
              {connectedHosts.map((host) => (
                <span key={host} className="settings-token">{host}</span>
              ))}
            </div>
          )}
          <div className="settings-hint">Add hosts with `gh auth login --hostname &lt;host&gt;`.</div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Remote</div>
          <h3>Remote Endpoints</h3>
          <p className="settings-description">
            SSH targets that the local daemon bootstraps and keeps connected as remote attn peers.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-form-grid endpoint-form">
            <input
              type="text"
              value={newEndpointName}
              onChange={(e) => setNewEndpointName(e.target.value)}
              placeholder="gpu-box"
              className="settings-input"
              aria-label="Endpoint name"
              disabled={endpointActionInFlight}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <input
              type="text"
              value={newEndpointTarget}
              onChange={(e) => setNewEndpointTarget(e.target.value)}
              placeholder="user@gpu-box"
              className="settings-input"
              aria-label="SSH target"
              disabled={endpointActionInFlight}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <input
              type="text"
              value={newEndpointProfile}
              onChange={(e) => setNewEndpointProfile(e.target.value)}
              placeholder="default"
              pattern="[a-z0-9][a-z0-9-]{0,15}"
              className="settings-input"
              aria-label="Profile"
              disabled={endpointActionInFlight}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <button
              className="settings-action"
              onClick={() => void handleAddEndpoint()}
              disabled={endpointActionInFlight}
            >
              Add Endpoint
            </button>
          </div>
          {endpointError && <div className="settings-warning">{endpointError}</div>}
          {endpoints.length === 0 ? (
            <p className="settings-empty">No remote endpoints configured.</p>
          ) : (
            <div className="endpoint-list">
              {endpoints.map((endpoint) => {
                const isEditing = editingEndpointID === endpoint.id;
                const isBusy = endpointActionID === endpoint.id;
                const availableAgents = endpoint.capabilities?.agents_available || [];
                const remoteWebEnabled = endpoint.capabilities?.tailscale_enabled === true;
                const remoteWebStatus = endpoint.capabilities?.tailscale_status || (remoteWebEnabled ? 'starting' : 'disabled');
                const remoteWebURL = endpoint.capabilities?.tailscale_url;
                const remoteWebAuthURL = endpoint.capabilities?.tailscale_auth_url;
                const remoteWebError = endpoint.capabilities?.tailscale_error;
                const canToggleRemoteWeb = endpoint.status === 'connected' && !endpointActionInFlight;
                const canRebootstrap = endpoint.enabled !== false && !endpointActionInFlight;
                return (
                  <div key={endpoint.id} className={`endpoint-card status-${endpoint.status}`}>
                    <div className="endpoint-card-header">
                      <div className="endpoint-card-title">
                        <span className="endpoint-name">{endpoint.name}</span>
                        <span className="settings-pill">{endpoint.profile || 'default'}</span>
                        <span className={`endpoint-status-badge status-${endpoint.status}`}>
                          {endpoint.status}
                        </span>
                      </div>
                      <div className="endpoint-card-actions">
                        {isEditing ? (
                          <>
                            <button className="settings-action" onClick={() => void handleSaveEndpoint(endpoint.id)} disabled={isBusy}>
                              Save
                            </button>
                            <button className="settings-action" onClick={cancelEditEndpoint} disabled={endpointActionInFlight}>
                              Cancel
                            </button>
                          </>
                        ) : (
                          <button className="settings-action" onClick={() => beginEditEndpoint(endpoint)} disabled={endpointActionInFlight}>
                            Edit
                          </button>
                        )}
                        <button className="settings-action" onClick={() => void handleToggleEndpoint(endpoint)} disabled={endpointActionInFlight}>
                          {endpoint.enabled === false ? 'Enable' : 'Disable'}
                        </button>
                        <button
                          className="settings-action"
                          onClick={() => void handleRebootstrapEndpoint(endpoint)}
                          disabled={!canRebootstrap}
                        >
                          Re-bootstrap
                        </button>
                        <button
                          className="settings-action"
                          onClick={() => void handleSetEndpointRemoteWeb(endpoint.id, !remoteWebEnabled)}
                          disabled={!canToggleRemoteWeb}
                        >
                          {remoteWebEnabled ? 'Disable Web' : 'Enable Web'}
                        </button>
                        <button className="settings-action danger" onClick={() => void handleRemoveEndpoint(endpoint.id)} disabled={endpointActionInFlight}>
                          Remove
                        </button>
                      </div>
                    </div>
                    {isEditing ? (
                      <div className="settings-form-grid endpoint-form-inline">
                        <input
                          type="text"
                          value={editingEndpointName}
                          onChange={(e) => setEditingEndpointName(e.target.value)}
                          className="settings-input"
                          aria-label="Edit endpoint name"
                          disabled={endpointActionInFlight}
                          autoCapitalize="none"
                          autoCorrect="off"
                          spellCheck={false}
                        />
                        <input
                          type="text"
                          value={editingEndpointTarget}
                          onChange={(e) => setEditingEndpointTarget(e.target.value)}
                          className="settings-input"
                          aria-label="Edit SSH target"
                          disabled={endpointActionInFlight}
                          autoCapitalize="none"
                          autoCorrect="off"
                          spellCheck={false}
                        />
                        <input
                          type="text"
                          value={editingEndpointProfile}
                          onChange={(e) => setEditingEndpointProfile(e.target.value)}
                          className="settings-input"
                          aria-label="Edit profile"
                          placeholder="default"
                          pattern="[a-z0-9][a-z0-9-]{0,15}"
                          disabled={endpointActionInFlight}
                          autoCapitalize="none"
                          autoCorrect="off"
                          spellCheck={false}
                        />
                      </div>
                    ) : (
                      <div className="endpoint-summary">
                        <div className="settings-meta-row">
                          <span className="settings-meta-label">SSH</span>
                          <code>{endpoint.ssh_target}</code>
                        </div>
                        <div className="settings-meta-row">
                          <span className="settings-meta-label">Enabled</span>
                          <span>{endpoint.enabled === false ? 'No' : 'Yes'}</span>
                        </div>
                        {endpoint.status_message && (
                          <div className="settings-meta-row">
                            <span className="settings-meta-label">Status</span>
                            <span>{endpoint.status_message}</span>
                          </div>
                        )}
                        {endpoint.capabilities && (
                          <>
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">Protocol</span>
                              <span>{endpoint.capabilities.protocol_version}</span>
                            </div>
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">PTY</span>
                              <span>{endpoint.capabilities.pty_backend_mode || 'unknown'}</span>
                            </div>
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">Sessions</span>
                              <span>{endpoint.session_count ?? 0}</span>
                            </div>
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">Remote Web</span>
                              <span>{remoteWebStatus}</span>
                            </div>
                            <div className="settings-meta-row">
                              <span className="settings-meta-label">Agents</span>
                              <span>{availableAgents.length > 0 ? availableAgents.join(', ') : 'none reported'}</span>
                            </div>
                            {remoteWebURL && (
                              <div className="settings-meta-row">
                                <span className="settings-meta-label">Remote URL</span>
                                <a href={remoteWebURL} target="_blank" rel="noreferrer">{remoteWebURL}</a>
                              </div>
                            )}
                            {remoteWebAuthURL && (
                              <div className="settings-warning">
                                Sign this host into Tailscale:{' '}
                                <a href={remoteWebAuthURL} target="_blank" rel="noreferrer">{remoteWebAuthURL}</a>
                              </div>
                            )}
                            {endpoint.capabilities.tailscale_domain && !remoteWebURL && (
                              <div className="settings-meta-row">
                                <span className="settings-meta-label">Remote DNS</span>
                                <code>{endpoint.capabilities.tailscale_domain}</code>
                              </div>
                            )}
                            {remoteWebError && (
                              <div className="settings-warning">{remoteWebError}</div>
                            )}
                            {endpoint.capabilities.projects_directory && (
                              <div className="settings-meta-row">
                                <span className="settings-meta-label">Projects</span>
                                <code>{endpoint.capabilities.projects_directory}</code>
                              </div>
                            )}
                          </>
                        )}
                        {!canToggleRemoteWeb && (
                          <div className="settings-hint">Connect to the remote daemon before changing its web access.</div>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </section>
    </>
  );

  const renderPluginSettings = () => (
    <section className="settings-block">
      <div className="settings-block-intro">
        <div className="settings-kicker">Extensions</div>
        <h3>Plugins</h3>
        <p className="settings-description">
          Install user-owned plugins from local directories and control provider dispatch priority.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-inline-form plugin-form">
          <input
            type="text"
            value={pluginSourcePath}
            onChange={(e) => setPluginSourcePath(e.target.value)}
            placeholder="/Users/you/src/my-attn-plugin"
            className="settings-input"
            aria-label="Plugin directory"
            disabled={pluginActionName !== null}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
          <button className="settings-action" onClick={() => void handleBrowsePluginPath()} disabled={pluginActionName !== null}>
            Browse
          </button>
          <button className="settings-action" onClick={() => void handleInstallPlugin()} disabled={pluginActionName !== null}>
            Install Plugin
          </button>
        </div>
        {pluginError && <div className="settings-warning">{pluginError}</div>}
        {pluginIssues.map((issue) => (
          <div key={issue.path} className="settings-warning">
            {issue.path}: {issue.error}
          </div>
        ))}
        {pluginsLoading ? (
          <p className="settings-empty">Loading plugins...</p>
        ) : plugins.length === 0 ? (
          <p className="settings-empty">No plugins installed.</p>
        ) : (
          <div className="plugin-list">
            {plugins.map((plugin) => {
              const busy = pluginActionName === plugin.name;
              const draftPriority = pluginPriorityDrafts[plugin.name] ?? String(plugin.priority);
              const healthStatus = plugin.health_status || 'unknown';
              return (
                <div key={plugin.name} className="plugin-card">
                  <div className="plugin-card-header">
                    <div className="plugin-card-title">
                      <span className="endpoint-name">{plugin.name}</span>
                      <span className="settings-pill">v{plugin.version}</span>
                      <span className={`plugin-status-badge ${plugin.connected ? 'connected' : plugin.running ? 'starting' : 'stopped'}`}>
                        {plugin.connected ? 'connected' : plugin.running ? 'starting' : 'stopped'}
                      </span>
                      <span className={`plugin-health-badge ${healthStatus}`}>
                        {healthStatus}
                      </span>
                    </div>
                    <button className="settings-action danger" onClick={() => void handleRemovePlugin(plugin.name)} disabled={pluginActionName !== null}>
                      Remove
                    </button>
                  </div>
                  {plugin.description && <p className="settings-description plugin-description">{plugin.description}</p>}
                  {plugin.health_message && (
                    <div className="settings-warning">
                      Healthcheck: {plugin.health_message}
                    </div>
                  )}
                  <div className="plugin-meta-grid">
                    <div className="settings-meta-row">
                      <span className="settings-meta-label">Path</span>
                      <code>{plugin.dir}</code>
                    </div>
                    <label className="plugin-priority-control">
                      <span className="settings-meta-label">Priority</span>
                      <input
                        type="number"
                        value={draftPriority}
                        onChange={(e) => handlePluginPriorityChange(plugin.name, e.target.value)}
                        className="settings-input plugin-priority-input"
                        aria-label={`${plugin.name} priority`}
                        disabled={pluginActionName !== null}
                      />
                      <button
                        className="settings-action"
                        onClick={() => void handleSavePluginPriority(plugin.name)}
                        disabled={pluginActionName !== null || draftPriority === String(plugin.priority)}
                      >
                        Save
                      </button>
                    </label>
                  </div>
                  {busy && <div className="settings-hint">Updating {plugin.name}...</div>}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );

  const renderAgentSettings = () => (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Paths</div>
          <h3>Executables</h3>
          <p className="settings-description">
            Override the CLI used to launch agents. Empty values use the default on PATH.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-field-grid">
            {orderedAgentList.map((agent) => {
              const available = isAgentAvailable(agentAvailability, agent);
              const inputId = `settings-${agent}-exec`;
              const value = agentExecutables[agent] || '';
              return (
                <div className="settings-field" key={agent}>
                  <label className="settings-label" htmlFor={inputId}>{agentLabel(agent)}</label>
                  <span className={`settings-status ${available ? 'available' : 'missing'}`}>
                    {available ? 'Found in PATH' : 'Not found in PATH'}
                  </span>
                  <input
                    id={inputId}
                    type="text"
                    value={value}
                    onChange={(e) => handleExecutableChange(agent, e.target.value)}
                    onBlur={() => commitExecutable(agent)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        commitExecutable(agent);
                      }
                    }}
                    placeholder={agent}
                    className="settings-input"
                    autoCapitalize="none"
                    autoCorrect="off"
                    spellCheck={false}
                  />
                </div>
              );
            })}
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-editor-exec">Editor</label>
              <span className="settings-status">Used when opening files</span>
              <input
                id="settings-editor-exec"
                type="text"
                value={editorExecutable}
                onChange={handleEditorChange}
                onBlur={handleEditorBlur}
                onKeyDown={handleEditorKeyDown}
                placeholder="$EDITOR"
                className="settings-input"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
            </div>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Default</div>
          <h3>Default Session Agent</h3>
          <p className="settings-description">
            Used for new sessions and opening PRs. Individual sessions can still choose a different agent.
          </p>
        </div>
        <div className="settings-block-body">
          {!hasAvailableAgents && (
            <div className="settings-warning">No supported agent CLI found in PATH.</div>
          )}
          <div className="settings-segmented" role="radiogroup" aria-label="Default session agent">
            {orderedAgentList.map((agent) => {
              const available = isAgentAvailable(agentAvailability, agent);
              return (
                <button
                  key={agent}
                  type="button"
                  className={`settings-segmented-option ${defaultAgent === agent ? 'active' : ''}`}
                  onClick={() => handleDefaultAgentChange(agent)}
                  aria-checked={defaultAgent === agent}
                  disabled={!available}
                  title={!available ? `${agentLabel(agent)} CLI not found in PATH` : undefined}
                >
                  {agentLabel(agent)}
                </button>
              );
            })}
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Capabilities</div>
          <h3>Agent Capabilities</h3>
          <p className="settings-description">
            Optional integration features reported by each agent.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="agent-capabilities-list">
            {orderedAgentList.map((agent) => {
              const caps = actualAgentCapabilities[agent] || {};
              const knownCaps = agentCapabilityOrder.filter((cap) => cap in caps);
              const extraCaps = Object.keys(caps)
                .filter((cap) => !agentCapabilityOrder.includes(cap))
                .sort((a, b) => a.localeCompare(b));
              const capKeys = [...knownCaps, ...extraCaps];
              return (
                <div key={agent} className="agent-capabilities-item">
                  <div className="agent-capabilities-agent">{agentLabel(agent)}</div>
                  {capKeys.length === 0 ? (
                    <span className="agent-capability-pill">No capability metadata</span>
                  ) : (
                    <div className="agent-capabilities-pills">
                      {capKeys.map((cap) => (
                        <span
                          key={`${agent}-${cap}`}
                          className={`agent-capability-pill ${caps[cap] ? 'enabled' : 'disabled'}`}
                          title={caps[cap] ? 'Enabled' : 'Disabled'}
                        >
                          {agentCapabilityLabel(cap)}: {caps[cap] ? 'on' : 'off'}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Terminal</div>
          <h3>PTY Backend</h3>
          <p className="settings-description">
            Shows whether terminal sessions run in external worker processes or directly in the daemon.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card compact">
            <div>
              <p className="settings-row-title">Runtime mode</p>
              <p className="settings-row-copy">{ptyBackendHint}</p>
            </div>
            <span className={`settings-status mode-${ptyBackendMode}`}>
              {ptyBackendLabel}
            </span>
          </div>
        </div>
      </section>
    </>
  );

  const renderReviewSettings = () => (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Prompts</div>
          <h3>Review Loop Prompts</h3>
          <p className="settings-description">
            Saved custom prompts for session review loops. Built-in presets stay available in the loop bar.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="review-loop-settings">
            <div className="review-loop-settings-list">
              <div className="settings-row-inline">
                <label className="settings-label" htmlFor="review-loop-preset-select">Saved prompts</label>
                <button className="settings-action" onClick={handleNewReviewLoopPreset}>New</button>
              </div>
              {reviewLoopPresets.length === 0 ? (
                <p className="settings-empty">No saved review-loop prompts</p>
              ) : (
                <select
                  id="review-loop-preset-select"
                  className="settings-input"
                  value={selectedReviewLoopPresetID}
                  onChange={(e) => handleSelectReviewLoopPreset(e.target.value)}
                >
                  {reviewLoopPresets.map((preset) => (
                    <option key={preset.id} value={preset.id}>{preset.name}</option>
                  ))}
                </select>
              )}
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="review-loop-preset-name">Prompt name</label>
              <input
                id="review-loop-preset-name"
                type="text"
                className="settings-input"
                value={reviewLoopPresetName}
                onChange={(e) => setReviewLoopPresetName(e.target.value)}
                placeholder="Architect pass"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="review-loop-preset-iterations">Default iterations</label>
              <input
                id="review-loop-preset-iterations"
                type="number"
                min={1}
                className="settings-input"
                value={reviewLoopIterations}
                onChange={(e) => setReviewLoopIterations(Number(e.target.value) || 1)}
              />
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="review-loop-preset-prompt">Prompt</label>
              <textarea
                id="review-loop-preset-prompt"
                className="settings-textarea"
                value={reviewLoopPrompt}
                onChange={(e) => setReviewLoopPrompt(e.target.value)}
                rows={6}
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                placeholder="Do a full review of these changes..."
              />
            </div>
            <div className="settings-row-inline review-loop-settings-actions">
              <button
                className="settings-action"
                onClick={handleSaveReviewLoopPreset}
                disabled={!reviewLoopPresetName.trim() || !reviewLoopPrompt.trim()}
              >
                Save Prompt
              </button>
              <button
                className="settings-action danger"
                onClick={handleDeleteReviewLoopPreset}
                disabled={!selectedReviewLoopPresetID}
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Models</div>
          <h3>Review Models</h3>
          <p className="settings-description">
            Override the Claude models used for SDK-based review work. Empty values use built-in defaults.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-field-grid two-column">
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-review-loop-model">Review loop model</label>
              <input
                id="settings-review-loop-model"
                type="text"
                value={reviewLoopModel}
                onChange={(e) => setReviewLoopModel(e.target.value)}
                onBlur={commitReviewLoopModel}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    commitReviewLoopModel();
                  }
                }}
                placeholder="claude-sonnet-4-6"
                className="settings-input"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-reviewer-model">Reviewer model</label>
              <input
                id="settings-reviewer-model"
                type="text"
                value={reviewerModel}
                onChange={(e) => setReviewerModel(e.target.value)}
                onBlur={commitReviewerModel}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    commitReviewerModel();
                  }
                }}
                placeholder="claude-opus-4-6"
                className="settings-input"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
            </div>
          </div>
        </div>
      </section>
    </>
  );

  const renderHygieneSettings = () => (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Repositories</div>
          <h3>Muted Repositories</h3>
          <p className="settings-description">
            Repositories hidden from the attention queue.
          </p>
        </div>
        <div className="settings-block-body">
          {mutedRepos.length === 0 ? (
            <p className="settings-empty">No muted repositories</p>
          ) : (
            <ul className="muted-items-list" data-testid="settings-muted-repositories-list">
              {mutedRepos.map(repo => (
                <li key={repo} className="muted-item" data-testid="settings-muted-repository-item">
                  <span className="muted-item-name">{repo}</span>
                  <button
                    className="settings-action"
                    data-testid="settings-unmute-repository-button"
                    onClick={() => onUnmuteRepo(repo)}
                  >
                    Unmute
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Authors</div>
          <h3>Muted Authors</h3>
          <p className="settings-description">
            Authors hidden from the attention queue.
          </p>
        </div>
        <div className="settings-block-body">
          {mutedAuthors.length === 0 ? (
            <p className="settings-empty">No muted authors</p>
          ) : (
            <ul className="muted-items-list" data-testid="settings-muted-authors-list">
              {mutedAuthors.map(author => (
                <li key={author} className="muted-item" data-testid="settings-muted-author-item">
                  <span className="muted-item-name">{author}</span>
                  <button
                    className="settings-action"
                    data-testid="settings-unmute-author-button"
                    onClick={() => onUnmuteAuthor(author)}
                  >
                    Unmute
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </>
  );

  const renderSelectedSection = () => {
    switch (selectedSection) {
      case 'general':
        return renderGeneralSettings();
      case 'plugins':
        return renderPluginSettings();
      case 'agents':
        return renderAgentSettings();
      case 'review':
        return renderReviewSettings();
      case 'hygiene':
        return renderHygieneSettings();
      case 'connectivity':
      default:
        return renderConnectivitySettings();
    }
  };

  if (!isOpen) return null;

  return (
    <div className="settings-overlay" data-testid="settings-overlay" onClick={onClose}>
      <div className="settings-modal" data-testid="settings-modal" onClick={e => e.stopPropagation()}>
        <div className="settings-header" data-testid="settings-header">
          <div className="settings-title">
            <h2>Settings</h2>
            <span className="settings-profile">local daemon</span>
          </div>
          <div className="settings-top-actions">
            <input
              className="settings-search"
              type="search"
              value={settingsSearch}
              onChange={(e) => setSettingsSearch(e.target.value)}
              placeholder="Search settings"
              aria-label="Search settings"
            />
            <button className="settings-close" data-testid="settings-close" onClick={onClose} aria-label="Close settings">
              x
            </button>
          </div>
        </div>

        <div className="settings-layout">
          <nav className="settings-nav" aria-label="Settings sections">
            {filteredNavGroups.length === 0 ? (
              <p className="settings-empty nav-empty">No matching settings.</p>
            ) : (
              filteredNavGroups.map((group) => (
                <div className="settings-nav-group" key={group.label}>
                  <div className="settings-nav-label">{group.label}</div>
                  {group.items.map((item) => (
                    <button
                      key={item.id}
                      type="button"
                      data-testid={`settings-nav-${item.id}`}
                      className={`settings-nav-item ${selectedSection === item.id ? 'active' : ''}`}
                      onClick={() => setSelectedSection(item.id)}
                    >
                      <span>{item.label}</span>
                      <span className="settings-nav-count">{item.count}</span>
                    </button>
                  ))}
                </div>
              ))
            )}
          </nav>

          <main className="settings-body" data-testid="settings-body">
            <div className="settings-content-head">
              <div>
                <div className="settings-kicker">{selectedNavItem?.label}</div>
                <h1>{selectedNavItem?.title}</h1>
                <p className="settings-lead">{selectedNavItem?.description}</p>
              </div>
              <div className="settings-status-pair">
                {renderSectionStatusPills()}
              </div>
            </div>
            <div className="settings-section-content" data-testid={`settings-section-${selectedSection}`}>
              {renderSelectedSection()}
            </div>
          </main>
        </div>
      </div>
    </div>
  );
}
