import { forwardRef, useEffect, useMemo, useRef, useState } from 'react';
import { useNotebookSurfaceContext } from '../../contexts/NotebookSurfaceContext';
import { NotebookSurface, type NotebookSurfaceHandle } from '../NotebookSurface';

// The in-workspace shape of the Notebook: a `tile`-variant NotebookSurface wired
// to the daemon via context, reopening to the tile's persisted `initialPath`.
//
// `root` pins the tile to a filesystem root other than the notebook storage
// root. The daemon watches the notebook root unconditionally and nothing else,
// so a root-bound tile owns its own fs_watch subscription.
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

  // Gates the notebook-only affordances below; the fs_watch effect makes the
  // same comparison, so on-root/off-root agree everywhere.
  const offRoot = !!root && root !== effectiveNotebookRoot;

  // fs_watch_result may normalize `root` (/tmp -> /private/tmp), and fs_changed
  // carries the resolved form, so fs_* calls and changeSignal must use it or the
  // live refresh is silently dead. Reset per root change, never leaking a stale one.
  const [resolvedRoot, setResolvedRoot] = useState<string | null>(null);
  const effectiveRoot = root ? (resolvedRoot ?? root) : undefined;
  const daemon = useMemo(() => makeDaemon(effectiveRoot), [makeDaemon, effectiveRoot]);

  // Cleanup must unwatch the resolution the daemon echoed back, not the raw prop.
  const watchedRootRef = useRef<string | null>(null);

  useEffect(() => {
    setResolvedRoot(null);
    // Only a distinct root needs a subscription of its own.
    if (!root || root === effectiveNotebookRoot) {
      return;
    }
    // connectionGeneration is a dep, unread, purely to re-run on every fresh
    // connect: the daemon drops the fs_watch ref on disconnect and nothing else
    // here changes identity, so no other dep would re-subscribe.
    let cancelled = false;
    sendFsWatch(root)
      .then((result) => {
        if (cancelled) {
          // Cleanup already ran with no watchedRootRef, so this late-established
          // watcher can only be dropped here.
          sendFsUnwatch(result.root).catch((error) => {
            console.warn('[NotebookTile] fs_unwatch failed for root', result.root, error);
          });
          return;
        }
        watchedRootRef.current = result.root;
        setResolvedRoot(result.root);
      })
      .catch((error) => {
        // Still usable without live refresh; just no fs_changed-driven reload.
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
      // Remount on root change: the surface's init effect depends only on
      // `active`, so without a fresh key the old root's draft survives the swap
      // and the next autosave writes it under the NEW root. The RAW prop, never
      // the resolved one — a path normalization must not force a remount. The
      // switcher awaits flushPendingSave() before params change, so the old
      // instance has already persisted to the old root.
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
