/**
 * DiffView — thin wrapper around @pierre/diffs (diffs.com) that renders a single
 * file's diff and wires attn's review-comment workflow into the library's native
 * primitives.
 *
 * The library computes the diff internally from `original`/`modified` file
 * contents, handles syntax highlighting (Shiki), unified/split layout, and hunk
 * collapsing/expansion. We add:
 *   - comment threads + a draft form via the native `lineAnnotations` /
 *     `renderAnnotation` slots,
 *   - the gutter hover "+" opens a comment draft directly on that line
 *     (`onGutterUtilityClick`),
 *   - clicking anywhere on a line — or selecting a range of line numbers —
 *     opens an action popup (Add comment / Send to Claude) via `onLineClick`
 *     and `enableLineSelection` + `onLineSelectionEnd`.
 *
 * Comment <-> annotation convention (unchanged protocol): a comment's
 * `line_end < 0` encodes the original/deleted side; `line_start` is the anchor
 * line on that side; `abs(line_end)` is the range end.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  MultiFileDiff,
  Virtualizer,
  useStableCallback,
  type AnnotationSide,
  type DiffLineAnnotation,
  type FileContents,
  type SelectedLineRange,
} from '@pierre/diffs/react';
// FileDiffOptions / OnDiffLineClickProps are exported from the package root, not the /react entry.
import type { FileDiffOptions, OnDiffLineClickProps } from '@pierre/diffs';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { ResolvedTheme } from '../hooks/useTheme';
import type { ReviewComment } from '../types/generated';
import { buildLineRef, commentLineRef, isOriginalSideComment } from '../utils/reviewComment';
import { hashContent } from '../utils/reviewHash';
import { ClaudeIcon } from './icons/ClaudeIcon';
import { DiffCommentThread } from './DiffCommentThread';

// Metadata carried on each native line annotation: every saved comment sharing
// the same (side, anchor line) is grouped into one thread, plus an optional
// in-progress draft form on that same anchor.
interface AnnotationMeta {
  side: AnnotationSide;
  lineNumber: number;
  comments: ReviewComment[];
  draft: boolean;
}

type DraftState = {
  side: AnnotationSide;
  start: number;
  end: number;
};

type SelectionPopupState = {
  side: AnnotationSide;
  start: number;
  end: number;
  x: number;
  y: number;
};

export interface DiffViewProps {
  original: string;
  modified: string;
  filePath?: string;
  comments: ReviewComment[];
  editingCommentId: string | null;
  resolvedTheme?: ResolvedTheme;
  diffStyle: 'unified' | 'split';
  /** false = hunks (collapse unchanged), true = full file. */
  expandUnchanged: boolean;
  /** Diff code font size in px; drives the `--diffs-font-size` CSS variable. */
  fontSize?: number;
  onAddComment: (lineStart: number, lineEnd: number, content: string) => Promise<void> | void;
  onEditComment: (id: string, content: string) => Promise<void> | void;
  onStartEdit: (id: string) => void;
  onCancelEdit: () => void;
  onResolveComment: (id: string, resolved: boolean) => Promise<void> | void;
  onDeleteComment: (id: string) => Promise<void> | void;
  onSendToClaude?: (reference: string) => void;
}

/** Stable no-op for the stale-comment thread, which never hosts a draft form. */
const noop = () => {};

function normalizeRange(range: SelectedLineRange): { side: AnnotationSide; start: number; end: number } {
  const start = Math.min(range.start, range.end);
  const end = Math.max(range.start, range.end);
  const side = range.side ?? range.endSide ?? 'additions';
  return { side, start, end };
}

