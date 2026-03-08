// app/src/components/DiffDetailPanel.tsx
import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import type { GitStatusUpdate, FileDiffResult, ReviewState, BranchDiffFile, BranchDiffFilesResult } from '../hooks/useDaemonSocket';
import type { ResolvedTheme } from '../hooks/useTheme';
import type { ReviewComment } from '../types/generated';
import UnifiedDiffEditor, {
  buildUnifiedDocument,
  resolveAnchor,
  hashContent,
  type DiffLine,
  type CommentAnchor,
  type InlineComment as EditorComment,
} from './UnifiedDiffEditor';
import './DiffDetailPanel.css';

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

// ============================================================================
// Comment Conversion Utilities (for UnifiedDiffEditor integration)
// ============================================================================

/**
 * Convert a ReviewComment (API format) to EditorComment format.
 * Uses resolveAnchor to find the docLine and detect outdated/orphaned state.
 *
 * Convention: negative line_end encodes 'original' side (deleted lines).
 */
function toEditorComment(
  comment: ReviewComment,
  lines: DiffLine[]
): EditorComment | null {
  // Detect side from line_end convention: negative = original (deleted lines)
  const isOriginalSide = comment.line_end < 0;
  const anchor: CommentAnchor = {
    side: isOriginalSide ? 'original' : 'modified',
    line: comment.line_start,
    anchorContent: '',
  };

  // Try to resolve the anchor
  const result = resolveAnchor(anchor, lines);

  if ('isOrphaned' in result) {
    // Line no longer exists - still show comment but mark as orphaned
    // Place it at line 1 as fallback
    return {
      id: comment.id,
      docLine: 1,
      content: comment.content,
      resolved: comment.resolved,
      resolvedBy: comment.resolved_by as 'user' | 'agent' | undefined,
      wontFix: comment.wont_fix,
      wontFixBy: comment.wont_fix_by as 'user' | 'agent' | undefined,
      author: comment.author as 'user' | 'agent',
      anchor,
      isOrphaned: true,
    };
  }

  return {
    id: comment.id,
    docLine: result.docLine,
    content: comment.content,
    resolved: comment.resolved,
    resolvedBy: comment.resolved_by as 'user' | 'agent' | undefined,
    wontFix: comment.wont_fix,
    wontFixBy: comment.wont_fix_by as 'user' | 'agent' | undefined,
    author: comment.author as 'user' | 'agent',
    anchor,
    isOutdated: result.isOutdated,
  };
}

/**
 * Extract API-compatible fields from a CommentAnchor.
 * Used when saving a new comment to the backend.
 *
 * Convention: negative line_end encodes 'original' side (deleted lines).
 * - side='modified': line_end = line_start (positive)
 * - side='original': line_end = -line_start (negative)
 */
function fromEditorAnchor(anchor: CommentAnchor): {
  line_start: number;
  line_end: number;
} {
  return {
    line_start: anchor.line,
    line_end: anchor.side === 'original' ? -anchor.line : anchor.line,
  };
}

// Abbreviate path for display
function abbreviatePath(path: string): string {
  const parts = path.split('/');
  if (parts.length <= 3) return path;
  const filename = parts.pop()!;
  const dir = parts.slice(-2).join('/');
  return `.../${dir}/${filename}`;
}

interface ReviewFile {
  path: string;
  status: string;
  staged: boolean;  // Deprecated, kept for compatibility
  additions?: number;
  deletions?: number;
  isAutoSkip: boolean;
  hasUncommitted?: boolean;  // True if file has uncommitted changes on top of committed
  oldPath?: string;  // For renames
}

type TreeNode = {
  type: 'file' | 'dir';
  name: string;
  fullPath?: string;
  file?: ReviewFile;
  children?: TreeNode[];
};

