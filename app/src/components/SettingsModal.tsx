// app/src/components/SettingsModal.tsx
import { useState, useCallback, useEffect, useMemo } from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import { DaemonEndpoint, DaemonSettings } from '../hooks/useDaemonSocket';
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
  onAddEndpoint: (name: string, sshTarget: string) => Promise<{ success: boolean }>;
  onUpdateEndpoint: (endpointId: string, updates: { name?: string; ssh_target?: string; enabled?: boolean }) => Promise<{ success: boolean }>;
  onRemoveEndpoint: (endpointId: string) => Promise<{ success: boolean }>;
  onSetEndpointRemoteWeb: (endpointId: string, enabled: boolean) => Promise<{ success: boolean }>;
  onSetSetting: (key: string, value: string) => void;
  themePreference: ThemePreference;
  onSetTheme: (theme: ThemePreference) => void;
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
  onAddEndpoint,
  onUpdateEndpoint,
  onRemoveEndpoint,
  onSetEndpointRemoteWeb,
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
  const [editingEndpointID, setEditingEndpointID] = useState<string | null>(null);
  const [editingEndpointName, setEditingEndpointName] = useState('');
  const [editingEndpointTarget, setEditingEndpointTarget] = useState('');
  const [endpointError, setEndpointError] = useState<string | null>(null);
  const [endpointActionID, setEndpointActionID] = useState<string | null>(null);
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

  useEffect(() => {
    if (!isOpen) return;

    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') {
        return;
      }
      e.preventDefault();
      e.stopPropagation();
      onClose();
    };

    window.addEventListener('keydown', handleGlobalKeyDown, true);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown, true);
  }, [isOpen, onClose]);

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
    if (!name || !sshTarget) {
      setEndpointError('Endpoint name and SSH target are required.');
      return;
    }
    setEndpointError(null);
    setEndpointActionID('new');
    try {
      await onAddEndpoint(name, sshTarget);
      setNewEndpointName('');
      setNewEndpointTarget('');
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to add endpoint');
    } finally {
      setEndpointActionID(null);
    }
  }, [newEndpointName, newEndpointTarget, onAddEndpoint]);

  const beginEditEndpoint = useCallback((endpoint: DaemonEndpoint) => {
    setEndpointError(null);
    setEditingEndpointID(endpoint.id);
    setEditingEndpointName(endpoint.name);
    setEditingEndpointTarget(endpoint.ssh_target);
  }, []);

  const cancelEditEndpoint = useCallback(() => {
    setEditingEndpointID(null);
    setEditingEndpointName('');
    setEditingEndpointTarget('');
  }, []);

  const handleSaveEndpoint = useCallback(async (endpointId: string) => {
    const name = editingEndpointName.trim();
    const sshTarget = editingEndpointTarget.trim();
    if (!name || !sshTarget) {
      setEndpointError('Endpoint name and SSH target are required.');
      return;
    }
    setEndpointError(null);
    setEndpointActionID(endpointId);
    try {
      await onUpdateEndpoint(endpointId, { name, ssh_target: sshTarget });
      cancelEditEndpoint();
    } catch (error) {
      setEndpointError(error instanceof Error ? error.message : 'Failed to update endpoint');
    } finally {
      setEndpointActionID(null);
    }
  }, [cancelEditEndpoint, editingEndpointName, editingEndpointTarget, onUpdateEndpoint]);

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

  if (!isOpen) return null;

  return (
    <div className="settings-overlay" onClick={onClose}>
      <div className="settings-modal" onClick={e => e.stopPropagation()}>
        <div className="settings-header">
          <h2>Settings</h2>
          <button className="settings-close" onClick={onClose}>×</button>
        </div>
        <div className="settings-body">
          <div className="settings-section">
            <h3>Theme</h3>
            <div className="settings-agent-toggle" role="radiogroup" aria-label="Theme preference">
              <button
                type="button"
                className={`agent-option ${themePreference === 'dark' ? 'active' : ''}`}
                onClick={() => onSetTheme('dark')}
                aria-checked={themePreference === 'dark'}
              >
                Dark
              </button>
              <button
                type="button"
                className={`agent-option ${themePreference === 'light' ? 'active' : ''}`}
                onClick={() => onSetTheme('light')}
                aria-checked={themePreference === 'light'}
              >
                Light
              </button>
              <button
                type="button"
                className={`agent-option ${themePreference === 'system' ? 'active' : ''}`}
                onClick={() => onSetTheme('system')}
                aria-checked={themePreference === 'system'}
              >
                System
              </button>
            </div>
          </div>

          <div className="settings-section">
            <h3>Projects Directory</h3>
            <p className="settings-description">
              Directory where your Git repositories are cloned. Used to open PRs in worktrees.
            </p>
            <div className="projects-dir-input">
              <input
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
              <button className="browse-btn" onClick={handleBrowse}>
                Browse
              </button>
            </div>
          </div>

          <div className="settings-section">
            <h3>Mobile Web Client</h3>
            <p className="settings-description">
              Expose the daemon through this machine&apos;s existing Tailscale device identity so the embedded mobile web client can attach to running sessions from Safari or any browser.
            </p>
            <div className="settings-row-inline">
              <span className="settings-label">Tailscale Serve</span>
              <button className="browse-btn" onClick={handleToggleTailscale}>
                {tailscaleEnabled ? 'Disable' : 'Enable'}
              </button>
            </div>
            <div className="settings-hint">Status: {tailscaleStatus}</div>
            {tailscaleDomain && (
              <div className="settings-hint">Device DNS name: <code>{tailscaleDomain}</code></div>
            )}
            {tailscaleURL && (
              <div className="settings-hint">
                Web URL: <a href={tailscaleURL} target="_blank" rel="noreferrer">{tailscaleURL}</a>
              </div>
            )}
            {tailscaleAuthURL && (
              <div className="settings-agent-warning">
                Sign this machine into Tailscale:
                {' '}
                <a href={tailscaleAuthURL} target="_blank" rel="noreferrer">{tailscaleAuthURL}</a>
              </div>
            )}
            {tailscaleError && (
              <div className="settings-agent-warning">{tailscaleError}</div>
            )}
            <div className="settings-hint">
              This uses the host Tailscale client and does not register a second tailnet device for attn.
            </div>
          </div>

          <div className="settings-section">
            <h3>GitHub Hosts</h3>
            <p className="settings-description">
              Hosts currently detected from authenticated PRs.
            </p>
            {connectedHosts.length === 0 ? (
              <p className="settings-empty">No authenticated hosts detected.</p>
            ) : (
              <div className="host-list">
                {connectedHosts.map((host) => (
                  <span key={host} className="host-pill">{host}</span>
                ))}
              </div>
            )}
            <div className="settings-hint">Add hosts with `gh auth login --hostname &lt;host&gt;`.</div>
          </div>

          <div className="settings-section">
            <h3>Remote Endpoints</h3>
            <p className="settings-description">
              SSH targets that the local daemon bootstraps and keeps connected as remote attn peers.
            </p>
            <div className="endpoint-form">
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
              <button
                className="browse-btn"
                onClick={() => void handleAddEndpoint()}
                disabled={endpointActionInFlight}
              >
                Add Endpoint
              </button>
            </div>
            {endpointError && <div className="settings-agent-warning">{endpointError}</div>}
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
                  return (
                    <div key={endpoint.id} className={`endpoint-card status-${endpoint.status}`}>
                      <div className="endpoint-card-header">
                        <div className="endpoint-card-title">
                          <span className="endpoint-name">{endpoint.name}</span>
                          <span className={`endpoint-status-badge status-${endpoint.status}`}>
                            {endpoint.status}
                          </span>
                        </div>
                        <div className="endpoint-card-actions">
                          {isEditing ? (
                            <>
                              <button className="browse-btn" onClick={() => void handleSaveEndpoint(endpoint.id)} disabled={isBusy}>
                                Save
                              </button>
                              <button className="browse-btn" onClick={cancelEditEndpoint} disabled={endpointActionInFlight}>
                                Cancel
                              </button>
                            </>
                          ) : (
                            <button className="browse-btn" onClick={() => beginEditEndpoint(endpoint)} disabled={endpointActionInFlight}>
                              Edit
                            </button>
                          )}
                          <button className="browse-btn" onClick={() => void handleToggleEndpoint(endpoint)} disabled={endpointActionInFlight}>
                            {endpoint.enabled === false ? 'Enable' : 'Disable'}
                          </button>
                          <button
                            className="browse-btn"
                            onClick={() => void handleSetEndpointRemoteWeb(endpoint.id, !remoteWebEnabled)}
                            disabled={!canToggleRemoteWeb}
                          >
                            {remoteWebEnabled ? 'Disable Web' : 'Enable Web'}
                          </button>
                          <button className="browse-btn danger" onClick={() => void handleRemoveEndpoint(endpoint.id)} disabled={endpointActionInFlight}>
                            Remove
                          </button>
                        </div>
                      </div>
                      {isEditing ? (
                        <div className="endpoint-form endpoint-form-inline">
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
                        </div>
                      ) : (
                        <div className="endpoint-summary">
                          <div className="endpoint-meta">
                            <span className="endpoint-meta-label">SSH</span>
                            <code>{endpoint.ssh_target}</code>
                          </div>
                          <div className="endpoint-meta">
                            <span className="endpoint-meta-label">Enabled</span>
                            <span>{endpoint.enabled === false ? 'No' : 'Yes'}</span>
                          </div>
                          {endpoint.status_message && (
                            <div className="endpoint-meta">
                              <span className="endpoint-meta-label">Status</span>
                              <span>{endpoint.status_message}</span>
                            </div>
                          )}
                          {endpoint.capabilities && (
                            <>
                              <div className="endpoint-meta">
                                <span className="endpoint-meta-label">Protocol</span>
                                <span>{endpoint.capabilities.protocol_version}</span>
                              </div>
                              <div className="endpoint-meta">
                                <span className="endpoint-meta-label">PTY</span>
                                <span>{endpoint.capabilities.pty_backend_mode || 'unknown'}</span>
                              </div>
                              <div className="endpoint-meta">
                                <span className="endpoint-meta-label">Sessions</span>
                                <span>{endpoint.session_count ?? 0}</span>
                              </div>
                              <div className="endpoint-meta">
                                <span className="endpoint-meta-label">Remote Web</span>
                                <span>{remoteWebStatus}</span>
                              </div>
                              <div className="endpoint-meta">
                                <span className="endpoint-meta-label">Agents</span>
                                <span>{availableAgents.length > 0 ? availableAgents.join(', ') : 'none reported'}</span>
                              </div>
                              {remoteWebURL && (
                                <div className="endpoint-meta">
                                  <span className="endpoint-meta-label">Remote URL</span>
                                  <a href={remoteWebURL} target="_blank" rel="noreferrer">{remoteWebURL}</a>
                                </div>
                              )}
                              {remoteWebAuthURL && (
                                <div className="settings-agent-warning">
                                  Sign this host into Tailscale:
                                  {' '}
                                  <a href={remoteWebAuthURL} target="_blank" rel="noreferrer">{remoteWebAuthURL}</a>
                                </div>
                              )}
                              {endpoint.capabilities.tailscale_domain && !remoteWebURL && (
                                <div className="endpoint-meta">
                                  <span className="endpoint-meta-label">Remote DNS</span>
                                  <code>{endpoint.capabilities.tailscale_domain}</code>
                                </div>
                              )}
                              {remoteWebError && (
                                <div className="settings-agent-warning">{remoteWebError}</div>
                              )}
                              {endpoint.capabilities.projects_directory && (
                                <div className="endpoint-meta">
                                  <span className="endpoint-meta-label">Projects</span>
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

          <div className="settings-section">
            <h3>Executables</h3>
            <p className="settings-description">
              Override the CLI used to launch agents. Leave empty to use the default on your PATH.
            </p>
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

          <div className="settings-section">
            <h3>Review Loop Prompts</h3>
            <p className="settings-description">
              Manage saved custom prompts for session review loops. Built-in presets stay available in the loop bar.
            </p>
            <div className="review-loop-settings">
              <div className="review-loop-settings-list">
                <div className="settings-row-inline">
                  <label className="settings-label" htmlFor="review-loop-preset-select">Saved prompts</label>
                  <button className="browse-btn" onClick={handleNewReviewLoopPreset}>New</button>
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
                  className="browse-btn"
                  onClick={handleSaveReviewLoopPreset}
                  disabled={!reviewLoopPresetName.trim() || !reviewLoopPrompt.trim()}
                >
                  Save Prompt
                </button>
                <button
                  className="browse-btn danger"
                  onClick={handleDeleteReviewLoopPreset}
                  disabled={!selectedReviewLoopPresetID}
                >
                  Delete
                </button>
              </div>
            </div>
          </div>

          <div className="settings-section">
            <h3>Review Models</h3>
            <p className="settings-description">
              Override the Claude models used for SDK-based review work. Leave empty to use the built-in defaults.
            </p>
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

          <div className="settings-section">
            <h3>Default Session Agent</h3>
            <p className="settings-description">
              Used for new sessions and when opening PRs. You can override per session in the new session dialog.
            </p>
            {!hasAvailableAgents && (
              <div className="settings-agent-warning">No supported agent CLI found in PATH.</div>
            )}
            <div className="settings-agent-toggle" role="radiogroup" aria-label="Default session agent">
              {orderedAgentList.map((agent) => {
                const available = isAgentAvailable(agentAvailability, agent);
                return (
                  <button
                    key={agent}
                    type="button"
                    className={`agent-option ${defaultAgent === agent ? 'active' : ''}`}
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

          <div className="settings-section">
            <h3>Agent Capabilities</h3>
            <p className="settings-description">
              Shows which optional integration features are enabled per agent.
            </p>
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

          <div className="settings-section">
            <h3>PTY Backend</h3>
            <p className="settings-description">
              Shows whether terminal sessions run in external worker processes or directly in the daemon.
            </p>
            <div className="settings-field">
              <label className="settings-label">Runtime mode</label>
              <span className={`settings-status mode-${ptyBackendMode}`}>
                {ptyBackendLabel}
              </span>
              <div className="settings-hint">{ptyBackendHint}</div>
            </div>
          </div>

          <div className="settings-section">
            <h3>Muted Repositories</h3>
            {mutedRepos.length === 0 ? (
              <p className="settings-empty">No muted repositories</p>
            ) : (
              <ul className="muted-items-list">
                {mutedRepos.map(repo => (
                  <li key={repo} className="muted-item">
                    <span className="muted-item-name">{repo}</span>
                    <button
                      className="unmute-btn"
                      onClick={() => onUnmuteRepo(repo)}
                    >
                      Unmute
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="settings-section">
            <h3>Muted Authors</h3>
            {mutedAuthors.length === 0 ? (
              <p className="settings-empty">No muted authors</p>
            ) : (
              <ul className="muted-items-list">
                {mutedAuthors.map(author => (
                  <li key={author} className="muted-item">
                    <span className="muted-item-name">{author.toLowerCase().includes('bot') ? '🤖' : '👤'} {author}</span>
                    <button
                      className="unmute-btn"
                      onClick={() => onUnmuteAuthor(author)}
                    >
                      Unmute
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
