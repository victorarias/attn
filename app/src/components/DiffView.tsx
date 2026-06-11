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
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  FileDiff,
  MultiFileDiff,
  Virtualizer,
  useStableCallback,
  type AnnotationSide,
  type DiffLineAnnotation,
  type FileContents,
  type SelectedLineRange,
} from '@pierre/diffs/react';
// FileDiffOptions / OnDiffLineClickProps are exported from the package root, not the /react entry.
import {
  parseDiffFromFile,
  type FileDiffMetadata,
  type FileDiffOptions,
  type OnDiffLineClickProps,
} from '@pierre/diffs';
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
  agentLabel?: string;
  /** Render bounded source context so comments outside changed hunks stay inline. */
  revealCommentContext?: boolean;
  getCommentCapabilities?: (comment: ReviewComment) => {
    edit: boolean;
    resolve: boolean;
    delete: boolean;
  };
}

/** Stable no-op for the top-banner comment thread, which never hosts a draft form. */
const noop = () => {};

type VisibleLineRanges = Record<AnnotationSide, Array<[number, number]>>;

export interface CommentContextBlock {
  start: number;
  end: number;
  comments: ReviewComment[];
  fileDiff: FileDiffMetadata;
}

export function normalizeRange(range: SelectedLineRange): { side: AnnotationSide; start: number; end: number } | null {
  const side = range.side ?? range.endSide ?? 'additions';
  const endSide = range.endSide ?? side;
  if (side !== endSide) return null;
  const start = Math.min(range.start, range.end);
  const end = Math.max(range.start, range.end);
  return { side, start, end };
}

function isLineInRanges(line: number, ranges: Array<[number, number]>): boolean {
  return ranges.some(([start, end]) => line >= start && line <= end);
}

function getVisibleLineRanges(
  diff: FileDiffMetadata | null,
  expandUnchanged: boolean
): VisibleLineRanges | null {
  if (expandUnchanged || !diff) return null;
  return diff.hunks.reduce<VisibleLineRanges>(
    (ranges, hunk) => {
      ranges.deletions.push([hunk.deletionStart, hunk.deletionStart + hunk.deletionCount - 1]);
      ranges.additions.push([hunk.additionStart, hunk.additionStart + hunk.additionCount - 1]);
      return ranges;
    },
    { additions: [], deletions: [] }
  );
}