function buildTree(files: ReviewFile[]): TreeNode[] {
  const dirToFiles: Map<string, ReviewFile[]> = new Map();

  files.forEach(file => {
    const parts = file.path.split('/');
    if (parts.length === 1) {
      if (!dirToFiles.has('')) {
        dirToFiles.set('', []);
      }
      dirToFiles.get('')!.push(file);
    } else {
      const dir = parts.slice(0, -1).join('/');
      if (!dirToFiles.has(dir)) {
        dirToFiles.set(dir, []);
      }
      dirToFiles.get(dir)!.push(file);
    }
  });

  const result: TreeNode[] = [];
  const sortedDirs = Array.from(dirToFiles.keys()).sort();

  sortedDirs.forEach(dir => {
    const filesInDir = dirToFiles.get(dir)!;

    if (dir === '') {
      filesInDir.forEach(file => {
        result.push({
          type: 'file',
          name: file.path,
          file,
        });
      });
    } else {
      result.push({
        type: 'dir',
        name: abbreviatePath(dir),
        fullPath: dir,
        children: filesInDir.map(file => ({
          type: 'file',
          name: file.path.split('/').pop() || file.path,
          file,
        })),
      });
    }
  });

  return result;
}

// ReviewerEvent is now imported from useDaemonSocket

interface DiffDetailPanelProps {
  isOpen: boolean;
  gitStatus: GitStatusUpdate | null;  // Still used for real-time updates
  repoPath: string;
  branch: string;
  onClose: () => void;
  // Diff fetching - options: baseRef for PR-like diffs
  fetchDiff: (path: string, options?: { staged?: boolean; baseRef?: string }) => Promise<FileDiffResult>;
  // Branch diff - fetches all files changed vs origin/main
  sendGetBranchDiffFiles: (directory: string, baseRef?: string) => Promise<BranchDiffFilesResult>;
  // Fetch remotes to ensure we have latest origin state
  sendFetchRemotes: (repo: string) => Promise<{ success: boolean; error?: string }>;
  getReviewState: (repoPath: string, branch: string) => Promise<{ success: boolean; state?: ReviewState; error?: string }>;
  markFileViewed: (reviewId: string, filepath: string, viewed: boolean) => Promise<{ success: boolean; error?: string }>;
  onOpenEditor?: (filePath?: string) => void;
  // Comment operations
  addComment?: (reviewId: string, filepath: string, lineStart: number, lineEnd: number, content: string) => Promise<{ success: boolean; comment?: ReviewComment }>;
  updateComment?: (commentId: string, content: string) => Promise<{ success: boolean }>;
  resolveComment?: (commentId: string, resolved: boolean) => Promise<{ success: boolean }>;
  wontFixComment?: (commentId: string, wontFix: boolean) => Promise<{ success: boolean }>;
  deleteComment?: (commentId: string) => Promise<{ success: boolean }>;
  getComments?: (reviewId: string, filepath?: string) => Promise<{ success: boolean; comments?: ReviewComment[] }>;
  resolvedTheme?: ResolvedTheme;
  // Initial file to select when panel opens
  initialSelectedFile?: string;
}

