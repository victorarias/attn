// app/src/components/DiffDetailPanel.tsx
import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { GitStatusUpdate, FileDiffResult, ReviewState, BranchDiffFile, BranchDiffFilesResult } from '../hooks/useDaemonSocket';
import type { ResolvedTheme } from '../hooks/useTheme';
import type { ReviewComment } from '../types/generated';
import DiffView from './DiffView';
import './DiffDetailPanel.css';
import { updateReviewPerf } from '../utils/reviewPerf';
import { hashContent } from '../utils/reviewHash';
import { commentLineRef, isOriginalSideComment } from '../utils/reviewComment';

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

const BACKGROUND_CHANGE_CHECK_CONCURRENCY = 2;
const BACKGROUND_CHANGE_CHECK_DEBOUNCE_MS = 500;

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

type DiffContent = {
  original: string;
  modified: string;
};

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
  deleteComment?: (commentId: string) => Promise<{ success: boolean }>;
  getComments?: (reviewId: string, filepath?: string) => Promise<{ success: boolean; comments?: ReviewComment[] }>;
  resolvedTheme?: ResolvedTheme;
  // Controlled selection. The parent owns which file is shown so that
  // external triggers (ChangesPanel clicks, shortcut open) and internal
  // navigation share a single source of truth.
  selectedFilePath: string | null;
  onSelectFilePath: (path: string | null) => void;
  // Send a code reference to the active agent session
  onSendToClaude?: (reference: string) => void;
  // Global UI scale; drives the diff code font size (--diffs-font-size).
  scale?: number;
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
  deleteComment,
  getComments,
  resolvedTheme = 'dark',
  selectedFilePath,
  onSelectFilePath,
  onSendToClaude,
  scale = 1,
}: DiffDetailPanelProps) {
  const [contentVisible, setContentVisible] = useState(isOpen);
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [reviewId, setReviewId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [showSelectedDiffPending, setShowSelectedDiffPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [diffContent, setDiffContent] = useState<DiffContent | null>(null);
  const [expandedContext, setExpandedContext] = useState(0); // 0 = hunks mode (collapse unchanged), -1 = full file
  const [diffStyle, setDiffStyle] = useState<'unified' | 'split'>('unified');
  const fontSize = Math.round(13 * scale);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const diffContentCacheRef = useRef<Map<string, DiffContent>>(new Map());
  const selectedDiffPendingTimerRef = useRef<number | null>(null);

  // Branch diff state - PR-like comparison against origin/main
  const [branchDiffFiles, setBranchDiffFiles] = useState<BranchDiffFile[]>([]);
  const [baseRef, setBaseRef] = useState<string>('');
  const [isLoadingBranchDiff, setIsLoadingBranchDiff] = useState(false);
  const [isSyncingRemotes, setIsSyncingRemotes] = useState(false);
  const [remotesSyncWarning, setRemotesSyncWarning] = useState<string | null>(null);
  const branchDiffRequestIdRef = useRef(0);

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
  const [backgroundChangeCheckCount, setBackgroundChangeCheckCount] = useState(0);
  const backgroundCheckQueueRef = useRef<string[]>([]);
  const backgroundCheckQueuedPathsRef = useRef<Set<string>>(new Set());
  const backgroundCheckInFlightPathsRef = useRef<Set<string>>(new Set());
  const backgroundCheckRerunPathsRef = useRef<Set<string>>(new Set());
  const backgroundCheckTimerRef = useRef<number | null>(null);
  const backgroundCheckGenerationRef = useRef(0);
  const selectedFilePathRef = useRef<string | null>(selectedFilePath);
  const contentVisibleRef = useRef(contentVisible);
  const baseRefRef = useRef(baseRef);
  const fetchDiffRef = useRef(fetchDiff);
  const lastBackgroundGitStatusRef = useRef<GitStatusUpdate | null>(null);

  useEffect(() => {
    selectedFilePathRef.current = selectedFilePath;
    contentVisibleRef.current = contentVisible;
    baseRefRef.current = baseRef;
    fetchDiffRef.current = fetchDiff;
  }, [baseRef, contentVisible, fetchDiff, selectedFilePath]);

  const syncBackgroundChangeCheckCount = useCallback(() => {
    const activePaths = new Set([
      ...backgroundCheckQueueRef.current,
      ...backgroundCheckInFlightPathsRef.current,
      ...backgroundCheckRerunPathsRef.current,
    ]);
    setBackgroundChangeCheckCount(activePaths.size);
  }, []);

  const clearSelectedDiffPendingTimer = useCallback(() => {
    if (selectedDiffPendingTimerRef.current !== null) {
      window.clearTimeout(selectedDiffPendingTimerRef.current);
      selectedDiffPendingTimerRef.current = null;
    }
  }, []);

  const clearBackgroundChangeCheckTimer = useCallback(() => {
    if (backgroundCheckTimerRef.current !== null) {
      window.clearTimeout(backgroundCheckTimerRef.current);
      backgroundCheckTimerRef.current = null;
    }
  }, []);

  const resetBackgroundChangeChecks = useCallback(() => {
    backgroundCheckGenerationRef.current += 1;
    clearBackgroundChangeCheckTimer();
    backgroundCheckQueueRef.current = [];
    backgroundCheckQueuedPathsRef.current.clear();
    backgroundCheckInFlightPathsRef.current.clear();
    backgroundCheckRerunPathsRef.current.clear();
    setBackgroundChangeCheckCount(0);
  }, [clearBackgroundChangeCheckTimer]);

  const drainBackgroundChangeChecks = useCallback(() => {
    if (!contentVisibleRef.current) {
      resetBackgroundChangeChecks();
      return;
    }

    while (
      backgroundCheckInFlightPathsRef.current.size < BACKGROUND_CHANGE_CHECK_CONCURRENCY &&
      backgroundCheckQueueRef.current.length > 0
    ) {
      const viewedPath = backgroundCheckQueueRef.current.shift();
      if (!viewedPath) {
        continue;
      }

      backgroundCheckQueuedPathsRef.current.delete(viewedPath);
      if (backgroundCheckInFlightPathsRef.current.has(viewedPath)) {
        continue;
      }

      if (viewedPath === selectedFilePathRef.current) {
        continue;
      }

      const prevHash = viewedDiffHashesRef.current.get(viewedPath);
      if (!prevHash) {
        continue;
      }

      const generation = backgroundCheckGenerationRef.current;
      backgroundCheckInFlightPathsRef.current.add(viewedPath);
      syncBackgroundChangeCheckCount();

      fetchDiffRef.current(viewedPath, { baseRef: baseRefRef.current })
        .then((result) => {
          if (
            generation !== backgroundCheckGenerationRef.current ||
            !contentVisibleRef.current ||
            viewedPath === selectedFilePathRef.current
          ) {
            return;
          }

          const newHash = hashContent(result.original + result.modified);
          const currentHash = viewedDiffHashesRef.current.get(viewedPath);
          if (currentHash && currentHash !== newHash) {
            setChangedSinceViewed(prev => new Set(prev).add(viewedPath));
            viewedDiffHashesRef.current.set(viewedPath, newHash);
            diffContentCacheRef.current.set(viewedPath, {
              original: result.original,
              modified: result.modified,
            });
          }
        })
        .catch(() => {
          // Background checks are advisory. Keep the foreground diff stable.
        })
        .finally(() => {
          if (generation !== backgroundCheckGenerationRef.current) {
            return;
          }
          backgroundCheckInFlightPathsRef.current.delete(viewedPath);
          if (
            backgroundCheckRerunPathsRef.current.delete(viewedPath) &&
            contentVisibleRef.current &&
            viewedPath !== selectedFilePathRef.current &&
            viewedDiffHashesRef.current.has(viewedPath)
          ) {
            backgroundCheckQueueRef.current.push(viewedPath);
            backgroundCheckQueuedPathsRef.current.add(viewedPath);
          }
          syncBackgroundChangeCheckCount();
          drainBackgroundChangeChecks();
        });
    }

    syncBackgroundChangeCheckCount();
  }, [resetBackgroundChangeChecks, syncBackgroundChangeCheckCount]);

  const scheduleBackgroundChangeChecks = useCallback((paths: string[]) => {
    let added = false;
    for (const path of paths) {
      if (path === selectedFilePathRef.current) {
        continue;
      }
      if (!viewedDiffHashesRef.current.has(path)) {
        continue;
      }
      if (
        backgroundCheckQueuedPathsRef.current.has(path)
      ) {
        continue;
      }
      if (backgroundCheckInFlightPathsRef.current.has(path)) {
        if (!backgroundCheckRerunPathsRef.current.has(path)) {
          backgroundCheckRerunPathsRef.current.add(path);
          added = true;
        }
        continue;
      }
      backgroundCheckQueueRef.current.push(path);
      backgroundCheckQueuedPathsRef.current.add(path);
      added = true;
    }

    if (!added) {
      syncBackgroundChangeCheckCount();
      return;
    }

    syncBackgroundChangeCheckCount();
    clearBackgroundChangeCheckTimer();
    backgroundCheckTimerRef.current = window.setTimeout(() => {
      backgroundCheckTimerRef.current = null;
      drainBackgroundChangeChecks();
    }, BACKGROUND_CHANGE_CHECK_DEBOUNCE_MS);
  }, [clearBackgroundChangeCheckTimer, drainBackgroundChangeChecks, syncBackgroundChangeCheckCount]);

  // Fetch branch diff files when panel opens (PR-like comparison vs origin/main)
  // Load local refs immediately, then refresh in background after fetching remotes.
  useEffect(() => {
    if (!isOpen || !repoPath) return;

    let cancelled = false;
    const requestId = ++branchDiffRequestIdRef.current;
    const isCurrentRequest = () => !cancelled && requestId === branchDiffRequestIdRef.current;
    let localRequestStarted = false;
    let localApplied = false;
    let remoteApplied = false;
    let localTimer: ReturnType<typeof setTimeout> | null = null;
    let localDiffPromise: Promise<void> | null = null;

    const applyBranchDiff = (result: BranchDiffFilesResult) => {
      setBranchDiffFiles(result.files);
      setBaseRef(result.base_ref);
    };

    const runLocalDiff = () => {
      if (!isCurrentRequest() || localRequestStarted || remoteApplied) return;
      localRequestStarted = true;

      localDiffPromise = sendGetBranchDiffFiles(repoPath)
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
    // Clear stale files from a previous branch so orphan-cleanup
    // doesn't run against the wrong file list while the fetch is in-flight.
    setBranchDiffFiles([]);
    setIsLoadingBranchDiff(true);
    // Small delay avoids guaranteed duplicate heavy diff calls when remote sync is fast.
    localTimer = setTimeout(() => {
      runLocalDiff();
    }, 150);

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
        return (localDiffPromise ?? Promise.resolve()).then(() => {
          if (!isCurrentRequest()) return null;
          return sendGetBranchDiffFiles(repoPath);
        });
      })
      .then((result) => {
        if (!result) return;
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

  // Only count unresolved comments on files that are currently in the diff
  const diffFilePaths = useMemo(() => new Set(allFiles.map(f => f.path)), [allFiles]);
  const unresolvedComments = useMemo(
    () => allReviewComments.filter(c => !c.resolved && diffFilePaths.has(c.filepath)),
    [allReviewComments, diffFilePaths]
  );

  // Wrap onSendToClaude so any send — header button or per-comment — also closes the panel.
  const sendToClaudeAndClose = useCallback((reference: string) => {
    onSendToClaude?.(reference);
    onClose();
  }, [onSendToClaude, onClose]);

  const handleSendUnresolvedToClaude = useCallback(() => {
    if (!onSendToClaude || unresolvedComments.length === 0) return;

    const byFile = new Map<string, typeof unresolvedComments>();
    for (const comment of unresolvedComments) {
      const list = byFile.get(comment.filepath) || [];
      list.push(comment);
      byFile.set(comment.filepath, list);
    }

    const lines: string[] = ['Unresolved review comments:'];
    for (const [filepath, fileComments] of byFile.entries()) {
      lines.push(`\n${filepath}`);
      for (const comment of fileComments) {
        const side = isOriginalSideComment(comment) ? 'original' : 'modified';
        lines.push(`- @${filepath}:${commentLineRef(comment)} (${side}) ${comment.content}`);
      }
    }

    sendToClaudeAndClose(lines.join('\n'));
  }, [onSendToClaude, unresolvedComments, sendToClaudeAndClose]);

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

  // Fallback: when the panel opens without a selection, pick the first
  // reviewable file.
  useEffect(() => {
    if (!isOpen || selectedFilePath || needsReviewFiles.length === 0) return;
    onSelectFilePath(needsReviewFiles[0].path);
  }, [isOpen, needsReviewFiles, selectedFilePath, onSelectFilePath]);

  // Keep current selection on branch diff refresh when possible.
  // If the file disappears, select the first available review file.
  useEffect(() => {
    if (!isOpen || !selectedFilePath) return;
    // Avoid selection ping-pong while branch diff is still empty/loading.
    if (allFiles.length === 0) return;
    if (allFiles.some((f) => f.path === selectedFilePath)) return;

    if (needsReviewFiles.length > 0) {
      onSelectFilePath(needsReviewFiles[0].path);
      return;
    }
    if (allFiles.length > 0) {
      onSelectFilePath(allFiles[0].path);
      return;
    }
    onSelectFilePath(null);
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

  // Auto-delete comments for files no longer in the branch diff.
  // diffFilePaths.size === 0 means the diff is still loading — don't touch anything yet.
  // (branchDiffFiles is cleared to [] on branch change before the fresh fetch returns.)
  useEffect(() => {
    if (!deleteComment || diffFilePaths.size === 0 || allReviewComments.length === 0) return;

    const orphaned = allReviewComments.filter(c => !diffFilePaths.has(c.filepath));
    if (orphaned.length === 0) return;

    setAllReviewComments(prev => prev.filter(c => diffFilePaths.has(c.filepath)));
    for (const comment of orphaned) {
      deleteComment(comment.id).catch(console.error);
    }
  }, [allReviewComments, diffFilePaths, deleteComment]);

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

  // Move focus into the panel when it opens so keyboard shortcuts
  // (j/k, ], e, etc.) work immediately instead of being swallowed by
  // the previously focused terminal textarea or contentEditable editor.
  useEffect(() => {
    if (!isOpen) return;
    panelRef.current?.focus({ preventScroll: true });
  }, [isOpen]);

  // Reset state when closing
  useEffect(() => {
    if (!contentVisible) {
      onSelectFilePath(null);
      setDiffContent(null);
      setError(null);
      setExpandedContext(0);
      setLoading(false);
      setShowSelectedDiffPending(false);
      clearSelectedDiffPendingTimer();
      setIsLoadingBranchDiff(false);
      setIsSyncingRemotes(false);
      setRemotesSyncWarning(null);
      diffContentCacheRef.current.clear();
      // Clear change detection state
      viewedDiffHashesRef.current.clear();
      setChangedSinceViewed(new Set());
      resetBackgroundChangeChecks();
    }
  }, [clearSelectedDiffPendingTimer, contentVisible, resetBackgroundChangeChecks]);

  const previousRepoPathRef = useRef<string | null>(null);
  useEffect(() => {
    if (!isOpen) return;
    const prevRepo = previousRepoPathRef.current;
    if (prevRepo && prevRepo !== repoPath) {
      onSelectFilePath(null);
      setDiffContent(null);
      setError(null);
      setLoading(false);
      setShowSelectedDiffPending(false);
      clearSelectedDiffPendingTimer();
      setExpandedContext(0);
      setBranchDiffFiles([]);
      setBaseRef('');
      setRemotesSyncWarning(null);
      diffContentCacheRef.current.clear();
      viewedDiffHashesRef.current.clear();
      setChangedSinceViewed(new Set());
      setViewedFiles(new Set());
      resetBackgroundChangeChecks();
    }
    previousRepoPathRef.current = repoPath;
  }, [clearSelectedDiffPendingTimer, isOpen, repoPath, resetBackgroundChangeChecks]);

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
      setLoading(false);
      setShowSelectedDiffPending(false);
      clearSelectedDiffPendingTimer();
      return;
    }

    // Store current path to check if we're still viewing the same file when fetch completes
    const fetchPath = selectedFile.path;
    const isFirstView = !viewedFiles.has(fetchPath);
    const cachedContent = diffContentCacheRef.current.get(fetchPath) || null;

    setDiffContent(cachedContent);
    setLoading(true);
    setShowSelectedDiffPending(false);
    setError(null);
    clearSelectedDiffPendingTimer();
    selectedDiffPendingTimerRef.current = window.setTimeout(() => {
      selectedDiffPendingTimerRef.current = null;
      if (selectedFilePathRef.current === fetchPath) {
        setShowSelectedDiffPending(true);
      }
    }, 250);

    // Use baseRef for PR-like diff comparison (origin/main vs working directory)
    fetchDiff(fetchPath, { baseRef })
      .then((result) => {
        // Only update if we're still viewing the same file
        if (selectedFilePathRef.current !== fetchPath) return;

        const newContent = { original: result.original, modified: result.modified };
        const newHash = hashContent(result.original + result.modified);
        const prevHash = viewedDiffHashesRef.current.get(fetchPath);

        diffContentCacheRef.current.set(fetchPath, newContent);
        setDiffContent(newContent);
        setLoading(false);
        setShowSelectedDiffPending(false);
        clearSelectedDiffPendingTimer();

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
        if (selectedFilePathRef.current !== fetchPath) return;
        setError(err.message || 'Failed to load diff');
        setLoading(false);
        setShowSelectedDiffPending(false);
        clearSelectedDiffPendingTimer();
      });
    return () => {
      clearSelectedDiffPendingTimer();
    };
  // Note: viewedFiles intentionally excluded - we read it but don't want re-runs when it changes
  }, [clearSelectedDiffPendingTimer, diffFetchKey, selectedFile, fetchDiff, baseRef, reviewId, markFileViewed]);

  // Check for changes in viewed files when gitStatus updates (for files not currently selected)
  useEffect(() => {
    if (!gitStatus || !contentVisible) return;
    if (lastBackgroundGitStatusRef.current === gitStatus) return;
    lastBackgroundGitStatusRef.current = gitStatus;

    const changedPaths = new Set(
      [...gitStatus.staged, ...gitStatus.unstaged, ...gitStatus.untracked].flatMap((file) => (
        file.old_path ? [file.path, file.old_path] : [file.path]
      ))
    );
    const candidates = Array.from(viewedFiles).filter((viewedPath) => (
      viewedPath !== selectedFilePath &&
      changedPaths.has(viewedPath) &&
      viewedDiffHashesRef.current.has(viewedPath)
    ));

    if (candidates.length === 0) {
      return;
    }

    scheduleBackgroundChangeChecks(candidates);
  }, [allFiles, contentVisible, gitStatus, scheduleBackgroundChangeChecks, selectedFilePath, viewedFiles]);

  useEscapeStack(onClose, isOpen);

  // Keyboard navigation — capture phase to intercept before the diff view
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      // Don't capture navigation keystrokes when typing in inputs or editors
      if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) {
        return;
      }

      // Font size (Cmd/Ctrl + = / - / 0) is handled by the global
      // useUIScale shortcut, which drives `scale` and thus `fontSize`.
      if (e.metaKey || e.ctrlKey) {
        return;
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
          // Toggle between the two modes the UI actually supports:
          // hunks (0) and full file (-1).
          e.preventDefault();
          setExpandedContext(prev => prev === -1 ? 0 : -1);
          break;
      }
    };

    window.addEventListener('keydown', handleKeyDown, true);
    return () => window.removeEventListener('keydown', handleKeyDown, true);
  }, [isOpen, allFiles, selectedFile]);

  const navigateFiles = useCallback((direction: 'prev' | 'next') => {
    if (!selectedFilePath || allFiles.length === 0) return;
    const currentIndex = allFiles.findIndex(f => f.path === selectedFilePath);
    const newIndex = direction === 'next'
      ? Math.min(currentIndex + 1, allFiles.length - 1)
      : Math.max(currentIndex - 1, 0);
    onSelectFilePath(allFiles[newIndex].path);
  }, [allFiles, selectedFilePath]);

  const navigateToNextUnreviewed = useCallback(() => {
    const current = selectedFilePath;

    // Mark the current file viewed on its way out so `]` consistently
    // advances instead of re-selecting the same file.
    if (current) {
      const wasFirstView = !viewedFiles.has(current);
      setViewedFiles(prev => {
        if (prev.has(current)) return prev;
        return new Set(prev).add(current);
      });
      setChangedSinceViewed(prev => {
        if (!prev.has(current)) return prev;
        const next = new Set(prev);
        next.delete(current);
        return next;
      });
      if (wasFirstView && reviewId) {
        markFileViewed(reviewId, current, true).catch(err => {
          console.error('Failed to persist viewed state:', err);
        });
      }
    }

    // Prefer files changed since last viewed, then any unviewed file,
    // always excluding the one we just marked.
    const changedFile = needsReviewFiles.find(
      f => f.path !== current && changedSinceViewed.has(f.path)
    );
    if (changedFile) {
      onSelectFilePath(changedFile.path);
      return;
    }
    const unreviewed = needsReviewFiles.find(
      f => f.path !== current && !viewedFiles.has(f.path)
    );
    if (unreviewed) {
      onSelectFilePath(unreviewed.path);
    }
  }, [needsReviewFiles, viewedFiles, changedSinceViewed, selectedFilePath, reviewId, markFileViewed]);

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
  // Diff rendering perf telemetry
  // ============================================================================

  useEffect(() => {
    updateReviewPerf({
      panel: {
        active: isOpen,
        selectedFilePath,
        fileCount: allFiles.length,
        needsReviewFileCount: needsReviewFiles.length,
        autoSkipFileCount: autoSkipFiles.length,
        commentCount: comments.length,
        originalLength: diffContent?.original.length || 0,
        modifiedLength: diffContent?.modified.length || 0,
      },
    });
  }, [
    allFiles.length,
    autoSkipFiles.length,
    comments.length,
    diffContent,
    isOpen,
    needsReviewFiles.length,
    selectedFilePath,
  ]);

  useEffect(() => {
    return () => {
      updateReviewPerf({
        panel: {
          active: false,
          selectedFilePath: null,
          fileCount: 0,
          needsReviewFileCount: 0,
          autoSkipFileCount: 0,
          commentCount: 0,
          originalLength: 0,
          modifiedLength: 0,
        },
      });
    };
  }, []);

  // ============================================================================
  // DiffView comment callbacks
  // ============================================================================

  // Add a comment. line_start/line_end already follow the protocol convention
  // (negative line_end encodes the original/deleted side).
  const handleEditorAddComment = useCallback(async (
    line_start: number,
    line_end: number,
    content: string,
  ) => {
    if (!reviewId || !selectedFilePath || !addComment) return;
    try {
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

  // Resolve comment
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
          onClick={() => onSelectFilePath(file.path)}
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
  const selectedDiffIsPending = loading && showSelectedDiffPending;
  const selectedDiffPendingLabel = diffContent
    ? 'Loading selected diff... showing cached content'
    : 'Loading selected diff...';

  return (
      <div className="review-panel" ref={panelRef} tabIndex={-1}>
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
          {backgroundChangeCheckCount > 0 && (
            <span className="review-sync-status background-checking">
              Checking {backgroundChangeCheckCount} viewed {backgroundChangeCheckCount === 1 ? 'file' : 'files'}...
            </span>
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
            {onSendToClaude && (
              <button
                className="review-send-btn"
                onClick={handleSendUnresolvedToClaude}
                disabled={unresolvedComments.length === 0}
                title="Send all unresolved comments to the active agent session"
              >
                Send unresolved ({unresolvedComments.length})
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
              {selectedDiffIsPending && selectedFile && (
                <span className="diff-primary-status" role="status">
                  <span className="diff-status-dot" />
                  {selectedDiffPendingLabel}
                </span>
              )}
              <div className="diff-actions">
                <button
                  className={`expand-btn ${diffStyle === 'unified' ? 'active' : ''}`}
                  onClick={() => setDiffStyle('unified')}
                  title="Unified layout"
                >
                  Unified
                </button>
                <button
                  className={`expand-btn ${diffStyle === 'split' ? 'active' : ''}`}
                  onClick={() => setDiffStyle('split')}
                  title="Split layout"
                >
                  Split
                </button>
                <span className="diff-actions-divider" aria-hidden="true" />
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
                  title="Full file (e)"
                >
                  Full
                </button>
              </div>
            </div>
            <div className="diff-content">
              {error ? (
                <div className="diff-error">{error}</div>
              ) : !selectedFile ? (
                <div className="diff-placeholder">Select a file to view diff</div>
              ) : !diffContent ? (
                <div className="diff-loading diff-loading-detail">
                  <div className="diff-loading-copy">
                    <span className="diff-loading-title">Loading selected diff...</span>
                    <span className="diff-loading-subtitle">Waiting for git to return the file diff.</span>
                  </div>
                  <div className="diff-loading-skeleton" aria-hidden="true">
                    <span />
                    <span />
                    <span />
                    <span />
                    <span />
                  </div>
                </div>
              ) : (
                <div className={`diff-editor-shell ${selectedDiffIsPending ? 'refreshing' : ''}`}>
                  <DiffView
                    original={diffContent.original}
                    modified={diffContent.modified}
                    filePath={selectedFilePath || undefined}
                    comments={comments}
                    editingCommentId={editingCommentId}
                    resolvedTheme={resolvedTheme}
                    fontSize={fontSize}
                    diffStyle={diffStyle}
                    expandUnchanged={expandedContext === -1}
                    onAddComment={handleEditorAddComment}
                    onEditComment={handleEditorEditComment}
                    onStartEdit={handleEditorStartEdit}
                    onCancelEdit={handleEditorCancelEdit}
                    onResolveComment={handleEditorResolveComment}
                    onDeleteComment={handleEditorDeleteComment}
                    onSendToClaude={onSendToClaude ? sendToClaudeAndClose : undefined}
                  />
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="review-footer">
          <span className="shortcut"><kbd>j</kbd>/<kbd>k</kbd> navigate</span>
          <span className="shortcut"><kbd>]</kbd> next unreviewed</span>
          <span className="shortcut"><kbd>e</kbd> toggle full/hunks</span>
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
