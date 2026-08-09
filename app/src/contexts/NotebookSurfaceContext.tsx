import { createContext, useContext, type ReactNode } from 'react';
import type {
  FsEntry,
  FsExistsResult,
  FsReadAssetResult,
  FsReadResult,
  FsWatchResult,
  FsWriteResult,
  NotebookEntry,
  NotebookSendToChiefResult,
} from '../hooks/useDaemonSocket';

// The daemon surface a NotebookSurface needs. The modal gets these as props; a
// tile reads them here rather than threading them through the workspace tree.
export interface NotebookSurfaceDaemon {
  listDir: (path: string) => Promise<FsEntry[]>;
  readFile: (path: string) => Promise<FsReadResult>;
  writeFile: (path: string, content: string, baseHash?: string) => Promise<FsWriteResult>;
  existsFile: (path: string) => Promise<FsExistsResult>;
  readAsset: (path: string) => Promise<FsReadAssetResult>;
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  sendToChief: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  // Flat list for a tile's ⌘P finder. Unlike backlinksNotebook/sendToChief below,
  // this one DOES follow `root`.
  listFiles: () => Promise<NotebookEntry[]>;
  // Bumps on an fs_changed for this daemon's root only, never for other roots.
  changeSignal: number;
}

// Builds a NotebookSurfaceDaemon bound to `root` (undefined = the notebook
// storage root); each fs_* call and changeSignal is scoped to it.
//
// CRITICAL BOUNDARY: backlinksNotebook and sendToChief are Notebook-storage
// commands and stay bound to the notebook root whatever `root` says — widening
// them to an arbitrary filesystem root is forbidden. NotebookTile passes
// undefined for both on an off-root tile, so their UI never renders there.
// listFiles is different: it goes through the same root-scoped fs chokepoint as
// listDir/readFile, so it does follow `root`.
export type MakeNotebookSurfaceDaemon = (root?: string) => NotebookSurfaceDaemon;

export interface NotebookSurfaceContextValue {
  makeDaemon: MakeNotebookSurfaceDaemon;
  // settings['notebook.root.effective']: a tile off this root needs its own watch.
  effectiveNotebookRoot: string;
  sendFsWatch: (root?: string) => Promise<FsWatchResult>;
  sendFsUnwatch: (root?: string) => Promise<FsWatchResult>;
  // Bumps on every fresh connect. The daemon drops fs_watch refs on disconnect,
  // so a root-bound tile must re-issue fs_watch per generation or go blind.
  connectionGeneration: number;
}

const NotebookSurfaceContext = createContext<NotebookSurfaceContextValue | null>(null);

export function NotebookSurfaceProvider({ value, children }: { value: NotebookSurfaceContextValue; children: ReactNode }) {
  return <NotebookSurfaceContext.Provider value={value}>{children}</NotebookSurfaceContext.Provider>;
}

export function useNotebookSurfaceContext(): NotebookSurfaceContextValue {
  const ctx = useContext(NotebookSurfaceContext);
  if (!ctx) {
    throw new Error('useNotebookSurfaceContext must be used within a NotebookSurfaceProvider');
  }
  return ctx;
}
