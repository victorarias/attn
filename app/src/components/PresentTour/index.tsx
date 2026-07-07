/**
 * PresentTour — continuous scroll-tour reader for a Present round.
 *
 * Renders every manifest file's diff as a card in reading order inside ONE
 * `@pierre/diffs` `CodeView` (react entry), the multi-item sibling of the
 * single-file `DiffView` used by the main window. See DiffView.tsx for the
 * annotation/draft wiring this ports from — same library primitives
 * (`renderAnnotation`, `renderGutterUtility`/`onGutterUtilityClick`,
 * controlled `selectedLines`, signed `line_end` convention), generalized so a
 * comment/draft anchor is `${filepath}:${side}:${start}` instead of just
 * `${side}:${start}`.
 *
 * Two design points called out in the slice-1 brief as "investigate, fall
 * back if unworkable" were resolved toward the documented fallback rather
 * than the preferred option, given the size of this slice:
 *
 *   - Per-file loading/error state: CodeView's controlled `items` only
 *     accept a real `CodeViewDiffItem` (needs a parsed `FileDiffMetadata`)
 *     or a `CodeViewFileItem` (needs real file contents) — there is no
 *     skeleton/placeholder item type. Rather than synthesize placeholder
 *     diffs, this component renders a single "loading tour…" state until
 *     every file's diff fetch has settled, then builds the full item list
 *     once. A per-file error still gets its own card in-order (rendered as
 *     a plain `type: 'file'` item carrying the error text) rather than
 *     being dropped, but it will not stream in before its siblings.
 *   - Summary/footer placement: CodeView's react wrapper renders its own
 *     internal scroll container (via `containerRef`), not a slot host
 *     that's known to tolerate injected sibling DOM. Rather than risk
 *     fighting the library's own layout, the summary card and end-of-tour
 *     footer are flex siblings around the CodeView, not inside its own
 *     scroller. They stay put (do not scroll with the tour) as a result —
 *     acceptable for slice 1 per the brief's stated fallback.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import {
  CodeView,
  useStableCallback,
  type AnnotationSide,
  type CodeViewHandle,
  type CodeViewItem,
  type DiffLineAnnotation,
  type FileContents,
  type SelectedLineRange,
} from '@pierre/diffs/react';
// CodeViewLineSelection/CodeViewOptions are exported from the package root,
// not the /react entry (same split FileDiffOptions has in DiffView.tsx).
import { parseDiffFromFile, type CodeViewLineSelection, type CodeViewOptions } from '@pierre/diffs';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import type { ResolvedTheme } from '../../hooks/useTheme';
import type { ReviewComment } from '../../types/generated';
import { isOriginalSideComment } from '../../utils/reviewComment';
import { normalizeRange } from '../DiffView';
import { DiffCommentThread } from '../DiffCommentThread';
import '../DiffView.css';
import './PresentTour.css';

export interface PresentTourFileDiff {
  loading: boolean;
  original?: string;
  modified?: string;
  error?: string;
}

export interface PresentTourFile {
  path: string;
  note?: string;
  diff: PresentTourFileDiff;
}

export interface PresentTourProps {
  summary?: string;
  files: PresentTourFile[];
  comments: ReviewComment[];
  editingCommentId: string | null;
  readOnlyCommentIds: Set<string>;
  resolvedTheme?: ResolvedTheme;
  fontSize?: number;
  onAddComment: (filepath: string, lineStart: number, lineEnd: number, content: string) => void;
  onEditComment: (id: string, content: string) => void;
  onStartEdit: (id: string) => void;
  onCancelEdit: () => void;
  onResolveComment: (id: string, resolved: boolean) => void;
  onDeleteComment: (id: string) => void;
  onSendToClaude?: (reference: string) => void;
  /** Path the caller wants the tour scrolled to (rail click / j-k). Paired
   * with `scrollNonce` so re-clicking the same file still re-scrolls. */
  scrollToPath?: string | null;
  scrollNonce?: number;
  /** Fires with the path nearest the top of the viewport as the user scrolls. */
  onActivePathChange?: (path: string) => void;
}

// Metadata carried on each native line annotation, generalized from DiffView's
// AnnotationMeta with a filepath added so the same (side, line) can anchor
// independently in different files sharing this one CodeView.
interface AnnotationMeta {
  filepath: string;
  side: AnnotationSide;
  lineNumber: number;
  comments: ReviewComment[];
  draft: boolean;
  anchorKey: string;
}

