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
 *   - clicking a line-number cell, or dragging across several, opens a
 *     comment draft directly on that line/range (`enableLineSelection` +
 *     `onLineSelectionEnd`) — a bare click on the number column is a
 *     zero-length selection as far as the library is concerned, so it reports
 *     through the same callback as a drag; we treat that as intentional (one
 *     fewer click than a popup) rather than something to filter out. Clicking
 *     the code area of a line, outside the number column, does nothing.
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
// FileDiffOptions is exported from the package root, not the /react entry.
import { parseDiffFromFile, type FileDiffOptions } from '@pierre/diffs';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { ResolvedTheme } from '../hooks/useTheme';
import type { ReviewComment } from '../types/generated';
import { commentLineRef, isOriginalSideComment } from '../utils/reviewComment';
import { hashContent } from '../utils/reviewHash';
import { DiffCommentThread } from './DiffCommentThread';
import './DiffView.css';

// Metadata carried on each native line annotation: every saved comment sharing
// the same (side, anchor line) is grouped into one thread, plus an optional
// in-progress draft form on that same anchor.
interface AnnotationMeta {
  side: AnnotationSide;
  lineNumber: number;
  comments: ReviewComment[];
  draft: boolean;
  /** `${side}:${startLine}` — looks up this group's own draft in `draftsByFile`. */
  anchorKey: string;
}

type DraftState = {
  side: AnnotationSide;
  start: number;
  end: number;
  content: string;
};

/** Draft anchor key: a (side, start line) pair identifies one open comment box. */
function anchorKeyOf(side: AnnotationSide, start: number): string {
  return `${side}:${start}`;
}

/** Stable empty-record reference so `draftsByFile[name] ?? EMPTY_DRAFTS` doesn't
 * allocate a fresh object identity for files with no open drafts. */
const EMPTY_DRAFTS: Record<string, DraftState> = {};

export interface DiffViewProps {
  original: string;
  modified: string;
  filePath?: string;
  comments: ReviewComment[];
  editingCommentId: string | null;
  /** Comment ids to render without Edit/Resolve/Delete actions (already-submitted, non-draft comments). */
  readOnlyCommentIds?: Set<string>;
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

/** Stable no-op for the top-banner comment thread, which never hosts a draft form. */
const noop = () => {};

type VisibleLineRanges = Record<AnnotationSide, Array<[number, number]>>;

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
  oldFile: FileContents,
  newFile: FileContents,
  expandUnchanged: boolean
): VisibleLineRanges | null {
  if (expandUnchanged) return null;
  try {
    const diff = parseDiffFromFile(oldFile, newFile);
    return diff.hunks.reduce<VisibleLineRanges>(
      (ranges, hunk) => {
        ranges.deletions.push([hunk.deletionStart, hunk.deletionStart + hunk.deletionCount - 1]);
        ranges.additions.push([hunk.additionStart, hunk.additionStart + hunk.additionCount - 1]);
        return ranges;
      },
      { additions: [], deletions: [] }
    );
  } catch {
    return null;
  }
}

