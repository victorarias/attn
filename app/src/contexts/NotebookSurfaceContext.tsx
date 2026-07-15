import { createContext, useContext, type ReactNode } from 'react';
import type {
  FsEntry,
  FsExistsResult,
  FsReadAssetResult,
  FsReadResult,
  FsWriteResult,
  NotebookEntry,
  NotebookSendToChiefResult,
} from '../hooks/useDaemonSocket';

// The daemon surface a NotebookSurface needs. The fullscreen modal already gets
// these as props from App; a notebook tile lives deep inside the workspace
// split-tree, so it reads them from this context instead of threading ten daemon
// callbacks through the terminal workspace that has nothing to do with the notebook.
export interface NotebookSurfaceDaemon {
  listDir: (path: string) => Promise<FsEntry[]>;
  readFile: (path: string) => Promise<FsReadResult>;
  writeFile: (path: string, content: string, baseHash?: string) => Promise<FsWriteResult>;
  existsFile: (path: string) => Promise<FsExistsResult>;
  readAsset: (path: string) => Promise<FsReadAssetResult>;
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  sendToChief: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  // Walk the whole vault (flat list of notes, with titles) for a tile's fuzzy finder.
  listFiles: () => Promise<NotebookEntry[]>;
  // Bumps when an fs_changed broadcast arrives, so an open tile reloads its file.
  changeSignal: number;
}

const NotebookSurfaceContext = createContext<NotebookSurfaceDaemon | null>(null);

export function NotebookSurfaceProvider({ value, children }: { value: NotebookSurfaceDaemon; children: ReactNode }) {
  return <NotebookSurfaceContext.Provider value={value}>{children}</NotebookSurfaceContext.Provider>;
}

export function useNotebookSurfaceDaemon(): NotebookSurfaceDaemon {
  const ctx = useContext(NotebookSurfaceContext);
  if (!ctx) {
    throw new Error('useNotebookSurfaceDaemon must be used within a NotebookSurfaceProvider');
  }
  return ctx;
}
