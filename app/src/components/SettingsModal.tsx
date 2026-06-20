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
import {
  defaultKeeperDutyModel,
  keeperDutyModelSelection,
  KEEPER_DUTIES,
  KEEPER_DUTY_BY_KEY,
  parseKeeperConfig,
  serializeKeeperConfig,
  type KeeperConfig,
  type KeeperDutyDescriptor,
  type KeeperDutyKey,
} from '../utils/keeperDuties';
import './SettingsModal.css';

interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  mutedRepos: string[];
  githubHosts: string[];
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
  onInstallPlugin: (source: string) => Promise<{ success: boolean; name?: string }>;
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

interface KeeperDraft {
  agent: SessionAgent | '';
  model: string;
}

const emptyKeeperDrafts: Record<KeeperDutyKey, KeeperDraft> = {
  summarize: { agent: '', model: '' },
  narrate: { agent: '', model: '' },
  compact: { agent: '', model: '' },
};

// initialKeeperDraft seeds a row's editable draft from the saved config. An always-on
// duty with no override pre-selects its built-in default agent (or the first eligible
// one) plus that agent's recommended model, so the row shows what "unset" resolves to;
// an opt-in duty with no override starts blank (the Disabled state).
function initialKeeperDraft(
  duty: KeeperDutyDescriptor,
  saved: KeeperConfig | null,
  agents: readonly SessionAgent[],
): KeeperDraft {
  if (saved) return { agent: saved.agent, model: saved.model };
  if (duty.optInOnly) return { agent: '', model: '' };
  const agent = agents.includes('claude') ? 'claude' : agents[0] ?? '';
  return { agent, model: agent ? defaultKeeperDutyModel(duty.key, agent) : '' };
}

