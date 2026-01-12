// app/src/components/SettingsModal.tsx
import { useState, useCallback, useEffect } from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import { DaemonSettings } from '../hooks/useDaemonSocket';
import './SettingsModal.css';

interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  mutedRepos: string[];
  onUnmuteRepo: (repo: string) => void;
  settings: DaemonSettings;
  onSetSetting: (key: string, value: string) => void;
}

export function SettingsModal({
  isOpen,
  onClose,
  mutedRepos,
  onUnmuteRepo,
  settings,
  onSetSetting,
}: SettingsModalProps) {
  const [projectsDir, setProjectsDir] = useState(settings.projects_directory || '');
  const [claudeExecutable, setClaudeExecutable] = useState(settings.claude_executable || '');
  const [codexExecutable, setCodexExecutable] = useState(settings.codex_executable || '');

  // Sync with settings when modal opens
  const actualProjectsDir = settings.projects_directory || '';
  const actualClaudeExecutable = settings.claude_executable || '';
  const actualCodexExecutable = settings.codex_executable || '';

  useEffect(() => {
    if (!isOpen) return;
    setProjectsDir(actualProjectsDir);
    setClaudeExecutable(actualClaudeExecutable);
    setCodexExecutable(actualCodexExecutable);
  }, [isOpen, actualProjectsDir, actualClaudeExecutable, actualCodexExecutable]);

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
          </div>

          <div className="settings-section">
            <h3>Muted Repositories</h3>
            {mutedRepos.length === 0 ? (
              <p className="settings-empty">No muted repositories</p>
            ) : (
              <ul className="muted-repos-list">
                {mutedRepos.map(repo => (
                  <li key={repo} className="muted-repo-item">
                    <span className="muted-repo-name">{repo}</span>
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
        </div>
      </div>
    </div>
  );
}
