/**
 * PresentTour — every manifest file's diff as a card in reading order inside
 * ONE `@pierre/diffs` `CodeView`. Ports DiffView.tsx's annotation/draft wiring,
 * generalized so an anchor is `${filepath}:${side}:${start}`.
 *
 * The summary card is a flex sibling ABOVE the CodeView, not inside its
 * scroller: the react wrapper owns that container and is not a slot host for
 * injected sibling DOM. It folds via `summaryVisible` instead of scrolling away.
 *
 * CodeView mounts on the first manifest file, not on every diff settling. An
 * unsettled file renders as a zero-hunk placeholder until the frame-budgeted
 * admission effect (see `readyPaths`) parses it a few files per frame; an
 * errored file gets its card immediately and needs no admission.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
// These two are exported from the package root, not the /react entry.
import { parseDiffFromFile, type CodeViewLineSelection, type CodeViewOptions } from '@pierre/diffs';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import type { ResolvedTheme } from '../../hooks/useTheme';
import type { ReviewComment } from '../../types/generated';
import { isOriginalSideComment } from '../../utils/reviewComment';
import { normalizeRange } from '../DiffView';
import { DiffCommentThread } from '../DiffCommentThread';
import { Markdown } from '../Markdown';
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
  /** Rail section; 'skip' de-emphasizes the card and hides the reviewed
   * toggle. Omitted means 'tour'. */
  group?: 'tour' | 'other' | 'skip';
}

/** An annotation's render anchor: its file, and the `data-anchor-key` its
 * rendered thread carries. Used for N/P hopping across the round. */
export interface AnnotationAnchor {
  path: string;
  anchorKey: string;
}

export interface PresentTourProps {
  summary?: string;
  /** The card stays mounted so the fold can animate. */
  summaryVisible?: boolean;
  /** The caller owns `summaryVisible`; this component never flips it itself. */
  onSummaryVisibleChange?: (visible: boolean) => void;
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
  /** Paired with `scrollNonce` so re-clicking the same file re-scrolls. */
  scrollToPath?: string | null;
  scrollNonce?: number;
  /** Fires with the path nearest the viewport top, never with null: the
   * Summary stop is entered only explicitly, never by scrolling to the top. */
  onActivePathChange?: (path: string | null) => void;
  reviewedPaths: ReadonlySet<string>;
  onToggleReviewed: (path: string) => void;
  /** Comments originating from manifest author annotations rather than
   * reviewer replies; drives the outside-diff fallback and the N/P hop list. */
  annotationCommentIds?: Set<string>;
  /** Every annotation's anchor in document order: `files` order, then by
   * rendered line. PresentRoot drives N/P hopping from it. */
  onAnnotationAnchorsChange?: (anchors: AnnotationAnchor[]) => void;
  /** Paired with `annotationScrollNonce` so re-hopping to the same anchor
   * still re-scrolls. */
  scrollToAnnotation?: AnnotationAnchor | null;
  annotationScrollNonce?: number;
}

// Carries a filepath so the same (side, line) can anchor independently in
// different files sharing this one CodeView.
interface AnnotationMeta {
  filepath: string;
  side: AnnotationSide;
  lineNumber: number;
  comments: ReviewComment[];
  draft: boolean;
  anchorKey: string;
  /** Set when the anchor was moved in from outside the visible hunks. */
  outsideDiffNote?: string;
  /** A synthetic annotation carrying a file note; see `notePlacedPathsRef`. */
  kind?: 'note';
  noteMarkdown?: string;
}

// Deliberately carries no live textarea content — see draftContentsRef.
type DraftAnchor = {
  filepath: string;
  side: AnnotationSide;
  start: number;
  end: number;
};

function anchorKeyOf(filepath: string, side: AnnotationSide, start: number): string {
  return `${filepath}:${side}:${start}`;
}

type VisibleLineRanges = Record<AnnotationSide, Array<[number, number]>>;

function isLineInRanges(line: number, ranges: Array<[number, number]>): boolean {
  return ranges.some(([start, end]) => line >= start && line <= end);
}

// Nearest visible point to `target` across a side's hunk ranges, ties broken
// toward the earlier line.
function nearestVisibleLine(target: number, ranges: Array<[number, number]>): number | null {
  let best: number | null = null;
  let bestDistance = Infinity;
  for (const [start, end] of ranges) {
    const clamped = Math.max(start, Math.min(end, target));
    const distance = Math.abs(target - clamped);
    if (distance < bestDistance || (distance === bestDistance && best !== null && clamped < best)) {
      bestDistance = distance;
      best = clamped;
    }
  }
  return best;
}