export function SettingsModal({
  isOpen,
  onClose,
  mutedRepos,
  githubHosts,
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
  const [notebookRoot, setNotebookRoot] = useState(settings['notebook.root'] || '');
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
  // One draft (agent + model) per keeper duty, edited locally and committed per-row.
  const [keeperDrafts, setKeeperDrafts] = useState<Record<KeeperDutyKey, KeeperDraft>>(
    emptyKeeperDrafts,
  );
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
  // notebook.root is the user override (blank => default); notebook.root.effective is
  // the daemon-resolved absolute folder the notebook actually lives in right now.
  const actualNotebookRoot = settings['notebook.root'] || '';
  const effectiveNotebookRoot = settings['notebook.root.effective'] || '';
  const tailscaleEnabled = (settings.tailscale_enabled || 'false') === 'true';
  const workflowsEnabled = (settings.workflows_enabled || 'false') === 'true';
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
  // The saved (persisted) config for every keeper duty, keyed by duty. A null entry
  // means the setting is blank (default for always-on duties, disabled for opt-in).
  const actualKeeperConfigs = useMemo(() => {
    const configs = {} as Record<KeeperDutyKey, KeeperConfig | null>;
    for (const duty of KEEPER_DUTIES) {
      configs[duty.key] = parseKeeperConfig(settings[duty.settingKey]);
    }
    return configs;
  }, [settings]);
  // The keeper master switch is daemon-normalized to its effective value (default ON),
  // so a missing key reads as enabled rather than off.
  const keeperTasksEnabled = (settings['notebook.tasks_enabled'] ?? 'true') !== 'false';
  const resolvedDefaultAgent = resolvePreferredAgent(actualDefaultAgent, agentAvailability, 'codex');
  const orderedAgentList = useMemo(
    () => orderedAgents(agentAvailability, resolvedDefaultAgent, 'codex'),
    [agentAvailability, resolvedDefaultAgent],
  );
  const executableAgentList = useMemo(
    () => orderedAgentList.filter((agent) => ['codex', 'claude', 'copilot'].includes(agent)),
    [orderedAgentList],
  );
  // Agents eligible to run any keeper duty: installed, headless-task capable, and one
  // of claude/codex. Any agent already configured on a duty is kept in the list even
  // if it has since become unavailable, so its row still shows the saved selection.
  const keeperAgents = useMemo(() => {
    const eligible = orderedAgentList.filter((agent) => (
      ['codex', 'claude'].includes(agent)
      && isAgentAvailable(agentAvailability, agent)
      && actualAgentCapabilities[agent]?.headless_task === true
    ));
    for (const duty of KEEPER_DUTIES) {
      const configured = actualKeeperConfigs[duty.key]?.agent;
      if (configured && ['codex', 'claude'].includes(configured) && !eligible.includes(configured)) {
        eligible.push(configured);
      }
    }
    return eligible;
  }, [actualAgentCapabilities, actualKeeperConfigs, agentAvailability, orderedAgentList]);
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
    setNotebookRoot(actualNotebookRoot);
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
    setKeeperDrafts({
      summarize: initialKeeperDraft(KEEPER_DUTY_BY_KEY.summarize, actualKeeperConfigs.summarize, keeperAgents),
      narrate: initialKeeperDraft(KEEPER_DUTY_BY_KEY.narrate, actualKeeperConfigs.narrate, keeperAgents),
      compact: initialKeeperDraft(KEEPER_DUTY_BY_KEY.compact, actualKeeperConfigs.compact, keeperAgents),
    });
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
  }, [isOpen, actualProjectsDir, actualNotebookRoot, actualAgentExecutables, actualEditorExecutable, resolvedDefaultAgent, actualReviewLoopPresets, actualReviewLoopModel, actualReviewerModel, actualKeeperConfigs, keeperAgents]);

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

  const handleToggleWorkflows = useCallback(() => {
    onSetSetting('workflows_enabled', workflowsEnabled ? 'false' : 'true');
  }, [onSetSetting, workflowsEnabled]);

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

  const handleBrowseNotebookRoot = useCallback(async () => {
    const selected = await open({
      directory: true,
      multiple: false,
      title: 'Select Notebook Folder',
    });
    if (selected && typeof selected === 'string') {
      setNotebookRoot(selected);
      onSetSetting('notebook.root', selected);
    }
  }, [onSetSetting]);

  const handleNotebookRootChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setNotebookRoot(e.target.value);
  }, []);

  // Blank commits as an empty override, which the daemon resolves back to the
  // per-profile default (~/attn-notebook).
  const commitNotebookRoot = useCallback(() => {
    if (notebookRoot !== actualNotebookRoot) {
      onSetSetting('notebook.root', notebookRoot);
    }
  }, [notebookRoot, actualNotebookRoot, onSetSetting]);

  const handleNotebookRootKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      commitNotebookRoot();
    }
  }, [commitNotebookRoot]);

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

  const handleToggleKeeperTasks = useCallback(() => {
    onSetSetting('notebook.tasks_enabled', keeperTasksEnabled ? 'false' : 'true');
  }, [keeperTasksEnabled, onSetSetting]);

  // Switching a duty's agent resets its model to that agent's recommended default
  // (the first preset); choosing the empty "Disabled" agent (opt-in duties only)
  // clears the model so Save stays disabled.
  const handleKeeperAgentChange = useCallback((dutyKey: KeeperDutyKey, agent: SessionAgent | '') => {
    setKeeperDrafts((prev) => ({
      ...prev,
      [dutyKey]: { agent, model: agent ? defaultKeeperDutyModel(dutyKey, agent) : '' },
    }));
  }, []);

  // The model <select> emits a preset value or the 'custom' sentinel; 'custom' blanks
  // the model so the free-form input takes over and Save waits for it to be filled.
  const handleKeeperModelSelection = useCallback((dutyKey: KeeperDutyKey, model: string) => {
    setKeeperDrafts((prev) => ({
      ...prev,
      [dutyKey]: { ...prev[dutyKey], model: model === 'custom' ? '' : model },
    }));
  }, []);

  const handleKeeperCustomModelChange = useCallback((dutyKey: KeeperDutyKey, model: string) => {
    setKeeperDrafts((prev) => ({
      ...prev,
      [dutyKey]: { ...prev[dutyKey], model },
    }));
  }, []);

  const saveKeeperDuty = useCallback((dutyKey: KeeperDutyKey) => {
    const draft = keeperDrafts[dutyKey];
    const model = draft.model.trim();
    if (!draft.agent || !model) return;
    onSetSetting(
      KEEPER_DUTY_BY_KEY[dutyKey].settingKey,
      serializeKeeperConfig({ agent: draft.agent, model }),
    );
  }, [keeperDrafts, onSetSetting]);

  // Clearing writes a blank override. For an opt-in duty that disables it; for an
  // always-on duty it reverts to the built-in tier default. Either way the draft
  // re-seeds to its unset starting point.
  const clearKeeperDuty = useCallback((dutyKey: KeeperDutyKey) => {
    const duty = KEEPER_DUTY_BY_KEY[dutyKey];
    onSetSetting(duty.settingKey, '');
    setKeeperDrafts((prev) => ({
      ...prev,
      [dutyKey]: initialKeeperDraft(duty, null, keeperAgents),
    }));
  }, [keeperAgents, onSetSetting]);

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
    const source = pluginSourcePath.trim();
    if (source === '') {
      setPluginError('Plugin source is required');
      return;
    }
    setPluginError(null);
    setPluginActionName('install');
    try {
      await onInstallPlugin(source);
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
          title: 'Appearance, project roots, and Notebook',
          description: 'Theme selection, the directory attn uses when opening repositories and worktrees, and where your Notebook lives.',
          count: 3,
          keywords: 'theme appearance dark light system projects directory worktrees roots notebook folder knowledge base journal location',
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
          count: Math.max(3, endpoints.length + githubHosts.length + 1),
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
          description: 'Agent executable paths, defaults, context maintenance, capabilities, and PTY runtime mode.',
          count: orderedAgentList.length + 4,
          keywords: 'agents executables claude codex cursor default capabilities pty backend editor context keeper compact model',
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
    githubHosts.length,
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
            <span className="settings-pill">
              {keeperTasksEnabled ? 'keeper on' : 'keeper off'}
            </span>
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

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Notebook</div>
          <h3>Notebook Folder</h3>
          <p className="settings-description">
            Where attn keeps your durable Notebook — dated journals and the knowledge base — as plain
            markdown you own. Leave blank to use the default (<code>~/attn-notebook</code>, separate per
            profile). Changing this points attn at the new folder; your existing notes are not moved, so
            move or sync the folder yourself if you want the current contents to come along.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-inline-form projects-dir-input">
            <input
              data-testid="settings-notebook-root-input"
              type="text"
              value={notebookRoot}
              onChange={handleNotebookRootChange}
              onBlur={commitNotebookRoot}
              onKeyDown={handleNotebookRootKeyDown}
              placeholder={effectiveNotebookRoot || '~/attn-notebook'}
              className="settings-input"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <button className="settings-action" onClick={handleBrowseNotebookRoot}>
              Browse
            </button>
          </div>
          {effectiveNotebookRoot && (
            <p className="settings-description" data-testid="settings-notebook-root-effective">
              Currently: <code>{effectiveNotebookRoot}</code>
            </p>
          )}
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
          {githubHosts.length === 0 ? (
            <p className="settings-empty">No authenticated hosts detected.</p>
          ) : (
            <div className="settings-token-list">
              {githubHosts.map((host) => (
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
          Install user-owned plugins from a local directory or Git repository and control provider dispatch priority.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-inline-form plugin-form">
          <input
            type="text"
            value={pluginSourcePath}
            onChange={(e) => setPluginSourcePath(e.target.value)}
            placeholder="git@host:team/my-attn-plugin.git or /Users/you/src/my-attn-plugin"
            className="settings-input"
            aria-label="Plugin source"
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
            {executableAgentList.map((agent) => {
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
          <div className="settings-kicker">Notebook</div>
          <h3>Keeper</h3>
          <p className="settings-description">
            The keeper runs three background duties off the notebook: it summarizes finished
            sessions, curates the work journal, and compacts large shared workspace contexts.
            Each duty picks its own non-interactive agent and model.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Background tasks</p>
              <p className="settings-row-copy">
                Master switch for every keeper duty below. While off, the keeper queues and
                runs no background work; the per-duty agent and model stay configurable.
                Turning it off won't interrupt a run already in flight.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-keeper-tasks-toggle"
              onClick={handleToggleKeeperTasks}
            >
              {keeperTasksEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>
          {keeperAgents.length === 0 && (
            <div className="settings-warning">No installed agent supports scoped headless tasks.</div>
          )}
          <div className={`settings-keeper-duties${keeperTasksEnabled ? '' : ' is-disabled'}`}>
            {KEEPER_DUTIES.map((duty) => {
              const draft = keeperDrafts[duty.key];
              const presets = duty.modelPresets(draft.agent);
              const modelSelection = keeperDutyModelSelection(duty.key, draft.agent, draft.model);
              const hasOverride = actualKeeperConfigs[duty.key] !== null;
              const agentId = `${duty.testIdPrefix}-agent`;
              const modelId = `${duty.testIdPrefix}-model`;
              const customId = `${duty.testIdPrefix}-model-custom`;
              return (
                <div className="settings-keeper-duty" key={duty.key}>
                  <div className="settings-keeper-duty-head">
                    <p className="settings-row-title">{duty.title}</p>
                    <p className="settings-row-copy">{duty.description}</p>
                  </div>
                  <div className="settings-field-grid two-column">
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={agentId}>Agent</label>
                      <select
                        id={agentId}
                        data-testid={agentId}
                        className="settings-input"
                        value={draft.agent}
                        onChange={(event) => handleKeeperAgentChange(duty.key, event.target.value as SessionAgent | '')}
                      >
                        {duty.optInOnly && <option value="">Disabled</option>}
                        {keeperAgents.map((agent) => (
                          <option key={agent} value={agent}>{agentLabel(agent)}</option>
                        ))}
                      </select>
                    </div>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={modelId}>Model</label>
                      <select
                        id={modelId}
                        data-testid={modelId}
                        value={modelSelection}
                        onChange={(event) => handleKeeperModelSelection(duty.key, event.target.value)}
                        className="settings-input"
                        disabled={!draft.agent}
                      >
                        {!draft.agent && <option value="">Select an agent</option>}
                        {presets.map((preset) => (
                          <option key={preset.value} value={preset.value}>{preset.label}</option>
                        ))}
                        <option value="custom">Custom...</option>
                      </select>
                    </div>
                  </div>
                  {draft.agent && modelSelection === 'custom' && (
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={customId}>Custom model</label>
                      <input
                        id={customId}
                        data-testid={customId}
                        type="text"
                        value={draft.model}
                        onChange={(event) => handleKeeperCustomModelChange(duty.key, event.target.value)}
                        placeholder={draft.agent === 'claude' ? 'claude-opus-4-6' : 'model ID'}
                        className="settings-input"
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                    </div>
                  )}
                  <div className="settings-row-inline">
                    <button
                      type="button"
                      className="settings-action"
                      data-testid={`${duty.testIdPrefix}-save`}
                      onClick={() => saveKeeperDuty(duty.key)}
                      disabled={!draft.agent || !draft.model.trim()}
                    >
                      Save
                    </button>
                    <button
                      type="button"
                      className="settings-action"
                      data-testid={`${duty.testIdPrefix}-clear`}
                      onClick={() => clearKeeperDuty(duty.key)}
                      disabled={!hasOverride}
                    >
                      {duty.optInOnly ? 'Disable' : 'Use default'}
                    </button>
                  </div>
                  {duty.optInOnly ? (
                    <div className="settings-hint">
                      Runs after a 10-minute debounce when canonical context exceeds 12 KiB. Use
                      `attn workspace context compact` to run it immediately.
                    </div>
                  ) : (
                    <div className="settings-hint">Defaults to {duty.defaultLabel} when unset.</div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Agents</div>
          <h3>Workflows</h3>
          <p className="settings-description">
            Lets managed agents run durable multi-agent workflows. Off by default. When on,
            agents learn how and when to use workflows and only start one when you opt in per
            task ("attn workflow") or for the session ("hypercode").
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Enable workflows</p>
              <p className="settings-row-copy">
                While off, "attn workflow run" is refused and agents aren't told about
                workflows. Turning it off won't interrupt a run already in flight.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-workflows-toggle"
              onClick={handleToggleWorkflows}
            >
              {workflowsEnabled ? 'Disable' : 'Enable'}
            </button>
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
