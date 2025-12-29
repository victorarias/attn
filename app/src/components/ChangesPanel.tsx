// app/src/components/ChangesPanel.tsx
import { useMemo, memo, useCallback } from 'react';
import type { GitStatusUpdate } from '../hooks/useDaemonSocket';
import './ChangesPanel.css';

interface ChangesPanelProps {
  gitStatus: GitStatusUpdate | null;
  attentionCount: number;
  selectedFile: string | null;
  onFileSelect: (path: string, staged: boolean) => void;
  onAttentionClick: () => void;
}

type GitFileChange = {
  path: string;
  status: string;
  additions?: number;
  deletions?: number;
};

type TreeNode = {
  type: 'file' | 'dir';
  name: string;  // abbreviated path for dirs, filename for files
  fullPath?: string;  // for dirs
  file?: GitFileChange;  // for files
  children?: TreeNode[];  // for dirs
};

// Abbreviate path if more than 3 levels deep
// Example: src/components/forms/inputs -> s/c/forms/inputs
function abbreviatePath(path: string): string {
  const parts = path.split('/');
  if (parts.length <= 3) {
    return path;
  }

  const toAbbreviate = parts.length - 3;
  const abbreviated = parts.slice(0, toAbbreviate).map(p => p[0] || '');
  const rest = parts.slice(toAbbreviate);

  return [...abbreviated, ...rest].join('/');
}

// Build tree structure from flat file list
function buildTree(files: GitFileChange[]): TreeNode[] {
  // Group files by directory
  const dirToFiles: Map<string, GitFileChange[]> = new Map();

  files.forEach(file => {
    const parts = file.path.split('/');
    if (parts.length === 1) {
      // File at root
      if (!dirToFiles.has('')) {
        dirToFiles.set('', []);
      }
      dirToFiles.get('')!.push(file);
    } else {
      // File in directory
      const dir = parts.slice(0, -1).join('/');
      if (!dirToFiles.has(dir)) {
        dirToFiles.set(dir, []);
      }
      dirToFiles.get(dir)!.push(file);
    }
  });

  // Build tree nodes
  const result: TreeNode[] = [];

  // Sort directories by path for consistent ordering
  const sortedDirs = Array.from(dirToFiles.keys()).sort();

  sortedDirs.forEach(dir => {
    const filesInDir = dirToFiles.get(dir)!;

    if (dir === '') {
      // Root files
      filesInDir.forEach(file => {
        result.push({
          type: 'file',
          name: file.path,
          file,
        });
      });
    } else {
      // Directory with files
      const dirNode: TreeNode = {
        type: 'dir',
        name: abbreviatePath(dir),
        fullPath: dir,
        children: filesInDir.map(file => ({
          type: 'file',
          name: file.path.split('/').pop() || file.path,
          file,
        })),
      };
      result.push(dirNode);
    }
  });

  return result;
}

export const ChangesPanel = memo(function ChangesPanel({
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

  // Memoize tree building to avoid rebuilding on every render
  const stagedTree = useMemo(() =>
    gitStatus?.staged ? buildTree(gitStatus.staged) : [],
    [gitStatus?.staged]
  );
  const unstagedTree = useMemo(() =>
    gitStatus?.unstaged ? buildTree(gitStatus.unstaged) : [],
    [gitStatus?.unstaged]
  );
  const untrackedTree = useMemo(() =>
    gitStatus?.untracked ? buildTree(gitStatus.untracked) : [],
    [gitStatus?.untracked]
  );

  const getStatusIcon = useCallback((status: string) => {
    switch (status) {
      case 'modified': return 'M';
      case 'added': return 'A';
      case 'deleted': return 'D';
      case 'renamed': return 'R';
      case 'untracked': return '?';
      default: return 'M';
    }
  }, []);

  const renderTree = (nodes: TreeNode[], depth: number, staged: boolean) => {
    return nodes.map((node, index) => {
      if (node.type === 'dir') {
        return (
          <div key={`dir-${node.fullPath}-${index}`}>
            <div
              className="tree-dir"
              style={{ paddingLeft: `${depth * 12 + 12}px` }}
              title={node.fullPath}
            >
              <span className="dir-name">{node.name}</span>
            </div>
            {node.children && renderTree(node.children, depth + 1, staged)}
          </div>
        );
      } else {
        const file = node.file!;
        return (
          <div
            key={`file-${file.path}-${index}`}
            className={`tree-file change-file ${selectedFile === file.path ? 'selected' : ''}`}
            style={{ paddingLeft: `${depth * 12 + 12}px` }}
            onClick={() => onFileSelect(file.path, staged)}
            title={file.path}
          >
            <span className={`file-status ${file.status}`}>
              {getStatusIcon(file.status)}
            </span>
            <span className="file-name">{node.name}</span>
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
        );
      }
    });
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

      {totalStats.files > 0 && (
        <div className="changes-summary">
          <span>{totalStats.files} files</span>
          {totalStats.additions > 0 && <span className="stat-add">+{totalStats.additions}</span>}
          {totalStats.deletions > 0 && <span className="stat-del">-{totalStats.deletions}</span>}
        </div>
      )}

      <div className="changes-body">
        {gitStatus?.error ? (
          <div className="changes-error">{gitStatus.error}</div>
        ) : totalStats.files === 0 ? (
          <div className="changes-empty">No changes</div>
        ) : (
          <>
            {stagedTree.length > 0 && (
              <div className="changes-section">
                <div className="section-header">Staged ({gitStatus?.staged?.length || 0})</div>
                {renderTree(stagedTree, 0, true)}
              </div>
            )}

            {unstagedTree.length > 0 && (
              <div className="changes-section">
                <div className="section-header">Changes ({gitStatus?.unstaged?.length || 0})</div>
                {renderTree(unstagedTree, 0, false)}
              </div>
            )}

            {untrackedTree.length > 0 && (
              <div className="changes-section">
                <div className="section-header">Untracked ({gitStatus?.untracked?.length || 0})</div>
                {renderTree(untrackedTree, 0, false)}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
});
