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
  // Flat file list for a tile's ⌘P finder, root-scoped: fs_index over this
  // daemon's `root` (or the notebook root when `root` is undefined). See the
  // boundary note below — unlike backlinksNotebook/sendToChief, this one DOES
  // follow `root`.
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
// CRITICAL BOUNDARY: backlinksNotebook and sendToChief are Notebook-storage
// commands on the daemon side (notebook_backlinks, notebook_send_to_chief) —
// they stay bound to the notebook root exactly as before regardless of the
// `root` argument here. Backlinks only exist between notebook notes and "send
// to chief" appends to the notebook inbox, so widening either to an arbitrary
// filesystem root is out of scope and forbidden; NotebookTile omits both
// capabilities (passes undefined) when its tile is bound to an off-root
// (non-notebook) root, so NotebookSurface never renders their UI there (see
// the arbitrary-roots plan's authorization-boundary note). listFiles is DIFFERENT: it now sources
// fs_index, which the daemon resolves through the same root-scoped chokepoint
// as every other fs_* command, so it follows `root` like listDir/readFile/etc.
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
