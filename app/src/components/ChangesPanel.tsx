// app/src/components/ChangesPanel.tsx
import { useMemo, memo, useCallback } from 'react';
import type { BranchDiffFile } from '../hooks/useDaemonSocket';
import './ChangesPanel.css';

interface ChangesPanelProps {
  branchDiffFiles: BranchDiffFile[];
  branchDiffBaseRef?: string;
  branchDiffError?: string | null;
  attentionCount: number;
  selectedFile: string | null;
  onFileSelect: (path: string, staged: boolean) => void;
  onAttentionClick: () => void;
  onReviewClick?: () => void;
  onOpenEditor?: () => void;
}

type BranchChange = {
  path: string;
  status: string;
  additions?: number;
  deletions?: number;
  hasUncommitted?: boolean;
};

type TreeNode = {
  type: 'file' | 'dir';
  name: string;  // abbreviated path for dirs, filename for files
  fullPath?: string;  // for dirs
  file?: BranchChange;  // for files
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
function buildTree(files: BranchChange[]): TreeNode[] {
  // Group files by directory
  const dirToFiles: Map<string, BranchChange[]> = new Map();

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
  branchDiffFiles,
  branchDiffBaseRef,
  branchDiffError,
  attentionCount,
  selectedFile,
  onFileSelect,
  onAttentionClick,
  onReviewClick,
  onOpenEditor,
}: ChangesPanelProps) {
  const totalStats = useMemo(() => {
    const additions = branchDiffFiles.reduce((sum, f) => sum + (f.additions || 0), 0);
    const deletions = branchDiffFiles.reduce((sum, f) => sum + (f.deletions || 0), 0);

    return { files: branchDiffFiles.length, additions, deletions };
  }, [branchDiffFiles]);
  const isLoading = !branchDiffError && totalStats.files === 0;

  // Memoize tree building to avoid rebuilding on every render
  const branchTree = useMemo(() => buildTree(branchDiffFiles), [branchDiffFiles]);

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
          {onOpenEditor && (
            <button className="editor-btn" onClick={onOpenEditor} title="Open project in $EDITOR">
              Open
            </button>
          )}
          {totalStats.files > 0 && onReviewClick && (
            <button className="review-btn" onClick={onReviewClick} title="Review changes (r)">
              Review
            </button>
          )}
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
          {branchDiffBaseRef && <span>vs {branchDiffBaseRef}</span>}
          {totalStats.additions > 0 && <span className="stat-add">+{totalStats.additions}</span>}
          {totalStats.deletions > 0 && <span className="stat-del">-{totalStats.deletions}</span>}
        </div>
      )}

      <div className="changes-body">
        {branchDiffError ? (
          <div className="changes-error">{branchDiffError}</div>
        ) : isLoading ? (
          <div className="changes-loading">
            <div className="loading-header" />
            {Array.from({ length: 5 }).map((_, index) => (
              <div key={index} className="loading-row" />
            ))}
          </div>
        ) : totalStats.files === 0 ? (
          <div className="changes-empty">No changes</div>
        ) : (
          <div className="changes-section">
            <div className="section-header">Files ({branchDiffFiles.length})</div>
            {renderTree(branchTree, 0, false)}
          </div>
        )}
      </div>
    </div>
  );
});