export function DiffView({
  original,
  modified,
  filePath,
  comments,
  editingCommentId,
  readOnlyCommentIds,
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
  // The library commits a trailing line-selection on the same pointerup that
  // fires onGutterUtilityClick. The "+" should open only the draft, so swallow
  // that one selection-end instead of letting it also open a second draft.
  const suppressSelectionEndRef = useRef(false);

  // On a cold present-window load, this diff can first render inside a hidden
  // or throttled webview. @pierre/diffs' Virtualizer captures its scroll
  // anchor against unmeasured row geometry during that throttled window, then
  // autonomously scrolls `.diff-view-scroller` to garbage positions (observed
  // in the wild: scrollTo 8792 -> clamp 8265 -> 5565) before the user has
  // touched the page at all. The first real click then forces a re-measure
  // that remaps content under that now-stale scrollTop, producing a visible
  // jump and a click that resolves against the wrong row entirely. Warm loads
  // never exhibit this — the library only mis-measures against a
  // hidden/throttled layout.
  //
  // Pin the scroller to the top until the user deliberately scrolls it
  // themselves; once real input arrives, get out of the way for good. Empty
  // dep array: this arms once per DiffView mount and must NOT re-arm on file
  // switch or diffKey change, or a mid-review file switch would yank the
  // viewport back to the top.
  useEffect(() => {
    const scroller = wrapperRef.current?.querySelector<HTMLElement>('.diff-view-scroller');
    if (!scroller) return;
    let userTookOver = false;
    const takeover = () => {
      userTookOver = true;
    };
    const onScroll = () => {
      if (!userTookOver && scroller.scrollTop !== 0) scroller.scrollTop = 0;
    };
    scroller.addEventListener('wheel', takeover, { passive: true });
    scroller.addEventListener('touchstart', takeover, { passive: true });
    scroller.addEventListener('pointerdown', takeover, { passive: true });
    scroller.addEventListener('keydown', takeover);
    scroller.addEventListener('scroll', onScroll);
    return () => {
      scroller.removeEventListener('wheel', takeover);
      scroller.removeEventListener('touchstart', takeover);
      scroller.removeEventListener('pointerdown', takeover);
      scroller.removeEventListener('keydown', takeover);
      scroller.removeEventListener('scroll', onScroll);
    };
  }, []);

  // Per-anchor draft storage: multiple comment boxes can be open at once in the
  // same file, each keyed by the (side, start line) it's anchored to. Object key
  // insertion order is preserved for these string keys, which the escape-stack
  // handler below relies on to find the most-recently-opened draft.
  const [draftsByFile, setDraftsByFile] = useState<Record<string, Record<string, DraftState>>>({});
  // Comments that cannot render inline are collapsed by default.
  const [staleExpanded, setStaleExpanded] = useState(false);

  const name = filePath ?? 'file.txt';
  const draftsForFile = draftsByFile[name] ?? EMPTY_DRAFTS;
  const draftKeys = useMemo(() => Object.keys(draftsForFile), [draftsForFile]);

  // Opens a new draft box at (side, start..end). No-op if a draft is already
  // open at that exact anchor — clicking an anchor that already has an open box
  // should neither duplicate it nor wipe its typed text.
  const openDraft = useCallback(
    (side: AnnotationSide, start: number, end: number) => {
      const key = anchorKeyOf(side, start);
      setDraftsByFile((current) => {
        const forFile = current[name] ?? EMPTY_DRAFTS;
        if (forFile[key]) return current;
        return { ...current, [name]: { ...forFile, [key]: { side, start, end, content: '' } } };
      });
    },
    [name]
  );

  const updateDraftContent = useCallback(
    (key: string, content: string) => {
      setDraftsByFile((current) => {
        const forFile = current[name];
        if (!forFile?.[key]) return current;
        return { ...current, [name]: { ...forFile, [key]: { ...forFile[key], content } } };
      });
    },
    [name]
  );

  const closeDraft = useCallback(
    (key: string) => {
      setDraftsByFile((current) => {
        const forFile = current[name];
        if (!forFile || !(key in forFile)) return current;
        const { [key]: _removed, ...rest } = forFile;
        return { ...current, [name]: rest };
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
  const formOpen = draftKeys.length > 0 || editingHere;

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
    setFrozen(null);
    setStaleExpanded(false);
  }, [filePath]);

  const oldFile = useMemo<FileContents>(() => ({ name, contents: shownOriginal }), [name, shownOriginal]);
  const newFile = useMemo<FileContents>(() => ({ name, contents: shownModified }), [name, shownModified]);

  const visibleLineRanges = useMemo(
    () => getVisibleLineRanges(oldFile, newFile, expandUnchanged),
    [oldFile, newFile, expandUnchanged]
  );

  // A comment cannot render inline when its anchor line no longer exists or when
  // Hunks mode collapses that unchanged line. The diff library silently drops
  // annotations for non-rendered lines, so surface them in a collapsed banner.
  const lineCounts = useMemo(
    () => ({ additions: shownModified.split('\n').length, deletions: shownOriginal.split('\n').length }),
    [shownModified, shownOriginal]
  );
  const { anchoredComments, staleComments } = useMemo(() => {
    const anchored: ReviewComment[] = [];
    const stale: ReviewComment[] = [];
    for (const c of comments) {
      const side: AnnotationSide = isOriginalSideComment(c) ? 'deletions' : 'additions';
      const max = side === 'deletions' ? lineCounts.deletions : lineCounts.additions;
      const lineExists = c.line_start >= 1 && c.line_start <= max;
      const lineVisible = !visibleLineRanges || isLineInRanges(c.line_start, visibleLineRanges[side]);
      (lineExists && lineVisible ? anchored : stale).push(c);
    }
    return { anchoredComments: anchored, staleComments: stale };
  }, [comments, lineCounts, visibleLineRanges]);

  // Remount the diff whenever the shown target (path or content) changes — the
  // library's intended way to switch files. While frozen, the shown content is
  // held constant, so an incoming change to the current file can't remount it.
  const diffKey = useMemo(
    () => `${name}:${hashContent(shownOriginal)}:${hashContent(shownModified)}`,
    [name, shownOriginal, shownModified]
  );

  // Controlled selection — ALWAYS null. Passing the `selectedLines` prop at all
  // (rather than omitting it) is what keeps @pierre/diffs out of uncontrolled
  // mode; if the prop is omitted the library commits its own internal
  // selectedRange on the first gutter-"+" click or drag-driven draft, and
  // InteractionManager then keeps re-anchoring the hover "+" to that stale
  // range on every pointer move instead of following the mouse (see
  // InteractionManager.placeUtilityFromSelection). This file used to reflect
  // the single open draft's range back into this prop to keep the library's
  // selection "in sync" with ours — but with multiple simultaneous drafts
  // there is no longer one range to reflect, and pinning to any one of them
  // would resume the hover-death bug on every OTHER draft's lines. A draft's
  // anchor is fully communicated by the inline comment box rendered at that
  // (side, line) via lineAnnotations/renderAnnotation, so this prop has
  // nothing to carry — always-null keeps the library controlled while
  // leaving its hover "+" free to follow the pointer everywhere.
  const selectedLines: SelectedLineRange | null = null;

  // The library forces a full re-render whenever the options object changes by
  // value (function identities included), so keep callbacks stable and memoize
  // the options object on the inputs that should actually retrigger a render.
  const handleGutterUtilityClick = useStableCallback((range: SelectedLineRange) => {
    const normalized = normalizeRange(range);
    if (!normalized) return;
    const { side, start, end } = normalized;
    suppressSelectionEndRef.current = true;
    openDraft(side, start, end);
  });

  // `enableLineSelection` requires a click to land on the number column to
  // start a selection (InteractionManager's `requireNumberColumn`), and its
  // pointerup handler reports a selection end unconditionally — there's no
  // movement/distance threshold distinguishing a drag from a bare click. So a
  // single click on a line number arrives here as a zero-length (start ===
  // end) range, same callback as an actual drag. We open the draft directly
  // either way: this is the intended affordance (a line-number click creates
  // a single-line comment, same as GitHub), not something to filter out.
  const handleLineSelectionEnd = useStableCallback((range: SelectedLineRange | null) => {
    if (suppressSelectionEndRef.current) {
      suppressSelectionEndRef.current = false;
      return;
    }
    if (!range) return;
    const normalized = normalizeRange(range);
    if (!normalized) return;
    const { side, start, end } = normalized;
    openDraft(side, start, end);
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
    // Clicking a line-number cell, or dragging across several, opens a
    // comment draft directly on that line/range; clicking the code area does
    // nothing (see handleLineSelectionEnd for why a click counts here).
    enableLineSelection: true,
    onLineSelectionEnd: handleLineSelectionEnd,
    // Render the native hover "+" in the line-number gutter; clicking it opens a
    // draft on that line. Without this the onGutterUtilityClick handler is dead
    // (the library gates the button behind enableGutterUtility, default false).
    enableGutterUtility: true,
    onGutterUtilityClick: handleGutterUtilityClick,
  }), [diffStyle, expandUnchanged, resolvedTheme, handleLineSelectionEnd, handleGutterUtilityClick]);

  // Group saved comments + any open drafts into one annotation per
  // (side, anchor line). The library slots annotations by `side`+`lineNumber`,
  // so collisions must be merged into a single thread — a draft can land on
  // the same anchor as an existing comment thread, or on its own empty one.
  const lineAnnotations = useMemo<DiffLineAnnotation<AnnotationMeta>[]>(() => {
    const groups = new Map<string, AnnotationMeta>();

    for (const comment of anchoredComments) {
      const side: AnnotationSide = isOriginalSideComment(comment) ? 'deletions' : 'additions';
      const line = comment.line_start;
      const key = anchorKeyOf(side, line);
      let group = groups.get(key);
      if (!group) {
        group = { side, lineNumber: line, comments: [], draft: false, anchorKey: key };
        groups.set(key, group);
      }
      group.comments.push(comment);
    }

    for (const key of draftKeys) {
      const d = draftsForFile[key];
      let group = groups.get(key);
      if (!group) {
        group = { side: d.side, lineNumber: d.start, comments: [], draft: true, anchorKey: key };
        groups.set(key, group);
      } else {
        group.draft = true;
      }
    }

    // The library keys annotation slots by array index (renderDiffChildren maps
    // with the index as the React key), so an annotation's index must stay stable
    // or React remounts its subtree — which would wipe an in-progress draft/edit
    // form (its typed text and focus). Keep ALL annotations hosting an open form
    // (any open draft, or the comment being edited) at the front, in a stable
    // order among themselves (sorted by anchor key so their relative order never
    // depends on Map iteration or comment arrival order), so that comments
    // arriving or leaving in the background only ever shift the trailing,
    // form-less threads. Visual placement is unaffected: the library positions
    // each thread by its `slot` (side+line), not array order.
    const all = Array.from(groups.values());
    const hasOpenForm = (g: AnnotationMeta) =>
      g.draft || g.comments.some((c) => c.id === editingCommentId);
    const active = all.filter(hasOpenForm).sort((a, b) => a.anchorKey.localeCompare(b.anchorKey));
    const rest = all.filter((g) => !hasOpenForm(g));
    return [...active, ...rest].map((meta) => ({
      side: meta.side,
      lineNumber: meta.lineNumber,
      metadata: meta,
    }));
  }, [anchoredComments, draftKeys, draftsForFile, editingCommentId]);

  const handleSaveDraft = useCallback(
    async (key: string, content: string) => {
      const d = draftsForFile[key];
      if (!d) return;
      const lineStart = d.start;
      const lineEnd = d.side === 'deletions' ? -d.end : d.end;
      try {
        await onAddComment(lineStart, lineEnd, content);
        closeDraft(key);
      } catch {
        // The parent owns user-visible error reporting; keep the draft intact so
        // the user can retry without losing typed text.
      }
    },
    [draftsForFile, onAddComment, closeDraft]
  );

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
      const key = meta.anchorKey;
      return (
        <DiffCommentThread
          // Keyed by anchor, not just positioned by array index: the "front"
          // slot a given index hosts can switch which anchor it represents
          // between renders (e.g. draft A at index 0 closes, sliding draft B
          // from index 1 into index 0). Without this key React reconciles
          // that as "the same DiffCommentThread, new props" and reuses the
          // mounted CommentForm instance — whose `value` state is seeded
          // once from `initialValue` — leaving B's box showing A's stale
          // typed text. The key forces a remount whenever the anchor at a
          // slot actually changes, while leaving it stable (untouched
          // state/focus) across renders where the same anchor stays put.
          key={key}
          comments={meta.comments}
          draft={meta.draft}
          editingCommentId={editingCommentId}
          readOnlyCommentIds={readOnlyCommentIds}
          showSendToClaude={!!onSendToClaude && !!filePath}
          draftContent={meta.draft ? draftsForFile[key]?.content : undefined}
          onDraftContentChange={meta.draft ? (content) => updateDraftContent(key, content) : undefined}
          onSaveDraft={(content) => handleSaveDraft(key, content)}
          onCancelDraft={() => closeDraft(key)}
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
      readOnlyCommentIds,
      onSendToClaude,
      filePath,
      draftsForFile,
      updateDraftContent,
      handleSaveDraft,
      closeDraft,
      onStartEdit,
      onEditComment,
      onCancelEdit,
      onResolveComment,
      onDeleteComment,
      handleSendComment,
    ]
  );

  // Escape closes the most-recently-opened draft first (LIFO among drafts),
  // before falling through to the panel's own escape handling. Object key
  // insertion order (preserved for these string keys) doubles as open order,
  // so the last key is the most recently opened still-open draft.
  const handleEscapeDraft = useCallback(() => {
    if (draftKeys.length === 0) return;
    closeDraft(draftKeys[draftKeys.length - 1]);
  }, [draftKeys, closeDraft]);
  useEscapeStack(handleEscapeDraft, draftKeys.length > 0);
  useEscapeStack(onCancelEdit, editingCommentId !== null);

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
                readOnlyCommentIds={readOnlyCommentIds}
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
          selectedLines={selectedLines}
          renderAnnotation={renderAnnotation}
          disableWorkerPool
        />
      </Virtualizer>
    </div>
  );
}

export default DiffView;
