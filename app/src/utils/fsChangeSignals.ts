import type { FsIndexResult, NotebookEntry } from '../hooks/useDaemonSocket';

// The per-root fs_changed signal key: the daemon-resolved root verbatim, or
// `effectiveNotebookRoot` when the event carries no root (the daemon's
// notebook-root events, and — as a safety fallback — any malformed event
// missing the field). A rootless tile and the fullscreen browser look
// themselves up with the same normalization (root='' → effectiveNotebookRoot),
// so they land on this key too.
export function fsChangeSignalKey(root: string, effectiveNotebookRoot: string): string {
  return root || effectiveNotebookRoot;
}

// Pure reducer for the per-root fs_changed signal map: bumps only the key the
// event resolves to, leaving every other root's counter untouched so a tile
// bound to root A never re-renders for a change under root B.
export function bumpFsChangeSignal(
  signals: Record<string, number>,
  root: string,
  effectiveNotebookRoot: string,
): Record<string, number> {
  const key = fsChangeSignalKey(root, effectiveNotebookRoot);
  return { ...signals, [key]: (signals[key] || 0) + 1 };
}

// Adapts fs_index (a flat, root-scoped file listing) to the shape the ⌘P
// finder and NotebookBrowser already expect from notebook_list. size is
// always 0: fs_index deliberately omits stat() per entry to stay fast over
// large repos, and nothing downstream of the finder reads it. A truncated
// result still renders — an incomplete list beats none — but is flagged to
// the console so a silently-cut-off finder doesn't look like a bug report.
export function fsIndexToNotebookEntries(result: FsIndexResult): NotebookEntry[] {
  if (result.truncated) {
    console.warn('[App] fs_index truncated for', result.root, '— finder list is incomplete');
  }
  return result.files.map((path) => ({ path, size: 0 }));
}
