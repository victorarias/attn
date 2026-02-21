// app/src/components/SettingsModal.tsx
import { useState, useCallback, useEffect, useMemo } from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import { DaemonSettings } from '../hooks/useDaemonSocket';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import type { ThemePreference } from '../hooks/useTheme';
import {
  getAgentAvailability,
  hasAnyAvailableAgents,
  isAgentAvailable,
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
  const [claudeExecutable, setClaudeExecutable] = useState(settings.claude_executable || '');
  const [codexExecutable, setCodexExecutable] = useState(settings.codex_executable || '');
  const [copilotExecutable, setCopilotExecutable] = useState(settings.copilot_executable || '');
  const [editorExecutable, setEditorExecutable] = useState(settings.editor_executable || '');
  const [defaultAgent, setDefaultAgent] = useState<SessionAgent>('claude');
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = useMemo(
    () => hasAnyAvailableAgents(agentAvailability),
    [agentAvailability],
  );

  // Sync with settings when modal opens
  const actualProjectsDir = settings.projects_directory || '';
  const actualClaudeExecutable = settings.claude_executable || '';
  const actualCodexExecutable = settings.codex_executable || '';
  const actualCopilotExecutable = settings.copilot_executable || '';
  const actualEditorExecutable = settings.editor_executable || '';
  const actualDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
  const resolvedDefaultAgent = resolvePreferredAgent(actualDefaultAgent, agentAvailability, 'codex');
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
    setClaudeExecutable(actualClaudeExecutable);
    setCodexExecutable(actualCodexExecutable);
    setCopilotExecutable(actualCopilotExecutable);
    setEditorExecutable(actualEditorExecutable);
    setDefaultAgent(resolvedDefaultAgent);
  }, [isOpen, actualProjectsDir, actualClaudeExecutable, actualCodexExecutable, actualCopilotExecutable, actualEditorExecutable, resolvedDefaultAgent]);

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

  const handleClaudeChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setClaudeExecutable(e.target.value);
  }, []);

  const handleCodexChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setCodexExecutable(e.target.value);
  }, []);

  const handleEditorChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setEditorExecutable(e.target.value);
  }, []);

  const handleCopilotChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setCopilotExecutable(e.target.value);
  }, []);

  const handleClaudeBlur = useCallback(() => {
    if (claudeExecutable !== actualClaudeExecutable) {
      onSetSetting('claude_executable', claudeExecutable);
    }
  }, [claudeExecutable, actualClaudeExecutable, onSetSetting]);

  const handleCodexBlur = useCallback(() => {
    if (codexExecutable !== actualCodexExecutable) {
      onSetSetting('codex_executable', codexExecutable);
    }
  }, [codexExecutable, actualCodexExecutable, onSetSetting]);

  const handleEditorBlur = useCallback(() => {
    if (editorExecutable !== actualEditorExecutable) {
      onSetSetting('editor_executable', editorExecutable);
    }
  }, [editorExecutable, actualEditorExecutable, onSetSetting]);

  const handleCopilotBlur = useCallback(() => {
    if (copilotExecutable !== actualCopilotExecutable) {
      onSetSetting('copilot_executable', copilotExecutable);
    }
  }, [copilotExecutable, actualCopilotExecutable, onSetSetting]);

  const handleClaudeKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      if (claudeExecutable !== actualClaudeExecutable) {
        onSetSetting('claude_executable', claudeExecutable);
      }
    }
  }, [claudeExecutable, actualClaudeExecutable, onSetSetting]);

  const handleCodexKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      if (codexExecutable !== actualCodexExecutable) {
        onSetSetting('codex_executable', codexExecutable);
      }
    }
  }, [codexExecutable, actualCodexExecutable, onSetSetting]);

  const handleEditorKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      if (editorExecutable !== actualEditorExecutable) {
        onSetSetting('editor_executable', editorExecutable);
      }
    }
  }, [editorExecutable, actualEditorExecutable, onSetSetting]);

  const handleCopilotKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      if (copilotExecutable !== actualCopilotExecutable) {
        onSetSetting('copilot_executable', copilotExecutable);
      }
    }
  }, [copilotExecutable, actualCopilotExecutable, onSetSetting]);

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
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-claude-exec">Claude Code</label>
              <span className={`settings-status ${agentAvailability.claude ? 'available' : 'missing'}`}>
                {agentAvailability.claude ? 'Found in PATH' : 'Not found in PATH'}
              </span>
              <input
                id="settings-claude-exec"
                type="text"
                value={claudeExecutable}
                onChange={handleClaudeChange}
                onBlur={handleClaudeBlur}
                onKeyDown={handleClaudeKeyDown}
                placeholder="claude"
                className="settings-input"
              />
            </div>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-codex-exec">Codex</label>
              <span className={`settings-status ${agentAvailability.codex ? 'available' : 'missing'}`}>
                {agentAvailability.codex ? 'Found in PATH' : 'Not found in PATH'}
              </span>
              <input
                id="settings-codex-exec"
                type="text"
                value={codexExecutable}
                onChange={handleCodexChange}
                onBlur={handleCodexBlur}
                onKeyDown={handleCodexKeyDown}
                placeholder="codex"
                className="settings-input"
              />
            </div>
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
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-copilot-exec">Copilot</label>
              <span className={`settings-status ${agentAvailability.copilot ? 'available' : 'missing'}`}>
                {agentAvailability.copilot ? 'Found in PATH' : 'Not found in PATH'}
              </span>
              <input
                id="settings-copilot-exec"
                type="text"
                value={copilotExecutable}
                onChange={handleCopilotChange}
                onBlur={handleCopilotBlur}
                onKeyDown={handleCopilotKeyDown}
                placeholder="copilot"
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
              <div className="settings-agent-warning">No supported agent CLIs found in PATH.</div>
            )}
            <div className="settings-agent-toggle" role="radiogroup" aria-label="Default session agent">
              <button
                type="button"
                className={`agent-option ${defaultAgent === 'codex' ? 'active' : ''}`}
                onClick={() => handleDefaultAgentChange('codex')}
                aria-checked={defaultAgent === 'codex'}
                disabled={!agentAvailability.codex}
                title={!agentAvailability.codex ? 'Codex CLI not found in PATH' : undefined}
              >
                Codex
              </button>
              <button
                type="button"
                className={`agent-option ${defaultAgent === 'claude' ? 'active' : ''}`}
                onClick={() => handleDefaultAgentChange('claude')}
                aria-checked={defaultAgent === 'claude'}
                disabled={!agentAvailability.claude}
                title={!agentAvailability.claude ? 'Claude CLI not found in PATH' : undefined}
              >
                Claude
              </button>
              <button
                type="button"
                className={`agent-option ${defaultAgent === 'copilot' ? 'active' : ''}`}
                onClick={() => handleDefaultAgentChange('copilot')}
                aria-checked={defaultAgent === 'copilot'}
                disabled={!agentAvailability.copilot}
                title={!agentAvailability.copilot ? 'Copilot CLI not found in PATH' : undefined}
              >
                Copilot
              </button>
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
