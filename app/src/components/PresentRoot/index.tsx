import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useDaemonSocket } from '../../hooks/useDaemonSocket';
import { DiffView } from '../DiffView';
import type { Presentation, PresentationRound, PresentationComment, FileObject } from '../../types/generated';
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

  const { hasReceivedInitialState, connectionError, getPresentationRound, sendGetFileDiff } = useDaemonSocket({
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
      </section>

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
                // comments wired in the next chunk
                <DiffView
                  original={diffState.original}
                  modified={diffState.modified}
                  filePath={selectedFile.path}
                  comments={[]}
                  editingCommentId={null}
                  diffStyle="unified"
                  expandUnchanged={false}
                  onAddComment={noop}
                  onEditComment={noop}
                  onStartEdit={noop}
                  onCancelEdit={noop}
                  onResolveComment={noop}
                  onDeleteComment={noop}
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
