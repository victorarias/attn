/**
 * DiffView — wrapper around @pierre/diffs that renders one file's diff and wires
 * attn's review comments into the library's native primitives: threads and draft
 * forms through `lineAnnotations`/`renderAnnotation`, the gutter hover "+"
 * (`onGutterUtilityClick`), and line-number click/drag (`enableLineSelection` +
 * `onLineSelectionEnd`).
 *
 * Comment <-> annotation convention: `line_end < 0` encodes the original/deleted
 * side, `line_start` is the anchor line, `abs(line_end)` the range end.
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

// Every saved comment sharing a (side, anchor line) is one thread, plus an
// optional in-progress draft form on that same anchor.
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

/** Stable reference so a file with no drafts gets no fresh object identity. */
const EMPTY_DRAFTS: Record<string, DraftState> = {};

export interface DiffViewProps {
  original: string;
  modified: string;
  filePath?: string;
  comments: ReviewComment[];
  editingCommentId: string | null;
  /** Comment ids to render without Edit/Resolve/Delete actions. */
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
  // fires onGutterUtilityClick; swallow it so the "+" opens only one draft.
  const suppressSelectionEndRef = useRef(false);

  // On a cold present-window load @pierre/diffs' Virtualizer measures against a
  // hidden/throttled layout and autonomously scrolls to garbage positions
  // (observed: scrollTo 8792 -> clamp 8265 -> 5565), so the first click resolves
  // against the wrong row. Pin the scroller to the top until real input arrives.
  // Empty dep array: arms once per mount, never re-arming on file switch.
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

  // Per-anchor draft storage keyed by (side, start line); key insertion order is
  // open order, which the escape-stack handler relies on.
  const [draftsByFile, setDraftsByFile] = useState<Record<string, Record<string, DraftState>>>({});
  // Comments that cannot render inline are collapsed by default.
  const [staleExpanded, setStaleExpanded] = useState(false);

  const name = filePath ?? 'file.txt';
  const draftsForFile = draftsByFile[name] ?? EMPTY_DRAFTS;
  const draftKeys = useMemo(() => Object.keys(draftsForFile), [draftsForFile]);

  // Opens a draft box at (side, start..end). No-op at an anchor that already has
  // one, so a repeat click neither duplicates it nor wipes its typed text.
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

  // Is the user typing a comment on THIS file — a draft or an edit?
  const editingHere = useMemo(
    () => editingCommentId != null && comments.some((c) => c.id === editingCommentId),
    [editingCommentId, comments]
  );
  const formOpen = draftKeys.length > 0 || editingHere;

  // Freeze the diff content while a comment form is open. @pierre/diffs cannot
  // swap its target in place (VirtualizedFileDiff.render keeps `this.fileDiff`
  // via `??=`), so a content change remounts (see `diffKey`) and discards any
  // in-progress comment — and the file changes constantly while the agent edits.
  const [frozen, setFrozen] = useState<{ original: string; modified: string } | null>(null);
  useEffect(() => {
    setFrozen((current) => (formOpen ? current ?? { original, modified } : null));
  }, [formOpen, original, modified]);

  const shownOriginal = frozen?.original ?? original;
  const shownModified = frozen?.modified ?? modified;

  // Selection/frozen content belong to one file; drafts are keyed by file so
  // navigating away and back does not destroy an unsaved comment.
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

  // The library silently drops annotations for non-rendered lines (anchor gone,
  // or collapsed by Hunks mode), so surface those in a collapsed banner.
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

  // Remount whenever the shown target changes — the library's way to switch
  // files. While frozen the shown content is constant, so nothing remounts.
  const diffKey = useMemo(
    () => `${name}:${hashContent(shownOriginal)}:${hashContent(shownModified)}`,
    [name, shownOriginal, shownModified]
  );

  // Controlled selection — ALWAYS null. Passing the prop at all is what keeps
  // @pierre/diffs out of uncontrolled mode; omitted, it commits its own
  // selectedRange and InteractionManager re-anchors the hover "+" to that stale
  // range on every pointer move instead of following the mouse. Nothing needs to
  // be reflected back: a draft's anchor is carried by its inline comment box.
  const selectedLines: SelectedLineRange | null = null;

  // The library re-renders fully whenever the options object changes by value
  // (function identities included), so keep callbacks stable and memoize it.
  const handleGutterUtilityClick = useStableCallback((range: SelectedLineRange) => {
    const normalized = normalizeRange(range);
    if (!normalized) return;
    const { side, start, end } = normalized;
    suppressSelectionEndRef.current = true;
    openDraft(side, start, end);
  });

  // The library reports a selection end unconditionally on pointerup, with no
  // drag threshold, so a bare line-number click arrives as a zero-length range
  // through this same callback. Both open a draft: that is the affordance.
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
    // Pure-JS Shiki: WASM fetching is unreliable under Tauri's protocol + CSP.
    preferredHighlighter: 'shiki-js',
    // Line-number click or drag opens a draft; the code area does nothing.
    enableLineSelection: true,
    onLineSelectionEnd: handleLineSelectionEnd,
    // Without this the onGutterUtilityClick handler is dead — the library gates
    // the hover "+" behind enableGutterUtility, default false.
    enableGutterUtility: true,
    onGutterUtilityClick: handleGutterUtilityClick,
  }), [diffStyle, expandUnchanged, resolvedTheme, handleLineSelectionEnd, handleGutterUtilityClick]);

  // Group saved comments and open drafts into one annotation per (side, anchor
  // line): the library slots by `side`+`lineNumber`, so collisions must merge.
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

    // The library keys annotation slots by ARRAY INDEX, so a moved index remounts
    // the subtree and wipes an in-progress form. Every annotation hosting an open
    // form goes to the front in anchor-key order, so background comment churn
    // only shifts the trailing form-less threads. Placement is unaffected: the
    // library positions each thread by its `slot` (side+line), not array order.
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
        // The parent reports the error; keep the draft so a retry keeps its text.
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
          // Keyed by anchor: an index can change which anchor it hosts between
          // renders, and React would then reuse the mounted CommentForm, whose
          // `value` is seeded once from `initialValue` — showing the previous
          // draft's text. The key remounts only when the anchor actually moves.
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

  // Escape closes the most-recently-opened draft first, then falls through to
  // the panel's own handling; key insertion order doubles as open order.
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
