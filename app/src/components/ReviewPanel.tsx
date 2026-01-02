// app/src/components/ReviewPanel.tsx
import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { EditorView } from '@codemirror/view';
import { EditorState } from '@codemirror/state';
import { unifiedMergeView } from '@codemirror/merge';
import { oneDark } from '@codemirror/theme-one-dark';
import { javascript } from '@codemirror/lang-javascript';
import { markdown } from '@codemirror/lang-markdown';
import { python } from '@codemirror/lang-python';
import { lineNumbers, highlightActiveLineGutter, highlightSpecialChars, drawSelection, rectangularSelection } from '@codemirror/view';
import { foldGutter, indentOnInput, syntaxHighlighting, defaultHighlightStyle, bracketMatching } from '@codemirror/language';
import { history } from '@codemirror/commands';
import { highlightSelectionMatches } from '@codemirror/search';
import type { GitStatusUpdate, FileDiffResult, ReviewState } from '../hooks/useDaemonSocket';
import './ReviewPanel.css';

// Auto-skip patterns for lockfiles and generated files
const AUTO_SKIP_PATTERNS = [
  'pnpm-lock.yaml',
  'package-lock.json',
  'yarn.lock',
  'go.sum',
  'Cargo.lock',
  'composer.lock',
  'Gemfile.lock',
  'poetry.lock',
];

interface ReviewFile {
  path: string;
  status: string;
  staged: boolean;
  additions?: number;
  deletions?: number;
  isAutoSkip: boolean;
}

interface ReviewPanelProps {
  isOpen: boolean;
  gitStatus: GitStatusUpdate | null;
  repoPath: string;
  branch: string;
  onClose: () => void;
  fetchDiff: (path: string, staged: boolean) => Promise<FileDiffResult>;
  getReviewState: (repoPath: string, branch: string) => Promise<{ success: boolean; state?: ReviewState; error?: string }>;
  markFileViewed: (reviewId: string, filepath: string, viewed: boolean) => Promise<{ success: boolean; error?: string }>;
  onSendToClaude?: (reference: string) => void;
}

