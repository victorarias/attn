// app/src/components/ReviewPanel.tsx
import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { EditorView, gutter, GutterMarker, Decoration, WidgetType } from '@codemirror/view';
import { EditorState, RangeSet, StateField, StateEffect } from '@codemirror/state';
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
import type { ReviewComment } from '../types/generated';
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

// Comment gutter marker - renders clickable button for each line
class CommentMarker extends GutterMarker {
  constructor(public commentCount: number, public lineNum: number) {
    super();
  }

  toDOM() {
    const btn = document.createElement('button');
    btn.className = this.commentCount > 0 ? 'comment-gutter-btn has-comment' : 'comment-gutter-btn';
    btn.type = 'button';
    btn.dataset.lineNum = String(this.lineNum);

    if (this.commentCount > 0) {
      btn.innerHTML = `<span class="comment-icon">💬</span>`;
      btn.title = `${this.commentCount} comment${this.commentCount > 1 ? 's' : ''} - click to view`;
    } else {
      btn.innerHTML = `<span class="comment-icon-add">+</span>`;
      btn.title = 'Add comment';
    }

    return btn;
  }
}

// Inline comment widget - displays saved comments below the line
class InlineCommentWidget extends WidgetType {
  constructor(
    public comment: ReviewComment,
    public isEditing: boolean,
    public onSave: (content: string) => void,
    public onCancel: () => void,
    public onStartEdit: () => void,
    public onResolve: (resolved: boolean) => void
  ) {
    super();
  }

  toDOM() {
    const wrapper = document.createElement('div');
    wrapper.className = `inline-comment ${this.comment.resolved ? 'resolved' : ''}`;

    if (this.isEditing) {
      // Edit mode - show form
      const form = document.createElement('div');
      form.className = 'inline-comment-form';

      const textarea = document.createElement('textarea');
      textarea.className = 'inline-comment-textarea';
      textarea.value = this.comment.content;
      textarea.rows = 3;
      textarea.placeholder = 'Edit comment...';
      form.appendChild(textarea);

      const buttons = document.createElement('div');
      buttons.className = 'inline-comment-buttons';

      const cancelBtn = document.createElement('button');
      cancelBtn.className = 'inline-comment-btn';
      cancelBtn.textContent = 'Cancel';
      cancelBtn.type = 'button';
      cancelBtn.onclick = (e) => {
        e.stopPropagation();
        this.onCancel();
      };
      buttons.appendChild(cancelBtn);

      const saveBtn = document.createElement('button');
      saveBtn.className = 'inline-comment-btn save';
      saveBtn.textContent = 'Save';
      saveBtn.type = 'button';
      saveBtn.onclick = (e) => {
        e.stopPropagation();
        this.onSave(textarea.value);
      };
      buttons.appendChild(saveBtn);

      form.appendChild(buttons);
      wrapper.appendChild(form);

      // Focus textarea after render
      setTimeout(() => textarea.focus(), 0);
    } else {
      // Display mode
      const header = document.createElement('div');
      header.className = 'inline-comment-header';

      const author = document.createElement('span');
      author.className = `inline-comment-author ${this.comment.author}`;
      author.textContent = this.comment.author === 'agent' ? 'Claude' : 'You';
      header.appendChild(author);

      const actions = document.createElement('div');
      actions.className = 'inline-comment-actions';

      const editBtn = document.createElement('button');
      editBtn.className = 'inline-comment-btn';
      editBtn.textContent = 'Edit';
      editBtn.onclick = (e) => {
        e.stopPropagation();
        this.onStartEdit();
      };
      actions.appendChild(editBtn);

      if (!this.comment.resolved) {
        const resolveBtn = document.createElement('button');
        resolveBtn.className = 'inline-comment-btn resolve';
        resolveBtn.textContent = 'Resolve';
        resolveBtn.onclick = (e) => {
          e.stopPropagation();
          this.onResolve(true);
        };
        actions.appendChild(resolveBtn);
      }

      header.appendChild(actions);
      wrapper.appendChild(header);

      const content = document.createElement('div');
      content.className = 'inline-comment-content';
      content.textContent = this.comment.content;
      wrapper.appendChild(content);
    }

    return wrapper;
  }

