import { useEffect, useMemo, useRef } from 'react';
import { useNotebookSurfaceContext } from '../../contexts/NotebookSurfaceContext';
import { NotebookSurface } from '../NotebookSurface';

// NotebookTile is the in-workspace shape of the Notebook: a `tile`-variant
// NotebookSurface wired to the daemon via context. It reopens to `initialPath`
// (the tile's persisted file) and reports each opened file back so the tile's
// params persist the new path. A tile is always active once mounted.
//
// `root`, when set, pins the tile to an arbitrary filesystem root instead of
// the notebook storage root (editor-over-arbitrary-roots). A root-bound tile
// also owns its own fs_watch subscription — the notebook root is watched by
// the daemon unconditionally, but nothing else is, so a tile is the one
// deciding when it needs live updates for its root and dropping that
// subscription when it stops needing them.
export function NotebookTile({
  initialPath,
  root,
  onOpenFile,
}: {
  initialPath: string | null;
  root?: string;
  onOpenFile: (path: string) => void;
}) {
  const { makeDaemon, effectiveNotebookRoot, sendFsWatch, sendFsUnwatch } = useNotebookSurfaceContext();
  const daemon = useMemo(() => makeDaemon(root), [makeDaemon, root]);

  // The root this tile is actually subscribed to via fs_watch, so the effect's
  // cleanup unwatches the resolution the daemon echoed back — not necessarily
  // the raw `root` prop, which fs_watch_result may have normalized.
  const watchedRootRef = useRef<string | null>(null);

  useEffect(() => {
    // The notebook root is watched by the daemon unconditionally; only an
    // arbitrary, distinct root needs this tile to open its own subscription.
    if (!root || root === effectiveNotebookRoot) {
      return;
    }
    let cancelled = false;
    sendFsWatch(root)
      .then((result) => {
        if (cancelled) {
          return;
        }
        watchedRootRef.current = result.root;
      })
      .catch((error) => {
        // The tile still works without live refresh (e.g. the daemon's watch
        // cap is reached) — just no fs_changed-driven reload for this root.
        console.warn('[NotebookTile] fs_watch failed for root', root, error);
      });
    return () => {
      cancelled = true;
      const watchedRoot = watchedRootRef.current;
      watchedRootRef.current = null;
      if (!watchedRoot) {
        return;
      }
      sendFsUnwatch(watchedRoot).catch((error) => {
        console.warn('[NotebookTile] fs_unwatch failed for root', watchedRoot, error);
      });
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- sendFsWatch/sendFsUnwatch are stable daemon callbacks
  }, [root, effectiveNotebookRoot]);

  return (
    <NotebookSurface
      variant="tile"
      active
      initialPath={initialPath}
      onOpenFile={onOpenFile}
      listDir={daemon.listDir}
      readFile={daemon.readFile}
      writeFile={daemon.writeFile}
      existsFile={daemon.existsFile}
      readAsset={daemon.readAsset}
      backlinksNotebook={daemon.backlinksNotebook}
      sendToChief={daemon.sendToChief}
      changeSignal={daemon.changeSignal}
      listFiles={daemon.listFiles}
    />
  );
}
