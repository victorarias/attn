// app/src/components/SettingsModal.tsx
import { useState, useCallback, useEffect } from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import { DaemonSettings } from '../hooks/useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import './SettingsModal.css';

interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  mutedRepos: string[];
  onUnmuteRepo: (repo: string) => void;
  mutedAuthors: string[];
  onUnmuteAuthor: (author: string) => void;
  settings: DaemonSettings;
  onSetSetting: (key: string, value: string) => void;
}

export function SettingsModal({
  isOpen,
  onClose,
  mutedRepos,
  onUnmuteRepo,
  mutedAuthors,
  onUnmuteAuthor,
  settings,
  onSetSetting,
}: SettingsModalProps) {
  const [projectsDir, setProjectsDir] = useState(settings.projects_directory || '');
  const [claudeExecutable, setClaudeExecutable] = useState(settings.claude_executable || '');
  const [codexExecutable, setCodexExecutable] = useState(settings.codex_executable || '');
  const [editorExecutable, setEditorExecutable] = useState(settings.editor_executable || '');
  const [defaultAgent, setDefaultAgent] = useState<SessionAgent>((settings.new_session_agent as SessionAgent) || 'claude');

  // Sync with settings when modal opens
  const actualProjectsDir = settings.projects_directory || '';
  const actualClaudeExecutable = settings.claude_executable || '';
  const actualCodexExecutable = settings.codex_executable || '';
  const actualEditorExecutable = settings.editor_executable || '';
  const actualDefaultAgent = (settings.new_session_agent as SessionAgent) || 'claude';

  useEffect(() => {
    if (!isOpen) return;
    setProjectsDir(actualProjectsDir);
    setClaudeExecutable(actualClaudeExecutable);
    setCodexExecutable(actualCodexExecutable);
    setEditorExecutable(actualEditorExecutable);
    setDefaultAgent(actualDefaultAgent);
  }, [isOpen, actualProjectsDir, actualClaudeExecutable, actualCodexExecutable, actualEditorExecutable, actualDefaultAgent]);

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

  const handleDefaultAgentChange = useCallback((agent: SessionAgent) => {
    setDefaultAgent(agent);
    if (agent !== actualDefaultAgent) {
      onSetSetting('new_session_agent', agent);
    }
  }, [actualDefaultAgent, onSetSetting]);

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
            <h3>Executables</h3>
            <p className="settings-description">
              Override the CLI used to launch agents. Leave empty to use the default on your PATH.
            </p>
            <div className="settings-field">
              <label className="settings-label" htmlFor="settings-claude-exec">Claude Code</label>
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
          </div>

          <div className="settings-section">
            <h3>Default Session Agent</h3>
            <p className="settings-description">
              Used for new sessions and when opening PRs. You can override per session in the new session dialog.
            </p>
            <div className="settings-agent-toggle" role="radiogroup" aria-label="Default session agent">
              <button
                type="button"
                className={`agent-option ${defaultAgent === 'codex' ? 'active' : ''}`}
                onClick={() => handleDefaultAgentChange('codex')}
                aria-checked={defaultAgent === 'codex'}
              >
                Codex
              </button>
              <button
                type="button"
                className={`agent-option ${defaultAgent === 'claude' ? 'active' : ''}`}
                onClick={() => handleDefaultAgentChange('claude')}
                aria-checked={defaultAgent === 'claude'}
              >
                Claude
              </button>
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
