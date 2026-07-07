import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getCurrentWindow } from '@tauri-apps/api/window';
import { useDaemonSocket } from '../../hooks/useDaemonSocket';
import { usePresentAutomationBridge } from '../../hooks/usePresentAutomationBridge';
import { PresentTour, type PresentTourFile } from '../PresentTour';
import type {
  Presentation,
  PresentationRound,
  PresentationComment,
  ReviewComment,
  PresentCommentInput,
} from '../../types/generated';
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
  const [activePath, setActivePath] = useState<string | null>(null);
  const [scrollRequest, setScrollRequest] = useState<{ path: string; nonce: number } | null>(null);
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

  const requestScroll = useCallback((path: string) => {
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

  // Fetch every manifest file's diff, pinned to the round's base/head SHAs,
  // with bounded concurrency — the tour renders all of them at once, unlike
  // the old single-selection pane that only ever needed one file at a time.
  // A fresh round (new refreshSignal / new round.id) always gets a fresh
  // fetch pass: `fileDiffs` is reset to all-loading up front.
  useEffect(() => {
    if (!presentation || !round) {
      setFileDiffs({});
      return;
    }

    const paths = round.manifest.files.map((f) => f.path);
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

  const files = round?.manifest.files ?? [];

  const moveSelection = useCallback((delta: number) => {
    if (files.length === 0) return;
    const index = activePath ? files.findIndex((f) => f.path === activePath) : -1;
    const nextIndex = index === -1 ? 0 : Math.max(0, Math.min(files.length - 1, index + delta));
    const next = files[nextIndex]?.path;
    if (next) requestScroll(next);
  }, [files, activePath, requestScroll]);

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (isTypingTarget(e.target)) return;
      if (e.key === 'j' || e.key === 'ArrowDown') {
        e.preventDefault();
        moveSelection(1);
      } else if (e.key === 'k' || e.key === 'ArrowUp') {
        e.preventDefault();
        moveSelection(-1);
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [moveSelection]);

  useEffect(() => {
    if (!activePath || !railRef.current) return;
    const el = railRef.current.querySelector<HTMLElement>(`[data-path="${CSS.escape(activePath)}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [activePath]);

  const draftIds = useMemo(() => new Set(drafts.map((d) => d.id)), [drafts]);

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

  const handleSubmit = useCallback(async () => {
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
      await submitPresentationRound({ roundId: round.id, comments, handback: true });
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
  // All comments across all files: the tour renders every file's diff at
  // once, unlike the old single-selection pane.
  const allComments: ReviewComment[] = [
    ...comments.map(submittedToReviewComment),
    ...drafts.map(draftToReviewComment),
  ];
  // Submitted comments (author is the round's owner or agent, not a local
  // draft) render read-only in the reader — only the user's own in-progress
  // drafts are editable/resolvable/deletable here.
  const readOnlyCommentIds = new Set(
    allComments.filter((c) => !draftIds.has(c.id)).map((c) => c.id)
  );
  const tourFiles: PresentTourFile[] = files.map((file) => ({
    path: file.path,
    note: file.note,
    diff: fileDiffs[file.path] ?? { loading: true },
  }));

  return (
    <div className="present-root">
      <header className="present-root-header">
        <h1>{presentation.title}</h1>
        <div className="present-root-meta">
          <span className="present-root-kind">{presentation.kind}</span>
          <span className="present-root-repo">{presentation.repo_path}</span>
        </div>
      </header>

      <section className="present-root-round">
        <span>Round {round.seq}</span>
        <span className="present-root-shas">
          {shortSha(round.base_sha)}…{shortSha(round.head_sha)}
        </span>
        <span className={`present-root-status ${round.submitted_at ? 'submitted' : 'draft'}`}>
          {round.submitted_at ? 'Submitted' : 'Draft'}
        </span>
        <button
          type="button"
          className="present-root-submit-button"
          onClick={() => {
            setSubmitError(null);
            setShowSubmitDialog(true);
          }}
          disabled={submitting}
        >
          {drafts.length > 0 ? `Submit review (${drafts.length})` : 'Submit review'}
        </button>
      </section>

      {showSubmitDialog && (
        <div className="present-root-submit-overlay" role="dialog" aria-modal="true" aria-label="Submit review">
          <div className="present-root-submit-dialog">
            <h2>Submit review</h2>
            <p>
              {drafts.length} comment{drafts.length === 1 ? '' : 's'}
            </p>
            {submitError && <div className="present-root-submit-error">{submitError}</div>}
            <div className="present-root-submit-actions">
              <button type="button" onClick={() => setShowSubmitDialog(false)} disabled={submitting}>
                Cancel
              </button>
              <button type="button" onClick={handleSubmit} disabled={submitting}>
                {submitting ? 'Submitting…' : 'Submit'}
              </button>
            </div>
          </div>
        </div>
      )}

      {showDrift && (
        <div className="present-root-drift" role="status">
          <span>
            The repo has moved on since this round was pinned ({shortSha(round.head_sha)} → {shortSha(repoHeadSha!)}).
            Diffs below still show the pinned commits.
          </span>
          <button
            type="button"
            className="present-root-drift-dismiss"
            aria-label="Dismiss"
            onClick={() => setDriftDismissed(true)}
          >
            ×
          </button>
        </div>
      )}

      {comments.length > 0 && (
        <p className="present-root-comment-count">
          {comments.length} comment{comments.length === 1 ? '' : 's'} on this round
        </p>
      )}

      <div className="present-root-body">
        <ol className="present-root-files" ref={railRef}>
          {files.map((file, index) => (
            <li
              key={file.path}
              data-path={file.path}
              className={`present-root-file ${file.path === activePath ? 'selected' : ''}`}
              onClick={() => requestScroll(file.path)}
            >
              <span className="present-root-file-index">{index + 1}</span>
              <code className="present-root-file-path">{file.path}</code>
              {file.note && <span className="present-root-file-note-marker" title="Has a note">●</span>}
            </li>
          ))}

          {round.manifest.skip.length > 0 && (
            <>
              <li className="present-root-skip-divider">Skipped</li>
              {round.manifest.skip.map((path) => (
                <li key={path} className="present-root-file present-root-file-skipped">
                  <code className="present-root-file-path">{path}</code>
                </li>
              ))}
            </>
          )}
        </ol>

        <main className="present-root-diff-pane">
          {files.length === 0 ? (
            <div className="present-root-diff-placeholder">No files in this round.</div>
          ) : (
            <PresentTour
              summary={round.manifest.summary}
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
            />
          )}
        </main>
      </div>
    </div>
  );
}

export default PresentRoot;