  eq(other: InlineCommentWidget) {
    return other.comment.id === this.comment.id &&
           other.comment.content === this.comment.content &&
           other.comment.resolved === this.comment.resolved &&
           other.isEditing === this.isEditing;
  }

  ignoreEvent() {
    return false;
  }
}

// Widget for adding a new comment
class NewCommentWidget extends WidgetType {
  constructor(
    public lineNum: number,
    public onSave: (content: string) => void,
    public onCancel: () => void
  ) {
    super();
  }

  toDOM() {
    const wrapper = document.createElement('div');
    wrapper.className = 'inline-comment new';

    const form = document.createElement('div');
    form.className = 'inline-comment-form';

    const label = document.createElement('div');
    label.className = 'inline-comment-label';
    label.textContent = `Line ${this.lineNum}`;
    form.appendChild(label);

    const textarea = document.createElement('textarea');
    textarea.className = 'inline-comment-textarea';
    textarea.rows = 3;
    textarea.placeholder = 'Add a comment...';
    form.appendChild(textarea);

    const buttons = document.createElement('div');
    buttons.className = 'inline-comment-buttons';

    const cancelBtn = document.createElement('button');
    cancelBtn.className = 'inline-comment-btn';
    cancelBtn.textContent = 'Cancel';
    cancelBtn.type = 'button';
    cancelBtn.onclick = (e) => {
      e.stopPropagation();
      this.onCancel();
    };
    buttons.appendChild(cancelBtn);

    const saveBtn = document.createElement('button');
    saveBtn.className = 'inline-comment-btn save';
    saveBtn.textContent = 'Save';
    saveBtn.type = 'button';
    saveBtn.onclick = (e) => {
      e.stopPropagation();
      const content = textarea.value.trim();
      if (content) {
        this.onSave(content);
      }
    };
    buttons.appendChild(saveBtn);

    form.appendChild(buttons);
    wrapper.appendChild(form);

    // Focus textarea after render
    setTimeout(() => textarea.focus(), 0);

    return wrapper;
  }

  eq(other: NewCommentWidget) {
    return other.lineNum === this.lineNum;
  }

  ignoreEvent() {
    return false;
  }
}

// State effect to update comment markers
const setCommentLines = StateEffect.define<Map<number, number>>();

// State field that tracks which lines have comments
const commentLinesField = StateField.define<Map<number, number>>({
  create() {
    return new Map();
  },
  update(value, tr) {
    for (const effect of tr.effects) {
      if (effect.is(setCommentLines)) {
        return effect.value;
      }
    }
    return value;
  },
});

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
  // Comment operations
  addComment?: (reviewId: string, filepath: string, lineStart: number, lineEnd: number, content: string) => Promise<{ success: boolean; comment?: ReviewComment }>;
  updateComment?: (commentId: string, content: string) => Promise<{ success: boolean }>;
  resolveComment?: (commentId: string, resolved: boolean) => Promise<{ success: boolean }>;
  deleteComment?: (commentId: string) => Promise<{ success: boolean }>;
  getComments?: (reviewId: string, filepath?: string) => Promise<{ success: boolean; comments?: ReviewComment[] }>;
}

