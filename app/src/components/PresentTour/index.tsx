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
 * One design point called out in the slice-1 brief as "investigate, fall
 * back if unworkable" was resolved toward the documented fallback rather
 * than the preferred option, given the size of that slice:
 *
 *   - Summary placement: CodeView's react wrapper renders its own internal
 *     scroll container (via `containerRef`), not a slot host that's known to
 *     tolerate injected sibling DOM. Rather than risk fighting the library's
 *     own layout, the summary card is a flex sibling above the CodeView, not
 *     inside its own scroller — it does not scroll with the tour as a
 *     result. Instead it folds away via `summaryVisible` once the caller
 *     reports the user has scrolled past the pinned Summary stop, so it
 *     doesn't permanently steal space from the diff.
 *
 * CodeView mounts as soon as the manifest has at least one file — it does not
 * wait for every diff fetch to settle. A file whose diff hasn't settled yet
 * renders as a lightweight placeholder item (zero-hunk parse, header-only
 * card, no annotations or note — see the items memo's pending branch); once
 * its fetch settles, a frame-budgeted admission effect (see `readyPaths`)
 * parses it and swaps the real item in, a handful of files per animation
 * frame so no single pass stalls the main thread on a large changeset. A
 * per-file error still gets its own card in-order immediately (rendered as a
 * plain `type: 'file'` item carrying the error text) — it needs no admission.
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
// CodeViewLineSelection/CodeViewOptions are exported from the package root,
// not the /react entry (same split FileDiffOptions has in DiffView.tsx).
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
  /** Which rail section this card belongs to. Drives the skip-card
   * de-emphasis and hides the reviewed toggle on skip cards, since skipped
   * files aren't part of review progress. Defaults to 'tour' semantics when
   * omitted (no de-emphasis, toggle shown). */
  group?: 'tour' | 'other' | 'skip';
}

/** One annotation's render anchor: which file it's on, and the key
 * (`${filepath}:${side}:${line}`) its rendered thread carries as
 * `data-anchor-key`. Used for N/P hopping across the whole round. */
export interface AnnotationAnchor {
  path: string;
  anchorKey: string;
}

export interface PresentTourProps {
  summary?: string;
  /** Whether the summary card is expanded. The card stays mounted so the fold
   * can animate; false collapses it to zero height. */
  summaryVisible?: boolean;
  /** Fires when the user asks to change the summary's expanded state — the
   * manual toggle button, or the overscroll-to-collapse wheel gesture over
   * the card (see `handleSummaryWheel`). The caller owns the actual
   * `summaryVisible` state; this component never flips it itself. */
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
  /** Path the caller wants the tour scrolled to (rail click / j-k). Paired
   * with `scrollNonce` so re-clicking the same file still re-scrolls. */
  scrollToPath?: string | null;
  scrollNonce?: number;
  /** Fires with the path nearest the top of the viewport as the user
   * passively scrolls — never with null. The Summary stop (null) is entered
   * only explicitly (initial state, rail Summary click, or K from the first
   * file), never as a side effect of scrolling to the top of the tour; see
   * `handleScroll` below for why passive tracking can't report it. */
  onActivePathChange?: (path: string | null) => void;
  /** Paths the user has marked reviewed (jaunt-style per-file mark). */
  reviewedPaths: ReadonlySet<string>;
  onToggleReviewed: (path: string) => void;
  /** Ids (within `comments`) that originated from manifest author
   * annotations rather than reviewer replies. Drives read-only rendering
   * (already covered by readOnlyCommentIds), the outside-diff fallback, and
   * the N/P hop list — all annotation-specific behavior. */
  annotationCommentIds?: Set<string>;
  /** Fires whenever the annotation anchors change (new round, items settle),
   * with every annotation's render anchor in document order: files in `files`
   * order, then by rendered line within a file. PresentRoot uses this to
   * drive N/P hopping without duplicating the grouping logic here. */
  onAnnotationAnchorsChange?: (anchors: AnnotationAnchor[]) => void;
  /** Rail-equivalent imperative "scroll to this annotation" instruction for
   * N/P, paired with `annotationScrollNonce` so re-hopping to the same
   * anchor (single annotation in the round) still re-scrolls. */
  scrollToAnnotation?: AnnotationAnchor | null;
  annotationScrollNonce?: number;
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
  /** Set when this group's anchor was re-positioned from a line outside the
   * visible diff hunks (see the outside-diff fallback below); rendered as a
   * caption above the thread. */
  outsideDiffNote?: string;
  /** Marks a synthetic annotation carrying a file note rather than review
   * comments — see the file-note-as-annotation design note below `notePlacedPathsRef`
   * for why notes have to enter the annotation system instead of
   * `renderHeaderMetadata`. */
  kind?: 'note';
  noteMarkdown?: string;
}

