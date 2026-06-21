import { createContext, useContext, type ReactNode } from 'react';
import type {
  FsEntry,
  FsExistsResult,
  FsReadResult,
  FsWriteResult,
  NotebookEntry,
  NotebookSendToChiefResult,
  NotebookTask,
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
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  sendToChief: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  listTasks: () => Promise<NotebookTask[]>;
  retryTask: (taskId: string) => Promise<NotebookTask | null>;
  // Bumps when an fs_changed / notebook_tasks_changed broadcast arrives, so an open
  // tile reloads its file / refetches its task list.
  changeSignal: number;
  taskChangeSignal: number;
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
