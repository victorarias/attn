// app/src/components/ChangesPanel.tsx
import { useMemo } from 'react';
import type { GitStatusUpdate } from '../hooks/useDaemonSocket';
import './ChangesPanel.css';

interface ChangesPanelProps {
  gitStatus: GitStatusUpdate | null;
  attentionCount: number;
  selectedFile: string | null;
  onFileSelect: (path: string, staged: boolean) => void;
  onAttentionClick: () => void;
}

export function ChangesPanel({
  gitStatus,
  attentionCount,
  selectedFile,
  onFileSelect,
  onAttentionClick,
}: ChangesPanelProps) {
  const totalStats = useMemo(() => {
    if (!gitStatus) return { files: 0, additions: 0, deletions: 0 };

    const allFiles = [...gitStatus.staged, ...gitStatus.unstaged, ...gitStatus.untracked];
    const additions = allFiles.reduce((sum, f) => sum + (f.additions || 0), 0);
    const deletions = allFiles.reduce((sum, f) => sum + (f.deletions || 0), 0);

    return { files: allFiles.length, additions, deletions };
  }, [gitStatus]);

  const getStatusIcon = (status: string) => {
    switch (status) {
      case 'modified': return 'M';
      case 'added': return 'A';
      case 'deleted': return 'D';
      case 'renamed': return 'R';
      case 'untracked': return '?';
      default: return 'M';
    }
  };

  const getFileName = (path: string) => {
    return path.split('/').pop() || path;
  };

  return (
    <div className="changes-panel">
      <div className="changes-header">
        <span className="changes-title">Changes</span>
        <div className="changes-header-actions">
          {attentionCount > 0 && (
            <button className="attention-badge" onClick={onAttentionClick}>
              ⚠ {attentionCount}
            </button>
          )}
        </div>
      </div>

      <div className="changes-body">
        {gitStatus?.error ? (
          <div className="changes-error">{gitStatus.error}</div>
        ) : totalStats.files === 0 ? (
          <div className="changes-empty">No changes</div>
        ) : (
          <>
            {gitStatus?.staged && gitStatus.staged.length > 0 && (
              <div className="changes-section">
                <div className="section-header">Staged ({gitStatus.staged.length})</div>
                {gitStatus.staged.map((file) => (
                  <div
                    key={`staged-${file.path}`}
                    className={`change-file ${selectedFile === file.path ? 'selected' : ''}`}
                    onClick={() => onFileSelect(file.path, true)}
                    title={file.path}
                  >
                    <span className={`file-status ${file.status}`}>
                      {getStatusIcon(file.status)}
                    </span>
                    <span className="file-name">{getFileName(file.path)}</span>
                    {(file.additions !== undefined || file.deletions !== undefined) && (
                      <span className="file-stats">
                        {file.additions !== undefined && file.additions > 0 && (
                          <span className="stat-add">+{file.additions}</span>
                        )}
                        {file.deletions !== undefined && file.deletions > 0 && (
                          <span className="stat-del">-{file.deletions}</span>
                        )}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}

            {gitStatus?.unstaged && gitStatus.unstaged.length > 0 && (
              <div className="changes-section">
                <div className="section-header">Changes ({gitStatus.unstaged.length})</div>
                {gitStatus.unstaged.map((file) => (
                  <div
                    key={`unstaged-${file.path}`}
                    className={`change-file ${selectedFile === file.path ? 'selected' : ''}`}
                    onClick={() => onFileSelect(file.path, false)}
                    title={file.path}
                  >
                    <span className={`file-status ${file.status}`}>
                      {getStatusIcon(file.status)}
                    </span>
                    <span className="file-name">{getFileName(file.path)}</span>
                    {(file.additions !== undefined || file.deletions !== undefined) && (
                      <span className="file-stats">
                        {file.additions !== undefined && file.additions > 0 && (
                          <span className="stat-add">+{file.additions}</span>
                        )}
                        {file.deletions !== undefined && file.deletions > 0 && (
                          <span className="stat-del">-{file.deletions}</span>
                        )}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}

            {gitStatus?.untracked && gitStatus.untracked.length > 0 && (
              <div className="changes-section">
                <div className="section-header">Untracked ({gitStatus.untracked.length})</div>
                {gitStatus.untracked.map((file) => (
                  <div
                    key={`untracked-${file.path}`}
                    className={`change-file ${selectedFile === file.path ? 'selected' : ''}`}
                    onClick={() => onFileSelect(file.path, false)}
                    title={file.path}
                  >
                    <span className="file-status untracked">?</span>
                    <span className="file-name">{getFileName(file.path)}</span>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>

      {totalStats.files > 0 && (
        <div className="changes-footer">
          <span>{totalStats.files} files</span>
          {totalStats.additions > 0 && <span className="stat-add">+{totalStats.additions}</span>}
          {totalStats.deletions > 0 && <span className="stat-del">-{totalStats.deletions}</span>}
        </div>
      )}
    </div>
  );
}
