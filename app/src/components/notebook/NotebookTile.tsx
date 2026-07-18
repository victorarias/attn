import { forwardRef, useEffect, useMemo, useRef, useState } from 'react';
import { useNotebookSurfaceContext } from '../../contexts/NotebookSurfaceContext';
import { NotebookSurface, type NotebookSurfaceHandle } from '../NotebookSurface';

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
export const NotebookTile = forwardRef<NotebookSurfaceHandle, {
  initialPath: string | null;
  root?: string;
  onOpenFile: (path: string) => void;
}>(function NotebookTile({
  initialPath,
  root,
  onOpenFile,
}, ref) {
  const { makeDaemon, effectiveNotebookRoot, sendFsWatch, sendFsUnwatch, connectionGeneration } = useNotebookSurfaceContext();

  // Off-root: this tile is pinned to a filesystem root other than the notebook
  // storage root. Notebook-only affordances (backlinks, send-to-chief) are
  // gated off below — mirrors the same root-vs-notebook-root comparison the
  // fs_watch effect uses (line ~46) so on-root/off-root agree everywhere.
  const offRoot = !!root && root !== effectiveNotebookRoot;

  // fs_watch_result may normalize `root` (e.g. macOS resolving /tmp to
  // /private/tmp) — and fs_changed events for this subscription carry that
  // resolved form, not the raw prop. Once resolution lands, both the fs_*
  // calls and the changeSignal lookup must use it, or a root-bound tile's
  // live refresh is silently dead. Reset on every root change so a stale
  // resolution from a previous root never leaks into the new one.
  const [resolvedRoot, setResolvedRoot] = useState<string | null>(null);
  const effectiveRoot = root ? (resolvedRoot ?? root) : undefined;
  const daemon = useMemo(() => makeDaemon(effectiveRoot), [makeDaemon, effectiveRoot]);

  // The root this tile is actually subscribed to via fs_watch, so the effect's
  // cleanup unwatches the resolution the daemon echoed back — not necessarily
  // the raw `root` prop, which fs_watch_result may have normalized.
  const watchedRootRef = useRef<string | null>(null);

  useEffect(() => {
    setResolvedRoot(null);
    // The notebook root is watched by the daemon unconditionally; only an
    // arbitrary, distinct root needs this tile to open its own subscription.
    if (!root || root === effectiveNotebookRoot) {
      return;
    }
    // connectionGeneration is a dep (not read below) purely to force this
    // effect to re-run on every fresh WebSocket connect: the daemon drops
    // this tile's explicit fs_watch ref whenever the socket disconnects, but
    // a normal frontend reconnect leaves the tile mounted and these callback
    // identities intact, so nothing else here would re-fire the subscription.
    let cancelled = false;
    sendFsWatch(root)
      .then((result) => {
        if (cancelled) {
          // The tile unmounted (or root changed) before this resolved: the
          // daemon-side watcher was just established, so it must still be
          // dropped here — the cleanup below already ran and saw no
          // watchedRootRef to unwatch, so this is the only chance.
          sendFsUnwatch(result.root).catch((error) => {
            console.warn('[NotebookTile] fs_unwatch failed for root', result.root, error);
          });
          return;
        }
        watchedRootRef.current = result.root;
        setResolvedRoot(result.root);
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
  }, [root, effectiveNotebookRoot, connectionGeneration]);

  return (
    <NotebookSurface
      // Remount on root change: NotebookSurface's init effect only depends on
      // `active`, so without a key change, switching roots via the header
      // switcher leaves selectedPath/note/draft carrying over from the old
      // root while the daemon rebinds underneath — the next autosave can then
      // write the old document's buffer to the same relative path under the
      // NEW root. Keying on `root` forces a fresh mount (fresh state, fresh
      // init effect) on every root change.
      //
      // This is the RAW `root` prop, not effectiveRoot/resolvedRoot above:
      // fs_watch_result can normalize the path (e.g. /tmp -> /private/tmp
      // on macOS), and that normalization must never itself trigger a
      // remount — only a real root change (a new raw prop value) should.
      // The root switcher (WorkspaceDockTile) awaits flushPendingSave()
      // BEFORE updating params, so the old instance persists to the old
      // root before this key ever changes.
      key={root ?? ''}
      ref={ref}
      variant="tile"
      active
      initialPath={initialPath}
      onOpenFile={onOpenFile}
      listDir={daemon.listDir}
      readFile={daemon.readFile}
      writeFile={daemon.writeFile}
      existsFile={daemon.existsFile}
      readAsset={daemon.readAsset}
      backlinksNotebook={offRoot ? undefined : daemon.backlinksNotebook}
      sendToChief={offRoot ? undefined : daemon.sendToChief}
      changeSignal={daemon.changeSignal}
      listFiles={daemon.listFiles}
    />
  );
});