// The anchor identifying a draft (which file/side/line range it's attached
// to). Deliberately does NOT carry the live textarea content — see
// draftContentsRef below for why that lives outside React state entirely.
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

// Nearest point to `target` that IS visible, across every hunk range on a
// side. Every line inside a range is visible, so the nearest point within a
// given range is just `target` clamped to that range; the overall nearest is
// whichever range's clamp is closest, with ties broken toward the earlier
// line (matches the outside-diff fallback's stated tie-break).
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

// Whether a file's note or any of its comments carries a mermaid fence. Only
// files that can possibly grow via an async diagram settling need
// `diagramLayoutTick` in their per-file signature (see the items memo) — for
// every other file, including the tick would bump its version (and force a
// wasted re-parse-free-but-still-reconcile pass) on every unrelated diagram
// elsewhere in the round.
function fileHasMermaid(file: PresentTourFile, fileComments: ReviewComment[]): boolean {
  if (file.note?.includes('```mermaid')) return true;
  return fileComments.some((c) => c.content.includes('```mermaid'));
}

// Cached parse/derived-data for one file's currently-shown (original,
// modified) pair — see parseCacheRef's declaration below for why this is kept
// separate from the item cache.
interface ParsedFileCacheEntry {
  original: string;
  modified: string;
  fileDiff: ReturnType<typeof parseDiffFromFile>;
  visibleLineRanges: VisibleLineRanges;
  lineCounts: { additions: number; deletions: number };
}

// Cached built CodeViewItem for one file, plus everything the items memo
// needs to skip rebuilding it — see fileItemCacheRef's declaration below.
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

