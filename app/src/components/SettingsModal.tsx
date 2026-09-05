import { Fragment, useState, useCallback, useEffect, useMemo } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { open } from '@tauri-apps/plugin-dialog';
import {
  DaemonEndpoint,
  DaemonPlugin,
  DaemonPluginIssue,
  DaemonSettings,
  Task,
  PluginListResult,
} from '../hooks/useDaemonSocket';
import { BackgroundTasksSettings } from './BackgroundTasksSettings';
import { EventBusSettings } from './EventBusSettings';
import { AutoModeSettings } from './AutoModeSettings';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';
import { SavedMark, useSavedFlash } from './useSavedFlash';
import { useAgentSettingDrafts, useSettingDraft } from './settingsDrafts';
import { useEndpointPanel } from './useEndpointPanel';
import { usePluginPanel } from './usePluginPanel';
import {
  assertValidSettingsSectionID,
  setSettingsAutomationHandle,
} from './settingsAutomation';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import {
  isAutoSettleEnabled,
  autoSettleSeconds,
  AUTO_SETTLE_ENABLED_SETTING,
  AUTO_SETTLE_ARM_SETTING,
  AUTO_SETTLE_COUNTDOWN_SETTING,
} from '../utils/queueBands';
import type { ThemePreference } from '../hooks/useTheme';
import { useDaemonApi } from '../contexts/DaemonApiContext';
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
import { SessionActivitySettings } from './SessionActivitySettings';
import { GardenAdvisorSettings } from './GardenAdvisorSettings';
import { parseGardenAdvisorSetting } from '../utils/gardenAdvisorSettings';
import { SessionCostPriceSettings } from './SessionCostPriceSettings';
import './SettingsModal.css';
import { formatShortcut } from '../shortcuts/formatShortcut';

const OPEN_SENT_FILES_ENABLED_SETTING = 'open_sent_files_enabled';

const PTY_BACKENDS: Record<string, { label: string; hint: string }> = {
  migrating: {
    label: 'Dedicated + shared workers',
    hint: 'Existing terminals keep their worker, including across daemon restarts.',
  },
  shared: {
    label: 'Shared Rust host',
    hint: 'New terminals share a Rust host. Selected by a backend override.',
  },
  worker: {
    label: 'Dedicated Go workers',
    hint: 'Sessions run in per-session worker processes and can survive daemon restarts.',
  },
  embedded: {
    label: 'Embedded in daemon',
    hint: 'Sessions run inside the daemon process and stop if the daemon restarts.',
  },
  unknown: {
    label: 'Unknown',
    hint: 'Backend mode is not currently reported by the daemon.',
  },
};

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
  onInstallBundledPlugin?: (name: string) => Promise<{ success: boolean; name?: string }>;
  onUninstallPlugin?: (name: string) => Promise<{ success: boolean; name?: string }>;
  onRemovePlugin: (name: string) => Promise<{ success: boolean; name?: string }>;
  onSetPluginPriority: (name: string, priority: number) => Promise<{ success: boolean; name?: string }>;
  onSetSetting: (key: string, value: string) => void;
  themePreference: ThemePreference;
  onSetTheme: (theme: ThemePreference) => void;
  uiScale?: number;
  onIncreaseUIScale?: () => void;
  onDecreaseUIScale?: () => void;
  onResetUIScale?: () => void;
  gardenScale?: number | null;
  effectiveGardenScale?: number;
  onIncreaseGardenScale?: () => void;
  onDecreaseGardenScale?: () => void;
  onMatchAppGardenScale?: () => void;
  listTasks?: () => Promise<Task[]>;
  retryTask?: (taskId: string) => Promise<Task | null>;
  taskChangeSignal?: number;
}

// Section ids are the deep-link and automation vocabulary, so they outlive the
// labels above them; settingsAutomation.ts moves with this union.
type SettingsSectionID =
  | 'general'
  | 'workspace'
  | 'hygiene'
  | 'agents'
  | 'keeper'
  | 'autoMode'
  | 'connectivity'
  | 'plugins'
  | 'backgroundTasks'
  | 'eventBus'
  | 'data';

// Fallback shown when the daemon has not yet sent a normalized value; the daemon
// mirrors this default (agent.DefaultContextWindowCap) for both context-window caps.
const DEFAULT_CONTEXT_WINDOW_CAP = 128000;
const MODEL_CAPTURE_INTERVAL_OPTIONS = [5, 10, 30, 60];
const MODEL_CAPTURE_MAX_GB_OPTIONS = [1, 2, 5, 10, 25];

