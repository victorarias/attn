import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getCurrentWindow } from '@tauri-apps/api/window';
import { useDaemonSocket } from '../../hooks/useDaemonSocket';
import { usePresentAutomationBridge } from '../../hooks/usePresentAutomationBridge';
import { usePresentReviewedMarks } from '../../hooks/usePresentReviewedMarks';
import { PresentTour, type PresentTourFile } from '../PresentTour';
import { DriveBar } from './DriveBar';
import type {
  Presentation,
  PresentationRound,
  PresentationComment,
  ReviewComment,
  PresentCommentInput,
} from '../../types/generated';
import type { AnnotationAnchor } from '../PresentTour';
import { hideBootSplash } from '../../utils/bootSplash';
import '../../App.css';
import './PresentRoot.css';

function shortSha(sha: string): string {
  return sha.slice(0, 7);
}

// Mirrors useTheme's data-theme application, without the settings-write half
// (this window never edits the theme, only follows it).
function applyThemeAttribute(preference: string | undefined) {
  if (preference === 'system') {
    document.documentElement.removeAttribute('data-theme');
    return;
  }
  if (preference === 'dark' || preference === 'light') {
    document.documentElement.setAttribute('data-theme', preference);
    return;
  }
  // Not yet loaded (or unrecognized) - match useTheme's DEFAULT_PREFERENCE.
  document.documentElement.setAttribute('data-theme', 'dark');
}

// noop callbacks for the required-but-irrelevant parts of UseDaemonSocketOptions.
// PresentRoot only cares about presentation/round data and theme settings.
function noop() {}

interface DiffCacheEntry {
  loading: boolean;
  original?: string;
  modified?: string;
  error?: string;
}

// A rail/tour document row, unified across the three groups the rail derives
// from a round: the authored tour (manifest.files, in order), Other (paths
// changed in the round but not named by the manifest, alphabetical), and
// Skipped (manifest.skip). Additions/deletions on Other/Skipped rows come
// from the round's changed_files list — the manifest itself only carries
// stats for tour files.
interface DocFile {
  path: string;
  note?: string;
  additions?: number;
  deletions?: number;
  group: 'tour' | 'other' | 'skip';
}

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || target.isContentEditable;
}

// A locally-held, not-yet-submitted comment. line_start/line_end are always
// positive here (unlike ReviewComment's signed line_end); `side` carries the
// old/new distinction explicitly, matching the wire shape so submission is a
// direct field copy.
interface Draft {
  id: string;
  filepath: string;
  line_start: number;
  line_end: number;
  side: 'new' | 'old';
  content: string;
}

// Protocol convention (see app/src/utils/reviewComment.ts and DiffView.tsx):
// a comment anchors at line_start; a NEGATIVE line_end encodes the
// original/deleted side, with abs(line_end) giving the actual end line.
function draftToReviewComment(draft: Draft): ReviewComment {
  return {
    id: draft.id,
    content: draft.content,
    filepath: draft.filepath,
    line_start: draft.line_start,
    line_end: draft.side === 'old' ? -draft.line_end : draft.line_end,
    author: 'user',
    resolved: false,
    created_at: '',
    review_id: '',
  };
}

// Inverse of the sign convention above, applied to DiffView's onAddComment
// callback args (which already arrive pre-encoded the same way).
function draftFromAddComment(filepath: string, lineStart: number, lineEnd: number, content: string, id: string): Draft {
  return {
    id,
    filepath,
    line_start: lineStart,
    line_end: Math.abs(lineEnd),
    side: lineEnd < 0 ? 'old' : 'new',
    content,
  };
}

// PresentationComment (already-submitted, wire shape) uses an explicit
// side string with positive line_end rather than DiffView's signed
// convention, so it needs its own mapper into ReviewComment.
function submittedToReviewComment(comment: PresentationComment): ReviewComment {
  return {
    id: comment.id,
    content: comment.content,
    filepath: comment.filepath,
    line_start: comment.line_start,
    line_end: comment.side === 'old' ? -comment.line_end : comment.line_end,
    author: comment.author,
    resolved: false,
    created_at: comment.created_at,
    review_id: comment.round_id,
  };
}

