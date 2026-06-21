import { useEffect, useRef, useState } from 'react';
import type { NotebookEntry } from './useDaemonSocket';

// How long to wait after a change signal before re-walking the vault. A burst of
// fs_changed events (an agent writing several notes) collapses into one refetch.
export const FILE_INDEX_REFETCH_DEBOUNCE_MS = 300;

export interface NotebookFileIndex {
  files: NotebookEntry[];
  loading: boolean;
  error: string | null;
}

// Maintains the flat file index the in-tile finder searches: one notebook_list
// (empty-prefix, whole-vault) walk when the finder's surface mounts, then a
// debounced re-walk whenever `changeSignal` bumps (an fs_changed broadcast), so a
// just-created or renamed note shows up without reopening the tile. Disabled (no
// listFiles, or not a tile) it holds an empty index and never calls the daemon.
//
// Stale responses are dropped via a monotonic token, so a slow walk that resolves
// after a newer one can't overwrite fresher results.
export function useNotebookFileIndex(
  listFiles: (() => Promise<NotebookEntry[]>) | undefined,
  changeSignal: number,
  enabled: boolean,
): NotebookFileIndex {
  const [files, setFiles] = useState<NotebookEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const seqRef = useRef(0);
  // The first fetch after enabling is immediate (the finder wants data now); later
  // refetches driven by changeSignal are debounced to coalesce write bursts.
  const didInitialRef = useRef(false);

  useEffect(() => {
    if (!enabled || !listFiles) {
      seqRef.current += 1; // invalidate any in-flight walk
      didInitialRef.current = false;
      setFiles([]);
      setError(null);
      setLoading(false);
      return;
    }
    const delay = didInitialRef.current ? FILE_INDEX_REFETCH_DEBOUNCE_MS : 0;
    didInitialRef.current = true;
    const seq = ++seqRef.current;
    setLoading(true);
    const timer = window.setTimeout(() => {
      void listFiles()
        .then((result) => {
          if (seqRef.current !== seq) return;
          setFiles(result);
          setError(null);
          setLoading(false);
        })
        .catch((err) => {
          if (seqRef.current !== seq) return;
          setError(err instanceof Error ? err.message : 'Could not list notebook files');
          setLoading(false);
        });
    }, delay);
    return () => window.clearTimeout(timer);
  }, [enabled, listFiles, changeSignal]);

  return { files, loading, error };
}
