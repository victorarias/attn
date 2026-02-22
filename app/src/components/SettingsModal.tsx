// app/src/components/SettingsModal.tsx
import { useState, useCallback, useEffect, useMemo } from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import { DaemonSettings } from '../hooks/useDaemonSocket';
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
  onSetSetting,
  themePreference,
  onSetTheme,
}: SettingsModalProps) {
  const [projectsDir, setProjectsDir] = useState(settings.projects_directory || '');
  const [agentExecutables, setAgentExecutables] = useState<Record<SessionAgent, string>>({});
  const [editorExecutable, setEditorExecutable] = useState(settings.editor_executable || '');
  const [defaultAgent, setDefaultAgent] = useState<SessionAgent>('claude');
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  // Sync with settings when modal opens
  const actualProjectsDir = settings.projects_directory || '';
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

  useEffect(() => {
    if (!isOpen) return;
    setProjectsDir(actualProjectsDir);
    setAgentExecutables(actualAgentExecutables);
    setEditorExecutable(actualEditorExecutable);
    setDefaultAgent(resolvedDefaultAgent);
  }, [isOpen, actualProjectsDir, actualAgentExecutables, actualEditorExecutable, resolvedDefaultAgent]);

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
              />
              <button className="browse-btn" onClick={handleBrowse}>
                Browse
              </button>
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