type DraftState = {
  filepath: string;
  side: AnnotationSide;
  start: number;
  end: number;
  content: string;
};

function anchorKeyOf(filepath: string, side: AnnotationSide, start: number): string {
  return `${filepath}:${side}:${start}`;
}

type VisibleLineRanges = Record<AnnotationSide, Array<[number, number]>>;

function isLineInRanges(line: number, ranges: Array<[number, number]>): boolean {
  return ranges.some(([start, end]) => line >= start && line <= end);
}

function getVisibleLineRanges(oldFile: FileContents, newFile: FileContents): VisibleLineRanges | null {
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

export function PresentTour({
  summary,
  files,
  comments,
  editingCommentId,
  readOnlyCommentIds,
  resolvedTheme = 'dark',
  fontSize,
  onAddComment,
  onEditComment,
  onStartEdit,
  onCancelEdit,
  onResolveComment,
  onDeleteComment,
  onSendToClaude,
  scrollToPath,
  scrollNonce,
  onActivePathChange,
}: PresentTourProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<CodeViewHandle<AnnotationMeta> | null>(null);
  const suppressSelectionEndRef = useRef(false);
  // CodeView's controlled-item reconciliation keys off each item's `version`
  // field (see components/CodeView.js `syncItemRecord`): a matching version
  // — including two `undefined`s — means "no change, keep the cached
  // record", so a brand new `items` array with different annotations but no
  // `version` bump is silently ignored (verified against the real library:
  // annotations built into `items` never reached the DOM without this).
  // Bumped once per recompute of `items` below and shared by every item in
  // that pass, since firing on any recompute is correct even though it's
  // coarser than a per-file version would be.
  const itemsVersionRef = useRef(0);

  // Per-anchor draft storage, keyed globally (filepath is already part of the
  // anchor key) so a draft on file A and a draft on file B can be open at once
  // — the multi-draft parity requirement this slice must keep.
  const [drafts, setDrafts] = useState<Record<string, DraftState>>({});
  const draftKeys = useMemo(() => Object.keys(drafts), [drafts]);

  const openDraft = useCallback((filepath: string, side: AnnotationSide, start: number, end: number) => {
    const key = anchorKeyOf(filepath, side, start);
    setDrafts((current) => {
      if (current[key]) return current;
      return { ...current, [key]: { filepath, side, start, end, content: '' } };
    });
  }, []);

  const updateDraftContent = useCallback((key: string, content: string) => {
    setDrafts((current) => {
      if (!current[key]) return current;
      return { ...current, [key]: { ...current[key], content } };
    });
  }, []);

  const closeDraft = useCallback((key: string) => {
    setDrafts((current) => {
      if (!(key in current)) return current;
      const { [key]: _removed, ...rest } = current;
      return rest;
    });
  }, []);

  // Escape closes the most-recently-opened draft first, across ALL files —
  // same LIFO contract DiffView has for one file, generalized.
  const handleEscapeDraft = useCallback(() => {
    if (draftKeys.length === 0) return;
    closeDraft(draftKeys[draftKeys.length - 1]);
  }, [draftKeys, closeDraft]);
  useEscapeStack(handleEscapeDraft, draftKeys.length > 0);
  useEscapeStack(onCancelEdit, editingCommentId !== null);

  // Freeze each file's shown content while that file has an open draft or is
  // hosting the comment being edited — same reasoning as DiffView's single
  // `frozen` state, scoped per file since many files render at once here.
  const formOpenByFile = useMemo(() => {
    const open = new Set<string>();
    for (const key of draftKeys) open.add(drafts[key].filepath);
    if (editingCommentId) {
      const editing = comments.find((c) => c.id === editingCommentId);
      if (editing) open.add(editing.filepath);
    }
    return open;
  }, [draftKeys, drafts, editingCommentId, comments]);

  const frozenRef = useRef<Map<string, { original: string; modified: string }>>(new Map());
  // Housekeeping: drop frozen snapshots for files that no longer have an open
  // form, so the next content change is adopted immediately.
  for (const path of Array.from(frozenRef.current.keys())) {
    if (!formOpenByFile.has(path)) frozenRef.current.delete(path);
  }

  const commentsByFile = useMemo(() => {
    const map = new Map<string, ReviewComment[]>();
    for (const c of comments) {
      const list = map.get(c.filepath);
      if (list) list.push(c);
      else map.set(c.filepath, [c]);
    }
    return map;
  }, [comments]);

  const draftsByFile = useMemo(() => {
    const map = new Map<string, string[]>();
    for (const key of draftKeys) {
      const filepath = drafts[key].filepath;
      const list = map.get(filepath);
      if (list) list.push(key);
      else map.set(filepath, [key]);
    }
    return map;
  }, [draftKeys, drafts]);

  const allSettled = files.length > 0 && files.every((f) => !f.diff.loading);

  const handleSaveDraft = useCallback(
    async (key: string, content: string) => {
      const d = drafts[key];
      if (!d) return;
      const lineStart = d.start;
      const lineEnd = d.side === 'deletions' ? -d.end : d.end;
      try {
        onAddComment(d.filepath, lineStart, lineEnd, content);
        closeDraft(key);
      } catch {
        // Parent owns error reporting; keep the draft so the user can retry.
      }
    },
    [drafts, onAddComment, closeDraft]
  );

  const handleSendComment = useCallback(
    (comment: ReviewComment) => {
      if (!onSendToClaude) return;
      const ref = normalizeRange({ side: isOriginalSideComment(comment) ? 'deletions' : 'additions', start: comment.line_start, end: Math.abs(comment.line_end) });
      if (!ref) return;
      onSendToClaude(`@${comment.filepath}:L${ref.start}${ref.start === ref.end ? '' : `-L${ref.end}`}\nComment: ${comment.content}`);
    },
    [onSendToClaude]
  );

  const renderAnnotation = useCallback(
    (annotation: DiffLineAnnotation<AnnotationMeta>) => {
      const meta = annotation.metadata;
      if (!meta) return null;
      const key = meta.anchorKey;
      return (
        <DiffCommentThread
          key={key}
          comments={meta.comments}
          draft={meta.draft}
          editingCommentId={editingCommentId}
          readOnlyCommentIds={readOnlyCommentIds}
          showSendToClaude={!!onSendToClaude}
          draftContent={meta.draft ? drafts[key]?.content : undefined}
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
      drafts,
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

  // Build one CodeViewItem per manifest file, in reading order. Only runs
  // once every file's diff fetch has settled (see the module doc for why).
  const items = useMemo<CodeViewItem<AnnotationMeta>[]>(() => {
    if (!allSettled) return [];
    itemsVersionRef.current += 1;
    const version = itemsVersionRef.current;
    return files.map((file): CodeViewItem<AnnotationMeta> => {
      const { diff } = file;
      if (diff.error || diff.original === undefined || diff.modified === undefined) {
        return {
          id: file.path,
          type: 'file',
          file: { name: file.path, contents: diff.error ?? 'Failed to load this file’s diff.' },
          version,
        };
      }

      const frozen = frozenRef.current.get(file.path);
      if (formOpenByFile.has(file.path) && !frozen) {
        frozenRef.current.set(file.path, { original: diff.original, modified: diff.modified });
      }
      const shown = frozenRef.current.get(file.path) ?? { original: diff.original, modified: diff.modified };

      const oldFile: FileContents = { name: file.path, contents: shown.original };
      const newFile: FileContents = { name: file.path, contents: shown.modified };
      const fileDiff = parseDiffFromFile(oldFile, newFile);
      const visibleLineRanges = getVisibleLineRanges(oldFile, newFile);
      const lineCounts = {
        additions: shown.modified.split('\n').length,
        deletions: shown.original.split('\n').length,
      };

      const fileComments = commentsByFile.get(file.path) ?? [];
      const fileDraftKeys = draftsByFile.get(file.path) ?? [];

      const groups = new Map<string, AnnotationMeta>();
      for (const comment of fileComments) {
        const side: AnnotationSide = isOriginalSideComment(comment) ? 'deletions' : 'additions';
        const line = comment.line_start;
        const max = side === 'deletions' ? lineCounts.deletions : lineCounts.additions;
        const lineExists = line >= 1 && line <= max;
        const lineVisible = !visibleLineRanges || isLineInRanges(line, visibleLineRanges[side]);
        // Comments anchored to a line that no longer renders (collapsed hunk
        // or a stale line number) simply don't get a native annotation slot —
        // parity note: DiffView surfaces these via a collapsible "not visible"
        // banner; this slice drops that affordance (see PR report).
        if (!lineExists || !lineVisible) continue;
        const key = anchorKeyOf(file.path, side, line);
        let group = groups.get(key);
        if (!group) {
          group = { filepath: file.path, side, lineNumber: line, comments: [], draft: false, anchorKey: key };
          groups.set(key, group);
        }
        group.comments.push(comment);
      }
      for (const key of fileDraftKeys) {
        const d = drafts[key];
        let group = groups.get(key);
        if (!group) {
          group = { filepath: file.path, side: d.side, lineNumber: d.start, comments: [], draft: true, anchorKey: key };
          groups.set(key, group);
        } else {
          group.draft = true;
        }
      }

      const all = Array.from(groups.values());
      const hasOpenForm = (g: AnnotationMeta) => g.draft || g.comments.some((c) => c.id === editingCommentId);
      const active = all.filter(hasOpenForm).sort((a, b) => a.anchorKey.localeCompare(b.anchorKey));
      const rest = all.filter((g) => !hasOpenForm(g));
      const annotations: DiffLineAnnotation<AnnotationMeta>[] = [...active, ...rest].map((meta) => ({
        side: meta.side,
        lineNumber: meta.lineNumber,
        metadata: meta,
      }));

      return { id: file.path, type: 'diff', fileDiff, annotations, version };
    });
    // frozenRef is a ref (mutated in-place above); its contents are captured
    // deliberately as part of this computation and don't need to be a dep.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allSettled, files, commentsByFile, draftsByFile, drafts, formOpenByFile, editingCommentId]);

  const handleGutterUtilityClick = useStableCallback(
    (range: SelectedLineRange, context: { item: CodeViewItem<AnnotationMeta> }) => {
      const normalized = normalizeRange(range);
      if (!normalized) return;
      const { side, start, end } = normalized;
      suppressSelectionEndRef.current = true;
      openDraft(context.item.id, side, start, end);
    }
  );

  const handleLineSelectionEnd = useStableCallback(
    (range: SelectedLineRange | null, context: { item: CodeViewItem<AnnotationMeta> }) => {
      if (suppressSelectionEndRef.current) {
        suppressSelectionEndRef.current = false;
        return;
      }
      if (!range) return;
      const normalized = normalizeRange(range);
      if (!normalized) return;
      const { side, start, end } = normalized;
      openDraft(context.item.id, side, start, end);
    }
  );

  const noteByPath = useMemo(() => new Map(files.map((f) => [f.path, f.note])), [files]);

  const renderHeaderMetadata = useCallback(
    (item: CodeViewItem<AnnotationMeta>) => {
      const note = noteByPath.get(item.id);
      if (!note) return null;
      return (
        <div className="present-tour-file-note">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{note}</ReactMarkdown>
        </div>
      );
    },
    [noteByPath]
  );

  const options = useMemo<CodeViewOptions<AnnotationMeta>>(
    () => ({
      diffStyle: 'unified',
      expandUnchanged: false,
      diffIndicators: 'classic',
      theme: { dark: 'pierre-dark', light: 'pierre-light' },
      themeType: resolvedTheme,
      preferredHighlighter: 'shiki-js',
      enableLineSelection: true,
      onLineSelectionEnd: handleLineSelectionEnd,
      enableGutterUtility: true,
      onGutterUtilityClick: handleGutterUtilityClick,
      stickyHeaders: true,
    }),
    [resolvedTheme, handleLineSelectionEnd, handleGutterUtilityClick]
  );

  const selectedLines: CodeViewLineSelection | null = null;

  // Scroll-pin: apply DiffView's cold-window defense (see the long comment
  // there) to the tour's own scroll container. Arms once per mount. Shared
  // as a ref (not a closure-local variable) because the rail/j-k scrollTo
  // effect below also needs to arm it — see that effect's comment for why.
  const userTookOverRef = useRef(false);
  useEffect(() => {
    const scroller = containerRef.current;
    if (!scroller) return;
    const takeover = () => {
      userTookOverRef.current = true;
    };
    const onNativeScroll = () => {
      if (!userTookOverRef.current && scroller.scrollTop !== 0) scroller.scrollTop = 0;
    };
    scroller.addEventListener('wheel', takeover, { passive: true });
    scroller.addEventListener('touchstart', takeover, { passive: true });
    scroller.addEventListener('pointerdown', takeover, { passive: true });
    scroller.addEventListener('keydown', takeover);
    scroller.addEventListener('scroll', onNativeScroll);
    return () => {
      scroller.removeEventListener('wheel', takeover);
      scroller.removeEventListener('touchstart', takeover);
      scroller.removeEventListener('pointerdown', takeover);
      scroller.removeEventListener('keydown', takeover);
      scroller.removeEventListener('scroll', onNativeScroll);
    };
  }, []);

  // Rail-driven / j-k-driven scroll: nonce forces a re-scroll even to the
  // same path (e.g. re-pressing j at the last file).
  //
  // This request is itself deliberate user-driven navigation, but it does
  // not fire a native wheel/touch/pointerdown/keydown ON THE SCROLLER (the
  // rail lives outside PresentTour, and the click/keypress that triggered it
  // targets the rail, not the scroll container) — the gutter "+" button
  // used to open a draft has the same property (its click doesn't bubble a
  // pointerdown out to the container's listener). Verified empirically: the
  // library's smooth `scrollTo` fires native `scroll` events on the same
  // container the cold-window pin listens on, and without this line those
  // events kept getting fought back to 0 mid-animation, so a rail click
  // issued before the user's first raw wheel/touch was silently swallowed.
  // Arm the same flag the pin checks so the pin treats this as real input.
  useEffect(() => {
    if (!scrollToPath) return;
    userTookOverRef.current = true;
    handleRef.current?.scrollTo({ type: 'item', id: scrollToPath, align: 'start', behavior: 'smooth' });
    // scrollNonce intentionally included so repeat clicks on the same path re-fire.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToPath, scrollNonce]);

  // Track the file nearest the top of the viewport so the rail can highlight
  // it. There is no `data-*` attribute on CodeView's rendered item roots to
  // query by id (confirmed against the real shadow DOM output, not just the
  // types), so this uses the library's own `getRenderedItems()` — each
  // record carries the real mounted `element` for that item — rather than a
  // guessed selector.
  const handleScroll = useCallback(
    (scrollTop: number) => {
      if (!onActivePathChange || !containerRef.current) return;
      const instance = handleRef.current?.getInstance();
      if (!instance) return;
      const containerTop = containerRef.current.getBoundingClientRect().top;
      const threshold = 80;
      let bestPath: string | null = null;
      let bestTop = -Infinity; // largest top at-or-above the threshold line
      let nearestPath: string | null = null;
      let nearestDistance = Infinity; // fallback: closest to the threshold line either side
      for (const rendered of instance.getRenderedItems()) {
        const top = rendered.element.getBoundingClientRect().top - containerTop;
        if (top <= threshold && top > bestTop) {
          bestTop = top;
          bestPath = rendered.id;
        }
        const distance = Math.abs(top - threshold);
        if (distance < nearestDistance) {
          nearestDistance = distance;
          nearestPath = rendered.id;
        }
      }
      const path = bestPath ?? nearestPath;
      if (path) onActivePathChange(path);
      void scrollTop;
    },
    [onActivePathChange]
  );

  return (
    <div
      className="present-tour"
      data-testid="present-tour"
      style={fontSize ? ({ '--diffs-font-size': `${fontSize}px` } as React.CSSProperties) : undefined}
    >
      {summary && (
        <div className="present-tour-summary" data-testid="present-tour-summary">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{summary}</ReactMarkdown>
        </div>
      )}

      {!allSettled ? (
        <div className="present-tour-loading">Loading tour…</div>
      ) : items.length === 0 ? (
        <div className="present-tour-loading">No files in this round.</div>
      ) : (
        <CodeView<AnnotationMeta>
          ref={handleRef}
          items={items}
          options={options}
          className="present-tour-scroller"
          style={{ flex: 1, minHeight: 0, overflow: 'auto' }}
          containerRef={containerRef}
          selectedLines={selectedLines}
          renderAnnotation={renderAnnotation}
          renderHeaderMetadata={renderHeaderMetadata}
          onScroll={handleScroll}
          disableWorkerPool
        />
      )}

      {allSettled && items.length > 0 && (
        <div className="present-tour-footer" data-testid="present-tour-footer">
          End of tour — {items.length} file{items.length === 1 ? '' : 's'} reviewed.
        </div>
      )}
    </div>
  );
}

export default PresentTour;