// Shared parse-and-cache logic for one file's (original, modified) pair, used
// by both the items memo (the rare content-changed-while-ready case) and the
// admission effect (the primary parse site for newly-settled files) — kept in
// one place so the cache-entry shape isn't duplicated between them. A hit
// requires the cached entry's own (original, modified) to match; a miss
// parses, stores, and returns the fresh entry.
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
  // The summary card is a flex sibling above the CodeView scroller (see the
  // module doc), so a wheel gesture with the cursor over the card never
  // reaches the takeover/scroll listeners below — without this, wheeling
  // over the card was a dead zone that neither scrolled the tour nor folded
  // the summary. Wheel-down over the card collapses it, but only once the
  // card's own internal scroll (see `.present-tour-summary-body`'s
  // `overflow-y: auto`) is exhausted, so a tall summary can still be read.
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
  // Populated as a side effect of the items useMemo below (document order:
  // files order, then rendered line within a file) and read back out by the
  // annotation-anchors effect further down — same "mutate a ref inside the
  // items useMemo" pattern this component already uses for frozenRef.
  const annotationAnchorsRef = useRef<AnnotationAnchor[]>([]);
  // CodeView computes item heights analytically from a global header-height
  // constant (see `dist/types.d.ts` `diffHeaderHeight`); a file note rendered
  // through `renderHeaderMetadata` (part of that header) breaks the layout
  // math the moment its content is taller than that constant — no `version`
  // bump can fix it, since the bug is in the height CodeView assumes, not in
  // whether it re-measures. Annotation slots, by contrast, are DOM-measured
  // by CodeView's own ResizeManager, so a note that needs to hold a mermaid
  // diagram has to enter the annotation system as a synthetic "note"
  // annotation on the file's first visible line instead. Populated in the
  // items memo below (paths that got a placed note annotation), consumed by
  // `renderHeaderMetadata` to suppress the header rendering for those files —
  // the header path stays live as a fallback for files with no visible diff
  // line to anchor to (see the memo).
  const notePlacedPathsRef = useRef<Set<string>>(new Set());
  // CodeView's controlled-item reconciliation keys off each item's `version`
  // field (see components/CodeView.js `syncItemRecord`): a matching version
  // — including two `undefined`s — means "no change, keep the cached
  // record", so a brand new `items` array with different annotations but no
  // `version` bump is silently ignored (verified against the real library:
  // annotations built into `items` never reached the DOM without this).
  // A single global counter, but no longer bumped once per recompute of the
  // whole `items` array — that used to mean ANY state change (a keystroke in
  // one file's draft, a `j` auto-mark on another) re-parsed and re-versioned
  // EVERY file, which is the perf bug this per-file cache exists to remove.
  // Instead each file gets its own decision (see the items memo and
  // fileItemCacheRef below): only a file whose own per-file signature changed
  // gets `++itemsVersionRef.current`, so an untouched file keeps the exact
  // same item object (and the exact same version) across the recompute, and
  // CodeView's `syncItemRecord` skips it entirely. The counter only needs to
  // keep producing values distinct from whatever a file last held — which
  // file "owns" which number doesn't matter.
  const itemsVersionRef = useRef(0);
  // Parsed-diff cache, keyed by file path, independent of the item cache
  // below: a file's `parseDiffFromFile`/visible-range/line-count derivation
  // depends only on its shown (original, modified) pair, not on comments,
  // drafts, or review state — so this cache can serve a hit (skip re-parsing)
  // even on a signature miss (comments changed but the file's own content
  // didn't), and vice versa.
  const parseCacheRef = useRef<Map<string, ParsedFileCacheEntry>>(new Map());
  // Built-item cache, keyed by file path: holds the last CodeViewItem this
  // component produced for that file, plus everything needed to decide
  // whether it can be reused as-is (see the items memo's per-file signature).
  // A cache hit means the SAME item object goes back into the `items` array
  // (not just an equal one) — object identity is what lets CodeView's
  // `syncItemRecord` see a matching version and do nothing at all for that
  // file, not merely a cheap diff.
  const fileItemCacheRef = useRef<Map<string, FileItemCacheEntry>>(new Map());
  // Paths whose item is currently the pending placeholder rather than the
  // real diff — reset and repopulated each items-memo run (same lifecycle as
  // notePlacedPathsRef above), consumed by syncCardClasses below to toggle
  // `.present-tour-card-pending` on the rendered card.
  const pendingPathsRef = useRef<Set<string>>(new Set());
  // Paths whose diff has settled AND been admitted for parsing (see the
  // admission effect below). A file's item stays the pending placeholder
  // until its path lands here — admission is deliberately decoupled from
  // "settled" so a burst of files settling in the same tick still gets
  // parsed a few at a time per animation frame, not all at once.
  const [readyPaths, setReadyPaths] = useState<ReadonlySet<string>>(() => new Set());

  // Mermaid diagrams (in file notes and annotation bodies) render
  // asynchronously: the item is first measured against a short "loading"
  // placeholder, then the SVG lands and the header/annotation grows by
  // hundreds of px. CodeView's cached layout never learns about that growth
  // on its own, so a diagram completing has to force the same `version` bump
  // reviewedPaths already forces above — this tick is folded into that memo's
  // deps for exactly that purpose. rAF-coalesced so several diagrams settling
  // in the same frame (a file note landing at the same time as annotations
  // below it) produce one bump instead of one per diagram.
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

  // Per-anchor draft storage, keyed globally (filepath is already part of the
  // anchor key) so a draft on file A and a draft on file B can be open at once
  // — the multi-draft parity requirement this slice must keep.
  const [drafts, setDrafts] = useState<Record<string, DraftAnchor>>({});
  const draftKeys = useMemo(() => Object.keys(drafts), [drafts]);

  // Live textarea content per draft, deliberately OUTSIDE React state: the
  // items memo below feeds the file-item cache (see the per-file signature),
  // and any state a keystroke touched would bump that file's — or worse,
  // every file's — version on every character typed (the perf bug this slice
  // fixes). `CommentForm` in DiffCommentThread.tsx owns the live value in its
  // own local state; this ref exists only as remount insurance — CodeView
  // virtualizes and portals can unmount/remount an annotation slot (scroll
  // far away and back, or a version bump), and on remount the form re-seeds
  // from `draftContent`. Written on every keystroke, read only when a form
  // (re)mounts.
  const draftContentsRef = useRef<Map<string, string>>(new Map());

  const openDraft = useCallback((filepath: string, side: AnnotationSide, start: number, end: number) => {
    const key = anchorKeyOf(filepath, side, start);
    setDrafts((current) => {
      if (current[key]) return current;
      draftContentsRef.current.set(key, '');
      return { ...current, [key]: { filepath, side, start, end } };
    });
  }, []);

  // Stable identity, no setState: a keystroke must never touch PresentTour
  // state (see draftContentsRef's declaration).
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

  // CodeView mounts as soon as there's at least one manifest file — it no
  // longer waits for every diff to settle (see the module doc). Drives the
  // effects below that used to gate on "every diff settled": they only need
  // CodeView to actually be in the DOM, not for its content to be final.
  const tourMounted = files.length > 0;

  // Frame-budgeted parse admission: the primary site where a newly-settled
  // file's diff actually gets parsed (the items memo's own parse fallback
  // below only covers the rarer case of a ready file's content changing).
  // Runs one rAF-scheduled slice at a time rather than parsing every
  // just-settled file synchronously, so a changeset where many fetches
  // resolve in the same tick doesn't stall the main thread in one pass.
  useEffect(() => {
    const admittable = files.filter(
      (f) => !f.diff.loading && !f.diff.error && f.diff.original !== undefined && f.diff.modified !== undefined && !readyPaths.has(f.path)
    );
    // Paths readyPaths still holds for files no longer in the manifest at
    // all — dropped so the set doesn't grow unbounded across rounds. A file
    // that instead went back to `loading` (a round reload) deliberately
    // KEEPS its stale readyPaths membership: the items memo already routes
    // any `diff.loading` file to the pending branch regardless of
    // readyPaths, and if it re-settles with the same content it's ready
    // again immediately (no extra admission round-trip needed) — the memo's
    // own parse-cache check is what keeps that case free of a re-parse.
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
    // Re-running when readyPaths changes is what schedules the NEXT slice —
    // this effect is its own driver, not a one-shot kickoff.
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
        // data-anchor-key lets the N/P scroll effect below locate this
        // thread's mounted element without CodeView exposing an id hook of
        // its own on rendered annotation slots.
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

  // Build one CodeViewItem per manifest file, in reading order — CodeView
  // mounts as soon as there's at least one file (see the module doc), so this
  // runs well before every diff has settled. Per file, this first computes a
  // cheap "signature" of everything that affects that file's rendered
  // payload (see fileItemCacheRef above) and compares it — together with the
  // shown (original, modified) strings — to what produced the file's last
  // cached item. A match reuses that exact item object (no re-parse, no new
  // version); a miss rebuilds it, using parseCacheRef to skip re-parsing if
  // only comments/drafts/review-state changed rather than the file's own
  // content.
  const items = useMemo<CodeViewItem<AnnotationMeta>[]>(() => {
    annotationAnchorsRef.current = [];
    notePlacedPathsRef.current = new Set();
    pendingPathsRef.current = new Set();

    const result = files.map((file): CodeViewItem<AnnotationMeta> => {
      const { diff } = file;
      const cached = fileItemCacheRef.current.get(file.path);

      // A still-loading file legitimately has no original/modified yet (see
      // PresentRoot's initial `{ loading: true }` diff state) — that's the
      // pending case below, not an error. Only missing content on a file
      // that ISN'T loading is the anomalous "settled with nothing to show"
      // case this treats as an error.
      if (diff.error || (!diff.loading && (diff.original === undefined || diff.modified === undefined))) {
        // Discriminator ('error' vs 'diff'/'pending' below) so a file that
        // flips between an errored and a settled diff across renders can
        // never signature-match its own stale cache entry from the other
        // branch. An error needs no admission — it renders as soon as this
        // file settles, regardless of its siblings.
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
        // Not yet admitted (see the admission effect above) — a cheap
        // zero-hunk placeholder so the card renders as a header-only shell
        // rather than blocking the whole tour. Deliberately carries no
        // annotations: a note or thread here would render through the same
        // header/annotation paths the real item uses once admitted, and
        // CodeView would have to re-measure the card's height on the swap —
        // notes/threads simply wait for the real item, same as the diff body.
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

      // Both branches above already returned for every case where original/
      // modified could be undefined (not-loading-with-no-content is the
      // error branch; still-loading is the pending branch just above) — TS
      // can't follow that across the two separate conditionals, so this
      // narrows explicitly rather than threading `as string` through every
      // use below.
      const original = diff.original as string;
      const modified = diff.modified as string;

      const frozen = frozenRef.current.get(file.path);
      if (formOpenByFile.has(file.path) && !frozen) {
        frozenRef.current.set(file.path, { original, modified });
      }
      const shown = frozenRef.current.get(file.path) ?? { original, modified };

      const fileComments = commentsByFile.get(file.path) ?? [];
      const fileDraftKeys = draftsByFile.get(file.path) ?? [];
      // editingCommentId only ever names a saved comment, never a draft key,
      // but this checks both sources per the same "does this belong to me"
      // logic rather than assuming which collection it can appear in.
      const editingBelongsToFile =
        fileComments.some((c) => c.id === editingCommentId) || fileDraftKeys.includes(editingCommentId ?? '');
      const hasMermaid = fileHasMermaid(file, fileComments);

      // Every input that affects this file's built item. Comment content and
      // resolved state are embedded in the annotation metadata snapshot the
      // item carries, so any of those changing has to produce a new
      // signature (and thus a new version) even though the comment's `id`
      // alone wouldn't. `diagramLayoutTick` is included only when this file
      // could possibly hold a settling diagram (see fileHasMermaid) so an
      // unrelated diagram elsewhere in the round never bumps this file.
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

      // Usually already parsed by the admission effect above (this is the
      // rarer content-changed-while-ready case — e.g. a frozen file's form
      // closes and its shown content moves back to the live diff).
      const { fileDiff, visibleLineRanges, lineCounts } = ensureParsedFile(parseCacheRef.current, file.path, shown.original, shown.modified);

      const groups = new Map<string, AnnotationMeta>();
      for (const comment of fileComments) {
        const side: AnnotationSide = isOriginalSideComment(comment) ? 'deletions' : 'additions';
        const max = side === 'deletions' ? lineCounts.deletions : lineCounts.additions;
        const lineExists = comment.line_start >= 1 && comment.line_start <= max;
        // A line that doesn't exist at all (stale line number past the
        // file's own length) is dropped regardless of comment kind — parity
        // note: DiffView surfaces these via a collapsible "not visible"
        // banner; this slice drops that affordance (see PR report).
        if (!lineExists) continue;
        const ranges = visibleLineRanges[side];
        let line = comment.line_start;
        let outsideDiffNote: string | undefined;
        if (!isLineInRanges(line, ranges)) {
          // A collapsed-hunk line otherwise gets dropped the same way — EXCEPT
          // manifest author annotations, which can legitimately point at
          // unchanged code far from any hunk. Those get re-anchored to the
          // nearest visible line instead of disappearing.
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

      // Doc-order anchor list for N/P: this file's annotation-bearing groups,
      // by rendered line — independent of the active/rest render-order split
      // just below, which is about which thread paints an open form first,
      // not hop order. Consumed by the annotation-anchors effect after this
      // memo commits (see annotationAnchorsRef's declaration for why a ref).
      const fileAnnotationGroups = all
        .filter((g) => g.comments.some((c) => annotationCommentIds?.has(c.id)))
        .sort((a, b) => a.lineNumber - b.lineNumber);
      // Also the anchor list stashed on this file's cache entry below, so a
      // future cache hit can replay it into annotationAnchorsRef without
      // rebuilding `groups`.
      const fileAnchors: AnnotationAnchor[] = fileAnnotationGroups.map((g) => ({ path: file.path, anchorKey: g.anchorKey }));
      annotationAnchorsRef.current.push(...fileAnchors);

      const hasOpenForm = (g: AnnotationMeta) => g.draft || g.comments.some((c) => c.id === editingCommentId);
      const active = all.filter(hasOpenForm).sort((a, b) => a.anchorKey.localeCompare(b.anchorKey));
      const rest = all.filter((g) => !hasOpenForm(g));

      // A file note anchors to the first visible line (additions, falling
      // back to deletions) so it renders as a real DOM-measured annotation —
      // see notePlacedPathsRef's declaration for why. A file with no visible
      // hunk lines on either side (fully-context diff) can't anchor one; that
      // case falls back to the header path via notePlacedPathsRef staying
      // empty for it.
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

    // Drop cache entries for files no longer in the manifest — otherwise a
    // path that briefly leaves and later returns with the same signature
    // would wrongly hit stale cached content, and the caches would grow
    // unbounded across rounds.
    const currentPaths = new Set(files.map((f) => f.path));
    for (const path of Array.from(fileItemCacheRef.current.keys())) {
      if (!currentPaths.has(path)) fileItemCacheRef.current.delete(path);
    }
    for (const path of Array.from(parseCacheRef.current.keys())) {
      if (!currentPaths.has(path)) parseCacheRef.current.delete(path);
    }

    return result;
    // frozenRef, parseCacheRef, and fileItemCacheRef are refs (mutated
    // in-place above); their contents are captured deliberately as part of
    // this computation and don't need to be deps. reviewedPaths,
    // annotationCommentIds, and diagramLayoutTick are included purely so a
    // change in any of them re-runs this memo and lets the per-file signature
    // comparison decide which files actually need a new version — the same
    // reasoning editingCommentId and drafts already need for their own
    // per-file signature contribution. None of these bump every file's
    // version unconditionally anymore (see fileItemCacheRef's declaration);
    // an unrelated file's signature comes out identical and that file's cache
    // entry is reused as-is. readyPaths is included so a file's admission
    // (see the effect above) re-runs this memo and swaps its placeholder for
    // the real item.
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

  // Notify the parent of the current annotation anchor list once items has
  // committed (annotationAnchorsRef was populated by the memo above). A plain
  // effect, not a callback inside the memo itself — that memo runs during
  // render and must stay side-effect-free.
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

  // The library's items don't carry an arbitrary className slot, so the
  // skip-card de-emphasis (see .present-tour-card-skip in PresentTour.css)
  // and the pending-card dimming (see .present-tour-card-pending, driven by
  // pendingPathsRef from the items memo) are applied imperatively to each
  // rendered card's root element — the same `getRenderedItems()` escape hatch
  // handleScroll below already relies on. CodeView virtualizes: scrolling can
  // mount new item elements without the `items` array changing, so this also
  // has to re-run on every scroll (see handleScroll) — this effect alone only
  // covers items already rendered when the item list settles.
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
      // The note already rendered as an annotation for this file (see
      // notePlacedPathsRef) — this header path is only the fallback for
      // files with no visible diff line to anchor a note annotation to. A
      // pending (not yet admitted) file is also excluded here even though
      // it isn't in notePlacedPathsRef either — its placeholder item is
      // deliberately note-less (see the items memo's pending branch), and
      // rendering the note here in the meantime would mount its Markdown/
      // MermaidDiagram early, then remount it again once the real
      // annotation takes over on admission.
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

  // Scroll-pin: apply DiffView's cold-window defense (see the long comment
  // there) to the tour's own scroll container. Arms once per mount. Shared
  // as a ref (not a closure-local variable) because the rail/j-k scrollTo
  // effect below also needs to arm it — see that effect's comment for why.
  const userTookOverRef = useRef(false);
  // Tracks tourMounted as of the previous run of the scroll-replay effect
  // below, to detect the specific false->true transition (CodeView just
  // mounted this commit) rather than "a scroll has ever fired". See that
  // effect's comment.
  const wasMountedRef = useRef(false);
  // True while a programmatic smooth scroll (rail/j-k or N/P) is settling —
  // handleScroll swallows passive reports during this window so the
  // animation's own scroll events can't fight the explicit navigation that
  // triggered it (e.g. K from the first file scrolling to the summary must
  // not have its own settling events report the first file and re-fold the
  // summary). A real user gesture (see `takeover` below) clears this
  // immediately, since a user's own scroll always wins over a still-animating
  // programmatic one.
  const passiveSuppressedRef = useRef(false);
  // Quiet-window timer backing passiveSuppressedRef: each settling scroll
  // event re-arms it (see handleScroll), so suppression lifts ~200ms after
  // the last such event rather than on a fixed delay from when the
  // programmatic scroll started.
  const suppressQuietTimerRef = useRef<number>(0);
  // Must attach once CodeView actually mounts, not just on this component's
  // own mount: on first render there may be no manifest files yet, so
  // `tourMounted` is still false, the "Loading tour…" branch renders instead
  // of CodeView, and `containerRef.current` is null — an empty dep array
  // would run this effect exactly once against that null ref and never
  // again, permanently disarming the takeover/cold-window-pin listeners (the
  // summary card would then never fold, since passive scroll reports never
  // arm `userTookOverRef`). `tourMounted` in the deps re-runs the effect the
  // moment CodeView mounts; the cleanup also re-runs if the manifest ever
  // drops to zero files and back (tourMounted true -> false -> true), which
  // is correct since the container itself may have been swapped out during
  // the loading branch.
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

  // Rail-driven / j-k-driven scroll: nonce forces a re-scroll even to the
  // same path (e.g. re-pressing j at the last file). A null scrollToPath
  // paired with an advanced nonce means "scroll to the summary" (stop 0):
  // the rail's pinned Summary row has no item id to scroll to, so this
  // resets the scroller itself to the top instead of asking CodeView to
  // resolve an item. `scrollNonce` (not scrollToPath) is what distinguishes
  // "no request yet" (nonce still at its initial 0) from an explicit
  // scroll-to-summary request, since scrollToPath is null in both cases.
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
  //
  // `tourMounted` is in the deps so a request that arrives while CodeView
  // isn't mounted yet (handleRef.current is null / rows aren't laid out) is
  // not lost: this effect no-ops without touching userTookOverRef while
  // there are no files yet, then re-runs and performs the still-pending
  // scroll once tourMounted flips true. The arming stays here (at actual
  // scroll time), not in a separate loading-phase branch, so a request that
  // never got to scroll never marks the pin as taken over.
  //
  // A rail/j-k scroll to a file whose diff hasn't settled yet lands on its
  // placeholder card; the real content grows in place once admitted — no
  // special handling needed, same as any other item height change.
  //
  // wasMountedRef tracks whether the PREVIOUS run of this same effect already
  // saw tourMounted=true. It updates on every run (even the early-return
  // ones, so a mount that happens with no scroll pending is still recorded)
  // and is read before that update — so it's true exactly when CodeView had a
  // prior commit to lay itself out in before this scroll, and false the one
  // time tourMounted just flipped true THIS render (CodeView mounts and this
  // effect fires in the same commit, before the browser has laid out the
  // fresh container). CodeView.scrollTo silently no-ops if it can't resolve a
  // destination against an unmeasured layout, so that one case gets a rAF to
  // let layout settle first; every other scroll (rail/j-k on an
  // already-mounted tour) stays perfectly synchronous, unchanged from before.
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
    // scrollNonce intentionally included so repeat clicks on the same path (or
    // repeat clicks on Summary) re-fire.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToPath, scrollNonce, tourMounted]);

  // N/P annotation hop: first get the target file into view via the same
  // item-scroll CodeView uses for the rail/j-k path above, then locate the
  // specific thread by its data-anchor-key (see renderAnnotation) and center
  // it. CodeView virtualizes, so a just-scrolled-into-view file's annotation
  // slot may not be mounted yet on the very next frame — retry on every
  // animation frame until the anchor mounts or a time budget elapses, rather
  // than risk polling forever. A fixed attempt-count cap under-scrolled the
  // first hop into a far, unmounted file: a smooth cross-file `scrollTo`
  // routinely outlasts a handful of frames, so the poll gave up before the
  // anchor ever mounted. A wall-clock budget tolerates that variance instead.
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
    // annotationScrollNonce intentionally included so re-hopping to the same
    // anchor (e.g. a round with a single annotation) still re-scrolls.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToAnnotation, annotationScrollNonce, tourMounted]);

  // Track the file nearest the top of the viewport so the rail can highlight
  // it. There is no `data-*` attribute on CodeView's rendered item roots to
  // query by id (confirmed against the real shadow DOM output, not just the
  // types), so this uses the library's own `getRenderedItems()` — each
  // record carries the real mounted `element` for that item — rather than a
  // guessed selector.
  const handleScroll = useCallback(
    (_scrollTop: number) => {
      syncCardClasses();
      if (!onActivePathChange || !containerRef.current) return;
      // Mount/cold-window-pin scroll noise never reports — passive tracking
      // only starts once the user has taken over with a real gesture (see the
      // takeover listeners above). Without this, the initial scroll-pin
      // settling could fold the summary before the user ever touched
      // anything.
      if (!userTookOverRef.current) return;
      // A programmatic smooth scroll (rail/j-k, N/P) is still settling — its
      // own scroll events must not fight the explicit navigation that
      // triggered them (e.g. K from the first file scrolling to the summary
      // must not have the animation's own events report the first file and
      // re-fold the summary). Re-arm the quiet window on every such event; it
      // clears itself ~200ms after the last one. If the programmatic scroll
      // produces no events at all (target already in view), suppression just
      // stays armed until the user's next gesture clears it.
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