export function buildCommentContextBlocks(
  fileName: string,
  source: string,
  comments: ReviewComment[],
  contextLines = 3
): CommentContextBlock[] {
  const sourceLines = source.split('\n');
  const spans = comments
    .map((comment) => ({
      start: Math.max(1, comment.line_start - contextLines),
      end: Math.min(sourceLines.length, comment.line_start + contextLines),
      comment,
    }))
    .filter((span) => span.start <= span.end)
    .sort((left, right) => left.start - right.start || left.end - right.end);
  const groups: Array<{ start: number; end: number; comments: ReviewComment[] }> = [];

  for (const span of spans) {
    const previous = groups[groups.length - 1];
    if (previous && span.start <= previous.end + 1) {
      previous.end = Math.max(previous.end, span.end);
      previous.comments.push(span.comment);
    } else {
      groups.push({ start: span.start, end: span.end, comments: [span.comment] });
    }
  }

  return groups.map((group) => {
    const lines = sourceLines.slice(group.start - 1, group.end);
    const lineCount = lines.length;
    const diffLines = lines.map((line) => `${line}\n`);
    return {
      ...group,
      fileDiff: {
        name: fileName,
        type: 'change',
        hunks: [{
          collapsedBefore: 0,
          additionStart: group.start,
          additionCount: lineCount,
          additionLines: 0,
          additionLineIndex: 0,
          deletionStart: group.start,
          deletionCount: lineCount,
          deletionLines: 0,
          deletionLineIndex: 0,
          hunkContent: [{
            type: 'context',
            lines: lineCount,
            additionLineIndex: 0,
            deletionLineIndex: 0,
          }],
          hunkSpecs: `@@ -${group.start},${lineCount} +${group.start},${lineCount} @@\n`,
          splitLineStart: 0,
          splitLineCount: lineCount,
          unifiedLineStart: 0,
          unifiedLineCount: lineCount,
          noEOFCRAdditions: false,
          noEOFCRDeletions: false,
        }],
        splitLineCount: lineCount,
        unifiedLineCount: lineCount,
        isPartial: true,
        deletionLines: diffLines,
        additionLines: diffLines,
      },
    };
  });
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
  agentLabel,
  revealCommentContext = false,
  getCommentCapabilities,
}: DiffViewProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const pointerRef = useRef({ x: 0, y: 0 });
  const draftContentByFileRef = useRef<Record<string, string>>({});
  const scrollPositionsByFileRef = useRef<Record<string, { top: number; left: number }>>({});
  const activeFileNameRef = useRef(filePath ?? 'file.txt');
  // The library commits a trailing line-selection on the same pointerup that
  // fires onGutterUtilityClick. The "+" should open only the draft, so swallow
  // that one selection-end instead of letting it pop the action menu too.
  const suppressSelectionEndRef = useRef(false);

  const [draftsByFile, setDraftsByFile] = useState<Record<string, DraftState>>({});
  const [selectionPopup, setSelectionPopup] = useState<SelectionPopupState | null>(null);
  // Comments that cannot render inline are collapsed by default.
  const [staleExpanded, setStaleExpanded] = useState(false);

  const name = filePath ?? 'file.txt';
  activeFileNameRef.current = name;
  const draft = draftsByFile[name] ?? null;

  const openDraftForCurrentFile = useCallback(
    (next: DraftState) => {
      draftContentByFileRef.current[name] = '';
      setDraftsByFile((current) => ({ ...current, [name]: next }));
    },
    [name]
  );

  const clearDraftForCurrentFile = useCallback(
    () => {
      delete draftContentByFileRef.current[name];
      setDraftsByFile((current) => {
        const { [name]: _removed, ...rest } = current;
        return rest;
      });
    },
    [name]
  );

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

  // Selection/frozen content belong to one file; draft anchors and text are keyed
  // by file so navigating away and back does not destroy an unsaved comment.
  useEffect(() => {
    setSelectionPopup(null);
    setFrozen(null);
    setStaleExpanded(false);
  }, [filePath]);

  const oldFile = useMemo<FileContents>(() => ({ name, contents: shownOriginal }), [name, shownOriginal]);
  const newFile = useMemo<FileContents>(() => ({ name, contents: shownModified }), [name, shownModified]);
  const parsedDiff = useMemo(() => {
    try {
      return parseDiffFromFile(oldFile, newFile);
    } catch {
      return null;
    }
  }, [newFile, oldFile]);
  const lineCounts = useMemo(
    () => ({ additions: shownModified.split('\n').length, deletions: shownOriginal.split('\n').length }),
    [shownModified, shownOriginal]
  );

  const visibleLineRanges = useMemo(
    () => getVisibleLineRanges(parsedDiff, expandUnchanged),
    [expandUnchanged, parsedDiff]
  );

  // A comment cannot render inline when its anchor line no longer exists or when
  // Hunks mode collapses that unchanged line. The diff library silently drops
  // annotations for non-rendered lines, so surface them in a collapsed banner.
  const { anchoredComments, contextComments, staleComments } = useMemo(() => {
    const anchored: ReviewComment[] = [];
    const context: ReviewComment[] = [];
    const stale: ReviewComment[] = [];
    for (const c of comments) {
      const side: AnnotationSide = isOriginalSideComment(c) ? 'deletions' : 'additions';
      const max = side === 'deletions' ? lineCounts.deletions : lineCounts.additions;
      const lineExists = c.line_start >= 1 && c.line_start <= max;
      const lineVisible = !visibleLineRanges || isLineInRanges(c.line_start, visibleLineRanges[side]);
      if (lineExists && lineVisible) {
        anchored.push(c);
      } else if (lineExists && revealCommentContext && side === 'additions') {
        context.push(c);
      } else {
        stale.push(c);
      }
    }
    return { anchoredComments: anchored, contextComments: context, staleComments: stale };
  }, [comments, lineCounts, revealCommentContext, visibleLineRanges]);
  const commentContextBlocks = useMemo(
    () => buildCommentContextBlocks(name, shownModified, contextComments),
    [contextComments, name, shownModified]
  );

  // Remount the diff whenever the shown target (path or content) changes — the
  // library's intended way to switch files. While frozen, the shown content is
  // held constant, so an incoming change to the current file can't remount it.
  const diffKey = useMemo(
    () => `${name}:${hashContent(shownOriginal)}:${hashContent(shownModified)}`,
    [name, shownOriginal, shownModified]
  );

  useLayoutEffect(() => {
    const scroller = wrapperRef.current?.querySelector<HTMLElement>('.diff-view-scroller');
    if (!scroller) return;
    const rememberScroll = () => {
      scrollPositionsByFileRef.current[activeFileNameRef.current] = {
        top: scroller.scrollTop,
        left: scroller.scrollLeft,
      };
    };
    scroller.addEventListener('scroll', rememberScroll, { passive: true });
    return () => {
      rememberScroll();
      scroller.removeEventListener('scroll', rememberScroll);
    };
  }, []);

  // The library forces a full re-render whenever the options object changes by
  // value (function identities included), so keep callbacks stable and memoize
  // the options object on the inputs that should actually retrigger a render.
  const handleGutterUtilityClick = useStableCallback((range: SelectedLineRange) => {
    const normalized = normalizeRange(range);
    if (!normalized) return;
    const { side, start, end } = normalized;
    suppressSelectionEndRef.current = true;
    setSelectionPopup(null);
    openDraftForCurrentFile({ side, start, end });
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
    const normalized = normalizeRange(range);
    if (!normalized) {
      setSelectionPopup(null);
      return;
    }
    const { side, start, end } = normalized;
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

  const restoreScrollPosition = useStableCallback(() => {
    const position = scrollPositionsByFileRef.current[name];
    if (!position) return;
    const scroller = wrapperRef.current?.querySelector<HTMLElement>('.diff-view-scroller');
    if (!scroller) return;
    scroller.scrollTop = position.top;
    scroller.scrollLeft = position.left;
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
    onPostRender: (_node, _instance, phase) => {
      if (phase !== 'unmount') restoreScrollPosition();
    },
  }), [
    diffStyle,
    expandUnchanged,
    resolvedTheme,
    handleLineSelectionEnd,
    handleLineClick,
    handleGutterUtilityClick,
    restoreScrollPosition,
  ]);
  const contextOptions = useMemo<FileDiffOptions<AnnotationMeta>>(() => ({
    ...options,
    disableFileHeader: true,
    expandUnchanged: false,
    onPostRender: undefined,
  }), [options]);

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

    const draftInContext = draft?.side === 'additions'
      && commentContextBlocks.some((block) => draft.start >= block.start && draft.start <= block.end);
    if (draft && !draftInContext) {
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
  }, [anchoredComments, commentContextBlocks, draft, editingCommentId]);

  const contextBlockAnnotations = useMemo(
    () => commentContextBlocks.map((block) => {
      const groups = new Map<number, AnnotationMeta>();
      for (const comment of block.comments) {
        const lineNumber = comment.line_start;
        const group = groups.get(lineNumber) ?? {
          side: 'additions' as const,
          lineNumber,
          comments: [],
          draft: false,
        };
        group.comments.push(comment);
        groups.set(lineNumber, group);
      }
      if (
        draft?.side === 'additions'
        && draft.start >= block.start
        && draft.start <= block.end
      ) {
        const group = groups.get(draft.start) ?? {
          side: 'additions' as const,
          lineNumber: draft.start,
          comments: [],
          draft: false,
        };
        group.draft = true;
        groups.set(draft.start, group);
      }
      return {
        block,
        annotations: [...groups.values()].map((metadata) => ({
          side: metadata.side,
          lineNumber: metadata.lineNumber,
          metadata,
        })),
      };
    }),
    [commentContextBlocks, draft]
  );

  const handleSaveDraft = useCallback(
    async (content: string) => {
      if (!draft) return;
      const lineStart = draft.start;
      const lineEnd = draft.side === 'deletions' ? -draft.end : draft.end;
      try {
        await onAddComment(lineStart, lineEnd, content);
        clearDraftForCurrentFile();
      } catch {
        // The parent owns user-visible error reporting; keep the draft intact so
        // the user can retry without losing typed text.
      }
    },
    [clearDraftForCurrentFile, draft, onAddComment]
  );

  const handleDraftContentChange = useCallback(
    (content: string) => {
      draftContentByFileRef.current[name] = content;
    },
    [name]
  );

  const handleCancelDraft = clearDraftForCurrentFile;

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
          draftContent={meta.draft ? draftContentByFileRef.current[name] : undefined}
          onDraftContentChange={meta.draft ? handleDraftContentChange : undefined}
          onSaveDraft={handleSaveDraft}
          onCancelDraft={handleCancelDraft}
          onStartEdit={onStartEdit}
          onEditComment={onEditComment}
          onCancelEdit={onCancelEdit}
          onResolveComment={onResolveComment}
          onDeleteComment={onDeleteComment}
          onSendComment={handleSendComment}
          agentLabel={agentLabel}
          getCommentCapabilities={getCommentCapabilities}
        />
      );
    },
    [
      editingCommentId,
      onSendToClaude,
      filePath,
      handleSaveDraft,
      handleDraftContentChange,
      name,
      handleCancelDraft,
      onStartEdit,
      onEditComment,
      onCancelEdit,
      onResolveComment,
      onDeleteComment,
      handleSendComment,
      agentLabel,
      getCommentCapabilities,
    ]
  );

  // Selection popup actions
  const addCommentFromSelection = useCallback(() => {
    if (!selectionPopup) return;
    const { side, start, end } = selectionPopup;
    openDraftForCurrentFile({ side, start, end });
    setSelectionPopup(null);
  }, [selectionPopup, openDraftForCurrentFile]);

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
            {staleComments.length} comment{staleComments.length === 1 ? '' : 's'} not visible in the current diff view
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
                agentLabel={agentLabel}
                getCommentCapabilities={getCommentCapabilities}
              />
            </div>
          )}
        </div>
      )}
      <Virtualizer className="diff-view-scroller" style={{ flex: 1, minHeight: 0, overflow: 'auto' }}>
        {contextBlockAnnotations.map(({ block, annotations }) => (
          <section className="diff-comment-context-block" key={`${block.start}:${block.end}`}>
            <header>Authored context / lines {block.start}-{block.end}</header>
            <FileDiff<AnnotationMeta>
              fileDiff={block.fileDiff}
              options={contextOptions}
              lineAnnotations={annotations}
              renderAnnotation={renderAnnotation}
              disableWorkerPool
            />
          </section>
        ))}
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
