import type { FsEntry, FsExistsResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult, NotebookTask } from '../hooks/useDaemonSocket';
import { NotebookSurface } from './NotebookSurface';

// parseNotebookHref / NotebookHref moved to NotebookSurface with the rest of the
// body; re-exported here so existing importers (and the unit test) are unaffected.
export { parseNotebookHref, type NotebookHref } from './NotebookSurface';

interface NotebookBrowserProps {
  isOpen: boolean;
  onClose: () => void;
  // List one directory's immediate children over the daemon's generic filesystem
  // surface. '' = the notebook root. Drives the lazy folder tree in the sidebar.
  listDir: (path: string) => Promise<FsEntry[]>;
  // Read one file's full bytes + content hash (for hash-CAS edits).
  readFile: (path: string) => Promise<FsReadResult>;
  // Save an edited file (hash-CAS). Omit baseHash to create-only; pass the file's
  // loaded hash to edit. Resolves with the outcome, including a conflict to reconcile.
  writeFile: (path: string, content: string, baseHash?: string) => Promise<FsWriteResult>;
  // Check whether an in-notebook link target exists (no read), to flag broken links
  // in the editor. Only consulted for markdown notes.
  existsFile: (path: string) => Promise<FsExistsResult>;
  // Backlinks ("Linked from") for a markdown note. Notebook-specific (walks .md link
  // graphs), so it is only consulted for .md files.
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  // Hand a highlighted selection to the daemon to deliver to the chief of staff
  // (appends to the chief inbox note + best-effort live PTY nudge). The UI never
  // messages the chief directly. sourcePath is the note the selection came from.
  sendToChief: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  // Increments whenever an fs_changed event arrives, so an open browser re-lists the
  // tree (handled by FileTree) and reloads the open file (covering agent and external
  // writes).
  changeSignal?: number;
  // List the durable runner's tasks (newest-updated first). Resolves empty when
  // the runner is disabled or has no tasks.
  listTasks: () => Promise<NotebookTask[]>;
  // Force a failed|dead task back to queued. Resolves with the requeued task, or
  // null when the task was non-terminal (a no-op retry).
  retryTask: (taskId: string) => Promise<NotebookTask | null>;
  // Increments whenever a notebook_tasks_changed broadcast arrives, so an open
  // Tasks panel re-fetches the list (any runner lifecycle transition).
  taskChangeSignal?: number;
  // The chief-pulse state for the top-bar indicator: true = a chief-of-staff session
  // is working, false = a chief exists but is idle, undefined = no chief session at
  // all (the indicator is hidden). Derived locally by the parent, not a socket call.
  chiefActive?: boolean;
}

// NotebookBrowser is the fullscreen Notebook: the dialog-shell wrapper around a
// `modal`-variant NotebookSurface. The surface stays mounted while closed (its
// state/refs survive a close→reopen), so the open/close gate is the `active` prop,
// not conditional mounting. Tile mode renders the same surface with variant="tile".
export function NotebookBrowser({ isOpen, onClose, ...rest }: NotebookBrowserProps) {
  return <NotebookSurface variant="modal" active={isOpen} onClose={onClose} {...rest} />;
}