// Simple hash function for detecting content changes
function simpleHash(str: string): string {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    const char = str.charCodeAt(i);
    hash = ((hash << 5) - hash) + char;
    hash = hash & hash; // Convert to 32bit integer
  }
  return hash.toString(36);
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
  onSendToClaude: _onSendToClaude,
  addComment,
  updateComment,
  resolveComment,
  deleteComment: _deleteComment,
  getComments,
}: ReviewPanelProps) {
  // These props are reserved for future use
  void _onSendToClaude;
  void _deleteComment;
  // Track selected file by path for stability across gitStatus updates
  const [selectedFilePath, setSelectedFilePath] = useState<string | null>(null);
  const [viewedFiles, setViewedFiles] = useState<Set<string>>(new Set());
  const [reviewId, setReviewId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [diffContent, setDiffContent] = useState<{ original: string; modified: string } | null>(null);
  const [expandedContext, setExpandedContext] = useState(0); // 0 = hunks only, -1 = full
  const [fontSize, setFontSize] = useState(13); // Default font size
  const editorContainerRef = useRef<HTMLDivElement>(null);
  const editorViewRef = useRef<EditorView | null>(null);
  const scrollPositionRef = useRef<number>(0);

  // Comment state
  const [allReviewComments, setAllReviewComments] = useState<ReviewComment[]>([]);
  const [newCommentLine, setNewCommentLine] = useState<number | null>(null);
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);

  // Helper: check if comment is on a deleted line (marked by line_end < 0)
  // line_end encodes the deleted line index: -1 = after line 0, -2 = after line 1, etc.
  const isDeletedLineComment = (comment: ReviewComment) => comment.line_end < 0;

  // Derive comments for current file (used for CodeMirror gutter markers)
  const comments = useMemo(() => {
    if (!selectedFilePath) return [];
    return allReviewComments.filter(c => c.filepath === selectedFilePath);
  }, [allReviewComments, selectedFilePath]);

  // Split comments into regular and deleted-line comments
  const regularComments = useMemo(() =>
    comments.filter(c => !isDeletedLineComment(c)), [comments]);
  const deletedLineComments = useMemo(() =>
    comments.filter(c => isDeletedLineComment(c)), [comments]);

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

  // Auto-select first file when opening
  useEffect(() => {
    if (isOpen && needsReviewFiles.length > 0 && !selectedFilePath) {
      setSelectedFilePath(needsReviewFiles[0].path);
    }
  }, [isOpen, needsReviewFiles, selectedFilePath]);

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
    if (!isOpen) {
      setSelectedFilePath(null);
      setDiffContent(null);
      setError(null);
      setExpandedContext(0);
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
    const fetchStaged = selectedFile.staged;
    const isFirstView = !viewedFiles.has(fetchPath);

    // Only show loading on first view to avoid flickering during updates
    if (isFirstView) {
      setLoading(true);
    }
    setError(null);

    fetchDiff(fetchPath, fetchStaged)
      .then((result) => {
        // Only update if we're still viewing the same file
        if (selectedFilePath !== fetchPath) return;

        const newContent = { original: result.original, modified: result.modified };
        const newHash = simpleHash(result.original + result.modified);
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
  }, [diffFetchKey, selectedFile, selectedFilePath, fetchDiff, reviewId, markFileViewed]);

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

      // Fetch and check hash
      try {
        const result = await fetchDiff(viewedPath, fileInChanges.staged);
        const newHash = simpleHash(result.original + result.modified);
        const prevHash = viewedDiffHashesRef.current.get(viewedPath);

        if (prevHash && prevHash !== newHash) {
          setChangedSinceViewed(prev => new Set(prev).add(viewedPath));
          viewedDiffHashesRef.current.set(viewedPath, newHash);
        }
      } catch {
        // Ignore errors for background checks
      }
    });
  }, [gitStatus, viewedFiles, selectedFilePath, allFiles, fetchDiff]);

  // Create/update CodeMirror editor
  useEffect(() => {
    if (!editorContainerRef.current || !diffContent) return;

    // Clean up existing editor - save scroll position first
    if (editorViewRef.current) {
      scrollPositionRef.current = editorViewRef.current.scrollDOM.scrollTop;
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
    // Note: Line wrapping styles are in ReviewPanel.css (flex:1 + min-width:0 on cm-content)
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

    // Build map of line numbers to comment counts for this file
    const commentLineMap = new Map<number, number>();
    for (const comment of comments) {
      // Comments may span multiple lines, but we show marker on line_start
      const lineNum = comment.line_start;
      commentLineMap.set(lineNum, (commentLineMap.get(lineNum) || 0) + 1);
    }

    // Comment gutter extension with click handling
    const commentGutter = gutter({
      class: 'comment-gutter',
      markers: (view) => {
        const commentLines = view.state.field(commentLinesField);
        const markers: { from: number; to: number; value: GutterMarker }[] = [];

        // Render a marker on EVERY line so the + button is always visible
        for (let i = 1; i <= view.state.doc.lines; i++) {
          const line = view.state.doc.line(i);
          const commentCount = commentLines.get(i) || 0;
          markers.push({ from: line.from, to: line.from, value: new CommentMarker(commentCount, i) });
        }

        return RangeSet.of(markers, true);
      },
    });

    // Click handler for gutter buttons and diff lines
    const clickHandler = EditorView.domEventHandlers({
      mousedown: (event, view) => {
        const target = event.target as HTMLElement;

        // Don't handle clicks on inline comment elements
        if (target.closest('.inline-comment')) {
          return false;
        }

        // Check if clicking a gutter button - only open new comment (existing are inline)
        const gutterBtn = target.closest('.comment-gutter-btn');
        if (gutterBtn instanceof HTMLElement) {
          const lineNum = parseInt(gutterBtn.dataset.lineNum || '0', 10);
          if (lineNum > 0) {
            const hasComment = commentLineMap.has(lineNum);
            if (!hasComment) {
              setNewCommentLine(lineNum);
              return true;
            }
          }
        }

        // Try to get line number from click position
        const editorRect = view.dom.getBoundingClientRect();
        const y = event.clientY - editorRect.top + view.scrollDOM.scrollTop;

        // Use lineBlockAtHeight to find which line block we're in
        try {
          const block = view.lineBlockAtHeight(y);
          const lineNum = view.state.doc.lineAt(block.from).number;
          const hasComment = commentLineMap.has(lineNum);

          if (!hasComment) {
            setNewCommentLine(lineNum);
            return true;
          }
        } catch {
          // Fallback: try to get from DOM element
          const line = target.closest('.cm-line');
          if (line instanceof HTMLElement) {
            try {
              const pos = view.posAtDOM(line);
              const lineNum = view.state.doc.lineAt(pos).number;
              const hasComment = commentLineMap.has(lineNum);

              if (!hasComment) {
                setNewCommentLine(lineNum);
                return true;
              }
            } catch {
              // Ignore
            }
          }
        }

        return false;
      },
    });

    // Helper to calculate position at end of line
    const getLineEndPos = (lineNum: number, content: string) => {
      const lines = content.split('\n');
      if (lineNum < 1 || lineNum > lines.length) return -1;
      let pos = 0;
      for (let i = 0; i < lineNum; i++) {
        pos += (lines[i]?.length || 0) + 1;
      }
      return Math.max(0, pos - 1);
    };

    // Create inline comment decorations
    const inlineWidgets: { pos: number; widget: WidgetType }[] = [];

    // Add existing comment widgets (only regular comments, not deleted-line comments)
    for (const comment of regularComments) {
      const lineNum = comment.line_start;
      const pos = getLineEndPos(lineNum, diffContent.modified);
      if (pos >= 0) {
        const isEditing = editingCommentId === comment.id;
        inlineWidgets.push({
          pos,
          widget: new InlineCommentWidget(
            comment,
            isEditing,
            async (content) => {
              if (updateComment) {
                const result = await updateComment(comment.id, content);
                if (result.success) {
                  setAllReviewComments(prev =>
                    prev.map(c => c.id === comment.id ? { ...c, content } : c)
                  );
                  setEditingCommentId(null);
                }
              }
            },
            () => setEditingCommentId(null),
            () => setEditingCommentId(comment.id),
            async (resolved) => {
              if (resolveComment) {
                const result = await resolveComment(comment.id, resolved);
                if (result.success) {
                  setAllReviewComments(prev =>
                    prev.map(c => c.id === comment.id ? { ...c, resolved } : c)
                  );
                }
              }
            }
          ),
        });
      }
    }

    // Add new comment widget if active
    if (newCommentLine !== null) {
      const pos = getLineEndPos(newCommentLine, diffContent.modified);
      if (pos >= 0) {
        inlineWidgets.push({
          pos,
          widget: new NewCommentWidget(
            newCommentLine,
            async (content) => {
              if (reviewId && selectedFilePath && addComment) {
                const result = await addComment(
                  reviewId,
                  selectedFilePath,
                  newCommentLine,
                  newCommentLine,
                  content
                );
                if (result.success && result.comment) {
                  setAllReviewComments(prev => [...prev, result.comment!]);
                  setNewCommentLine(null);
                }
              }
            },
            () => setNewCommentLine(null)
          ),
        });
      }
    }

    // Sort by position and create decoration set
    inlineWidgets.sort((a, b) => a.pos - b.pos);
    const inlineDecorations = Decoration.set(
      inlineWidgets.map(({ pos, widget }) =>
        Decoration.widget({ widget, block: true, side: 1 }).range(pos)
      )
    );

    const inlineCommentsExtension = EditorView.decorations.of(inlineDecorations);

    const extensions = [
      commentLinesField,
      commentGutter,
      clickHandler,
      inlineCommentsExtension,
      minimalSetup,
      langExtension,
      oneDark,
      diffTheme,
      EditorView.lineWrapping,
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

    // Initialize comment markers
    view.dispatch({
      effects: setCommentLines.of(commentLineMap),
    });

    editorViewRef.current = view;

    // Restore scroll position
    if (scrollPositionRef.current > 0) {
      view.scrollDOM.scrollTop = scrollPositionRef.current;
    }

    // Inject saved deleted-line comments into deleted chunks
    setTimeout(() => {
      if (!editorContainerRef.current) return;

      // Build a map of anchor line -> deleted chunk element
      const deletedChunks = editorContainerRef.current.querySelectorAll('.cm-deletedChunk');
      const anchorLineToChunk = new Map<number, Element>();

      deletedChunks.forEach((chunk) => {
        let prevLine = chunk.previousElementSibling;
        while (prevLine && !prevLine.classList.contains('cm-line')) {
          prevLine = prevLine.previousElementSibling;
        }
        if (prevLine && view) {
          try {
            const pos = view.posAtDOM(prevLine);
            const lineNum = view.state.doc.lineAt(pos).number;
            anchorLineToChunk.set(lineNum, chunk);
          } catch {
            // Ignore
          }
        }
      });

      // Inject saved comments for deleted lines
      deletedLineComments.forEach((comment) => {
        const chunk = anchorLineToChunk.get(comment.line_start);
        if (!chunk) return;

        // Check if already injected
        if (chunk.querySelector(`[data-comment-id="${comment.id}"]`)) return;

        // Create comment element
        const commentEl = document.createElement('div');
        commentEl.className = `inline-comment ${comment.resolved ? 'resolved' : ''}`;
        commentEl.dataset.commentId = comment.id;
        commentEl.innerHTML = `
          <div class="inline-comment-header">
            <span class="inline-comment-author ${comment.author}">${comment.author === 'agent' ? 'Claude' : 'You'}</span>
            <div class="inline-comment-actions">
              <button type="button" class="inline-comment-btn edit-btn">Edit</button>
              <button type="button" class="inline-comment-btn ${comment.resolved ? '' : 'resolve'} resolve-btn">${comment.resolved ? 'Unresolve' : 'Resolve'}</button>
            </div>
          </div>
          <div class="inline-comment-content">${comment.content.replace(/</g, '&lt;').replace(/>/g, '&gt;')}</div>
        `;

        // Wire up edit button
        commentEl.querySelector('.edit-btn')?.addEventListener('click', () => {
          setEditingCommentId(comment.id);
        });

        // Wire up resolve button
        const resolveBtn = commentEl.querySelector('.resolve-btn');
        resolveBtn?.addEventListener('click', async () => {
          if (resolveComment) {
            const newResolved = !comment.resolved;
            const resolveResult = await resolveComment(comment.id, newResolved);
            if (resolveResult.success) {
              setAllReviewComments(prev =>
                prev.map(c => c.id === comment.id ? { ...c, resolved: newResolved } : c)
              );
              if (newResolved) {
                commentEl.classList.add('resolved');
                resolveBtn.textContent = 'Unresolve';
                resolveBtn.classList.remove('resolve');
              } else {
                commentEl.classList.remove('resolved');
                resolveBtn.textContent = 'Resolve';
                resolveBtn.classList.add('resolve');
              }
            }
          }
        });

        // Insert after the specific deleted line (index encoded in line_end)
        // line_end = -(index + 1), so index = Math.abs(line_end) - 1
        const deletedLines = chunk.querySelectorAll('.cm-deletedLine');
        const deletedLineIndex = Math.abs(comment.line_end) - 1;
        const targetLine = deletedLines[deletedLineIndex] || deletedLines[deletedLines.length - 1];
        if (targetLine) {
          targetLine.after(commentEl);
        } else {
          chunk.appendChild(commentEl);
        }
      });
    }, 0);

    // Add document-level click handler for deleted chunks (they don't receive normal editor events)
    const handleDeletedChunkClick = (event: MouseEvent) => {
      const target = event.target as HTMLElement;
      const deletedChunk = target.closest('.cm-deletedChunk');
      if (!deletedChunk) return;

      // Only handle if click is within our editor container
      if (!editorContainerRef.current?.contains(target)) return;

      // Check if there's already a form in this chunk
      if (deletedChunk.querySelector('.inline-comment')) {
        return;
      }

      // Find the previous .cm-line sibling to anchor the comment
      let prevLine = deletedChunk.previousElementSibling;
      while (prevLine && !prevLine.classList.contains('cm-line')) {
        prevLine = prevLine.previousElementSibling;
      }

      // Get the anchor line number for storing the comment
      let anchorLineNum = 0;
      if (prevLine && view) {
        try {
          const pos = view.posAtDOM(prevLine);
          anchorLineNum = view.state.doc.lineAt(pos).number;
        } catch {
          // Ignore
        }
      }

      // Create and insert the comment form directly into the deleted chunk
      const form = document.createElement('div');
      form.className = 'inline-comment new';
      form.innerHTML = `
        <div class="inline-comment-form">
          <div class="inline-comment-label">Deleted content (after line ${anchorLineNum})</div>
          <textarea class="inline-comment-textarea" rows="3" placeholder="Add a comment..."></textarea>
          <div class="inline-comment-buttons">
            <button type="button" class="inline-comment-btn cancel-btn">Cancel</button>
            <button type="button" class="inline-comment-btn save save-btn">Save</button>
          </div>
        </div>
      `;

      // Find the deleted line that was clicked and its index within the chunk
      const clickedLine = target.closest('.cm-deletedLine');
      const allDeletedLines = deletedChunk.querySelectorAll('.cm-deletedLine');
      let clickedLineIndex = 0;
      if (clickedLine) {
        const foundIndex = Array.from(allDeletedLines).indexOf(clickedLine);
        clickedLineIndex = foundIndex >= 0 ? foundIndex : 0;
        clickedLine.after(form);
      } else {
        // Fallback: insert at beginning of chunk
        deletedChunk.insertBefore(form, deletedChunk.firstChild);
      }

      // Focus the textarea
      const textarea = form.querySelector('textarea');
      if (textarea) {
        setTimeout(() => textarea.focus(), 0);
      }

      // Handle cancel
      const cancelBtn = form.querySelector('.cancel-btn');
      cancelBtn?.addEventListener('click', (e) => {
        e.stopPropagation();
        form.remove();
      });

      // Handle save
      const saveBtn = form.querySelector('.save-btn');
      saveBtn?.addEventListener('click', async (e) => {
        e.stopPropagation();
        const content = textarea?.value.trim();
        if (content && reviewId && selectedFilePath && addComment) {
          // Use line_end = -(index + 1) to mark deleted-line comment and store position
          // -1 = after deleted line 0, -2 = after deleted line 1, etc.
          const result = await addComment(
            reviewId,
            selectedFilePath,
            anchorLineNum,
            -(clickedLineIndex + 1),  // Encode deleted line index
            content
          );
          if (result.success && result.comment) {
            // Add to state - the injection logic will handle positioning
            setAllReviewComments(prev => [...prev, result.comment!]);
            form.remove();
          }
        }
      });

      event.preventDefault();
      event.stopPropagation();
    };

    document.addEventListener('mousedown', handleDeletedChunkClick, true);

    return () => {
      document.removeEventListener('mousedown', handleDeletedChunkClick, true);
      view.destroy();
      editorViewRef.current = null;
    };
  }, [diffContent, selectedFile?.path, expandedContext, fontSize, regularComments, deletedLineComments, newCommentLine, editingCommentId, reviewId, selectedFilePath, addComment, updateComment, resolveComment]);

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
                    className={`file-item ${selectedFilePath === file.path ? 'selected' : ''} ${viewedFiles.has(file.path) ? 'viewed' : ''} ${changedSinceViewed.has(file.path) ? 'changed' : ''}`}
                    onClick={() => setSelectedFilePath(file.path)}
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
                    {fileCommentCounts[file.path] > 0 && (
                      <span className="file-comment-count">{fileCommentCounts[file.path]}</span>
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
                    className={`file-item auto-skip ${selectedFilePath === file.path ? 'selected' : ''}`}
                    onClick={() => setSelectedFilePath(file.path)}
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
