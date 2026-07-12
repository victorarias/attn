import { useNotebookSurfaceDaemon } from '../../contexts/NotebookSurfaceContext';
import { NotebookSurface } from '../NotebookSurface';

// NotebookTile is the in-workspace shape of the Notebook: a `tile`-variant
// NotebookSurface wired to the daemon via context. It reopens to `initialPath`
// (the tile's persisted file) and reports each opened file back so the tile's
// params persist the new path. A tile is always active once mounted.
export function NotebookTile({
  initialPath,
  onOpenFile,
}: {
  initialPath: string | null;
  onOpenFile: (path: string) => void;
}) {
  const daemon = useNotebookSurfaceDaemon();
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
