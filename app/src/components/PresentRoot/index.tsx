import { useEffect, useMemo, useState } from 'react';
import { useDaemonSocket } from '../../hooks/useDaemonSocket';
import type { Presentation, PresentationRound, PresentationComment } from '../../types/generated';
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

export function PresentRoot() {
  const presentationId = useMemo(
    () => new URLSearchParams(window.location.search).get('presentation'),
    [],
  );

  const [presentation, setPresentation] = useState<Presentation | null>(null);
  const [round, setRound] = useState<PresentationRound | null>(null);
  const [comments, setComments] = useState<PresentationComment[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [daemonSettings, setDaemonSettings] = useState<Record<string, string>>({});
  const [refreshSignal, setRefreshSignal] = useState(0);

  // Fires once, unconditionally, regardless of load/error state below — the
  // splash must come down even if the presentation id is missing or the
  // fetch fails.
  useEffect(() => {
    hideBootSplash();
  }, []);

  const { hasReceivedInitialState, connectionError, getPresentationRound } = useDaemonSocket({
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
      .then(({ presentation: p, round: r, comments: c }) => {
        if (cancelled) return;
        setPresentation(p);
        setRound(r);
        setComments(c);
        setLoadError(null);
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

      {round.manifest.summary && (
        <p className="present-root-summary">{round.manifest.summary}</p>
      )}

      <ol className="present-root-files">
        {round.manifest.files.map((file) => (
          <li key={file.path} className="present-root-file">
            <code>{file.path}</code>
            {file.note && <p className="present-root-file-note">{file.note}</p>}
          </li>
        ))}
      </ol>

      {comments.length > 0 && (
        <p className="present-root-comment-count">
          {comments.length} comment{comments.length === 1 ? '' : 's'} on this round
        </p>
      )}

      <footer className="present-root-footer">Reader coming soon.</footer>
    </div>
  );
}

export default PresentRoot;
