// app/src/components/ReviewPanel.tsx
import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { GitStatusUpdate, FileDiffResult, ReviewState, BranchDiffFile, BranchDiffFilesResult } from '../hooks/useDaemonSocket';
import type { ReviewComment } from '../types/generated';
import type { ReviewerEvent } from '../hooks/useDaemonSocket';
import UnifiedDiffEditor, {
  buildUnifiedDocument,
  resolveAnchor,
  hashContent,
  type DiffLine,
  type CommentAnchor,
  type InlineComment as EditorComment,
} from './UnifiedDiffEditor';
import './ReviewPanel.css';

// Regex patterns for file references
// Pattern 1: file.ext:line or file.ext:line-line (e.g., "src/app.ts:123" or "file.md:10-20")
const FILE_WITH_LINE_REGEX = /([^\s`"'<>()[\]{}]+\.[a-zA-Z0-9]+):(\d+)(?:-(\d+))?/g;

// Pattern 2: Standalone filenames with common code extensions (inline in text)
// Matches filenames like "server/go.mod" or "m2-client-walking.md"
const COMMON_EXTENSIONS = 'md|ts|tsx|js|jsx|go|py|rs|java|css|scss|json|yaml|yml|toml|sql|sh|bash|html|xml|vue|svelte|rb|php|c|cpp|h|hpp|swift|kt|scala|ex|exs|mod|sum|lock';
const STANDALONE_FILE_REGEX = new RegExp(
  `(?:^|[\\s,;:()\\[\\]{}])` +  // Start of string or preceded by whitespace/punctuation
  `((?:[a-zA-Z0-9_.-]+/)*` +     // Optional path segments
  `[a-zA-Z0-9_.-]+\\.(?:${COMMON_EXTENSIONS}))` +  // Filename with extension
  `(?=[\\s,;:()\\[\\]{}]|$)`,    // Followed by whitespace/punctuation or end
  'g'
);

/**
 * Find a matching file using suffix matching.
 * Returns the full path if exactly ONE file matches, otherwise null.
 *
 * Examples:
 * - "example.go" matches "src/example.go" (suffix match)
 * - "server/go.mod" matches "myapp/server/go.mod" (suffix match)
 * - Returns null if 0 matches or >1 matches
 */
function findMatchingFile(searchPath: string, availableFiles: string[]): string | null {
  // Try exact match first
  if (availableFiles.includes(searchPath)) {
    return searchPath;
  }

  // Suffix match: find files ending with /searchPath
  const suffix = '/' + searchPath;
  const matches = availableFiles.filter(f => f.endsWith(suffix) || f === searchPath);

  // Only return if exactly one match
  return matches.length === 1 ? matches[0] : null;
}

// Helper to parse text and create elements with clickable file references
// Only makes files clickable if they uniquely match a file in availableFiles
function parseFileReferences(
  text: string,
  availableFiles: string[],
  onFileClick: (filepath: string, line?: number) => void
): React.ReactNode[] {
  const parts: React.ReactNode[] = [];
  let lastIndex = 0;

  // Collect all matches from both patterns
  type Match = { index: number; length: number; filepath: string; line?: number; fullMatch: string };
  const matches: Match[] = [];

  // Pattern 1: file:line references
  FILE_WITH_LINE_REGEX.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = FILE_WITH_LINE_REGEX.exec(text)) !== null) {
    matches.push({
      index: match.index,
      length: match[0].length,
      filepath: match[1],
      line: parseInt(match[2], 10),
      fullMatch: match[0],
    });
  }

  // Pattern 2: standalone filenames (only if not already matched by pattern 1)
  STANDALONE_FILE_REGEX.lastIndex = 0;
  while ((match = STANDALONE_FILE_REGEX.exec(text)) !== null) {
    const filepath = match[1];
    const fullMatchStart = match.index + match[0].indexOf(filepath);

    // Skip if this overlaps with an existing match
    const overlaps = matches.some(m =>
      (fullMatchStart >= m.index && fullMatchStart < m.index + m.length) ||
      (m.index >= fullMatchStart && m.index < fullMatchStart + filepath.length)
    );

    if (!overlaps) {
      matches.push({
        index: fullMatchStart,
        length: filepath.length,
        filepath: filepath,
        fullMatch: filepath,
      });
    }
  }

  // Sort matches by position
  matches.sort((a, b) => a.index - b.index);

  // Build result
  for (const m of matches) {
    // Add text before the match
    if (m.index > lastIndex) {
      parts.push(text.slice(lastIndex, m.index));
    }

    // Check if this file uniquely matches an available file
    const resolvedPath = findMatchingFile(m.filepath, availableFiles);

    if (resolvedPath) {
      // Unique match - make it clickable
      parts.push(
        <span
          key={`${m.index}-${m.filepath}`}
          className="file-reference clickable"
          onClick={(e) => {
            e.stopPropagation();
            onFileClick(resolvedPath, m.line);
          }}
          title={m.line ? `Open ${resolvedPath} at line ${m.line}` : `Open ${resolvedPath}`}
        >
          {m.fullMatch}
        </span>
      );
    } else {
      // No match or multiple matches - just render as plain text
      parts.push(m.fullMatch);
    }

    lastIndex = m.index + m.length;
  }

  // Add remaining text
  if (lastIndex < text.length) {
    parts.push(text.slice(lastIndex));
  }

  return parts.length > 0 ? parts : [text];
}

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

interface ReviewPanelProps {
  isOpen: boolean;
  gitStatus: GitStatusUpdate | null;  // Still used for real-time updates
  repoPath: string;
  branch: string;
  baseBranch?: string;
  onClose: () => void;
  // Diff fetching - options: baseRef for PR-like diffs
  fetchDiff: (path: string, options?: { staged?: boolean; baseRef?: string }) => Promise<FileDiffResult>;
  // Branch diff - fetches all files changed vs origin/main
  sendGetBranchDiffFiles: (directory: string, baseRef?: string) => Promise<BranchDiffFilesResult>;
  // Fetch remotes to ensure we have latest origin state
  sendFetchRemotes: (repo: string) => Promise<{ success: boolean; error?: string }>;
  getReviewState: (repoPath: string, branch: string) => Promise<{ success: boolean; state?: ReviewState; error?: string }>;
  markFileViewed: (reviewId: string, filepath: string, viewed: boolean) => Promise<{ success: boolean; error?: string }>;
  onSendToClaude?: (reference: string) => void;
  // Comment operations
  addComment?: (reviewId: string, filepath: string, lineStart: number, lineEnd: number, content: string) => Promise<{ success: boolean; comment?: ReviewComment }>;
  updateComment?: (commentId: string, content: string) => Promise<{ success: boolean }>;
  resolveComment?: (commentId: string, resolved: boolean) => Promise<{ success: boolean }>;
  wontFixComment?: (commentId: string, wontFix: boolean) => Promise<{ success: boolean }>;
  deleteComment?: (commentId: string) => Promise<{ success: boolean }>;
  getComments?: (reviewId: string, filepath?: string) => Promise<{ success: boolean; comments?: ReviewComment[] }>;
  // Reviewer agent operations
  sendStartReview?: (reviewId: string, repoPath: string, branch: string, baseBranch: string) => void;
  sendCancelReview?: (reviewId: string) => void;
  reviewerEvents?: ReviewerEvent[];
  reviewerRunning?: boolean;
  reviewerError?: string;
  agentComments?: ReviewComment[];
  agentResolvedCommentIds?: string[];
  // Initial file to select when panel opens
  initialSelectedFile?: string;
}

export function ReviewPanel({
  isOpen,
  gitStatus,
  repoPath,
  branch,
  baseBranch = 'main',
  onClose,
  fetchDiff,
  sendGetBranchDiffFiles,
  sendFetchRemotes,
  getReviewState,
  markFileViewed,
  onSendToClaude,
  addComment,
  updateComment,
  resolveComment,
  wontFixComment,
  deleteComment,
  getComments,
  sendStartReview,
  sendCancelReview,
  reviewerEvents = [],
  reviewerRunning = false,
  reviewerError,
  agentComments = [],
  agentResolvedCommentIds = [],
  initialSelectedFile,
}: ReviewPanelProps) {
  // Track selected file by path for stability across gitStatus updates
  const [selectedFilePath, setSelectedFilePath] = useState<string | null>(null);
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [reviewId, setReviewId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [diffContent, setDiffContent] = useState<{ original: string; modified: string } | null>(null);
  const [expandedContext, setExpandedContext] = useState(0); // 0 = hunks mode (uses 3 lines context), -1 = full file
  const [fontSize, setFontSize] = useState(13); // Default font size
  const [reviewerPanelHeight, setReviewerPanelHeight] = useState(400); // Default reviewer panel height
  const [scrollToLine, setScrollToLine] = useState<number | undefined>(undefined);

  // Branch diff state - PR-like comparison against origin/main
  const [branchDiffFiles, setBranchDiffFiles] = useState<BranchDiffFile[]>([]);
  const [baseRef, setBaseRef] = useState<string>('');
  const [_isLoadingBranchDiff, setIsLoadingBranchDiff] = useState(false);
  // TODO: Use _isLoadingBranchDiff to show loading indicator in file list
  const [reviewerPanelCollapsed, setReviewerPanelCollapsed] = useState(false);
  const reviewerResizeRef = useRef<{ startY: number; startHeight: number } | null>(null);
  const reviewerOutputRef = useRef<HTMLDivElement>(null);

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

  // Auto-expand reviewer panel when review starts
  useEffect(() => {
    if (reviewerRunning) {
      setReviewerPanelCollapsed(false);
    }
  }, [reviewerRunning]);

  // Auto-scroll reviewer output to bottom as content streams
  useEffect(() => {
    if (reviewerOutputRef.current && !reviewerPanelCollapsed) {
      reviewerOutputRef.current.scrollTop = reviewerOutputRef.current.scrollHeight;
    }
  }, [reviewerEvents, reviewerPanelCollapsed]);

  // Derive comments for current file
  const comments = useMemo(() => {
    if (!selectedFilePath) return [];
    return allReviewComments.filter(c => c.filepath === selectedFilePath);
  }, [allReviewComments, selectedFilePath]);

  const unresolvedComments = useMemo(
    () => allReviewComments.filter(c => !c.resolved && !c.wont_fix),
    [allReviewComments]
  );

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
  useEffect(() => {
    if (isOpen && repoPath) {
      setIsLoadingBranchDiff(true);
      // Fetch remotes first to ensure we have latest origin state
      sendFetchRemotes(repoPath)
        .then(() => sendGetBranchDiffFiles(repoPath))
        .then((result) => {
          if (result.success) {
            setBranchDiffFiles(result.files);
            setBaseRef(result.base_ref);
          }
        })
        .catch((err) => {
          console.error('[ReviewPanel] Failed to fetch branch diff:', err);
        })
        .finally(() => {
          setIsLoadingBranchDiff(false);
        });
    }
  }, [isOpen, repoPath, sendFetchRemotes, sendGetBranchDiffFiles]);

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

  // Merge agent comments into local state as they arrive
  useEffect(() => {
    if (agentComments.length === 0) return;

    setAllReviewComments(prev => {
      // Get IDs of existing comments
      const existingIds = new Set(prev.map(c => c.id));
      // Filter to only new comments
      const newComments = agentComments.filter(c => !existingIds.has(c.id));
      if (newComments.length === 0) return prev;
      return [...prev, ...newComments];
    });
  }, [agentComments]);

  // Handle agent-resolved comments
  useEffect(() => {
    if (agentResolvedCommentIds.length === 0) return;

    setAllReviewComments(prev => {
      const resolvedSet = new Set(agentResolvedCommentIds);
      let hasChanges = false;
      const updated = prev.map(c => {
        if (resolvedSet.has(c.id) && !c.resolved) {
          hasChanges = true;
          return { ...c, resolved: true, resolved_by: 'agent' as const };
        }
        return c;
      });
      return hasChanges ? updated : prev;
    });
  }, [agentResolvedCommentIds]);

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
    if (!isOpen) {
      setSelectedFilePath(null);
      setDiffContent(null);
      setError(null);
      setExpandedContext(0);
      setScrollToLine(undefined);
      // Clear change detection state
      viewedDiffHashesRef.current.clear();
      setChangedSinceViewed(new Set());
    }
  }, [isOpen]);

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

  // Wrap onSendToClaude to also close the review panel
  const handleSendToClaude = useCallback((reference: string) => {
    onSendToClaude?.(reference);
    onClose();
  }, [onSendToClaude, onClose]);

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

  const handleSendUnresolvedToClaude = useCallback(() => {
    if (!onSendToClaude || unresolvedComments.length === 0) return;

    const byFile = new Map<string, ReviewComment[]>();
    for (const comment of unresolvedComments) {
      const list = byFile.get(comment.filepath) || [];
      list.push(comment);
      byFile.set(comment.filepath, list);
    }

    const lines: string[] = ['Unresolved review comments:'];
    for (const [filepath, fileComments] of byFile.entries()) {
      lines.push(`\n${filepath}`);
      for (const comment of fileComments) {
        const lineStart = comment.line_start;
        const lineEnd = Math.abs(comment.line_end);
        const side = comment.line_end < 0 ? 'original' : 'modified';
        const lineRef = lineStart === lineEnd ? `L${lineStart}` : `L${lineStart}-L${lineEnd}`;
        lines.push(`- @${filepath}:${lineRef} (${side}) ${comment.content}`);
      }
    }

    handleSendToClaude(lines.join('\n'));
  }, [onSendToClaude, unresolvedComments, handleSendToClaude]);

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
          <div className="review-header-actions">
            {sendStartReview && reviewId && (
              <button
                className={`review-agent-btn ${reviewerRunning ? 'running' : ''}`}
                onClick={() => {
                  if (reviewerRunning) {
                    sendCancelReview?.(reviewId);
                  } else {
                    const reviewBaseRef = baseRef || baseBranch;
                    sendStartReview(reviewId, repoPath, branch, reviewBaseRef);
                  }
                }}
                title={reviewerRunning ? 'Cancel review' : 'Run AI review'}
              >
                {reviewerRunning ? '⏹ Cancel' : '🤖 Review'}
              </button>
            )}
            {onSendToClaude && (
              <button
                className="review-send-btn"
                onClick={handleSendUnresolvedToClaude}
                disabled={unresolvedComments.length === 0}
                title="Send unresolved comments to Claude Code"
              >
                Send unresolved
              </button>
            )}
            <button className="review-close" onClick={onClose}>×</button>
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
              <div className="file-list-empty">No changes to review</div>
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
                  onSendToClaude={handleSendToClaude}
                />
              )}
            </div>
          </div>
        </div>

        {/* Reviewer Agent Output Panel */}
        {(reviewerRunning || reviewerEvents.length > 0) && (
          <div
            className={`reviewer-output-panel ${reviewerPanelCollapsed ? 'collapsed' : ''}`}
            style={{ height: reviewerPanelCollapsed ? 'auto' : reviewerPanelHeight }}
          >
            {!reviewerPanelCollapsed && (
              <div
                className="reviewer-resize-handle"
                onMouseDown={(e) => {
                  e.preventDefault();
                  reviewerResizeRef.current = {
                    startY: e.clientY,
                    startHeight: reviewerPanelHeight,
                  };
                  const handleMouseMove = (moveEvent: MouseEvent) => {
                    if (!reviewerResizeRef.current) return;
                    const delta = reviewerResizeRef.current.startY - moveEvent.clientY;
                    const newHeight = Math.max(100, Math.min(800, reviewerResizeRef.current.startHeight + delta));
                    setReviewerPanelHeight(newHeight);
                  };
                  const handleMouseUp = () => {
                    reviewerResizeRef.current = null;
                    document.removeEventListener('mousemove', handleMouseMove);
                    document.removeEventListener('mouseup', handleMouseUp);
                  };
                  document.addEventListener('mousemove', handleMouseMove);
                  document.addEventListener('mouseup', handleMouseUp);
                }}
              />
            )}
            <div
              className="reviewer-output-header"
              onClick={() => setReviewerPanelCollapsed(prev => !prev)}
              style={{ cursor: 'pointer' }}
            >
              <span className="reviewer-collapse-icon">{reviewerPanelCollapsed ? '▸' : '▾'}</span>
              <span>🤖 AI Review</span>
              {reviewerError && <span className="reviewer-error">⚠ {reviewerError}</span>}
              {reviewerRunning && <div className="reviewer-progress-line" />}
            </div>
            {!reviewerPanelCollapsed && (
            <div ref={reviewerOutputRef} className="reviewer-output-content" style={{ fontSize }}>
              {reviewerEvents.length === 0 && reviewerRunning && (
                <div className="reviewer-loading">
                  <div className="reviewer-loading-spinner">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <circle cx="12" cy="12" r="10" strokeOpacity="0.2" />
                      <path d="M12 2a10 10 0 0 1 10 10" strokeLinecap="round" />
                    </svg>
                  </div>
                  <div className="reviewer-loading-text">Analyzing your changes</div>
                </div>
              )}
              {reviewerEvents.map((event, index) => {
                if (event.type === 'chunk') {
                  // Custom components that make file references clickable
                  // Only files that uniquely match a changed file are clickable
                  const availableFilePaths = allFiles.map(f => f.path);
                  const handleFileClick = (filepath: string, line?: number) => {
                    setSelectedFilePath(filepath);
                    setScrollToLine(line);
                  };

                  // Process text children to find file references
                  const processChildren = (children: React.ReactNode): React.ReactNode => {
                    if (typeof children === 'string') {
                      return parseFileReferences(children, availableFilePaths, handleFileClick);
                    }
                    if (Array.isArray(children)) {
                      return children.map((child, i) => {
                        if (typeof child === 'string') {
                          const parsed = parseFileReferences(child, availableFilePaths, handleFileClick);
                          return parsed.length === 1 && parsed[0] === child ? child : <span key={i}>{parsed}</span>;
                        }
                        return child;
                      });
                    }
                    return children;
                  };

                  // eslint-disable-next-line @typescript-eslint/no-explicit-any
                  const markdownComponents: any = {
                    // Block elements
                    p: ({ children, ...props }: { children?: React.ReactNode }) => (
                      <p {...props}>{processChildren(children)}</p>
                    ),
                    li: ({ children, ...props }: { children?: React.ReactNode }) => (
                      <li {...props}>{processChildren(children)}</li>
                    ),
                    td: ({ children, ...props }: { children?: React.ReactNode }) => (
                      <td {...props}>{processChildren(children)}</td>
                    ),
                    th: ({ children, ...props }: { children?: React.ReactNode }) => (
                      <th {...props}>{processChildren(children)}</th>
                    ),
                    blockquote: ({ children, ...props }: { children?: React.ReactNode }) => (
                      <blockquote {...props}>{processChildren(children)}</blockquote>
                    ),
                    // Inline elements
                    strong: ({ children, ...props }: { children?: React.ReactNode }) => (
                      <strong {...props}>{processChildren(children)}</strong>
                    ),
                    em: ({ children, ...props }: { children?: React.ReactNode }) => (
                      <em {...props}>{processChildren(children)}</em>
                    ),
                    code: ({ children, className, ...props }: { children?: React.ReactNode; className?: string }) => {
                      // Only process inline code (not code blocks which have className)
                      if (!className) {
                        return <code {...props}>{processChildren(children)}</code>;
                      }
                      return <code className={className} {...props}>{children}</code>;
                    },
                  };

                  return (
                    <ReactMarkdown key={index} remarkPlugins={[remarkGfm]} components={markdownComponents}>
                      {event.content}
                    </ReactMarkdown>
                  );
                } else {
                  // tool_use - make add_comment calls clickable to navigate to file
                  const isAddComment = event.name === 'add_comment';
                  const filepath = isAddComment ? String(event.input.filepath || '') : '';
                  // Use suffix matching to resolve filepath to a changed file
                  const availableFilePaths = allFiles.map(f => f.path);
                  const resolvedPath = filepath ? findMatchingFile(filepath, availableFilePaths) : null;
                  const canNavigate = isAddComment && resolvedPath;

                  return (
                    <div
                      key={index}
                      className={`reviewer-tool-call ${canNavigate ? 'clickable' : ''}`}
                      onClick={canNavigate ? () => {
                        const lineStart = Number(event.input.line_start) || undefined;
                        setSelectedFilePath(resolvedPath);
                        setScrollToLine(lineStart);
                      } : undefined}
                      title={canNavigate ? `Click to open ${resolvedPath}` : undefined}
                    >
                      <div className="tool-call-header">
                        <span className="tool-call-icon">⏺</span>
                        <span className="tool-call-name">{event.name}</span>
                        <span className="tool-call-input">
                          ({Object.entries(event.input).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join(', ')})
                        </span>
                      </div>
                      <div className="tool-call-output">
                        <span className="tool-call-output-icon">⎿</span>
                        <pre>{event.output.length > 500 ? event.output.slice(0, 500) + '...' : event.output}</pre>
                      </div>
                    </div>
                  );
                }
              })}
            </div>
            )}
          </div>
        )}

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

    </>
  );
}
