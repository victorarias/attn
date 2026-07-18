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
  // Notebook-storage only (see the boundary note below) — unaffected by `root`.
  listFiles: () => Promise<NotebookEntry[]>;
  // Bumps when an fs_changed broadcast arrives for this daemon's root, so an
  // open tile reloads its file. Scoped per-root: a root-bound tile only sees
  // this move for its own root, not for unrelated fs activity elsewhere.
  changeSignal: number;
}

// Builds a NotebookSurfaceDaemon bound to `root` (undefined = the notebook
// storage root, same behavior as before per-root tiles existed). Each fs_*
// call carries `root` down to the daemon; `changeSignal` is likewise sliced to
// events for that root.
//
// CRITICAL BOUNDARY: backlinksNotebook, sendToChief, and listFiles are
// Notebook-storage commands on the daemon side (notebook_backlinks,
// notebook_send_to_chief, notebook_list) — they are bound to the notebook
// root exactly as before regardless of the `root` argument here. Passing an
// arbitrary filesystem root into those notebook_* commands is out of scope
// and forbidden; UI for them on a non-notebook-rooted tile is gated off
// separately (see the arbitrary-roots plan's authorization-boundary note).
export type MakeNotebookSurfaceDaemon = (root?: string) => NotebookSurfaceDaemon;

export interface NotebookSurfaceContextValue {
  makeDaemon: MakeNotebookSurfaceDaemon;
  // settings['notebook.root.effective'], threaded through so a tile can tell
  // whether its own root differs from the (always-watched) notebook root and
  // therefore needs its own fs_watch/fs_unwatch subscription.
  effectiveNotebookRoot: string;
  sendFsWatch: (root?: string) => Promise<FsWatchResult>;
  sendFsUnwatch: (root?: string) => Promise<FsWatchResult>;
  // Bumps on every fresh WebSocket connect (including reconnects). The daemon
  // drops explicit fs_watch refs whenever a client's socket disconnects, so a
  // root-bound tile must re-issue fs_watch after each new generation or it
  // silently stops seeing external fs_changed events post-reconnect.
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