export function PresentRoot() {
  // Self-gating (isTauri() + __ATTN_AUTOMATION_ENABLED) — a no-op outside a
  // Tauri automation-enabled build. See usePresentAutomationBridge for the
  // present_window_* action set and why this window needs its own bridge
  // rather than sharing the main window's.
  usePresentAutomationBridge();

  const presentationId = useMemo(
    () => new URLSearchParams(window.location.search).get('presentation'),
    [],
  );

  const [presentation, setPresentation] = useState<Presentation | null>(null);
  const [round, setRound] = useState<PresentationRound | null>(null);
  const [comments, setComments] = useState<PresentationComment[]>([]);
  const [repoHeadSha, setRepoHeadSha] = useState<string | undefined>(undefined);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [daemonSettings, setDaemonSettings] = useState<Record<string, string>>({});
  const [refreshSignal, setRefreshSignal] = useState(0);
  // `activePath` drives the rail's highlight; it's updated both by explicit
  // navigation (rail click, j/k) and passively by the tour reporting which
  // file scrolled nearest the top. `scrollRequest` is the tour's imperative
  // "scroll to this file" instruction — kept separate from `activePath` so a
  // passive activePath update from scrolling can never itself re-trigger a
  // programmatic scroll (which would fight the user's own scroll gesture).
  // Passive scroll updates never report null — the Summary stop (null) is
  // only entered explicitly (initial state, rail Summary click, or K from the
  // first file).
  const [activePath, setActivePath] = useState<string | null>(null);
  const [scrollRequest, setScrollRequest] = useState<{ path: string | null; nonce: number } | null>(null);
  const scrollNonceRef = useRef(0);
  const [fileDiffs, setFileDiffs] = useState<Record<string, DiffCacheEntry>>({});
  const [driftDismissed, setDriftDismissed] = useState(false);
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);
  const [showSubmitDialog, setShowSubmitDialog] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const draftIdCounterRef = useRef(0);

  const railRef = useRef<HTMLOListElement>(null);
  // Kept in sync with the loaded round's identity so the diff-fetch effect
  // below can reject stale responses from a PREVIOUS round after a
  // presentation_updated refresh reloads the round (new round.id/base/head).
  const activeRoundKeyRef = useRef<string | null>(null);
  activeRoundKeyRef.current = round ? round.id : null;

  // path=null requests a scroll to the pinned Summary row (stop 0).
  const requestScroll = useCallback((path: string | null) => {
    scrollNonceRef.current += 1;
    setActivePath(path);
    setScrollRequest({ path, nonce: scrollNonceRef.current });
  }, []);

  // Fires once, unconditionally, regardless of load/error state below — the
  // splash must come down even if the presentation id is missing or the
  // fetch fails.
  useEffect(() => {
    hideBootSplash();
  }, []);

  const {
    hasReceivedInitialState,
    connectionError,
    getPresentationRound,
    sendGetFileDiff,
    submitPresentationRound,
    closePresentation,
  } = useDaemonSocket({
    onSessionsUpdate: noop,
    onWorkspacesUpdate: noop,
    onPRsUpdate: noop,
    onReposUpdate: noop,
    onAuthorsUpdate: noop,
    onSettingsUpdate: setDaemonSettings,
    onPresentationUpdated: (updated) => {
      if (presentationId && updated.id === presentationId) {
        setRefreshSignal((n) => n + 1);
      }
    },
  });

  useEffect(() => {
    applyThemeAttribute(daemonSettings.theme);
  }, [daemonSettings]);

  useEffect(() => {
    if (!presentationId) {
      setLoadError('No presentation specified.');
      return;
    }
    if (!hasReceivedInitialState) return;

    let cancelled = false;
    getPresentationRound(presentationId)
      .then(({ presentation: p, round: r, comments: c, repoHeadSha: h }) => {
        if (cancelled) return;
        setPresentation(p);
        setRound(r);
        setComments(c);
        setRepoHeadSha(h);
        setLoadError(null);
        setDriftDismissed(false);
        setActivePath((current) => {
          if (current && r.manifest.files.some((f) => f.path === current)) return current;
          // Default to the pinned Summary stop when the round has one — it
          // matches the actual initial scroll position (top = summary)
          // instead of jumping straight past it to the first file.
          if (r.manifest.summary) return null;
          return r.manifest.files[0]?.path ?? null;
        });
      })
      .catch((err) => {
        if (cancelled) return;
        console.error('[PresentRoot] failed to load presentation round:', err);
        setLoadError(err instanceof Error ? err.message : 'Failed to load presentation.');
      });

    return () => {
      cancelled = true;
    };
  }, [presentationId, hasReceivedInitialState, getPresentationRound, refreshSignal]);

  // tour = manifest.files, in authored order. other = paths the round's
  // changed_files carries that the manifest didn't name (neither tour nor
  // skip), alphabetical. skip = manifest.skip, with stats backfilled from
  // changed_files when available (the manifest itself only carries stats for
  // tour files — see present.go's handleGetPresentationRound). An absent
  // changed_files (old daemon, or a git error) collapses `other` to empty and
  // skip rows lose their stats, but tour/skip still render as before.
  const tourDocFiles = useMemo<DocFile[]>(
    () => (round?.manifest.files ?? []).map((f) => ({ ...f, group: 'tour' as const })),
    [round]
  );
  const changedByPath = useMemo(() => {
    const map = new Map<string, { additions?: number; deletions?: number }>();
    for (const f of round?.changed_files ?? []) map.set(f.path, { additions: f.additions, deletions: f.deletions });
    return map;
  }, [round]);
  const otherDocFiles = useMemo<DocFile[]>(() => {
    const tourPaths = new Set(tourDocFiles.map((f) => f.path));
    const skipPaths = new Set(round?.manifest.skip ?? []);
    return (round?.changed_files ?? [])
      .filter((f) => !tourPaths.has(f.path) && !skipPaths.has(f.path))
      .map((f) => ({ path: f.path, additions: f.additions, deletions: f.deletions, group: 'other' as const }))
      .sort((a, b) => a.path.localeCompare(b.path));
  }, [round, tourDocFiles]);
  const skipDocFiles = useMemo<DocFile[]>(
    () =>
      (round?.manifest.skip ?? []).map((path) => ({
        path,
        additions: changedByPath.get(path)?.additions,
        deletions: changedByPath.get(path)?.deletions,
        group: 'skip' as const,
      })),
    [round, changedByPath]
  );
  // Full tour document order: appended cards after the authored tour.
  const allDocFiles = useMemo<DocFile[]>(
    () => [...tourDocFiles, ...otherDocFiles, ...skipDocFiles],
    [tourDocFiles, otherDocFiles, skipDocFiles]
  );
  // Review progress (marks, "N of M", coverage advisory) covers tour + other
  // only — skipped files were never meant to be walked.
  const progressDocFiles = useMemo<DocFile[]>(() => [...tourDocFiles, ...otherDocFiles], [tourDocFiles, otherDocFiles]);
  const groupByPath = useMemo(() => new Map(allDocFiles.map((f) => [f.path, f.group])), [allDocFiles]);

  // Fetch every tour/other/skip file's diff, pinned to the round's base/head
  // SHAs, with bounded concurrency — the tour renders all of them at once,
  // unlike the old single-selection pane that only ever needed one file at a
  // time. A fresh round (new refreshSignal / new round.id) always gets a
  // fresh fetch pass: `fileDiffs` is reset to all-loading up front.
  useEffect(() => {
    if (!presentation || !round) {
      setFileDiffs({});
      return;
    }

    const paths = allDocFiles.map((f) => f.path);
    const initial: Record<string, DiffCacheEntry> = {};
    for (const path of paths) initial[path] = { loading: true };
    setFileDiffs(initial);

    let cancelled = false;
    const requestedRoundId = round.id;
    const repoPath = presentation.repo_path;
    const baseRef = round.base_sha;
    const headRef = round.head_sha;
    const CONCURRENCY = 4;
    let nextIndex = 0;

    const isStale = () => cancelled || activeRoundKeyRef.current !== requestedRoundId;

    async function worker() {
      for (;;) {
        const index = nextIndex++;
        if (index >= paths.length) return;
        const path = paths[index];
        try {
          const result = await sendGetFileDiff(repoPath, path, { baseRef, headRef });
          if (isStale()) return;
          const entry: DiffCacheEntry = result.success
            ? { loading: false, original: result.original, modified: result.modified }
            : { loading: false, error: result.error || 'Failed to load diff.' };
          setFileDiffs((prev) => ({ ...prev, [path]: entry }));
        } catch (err) {
          if (isStale()) return;
          setFileDiffs((prev) => ({
            ...prev,
            [path]: { loading: false, error: err instanceof Error ? err.message : 'Failed to load diff.' },
          }));
        }
      }
    }

    const workerCount = Math.min(CONCURRENCY, paths.length);
    for (let i = 0; i < workerCount; i++) void worker();

    return () => {
      cancelled = true;
    };
    // round.id/base_sha/head_sha/presentation.repo_path are stable for the
    // loaded round's lifetime; sendGetFileDiff is a stable callback.
  }, [presentation, round, sendGetFileDiff]);

  const progressPaths = useMemo(() => progressDocFiles.map((f) => f.path), [progressDocFiles]);

  const { reviewed, toggleReviewed, markReviewed } = usePresentReviewedMarks(
    presentationId,
    round?.id ?? null,
    progressPaths
  );

  // J/K walk the full appended-tour document order (tour, other, skip cards
  // alike); mark/toggle below are what keep skipped files out of progress.
  // K from the first file (index 0) steps past the clamp onto the pinned
  // Summary stop (activePath null) when the round has a summary, driving the
  // exact same path as a rail summary-row click — matching J's behavior of
  // moving from the summary (activePath null, index -1) onto the first file.
  // K while already on the summary (activePath null, no lower stop to reach)
  // stays a no-op. Without a summary, the old clamp-at-0 behavior holds.
  const hasSummary = !!round?.manifest.summary;
  const moveSelection = useCallback((delta: number) => {
    if (allDocFiles.length === 0) return;
    const index = activePath ? allDocFiles.findIndex((f) => f.path === activePath) : -1;
    if (index === -1) {
      // On the summary (or nothing selected yet): K has nowhere lower to go
      // (no-op), J lands on the first file.
      if (delta < 0) return;
      requestScroll(allDocFiles[0]?.path ?? null);
      return;
    }
    if (delta < 0 && index === 0 && hasSummary) {
      requestScroll(null);
      return;
    }
    const nextIndex = Math.max(0, Math.min(allDocFiles.length - 1, index + delta));
    const next = allDocFiles[nextIndex]?.path;
    if (next) requestScroll(next);
  }, [allDocFiles, activePath, requestScroll, hasSummary]);

  // N/P hop across every annotation anchor in the round, in the document
  // order PresentTour reports (files in tour order, then by rendered line
  // within a file) — independent of the rail's j/k file-level walk above.
  // annotationAnchorIndexRef tracks "current" position in that list across
  // hops; it starts at -1 so the first n lands on the first annotation.
  const [annotationAnchors, setAnnotationAnchors] = useState<AnnotationAnchor[]>([]);
  const annotationAnchorIndexRef = useRef(-1);
  const annotationScrollNonceRef = useRef(0);
  const [annotationScrollRequest, setAnnotationScrollRequest] = useState<
    (AnnotationAnchor & { nonce: number }) | null
  >(null);

  const hopAnnotation = useCallback((delta: number) => {
    if (annotationAnchors.length === 0) return;
    const nextIndex =
      (((annotationAnchorIndexRef.current + delta) % annotationAnchors.length) + annotationAnchors.length) %
      annotationAnchors.length;
    annotationAnchorIndexRef.current = nextIndex;
    const anchor = annotationAnchors[nextIndex];
    annotationScrollNonceRef.current += 1;
    setActivePath(anchor.path);
    setAnnotationScrollRequest({ ...anchor, nonce: annotationScrollNonceRef.current });
  }, [annotationAnchors]);

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (isTypingTarget(e.target)) return;
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return;
      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault();
        // Auto-mark-on-leave (jaunt semantics): advancing past a file marks
        // the one being left as reviewed. Visiting a file alone (arriving,
        // scrolling past it passively) never marks it. Skipped files never
        // get auto-marked — they aren't part of progress.
        if (activePath && groupByPath.get(activePath) !== 'skip') markReviewed(activePath);
        moveSelection(1);
      } else if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault();
        moveSelection(-1);
      } else if (e.key === 'r') {
        e.preventDefault();
        if (activePath && groupByPath.get(activePath) !== 'skip') toggleReviewed(activePath);
      } else if (e.key === 's') {
        e.preventDefault();
        setShowSubmitDialog((current) => {
          if (current) return current;
          setSubmitError(null);
          return true;
        });
      } else if (e.key === 'n') {
        e.preventDefault();
        hopAnnotation(1);
      } else if (e.key === 'p') {
        e.preventDefault();
        hopAnnotation(-1);
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [moveSelection, activePath, markReviewed, toggleReviewed, groupByPath, hopAnnotation]);

  useEffect(() => {
    if (!activePath || !railRef.current) return;
    const el = railRef.current.querySelector<HTMLElement>(`[data-path="${CSS.escape(activePath)}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [activePath]);

  const draftIds = useMemo(() => new Set(drafts.map((d) => d.id)), [drafts]);

  // Manifest author annotations, adapted into the same ReviewComment shape
  // reviewer comments use so the tour can render both through one pipeline.
  // Each annotation's thread entries become individually-addressable
  // comments (id `annot:${path}:${annotationIndex}:${threadIndex}`) so a
  // reply lands as its own ReviewComment once submitted. Annotation line
  // numbers are always the additions/new side (see the manifest schema), so
  // line_end carries through unsigned — matching submittedToReviewComment's
  // convention for a 'new'-side comment.
  const annotationComments = useMemo<ReviewComment[]>(() => {
    const result: ReviewComment[] = [];
    for (const f of round?.manifest.files ?? []) {
      (f.annotations ?? []).forEach((annotation, annotationIndex) => {
        annotation.comments.forEach((content, threadIndex) => {
          result.push({
            id: `annot:${f.path}:${annotationIndex}:${threadIndex}`,
            content,
            filepath: f.path,
            line_start: annotation.line_start,
            line_end: annotation.line_end,
            author: 'agent',
            resolved: false,
            created_at: '',
            review_id: '',
          });
        });
      });
    }
    return result;
  }, [round]);
  const annotationCommentIds = useMemo(
    () => new Set(annotationComments.map((c) => c.id)),
    [annotationComments]
  );

  // All comments across all files: the tour renders every file's diff at
  // once, unlike the old single-selection pane that only ever needed one
  // file at a time. Annotations are prepended so a shared anchor renders the
  // author's notes above any reviewer replies. Memoized (and hoisted above
  // the early-return branches below, since hooks must run unconditionally)
  // so a passive activePath update from scrolling doesn't rebuild these on
  // every render and bump every CodeView item's version.
  const allComments = useMemo<ReviewComment[]>(
    () => [...annotationComments, ...comments.map(submittedToReviewComment), ...drafts.map(draftToReviewComment)],
    [annotationComments, comments, drafts]
  );
  // Submitted comments and annotations (author is the round's owner or
  // agent, not a local draft) render read-only in the reader — only the
  // user's own in-progress drafts are editable/resolvable/deletable here.
  const readOnlyCommentIds = useMemo(
    () => new Set(allComments.filter((c) => !draftIds.has(c.id)).map((c) => c.id)),
    [allComments, draftIds]
  );
  // Rail comment chip: submitted comments (unresolved and resolved alike),
  // annotations, and in-progress drafts, all merged into the one chip — no
  // separate annotation indicator.
  const commentCountByPath = useMemo(() => {
    const counts = new Map<string, number>();
    for (const c of comments) counts.set(c.filepath, (counts.get(c.filepath) ?? 0) + 1);
    for (const d of drafts) counts.set(d.filepath, (counts.get(d.filepath) ?? 0) + 1);
    for (const c of annotationComments) counts.set(c.filepath, (counts.get(c.filepath) ?? 0) + 1);
    return counts;
  }, [comments, drafts, annotationComments]);
  const tourFiles = useMemo<PresentTourFile[]>(
    () =>
      allDocFiles.map((file) => ({
        path: file.path,
        note: file.note,
        diff: fileDiffs[file.path] ?? { loading: true },
        group: file.group,
      })),
    [allDocFiles, fileDiffs]
  );

  const handleAddComment = useCallback((filepath: string, lineStart: number, lineEnd: number, content: string) => {
    const id = `draft-${draftIdCounterRef.current++}`;
    setDrafts((prev) => [...prev, draftFromAddComment(filepath, lineStart, lineEnd, content, id)]);
  }, []);

  const handleStartEdit = useCallback((id: string) => {
    if (draftIds.has(id)) setEditingCommentId(id);
  }, [draftIds]);

  const handleEditComment = useCallback((id: string, content: string) => {
    if (!draftIds.has(id)) return;
    setDrafts((prev) => prev.map((d) => (d.id === id ? { ...d, content } : d)));
    setEditingCommentId(null);
  }, [draftIds]);

  const handleCancelEdit = useCallback(() => {
    setEditingCommentId(null);
  }, []);

  const handleDeleteComment = useCallback((id: string) => {
    if (!draftIds.has(id)) return;
    setDrafts((prev) => prev.filter((d) => d.id !== id));
  }, [draftIds]);

  // Drafts have no resolved state in this chunk, and submitted comments are
  // read-only here, so resolving is a no-op either way.
  const handleResolveComment = useCallback(() => {}, []);

  const handleSubmit = useCallback(async (verdict: 'approved' | 'feedback') => {
    if (!round) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const comments: PresentCommentInput[] = drafts.map((d) => ({
        filepath: d.filepath,
        line_start: d.line_start,
        line_end: d.line_end,
        side: d.side,
        content: d.content,
      }));
      await submitPresentationRound({ roundId: round.id, verdict, comments, handback: true });
      setDrafts([]);
      setShowSubmitDialog(false);
      setRefreshSignal((n) => n + 1);
      try {
        await getCurrentWindow().hide();
      } catch (hideErr) {
        console.warn('[PresentRoot] failed to hide presentation window after submit:', hideErr);
      }
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit review.');
    } finally {
      setSubmitting(false);
    }
  }, [round, drafts, submitPresentationRound]);

  // Dismiss without reviewing: no round submission (drafts are intentionally
  // discarded), no handback — just close the presentation and hide the window.
  const handleClose = useCallback(async () => {
    if (!presentation) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      await closePresentation(presentation.id);
      setShowSubmitDialog(false);
      setRefreshSignal((n) => n + 1);
      try {
        await getCurrentWindow().hide();
      } catch (hideErr) {
        console.warn('[PresentRoot] failed to hide presentation window after close:', hideErr);
      }
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : 'Failed to close presentation.');
    } finally {
      setSubmitting(false);
    }
  }, [presentation, closePresentation]);

  if (connectionError) {
    return (
      <div className="present-root present-root-message">
        <p>{connectionError}</p>
      </div>
    );
  }

  if (loadError) {
    return (
      <div className="present-root present-root-message">
        <p>{loadError}</p>
      </div>
    );
  }

  if (!presentation || !round) {
    return (
      <div className="present-root present-root-message">
        <p>Loading presentation…</p>
      </div>
    );
  }

  const showDrift = !!repoHeadSha && repoHeadSha !== round.head_sha && !driftDismissed;

  // Advisory-only coverage line for the submit dialog: never blocks
  // submission, just tells the user what the tour didn't see them mark.
  // Covers tour + other, never skipped.
  const unreviewedPaths = progressPaths.filter((p) => !reviewed.has(p));
  const COVERAGE_PREVIEW_COUNT = 5;
  const unreviewedCoverageText =
    unreviewedPaths.length > COVERAGE_PREVIEW_COUNT
      ? `${unreviewedPaths.slice(0, COVERAGE_PREVIEW_COUNT).join(', ')}, and ${unreviewedPaths.length - COVERAGE_PREVIEW_COUNT} more`
      : unreviewedPaths.join(', ');

  // Shared row renderer across the Tour/Other/Skipped sections. `index` is
  // 1-based for Tour rows and null for Other/Skipped (no numbering) — a
  // reviewed row always shows a checkmark regardless of section, since Other
  // participates in progress the same as Tour.
  function renderRailRow(file: DocFile, index: number | null) {
    const isReviewed = reviewed.has(file.path);
    const commentCount = commentCountByPath.get(file.path) ?? 0;
    const hasStats = file.additions !== undefined || file.deletions !== undefined;
    return (
      <li
        key={file.path}
        data-path={file.path}
        className={`present-root-file ${file.path === activePath ? 'selected' : ''} ${isReviewed ? 'reviewed' : ''} ${file.group === 'skip' ? 'present-root-file-skipped' : ''}`}
        onClick={() => requestScroll(file.path)}
      >
        <span className="present-root-file-index">{index !== null ? (isReviewed ? '✓' : index) : isReviewed ? '✓' : ''}</span>
        <code className="present-root-file-path">{file.path}</code>
        {file.note && <span className="present-root-file-note-marker" title="Has a note">●</span>}
        {commentCount > 0 && (
          <span className="present-root-file-comment-chip" title={`${commentCount} comment${commentCount === 1 ? '' : 's'}`}>
            {commentCount}
          </span>
        )}
        {hasStats && (
          <span className="present-root-file-stats">
            {file.additions !== undefined && <span className="adds">+{file.additions}</span>}
            {file.deletions !== undefined && <span className="dels">−{file.deletions}</span>}
          </span>
        )}
      </li>
    );
  }

  return (
    <div className="present-root">
      <header className="present-root-topbar">
        <h1 className="present-root-title" title={presentation.title}>{presentation.title}</h1>
        <span className="present-root-kind">{presentation.kind}</span>
        <span className="present-root-repo" title={presentation.repo_path}>{presentation.repo_path}</span>
        <div className="present-root-topbar-right">
          <span className="present-root-round-label">Round {round.seq}</span>
          <span className="present-root-shas">{shortSha(round.base_sha)}…{shortSha(round.head_sha)}</span>
          <span className={`present-root-status ${round.submitted_at ? 'submitted' : 'draft'}`}>{round.submitted_at ? 'Submitted' : 'Draft'}</span>
          {showDrift && (
            <span className="present-root-drift" role="status"
                  title={`The repo has moved on since this round was pinned (${shortSha(round.head_sha)} → ${shortSha(repoHeadSha!)}). Diffs below still show the pinned commits.`}>
              moved on → {shortSha(repoHeadSha!)}
              <button type="button" className="present-root-drift-dismiss" aria-label="Dismiss" onClick={() => setDriftDismissed(true)}>×</button>
            </span>
          )}
          {comments.length > 0 && (
            <span className="present-root-comment-count">{comments.length} comment{comments.length === 1 ? '' : 's'}</span>
          )}
        </div>
      </header>

      {showSubmitDialog && (
        <div className="present-root-submit-overlay" role="dialog" aria-modal="true" aria-label="Submit review">
          <div className="present-root-submit-dialog">
            <h2>Submit review</h2>
            <p>
              {drafts.length} comment{drafts.length === 1 ? '' : 's'}
            </p>
            {unreviewedPaths.length > 0 && (
              <p className="present-root-submit-coverage" data-testid="present-root-submit-coverage">
                Not yet walked: {unreviewedCoverageText}
              </p>
            )}
            {submitError && <div className="present-root-submit-error">{submitError}</div>}
            <div className="present-root-submit-actions">
              <button type="button" onClick={() => setShowSubmitDialog(false)} disabled={submitting}>
                Cancel
              </button>
              <button
                type="button"
                className="present-root-submit-close"
                onClick={handleClose}
                disabled={submitting}
              >
                Close review
              </button>
              <button
                type="button"
                className="present-root-submit-feedback"
                onClick={() => handleSubmit('feedback')}
                disabled={submitting}
              >
                {submitting ? 'Submitting…' : 'Submit feedback'}
              </button>
              <button
                type="button"
                className="present-root-submit-approve"
                onClick={() => handleSubmit('approved')}
                disabled={submitting}
              >
                {submitting ? 'Submitting…' : 'Approve'}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="present-root-body">
        <div className="present-root-rail">
          <div className="present-root-rail-header">
            <span className="present-root-rail-title">Files</span>
            <span className="present-root-rail-count" data-testid="present-root-rail-count">
              {reviewed.size}/{progressPaths.length}
            </span>
          </div>
          <div className="present-root-rail-progress-track">
            <div
              className="present-root-rail-progress-fill"
              style={{ width: `${progressPaths.length > 0 ? (reviewed.size / progressPaths.length) * 100 : 0}%` }}
            />
          </div>

          <ol className="present-root-files" ref={railRef}>
            <li
              key="__summary__"
              data-testid="present-root-summary-row"
              className={`present-root-summary-row ${activePath === null ? 'selected' : ''}`}
              onClick={() => requestScroll(null)}
            >
              <span className="present-root-file-path">Summary</span>
            </li>

            {tourDocFiles.length > 0 && (
              <>
                <li className="present-root-section-header">Tour · {tourDocFiles.length}</li>
                {tourDocFiles.map((file, index) => renderRailRow(file, index + 1))}
              </>
            )}

            {otherDocFiles.length > 0 && (
              <>
                <li className="present-root-section-header">Other · {otherDocFiles.length}</li>
                {otherDocFiles.map((file) => renderRailRow(file, null))}
              </>
            )}

            {skipDocFiles.length > 0 && (
              <>
                <li className="present-root-section-header">Skipped · {skipDocFiles.length}</li>
                {skipDocFiles.map((file) => renderRailRow(file, null))}
              </>
            )}
          </ol>
        </div>

        <main className="present-root-diff-pane">
          {allDocFiles.length === 0 ? (
            <div className="present-root-diff-placeholder">No files in this round.</div>
          ) : (
            <PresentTour
              summary={round.manifest.summary}
              summaryVisible={activePath === null}
              files={tourFiles}
              comments={allComments}
              editingCommentId={editingCommentId}
              readOnlyCommentIds={readOnlyCommentIds}
              onAddComment={handleAddComment}
              onEditComment={handleEditComment}
              onStartEdit={handleStartEdit}
              onCancelEdit={handleCancelEdit}
              onResolveComment={handleResolveComment}
              onDeleteComment={handleDeleteComment}
              scrollToPath={scrollRequest?.path ?? null}
              scrollNonce={scrollRequest?.nonce ?? 0}
              onActivePathChange={setActivePath}
              reviewedPaths={reviewed}
              onToggleReviewed={toggleReviewed}
              annotationCommentIds={annotationCommentIds}
              onAnnotationAnchorsChange={setAnnotationAnchors}
              scrollToAnnotation={annotationScrollRequest}
              annotationScrollNonce={annotationScrollRequest?.nonce ?? 0}
            />
          )}
        </main>
      </div>

      <DriveBar
        reviewedCount={reviewed.size}
        totalCount={progressPaths.length}
        draftCount={drafts.length}
        submitting={submitting}
        hasAnnotations={annotationComments.length > 0}
        onSubmit={() => {
          setSubmitError(null);
          setShowSubmitDialog(true);
        }}
      />
    </div>
  );
}

export default PresentRoot;
