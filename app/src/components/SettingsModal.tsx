// app/src/components/SettingsModal.tsx
import './SettingsModal.css';

interface SettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  mutedRepos: string[];
  onUnmuteRepo: (repo: string) => void;
}

export function SettingsModal({ isOpen, onClose, mutedRepos, onUnmuteRepo }: SettingsModalProps) {
  if (!isOpen) return null;

  return (
    <div className="settings-overlay" onClick={onClose}>
      <div className="settings-modal" onClick={e => e.stopPropagation()}>
        <div className="settings-header">
          <h2>Settings</h2>
          <button className="settings-close" onClick={onClose}>×</button>
        </div>
        <div className="settings-body">
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
  );
}