// Only a file that can grow when an async diagram settles needs
// `diagramLayoutTick` in its signature; for the rest it would bump the version
// on every unrelated diagram in the round.
function fileHasMermaid(file: PresentTourFile, fileComments: ReviewComment[]): boolean {
  if (file.note?.includes('```mermaid')) return true;
  return fileComments.some((c) => c.content.includes('```mermaid'));
}

// Parse/derived data for one file's shown (original, modified) pair; kept
// separate from the item cache — see parseCacheRef.
interface ParsedFileCacheEntry {
  original: string;
  modified: string;
  fileDiff: ReturnType<typeof parseDiffFromFile>;
  visibleLineRanges: VisibleLineRanges;
  lineCounts: { additions: number; deletions: number };
}

// A built CodeViewItem plus what the items memo needs to skip rebuilding it.
interface FileItemCacheEntry {
  signature: string;
  shownOriginal?: string; // undefined for error items (no diff content shown)
  shownModified?: string;
  item: CodeViewItem<AnnotationMeta>;
  anchors: AnnotationAnchor[];
  notePlaced: boolean;
}

function getVisibleLineRangesFromDiff(diff: ReturnType<typeof parseDiffFromFile>): VisibleLineRanges {
  return diff.hunks.reduce<VisibleLineRanges>(
    (ranges, hunk) => {
      ranges.deletions.push([hunk.deletionStart, hunk.deletionStart + hunk.deletionCount - 1]);
      ranges.additions.push([hunk.additionStart, hunk.additionStart + hunk.additionCount - 1]);
      return ranges;
    },
    { additions: [], deletions: [] }
  );
}

// Shared by the items memo and the admission effect. A hit requires the cached
// entry's own (original, modified) to match.
function ensureParsedFile(
  cache: Map<string, ParsedFileCacheEntry>,
  path: string,
  original: string,
  modified: string
): ParsedFileCacheEntry {
  const cached = cache.get(path);
  if (cached && cached.original === original && cached.modified === modified) return cached;
  const oldFile: FileContents = { name: path, contents: original };
  const newFile: FileContents = { name: path, contents: modified };
  const fileDiff = parseDiffFromFile(oldFile, newFile);
  const visibleLineRanges = getVisibleLineRangesFromDiff(fileDiff);
  const lineCounts = {
    additions: modified.split('\n').length,
    deletions: original.split('\n').length,
  };
  const entry: ParsedFileCacheEntry = { original, modified, fileDiff, visibleLineRanges, lineCounts };
  cache.set(path, entry);
  return entry;
}