export function DiffDetailPanel({
  isOpen,
  gitStatus,
  repoPath,
  branch,
  onClose,
  fetchDiff,
  sendGetBranchDiffFiles,
  sendFetchRemotes,
  getReviewState,
  markFileViewed,
  onOpenEditor,
  addComment,
  updateComment,
  resolveComment,
  wontFixComment,
  deleteComment,
  getComments,
  resolvedTheme = 'dark',
  initialSelectedFile,
}: DiffDetailPanelProps) {
  const [contentVisible, setContentVisible] = useState(isOpen);
  // Track selected file by path for stability across gitStatus updates
  const [selectedFilePath, setSelectedFilePath] = useState<string | null>(null);
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [reviewId, setReviewId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [diffContent, setDiffContent] = useState<{ original: string; modified: string } | null>(null);
  const [expandedContext, setExpandedContext] = useState(0); // 0 = hunks mode (uses 3 lines context), -1 = full file
  const [fontSize, setFontSize] = useState(13); // Default font size
  const [scrollToLine, setScrollToLine] = useState<number | undefined>(undefined);

  // Branch diff state - PR-like comparison against origin/main
  const [branchDiffFiles, setBranchDiffFiles] = useState<BranchDiffFile[]>([]);
  const [baseRef, setBaseRef] = useState<string>('');
  const [isLoadingBranchDiff, setIsLoadingBranchDiff] = useState(false);
  const [isSyncingRemotes, setIsSyncingRemotes] = useState(false);
  const [remotesSyncWarning, setRemotesSyncWarning] = useState<string | null>(null);
  const branchDiffRequestIdRef = useRef(0);
  const branchDiffCacheRef = useRef<Map<string, { files: BranchDiffFile[]; baseRef: string }>>(new Map());

  // Comment state
  const [allReviewComments, setAllReviewComments] = useState<ReviewComment[]>([]);
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);
  const [commentError, setCommentError] = useState<string | null>(null);

  // Auto-clear comment errors after 3 seconds
  useEffect(() => {
    if (commentError) {
      const timer = setTimeout(() => setCommentError(null), 3000);
      return () => clearTimeout(timer);
    }
  }, [commentError]);

  useEffect(() => {
    if (isOpen) {
      setContentVisible(true);
      return;
    }
    const timer = window.setTimeout(() => {
      setContentVisible(false);
    }, 260);
    return () => window.clearTimeout(timer);
  }, [isOpen]);

  // Derive comments for current file
  const comments = useMemo(() => {
    if (!selectedFilePath) return [];
    return allReviewComments.filter(c => c.filepath === selectedFilePath);
  }, [allReviewComments, selectedFilePath]);

  // Compute comment counts per file
  const fileCommentCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const comment of allReviewComments) {
      counts[comment.filepath] = (counts[comment.filepath] || 0) + 1;
    }
    return counts;
  }, [allReviewComments]);

  // Track diff hashes for "changed since viewed" detection
  const viewedDiffHashesRef = useRef<Map<string, string>>(new Map());
  const [changedSinceViewed, setChangedSinceViewed] = useState<Set<string>>(new Set());
  const previousSelectedPathRef = useRef<string | null>(null);

  // Fetch branch diff files when panel opens (PR-like comparison vs origin/main)
  // Load local refs immediately, then refresh in background after fetching remotes.
  useEffect(() => {
    if (!isOpen || !repoPath) return;

    let cancelled = false;
    const requestId = ++branchDiffRequestIdRef.current;
    const isCurrentRequest = () => !cancelled && requestId === branchDiffRequestIdRef.current;
    const cacheKey = `${repoPath}::${branch}`;
    let localRequestStarted = false;
    let localApplied = false;
    let remoteApplied = false;
    let localTimer: ReturnType<typeof setTimeout> | null = null;

    const applyBranchDiff = (result: BranchDiffFilesResult) => {
      setBranchDiffFiles(result.files);
      setBaseRef(result.base_ref);
      branchDiffCacheRef.current.set(cacheKey, {
        files: result.files,
        baseRef: result.base_ref,
      });
    };

    const runLocalDiff = () => {
      if (!isCurrentRequest() || localRequestStarted || remoteApplied) return;
      localRequestStarted = true;

      sendGetBranchDiffFiles(repoPath)
        .then((result) => {
          // Do not let stale local results overwrite fresher remote results.
          if (!isCurrentRequest() || remoteApplied) return;
          if (result.success) {
            applyBranchDiff(result);
            localApplied = true;
          }
        })
        .catch((err) => {
          if (!isCurrentRequest() || remoteApplied) return;
          console.error('[DiffDetailPanel] Failed to fetch local branch diff:', err);
        })
        .finally(() => {
          if (isCurrentRequest() && !remoteApplied) {
            setIsLoadingBranchDiff(false);
          }
        });
    };

    setRemotesSyncWarning(null);
    const cached = branchDiffCacheRef.current.get(cacheKey);
    if (cached) {
      setBranchDiffFiles(cached.files);
      setBaseRef(cached.baseRef);
      localApplied = true;
      setIsLoadingBranchDiff(false);
    } else {
      setIsLoadingBranchDiff(true);
      // Small delay avoids guaranteed duplicate heavy diff calls when remote sync is fast.
      localTimer = setTimeout(() => {
        runLocalDiff();
      }, 150);
    }

    // 2) Freshness path: refresh remotes in background and update in place
    setIsSyncingRemotes(true);
    sendFetchRemotes(repoPath)
      .then((fetchResult) => {
        if (fetchResult.success === false) {
          throw new Error(fetchResult.error || 'Failed to fetch remotes');
        }
        if (localTimer) {
          clearTimeout(localTimer);
          localTimer = null;
        }
        if (!localApplied) {
          setIsLoadingBranchDiff(true);
        }
        return sendGetBranchDiffFiles(repoPath);
      })
      .then((result) => {
        if (!isCurrentRequest()) return;
        if (result.success) {
          remoteApplied = true;
          applyBranchDiff(result);
          setRemotesSyncWarning(null);
          setIsLoadingBranchDiff(false);
          return;
        }
        setRemotesSyncWarning('Could not refresh remotes; showing local refs');
        if (!localApplied && !localRequestStarted) {
          runLocalDiff();
          return;
        }
        setIsLoadingBranchDiff(false);
      })
      .catch((err) => {
        if (!isCurrentRequest()) return;
        console.error('[DiffDetailPanel] Failed to refresh remotes:', err);
        setRemotesSyncWarning('Could not refresh remotes; showing local refs');
        if (!localApplied && !localRequestStarted) {
          runLocalDiff();
          return;
        }
        if (!remoteApplied) {
          setIsLoadingBranchDiff(false);
        }
      })
      .finally(() => {
        if (isCurrentRequest()) {
          setIsSyncingRemotes(false);
        }
      });

    return () => {
      cancelled = true;
      if (localTimer) {
        clearTimeout(localTimer);
      }
    };
  }, [isOpen, repoPath, branch, sendFetchRemotes, sendGetBranchDiffFiles]);

  // Build file list from branch diff (PR-like: all changes vs origin/main)
  const { needsReviewFiles, autoSkipFiles } = useMemo(() => {
    if (branchDiffFiles.length === 0) return { needsReviewFiles: [], autoSkipFiles: [] };

    const allFiles: ReviewFile[] = branchDiffFiles.map(f => ({
      path: f.path,
      status: f.status,
      staged: false,  // Deprecated field, kept for compatibility
      additions: f.additions,
      deletions: f.deletions,
      hasUncommitted: f.has_uncommitted,
      oldPath: f.old_path,
      isAutoSkip: AUTO_SKIP_PATTERNS.some(p => f.path.endsWith(p)),
    }));

    return {
      needsReviewFiles: allFiles.filter(f => !f.isAutoSkip),
      autoSkipFiles: allFiles.filter(f => f.isAutoSkip),
    };
  }, [branchDiffFiles]);

  const allFiles = useMemo(() => [...needsReviewFiles, ...autoSkipFiles], [needsReviewFiles, autoSkipFiles]);
  const needsReviewTree = useMemo(() => buildTree(needsReviewFiles), [needsReviewFiles]);
  const autoSkipTree = useMemo(() => buildTree(autoSkipFiles), [autoSkipFiles]);

  // Derive selectedFile from path (stable across gitStatus updates)
  const selectedFile = useMemo(() => {
    if (!selectedFilePath) return null;
    return allFiles.find(f => f.path === selectedFilePath) || null;
  }, [selectedFilePath, allFiles]);

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

  // Auto-select file when opening - use initialSelectedFile if provided, else first file
  useEffect(() => {
    if (isOpen && !selectedFilePath) {
      if (initialSelectedFile) {
        setSelectedFilePath(initialSelectedFile);
      } else if (needsReviewFiles.length > 0) {
        setSelectedFilePath(needsReviewFiles[0].path);
      }
    }
  }, [isOpen, needsReviewFiles, selectedFilePath, initialSelectedFile]);

  // Keep current selection on branch diff refresh when possible.
  // If the file disappears, select the first available review file.
  useEffect(() => {
    if (!isOpen || !selectedFilePath) return;
    // Avoid selection ping-pong while branch diff is still empty/loading.
    if (allFiles.length === 0) return;
    if (allFiles.some((f) => f.path === selectedFilePath)) return;

    if (needsReviewFiles.length > 0) {
      setSelectedFilePath(needsReviewFiles[0].path);
      return;
    }
    if (allFiles.length > 0) {
      setSelectedFilePath(allFiles[0].path);
      return;
    }
    setSelectedFilePath(null);
  }, [isOpen, selectedFilePath, needsReviewFiles, allFiles]);

  // Load all comments for the review
  useEffect(() => {
    if (!reviewId || !getComments) {
      setAllReviewComments([]);
      return;
    }

    // Load all comments for the review (no filepath filter)
    getComments(reviewId)
      .then((result) => {
        if (result.success && result.comments) {
          setAllReviewComments(result.comments);
        }
      })
      .catch(console.error);
  }, [reviewId, getComments]);

  // Clear "changed" status when navigating away from a file
  useEffect(() => {
    const prevPath = previousSelectedPathRef.current;
    if (prevPath && prevPath !== selectedFilePath) {
      // User navigated away from previous file - clear its "changed" status
      setChangedSinceViewed(prev => {
        if (!prev.has(prevPath)) return prev;
        const next = new Set(prev);
        next.delete(prevPath);
        return next;
      });
    }
    previousSelectedPathRef.current = selectedFilePath;
  }, [selectedFilePath]);

  // Reset state when closing
  useEffect(() => {
    if (!contentVisible) {
      setSelectedFilePath(null);
      setDiffContent(null);
      setError(null);
      setExpandedContext(0);
      setScrollToLine(undefined);
      setIsLoadingBranchDiff(false);
      setIsSyncingRemotes(false);
      setRemotesSyncWarning(null);
      // Clear change detection state
      viewedDiffHashesRef.current.clear();
      setChangedSinceViewed(new Set());
    }
  }, [contentVisible]);

  const previousRepoPathRef = useRef<string | null>(null);
  useEffect(() => {
    if (!isOpen) return;
    const prevRepo = previousRepoPathRef.current;
    if (prevRepo && prevRepo !== repoPath) {
      setSelectedFilePath(null);
      setDiffContent(null);
      setError(null);
      setLoading(false);
      setExpandedContext(0);
      setScrollToLine(undefined);
      setBranchDiffFiles([]);
      setBaseRef('');
      setRemotesSyncWarning(null);
      viewedDiffHashesRef.current.clear();
      setChangedSinceViewed(new Set());
      setViewedFiles(new Set());
    }
    previousRepoPathRef.current = repoPath;
  }, [isOpen, repoPath]);

  // Create a stable key that changes when we need to refetch the diff
  // This includes the file path and a representation of the git status for that file
  const diffFetchKey = useMemo(() => {
    if (!selectedFile) return null;
    // Include additions/deletions as a proxy for content changes
    return `${selectedFile.path}:${selectedFile.staged}:${selectedFile.additions ?? 0}:${selectedFile.deletions ?? 0}`;
  }, [selectedFile]);

  // Fetch diff when selected file changes OR when git status indicates the file changed
  useEffect(() => {
    if (!selectedFile || !diffFetchKey) {
      setDiffContent(null);
      return;
    }

    // Store current path to check if we're still viewing the same file when fetch completes
    const fetchPath = selectedFile.path;
    const isFirstView = !viewedFiles.has(fetchPath);

    // Only show loading on first view to avoid flickering during updates
    if (isFirstView) {
      setLoading(true);
    }
    setError(null);

    // Use baseRef for PR-like diff comparison (origin/main vs working directory)
    fetchDiff(fetchPath, { baseRef })
      .then((result) => {
        // Only update if we're still viewing the same file
        if (selectedFilePath !== fetchPath) return;

        const newContent = { original: result.original, modified: result.modified };
        const newHash = hashContent(result.original + result.modified);
        const prevHash = viewedDiffHashesRef.current.get(fetchPath);

        setDiffContent(newContent);
        setLoading(false);

        // Check if file changed since last viewed
        if (prevHash && prevHash !== newHash) {
          // File was viewed before but content changed - mark as changed
          setChangedSinceViewed(prev => new Set(prev).add(fetchPath));
        }

        // Update stored hash
        viewedDiffHashesRef.current.set(fetchPath, newHash);

        // Mark file as viewed (local state) - only create new Set if actually adding
        setViewedFiles(prev => {
          if (prev.has(fetchPath)) return prev; // same reference = no state change
          return new Set(prev).add(fetchPath);
        });

        // Persist to backend if we have a review ID (only on first view)
        if (isFirstView && reviewId) {
          markFileViewed(reviewId, fetchPath, true).catch((err) => {
            console.error('Failed to persist viewed state:', err);
          });
        }
      })
      .catch((err) => {
        if (selectedFilePath !== fetchPath) return;
        setError(err.message || 'Failed to load diff');
        setLoading(false);
      });
  // Note: viewedFiles intentionally excluded - we read it but don't want re-runs when it changes
  }, [diffFetchKey, selectedFile, selectedFilePath, fetchDiff, baseRef, reviewId, markFileViewed]);

  // Check for changes in viewed files when gitStatus updates (for files not currently selected)
  useEffect(() => {
    if (!gitStatus) return;

    // For each viewed file that's still in the changes, check if it changed
    viewedFiles.forEach(async (viewedPath) => {
      // Skip currently selected file (handled by the main effect)
      if (viewedPath === selectedFilePath) return;

      // Find the file in current git status
      const fileInChanges = allFiles.find(f => f.path === viewedPath);
      if (!fileInChanges) return; // File no longer in changes

      // Fetch and check hash using baseRef for PR-like diff
      try {
        const result = await fetchDiff(viewedPath, { baseRef });
        const newHash = hashContent(result.original + result.modified);
        const prevHash = viewedDiffHashesRef.current.get(viewedPath);

        if (prevHash && prevHash !== newHash) {
          setChangedSinceViewed(prev => new Set(prev).add(viewedPath));
          viewedDiffHashesRef.current.set(viewedPath, newHash);
        }
      } catch {
        // Ignore errors for background checks
      }
    });
  }, [gitStatus, viewedFiles, selectedFilePath, allFiles, fetchDiff, baseRef]);

  // Keyboard navigation
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      // Don't capture keystrokes when typing in inputs
      const target = e.target as HTMLElement;
      if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) {
        return;
      }

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
    if (!selectedFilePath || allFiles.length === 0) return;
    const currentIndex = allFiles.findIndex(f => f.path === selectedFilePath);
    const newIndex = direction === 'next'
      ? Math.min(currentIndex + 1, allFiles.length - 1)
      : Math.max(currentIndex - 1, 0);
    setSelectedFilePath(allFiles[newIndex].path);
  }, [allFiles, selectedFilePath]);

  const navigateToNextUnreviewed = useCallback(() => {
    // First look for files that changed since viewed
    const changedFile = needsReviewFiles.find(f => changedSinceViewed.has(f.path));
    if (changedFile) {
      setSelectedFilePath(changedFile.path);
      return;
    }
    // Then look for unviewed files
    const unreviewed = needsReviewFiles.find(f => !viewedFiles.has(f.path));
    if (unreviewed) {
      setSelectedFilePath(unreviewed.path);
    }
  }, [needsReviewFiles, viewedFiles, changedSinceViewed]);

  const getFileIcon = useCallback((file: ReviewFile) => {
    if (file.isAutoSkip) return '⊘';
    if (changedSinceViewed.has(file.path)) return '↻'; // Changed since viewed
    if (viewedFiles.has(file.path)) return '✓';
    return '';
  }, [viewedFiles, changedSinceViewed]);

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

  // ============================================================================
  // UnifiedDiffEditor Integration
  // ============================================================================

  // Build unified document and convert comments
  const editorComments = useMemo(() => {
    if (!diffContent) {
      return [] as EditorComment[];
    }
    const { lines } = buildUnifiedDocument(diffContent.original, diffContent.modified);
    return comments
      .map(c => toEditorComment(c, lines))
      .filter((c): c is EditorComment => c !== null);
  }, [diffContent, comments]);

  // Get language from file extension
  const editorLanguage = useMemo(() => {
    if (!selectedFilePath) return undefined;
    const ext = selectedFilePath.split('.').pop()?.toLowerCase();
    const langMap: Record<string, string> = {
      js: 'javascript',
      jsx: 'jsx',
      ts: 'typescript',
      tsx: 'tsx',
      py: 'python',
      md: 'markdown',
      json: 'json',
      css: 'css',
      html: 'html',
      go: 'go',
      rs: 'rust',
      sql: 'sql',
      java: 'java',
      kt: 'kotlin',
      yaml: 'yaml',
      yml: 'yaml',
    };
    return ext ? langMap[ext] : undefined;
  }, [selectedFilePath]);

  // UnifiedDiffEditor callbacks
  const handleEditorAddComment = useCallback(async (
    _docLine: number, // Not used - we get line info from anchor
    content: string,
    anchor: CommentAnchor
  ) => {
    if (!reviewId || !selectedFilePath || !addComment) return;
    try {
      const { line_start, line_end } = fromEditorAnchor(anchor);
      const result = await addComment(reviewId, selectedFilePath, line_start, line_end, content);
      if (result.success && result.comment) {
        setAllReviewComments(prev => [...prev, result.comment!]);
      } else {
        setCommentError('Failed to save comment');
      }
    } catch (err) {
      setCommentError('Failed to save comment');
      console.error('Add comment error:', err);
    }
  }, [reviewId, selectedFilePath, addComment]);

  const handleEditorEditComment = useCallback(async (id: string, content: string) => {
    if (!updateComment) return;
    try {
      const result = await updateComment(id, content);
      if (result.success) {
        setAllReviewComments(prev =>
          prev.map(c => c.id === id ? { ...c, content } : c)
        );
        setEditingCommentId(null);
      } else {
        setCommentError('Failed to update comment');
      }
    } catch (err) {
      setCommentError('Failed to update comment');
      console.error('Edit comment error:', err);
    }
  }, [updateComment]);

  const handleEditorStartEdit = useCallback((id: string) => {
    setEditingCommentId(id);
  }, []);

  const handleEditorCancelEdit = useCallback(() => {
    setEditingCommentId(null);
  }, []);

  // Resolve comment - also clears won't fix (mutual exclusivity)
  const handleEditorResolveComment = useCallback(async (id: string, resolved: boolean) => {
    if (!resolveComment) return;
    try {
      const result = await resolveComment(id, resolved);
      if (result.success) {
        setAllReviewComments(prev =>
          prev.map(c => c.id === id ? {
            ...c,
            resolved,
            resolved_by: resolved ? 'user' : '',
            // Clear won't fix when resolving (mutual exclusivity)
            wont_fix: resolved ? false : c.wont_fix,
            wont_fix_by: resolved ? '' : c.wont_fix_by,
          } : c)
        );
      } else {
        setCommentError('Failed to update comment');
      }
    } catch (err) {
      setCommentError('Failed to update comment');
      console.error('Resolve comment error:', err);
    }
  }, [resolveComment]);

  // Won't fix comment - also clears resolved (mutual exclusivity)
  const handleEditorWontFixComment = useCallback(async (id: string, wontFix: boolean) => {
    if (!wontFixComment) return;
    try {
      const result = await wontFixComment(id, wontFix);
      if (result.success) {
        setAllReviewComments(prev =>
          prev.map(c => c.id === id ? {
            ...c,
            wont_fix: wontFix,
            wont_fix_by: wontFix ? 'user' : '',
            // Clear resolved when marking won't fix (mutual exclusivity)
            resolved: wontFix ? false : c.resolved,
            resolved_by: wontFix ? '' : c.resolved_by,
          } : c)
        );
      } else {
        setCommentError('Failed to update comment');
      }
    } catch (err) {
      setCommentError('Failed to update comment');
      console.error('Won\'t fix comment error:', err);
    }
  }, [wontFixComment]);

  const handleEditorDeleteComment = useCallback(async (id: string) => {
    if (!deleteComment) return;
    try {
      const result = await deleteComment(id);
      if (result.success) {
        setAllReviewComments(prev => prev.filter(c => c.id !== id));
      } else {
        setCommentError('Failed to delete comment');
      }
    } catch (err) {
      setCommentError('Failed to delete comment');
      console.error('Delete comment error:', err);
    }
  }, [deleteComment]);

  const renderTree = useCallback((nodes: TreeNode[], depth: number, isAutoSkip: boolean) => {
    return nodes.map((node, index) => {
      if (node.type === 'dir') {
        return (
          <div key={`dir-${node.fullPath}-${index}`}>
            <div
              className="review-tree-dir"
              style={{ paddingLeft: `${depth * 12 + 12}px` }}
              title={node.fullPath}
            >
              {node.name}
            </div>
            {node.children && renderTree(node.children, depth + 1, isAutoSkip)}
          </div>
        );
      }

      const file = node.file!;
      const isSelected = selectedFilePath === file.path;
      const isViewed = viewedFiles.has(file.path);
      const isChanged = changedSinceViewed.has(file.path);
      return (
        <div
          key={`file-${file.path}-${index}`}
          className={`file-item ${isSelected ? 'selected' : ''} ${isViewed ? 'viewed' : ''} ${isChanged ? 'changed' : ''} ${isAutoSkip ? 'auto-skip' : ''}`}
          style={{ paddingLeft: `${depth * 12 + 12}px` }}
          onClick={() => setSelectedFilePath(file.path)}
        >
          <span className="file-icon">{getFileIcon(file)}</span>
          <span className={`file-status ${file.status}`}>{getStatusLabel(file.status)}</span>
          <span className="file-name" title={file.path}>{node.name}</span>
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
          {fileCommentCounts[file.path] > 0 && (
            <span className="file-comment-count">{fileCommentCounts[file.path]}</span>
          )}
        </div>
      );
    });
  }, [selectedFilePath, viewedFiles, changedSinceViewed, getFileIcon, getStatusLabel, fileCommentCounts]);

  const currentFileIndex = selectedFile ? allFiles.findIndex(f => f.path === selectedFile.path) : -1;

  return (
      <div className="review-panel">
        <div className="review-header">
          <span className="review-title">
            Diff: {gitStatus?.directory?.split('/').pop() || 'changes'}
          </span>
          <span className="review-file-count">
            {currentFileIndex + 1}/{allFiles.length} files
          </span>
          {isLoadingBranchDiff && allFiles.length === 0 && (
            <span className="review-sync-status">Loading changes...</span>
          )}
          {isSyncingRemotes && (
            <span className="review-sync-status syncing">Syncing with origin...</span>
          )}
          {remotesSyncWarning && (
            <span className="review-sync-status warning">{remotesSyncWarning}</span>
          )}
          <div className="review-header-actions">
            {onOpenEditor && (
              <button
                className="review-open-btn"
                onClick={() => onOpenEditor(selectedFilePath || undefined)}
                disabled={!repoPath}
                title={selectedFilePath ? 'Open file in $EDITOR' : 'Open project in $EDITOR'}
              >
                Open in Editor
              </button>
            )}
            <button className="review-close" onClick={onClose} title="Hide diff panel (Esc or ⌘⇧E)">
              Hide <kbd>Esc</kbd>
            </button>
          </div>
        </div>

        <div className="review-body">
          <div className="review-file-list">
            {needsReviewFiles.length > 0 && (
              <div className="file-group">
                <div className="file-group-header">NEEDS REVIEW</div>
                {renderTree(needsReviewTree, 0, false)}
              </div>
            )}

            {autoSkipFiles.length > 0 && (
              <div className="file-group auto-skip">
                <div className="file-group-header">AUTO-SKIP</div>
                {renderTree(autoSkipTree, 0, true)}
              </div>
            )}

            {allFiles.length === 0 && (
              <div className="file-list-empty">No changed files</div>
            )}
          </div>

          <div className="review-diff-area">
            <div className="diff-toolbar">
              <span className="diff-filename">
                {selectedFile?.path || 'Select a file'}
                {selectedFilePath && changedSinceViewed.has(selectedFilePath) && (
                  <span className="changed-badge">changed</span>
                )}
              </span>
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
              ) : !diffContent ? (
                <div className="diff-loading">Loading diff...</div>
              ) : (
                <UnifiedDiffEditor
                  original={diffContent.original}
                  modified={diffContent.modified}
                  comments={editorComments}
                  editingCommentId={editingCommentId}
                  resolvedTheme={resolvedTheme}
                  fontSize={fontSize}
                  language={editorLanguage}
                  contextLines={expandedContext === -1 ? 0 : 3}
                  scrollToLine={scrollToLine}
                  filePath={selectedFilePath || undefined}
                  onAddComment={handleEditorAddComment}
                  onEditComment={handleEditorEditComment}
                  onStartEdit={handleEditorStartEdit}
                  onCancelEdit={handleEditorCancelEdit}
                  onResolveComment={handleEditorResolveComment}
                  onWontFixComment={handleEditorWontFixComment}
                  onDeleteComment={handleEditorDeleteComment}
                />
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

        {/* Comment error toast */}
        {commentError && (
          <div style={{
            position: 'absolute',
            bottom: 60,
            left: '50%',
            transform: 'translateX(-50%)',
            background: '#dc2626',
            color: 'white',
            padding: '8px 16px',
            borderRadius: 6,
            fontSize: 13,
            boxShadow: '0 4px 12px rgba(0,0,0,0.3)',
            zIndex: 1010,
          }}>
            {commentError}
          </div>
        )}
      </div>
  );
}