export function ReviewPanel({
  isOpen,
  gitStatus,
  repoPath,
  branch,
  onClose,
  fetchDiff,
  getReviewState,
  markFileViewed,
  onSendToClaude: _onSendToClaude, // Will be used in Phase 2 for comments
}: ReviewPanelProps) {
  const [selectedFile, setSelectedFile] = useState<ReviewFile | null>(null);
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [reviewId, setReviewId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [diffContent, setDiffContent] = useState<{ original: string; modified: string } | null>(null);
  const [expandedContext, setExpandedContext] = useState(0); // 0 = hunks only, -1 = full
  const [fontSize, setFontSize] = useState(13); // Default font size
  const editorContainerRef = useRef<HTMLDivElement>(null);
  const editorViewRef = useRef<EditorView | null>(null);

  // Build file list from git status
  const { needsReviewFiles, autoSkipFiles } = useMemo(() => {
    if (!gitStatus) return { needsReviewFiles: [], autoSkipFiles: [] };

    const allFiles: ReviewFile[] = [
      ...(gitStatus.staged || []).map(f => ({
        path: f.path,
        status: f.status,
        staged: true,
        additions: f.additions,
        deletions: f.deletions,
        isAutoSkip: AUTO_SKIP_PATTERNS.some(p => f.path.endsWith(p)),
      })),
      ...(gitStatus.unstaged || []).map(f => ({
        path: f.path,
        status: f.status,
        staged: false,
        additions: f.additions,
        deletions: f.deletions,
        isAutoSkip: AUTO_SKIP_PATTERNS.some(p => f.path.endsWith(p)),
      })),
      ...(gitStatus.untracked || []).map(f => ({
        path: f.path,
        status: 'untracked',
        staged: false,
        additions: f.additions,
        deletions: f.deletions,
        isAutoSkip: AUTO_SKIP_PATTERNS.some(p => f.path.endsWith(p)),
      })),
    ];

    return {
      needsReviewFiles: allFiles.filter(f => !f.isAutoSkip),
      autoSkipFiles: allFiles.filter(f => f.isAutoSkip),
    };
  }, [gitStatus]);

  const allFiles = useMemo(() => [...needsReviewFiles, ...autoSkipFiles], [needsReviewFiles, autoSkipFiles]);

  // Load persisted review state when opening
  useEffect(() => {
    if (isOpen && repoPath && branch) {
      getReviewState(repoPath, branch)
        .then((result) => {
          if (result.success && result.state) {
            setReviewId(result.state.review_id);
            setViewedFiles(new Set(result.state.viewed_files || []));
          }
        })
        .catch((err) => {
          console.error('Failed to load review state:', err);
        });
    }
  }, [isOpen, repoPath, branch, getReviewState]);

  // Auto-select first file when opening
  useEffect(() => {
    if (isOpen && needsReviewFiles.length > 0 && !selectedFile) {
      setSelectedFile(needsReviewFiles[0]);
    }
  }, [isOpen, needsReviewFiles, selectedFile]);

  // Reset state when closing
  useEffect(() => {
    if (!isOpen) {
      setSelectedFile(null);
      setDiffContent(null);
      setError(null);
      setExpandedContext(0);
    }
  }, [isOpen]);

  // Fetch diff when selected file changes
  useEffect(() => {
    if (!selectedFile) {
      setDiffContent(null);
      return;
    }

    setLoading(true);
    setError(null);

    fetchDiff(selectedFile.path, selectedFile.staged)
      .then((result) => {
        setDiffContent({ original: result.original, modified: result.modified });
        setLoading(false);
        // Mark file as viewed (local state)
        setViewedFiles(prev => new Set(prev).add(selectedFile.path));
        // Persist to backend if we have a review ID
        if (reviewId) {
          markFileViewed(reviewId, selectedFile.path, true).catch((err) => {
            console.error('Failed to persist viewed state:', err);
          });
        }
      })
      .catch((err) => {
        setError(err.message || 'Failed to load diff');
        setLoading(false);
      });
  }, [selectedFile, fetchDiff, reviewId, markFileViewed]);

  // Create/update CodeMirror editor
  useEffect(() => {
    if (!editorContainerRef.current || !diffContent) return;

    // Clean up existing editor
    if (editorViewRef.current) {
      editorViewRef.current.destroy();
      editorViewRef.current = null;
    }

    // Detect language from file extension
    const ext = selectedFile?.path.split('.').pop()?.toLowerCase();
    let langExtension;
    switch (ext) {
      case 'js':
      case 'jsx':
      case 'ts':
      case 'tsx':
        langExtension = javascript({ typescript: ext === 'ts' || ext === 'tsx', jsx: ext === 'jsx' || ext === 'tsx' });
        break;
      case 'md':
        langExtension = markdown();
        break;
      case 'py':
        langExtension = python();
        break;
      default:
        langExtension = [];
    }

    // Custom diff styling that works with oneDark
    const diffTheme = EditorView.theme({
      '&': {
        height: '100%',
      },
      '.cm-scroller': {
        fontFamily: "'JetBrains Mono', 'Fira Code', Consolas, monospace",
        fontSize: `${fontSize}px`,
        lineHeight: '1.6',
      },
      '.cm-gutters': {
        borderRight: '1px solid #3e4451',
      },
      '.cm-lineNumbers .cm-gutterElement': {
        padding: '0 16px 0 8px',
        minWidth: '48px',
      },
      // Deleted chunk - red background for entire line
      '.cm-deletedChunk': {
        backgroundColor: '#3c1f1e !important',
      },
      // Deleted text highlight within the line
      '.cm-deletedChunk .cm-deletedText': {
        backgroundColor: '#6e3630 !important',
        textDecoration: 'none !important',
      },
      // Inserted chunk - green background for entire line
      '.cm-insertedChunk': {
        backgroundColor: '#1e3a1e !important',
      },
      // Inserted text highlight within the line
      '.cm-insertedChunk .cm-insertedText': {
        backgroundColor: '#2e5c2e !important',
      },
      // Changed line indicator
      '.cm-changedLine': {
        backgroundColor: 'rgba(187, 128, 9, 0.08)',
      },
      // Collapsed unchanged regions (hunks mode)
      '.cm-collapsedLines': {
        backgroundColor: '#2c313a',
        color: '#636d83',
        padding: '6px 16px',
        margin: '2px 0',
        borderRadius: '0',
        fontSize: '12px',
        cursor: 'pointer',
        borderTop: '1px solid #3e4451',
        borderBottom: '1px solid #3e4451',
      },
      '.cm-collapsedLines:hover': {
        backgroundColor: '#3e4451',
        color: '#abb2bf',
      },
      // Remove active line highlighting in diff view
      '.cm-activeLine': {
        backgroundColor: 'transparent !important',
      },
      '.cm-activeLineGutter': {
        backgroundColor: 'transparent !important',
      },
    }, { dark: true });

    // Minimal setup for read-only diff viewing
    const minimalSetup = [
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightSpecialChars(),
      history(),
      foldGutter(),
      drawSelection(),
      EditorState.allowMultipleSelections.of(true),
      indentOnInput(),
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      bracketMatching(),
      rectangularSelection(),
      highlightSelectionMatches(),
    ];

    const extensions = [
      minimalSetup,
      langExtension,
      oneDark,
      diffTheme,
      EditorView.editable.of(false),
      unifiedMergeView({
        original: diffContent.original,
        highlightChanges: true,
        syntaxHighlightDeletions: true,
        mergeControls: false,
        // Hunks mode: collapse unchanged regions, Full mode: show everything
        ...(expandedContext === 0 ? {
          collapseUnchanged: { margin: 3, minSize: 6 },
        } : {}),
      }),
    ];

    const state = EditorState.create({
      doc: diffContent.modified,
      extensions,
    });

    const view = new EditorView({
      state,
      parent: editorContainerRef.current,
    });

    editorViewRef.current = view;

    return () => {
      view.destroy();
      editorViewRef.current = null;
    };
  }, [diffContent, selectedFile?.path, expandedContext, fontSize]);

  // Keyboard navigation
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      // Handle Cmd/Ctrl + / - for font size
      if (e.metaKey || e.ctrlKey) {
        if (e.key === '=' || e.key === '+') {
          e.preventDefault();
          setFontSize(prev => Math.min(prev + 1, 24));
          return;
        }
        if (e.key === '-') {
          e.preventDefault();
          setFontSize(prev => Math.max(prev - 1, 9));
          return;
        }
        if (e.key === '0') {
          e.preventDefault();
          setFontSize(13); // Reset to default
          return;
        }
        return; // Don't process other keys with modifiers
      }

      if (e.altKey) return;

      switch (e.key) {
        case 'Escape':
          e.preventDefault();
          onClose();
          break;
        case 'j':
        case 'ArrowDown':
          e.preventDefault();
          navigateFiles('next');
          break;
        case 'k':
        case 'ArrowUp':
          e.preventDefault();
          navigateFiles('prev');
          break;
        case 'n':
          e.preventDefault();
          navigateFiles('next');
          break;
        case 'p':
          e.preventDefault();
          navigateFiles('prev');
          break;
        case ']':
          e.preventDefault();
          navigateToNextUnreviewed();
          break;
        case 'e':
          e.preventDefault();
          if (e.shiftKey) {
            setExpandedContext(-1); // Full file
          } else {
            setExpandedContext(prev => prev === -1 ? 0 : prev + 10);
          }
          break;
        case 'E':
          e.preventDefault();
          setExpandedContext(-1); // Full file
          break;
      }
    };

    window.addEventListener('keydown', handleKeyDown, true);
    return () => window.removeEventListener('keydown', handleKeyDown, true);
  }, [isOpen, onClose, allFiles, selectedFile]);

  const navigateFiles = useCallback((direction: 'prev' | 'next') => {
    if (!selectedFile || allFiles.length === 0) return;
    const currentIndex = allFiles.findIndex(f => f.path === selectedFile.path);
    const newIndex = direction === 'next'
      ? Math.min(currentIndex + 1, allFiles.length - 1)
      : Math.max(currentIndex - 1, 0);
    setSelectedFile(allFiles[newIndex]);
  }, [allFiles, selectedFile]);

  const navigateToNextUnreviewed = useCallback(() => {
    const unreviewed = needsReviewFiles.find(f => !viewedFiles.has(f.path));
    if (unreviewed) {
      setSelectedFile(unreviewed);
    }
  }, [needsReviewFiles, viewedFiles]);

  const getFileIcon = useCallback((file: ReviewFile) => {
    if (file.isAutoSkip) return '⊘';
    if (viewedFiles.has(file.path)) return '✓';
    return '';
  }, [viewedFiles]);

  const getStatusLabel = useCallback((status: string) => {
    switch (status) {
      case 'modified': return 'M';
      case 'added': return 'A';
      case 'deleted': return 'D';
      case 'renamed': return 'R';
      case 'untracked': return '?';
      default: return 'M';
    }
  }, []);

  // Abbreviate path for display
  const abbreviatePath = useCallback((path: string) => {
    const parts = path.split('/');
    if (parts.length <= 3) return path;
    const filename = parts.pop()!;
    const dir = parts.slice(-2).join('/');
    return `.../${dir}/${filename}`;
  }, []);

  if (!isOpen) return null;

  const currentFileIndex = selectedFile ? allFiles.findIndex(f => f.path === selectedFile.path) : -1;

  return (
    <>
      <div className="review-panel-backdrop" onClick={onClose} />
      <div className="review-panel">
        <div className="review-header">
          <span className="review-title">
            Review: {gitStatus?.directory?.split('/').pop() || 'changes'}
          </span>
          <span className="review-file-count">
            {currentFileIndex + 1}/{allFiles.length} files
          </span>
          <button className="review-close" onClick={onClose}>×</button>
        </div>

        <div className="review-body">
          <div className="review-file-list">
            {needsReviewFiles.length > 0 && (
              <div className="file-group">
                <div className="file-group-header">NEEDS REVIEW</div>
                {needsReviewFiles.map(file => (
                  <div
                    key={file.path}
                    className={`file-item ${selectedFile?.path === file.path ? 'selected' : ''} ${viewedFiles.has(file.path) ? 'viewed' : ''}`}
                    onClick={() => setSelectedFile(file)}
                  >
                    <span className="file-icon">{getFileIcon(file)}</span>
                    <span className={`file-status ${file.status}`}>{getStatusLabel(file.status)}</span>
                    <span className="file-name" title={file.path}>{abbreviatePath(file.path)}</span>
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

            {autoSkipFiles.length > 0 && (
              <div className="file-group auto-skip">
                <div className="file-group-header">AUTO-SKIP</div>
                {autoSkipFiles.map(file => (
                  <div
                    key={file.path}
                    className={`file-item auto-skip ${selectedFile?.path === file.path ? 'selected' : ''}`}
                    onClick={() => setSelectedFile(file)}
                  >
                    <span className="file-icon">{getFileIcon(file)}</span>
                    <span className={`file-status ${file.status}`}>{getStatusLabel(file.status)}</span>
                    <span className="file-name" title={file.path}>{abbreviatePath(file.path)}</span>
                  </div>
                ))}
              </div>
            )}

            {allFiles.length === 0 && (
              <div className="file-list-empty">No changes to review</div>
            )}
          </div>

          <div className="review-diff-area">
            <div className="diff-toolbar">
              <span className="diff-filename">{selectedFile?.path || 'Select a file'}</span>
              <div className="diff-actions">
                <button
                  className={`expand-btn ${expandedContext === 0 ? 'active' : ''}`}
                  onClick={() => setExpandedContext(0)}
                  title="Hunks only"
                >
                  Hunks
                </button>
                <button
                  className={`expand-btn ${expandedContext === -1 ? 'active' : ''}`}
                  onClick={() => setExpandedContext(-1)}
                  title="Full file (E)"
                >
                  Full
                </button>
              </div>
            </div>
            <div className="diff-content">
              {loading ? (
                <div className="diff-loading">Loading diff...</div>
              ) : error ? (
                <div className="diff-error">{error}</div>
              ) : !selectedFile ? (
                <div className="diff-placeholder">Select a file to view diff</div>
              ) : (
                <div ref={editorContainerRef} className="codemirror-container" />
              )}
            </div>
          </div>
        </div>

        <div className="review-footer">
          <span className="shortcut"><kbd>j</kbd>/<kbd>k</kbd> navigate</span>
          <span className="shortcut"><kbd>]</kbd> next unreviewed</span>
          <span className="shortcut"><kbd>e</kbd>/<kbd>E</kbd> expand</span>
          <span className="shortcut"><kbd>⌘+</kbd>/<kbd>⌘-</kbd> zoom</span>
          <span className="shortcut"><kbd>Esc</kbd> close</span>
        </div>
      </div>
    </>
  );
}