export function PresentTour({
  summary,
  summaryVisible = true,
  onSummaryVisibleChange,
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
  reviewedPaths,
  onToggleReviewed,
  annotationCommentIds,
  onAnnotationAnchorsChange,
  scrollToAnnotation,
  annotationScrollNonce,
}: PresentTourProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<CodeViewHandle<AnnotationMeta> | null>(null);
  const suppressSelectionEndRef = useRef(false);
  // A wheel over the card reaches none of the listeners below (it is a flex
  // sibling of the scroller), so it would be a dead zone. Wheel-down collapses
  // the card, but only once its own internal scroll is exhausted.
  const summaryBodyRef = useRef<HTMLDivElement>(null);
  const handleSummaryWheel = useCallback(
    (e: React.WheelEvent) => {
      if (!summaryVisible || e.deltaY <= 0) return;
      const body = summaryBodyRef.current;
      if (body && body.scrollTop + body.clientHeight < body.scrollHeight - 1) return; // still has content to scroll
      onSummaryVisibleChange?.(false);
    },
    [summaryVisible, onSummaryVisibleChange]
  );
  // Populated by the items memo in document order, read by the
  // annotation-anchors effect below.
  const annotationAnchorsRef = useRef<AnnotationAnchor[]>([]);
  // CodeView computes header heights from a global constant, so a note taller
  // than it through `renderHeaderMetadata` breaks the layout math and no
  // `version` bump can fix it. Annotation slots ARE DOM-measured, so a note
  // enters the annotation system instead. These paths got one placed;
  // `renderHeaderMetadata` suppresses the header for them and stays the
  // fallback for files with no visible line to anchor to.
  const notePlacedPathsRef = useRef<Set<string>>(new Set());
  // CodeView's `syncItemRecord` keys reconciliation off each item's `version`:
  // a matching version — including two `undefined`s — means "keep the cached
  // record", so new annotations without a bump never reach the DOM. Only a file
  // whose own signature changed gets a bump, so an untouched file keeps its
  // item object and version and is skipped entirely. Which file owns which
  // number does not matter, only that values stay distinct.
  const itemsVersionRef = useRef(0);
  // Independent of the item cache: parsing depends only on the shown content,
  // so this can hit on a signature miss (comments changed, content did not).
  const parseCacheRef = useRef<Map<string, ParsedFileCacheEntry>>(new Map());
  // A hit puts the SAME item object back into `items`; identity is what lets
  // `syncItemRecord` see a matching version and do nothing for that file.
  const fileItemCacheRef = useRef<Map<string, FileItemCacheEntry>>(new Map());
  // Reset and repopulated each items-memo run; syncCardClasses toggles
  // `.present-tour-card-pending` from it.
  const pendingPathsRef = useRef<Set<string>>(new Set());
  // Settled AND admitted for parsing. Admission is decoupled from settling so
  // a burst resolving in one tick is still parsed a few files per frame.
  const [readyPaths, setReadyPaths] = useState<ReadonlySet<string>>(() => new Set());

  // A mermaid diagram is measured as a small placeholder, then grows by
  // hundreds of px when its SVG lands; CodeView's cached layout never learns
  // that on its own, so completion has to force a `version` bump. rAF-coalesced
  // so several diagrams settling in one frame produce a single bump.
  const [diagramLayoutTick, setDiagramLayoutTick] = useState(0);
  const diagramLayoutRafRef = useRef<number | null>(null);
  const handleDiagramLayoutChange = useCallback(() => {
    if (diagramLayoutRafRef.current !== null) return;
    diagramLayoutRafRef.current = requestAnimationFrame(() => {
      diagramLayoutRafRef.current = null;
      setDiagramLayoutTick((tick) => tick + 1);
    });
  }, []);
  useEffect(() => {
    return () => {
      if (diagramLayoutRafRef.current !== null) cancelAnimationFrame(diagramLayoutRafRef.current);
    };
  }, []);

  // Keyed globally (the anchor key carries the filepath) so drafts on several
  // files can be open at once.
  const [drafts, setDrafts] = useState<Record<string, DraftAnchor>>({});
  const draftKeys = useMemo(() => Object.keys(drafts), [drafts]);

  // Deliberately OUTSIDE React state: any state a keystroke touched would bump
  // a file's version on every character typed. `CommentForm` owns the live
  // value; this is remount insurance, since CodeView virtualizes and an
  // annotation slot can unmount and re-seed from `draftContent`.
  const draftContentsRef = useRef<Map<string, string>>(new Map());

  const openDraft = useCallback((filepath: string, side: AnnotationSide, start: number, end: number) => {
    const key = anchorKeyOf(filepath, side, start);
    setDrafts((current) => {
      if (current[key]) return current;
      draftContentsRef.current.set(key, '');
      return { ...current, [key]: { filepath, side, start, end } };
    });
  }, []);

  // Stable identity, no setState: a keystroke must never touch component state.
  const updateDraftContent = useCallback((key: string, content: string) => {
    draftContentsRef.current.set(key, content);
  }, []);

  const closeDraft = useCallback((key: string) => {
    draftContentsRef.current.delete(key);
    setDrafts((current) => {
      if (!(key in current)) return current;
      const { [key]: _removed, ...rest } = current;
      return rest;
    });
  }, []);

  // Escape closes the most-recently-opened draft first, across ALL files.
  const handleEscapeDraft = useCallback(() => {
    if (draftKeys.length === 0) return;
    closeDraft(draftKeys[draftKeys.length - 1]);
  }, [draftKeys, closeDraft]);
  useEscapeStack(handleEscapeDraft, draftKeys.length > 0);
  useEscapeStack(onCancelEdit, editingCommentId !== null);

  // Freeze a file's shown content while it has an open draft or hosts the
  // comment being edited; per file, since many render at once here.
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
  // Drop frozen snapshots once a form closes, so the next change is adopted.
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

  // CodeView is in the DOM. The effects below need only that, not final content.
  const tourMounted = files.length > 0;

  // Every file admitted into its final form, not merely fetched. A scroll
  // issued while cards are zero-hunk placeholders can land short or no-op, and
  // nothing re-scrolls as real content grows in above the target — so the
  // scroll-replay effect below re-fires when this flips true. An errored file
  // needs no admission and counts as settled once it stops loading.
  const allSettled =
    files.length > 0 &&
    files.every((f) => {
      if (f.diff.loading) return false;
      // Mirrors the items memo's error branch: settled with nothing to show is
      // rendered as an error, and needs no admission.
      const isErrorCard = Boolean(f.diff.error) || f.diff.original === undefined || f.diff.modified === undefined;
      return isErrorCard || readyPaths.has(f.path);
    });

  // The primary parse site for newly-settled files, one rAF slice at a time so
  // many fetches resolving in one tick cannot stall the main thread.
  useEffect(() => {
    const admittable = files.filter(
      (f) => !f.diff.loading && !f.diff.error && f.diff.original !== undefined && f.diff.modified !== undefined && !readyPaths.has(f.path)
    );
    // Paths for files gone from the manifest, dropped so the set stays bounded
    // across rounds. A file that went back to `loading` deliberately KEEPS its
    // membership: the memo routes it to the pending branch anyway, and it is
    // ready again the instant it re-settles with the same content.
    const currentPaths = new Set(files.map((f) => f.path));
    const stale = Array.from(readyPaths).filter((p) => !currentPaths.has(p));
    if (admittable.length === 0 && stale.length === 0) return;

    const raf = requestAnimationFrame(() => {
      const sliceStart = performance.now();
      const admitted: string[] = [];
      const SLICE_BUDGET_MS = 8;
      for (const file of admittable) {
        ensureParsedFile(parseCacheRef.current, file.path, file.diff.original as string, file.diff.modified as string);
        admitted.push(file.path);
        if (performance.now() - sliceStart > SLICE_BUDGET_MS) break; // always admits at least one file per slice
      }
      setReadyPaths((current) => {
        const next = new Set(current);
        for (const path of admitted) next.add(path);
        for (const path of stale) next.delete(path);
        return next;
      });
    });
    return () => cancelAnimationFrame(raf);
    // Re-running on readyPaths is what schedules the next slice: this effect
    // drives itself.
  }, [files, readyPaths]);

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
      if (meta.kind === 'note') {
        return (
          <div key={key} className="present-tour-file-note-slot">
            <Markdown className="present-tour-file-note" onDiagramLayoutChange={handleDiagramLayoutChange}>
              {meta.noteMarkdown ?? ''}
            </Markdown>
          </div>
        );
      }
      return (
        // CodeView exposes no id hook on annotation slots, so the N/P scroll
        // effect locates the thread by this attribute.
        <div key={key} data-anchor-key={key}>
          <DiffCommentThread
            comments={meta.comments}
            draft={meta.draft}
            editingCommentId={editingCommentId}
            readOnlyCommentIds={readOnlyCommentIds}
            showSendToClaude={!!onSendToClaude}
            draftContent={meta.draft ? draftContentsRef.current.get(key) ?? '' : undefined}
            onDraftContentChange={meta.draft ? (content) => updateDraftContent(key, content) : undefined}
            onSaveDraft={(content) => handleSaveDraft(key, content)}
            onCancelDraft={() => closeDraft(key)}
            onStartEdit={onStartEdit}
            onEditComment={onEditComment}
            onCancelEdit={onCancelEdit}
            onResolveComment={onResolveComment}
            onDeleteComment={onDeleteComment}
            onSendComment={handleSendComment}
            caption={meta.outsideDiffNote}
            onReply={meta.draft ? undefined : () => openDraft(meta.filepath, meta.side, meta.lineNumber, meta.lineNumber)}
            onDiagramLayoutChange={handleDiagramLayoutChange}
          />
        </div>
      );
    },
    [
      editingCommentId,
      readOnlyCommentIds,
      onSendToClaude,
      updateDraftContent,
      handleSaveDraft,
      closeDraft,
      onStartEdit,
      onEditComment,
      onCancelEdit,
      onResolveComment,
      onDeleteComment,
      handleSendComment,
      openDraft,
      handleDiagramLayoutChange,
    ]
  );

  // One CodeViewItem per manifest file, in reading order, running well before
  // every diff has settled. Each file's cheap signature plus its shown content
  // decides between reusing the exact cached item object and rebuilding — and a
  // rebuild still hits parseCacheRef when only comments or review state moved.
  const items = useMemo<CodeViewItem<AnnotationMeta>[]>(() => {
    annotationAnchorsRef.current = [];
    notePlacedPathsRef.current = new Set();
    pendingPathsRef.current = new Set();

    const result = files.map((file): CodeViewItem<AnnotationMeta> => {
      const { diff } = file;
      const cached = fileItemCacheRef.current.get(file.path);

      // A still-loading file legitimately has no content yet — that is the
      // pending branch. Missing content on a settled file is the error case.
      if (diff.error || (!diff.loading && (diff.original === undefined || diff.modified === undefined))) {
        // The 'error' discriminator stops a file that flips between branches
        // from signature-matching its stale entry from the other one.
        const signature = JSON.stringify(['error', diff.error ?? 'Failed to load this file’s diff.']);
        if (cached && cached.signature === signature && cached.shownOriginal === undefined && cached.shownModified === undefined) {
          if (cached.notePlaced) notePlacedPathsRef.current.add(file.path);
          annotationAnchorsRef.current.push(...cached.anchors);
          return cached.item;
        }
        const item: CodeViewItem<AnnotationMeta> = {
          id: file.path,
          type: 'file',
          file: { name: file.path, contents: diff.error ?? 'Failed to load this file’s diff.' },
          version: ++itemsVersionRef.current,
        };
        fileItemCacheRef.current.set(file.path, { signature, item, anchors: [], notePlaced: false });
        return item;
      }

      if (diff.loading || !readyPaths.has(file.path)) {
        // Not yet admitted: a zero-hunk header-only shell. Deliberately
        // annotation-less, or CodeView would re-measure the card on the swap.
        pendingPathsRef.current.add(file.path);
        const signature = JSON.stringify(['pending']);
        if (cached && cached.signature === signature) return cached.item;
        const emptyFile: FileContents = { name: file.path, contents: '' };
        const item: CodeViewItem<AnnotationMeta> = {
          id: file.path,
          type: 'diff',
          fileDiff: parseDiffFromFile(emptyFile, emptyFile),
          annotations: [],
          version: ++itemsVersionRef.current,
        };
        fileItemCacheRef.current.set(file.path, {
          signature,
          shownOriginal: undefined,
          shownModified: undefined,
          item,
          anchors: [],
          notePlaced: false,
        });
        return item;
      }

      // Both branches above returned for every undefined case; TS cannot follow
      // that across two conditionals, so narrow once here.
      const original = diff.original as string;
      const modified = diff.modified as string;

      const frozen = frozenRef.current.get(file.path);
      if (formOpenByFile.has(file.path) && !frozen) {
        frozenRef.current.set(file.path, { original, modified });
      }
      const shown = frozenRef.current.get(file.path) ?? { original, modified };

      const fileComments = commentsByFile.get(file.path) ?? [];
      const fileDraftKeys = draftsByFile.get(file.path) ?? [];
      // Checks both sources rather than assuming which collection an id is in.
      const editingBelongsToFile =
        fileComments.some((c) => c.id === editingCommentId) || fileDraftKeys.includes(editingCommentId ?? '');
      const hasMermaid = fileHasMermaid(file, fileComments);

      // Every input affecting this file's built item. Comment content and
      // resolved state are embedded in the item's metadata snapshot, so ids
      // alone are not enough. `diagramLayoutTick` enters only for a file that
      // could hold a settling diagram (see fileHasMermaid).
      const signature = JSON.stringify([
        'diff',
        file.note ?? null,
        fileComments.map((c) => [
          c.id,
          c.content,
          c.line_start,
          c.line_end,
          c.resolved,
          c.resolved_by ?? null,
          c.author,
          annotationCommentIds?.has(c.id) ?? false,
        ]),
        fileDraftKeys.map((key) => {
          const d = drafts[key];
          return [key, d.side, d.start, d.end];
        }),
        editingBelongsToFile ? editingCommentId : null,
        reviewedPaths.has(file.path),
        hasMermaid ? diagramLayoutTick : null,
      ]);

      if (cached && cached.signature === signature && cached.shownOriginal === shown.original && cached.shownModified === shown.modified) {
        if (cached.notePlaced) notePlacedPathsRef.current.add(file.path);
        annotationAnchorsRef.current.push(...cached.anchors);
        return cached.item;
      }

      // Usually already parsed by the admission effect; this covers content
      // changing while ready, e.g. a frozen file's form closing.
      const { fileDiff, visibleLineRanges, lineCounts } = ensureParsedFile(parseCacheRef.current, file.path, shown.original, shown.modified);

      const groups = new Map<string, AnnotationMeta>();
      for (const comment of fileComments) {
        const side: AnnotationSide = isOriginalSideComment(comment) ? 'deletions' : 'additions';
        const max = side === 'deletions' ? lineCounts.deletions : lineCounts.additions;
        const lineExists = comment.line_start >= 1 && comment.line_start <= max;
        // A stale line past the file's own length is dropped, whatever the
        // comment kind.
        if (!lineExists) continue;
        const ranges = visibleLineRanges[side];
        let line = comment.line_start;
        let outsideDiffNote: string | undefined;
        if (!isLineInRanges(line, ranges)) {
          // A collapsed-hunk line is dropped too, EXCEPT for author
          // annotations, which legitimately point at unchanged code and get
          // re-anchored to the nearest visible line.
          if (!annotationCommentIds?.has(comment.id)) continue;
          const nearest = nearestVisibleLine(line, ranges);
          if (nearest === null) continue; // file has no visible lines at all on this side
          line = nearest;
          const originalEnd = Math.abs(comment.line_end);
          const rangeText = comment.line_start === originalEnd ? `${comment.line_start}` : `${comment.line_start}–${originalEnd}`;
          outsideDiffNote = `refers to line ${rangeText}, outside the visible diff`;
        }
        const key = anchorKeyOf(file.path, side, line);
        let group = groups.get(key);
        if (!group) {
          group = { filepath: file.path, side, lineNumber: line, comments: [], draft: false, anchorKey: key };
          groups.set(key, group);
        }
        if (outsideDiffNote && !group.outsideDiffNote) group.outsideDiffNote = outsideDiffNote;
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

      // Doc-order anchors for N/P, by rendered line — independent of the
      // active/rest split below, which is paint order, not hop order.
      const fileAnnotationGroups = all
        .filter((g) => g.comments.some((c) => annotationCommentIds?.has(c.id)))
        .sort((a, b) => a.lineNumber - b.lineNumber);
      // Stashed on the cache entry so a future hit replays it without
      // rebuilding `groups`.
      const fileAnchors: AnnotationAnchor[] = fileAnnotationGroups.map((g) => ({ path: file.path, anchorKey: g.anchorKey }));
      annotationAnchorsRef.current.push(...fileAnchors);

      const hasOpenForm = (g: AnnotationMeta) => g.draft || g.comments.some((c) => c.id === editingCommentId);
      const active = all.filter(hasOpenForm).sort((a, b) => a.anchorKey.localeCompare(b.anchorKey));
      const rest = all.filter((g) => !hasOpenForm(g));

      // A note anchors to the first visible line so it is DOM-measured (see
      // notePlacedPathsRef). A diff with no visible line on either side cannot
      // anchor one and falls back to the header path.
      let noteAnnotation: DiffLineAnnotation<AnnotationMeta> | undefined;
      let notePlaced = false;
      if (file.note) {
        const additionsStart = visibleLineRanges.additions[0]?.[0];
        const deletionsStart = visibleLineRanges.deletions[0]?.[0];
        const side: AnnotationSide | undefined = additionsStart !== undefined ? 'additions' : deletionsStart !== undefined ? 'deletions' : undefined;
        const lineNumber = side === 'additions' ? additionsStart : deletionsStart;
        if (side !== undefined && lineNumber !== undefined) {
          const noteMeta: AnnotationMeta = {
            kind: 'note',
            noteMarkdown: file.note,
            filepath: file.path,
            side,
            lineNumber,
            comments: [],
            draft: false,
            anchorKey: `note:${file.path}`,
          };
          noteAnnotation = { side, lineNumber, metadata: noteMeta };
          notePlaced = true;
          notePlacedPathsRef.current.add(file.path);
        }
      }

      const annotations: DiffLineAnnotation<AnnotationMeta>[] = [
        ...(noteAnnotation ? [noteAnnotation] : []),
        ...[...active, ...rest].map((meta) => ({
          side: meta.side,
          lineNumber: meta.lineNumber,
          metadata: meta,
        })),
      ];

      const item: CodeViewItem<AnnotationMeta> = { id: file.path, type: 'diff', fileDiff, annotations, version: ++itemsVersionRef.current };
      fileItemCacheRef.current.set(file.path, {
        signature,
        shownOriginal: shown.original,
        shownModified: shown.modified,
        item,
        anchors: fileAnchors,
        notePlaced,
      });
      return item;
    });

    // Drop entries for files gone from the manifest: a path that leaves and
    // returns would otherwise hit stale content, and the caches would grow.
    const currentPaths = new Set(files.map((f) => f.path));
    for (const path of Array.from(fileItemCacheRef.current.keys())) {
      if (!currentPaths.has(path)) fileItemCacheRef.current.delete(path);
    }
    for (const path of Array.from(parseCacheRef.current.keys())) {
      if (!currentPaths.has(path)) parseCacheRef.current.delete(path);
    }

    return result;
    // The three refs are mutated in place and captured deliberately, so they
    // are not deps. reviewedPaths, annotationCommentIds, diagramLayoutTick, and
    // readyPaths are deps only so a change re-runs the memo and lets each
    // file's signature decide whether it needs a new version.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    files,
    commentsByFile,
    draftsByFile,
    drafts,
    formOpenByFile,
    editingCommentId,
    reviewedPaths,
    annotationCommentIds,
    diagramLayoutTick,
    readyPaths,
  ]);

  // An effect, not a callback inside the memo: that memo runs during render and
  // must stay side-effect-free.
  useEffect(() => {
    onAnnotationAnchorsChange?.(annotationAnchorsRef.current);
  }, [items, onAnnotationAnchorsChange]);

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
  const groupByPath = useMemo(() => new Map(files.map((f) => [f.path, f.group ?? 'tour'])), [files]);

  // The library's items carry no className slot, so card classes are applied
  // imperatively through `getRenderedItems()`. CodeView virtualizes — scrolling
  // mounts new elements without `items` changing — so handleScroll calls this
  // too; the effect below only covers what is rendered when the list settles.
  const syncCardClasses = useCallback(() => {
    const instance = handleRef.current?.getInstance();
    if (!instance) return;
    for (const rendered of instance.getRenderedItems()) {
      const isSkip = groupByPath.get(rendered.id) === 'skip';
      rendered.element.classList.toggle('present-tour-card-skip', isSkip);
      rendered.element.classList.toggle('present-tour-card-pending', pendingPathsRef.current.has(rendered.id));
    }
  }, [groupByPath]);

  useEffect(() => {
    if (!tourMounted) return;
    syncCardClasses();
  }, [tourMounted, items, groupByPath, syncCardClasses]);

  const renderHeaderMetadata = useCallback(
    (item: CodeViewItem<AnnotationMeta>) => {
      // The header path is only the fallback for files with no visible line to
      // anchor a note annotation to. A pending file is excluded too: rendering
      // its note here would mount the Markdown/Mermaid early and remount it
      // once the real annotation takes over on admission.
      if (notePlacedPathsRef.current.has(item.id) || pendingPathsRef.current.has(item.id)) return null;
      const note = noteByPath.get(item.id);
      if (!note) return null;
      return (
        <Markdown className="present-tour-file-note" onDiagramLayoutChange={handleDiagramLayoutChange}>
          {note}
        </Markdown>
      );
    },
    [noteByPath, handleDiagramLayoutChange]
  );

  const renderHeaderPrefix = useCallback(
    (item: CodeViewItem<AnnotationMeta>) => {
      // Skipped files aren't part of review progress — no toggle to show.
      if (groupByPath.get(item.id) === 'skip') return null;
      const isReviewed = reviewedPaths.has(item.id);
      return (
        <button
          type="button"
          className={`present-tour-reviewed-toggle ${isReviewed ? 'is-reviewed' : ''}`}
          onClick={(e) => {
            e.stopPropagation();
            onToggleReviewed(item.id);
          }}
          title={isReviewed ? 'Mark as not reviewed' : 'Mark as reviewed'}
        >
          <span className="present-tour-reviewed-check">{isReviewed ? '✓' : '○'}</span>
          <span className="present-tour-reviewed-label">{isReviewed ? 'Reviewed' : 'Mark reviewed'}</span>
          <kbd>R</kbd>
        </button>
      );
    },
    [reviewedPaths, onToggleReviewed, groupByPath]
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

  // DiffView's cold-window scroll-pin defense, on the tour's own container. A
  // ref, not a closure local, because the scrollTo effect below also arms it.
  const userTookOverRef = useRef(false);
  // tourMounted as of the previous run of the scroll-replay effect, to detect
  // the false->true transition rather than "a scroll has ever fired".
  const wasMountedRef = useRef(false);
  // True while a programmatic smooth scroll settles: handleScroll swallows
  // passive reports so the animation's own events cannot fight the navigation
  // that triggered them. A real user gesture clears it immediately.
  const passiveSuppressedRef = useRef(false);
  // Each settling scroll event re-arms this, so suppression lifts ~200ms after
  // the last one rather than a fixed delay from the scroll's start.
  const suppressQuietTimerRef = useRef<number>(0);
  // `tourMounted` must be a dep: on first render containerRef is null (the
  // loading branch renders), so an empty dep array would attach these listeners
  // to nothing, forever. It also re-attaches if the manifest drops to zero
  // files and back, when the container itself was swapped out.
  useEffect(() => {
    const scroller = containerRef.current;
    if (!scroller) return;
    const takeover = () => {
      userTookOverRef.current = true;
      passiveSuppressedRef.current = false;
      window.clearTimeout(suppressQuietTimerRef.current);
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
      window.clearTimeout(suppressQuietTimerRef.current);
    };
  }, [tourMounted]);

  // Rail / j-k scroll. A null scrollToPath with an advanced nonce means the
  // Summary stop, which has no item id, so the scroller itself goes to the top;
  // `scrollNonce` is what separates that from "no request yet".
  //
  // The request never fires a native gesture ON THE SCROLLER — the rail lives
  // outside PresentTour — yet the library's smooth `scrollTo` does fire native
  // `scroll` events on the container the cold-window pin watches, which fought
  // them back to 0 mid-animation. So arm the pin's flag explicitly here, at
  // scroll time, meaning a request that never scrolled never arms it.
  //
  // wasMountedRef is read before it updates, so it is false exactly on the
  // commit where CodeView mounted and the browser has not laid it out yet.
  // `scrollTo` silently no-ops against an unmeasured layout, so that one case
  // gets a rAF; every other scroll stays synchronous.
  useEffect(() => {
    const wasMounted = wasMountedRef.current;
    wasMountedRef.current = tourMounted;
    const hasRequest = (scrollNonce ?? 0) > 0;
    if (!hasRequest || !tourMounted) return;
    const handle = handleRef.current;
    if (!handle) return;
    userTookOverRef.current = true;
    passiveSuppressedRef.current = true;
    window.clearTimeout(suppressQuietTimerRef.current);
    const performScroll = () => {
      if (scrollToPath) {
        handle.scrollTo({ type: 'item', id: scrollToPath, align: 'start', behavior: 'smooth' });
      } else {
        containerRef.current?.scrollTo({ top: 0, behavior: 'smooth' });
      }
    };
    if (!wasMounted) {
      const raf = requestAnimationFrame(performScroll);
      return () => cancelAnimationFrame(raf);
    }
    performScroll();
    // scrollNonce so repeat clicks re-fire; allSettled so a still-pending
    // request re-fires once the tour has its final layout.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToPath, scrollNonce, tourMounted, allSettled]);

  // N/P annotation hop: scroll the file into view, then find the thread by its
  // data-anchor-key and center it. CodeView virtualizes, so the slot may not be
  // mounted for several frames; a fixed attempt count gave up before a smooth
  // cross-file scroll finished, so the retry runs against a wall-clock budget.
  useEffect(() => {
    if (!scrollToAnnotation) return;
    const hasRequest = (annotationScrollNonce ?? 0) > 0;
    if (!hasRequest || !tourMounted) return;
    const handle = handleRef.current;
    if (!handle) return;
    userTookOverRef.current = true;
    passiveSuppressedRef.current = true;
    window.clearTimeout(suppressQuietTimerRef.current);
    const { path, anchorKey } = scrollToAnnotation;
    handle.scrollTo({ type: 'item', id: path, align: 'start', behavior: 'smooth' });
    const LOCATE_BUDGET_MS = 1500;
    const deadline = Date.now() + LOCATE_BUDGET_MS;
    let raf = 0;
    const tryLocate = () => {
      const el = containerRef.current?.querySelector<HTMLElement>(`[data-anchor-key="${CSS.escape(anchorKey)}"]`);
      if (el) {
        el.scrollIntoView({ block: 'center' });
        return;
      }
      if (Date.now() >= deadline) return;
      raf = requestAnimationFrame(tryLocate);
    };
    raf = requestAnimationFrame(tryLocate);
    return () => cancelAnimationFrame(raf);
    // annotationScrollNonce so re-hopping to the same anchor re-scrolls.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToAnnotation, annotationScrollNonce, tourMounted]);

  // Track the file nearest the viewport top for the rail. CodeView's rendered
  // item roots carry no queryable `data-*` attribute (confirmed against the
  // real shadow DOM), so this goes through `getRenderedItems()`.
  const handleScroll = useCallback(
    (_scrollTop: number) => {
      syncCardClasses();
      if (!onActivePathChange || !containerRef.current) return;
      // Passive tracking starts only once the user takes over: otherwise the
      // initial scroll-pin settling would fold the summary untouched.
      if (!userTookOverRef.current) return;
      // Re-arm the quiet window on every settling event; it clears ~200ms after
      // the last. A scroll that produces no events (target already in view)
      // leaves suppression armed until the user's next gesture.
      if (passiveSuppressedRef.current) {
        window.clearTimeout(suppressQuietTimerRef.current);
        suppressQuietTimerRef.current = window.setTimeout(() => {
          passiveSuppressedRef.current = false;
        }, 200);
        return;
      }
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
    },
    [onActivePathChange, syncCardClasses]
  );

  return (
    <div
      className="present-tour"
      data-testid="present-tour"
      style={fontSize ? ({ '--diffs-font-size': `${fontSize}px` } as React.CSSProperties) : undefined}
    >
      {summary && (
        <div
          className={`present-tour-summary ${summaryVisible ? '' : 'collapsed'}`}
          data-testid="present-tour-summary"
          onWheel={handleSummaryWheel}
        >
          <button
            type="button"
            className="present-tour-summary-toggle"
            data-testid="present-tour-summary-toggle"
            aria-expanded={summaryVisible}
            onClick={() => onSummaryVisibleChange?.(!summaryVisible)}
          >
            <span className={`present-tour-summary-chevron${summaryVisible ? ' is-open' : ''}`} aria-hidden="true">
              ▸
            </span>
            Summary
          </button>
          <div
            className="present-tour-summary-body"
            data-testid="present-tour-summary-body"
            aria-hidden={!summaryVisible}
            ref={summaryBodyRef}
          >
            <Markdown>{summary}</Markdown>
          </div>
        </div>
      )}

      {!tourMounted ? (
        <div className="present-tour-loading">Loading tour…</div>
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
          renderHeaderPrefix={renderHeaderPrefix}
          onScroll={handleScroll}
          disableWorkerPool
        />
      )}
    </div>
  );
}

export default PresentTour;