function formatByteCount(raw: string | undefined): string {
  const bytes = Number(raw || '0');
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / (1024 ** unit);
  return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

const CHIEF_EFFORT_LEVELS: Partial<Record<SessionAgent, string[]>> = {
  claude: ['low', 'medium', 'high', 'xhigh', 'max'],
  codex: ['minimal', 'low', 'medium', 'high', 'xhigh'],
};

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
  onInstallBundledPlugin,
  onUninstallPlugin,
  onRemovePlugin,
  onSetPluginPriority,
  onSetSetting,
  themePreference,
  onSetTheme,
  uiScale = 1,
  onIncreaseUIScale,
  onDecreaseUIScale,
  onResetUIScale,
  gardenScale = null,
  effectiveGardenScale,
  onIncreaseGardenScale,
  onDecreaseGardenScale,
  onMatchAppGardenScale,
  listTasks,
  retryTask,
  taskChangeSignal,
}: SettingsModalProps) {
  const {
    sendGetSettings,
    sendBusStatusGet,
    sendBusSetConsumerEnabled,
    sendAutoModeGet,
    sendAutoModePromote,
    sendAutoModeDiscard,
    sendAutoModePatternAdd,
    sendAutoModePatternRemove,
    sendAutoModeEnvSlot,
    sendAutoModeModelSet,
    sendAutoModeModels,
  } = useDaemonApi();
  const autoModePolicy = useAutoModePolicy({
    enabled: isOpen,
    getState: sendAutoModeGet,
    promoteProposal: sendAutoModePromote,
    discardProposal: sendAutoModeDiscard,
    addPattern: sendAutoModePatternAdd,
    removePattern: sendAutoModePatternRemove,
    setEnvironmentSlot: sendAutoModeEnvSlot,
    setModels: sendAutoModeModelSet,
    loadModels: sendAutoModeModels,
  });
  const savedFlash = useSavedFlash();
  const [defaultAgent, setDefaultAgent] = useState<SessionAgent>('claude');
  const [keeperDrafts, setKeeperDrafts] = useState<Record<KeeperDutyKey, KeeperDraft>>(
    emptyKeeperDrafts,
  );
  const [selectedSection, setSelectedSection] = useState<SettingsSectionID>('connectivity');
  const [settingsSearch, setSettingsSearch] = useState('');
  const endpointPanel = useEndpointPanel();
  const pluginPanel = usePluginPanel(onListPlugins);
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  const actualProjectsDir = settings.projects_directory || '';
  const actualNotebookRoot = settings['notebook.root'] || '';
  const effectiveNotebookRoot = settings['notebook.root.effective'] || '';
  const tailscaleEnabled = (settings.tailscale_enabled || 'false') === 'true';
  const workflowsEnabled = (settings.workflows_enabled || 'false') === 'true';
  const autoApproveEnabled = (settings.auto_approve_enabled || 'false') === 'true';
  const modelCaptureEnabled = (settings['model_capture.enabled'] || 'false') === 'true';
  const modelCaptureInterval = settings['model_capture.interval_seconds'] || '10';
  const modelCaptureMaxGB = settings['model_capture.max_gb'] || '5';
  const modelCapturePath = settings['model_capture.path'] || '';
  const modelCaptureBytes = formatByteCount(settings['model_capture.bytes']);
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
  const actualReviewerModel = settings.reviewer_model || '';
  const actualChiefContextCap = settings.chief_context_window_cap || String(DEFAULT_CONTEXT_WINDOW_CAP);
  const actualHeadlessContextCap = settings.headless_context_window_cap || String(DEFAULT_CONTEXT_WINDOW_CAP);
  const autoSettleEnabled = isAutoSettleEnabled(settings);
  const actualAutoSettleArm = String(autoSettleSeconds(settings, AUTO_SETTLE_ARM_SETTING));
  const actualAutoSettleCountdown = String(autoSettleSeconds(settings, AUTO_SETTLE_COUNTDOWN_SETTING));
  const actualKeeperConfigs = useMemo(() => {
    const configs = {} as Record<KeeperDutyKey, KeeperConfig | null>;
    for (const duty of KEEPER_DUTIES) {
      configs[duty.key] = parseKeeperConfig(settings[duty.settingKey]);
    }
    return configs;
  }, [settings]);
  const keeperTasksEnabled = (settings['notebook.tasks_enabled'] ?? 'true') !== 'false';
  const keeperDutyEnabled = useMemo(() => {
    const enabled = {} as Record<KeeperDutyKey, boolean>;
    for (const duty of KEEPER_DUTIES) {
      enabled[duty.key] = duty.enabledSettingKey === undefined
        || (settings[duty.enabledSettingKey] ?? 'true') !== 'false';
    }
    return enabled;
  }, [settings]);
  const resolvedDefaultAgent = resolvePreferredAgent(actualDefaultAgent, agentAvailability, 'codex');
  const orderedAgentList = useMemo(
    () => orderedAgents(agentAvailability, resolvedDefaultAgent, 'codex'),
    [agentAvailability, resolvedDefaultAgent],
  );
  const executableAgentList = useMemo(
    () => orderedAgentList.filter((agent) => ['codex', 'claude', 'copilot'].includes(agent)),
    [orderedAgentList],
  );
  const chiefOverrideAgentList = useMemo(() => {
    const list = orderedAgentList.filter((agent) => (
      ['codex', 'claude'].includes(agent) && isAgentAvailable(agentAvailability, agent)
    ));
    for (const agent of ['claude', 'codex'] as const) {
      if (!list.includes(agent) && (
        (settings[`chief_model_${agent}`] || '').trim() !== ''
        || (settings[`chief_effort_${agent}`] || '').trim() !== ''
      )) {
        list.push(agent);
      }
    }
    return list;
  }, [orderedAgentList, agentAvailability, settings]);
  const actualChiefModels = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of chiefOverrideAgentList) {
      out[agent] = settings[`chief_model_${agent}`] || '';
    }
    return out;
  }, [settings, chiefOverrideAgentList]);
  const actualChiefEfforts = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of chiefOverrideAgentList) {
      out[agent] = settings[`chief_effort_${agent}`] || '';
    }
    return out;
  }, [settings, chiefOverrideAgentList]);
  const defaultOverrideAgentList = useMemo(() => {
    const list = orderedAgentList.filter((agent) => (
      ['codex', 'claude'].includes(agent) && isAgentAvailable(agentAvailability, agent)
    ));
    for (const agent of ['claude', 'codex'] as const) {
      if (!list.includes(agent) && (
        (settings[`default_model_${agent}`] || '').trim() !== ''
        || (settings[`default_effort_${agent}`] || '').trim() !== ''
      )) {
        list.push(agent);
      }
    }
    return list;
  }, [orderedAgentList, agentAvailability, settings]);
  const actualDefaultModels = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of defaultOverrideAgentList) {
      out[agent] = settings[`default_model_${agent}`] || '';
    }
    return out;
  }, [settings, defaultOverrideAgentList]);
  const actualDefaultEfforts = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of defaultOverrideAgentList) {
      out[agent] = settings[`default_effort_${agent}`] || '';
    }
    return out;
  }, [settings, defaultOverrideAgentList]);
  const actualDefaultContextCaps = useMemo(() => {
    const out = {} as Record<SessionAgent, string>;
    for (const agent of defaultOverrideAgentList) {
      out[agent] = settings[`default_context_window_cap_${agent}`] || '';
    }
    return out;
  }, [settings, defaultOverrideAgentList]);
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
  const gardenAdvisorAgents = useMemo(() => {
    const eligible = orderedAgentList.filter((agent) => (
      ['codex', 'claude', 'copilot'].includes(agent)
      && isAgentAvailable(agentAvailability, agent)
      && actualAgentCapabilities[agent]?.headless_task === true
    ));
    const configured = parseGardenAdvisorSetting(settings['garden.advisor']).agent;
    if (!eligible.includes(configured)) eligible.push(configured);
    return eligible;
  }, [actualAgentCapabilities, agentAvailability, orderedAgentList, settings]);
  const agentCapabilityOrder = useMemo(
    () => AGENT_CAPABILITY_ORDER.map((cap) => cap as string),
    [],
  );
  const rawPtyBackendMode = (settings.pty_backend_mode || 'unknown').toLowerCase();
  const ptyBackendMode = Object.prototype.hasOwnProperty.call(PTY_BACKENDS, rawPtyBackendMode)
    ? rawPtyBackendMode
    : 'unknown';
  const { label: ptyBackendLabel, hint: ptyBackendHint } = PTY_BACKENDS[ptyBackendMode];
  const sharedPtyHostEnabled = settings.pty_shared_host_enabled === 'true';
  const sharedPtyHostActive = settings.pty_shared_host_active === 'true';

  const draftDeps = { active: isOpen, onSetSetting, savedFlash };
  const projectsDirDraft = useSettingDraft({
    ...draftDeps, actual: actualProjectsDir, settingKey: 'projects_directory',
  });
  const notebookRootDraft = useSettingDraft({
    ...draftDeps, actual: actualNotebookRoot, settingKey: 'notebook.root',
  });
  const editorDraft = useSettingDraft({
    ...draftDeps, actual: actualEditorExecutable, settingKey: 'editor_executable',
  });
  const reviewerModelDraft = useSettingDraft({
    ...draftDeps, actual: actualReviewerModel, settingKey: 'reviewer_model',
  });
  const chiefContextCapDraft = useSettingDraft({
    ...draftDeps, actual: actualChiefContextCap, settingKey: 'chief_context_window_cap', trim: true,
  });
  const headlessContextCapDraft = useSettingDraft({
    ...draftDeps, actual: actualHeadlessContextCap, settingKey: 'headless_context_window_cap', trim: true,
  });
  // Seconds. The modal mounts before the daemon's settings broadcast, so without the
  // reseed commit-on-blur writes the built-in defaults over a saved policy.
  const autoSettleArmDraft = useSettingDraft({
    ...draftDeps, actual: actualAutoSettleArm, settingKey: AUTO_SETTLE_ARM_SETTING, trim: true,
  });
  const autoSettleCountdownDraft = useSettingDraft({
    ...draftDeps, actual: actualAutoSettleCountdown, settingKey: AUTO_SETTLE_COUNTDOWN_SETTING, trim: true,
  });
  const executableDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualAgentExecutables,
    settingKey: (agent) => `${agent}_executable`,
  });
  const chiefModelDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualChiefModels,
    settingKey: (agent) => `chief_model_${agent}`,
    trim: true,
  });
  const chiefEffortDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualChiefEfforts,
    settingKey: (agent) => `chief_effort_${agent}`,
    flashKey: (agent) => `chief_model_${agent}`,
  });
  const defaultModelDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualDefaultModels,
    settingKey: (agent) => `default_model_${agent}`,
    trim: true,
  });
  const defaultEffortDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualDefaultEfforts,
    settingKey: (agent) => `default_effort_${agent}`,
    flashKey: (agent) => `default_model_${agent}`,
  });
  // Per-agent context-window cap; a chief launch still takes the chief cap above.
  // Blank => uncapped, unlike the chief and headless caps whose blank means the default.
  const defaultContextCapDrafts = useAgentSettingDrafts({
    ...draftDeps,
    actual: actualDefaultContextCaps,
    settingKey: (agent) => `default_context_window_cap_${agent}`,
    trim: true,
  });

  const { reopen: reopenEndpointPanel } = endpointPanel;
  const { setSourcePath: setPluginSourcePath } = pluginPanel;
  useEffect(() => {
    if (!isOpen) return;
    setDefaultAgent(resolvedDefaultAgent);
    setKeeperDrafts({
      summarize: initialKeeperDraft(KEEPER_DUTY_BY_KEY.summarize, actualKeeperConfigs.summarize, keeperAgents),
      narrate: initialKeeperDraft(KEEPER_DUTY_BY_KEY.narrate, actualKeeperConfigs.narrate, keeperAgents),
      compact: initialKeeperDraft(KEEPER_DUTY_BY_KEY.compact, actualKeeperConfigs.compact, keeperAgents),
    });
    reopenEndpointPanel();
    setPluginSourcePath('');
  }, [isOpen, resolvedDefaultAgent, actualKeeperConfigs, keeperAgents, reopenEndpointPanel, setPluginSourcePath]);

  useEscapeStack(onClose, isOpen);

  useEffect(() => {
    if (!isOpen || selectedSection !== 'data') return;

    sendGetSettings();
    if (!modelCaptureEnabled) return;

    const intervalSeconds = Number(modelCaptureInterval);
    if (!Number.isFinite(intervalSeconds) || intervalSeconds <= 0) return;

    const intervalID = window.setInterval(sendGetSettings, intervalSeconds * 1000);
    return () => window.clearInterval(intervalID);
  }, [isOpen, selectedSection, modelCaptureEnabled, modelCaptureInterval, sendGetSettings]);

  // UI automation bridge handle (testing only). Registered for the whole
  // lifetime so the bridge can read `open: false` through it.
  useEffect(() => {
    setSettingsAutomationHandle({
      getState: () => ({
        open: isOpen,
        activeSection: selectedSection,
        search: settingsSearch,
      }),
      selectSection: (sectionId) => {
        assertValidSettingsSectionID(sectionId);
        setSelectedSection(sectionId);
      },
    });
    return () => setSettingsAutomationHandle(null);
  }, [isOpen, selectedSection, settingsSearch]);

  const { set: setProjectsDir } = projectsDirDraft;
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
  }, [onSetSetting, setProjectsDir]);

  const handleToggleTailscale = useCallback(() => {
    onSetSetting('tailscale_enabled', tailscaleEnabled ? 'false' : 'true');
  }, [onSetSetting, tailscaleEnabled]);

  const handleToggleAutoSettle = useCallback(() => {
    onSetSetting(AUTO_SETTLE_ENABLED_SETTING, autoSettleEnabled ? 'false' : 'true');
  }, [onSetSetting, autoSettleEnabled]);

  const openSentFilesEnabled = (settings[OPEN_SENT_FILES_ENABLED_SETTING] || 'true') === 'true';
  const handleToggleOpenSentFiles = useCallback(() => {
    onSetSetting(OPEN_SENT_FILES_ENABLED_SETTING, openSentFilesEnabled ? 'false' : 'true');
  }, [onSetSetting, openSentFilesEnabled]);

  const handleToggleWorkflows = useCallback(() => {
    onSetSetting('workflows_enabled', workflowsEnabled ? 'false' : 'true');
  }, [onSetSetting, workflowsEnabled]);

  const handleToggleModelCapture = useCallback(() => {
    onSetSetting('model_capture.enabled', modelCaptureEnabled ? 'false' : 'true');
  }, [modelCaptureEnabled, onSetSetting]);

  const { set: setNotebookRoot } = notebookRootDraft;
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
  }, [onSetSetting, setNotebookRoot]);

  const handleToggleAutoApprove = useCallback(() => {
    onSetSetting('auto_approve_enabled', autoApproveEnabled ? 'false' : 'true');
  }, [autoApproveEnabled, onSetSetting]);

  const handleDefaultAgentChange = useCallback((agent: SessionAgent) => {
    if (!isAgentAvailable(agentAvailability, agent)) return;
    setDefaultAgent(agent);
    if (agent !== actualDefaultAgent) {
      onSetSetting('new_session_agent', agent);
    }
  }, [actualDefaultAgent, agentAvailability, onSetSetting]);

  // The daemon's default is on, so an unset value reads as enabled here too.
  const worktreeSweepEnabled = settings['worktree_sweep_enabled'] !== 'false';

  const handleToggleWorktreeSweep = useCallback(() => {
    onSetSetting('worktree_sweep_enabled', worktreeSweepEnabled ? 'false' : 'true');
  }, [worktreeSweepEnabled, onSetSetting]);

  const handleToggleKeeperTasks = useCallback(() => {
    onSetSetting('notebook.tasks_enabled', keeperTasksEnabled ? 'false' : 'true');
  }, [keeperTasksEnabled, onSetSetting]);

  const handleToggleKeeperDuty = useCallback((dutyKey: KeeperDutyKey) => {
    const duty = KEEPER_DUTY_BY_KEY[dutyKey];
    if (!duty.enabledSettingKey) return;
    onSetSetting(duty.enabledSettingKey, keeperDutyEnabled[dutyKey] ? 'false' : 'true');
  }, [keeperDutyEnabled, onSetSetting]);

  const commitKeeperDuty = useCallback((
    dutyKey: KeeperDutyKey,
    agent: SessionAgent | '',
    rawModel: string,
  ) => {
    const model = rawModel.trim();
    if (!agent || !model) return;
    onSetSetting(
      KEEPER_DUTY_BY_KEY[dutyKey].settingKey,
      serializeKeeperConfig({ agent, model }),
    );
    savedFlash.flash(KEEPER_DUTY_BY_KEY[dutyKey].settingKey);
  }, [onSetSetting, savedFlash]);

  const handleKeeperAgentChange = useCallback((dutyKey: KeeperDutyKey, agent: SessionAgent | '') => {
    const model = agent ? defaultKeeperDutyModel(dutyKey, agent) : '';
    setKeeperDrafts((prev) => ({ ...prev, [dutyKey]: { agent, model } }));
    commitKeeperDuty(dutyKey, agent, model);
  }, [commitKeeperDuty]);

  const handleKeeperModelSelection = useCallback((dutyKey: KeeperDutyKey, model: string) => {
    const next = model === 'custom' ? '' : model;
    setKeeperDrafts((prev) => ({ ...prev, [dutyKey]: { ...prev[dutyKey], model: next } }));
    if (next) commitKeeperDuty(dutyKey, keeperDrafts[dutyKey].agent, next);
  }, [commitKeeperDuty, keeperDrafts]);

  const handleKeeperCustomModelChange = useCallback((dutyKey: KeeperDutyKey, model: string) => {
    setKeeperDrafts((prev) => ({
      ...prev,
      [dutyKey]: { ...prev[dutyKey], model },
    }));
  }, []);

  const commitKeeperCustomModel = useCallback((dutyKey: KeeperDutyKey) => {
    const draft = keeperDrafts[dutyKey];
    commitKeeperDuty(dutyKey, draft.agent, draft.model);
  }, [commitKeeperDuty, keeperDrafts]);

  const clearKeeperDuty = useCallback((dutyKey: KeeperDutyKey) => {
    const duty = KEEPER_DUTY_BY_KEY[dutyKey];
    onSetSetting(duty.settingKey, '');
    setKeeperDrafts((prev) => ({
      ...prev,
      [dutyKey]: initialKeeperDraft(duty, null, keeperAgents),
    }));
  }, [keeperAgents, onSetSetting]);

  const handleAddEndpoint = useCallback(async () => {
    const name = endpointPanel.draft.name.trim();
    const sshTarget = endpointPanel.draft.target.trim();
    const profile = endpointPanel.draft.profile.trim();
    if (!name || !sshTarget) {
      endpointPanel.fail('Endpoint name and SSH target are required.');
      return;
    }
    await endpointPanel.run('new', 'Failed to add endpoint', async () => {
      await onAddEndpoint(name, sshTarget, profile);
      endpointPanel.clearDraft();
    });
  }, [endpointPanel, onAddEndpoint]);

  const handleSaveEndpoint = useCallback(async (endpointId: string) => {
    const editing = endpointPanel.editing;
    if (!editing) return;
    const name = editing.name.trim();
    const sshTarget = editing.target.trim();
    const profile = editing.profile.trim();
    if (!name || !sshTarget) {
      endpointPanel.fail('Endpoint name and SSH target are required.');
      return;
    }
    await endpointPanel.run(endpointId, 'Failed to update endpoint', async () => {
      await onUpdateEndpoint(endpointId, { name, ssh_target: sshTarget, profile });
      endpointPanel.cancelEdit();
    });
  }, [endpointPanel, onUpdateEndpoint]);

  const handleToggleEndpoint = useCallback(async (endpoint: DaemonEndpoint) => {
    await endpointPanel.run(endpoint.id, 'Failed to update endpoint', () =>
      onUpdateEndpoint(endpoint.id, { enabled: endpoint.enabled === false }).then(() => undefined));
  }, [endpointPanel, onUpdateEndpoint]);

  const handleRebootstrapEndpoint = useCallback(async (endpoint: DaemonEndpoint) => {
    if (endpoint.enabled === false) return;
    await endpointPanel.run(endpoint.id, 'Failed to re-bootstrap endpoint', async () => {
      await onUpdateEndpoint(endpoint.id, { enabled: false });
      await onUpdateEndpoint(endpoint.id, { enabled: true });
    });
  }, [endpointPanel, onUpdateEndpoint]);

  const handleRemoveEndpoint = useCallback(async (endpointId: string) => {
    await endpointPanel.run(endpointId, 'Failed to remove endpoint', async () => {
      await onRemoveEndpoint(endpointId);
      if (endpointPanel.editing?.id === endpointId) endpointPanel.cancelEdit();
    });
  }, [endpointPanel, onRemoveEndpoint]);

  const handleSetEndpointRemoteWeb = useCallback(async (endpointId: string, enabled: boolean) => {
    await endpointPanel.run(endpointId, 'Failed to update remote web access', () =>
      onSetEndpointRemoteWeb(endpointId, enabled).then(() => undefined));
  }, [endpointPanel, onSetEndpointRemoteWeb]);

  const { refresh: refreshPlugins } = pluginPanel;
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
  }, [setPluginSourcePath]);

  const handleInstallPlugin = useCallback(async () => {
    const source = pluginPanel.sourcePath.trim();
    if (source === '') {
      pluginPanel.fail('Plugin source is required');
      return;
    }
    await pluginPanel.run('install', 'Failed to install plugin', async () => {
      await onInstallPlugin(source);
      setPluginSourcePath('');
      await refreshPlugins();
    });
  }, [onInstallPlugin, pluginPanel, refreshPlugins, setPluginSourcePath]);

  const handleRemovePlugin = useCallback(async (name: string) => {
    await pluginPanel.run(name, 'Failed to remove plugin', async () => {
      await onRemovePlugin(name);
      await refreshPlugins();
    });
  }, [onRemovePlugin, pluginPanel, refreshPlugins]);

  const handleInstallBundledPlugin = useCallback(async (name: string) => {
    await pluginPanel.run(name, 'Failed to install bundled plugin', async () => {
      if (!onInstallBundledPlugin) throw new Error('Bundled plugin installation is unavailable');
      await onInstallBundledPlugin(name);
      await refreshPlugins();
    });
  }, [onInstallBundledPlugin, pluginPanel, refreshPlugins]);

  const handleUninstallPlugin = useCallback(async (name: string) => {
    await pluginPanel.run(name, 'Failed to uninstall plugin', async () => {
      if (!onUninstallPlugin) throw new Error('Plugin uninstall is unavailable');
      await onUninstallPlugin(name);
      await refreshPlugins();
    });
  }, [onUninstallPlugin, pluginPanel, refreshPlugins]);

  const commitPluginPriority = useCallback(async (name: string, current: number) => {
    const raw = pluginPanel.priorityDraft(name).trim();
    if (raw === String(current)) return;
    const priority = Number(raw);
    if (!Number.isInteger(priority)) {
      pluginPanel.fail('Plugin priority must be an integer');
      return;
    }
    await pluginPanel.run(name, 'Failed to update plugin priority', async () => {
      await onSetPluginPriority(name, priority);
      await refreshPlugins();
      savedFlash.flash(`plugin_priority_${name}`);
    });
  }, [onSetPluginPriority, pluginPanel, refreshPlugins, savedFlash]);

  const connectedEndpointCount = endpoints.filter((endpoint) => endpoint.status === 'connected').length;
  const activePluginCount = plugins.filter((plugin) => plugin.connected || plugin.running).length;
  const availableAgentCount = orderedAgentList.filter((agent) => isAgentAvailable(agentAvailability, agent)).length;
  const mutedItemCount = mutedRepos.length + mutedAuthors.length;
  const pluginProblemCount = pluginIssues.length + plugins.filter((plugin) => plugin.health_status === 'unhealthy').length;
  const hasProjectsDirChange = projectsDirDraft.value !== actualProjectsDir;
  const hasReviewModelChange = reviewerModelDraft.value !== actualReviewerModel;

  const settingsNavGroups = useMemo<SettingsNavGroup[]>(() => [
    {
      label: 'attn',
      items: [
        {
          id: 'general',
          label: 'Appearance',
          title: 'Appearance',
          description: 'Theme and text size, for the app and for the garden.',
          count: 2,
          keywords: 'theme appearance dark light system font size text scale zoom garden',
        },
        {
          id: 'workspace',
          label: 'Files and locations',
          title: 'Files and locations',
          description: 'Where attn opens repositories and worktrees, when merged worktrees are reclaimed, where your Notebook lives, and what it does with a file an agent sends you.',
          count: 4,
          keywords: 'projects directory worktrees roots notebook folder knowledge base journal location sent files tiles open markdown worktree sweep reclaim merged keep pin',
        },
        {
          id: 'hygiene',
          label: 'Attention queue',
          title: 'Attention queue',
          description: 'When a turn settles itself, and which repositories and authors never reach the queue at all.',
          count: mutedItemCount + 1,
          keywords: 'muted repositories repos authors hide unmute hygiene auto-settle settle turn countdown sidebar attention queue',
        },
      ],
    },
    {
      label: 'Agents',
      items: [
        {
          id: 'agents',
          label: 'Executables and models',
          title: 'Agents and models',
          description: 'Which binary each agent runs, which model and effort it launches with, its context caps, and how its terminal is hosted.',
          count: orderedAgentList.length + 8,
          keywords: 'agents executables claude codex copilot default capabilities pty backend editor model effort chief reviewer review garden advisor sdk context window cap tokens compaction headless workflows auto-approve unattended',
        },
        {
          id: 'keeper',
          label: 'Context maintenance',
          title: 'Context maintenance',
          description: 'The keeper: which agent summarizes, narrates and compacts your workspace context, and whether it runs at all.',
          count: 4,
          keywords: 'keeper context maintenance summarize summaries narrate compact background tasks duty roster haiku costs',
        },
        {
          id: 'autoMode',
          label: 'Auto mode',
          title: 'Auto mode',
          description: "Manage attn's pi automode plugin",
          count: autoModePolicy.pendingCount,
          keywords: 'auto mode automode pi safety envelope classifier proposals promote discard allow deny hard deny patterns policy permissions denials',
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
      label: 'System',
      items: [
        {
          id: 'backgroundTasks',
          label: 'Task runner',
          title: 'Background tasks',
          description: 'The durable task runner: compaction, summaries, narration, and reconciliation, with retry.',
          count: 1,
          keywords: 'background tasks runner durable compaction summarize narrate reconcile retry failed dead queue',
        },
        {
          id: 'eventBus',
          label: 'Event bus',
          title: 'Event bus',
          description: 'The durable event log: what it holds, which facts are written to it and how fast, and who reads it.',
          count: 1,
          keywords: 'event bus log durable facts producers consumers cursor lag retention compaction trim stalled disabled kill switch seq',
        },
        {
          id: 'data',
          label: 'Model data capture',
          title: 'Local model data capture',
          description: 'Opt-in collection of visible Codex and Claude terminal viewports for local model evaluation and training.',
          count: 3,
          keywords: 'model training dataset capture privacy terminal viewport local retention sampling',
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
    autoModePolicy.pendingCount,
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
            <span className={`settings-pill ${hasReviewModelChange ? 'warn' : 'good'}`}>
              {hasReviewModelChange ? 'reviewer model edited' : 'reviewer model saved'}
            </span>
          </>
        );
      case 'keeper':
        return (
          <>
            <span className={`settings-pill ${keeperTasksEnabled ? 'good' : ''}`}>
              {keeperTasksEnabled ? 'keeper on' : 'keeper off'}
            </span>
          </>
        );
      case 'data':
        return (
          <>
            <span className={`settings-pill ${modelCaptureEnabled ? 'warn' : 'good'}`}>
              {modelCaptureEnabled ? 'capture on' : 'capture off'}
            </span>
            <span className="settings-pill">{modelCaptureBytes}</span>
          </>
        );
      case 'autoMode':
        return (
          <>
            <span className={`settings-pill ${autoModePolicy.pendingCount === 0 ? 'good' : 'warn'}`}>
              {autoModePolicy.pendingCount === 0
                ? 'nothing waiting'
                : `${autoModePolicy.pendingCount} proposal${autoModePolicy.pendingCount === 1 ? '' : 's'} waiting`}
            </span>
            {autoModePolicy.state && (
              <span className="settings-pill">
                {autoModePolicy.state.config.models.length === 0
                  ? 'off: no model'
                  : autoModePolicy.state.config.enabled_default
                    ? 'on by default'
                    : 'off by default'}
              </span>
            )}
          </>
        );
      case 'hygiene':
        return (
          <>
            <span className={`settings-pill ${autoSettleEnabled ? 'good' : ''}`}>
              {autoSettleEnabled ? 'auto-settle on' : 'auto-settle off'}
            </span>
            <span className="settings-pill">{mutedRepos.length} repos</span>
            <span className="settings-pill">{mutedAuthors.length} authors</span>
          </>
        );
      case 'workspace':
        return (
          <>
            <span className={`settings-pill ${hasProjectsDirChange ? 'warn' : 'good'}`}>
              {hasProjectsDirChange ? 'project path edited' : 'project path saved'}
            </span>
            <span className={`settings-pill ${openSentFilesEnabled ? 'good' : ''}`}>
              {openSentFilesEnabled ? 'sent files open' : 'sent files ignored'}
            </span>
          </>
        );
      case 'general':
      default:
        return (
          <>
            <span className="settings-pill">{themePreference}</span>
            <span className="settings-pill">{Math.round(uiScale * 100)}% text</span>
          </>
        );
    }
  };

  const renderAppearanceSettings = () => (
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
          <div className="settings-kicker">Appearance</div>
          <h3>Font Size</h3>
          <p className="settings-description">
            Scale text across attn. The garden can use its own size, independent of the rest of
            the app.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">App</p>
              <p className="settings-row-copy">
                The whole interface, including terminals. Also adjustable with {formatShortcut('ui.increaseFontSize')} and{' '}
                {formatShortcut('ui.decreaseFontSize')}.
              </p>
            </div>
            <div className="settings-font-scale" data-testid="settings-app-font-scale">
              <button
                type="button"
                className="settings-action"
                onClick={onDecreaseUIScale}
                aria-label="Decrease app font size"
              >
                −
              </button>
              <span className="settings-font-scale-value" data-testid="settings-app-font-scale-value">
                {Math.round(uiScale * 100)}%
              </span>
              <button
                type="button"
                className="settings-action"
                onClick={onIncreaseUIScale}
                aria-label="Increase app font size"
              >
                +
              </button>
              {uiScale !== 1 && (
                <button type="button" className="settings-action" onClick={onResetUIScale}>
                  Reset
                </button>
              )}
            </div>
          </div>
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">The garden</p>
              <p className="settings-row-copy">
                Seeds, plots, and their logs. Matches the app size until you change it.
              </p>
            </div>
            <div className="settings-font-scale" data-testid="settings-garden-font-scale">
              <button
                type="button"
                className="settings-action"
                onClick={onDecreaseGardenScale}
                aria-label="Decrease garden font size"
              >
                −
              </button>
              <span
                className="settings-font-scale-value"
                data-testid="settings-garden-font-scale-value"
              >
                {gardenScale === null
                  ? 'Match app'
                  : `${Math.round((effectiveGardenScale ?? gardenScale) * 100)}%`}
              </span>
              <button
                type="button"
                className="settings-action"
                onClick={onIncreaseGardenScale}
                aria-label="Increase garden font size"
              >
                +
              </button>
              {gardenScale !== null && (
                <button
                  type="button"
                  className="settings-action"
                  onClick={onMatchAppGardenScale}
                >
                  Match app
                </button>
              )}
            </div>
          </div>
        </div>
      </section>
    </>
  );

  const renderWorkspaceSettings = () => (
    <>
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
              value={projectsDirDraft.value}
              onChange={projectsDirDraft.onChange}
              onBlur={projectsDirDraft.commit}
              onKeyDown={projectsDirDraft.onKeyDown}
              placeholder="/Users/you/projects"
              className="settings-input"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <SavedMark
              shown={savedFlash.saved('projects_directory')}
              testID="settings-projects-directory-saved"
            />
            <button className="settings-action" onClick={handleBrowse}>
              Browse
            </button>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Worktrees</div>
          <h3>Worktree sweep</h3>
          <p className="settings-description">
            Reclaims worktrees whose work has landed. A worktree is only removed when it has
            been idle for 14 days, has no uncommitted or stashed changes, has nothing unpushed
            beyond what merged, holds no live session or open seed, and its branch is on the
            repository's integration branch — as a merged pull request, as an ancestor, or with
            an identical tree. Everything it keeps says why, and every removal lands in the
            sweep log and as a note on the seeds that worked there. On by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Reclaim merged worktrees in the background</p>
              <p className="settings-row-copy">
                Turning it off stops removals; the daemon keeps refreshing worktree state so the
                Worktrees panel stays accurate, and the keep-forever pins you set stay set.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-worktree-sweep-toggle"
              onClick={handleToggleWorktreeSweep}
            >
              {worktreeSweepEnabled ? 'Disable' : 'Enable'}
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
              value={notebookRootDraft.value}
              onChange={notebookRootDraft.onChange}
              onBlur={notebookRootDraft.commit}
              onKeyDown={notebookRootDraft.onKeyDown}
              placeholder={effectiveNotebookRoot || '~/attn-notebook'}
              className="settings-input"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <SavedMark shown={savedFlash.saved('notebook.root')} testID="settings-notebook-root-saved" />
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

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Workspace</div>
          <h3>Sent files</h3>
          <p className="settings-description">
            When an agent hands you a file with its send-file tool, attn opens the ones it
            can show as workspace tiles. On by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Open files agents send you</p>
              <p className="settings-row-copy">
                A markdown file an agent sends opens as a live-reloading tile beside its
                terminal, so you see it without hunting through the transcript. File types
                attn cannot show are left alone.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-open-sent-files-toggle"
              onClick={handleToggleOpenSentFiles}
            >
              {openSentFilesEnabled ? 'Disable' : 'Enable'}
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
              value={endpointPanel.draft.name}
              onChange={(e) => endpointPanel.setDraft('name', e.target.value)}
              placeholder="gpu-box"
              className="settings-input"
              aria-label="Endpoint name"
              disabled={endpointPanel.busy}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <input
              type="text"
              value={endpointPanel.draft.target}
              onChange={(e) => endpointPanel.setDraft('target', e.target.value)}
              placeholder="user@gpu-box"
              className="settings-input"
              aria-label="SSH target"
              disabled={endpointPanel.busy}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <input
              type="text"
              value={endpointPanel.draft.profile}
              onChange={(e) => endpointPanel.setDraft('profile', e.target.value)}
              placeholder="default"
              pattern="[a-z0-9][a-z0-9-]{0,15}"
              className="settings-input"
              aria-label="Profile"
              disabled={endpointPanel.busy}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
            />
            <button
              className="settings-action"
              onClick={() => void handleAddEndpoint()}
              disabled={endpointPanel.busy}
            >
              Add Endpoint
            </button>
          </div>
          {endpointPanel.error && <div className="settings-warning">{endpointPanel.error}</div>}
          {endpoints.length === 0 ? (
            <p className="settings-empty">No remote endpoints configured.</p>
          ) : (
            <div className="endpoint-list">
              {endpoints.map((endpoint) => {
                const isEditing = endpointPanel.editing?.id === endpoint.id;
                const isBusy = endpointPanel.busyKey === endpoint.id;
                const availableAgents = endpoint.capabilities?.agents_available || [];
                const remoteWebEnabled = endpoint.capabilities?.tailscale_enabled === true;
                const remoteWebStatus = endpoint.capabilities?.tailscale_status || (remoteWebEnabled ? 'starting' : 'disabled');
                const remoteWebURL = endpoint.capabilities?.tailscale_url;
                const remoteWebAuthURL = endpoint.capabilities?.tailscale_auth_url;
                const remoteWebError = endpoint.capabilities?.tailscale_error;
                const canToggleRemoteWeb = endpoint.status === 'connected' && !endpointPanel.busy;
                const canRebootstrap = endpoint.enabled !== false && !endpointPanel.busy;
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
                            <button className="settings-action" onClick={endpointPanel.cancelEdit} disabled={endpointPanel.busy}>
                              Cancel
                            </button>
                          </>
                        ) : (
                          <button className="settings-action" onClick={() => endpointPanel.beginEdit(endpoint)} disabled={endpointPanel.busy}>
                            Edit
                          </button>
                        )}
                        <button className="settings-action" onClick={() => void handleToggleEndpoint(endpoint)} disabled={endpointPanel.busy}>
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
                        <button className="settings-action danger" onClick={() => void handleRemoveEndpoint(endpoint.id)} disabled={endpointPanel.busy}>
                          Remove
                        </button>
                      </div>
                    </div>
                    {isEditing ? (
                      <div className="settings-form-grid endpoint-form-inline">
                        <input
                          type="text"
                          value={endpointPanel.editing?.name ?? ''}
                          onChange={(e) => endpointPanel.setEdit('name', e.target.value)}
                          className="settings-input"
                          aria-label="Edit endpoint name"
                          disabled={endpointPanel.busy}
                          autoCapitalize="none"
                          autoCorrect="off"
                          spellCheck={false}
                        />
                        <input
                          type="text"
                          value={endpointPanel.editing?.target ?? ''}
                          onChange={(e) => endpointPanel.setEdit('target', e.target.value)}
                          className="settings-input"
                          aria-label="Edit SSH target"
                          disabled={endpointPanel.busy}
                          autoCapitalize="none"
                          autoCorrect="off"
                          spellCheck={false}
                        />
                        <input
                          type="text"
                          value={endpointPanel.editing?.profile ?? ''}
                          onChange={(e) => endpointPanel.setEdit('profile', e.target.value)}
                          className="settings-input"
                          aria-label="Edit profile"
                          placeholder="default"
                          pattern="[a-z0-9][a-z0-9-]{0,15}"
                          disabled={endpointPanel.busy}
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
          Install first-party bundled plugins or add user-owned plugins from a local directory or Git repository.
        </p>
      </div>
      <div className="settings-block-body">
        <div className="settings-inline-form plugin-form">
          <input
            type="text"
            value={pluginPanel.sourcePath}
            onChange={(e) => setPluginSourcePath(e.target.value)}
            placeholder="git@host:team/my-attn-plugin.git or /Users/you/src/my-attn-plugin"
            className="settings-input"
            aria-label="Plugin source"
            disabled={pluginPanel.busy}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
          <button className="settings-action" onClick={() => void handleBrowsePluginPath()} disabled={pluginPanel.busy}>
            Browse
          </button>
          <button className="settings-action" onClick={() => void handleInstallPlugin()} disabled={pluginPanel.busy}>
            Install Plugin
          </button>
        </div>
        {pluginPanel.error && <div className="settings-warning">{pluginPanel.error}</div>}
        {pluginIssues.map((issue) => (
          <div key={issue.path} className="settings-warning">
            {issue.path}: {issue.error}
          </div>
        ))}
        {pluginPanel.loading ? (
          <p className="settings-empty">Loading plugins...</p>
        ) : plugins.length === 0 ? (
          <p className="settings-empty">No plugins available or installed.</p>
        ) : (
          <div className="plugin-list">
            {plugins.map((plugin) => {
              const busy = pluginPanel.busyKey === plugin.name;
              const draftPriority = pluginPanel.priorityDraft(plugin.name, String(plugin.priority));
              const healthStatus = plugin.health_status || 'unknown';
              const installed = plugin.installation_state === 'installed';
              const runtimePhase = plugin.runtime_state || plugin.runtime_phase || (plugin.connected ? 'connected' : plugin.running ? 'starting' : 'stopped');
              return (
                <div key={plugin.name} className="plugin-card">
                  <div className="plugin-card-header">
                    <div className="plugin-card-title">
                      <span className="endpoint-name">{plugin.name}</span>
                      <span className="settings-pill">v{plugin.version}</span>
                      {plugin.availability === 'bundled' && <span className="settings-pill">Bundled</span>}
                      <span className="settings-pill">{installed ? 'Installed' : 'Available'}</span>
                      <span className={`plugin-status-badge ${runtimePhase}`}>
                        {runtimePhase}
                      </span>
                      {installed && (
                        <span className={`plugin-health-badge ${healthStatus}`}>
                          {healthStatus}
                        </span>
                      )}
                    </div>
                    {plugin.availability === 'bundled' && !installed ? (
                      <button className="settings-action" onClick={() => void handleInstallBundledPlugin(plugin.name)} disabled={pluginPanel.busy || !plugin.can_install}>
                        Install
                      </button>
                    ) : plugin.availability === 'bundled' ? (
                      <button className="settings-action danger" onClick={() => void handleUninstallPlugin(plugin.name)} disabled={pluginPanel.busy || !plugin.can_uninstall}>
                        Uninstall
                      </button>
                    ) : (
                      <button className="settings-action danger" onClick={() => void handleRemovePlugin(plugin.name)} disabled={pluginPanel.busy || !plugin.can_uninstall}>
                        Remove
                      </button>
                    )}
                  </div>
                  {plugin.description && <p className="settings-description plugin-description">{plugin.description}</p>}
                  {installed && plugin.health_message && (
                    <div className="settings-warning">
                      Healthcheck: {plugin.health_message}
                    </div>
                  )}
                  {installed && plugin.last_exit && (
                    <div className="settings-warning">
                      Last exit: {plugin.last_exit}
                    </div>
                  )}
                  <div className="plugin-meta-grid">
                    <div className="settings-meta-row">
                      <span className="settings-meta-label">Path</span>
                      <code>{plugin.dir}</code>
                    </div>
                    {plugin.restart_attempt !== undefined && (
                      <div className="settings-meta-row">
                        <span className="settings-meta-label">Restart attempt</span>
                        <code>{plugin.restart_attempt}</code>
                      </div>
                    )}
                    {plugin.next_restart_at && (
                      <div className="settings-meta-row">
                        <span className="settings-meta-label">Next restart</span>
                        <code>{plugin.next_restart_at}</code>
                      </div>
                    )}
                    {installed && <label className="plugin-priority-control">
                      <span className="settings-meta-label">Priority</span>
                      <input
                        type="number"
                        value={draftPriority}
                        onChange={(e) => pluginPanel.setPriorityDraft(plugin.name, e.target.value)}
                        onBlur={() => void commitPluginPriority(plugin.name, plugin.priority)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            void commitPluginPriority(plugin.name, plugin.priority);
                          }
                        }}
                        className="settings-input plugin-priority-input"
                        aria-label={`${plugin.name} priority`}
                        disabled={pluginPanel.busy}
                      />
                      <SavedMark
                        shown={savedFlash.saved(`plugin_priority_${plugin.name}`)}
                        testID={`settings-plugin-priority-saved-${plugin.name}`}
                      />
                    </label>}
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
              const value = executableDrafts.value(agent);
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
                    onChange={(e) => executableDrafts.set(agent, e.target.value)}
                    onBlur={() => executableDrafts.commit(agent)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        executableDrafts.commit(agent);
                      }
                    }}
                    placeholder={agent}
                    className="settings-input"
                    autoCapitalize="none"
                    autoCorrect="off"
                    spellCheck={false}
                  />
                  <SavedMark shown={savedFlash.saved(`${agent}_executable`)} testID={`settings-executable-saved-${agent}`} />
                </div>
              );
            })}
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-editor-exec">Editor</label>
              <span className="settings-status">Used when opening files</span>
              <input
                id="settings-editor-exec"
                type="text"
                value={editorDraft.value}
                onChange={editorDraft.onChange}
                onBlur={editorDraft.commit}
                onKeyDown={editorDraft.onKeyDown}
                placeholder="$EDITOR"
                className="settings-input"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
              <SavedMark shown={savedFlash.saved('editor_executable')} testID="settings-editor-saved" />
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

      <SessionActivitySettings
        settings={settings}
        agents={keeperAgents}
        onSetSetting={onSetSetting}
      />

      <GardenAdvisorSettings
        settings={settings}
        agents={gardenAdvisorAgents}
        onSetSetting={onSetSetting}
      />

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
          <div className="settings-kicker">Agents</div>
          <h3>Auto-approve</h3>
          <p className="settings-description">
            Launches managed agents in their native auto-approve mode (Claude
            "--permission-mode auto", Codex auto-review) so they can run unattended
            without stopping at every permission gate. Off by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Run agents unattended</p>
              <p className="settings-row-copy">
                While off, agents pause for approval on sensitive actions. Yolo sessions
                already bypass approvals and ignore this setting. Changing it only affects
                sessions launched afterward.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-auto-approve-toggle"
              onClick={handleToggleAutoApprove}
            >
              {autoApproveEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Agents</div>
          <h3>Chief-of-staff model &amp; effort</h3>
          <p className="settings-description">
            Pins the model and reasoning effort a chief-of-staff session launches with,
            per agent. Leave blank to use the agent's own default. Only applies to chief
            launches — regular sessions are unaffected.
          </p>
        </div>
        <div className="settings-block-body">
          {chiefOverrideAgentList.length === 0 ? (
            <div className="settings-warning">No installed agent supports a model or effort override.</div>
          ) : (
            <div className="settings-field-grid two-column">
              {chiefOverrideAgentList.map((agent) => {
                const inputId = `settings-chief-model-${agent}`;
                const effortId = `settings-chief-effort-${agent}`;
                const value = chiefModelDrafts.value(agent);
                const effortValue = chiefEffortDrafts.value(agent);
                const effortLevels = CHIEF_EFFORT_LEVELS[agent] || [];
                return (
                  <Fragment key={agent}>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={inputId}>{agentLabel(agent)}</label>
                      <input
                        id={inputId}
                        data-testid={inputId}
                        type="text"
                        value={value}
                        onChange={(e) => chiefModelDrafts.set(agent, e.target.value)}
                        onBlur={() => chiefModelDrafts.commit(agent)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            chiefModelDrafts.commit(agent);
                          }
                        }}
                        placeholder={agent === 'claude' ? 'opus — blank for default' : 'gpt-5.4 — blank for default'}
                        className="settings-input"
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                      <SavedMark shown={savedFlash.saved(`chief_model_${agent}`)} testID={`settings-chief-model-saved-${agent}`} />
                    </div>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={effortId}>{agentLabel(agent)} effort</label>
                      <select
                        id={effortId}
                        data-testid={effortId}
                        className="settings-input"
                        value={effortValue}
                        onChange={(e) => chiefEffortDrafts.apply(agent, e.target.value)}
                      >
                        <option value="">Agent default</option>
                        {effortLevels.map((level) => (
                          <option key={level} value={level}>{level}</option>
                        ))}
                      </select>
                    </div>
                  </Fragment>
                );
              })}
            </div>
          )}
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Agents</div>
          <h3>Default model &amp; effort</h3>
          <p className="settings-description">
            The model and reasoning effort every session of this agent starts
            with. Leave blank to use whatever the agent picks on its own. A
            chief-of-staff override above, or a model pinned when a session is
            launched, still wins over this.
          </p>
        </div>
        <div className="settings-block-body">
          {defaultOverrideAgentList.length === 0 ? (
            <div className="settings-warning">No installed agent supports a model or effort override.</div>
          ) : (
            <div className="settings-field-grid two-column">
              {defaultOverrideAgentList.map((agent) => {
                const inputId = `settings-default-model-${agent}`;
                const effortId = `settings-default-effort-${agent}`;
                const value = defaultModelDrafts.value(agent);
                const effortValue = defaultEffortDrafts.value(agent);
                const effortLevels = CHIEF_EFFORT_LEVELS[agent] || [];
                return (
                  <Fragment key={agent}>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={inputId}>{agentLabel(agent)}</label>
                      <input
                        id={inputId}
                        data-testid={inputId}
                        type="text"
                        value={value}
                        onChange={(e) => defaultModelDrafts.set(agent, e.target.value)}
                        onBlur={() => defaultModelDrafts.commit(agent)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            defaultModelDrafts.commit(agent);
                          }
                        }}
                        placeholder={agent === 'claude' ? 'opus — blank for default' : 'gpt-5.4 — blank for default'}
                        className="settings-input"
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                      <SavedMark shown={savedFlash.saved(`default_model_${agent}`)} testID={`settings-default-model-saved-${agent}`} />
                    </div>
                    <div className="settings-field">
                      <label className="settings-label" htmlFor={effortId}>{agentLabel(agent)} effort</label>
                      <select
                        id={effortId}
                        data-testid={effortId}
                        className="settings-input"
                        value={effortValue}
                        onChange={(e) => defaultEffortDrafts.apply(agent, e.target.value)}
                      >
                        <option value="">Agent default</option>
                        {effortLevels.map((level) => (
                          <option key={level} value={level}>{level}</option>
                        ))}
                      </select>
                    </div>
                  </Fragment>
                );
              })}
            </div>
          )}
        </div>
      </section>

      <SessionCostPriceSettings settings={settings} onSetSetting={onSetSetting} />

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Agents</div>
          <h3>Context window caps</h3>
          <p className="settings-description">
            Token thresholds at which auto-compaction fires instead of waiting for the
            model's full window. Lower caps keep the chief cheaper on each cache-cold
            wake and keep one-shot headless runs (keeper narration, reconciliation,
            workflow subagents) from ballooning; leave those at {DEFAULT_CONTEXT_WINDOW_CAP.toLocaleString()} for
            the default. The per-agent caps apply to every session of that agent —
            raise one to make long-lived sessions compact later. Blank means the
            agent's own compaction behavior; a chief launch still takes the chief cap.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-field-grid">
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-chief-context-cap">Chief of staff</label>
              <input
                id="settings-chief-context-cap"
                data-testid="settings-chief-context-cap"
                type="number"
                min={10000}
                max={2000000}
                step={1000}
                value={chiefContextCapDraft.value}
                onChange={chiefContextCapDraft.onChange}
                onBlur={chiefContextCapDraft.commit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    chiefContextCapDraft.commit();
                  }
                }}
                className="settings-input"
              />
              <SavedMark shown={savedFlash.saved('chief_context_window_cap')} testID="settings-chief-context-cap-saved" />
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-headless-context-cap">Headless runs</label>
              <input
                id="settings-headless-context-cap"
                data-testid="settings-headless-context-cap"
                type="number"
                min={10000}
                max={2000000}
                step={1000}
                value={headlessContextCapDraft.value}
                onChange={headlessContextCapDraft.onChange}
                onBlur={headlessContextCapDraft.commit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    headlessContextCapDraft.commit();
                  }
                }}
                className="settings-input"
              />
              <SavedMark shown={savedFlash.saved('headless_context_window_cap')} testID="settings-headless-context-cap-saved" />
            </div>
            {defaultOverrideAgentList.map((agent) => {
              const inputId = `settings-default-context-cap-${agent}`;
              return (
                <div className="settings-field" key={agent}>
                  <label className="settings-label" htmlFor={inputId}>{agentLabel(agent)} sessions</label>
                  <input
                    id={inputId}
                    data-testid={inputId}
                    type="number"
                    min={10000}
                    max={2000000}
                    step={1000}
                    value={defaultContextCapDrafts.value(agent)}
                    onChange={(e) => defaultContextCapDrafts.set(agent, e.target.value)}
                    onBlur={() => defaultContextCapDrafts.commit(agent)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        defaultContextCapDrafts.commit(agent);
                      }
                    }}
                    placeholder="blank — agent default"
                    className="settings-input"
                  />
                  <SavedMark shown={savedFlash.saved(`default_context_window_cap_${agent}`)} testID={`settings-default-context-cap-saved-${agent}`} />
                </div>
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
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Shared PTY host (experimental)</p>
              <p className="settings-row-copy" id="shared-pty-host-description">
                Off by default. Use a shared Rust process for new terminals and agents to reduce memory use.
                Changes apply to new or explicitly reloaded sessions. Running sessions stay untouched.
              </p>
              <p className="settings-row-copy" data-testid="settings-shared-pty-host-status">
                {ptyBackendMode !== 'migrating'
                  ? 'This setting requires the default daemon backend.'
                  : sharedPtyHostActive
                    ? 'New sessions use the shared Rust host.'
                    : sharedPtyHostEnabled
                      ? 'Shared host unavailable. New sessions use dedicated Go workers; disable and re-enable to retry.'
                      : 'New sessions use dedicated Go workers.'}
              </p>
            </div>
            <button
              type="button"
              role="switch"
              aria-label="Shared PTY host (experimental)"
              aria-checked={sharedPtyHostEnabled}
              aria-describedby="shared-pty-host-description"
              className="settings-action"
              data-testid="settings-shared-pty-host-toggle"
              disabled={ptyBackendMode !== 'migrating'}
              onClick={() => onSetSetting('pty_shared_host_enabled', sharedPtyHostEnabled ? 'false' : 'true')}
            >
              {sharedPtyHostEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>
        </div>
      </section>

      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Models</div>
          <h3>Review Models</h3>
          <p className="settings-description">
            Override the Claude model used for SDK-based review work. Empty value uses the built-in default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-field-grid">
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-reviewer-model">Reviewer model</label>
              <input
                id="settings-reviewer-model"
                type="text"
                value={reviewerModelDraft.value}
                onChange={reviewerModelDraft.onChange}
                onBlur={reviewerModelDraft.commit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    reviewerModelDraft.commit();
                  }
                }}
                placeholder="claude-opus-4-6"
                className="settings-input"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
              <SavedMark shown={savedFlash.saved('reviewer_model')} testID="settings-reviewer-model-saved" />
            </div>
          </div>
        </div>
      </section>
    </>
  );

  const renderKeeperSettings = () => (
    <>
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
              const dutyEnabled = keeperDutyEnabled[duty.key];
              return (
                <div
                  className={`settings-keeper-duty${dutyEnabled ? '' : ' is-disabled'}`}
                  key={duty.key}
                >
                  <div className="settings-keeper-duty-head">
                    <div>
                      <p className="settings-row-title">{duty.title}</p>
                      <p className="settings-row-copy">{duty.description}</p>
                    </div>
                    {duty.enabledSettingKey && (
                      <button
                        type="button"
                        className="settings-action"
                        data-testid={`${duty.testIdPrefix}-toggle`}
                        aria-label={`${dutyEnabled ? 'Disable' : 'Enable'} ${duty.title.toLowerCase()}`}
                        onClick={() => handleToggleKeeperDuty(duty.key)}
                      >
                        {dutyEnabled ? 'Disable' : 'Enable'}
                      </button>
                    )}
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
                        onBlur={() => commitKeeperCustomModel(duty.key)}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter') commitKeeperCustomModel(duty.key);
                        }}
                        placeholder={draft.agent === 'claude' ? 'claude-opus-4-6' : 'model ID'}
                        className="settings-input"
                        autoCapitalize="none"
                        autoCorrect="off"
                        spellCheck={false}
                      />
                    </div>
                  )}
                  <div className="settings-row-inline">
                    <SavedMark
                      shown={savedFlash.saved(duty.settingKey)}
                      testID={`${duty.testIdPrefix}-saved`}
                    />
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
                    <div className="settings-hint">
                      {duty.enabledSettingKey && !dutyEnabled
                        ? `Disabled. Its ${duty.defaultLabel} model setting is preserved.`
                        : `Defaults to ${duty.defaultLabel} when unset.`}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </section>
    </>
  );

  const renderDataSettings = () => (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Local models</div>
          <h3>Terminal viewport capture</h3>
          <p className="settings-description">
            Builds a local corpus for evaluating and training models that understand
            Codex and Claude terminal screens. Collection is off by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-warning">
            Captures exact visible terminal text. Records may contain source code,
            conversations, command output, and secrets. Files stay in this attn profile
            and are never uploaded automatically.
          </div>
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Capture local Codex and Claude sessions</p>
              <p className="settings-row-copy">
                Applies to sessions already running and sessions launched later. Disabling
                collection stops new records but keeps the corpus already on disk.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-model-capture-toggle"
              onClick={handleToggleModelCapture}
            >
              {modelCaptureEnabled ? 'Stop capture' : 'Enable capture'}
            </button>
          </div>
          <div className="settings-field-grid">
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-model-capture-interval">
                Changed-frame interval
              </label>
              <select
                id="settings-model-capture-interval"
                data-testid="settings-model-capture-interval"
                className="settings-input"
                value={modelCaptureInterval}
                onChange={(event) => onSetSetting('model_capture.interval_seconds', event.target.value)}
              >
                {!MODEL_CAPTURE_INTERVAL_OPTIONS.includes(Number(modelCaptureInterval)) && (
                  <option value={modelCaptureInterval}>{modelCaptureInterval} seconds</option>
                )}
                {MODEL_CAPTURE_INTERVAL_OPTIONS.map((seconds) => (
                  <option key={seconds} value={seconds}>{seconds} seconds</option>
                ))}
              </select>
              <span className="settings-hint">
                State changes are captured immediately; unchanged viewports are deduplicated.
              </span>
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-model-capture-max-gb">
                Storage cap
              </label>
              <select
                id="settings-model-capture-max-gb"
                data-testid="settings-model-capture-max-gb"
                className="settings-input"
                value={modelCaptureMaxGB}
                onChange={(event) => onSetSetting('model_capture.max_gb', event.target.value)}
              >
                {!MODEL_CAPTURE_MAX_GB_OPTIONS.includes(Number(modelCaptureMaxGB)) && (
                  <option value={modelCaptureMaxGB}>{modelCaptureMaxGB} GB</option>
                )}
                {MODEL_CAPTURE_MAX_GB_OPTIONS.map((gb) => (
                  <option key={gb} value={gb}>{gb} GB</option>
                ))}
              </select>
              <span className="settings-hint">Oldest hourly files are removed first.</span>
            </div>
          </div>
          <div className="settings-meta-row">
            <span className="settings-meta-label">Captured</span>
            <code data-testid="settings-model-capture-size">{modelCaptureBytes}</code>
          </div>
          <div className="settings-meta-row">
            <span className="settings-meta-label">Folder</span>
            <code data-testid="settings-model-capture-path">{modelCapturePath || 'Waiting for daemon settings…'}</code>
          </div>
        </div>
      </section>
    </>
  );

  const renderHygieneSettings = () => (
    <>
      <section className="settings-block">
        <div className="settings-block-intro">
          <div className="settings-kicker">Sidebar</div>
          <h3>Auto-settle</h3>
          <p className="settings-description">
            Closes a turn for you once you have steered the agent and walked away, so the queue
            does not fill up with turns you have already dealt with. Off by default.
          </p>
        </div>
        <div className="settings-block-body">
          <div className="settings-row-card">
            <div>
              <p className="settings-row-title">Settle a turn once you have steered the agent</p>
              <p className="settings-row-copy">
                When an agent you owe a turn goes back to work and stays there, its terminal
                tile runs a countdown and then settles the turn for you — the same thing{' '}
                {formatShortcut('session.settle')}
                does. Press {formatShortcut('session.cancelCountdown')} to keep the turn instead. Anything that makes the agent want you
                again — a question, an approval, an error, a finished run — cancels it. Off by
                default.
              </p>
            </div>
            <button
              type="button"
              className="settings-action"
              data-testid="settings-auto-settle-toggle"
              onClick={handleToggleAutoSettle}
            >
              {autoSettleEnabled ? 'Disable' : 'Enable'}
            </button>
          </div>

          <div className="settings-field-grid">
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-auto-settle-arm">
                Wait before counting down (seconds)
              </label>
              <input
                id="settings-auto-settle-arm"
                data-testid="settings-auto-settle-arm"
                type="number"
                min={5}
                max={3600}
                step={5}
                value={autoSettleArmDraft.value}
                onChange={autoSettleArmDraft.onChange}
                onBlur={autoSettleArmDraft.commit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    autoSettleArmDraft.commit();
                  }
                }}
                className="settings-input"
              />
              <SavedMark shown={savedFlash.saved(AUTO_SETTLE_ARM_SETTING)} testID="settings-auto-settle-arm-saved" />
              <p className="settings-hint">
                How long the agent must keep working before anything starts. Nothing is shown
                during this window.
              </p>
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-auto-settle-countdown">
                Countdown before settling (seconds)
              </label>
              <input
                id="settings-auto-settle-countdown"
                data-testid="settings-auto-settle-countdown"
                type="number"
                min={3}
                max={600}
                step={1}
                value={autoSettleCountdownDraft.value}
                onChange={autoSettleCountdownDraft.onChange}
                onBlur={autoSettleCountdownDraft.commit}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    autoSettleCountdownDraft.commit();
                  }
                }}
                className="settings-input"
              />
              <SavedMark shown={savedFlash.saved(AUTO_SETTLE_COUNTDOWN_SETTING)} testID="settings-auto-settle-countdown-saved" />
              <p className="settings-hint">
                How long the countdown runs on the tile — your window to press{' '}
                {formatShortcut('session.cancelCountdown')} and keep the
                turn.
              </p>
            </div>
          </div>
        </div>
      </section>

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

  const renderBackgroundTasksSettings = () => (
    <>
      {listTasks && retryTask ? (
        <BackgroundTasksSettings
          listTasks={listTasks}
          retryTask={retryTask}
          taskChangeSignal={taskChangeSignal ?? 0}
        />
      ) : (
        <section className="settings-block">
          <div className="settings-block-intro">
            <div className="settings-kicker">Background Tasks</div>
            <h3>Durable task runner</h3>
            <p className="settings-description">Task runner is unavailable.</p>
          </div>
        </section>
      )}
    </>
  );

  const renderEventBusSettings = () => (
    <EventBusSettings
      getBusStatus={sendBusStatusGet}
      setConsumerEnabled={sendBusSetConsumerEnabled}
    />
  );

  const renderSelectedSection = () => {
    switch (selectedSection) {
      case 'general':
        return renderAppearanceSettings();
      case 'workspace':
        return renderWorkspaceSettings();
      case 'keeper':
        return renderKeeperSettings();
      case 'plugins':
        return renderPluginSettings();
      case 'agents':
        return renderAgentSettings();
      case 'data':
        return renderDataSettings();
      case 'hygiene':
        return renderHygieneSettings();
      case 'backgroundTasks':
        return renderBackgroundTasksSettings();
      case 'eventBus':
        return renderEventBusSettings();
      case 'autoMode':
        return <AutoModeSettings policy={autoModePolicy} />;
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
                      <span className={`settings-nav-count${item.id === 'autoMode' && item.count > 0 ? ' waiting' : ''}`}>
                        {item.count}
                      </span>
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