export function DiffView({
  original,
  modified,
  filePath,
  comments,
  editingCommentId,
  resolvedTheme = 'dark',
  diffStyle,
  expandUnchanged,
  fontSize,
  onAddComment,
  onEditComment,
  onStartEdit,
  onCancelEdit,
  onResolveComment,
  onDeleteComment,
  onSendToClaude,
}: DiffViewProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const pointerRef = useRef({ x: 0, y: 0 });
  // The library commits a trailing line-selection on the same pointerup that
  // fires onGutterUtilityClick. The "+" should open only the draft, so swallow
  // that one selection-end instead of letting it pop the action menu too.
  const suppressSelectionEndRef = useRef(false);

  const [draft, setDraft] = useState<DraftState | null>(null);
  const [selectionPopup, setSelectionPopup] = useState<SelectionPopupState | null>(null);
  // Stale comments (anchor line no longer in the diff) are collapsed by default.
  const [staleExpanded, setStaleExpanded] = useState(false);

  const name = filePath ?? 'file.txt';

  // Is the user actively typing a comment on THIS file — a new-comment draft, or
  // an edit of one of this file's comments?
  const editingHere = useMemo(
    () => editingCommentId != null && comments.some((c) => c.id === editingCommentId),
    [editingCommentId, comments]
  );
  const formOpen = draft !== null || editingHere;

  // Freeze the diff content while a comment form is open. @pierre/diffs binds a
  // rendered instance to its first file and won't swap the target in place
  // (VirtualizedFileDiff.render keeps `this.fileDiff` via `??=`), so we remount
  // the diff whenever its content changes (see `diffKey`). That remount discards
  // any in-progress comment — and the file under review changes constantly while
  // the agent edits it. Buffering the latest content while a form is open keeps
  // the diff (and the form) steady; we adopt the newest content the moment the
  // form closes or the user switches files.
  const [frozen, setFrozen] = useState<{ original: string; modified: string } | null>(null);
  useEffect(() => {
    setFrozen((current) => (formOpen ? current ?? { original, modified } : null));
  }, [formOpen, original, modified]);

  const shownOriginal = frozen?.original ?? original;
  const shownModified = frozen?.modified ?? modified;

  // A draft/selection belongs to one file; reset (and drop any freeze) on switch.
  useEffect(() => {
    setDraft(null);
    setSelectionPopup(null);
    setFrozen(null);
    setStaleExpanded(false);
  }, [filePath]);

  // A comment is "stale" when its anchor line is no longer in the file (the code
  // shrank past it). Such a comment can't be slotted onto a line, so it would
  // silently vanish — instead we surface it in a collapsed banner. Comments
  // whose line still exists render inline as usual (even if the code drifted;
  // we can't detect that without the original line text).
  const lineCounts = useMemo(
    () => ({ additions: shownModified.split('\n').length, deletions: shownOriginal.split('\n').length }),
    [shownModified, shownOriginal]
  );
  const { anchoredComments, staleComments } = useMemo(() => {
    const anchored: ReviewComment[] = [];
    const stale: ReviewComment[] = [];
    for (const c of comments) {
      const max = isOriginalSideComment(c) ? lineCounts.deletions : lineCounts.additions;
      (c.line_start >= 1 && c.line_start <= max ? anchored : stale).push(c);
    }
    return { anchoredComments: anchored, staleComments: stale };
  }, [comments, lineCounts]);

  const oldFile = useMemo<FileContents>(() => ({ name, contents: shownOriginal }), [name, shownOriginal]);
  const newFile = useMemo<FileContents>(() => ({ name, contents: shownModified }), [name, shownModified]);

  // Remount the diff whenever the shown target (path or content) changes — the
  // library's intended way to switch files. While frozen, the shown content is
  // held constant, so an incoming change to the current file can't remount it.
  const diffKey = useMemo(
    () => `${name}:${hashContent(shownOriginal)}:${hashContent(shownModified)}`,
    [name, shownOriginal, shownModified]
  );

  // The library forces a full re-render whenever the options object changes by
  // value (function identities included), so keep callbacks stable and memoize
  // the options object on the inputs that should actually retrigger a render.
  const handleGutterUtilityClick = useStableCallback((range: SelectedLineRange) => {
    const { side, start, end } = normalizeRange(range);
    suppressSelectionEndRef.current = true;
    setSelectionPopup(null);
    setDraft({ side, start, end });
  });

  const handleLineSelectionEnd = useStableCallback((range: SelectedLineRange | null) => {
    if (suppressSelectionEndRef.current) {
      suppressSelectionEndRef.current = false;
      return;
    }
    if (!range) {
      setSelectionPopup(null);
      return;
    }
    const { side, start, end } = normalizeRange(range);
    setSelectionPopup({ side, start, end, x: pointerRef.current.x, y: pointerRef.current.y });
  });

  // A plain click anywhere on a line opens the action popup on that single line.
  // (Clicks on the gutter "+" are filtered out by the library before this fires;
  // clicks inside a comment thread don't resolve to a line target.)
  const handleLineClick = useStableCallback((props: OnDiffLineClickProps) => {
    const rect = wrapperRef.current?.getBoundingClientRect();
    const x = rect ? props.event.clientX - rect.left : 0;
    const y = rect ? props.event.clientY - rect.top : 0;
    setSelectionPopup({ side: props.annotationSide, start: props.lineNumber, end: props.lineNumber, x, y });
  });

  const options = useMemo<FileDiffOptions<AnnotationMeta>>(() => ({
    diffStyle,
    expandUnchanged,
    diffIndicators: 'classic',
    theme: { dark: 'pierre-dark', light: 'pierre-light' },
    themeType: resolvedTheme,
    // Use the pure-JS Shiki engine: avoids loading a WASM binary inside the
    // Tauri webview (custom protocol + CSP make WASM fetching unreliable).
    preferredHighlighter: 'shiki-js',
    // Dragging across line numbers selects a range; a plain click on a line
    // opens the action popup on that single line (onLineClick).
    enableLineSelection: true,
    onLineSelectionEnd: handleLineSelectionEnd,
    onLineClick: handleLineClick,
    // Render the native hover "+" in the line-number gutter; clicking it opens a
    // draft on that line. Without this the onGutterUtilityClick handler is dead
    // (the library gates the button behind enableGutterUtility, default false).
    enableGutterUtility: true,
    onGutterUtilityClick: handleGutterUtilityClick,
  }), [diffStyle, expandUnchanged, resolvedTheme, handleLineSelectionEnd, handleLineClick, handleGutterUtilityClick]);

  // Group saved comments + the optional draft into one annotation per
  // (side, anchor line). The library slots annotations by `side`+`lineNumber`,
  // so collisions must be merged into a single thread.
  const lineAnnotations = useMemo<DiffLineAnnotation<AnnotationMeta>[]>(() => {
    const groups = new Map<string, AnnotationMeta>();
    const keyOf = (side: AnnotationSide, line: number) => `${side}:${line}`;

    for (const comment of anchoredComments) {
      const side: AnnotationSide = isOriginalSideComment(comment) ? 'deletions' : 'additions';
      const line = comment.line_start;
      const key = keyOf(side, line);
      let group = groups.get(key);
      if (!group) {
        group = { side, lineNumber: line, comments: [], draft: false };
        groups.set(key, group);
      }
      group.comments.push(comment);
    }

    if (draft) {
      const key = keyOf(draft.side, draft.start);
      let group = groups.get(key);
      if (!group) {
        group = { side: draft.side, lineNumber: draft.start, comments: [], draft: true };
        groups.set(key, group);
      } else {
        group.draft = true;
      }
    }

    // The library keys annotation slots by array index (renderDiffChildren maps
    // with the index as the React key), so an annotation's index must stay stable
    // or React remounts its subtree — which would wipe an in-progress draft/edit
    // form (its typed text and focus). Keep the annotation(s) with an open form at
    // the front so that comments arriving or leaving in the background only ever
    // shift the trailing, form-less threads. Visual placement is unaffected: the
    // library positions each thread by its `slot` (side+line), not array order.
    const all = Array.from(groups.values());
    const hasOpenForm = (g: AnnotationMeta) =>
      g.draft || g.comments.some((c) => c.id === editingCommentId);
    const active = all.filter(hasOpenForm);
    const rest = all.filter((g) => !hasOpenForm(g));
    return [...active, ...rest].map((meta) => ({
      side: meta.side,
      lineNumber: meta.lineNumber,
      metadata: meta,
    }));
  }, [anchoredComments, draft, editingCommentId]);

  const handleSaveDraft = useCallback(
    (content: string) => {
      if (!draft) return;
      const lineStart = draft.start;
      const lineEnd = draft.side === 'deletions' ? -draft.end : draft.end;
      void onAddComment(lineStart, lineEnd, content);
      setDraft(null);
    },
    [draft, onAddComment]
  );

  const handleCancelDraft = useCallback(() => setDraft(null), []);

  const handleSendComment = useCallback(
    (comment: ReviewComment) => {
      if (!onSendToClaude || !filePath) return;
      onSendToClaude(`@${filePath}:${commentLineRef(comment)}\nComment: ${comment.content}`);
    },
    [onSendToClaude, filePath]
  );

  const renderAnnotation = useCallback(
    (annotation: DiffLineAnnotation<AnnotationMeta>) => {
      const meta = annotation.metadata;
      return (
        <DiffCommentThread
          comments={meta.comments}
          draft={meta.draft}
          editingCommentId={editingCommentId}
          showSendToClaude={!!onSendToClaude && !!filePath}
          onSaveDraft={handleSaveDraft}
          onCancelDraft={handleCancelDraft}
          onStartEdit={onStartEdit}
          onEditComment={onEditComment}
          onCancelEdit={onCancelEdit}
          onResolveComment={onResolveComment}
          onDeleteComment={onDeleteComment}
          onSendComment={handleSendComment}
        />
      );
    },
    [
      editingCommentId,
      onSendToClaude,
      filePath,
      handleSaveDraft,
      handleCancelDraft,
      onStartEdit,
      onEditComment,
      onCancelEdit,
      onResolveComment,
      onDeleteComment,
      handleSendComment,
    ]
  );

  // Selection popup actions
  const addCommentFromSelection = useCallback(() => {
    if (!selectionPopup) return;
    const { side, start, end } = selectionPopup;
    setDraft({ side, start, end });
    setSelectionPopup(null);
  }, [selectionPopup]);

  const sendSelectionToClaude = useCallback(() => {
    if (!selectionPopup || !onSendToClaude || !filePath) return;
    const { start, end } = selectionPopup;
    onSendToClaude(`@${filePath}:${buildLineRef(start, end)}`);
    setSelectionPopup(null);
  }, [selectionPopup, onSendToClaude, filePath]);

  // Escape closes the draft form / selection popup before the panel (LIFO).
  useEscapeStack(handleCancelDraft, draft !== null);
  useEscapeStack(onCancelEdit, editingCommentId !== null);
  useEscapeStack(() => setSelectionPopup(null), selectionPopup !== null);

  const capturePointer = useCallback((e: React.PointerEvent) => {
    const rect = wrapperRef.current?.getBoundingClientRect();
    if (rect) {
      pointerRef.current = { x: e.clientX - rect.left, y: e.clientY - rect.top };
    }
  }, []);

  // A pointerdown anywhere dismisses a stale selection popup — except when it
  // lands on the popup itself, otherwise this capture-phase handler would clear
  // the popup before its own buttons' click handlers run (the popup is a child
  // of this wrapper, so capture reaches here first).
  const dismissPopupOnPointerDown = useCallback((e: React.PointerEvent) => {
    if ((e.target as HTMLElement).closest('.diff-selection-popup')) return;
    setSelectionPopup(null);
  }, []);

  return (
    <div
      className="diff-view"
      data-testid="diff-view"
      ref={wrapperRef}
      style={{
        position: 'relative',
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        width: '100%',
        ...(fontSize ? { '--diffs-font-size': `${fontSize}px` } : {}),
      } as React.CSSProperties}
      onPointerUpCapture={capturePointer}
      onPointerDownCapture={dismissPopupOnPointerDown}
    >
      {staleComments.length > 0 && (
        <div className="diff-stale-comments">
          <button
            type="button"
            className="diff-stale-comments-toggle"
            data-testid="diff-stale-comments-toggle"
            aria-expanded={staleExpanded}
            onClick={() => setStaleExpanded((v) => !v)}
          >
            <span className="diff-stale-caret" aria-hidden="true">{staleExpanded ? '▾' : '▸'}</span>
            {staleComments.length} comment{staleComments.length === 1 ? '' : 's'} no longer anchored to the current code
          </button>
          {staleExpanded && (
            <div className="diff-stale-comments-body">
              <DiffCommentThread
                comments={staleComments}
                draft={false}
                editingCommentId={editingCommentId}
                showSendToClaude={!!onSendToClaude && !!filePath}
                onSaveDraft={noop}
                onCancelDraft={noop}
                onStartEdit={onStartEdit}
                onEditComment={onEditComment}
                onCancelEdit={onCancelEdit}
                onResolveComment={onResolveComment}
                onDeleteComment={onDeleteComment}
                onSendComment={handleSendComment}
              />
            </div>
          )}
        </div>
      )}
      <Virtualizer className="diff-view-scroller" style={{ flex: 1, minHeight: 0, overflow: 'auto' }}>
        <MultiFileDiff<AnnotationMeta>
          key={diffKey}
          oldFile={oldFile}
          newFile={newFile}
          options={options}
          lineAnnotations={lineAnnotations}
          renderAnnotation={renderAnnotation}
          disableWorkerPool
        />
      </Virtualizer>

      {selectionPopup && (
        <div
          className="diff-selection-popup"
          style={{ top: Math.max(0, selectionPopup.y - 40), left: selectionPopup.x }}
        >
          {onSendToClaude && filePath && (
            <button
              className="diff-selection-popup-btn send"
              title="Send to Claude Code"
              onClick={sendSelectionToClaude}
            >
              <ClaudeIcon size={16} />
            </button>
          )}
          <button
            className="diff-selection-popup-btn comment"
            title="Add comment"
            onClick={addCommentFromSelection}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
            </svg>
          </button>
        </div>
      )}
    </div>
  );
}

export default DiffView;
