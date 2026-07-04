import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useDaemonSocket } from '../../hooks/useDaemonSocket';
import { DiffView } from '../DiffView';
import type {
  Presentation,
  PresentationRound,
  PresentationComment,
  FileObject,
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
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [diffState, setDiffState] = useState<DiffCacheEntry | null>(null);
  const [driftDismissed, setDriftDismissed] = useState(false);
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [editingCommentId, setEditingCommentId] = useState<string | null>(null);
  const [showSubmitDialog, setShowSubmitDialog] = useState(false);
  const [handback, setHandback] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const draftIdCounterRef = useRef(0);

  // Cache of fetched diffs, keyed by `${round.id}:${path}`, valid for the
  // lifetime of the loaded round. A fresh round (new refreshSignal) gets a
  // fresh cache automatically because the key includes round.id.
  const diffCacheRef = useRef<Map<string, DiffCacheEntry>>(new Map());
  const railRef = useRef<HTMLOListElement>(null);
  // Kept in sync with selectedPath so the diff-fetch effect below can check
  // "is this response still for the current selection" without nesting a
  // setState call inside a setSelectedPath updater (updaters must stay pure).
  const selectedPathRef = useRef<string | null>(null);
  selectedPathRef.current = selectedPath;

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
        diffCacheRef.current = new Map();
        setSelectedPath((current) => {
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

  // Fetch (or serve from cache) the diff for the selected file, pinned to the
  // round's base/head SHAs. Guards against a stale response for a
  // previously-selected file clobbering the currently-selected one.
  useEffect(() => {
    if (!presentation || !round || !selectedPath) {
      setDiffState(null);
      return;
    }

    const cacheKey = `${round.id}:${selectedPath}`;
    const cached = diffCacheRef.current.get(cacheKey);
    if (cached) {
      setDiffState(cached);
      return;
    }

    const loadingEntry: DiffCacheEntry = { loading: true };
    diffCacheRef.current.set(cacheKey, loadingEntry);
    setDiffState(loadingEntry);

    const requestedPath = selectedPath;
    const requestedRoundId = round.id;
    sendGetFileDiff(presentation.repo_path, requestedPath, {
      baseRef: round.base_sha,
      headRef: round.head_sha,
    })
      .then((result) => {
        const entry: DiffCacheEntry = result.success
          ? { loading: false, original: result.original, modified: result.modified }
          : { loading: false, error: result.error || 'Failed to load diff.' };
        diffCacheRef.current.set(`${requestedRoundId}:${requestedPath}`, entry);
        if (selectedPathRef.current === requestedPath) {
          setDiffState(entry);
        }
      })
      .catch((err) => {
        const entry: DiffCacheEntry = {
          loading: false,
          error: err instanceof Error ? err.message : 'Failed to load diff.',
        };
        diffCacheRef.current.set(`${requestedRoundId}:${requestedPath}`, entry);
        if (selectedPathRef.current === requestedPath) {
          setDiffState(entry);
        }
      });
    // round.id/base_sha/head_sha are stable for the loaded round's lifetime;
    // presentation.repo_path likewise. sendGetFileDiff is a stable callback.
  }, [presentation, round, selectedPath, sendGetFileDiff]);

  const files: FileObject[] = round?.manifest.files ?? [];

  const moveSelection = useCallback((delta: number) => {
    setSelectedPath((current) => {
      if (files.length === 0) return current;
      const index = current ? files.findIndex((f) => f.path === current) : -1;
      const nextIndex = index === -1 ? 0 : Math.max(0, Math.min(files.length - 1, index + delta));
      return files[nextIndex]?.path ?? current;
    });
  }, [files]);

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
    if (!selectedPath || !railRef.current) return;
    const el = railRef.current.querySelector<HTMLElement>(`[data-path="${CSS.escape(selectedPath)}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [selectedPath]);

  // An in-progress edit is scoped to whichever file's diff is showing it;
  // switching files should not leave a stale editingCommentId pointing at a
  // comment that's no longer rendered.
  useEffect(() => {
    setEditingCommentId(null);
  }, [selectedPath]);

  const draftIds = useMemo(() => new Set(drafts.map((d) => d.id)), [drafts]);

  const handleAddComment = useCallback((lineStart: number, lineEnd: number, content: string) => {
    if (!selectedPath) return;
    const id = `draft-${draftIdCounterRef.current++}`;
    setDrafts((prev) => [...prev, draftFromAddComment(selectedPath, lineStart, lineEnd, content, id)]);
  }, [selectedPath]);

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
      await submitPresentationRound({ roundId: round.id, comments, handback });
      setDrafts([]);
      setShowSubmitDialog(false);
      setRefreshSignal((n) => n + 1);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : 'Failed to submit review.');
    } finally {
      setSubmitting(false);
    }
  }, [round, drafts, handback, submitPresentationRound]);

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

  const selectedFile = files.find((f) => f.path === selectedPath) ?? null;
  const showDrift = !!repoHeadSha && repoHeadSha !== round.head_sha && !driftDismissed;
  const selectedFileComments: ReviewComment[] = selectedFile
    ? [
        ...comments.filter((c) => c.filepath === selectedFile.path).map(submittedToReviewComment),
        ...drafts.filter((d) => d.filepath === selectedFile.path).map(draftToReviewComment),
      ]
    : [];

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
            <label className="present-root-submit-handback">
              <input
                type="checkbox"
                checked={handback}
                onChange={(e) => setHandback(e.target.checked)}
              />
              Hand back to agent
            </label>
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

      {round.manifest.summary && (
        <div className="present-root-summary">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{round.manifest.summary}</ReactMarkdown>
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
              className={`present-root-file ${file.path === selectedPath ? 'selected' : ''}`}
              onClick={() => setSelectedPath(file.path)}
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
          {!selectedFile ? (
            <div className="present-root-diff-placeholder">No files in this round.</div>
          ) : (
            <>
              {selectedFile.note && (
                <div className="present-root-file-note-banner">
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{selectedFile.note}</ReactMarkdown>
                </div>
              )}

              {diffState?.loading && (
                <div className="present-root-diff-placeholder">Loading diff…</div>
              )}

              {diffState?.error && (
                <div className="present-root-diff-error">{diffState.error}</div>
              )}

              {!diffState?.loading && !diffState?.error && diffState?.original !== undefined && diffState?.modified !== undefined && (
                <DiffView
                  original={diffState.original}
                  modified={diffState.modified}
                  filePath={selectedFile.path}
                  comments={selectedFileComments}
                  editingCommentId={editingCommentId}
                  diffStyle="unified"
                  expandUnchanged={false}
                  onAddComment={handleAddComment}
                  onEditComment={handleEditComment}
                  onStartEdit={handleStartEdit}
                  onCancelEdit={handleCancelEdit}
                  onResolveComment={handleResolveComment}
                  onDeleteComment={handleDeleteComment}
                />
              )}
            </>
          )}
        </main>
      </div>
    </div>
  );
}

export default PresentRoot;
