import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import type { FsEntry, FsExistsResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult, NotebookTask } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { FileTree } from './notebook/FileTree';
import { fileKind, isBinaryPath, isMarkdownPath } from './notebook/fileKind';
import { LiveMarkdownEditor, type LiveMarkdownEditorHandle, type LiveSelection } from './notebook/LiveMarkdownEditor';
import { parseOutline } from './notebook/outline';
import './NotebookBrowser.css';

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
}

// The file shown first when the browser opens with nothing selected, in order of
// preference. knowledge/index.md is the distilled map an agent is told to read. We
// probe these directly (a cheap read) rather than walking the whole tree, since the
// sidebar lists lazily and has no flat catalogue to scan.
const PREFERRED_FIRST = ['knowledge/index.md', 'index.md'];

// How long the buffer must be idle before an autosave fires. Short enough that
// edits persist promptly, long enough to coalesce a burst of keystrokes into one
// write (and one origin=ui broadcast).
const AUTOSAVE_DELAY_MS = 700;

// Outcome of persisting the buffer. On-demand callers (navigate/close) MUST react
// to 'conflict'/'error' — a CAS conflict cannot be silently dropped, or the user's
// edits vanish behind a navigation/close without the banner ever showing.
type PersistOutcome = 'saved' | 'conflict' | 'error' | 'noop';

export function NotebookBrowser({
  isOpen,
  onClose,
  listDir,
  readFile,
  writeFile,
  existsFile,
  backlinksNotebook,
  sendToChief,
  changeSignal = 0,
  listTasks,
  retryTask,
  taskChangeSignal = 0,
}: NotebookBrowserProps) {
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [note, setNote] = useState<FsReadResult | null>(null);
  const [noteError, setNoteError] = useState<string | null>(null);
  const [noteLoading, setNoteLoading] = useState(false);
  const [backlinks, setBacklinks] = useState<NotebookEntry[]>([]);
  // Backlinks load INDEPENDENTLY from (and far slower than) the note content, so the
  // panel needs its own loading flag: without it, a newly selected note would keep
  // showing the PREVIOUS note's "Linked from" list — or worse, falsely assert "No
  // other note links here." — until the slow walk resolves. While true, the panel
  // renders a neutral loading line instead of stale or misleading metadata.
  const [backlinksLoading, setBacklinksLoading] = useState(false);
  // --- Tasks panel (durable runner) ---
  const [tasks, setTasks] = useState<NotebookTask[]>([]);
  const [tasksError, setTasksError] = useState<string | null>(null);
  const [tasksLoading, setTasksLoading] = useState(false);
  // The Tasks section is collapsible; collapsed by default so it doesn't crowd the
  // file tree. Opening it (or a taskChangeSignal bump) triggers a refetch.
  const [tasksOpen, setTasksOpen] = useState(false);
  // Task ids whose Retry click is in flight, so their button can disable without
  // optimistically mutating the row (the broadcast-driven refetch reflects truth).
  const [retryingIds, setRetryingIds] = useState<Set<string>>(new Set());
  // Monotonic load token for the tasks fetch: a slow response from a superseded
  // fetch (panel closed/refetched) is dropped instead of stamping stale rows.
  const tasksSeqRef = useRef(0);
  // selectedPath drives loads; this ref lets the change-signal effect reload the
  // current file without depending on (and re-running for) selectedPath itself.
  const selectedPathRef = useRef<string | null>(null);
  selectedPathRef.current = selectedPath;
  // Monotonic load token, bumped synchronously at the start of every loadFile so
  // a slow response from a superseded navigation is dropped. (A render-synced ref
  // can't do this — it only updates on commit, after the await may have resolved.)
  const loadSeqRef = useRef(0);
  // Persists the outgoing file's unsaved buffer before a navigation/close replaces or
  // hides it — surfacing a CAS conflict rather than dropping it. Assigned below (after
  // the editing state/refs exist) and invoked via a ref so loadFile — declared above
  // the editing block — doesn't depend on declaration order. A no-op until assigned.
  const persistRef = useRef<() => Promise<PersistOutcome>>(async () => 'noop');
  // The dialog container is the deliberate initial focus target so keyboard/AT
  // users land inside the modal (engaging the focus trap) without auto-selecting
  // the Close button. Tab from here moves to the first interactive control.
  const dialogRef = useRef<HTMLDivElement>(null);
  // Imperative handle to the live editor, so the context rail's outline can scroll
  // the editor to a heading (navigation that originates outside the editor).
  const editorRef = useRef<LiveMarkdownEditorHandle>(null);
  // The right context rail's two sections fold independently; both open by default so
  // the outline and backlinks are visible without a click. Local UI state only.
  const [outlineOpen, setOutlineOpen] = useState(true);
  const [backlinksOpen, setBacklinksOpen] = useState(true);

  // Closing with unsaved edits persists them first; if that write conflicts (or
  // errors) we keep the modal open so the conflict banner can be reconciled, rather
  // than losing the buffer behind the close. dirtyRef short-circuits the clean case
  // to an immediate close (no write, no await).
  const requestClose = useCallback(async () => {
    if (dirtyRef.current) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    onClose();
  }, [onClose]);
  const handleEscape = useCallback(() => void requestClose(), [requestClose]);

  useEscapeStack(handleEscape, isOpen);

  // Fetch the durable runner's task list. A transient WS failure surfaces an error
  // rather than silently wiping the rows. The stale-guard drops a response that
  // resolved after a newer fetch (or a panel close).
  const refreshTasks = useCallback(async () => {
    const seq = ++tasksSeqRef.current;
    setTasksLoading(true);
    try {
      const next = await listTasks();
      if (tasksSeqRef.current !== seq) return;
      setTasks(next);
      setTasksError(null);
    } catch (err) {
      if (tasksSeqRef.current !== seq) return;
      setTasksError(err instanceof Error ? err.message : 'Could not load tasks');
    } finally {
      if (tasksSeqRef.current === seq) setTasksLoading(false);
    }
  }, [listTasks]);

  // Force a failed|dead task back to queued. The button is disabled while in
  // flight; on resolve/reject we only clear the in-flight mark — the
  // notebook_tasks_changed broadcast drives the refetch that reflects the truth,
  // so we never optimistically mutate the row here.
  const handleRetry = useCallback(async (taskId: string) => {
    setRetryingIds((prev) => {
      const next = new Set(prev);
      next.add(taskId);
      return next;
    });
    try {
      await retryTask(taskId);
    } catch {
      // A failed retry leaves the row as-is; the next broadcast/refetch reconciles.
    } finally {
      setRetryingIds((prev) => {
        const next = new Set(prev);
        next.delete(taskId);
        return next;
      });
    }
  }, [retryTask]);

  // Load `path` into the document pane. `prefetched` lets a caller that already read
  // the file (the on-open existence probe) seed the editor without a second read.
  const loadFile = useCallback(async (path: string, prefetched?: FsReadResult) => {
    // Persist any unsaved buffer on the file we're leaving before we replace it
    // (covers an edit made in the <debounce window of the autosave timer). If that
    // write conflicts (the file changed on disk) we ABORT the navigation and stay
    // put so the conflict banner can be reconciled — navigating away would discard
    // the buffer and the user's edits would vanish silently.
    if (dirtyRef.current && selectedPathRef.current && selectedPathRef.current !== path) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    const seq = ++loadSeqRef.current;
    setSelectedPath(path);
    // Loading replaces the rendered content (navigation or a same-file live reload),
    // so any floating "Send to chief" button is now mispositioned — drop it.
    setChiefSel(null);
    // Drop the outgoing file's backlinks the moment a new load starts (not when the
    // new walk resolves), so the panel never shows the previous selection's "Linked
    // from" list — the same stale-context bug the content decouple fixed, one panel
    // over.
    setBacklinks([]);
    setBacklinksLoading(false);

    // Binary files have no text editor: don't even read them (fs_read returns a
    // string, meaningless for binary bytes). Show the unsupported placeholder, which
    // the render derives from the selected path's kind. Clear note/draft so a prior
    // file's content doesn't linger behind the placeholder.
    if (isBinaryPath(path)) {
      setNote(null);
      setDraft('');
      setNoteError(null);
      setNoteLoading(false);
      return;
    }

    setNoteError(null);
    if (prefetched) {
      // Already read by the caller; seed the buffer directly. A fresh load is never
      // dirty.
      setNote(prefetched);
      setDraft(prefetched.content);
      setNoteLoading(false);
    } else {
      setNoteLoading(true);
      // Content and backlinks load INDEPENDENTLY — never gated together. The file
      // content is a single fast read; backlinks walks every note in the notebook
      // (reading each body to find links) and is far slower. Apply the content the
      // moment its read resolves; let backlinks fill in whenever it lands. Each guards
      // on the load token so a superseded navigation is dropped.
      void readFile(path)
        .then((value) => {
          if (loadSeqRef.current !== seq) return;
          setNote(value);
          // Seed the live editor buffer from disk; a fresh load is never dirty.
          setDraft(value.content);
          setNoteLoading(false);
        })
        .catch((err) => {
          if (loadSeqRef.current !== seq) return;
          setNote(null);
          setDraft('');
          setNoteError(err instanceof Error ? err.message : 'Could not read this file');
          setNoteLoading(false);
        });
    }
    // Backlinks are a markdown-note concept; only walk the link graph for .md files.
    // A backlinks failure must not blank the file — it just yields no backlinks.
    if (isMarkdownPath(path)) {
      setBacklinksLoading(true);
      void backlinksNotebook(path)
        .then((entries) => {
          if (loadSeqRef.current !== seq) return;
          setBacklinks(entries);
          setBacklinksLoading(false);
        })
        .catch(() => {
          if (loadSeqRef.current !== seq) return;
          setBacklinks([]);
          setBacklinksLoading(false);
        });
    }
  }, [readFile, backlinksNotebook]);

  // Drop the current selection and return the document pane to its empty state.
  // Bumping loadSeqRef invalidates any in-flight loadFile so a response that
  // resolves after this clear cannot resurrect the just-cleared file's content.
  const clearSelection = useCallback(() => {
    loadSeqRef.current += 1;
    setSelectedPath(null);
    setNote(null);
    setDraft('');
    setNoteError(null);
    setNoteLoading(false);
    setBacklinks([]);
    setBacklinksLoading(false);
  }, []);

  // --- Editing (single live surface; no view/edit toggle) ---
  // `draft` is the live editor buffer; `note` holds the value last synced from disk
  // (its content + hash). The file is "dirty" when draft diverges from note.content;
  // dirty edits autosave (debounced) via hash-CAS against note.hash.
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  // Set when an autosave's hash-CAS rejected because the file changed on disk since
  // it was loaded; carries the current on-disk hash so the user can overwrite it.
  const [conflict, setConflict] = useState<{ currentHash?: string } | null>(null);
  const [justSaved, setJustSaved] = useState(false);
  // Refs let the navigation/close persist and the live-refresh guard read the latest
  // draft / synced note / dirty state without re-subscribing every effect to them.
  const draftRef = useRef('');
  draftRef.current = draft;
  const noteRef = useRef<FsReadResult | null>(null);
  noteRef.current = note;
  // Dirty = the buffer diverges from the last synced content. Gates autosave and the
  // live-refresh reload (an unsaved buffer must not be clobbered by a disk reload).
  const dirty = note ? draft !== note.content : false;
  const dirtyRef = useRef(false);
  dirtyRef.current = dirty;

  // --- Send to chief ---
  // The current editor selection and where to float its action button (viewport
  // coords from the selection start). Cleared on navigation/scroll/collapse.
  const [chiefSel, setChiefSel] = useState<LiveSelection | null>(null);
  const [sendingToChief, setSendingToChief] = useState(false);
  // A transient outcome line ("Added to chief's inbox" / an error), auto-dismissed.
  const [chiefStatus, setChiefStatus] = useState<{ text: string; error: boolean } | null>(null);

  // Core write: persist `content` against `baseHash` (hash-CAS) and reconcile the
  // result. Returns the outcome so on-demand callers (navigate/close) can react —
  // a 'conflict'/'error' must NOT be silently dropped. An empty baseHash is a
  // create-only write, used to recreate a file deleted on disk while it was edited.
  const writeBuffer = useCallback(async (baseHash: string, content: string): Promise<PersistOutcome> => {
    const path = selectedPathRef.current;
    if (!path) return 'noop';
    // Freeze the load token so a navigation that lands while this write is in flight
    // is detected on resolve (mirrors loadFile's staleness guard; loadFile/
    // clearSelection bump loadSeqRef, writeBuffer does not). The bytes still reach
    // disk either way — we just don't stamp this file's result onto whatever file is
    // now shown. The OUTCOME still returns so the caller can react.
    const seq = loadSeqRef.current;
    setSaving(true);
    setSaveError(null);
    try {
      const res = await writeFile(path, content, baseHash || undefined);
      const superseded = loadSeqRef.current !== seq || selectedPathRef.current !== path;
      if (res.conflict) {
        // The file diverged on disk; let the user reconcile rather than clobber.
        // (Only surface it if this file is still shown — see `superseded`.)
        if (!superseded) setConflict({ currentHash: res.currentHash });
        return 'conflict';
      }
      if (!superseded) {
        // Saved: advance the synced base to the bytes just written. If the user kept
        // typing during the save, draft is now ahead of `content` → still dirty → the
        // autosave effect fires again. The origin=ui broadcast also refreshes.
        setConflict(null);
        setNote({ path, content, hash: res.hash ?? '' });
        setJustSaved(true);
      }
      return 'saved';
    } catch (err) {
      if (loadSeqRef.current === seq && selectedPathRef.current === path) {
        setSaveError(err instanceof Error ? err.message : 'Could not save this file');
      }
      return 'error';
    } finally {
      setSaving(false);
    }
  }, [writeFile]);

  // Persist the current dirty buffer against its synced base. Drives the debounced
  // autosave and — crucially — the navigate/close flush, so an outgoing edit that
  // conflicts surfaces the banner (and blocks the navigation/close) instead of being
  // dropped. A no-op when the buffer is in sync. Reads refs, so it stays stable.
  const persist = useCallback(async (): Promise<PersistOutcome> => {
    const current = noteRef.current;
    if (!current) return 'noop';
    const content = draftRef.current;
    if (content === current.content) return 'noop'; // in sync — nothing to persist
    return writeBuffer(current.hash, content);
  }, [writeBuffer]);
  // Indirection so loadFile/requestClose (declared above the editing block) can invoke
  // the latest persist without a forward declaration.
  persistRef.current = persist;

  // Discard the local buffer and reload the current on-disk version (the conflict
  // reconcile "reload from disk" path).
  const reloadFromDisk = useCallback(async () => {
    const path = selectedPathRef.current;
    if (!path) return;
    // Freeze the load token so a navigation that lands while this read is in flight
    // is detected on resolve (mirrors loadFile/writeBuffer). Without it, a slow reload
    // of file A could stamp A's content/hash onto file B after the user moved on.
    const seq = loadSeqRef.current;
    setConflict(null);
    setSaveError(null);
    try {
      const fresh = await readFile(path);
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setNote(fresh);
      setDraft(fresh.content);
    } catch (err) {
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setSaveError(err instanceof Error ? err.message : 'Could not reload this file');
    }
  }, [readFile]);

  // Hand the captured selection to the daemon for the chief of staff. The daemon
  // appends it to the chief inbox note and best-effort nudges a live chief; the
  // UI only surfaces the outcome and never messages the chief directly.
  const sendSelectionToChief = useCallback(async () => {
    if (!chiefSel) return;
    const path = selectedPathRef.current ?? undefined;
    // Freeze the load token (as writeBuffer does) so an outcome that resolves after
    // the user navigated away doesn't flash on the now-selected file.
    const seq = loadSeqRef.current;
    setSendingToChief(true);
    try {
      await sendToChief(chiefSel.text, path);
      if (loadSeqRef.current !== seq || (selectedPathRef.current ?? undefined) !== path) return;
      setChiefSel(null);
      setChiefStatus({ text: "Added to chief's inbox", error: false });
    } catch (err) {
      if (loadSeqRef.current !== seq || (selectedPathRef.current ?? undefined) !== path) return;
      setChiefStatus({ text: err instanceof Error ? err.message : 'Could not send to chief', error: true });
    } finally {
      setSendingToChief(false);
    }
  }, [chiefSel, sendToChief]);

  // On open, select a sensible first file: keep the prior selection if it still
  // reads, else probe the preferred entry points, else fall back to the first file
  // at the root. The lazy sidebar lists itself; this only chooses what to show.
  useEffect(() => {
    if (!isOpen) return;
    // Start clean: a transient outcome/selection from a prior session must not
    // reappear on reopen. The [selectedPath] reset can't cover a reopen on the
    // same file (selectedPath doesn't change), so clear it here.
    setChiefStatus(null);
    setChiefSel(null);
    setJustSaved(false);
    let cancelled = false;
    void (async () => {
      // Keep the current selection if it still exists (a reopen on the same file).
      const current = selectedPathRef.current;
      if (current) {
        // A binary selection is preserved WITHOUT reading it — fs_read is never called
        // for binary files (it returns a string, meaningless for bytes). loadFile
        // re-renders the placeholder. (Probing it with a read here would leak the very
        // fs_read the binary gate exists to prevent.)
        if (isBinaryPath(current)) {
          if (!cancelled) void loadFile(current);
          return;
        }
        // Otherwise probe by reading; the read is reused to seed the editor (no second
        // read), and a rejection means the file fell away while closed.
        try {
          const res = await readFile(current);
          if (!cancelled) void loadFile(current, res);
          return;
        } catch {
          // Fell away while closed; fall through to pick a fresh entry point.
        }
      }
      // Probe the preferred entry points in order; the first that reads wins, and its
      // read seeds the editor. (A cheap 1–2 reads, vs. walking the whole tree the lazy
      // sidebar never materializes.)
      for (const candidate of PREFERRED_FIRST) {
        if (cancelled) return;
        try {
          const res = await readFile(candidate);
          if (!cancelled) void loadFile(candidate, res);
          return;
        } catch {
          // Not present; try the next candidate.
        }
      }
      // Last resort: the first file directly under the root.
      try {
        const root = await listDir('');
        if (cancelled) return;
        const firstFile = root.find((e) => !e.isDir);
        if (firstFile) void loadFile(firstFile.path);
        else clearSelection();
      } catch {
        if (!cancelled) clearSelection();
      }
    })();
    return () => { cancelled = true; };
    // Only re-run when opening; navigation is driven by loadFile directly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen]);

  // Live refresh: an fs_changed event reloads the open file so agent/external writes
  // show up without reopening. The sidebar tree re-lists itself via its own
  // changeSignal prop; here we only refresh the open document.
  useEffect(() => {
    if (!isOpen || changeSignal === 0) return;
    // With unsaved edits, never reload — that would clobber the buffer. On-disk
    // divergence surfaces as a save-time conflict instead.
    if (dirtyRef.current) return;
    const current = selectedPathRef.current;
    if (!current) return;
    // A binary selection shows a placeholder we never read; nothing to reload.
    if (isBinaryPath(current)) return;
    // Re-read content (and, for .md, backlinks). If the file was deleted on disk the
    // read fails and the document pane shows "unavailable" — honest, and the tree
    // drops the node on its own re-list.
    void loadFile(current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changeSignal]);

  // Fetch tasks while the Tasks section is open (and the browser is open): once on
  // open, and again whenever a notebook_tasks_changed broadcast bumps taskChangeSignal
  // so runner transitions show without reopening the section.
  useEffect(() => {
    if (!isOpen || !tasksOpen) return;
    void refreshTasks();
  }, [isOpen, tasksOpen, refreshTasks, taskChangeSignal]);

  // Drop the staleness token when the browser closes so an in-flight tasks fetch
  // can't stamp rows onto a reopened panel.
  useEffect(() => {
    if (isOpen) return;
    tasksSeqRef.current += 1;
    setTasksLoading(false);
  }, [isOpen]);

  // Navigating to another file clears the previous file's edit status. Keyed on
  // selectedPath, so it fires on navigation but not on a same-file live reload
  // (which keeps selectedPath unchanged).
  useEffect(() => {
    setConflict(null);
    setSaveError(null);
    setJustSaved(false);
    setChiefSel(null);
    setChiefStatus(null);
  }, [selectedPath]);

  // Debounced autosave: once the buffer diverges from the synced file, persist it
  // via hash-CAS against note.hash after a short idle. Gated off while a file is
  // loading (the buffer is being re-seeded), while a save is already in flight, and
  // while a conflict is unresolved (the user must reconcile first). Every dep change
  // clears the pending timer, so navigation can't leave a stale write scheduled.
  useEffect(() => {
    if (!note || noteLoading || saving || conflict) return;
    if (draft === note.content) return; // in sync — nothing to save
    const timer = window.setTimeout(() => {
      void persist();
    }, AUTOSAVE_DELAY_MS);
    return () => window.clearTimeout(timer);
  }, [draft, note, noteLoading, saving, conflict, persist]);

  // The "Send to chief" outcome line is a transient confirmation; auto-dismiss it
  // (errors linger a little longer than the success acknowledgement).
  useEffect(() => {
    if (!chiefStatus) return;
    const timer = window.setTimeout(() => setChiefStatus(null), chiefStatus.error ? 6000 : 3000);
    return () => window.clearTimeout(timer);
  }, [chiefStatus]);

  // The floating "Send to chief" button is fixed at viewport coords frozen from
  // the selection rect, so any geometry change invalidates its position. While it
  // is shown, clear it on window resize and on scroll anywhere — capture phase,
  // because scroll doesn't bubble, so a nested code-block scroller would otherwise
  // slip past and leave the button stranded over the wrong text.
  useEffect(() => {
    if (!chiefSel) return;
    const clear = () => setChiefSel(null);
    window.addEventListener('resize', clear);
    document.addEventListener('scroll', clear, true);
    return () => {
      window.removeEventListener('resize', clear);
      document.removeEventListener('scroll', clear, true);
    };
  }, [chiefSel]);

  // The "Saved" badge is a transient confirmation, not a persistent status. Clear
  // it on a timer so it doesn't linger while the user keeps reading the same file
  // (the navigation-reset effect above only fires on a path change, not on a
  // same-file live reload, so without this the badge would stick indefinitely).
  useEffect(() => {
    if (!justSaved) return;
    const timer = window.setTimeout(() => setJustSaved(false), 2500);
    return () => window.clearTimeout(timer);
  }, [justSaved]);

  // Mod-click on a rendered link: in-notebook .md targets navigate; external
  // targets open in the browser; fragments are ignored (no in-editor anchor jump).
  const handleFollowLink = useCallback((href: string) => {
    const target = parseNotebookHref(href);
    if (target.kind === 'note' && target.path) {
      void loadFile(target.path);
    } else if (target.kind === 'external' && target.href) {
      window.open(target.href, '_blank', 'noreferrer');
    }
  }, [loadFile]);

  // The editor reports its current selection (or null when collapsed); float the
  // "Send to chief" action over it.
  const handleSelectionChange = useCallback((selection: LiveSelection | null) => {
    setChiefSel(selection);
  }, []);

  // The outline is a markdown affordance only, derived from the LIVE buffer so it
  // tracks edits as you type. Indexing into `draft` keeps heading positions aligned
  // with what the editor holds, so a jump lands on the right line. (Hook: must run
  // before the isOpen early return, so it is gated by the path kind, not by render.)
  const selectedIsMarkdown = selectedPath ? isMarkdownPath(selectedPath) : false;
  const outline = useMemo(
    () => (selectedIsMarkdown ? parseOutline(draft) : []),
    [selectedIsMarkdown, draft],
  );

  if (!isOpen) return null;

  const selectedKind = selectedPath ? fileKind(selectedPath) : null;
  const showBinaryPlaceholder = selectedPath !== null && selectedKind === 'binary';
  // The context rail (outline + backlinks) is a markdown-document affordance; a text
  // or binary file shows neither, so it keeps the two-pane layout (no empty rail).
  const showRail = selectedKind === 'markdown' && !!note;
  // A single live save indicator (the error itself is surfaced by its own banner).
  const saveStatus = saveError
    ? null
    : saving
      ? 'Saving…'
      : dirty
        ? 'Unsaved…'
        : justSaved
          ? 'Saved'
          : null;

  return (
    <div className="notebook-browser-shell">
      <FocusTrap focusTrapOptions={{ escapeDeactivates: false, initialFocus: () => dialogRef.current ?? false }}>
        <div ref={dialogRef} tabIndex={-1} className="notebook-browser" role="dialog" aria-modal="true" aria-labelledby="notebook-browser-title">
          <header className="notebook-browser-header">
            <div className="notebook-browser-heading">
              <NotebookIcon />
              <div>
                <span className="notebook-browser-eyebrow">Knowledge base</span>
                <h1 id="notebook-browser-title">Notebook</h1>
              </div>
            </div>
            <button type="button" className="notebook-browser-close" onClick={() => void requestClose()}>
              <span>Close</span><kbd>esc</kbd>
            </button>
          </header>

          <div className={`notebook-browser-body${showRail ? ' has-rail' : ''}`}>
            <aside className="notebook-browser-list" aria-label="Notebook files">
              <FileTree
                listDir={listDir}
                selectedPath={selectedPath}
                onSelectFile={(path) => void loadFile(path)}
                changeSignal={changeSignal}
              />

              <section className="notebook-browser-tasks" aria-label="Tasks">
                <button
                  type="button"
                  className="notebook-browser-tasks-toggle"
                  aria-expanded={tasksOpen}
                  onClick={() => setTasksOpen((open) => !open)}
                >
                  <span className={`notebook-browser-tasks-caret${tasksOpen ? ' is-open' : ''}`} aria-hidden="true" />
                  <span className="notebook-browser-tasks-title">Tasks</span>
                  {tasksOpen && tasks.length > 0 && (
                    <span className="notebook-browser-tasks-count">{tasks.length}</span>
                  )}
                </button>
                {tasksOpen && (
                  <div className="notebook-browser-tasks-body">
                    {tasksError && (
                      <div className="notebook-browser-tasks-state">
                        <span>{tasksError}</span>
                        <button type="button" onClick={() => void refreshTasks()}>Try again</button>
                      </div>
                    )}
                    {!tasksError && tasksLoading && tasks.length === 0 && (
                      <div className="notebook-browser-tasks-state">Loading tasks…</div>
                    )}
                    {!tasksError && !tasksLoading && tasks.length === 0 && (
                      <p className="notebook-browser-tasks-empty">No tasks.</p>
                    )}
                    {tasks.length > 0 && (
                      <ul className="notebook-browser-tasks-list">
                        {tasks.map((task) => {
                          const nextAttempt = TASK_TERMINAL_STATES.has(task.state)
                            ? ''
                            : formatNextAttempt(task.next_attempt_at);
                          const canRetry = task.state === 'failed' || task.state === 'dead';
                          return (
                            <li className="notebook-browser-task" key={task.id}>
                              <div className="notebook-browser-task-head">
                                <span
                                  className={`notebook-browser-task-badge is-${task.state}`}
                                  title={task.state}
                                >
                                  {task.state}
                                </span>
                                <span className="notebook-browser-task-subject" title={`${task.kind}:${task.subject}`}>
                                  {task.kind}:{task.subject}
                                </span>
                                {canRetry && (
                                  <button
                                    type="button"
                                    className="notebook-browser-task-retry"
                                    onClick={() => void handleRetry(task.id)}
                                    disabled={retryingIds.has(task.id)}
                                  >
                                    {retryingIds.has(task.id) ? 'Retrying…' : 'Retry'}
                                  </button>
                                )}
                              </div>
                              <div className="notebook-browser-task-meta">
                                <span>attempts: {task.attempts}</span>
                                {nextAttempt && <span>next: {nextAttempt}</span>}
                              </div>
                              {task.last_error && (
                                <p className="notebook-browser-task-error" title={task.last_error}>
                                  {task.last_error}
                                </p>
                              )}
                            </li>
                          );
                        })}
                      </ul>
                    )}
                  </div>
                )}
              </section>
            </aside>

            <main className="notebook-browser-document">
              {noteLoading && !note && (
                <div className="notebook-browser-document-state">Loading…</div>
              )}
              {!noteLoading && noteError && (
                <div className="notebook-browser-document-state">
                  <NotebookIcon />
                  <h2>File unavailable</h2>
                  <p>{noteError}</p>
                </div>
              )}
              {!noteLoading && !noteError && showBinaryPlaceholder && (
                <div className="notebook-browser-document-state">
                  <NotebookIcon />
                  <h2>Preview not available</h2>
                  <p>{basename(selectedPath)} can't be opened here yet.</p>
                  <p className="notebook-browser-document-subtle">{selectedPath}</p>
                </div>
              )}
              {!noteError && !showBinaryPlaceholder && note && (
                <>
                  <div className="notebook-browser-document-meta">
                    <div className="notebook-browser-document-titles">
                      <h2>{basename(note.path)}</h2>
                      <p>{note.path}</p>
                    </div>
                    <div className="notebook-browser-document-actions">
                      {chiefStatus && (
                        <span
                          className={`notebook-browser-chief-status${chiefStatus.error ? ' is-error' : ''}`}
                          role="status"
                        >
                          {chiefStatus.text}
                        </span>
                      )}
                      {saveStatus && (
                        <span className="notebook-browser-save-status" role="status">{saveStatus}</span>
                      )}
                    </div>
                  </div>
                  <div className="notebook-browser-live">
                    {conflict && (
                      <div className="notebook-browser-editor-conflict" role="alert">
                        <span>
                          {conflict.currentHash
                            ? 'This file changed on disk since you opened it.'
                            : 'This file was deleted on disk since you opened it.'}
                        </span>
                        <div className="notebook-browser-editor-conflict-actions">
                          <button type="button" onClick={() => void reloadFromDisk()} disabled={saving}>
                            Reload from disk
                          </button>
                          <button type="button" onClick={() => void writeBuffer(conflict.currentHash ?? '', draft)} disabled={saving}>
                            Overwrite anyway
                          </button>
                        </div>
                      </div>
                    )}
                    {saveError && (
                      <p className="notebook-browser-editor-error" role="alert">{saveError}</p>
                    )}
                    <div className="notebook-browser-live-editor">
                      {selectedKind === 'markdown' ? (
                        <LiveMarkdownEditor
                          ref={editorRef}
                          value={draft}
                          onChange={setDraft}
                          onFollowLink={handleFollowLink}
                          onSelectionChange={handleSelectionChange}
                          existsFile={existsFile}
                          revalidateSignal={changeSignal}
                          ariaLabel="Note"
                        />
                      ) : (
                        <textarea
                          className="notebook-browser-plain-editor"
                          value={draft}
                          onChange={(event) => setDraft(event.target.value)}
                          spellCheck={false}
                          aria-label="File contents"
                        />
                      )}
                    </div>
                  </div>
                </>
              )}
              {!noteLoading && !noteError && !showBinaryPlaceholder && !note && (
                <div className="notebook-browser-document-state">
                  <NotebookIcon />
                  <h2>Nothing selected</h2>
                  <p>Choose a file from the tree to read it.</p>
                </div>
              )}
            </main>

            {showRail && (
              <aside className="notebook-browser-rail" aria-label="Context">
                <section className="notebook-browser-rail-section">
                  <button
                    type="button"
                    className="notebook-browser-rail-toggle"
                    aria-expanded={outlineOpen}
                    onClick={() => setOutlineOpen((open) => !open)}
                  >
                    <span className={`notebook-browser-rail-caret${outlineOpen ? ' is-open' : ''}`} aria-hidden="true" />
                    <span className="notebook-browser-rail-title">Outline</span>
                    {outlineOpen && outline.length > 0 && (
                      <span className="notebook-browser-rail-count">{outline.length}</span>
                    )}
                  </button>
                  {outlineOpen && (
                    <div className="notebook-browser-rail-body">
                      {outline.length === 0 ? (
                        <p className="notebook-browser-rail-empty">No headings.</p>
                      ) : (
                        <ul className="notebook-browser-outline">
                          {outline.map((heading) => (
                            <li key={`${heading.line}:${heading.pos}`}>
                              <button
                                type="button"
                                className={`notebook-browser-outline-item is-h${heading.level}`}
                                onClick={() => editorRef.current?.scrollToPos(heading.pos)}
                                title={heading.text}
                              >
                                {heading.text}
                              </button>
                            </li>
                          ))}
                        </ul>
                      )}
                    </div>
                  )}
                </section>

                <section className="notebook-browser-rail-section">
                  <button
                    type="button"
                    className="notebook-browser-rail-toggle"
                    aria-expanded={backlinksOpen}
                    onClick={() => setBacklinksOpen((open) => !open)}
                  >
                    <span className={`notebook-browser-rail-caret${backlinksOpen ? ' is-open' : ''}`} aria-hidden="true" />
                    <span className="notebook-browser-rail-title">Backlinks</span>
                    {backlinksOpen && !backlinksLoading && backlinks.length > 0 && (
                      <span className="notebook-browser-rail-count">{backlinks.length}</span>
                    )}
                  </button>
                  {backlinksOpen && (
                    <div className="notebook-browser-rail-body">
                      {backlinksLoading ? (
                        <p className="notebook-browser-rail-empty">Finding backlinks…</p>
                      ) : backlinks.length === 0 ? (
                        <p className="notebook-browser-rail-empty">No other note links here.</p>
                      ) : (
                        <ul className="notebook-browser-backlinks">
                          {backlinks.map((entry) => (
                            <li key={entry.path}>
                              <button
                                type="button"
                                className="notebook-browser-backlink"
                                onClick={() => void loadFile(entry.path)}
                                title={entry.path}
                                // The visible card shows title over a mono path; the
                                // accessible name is just the title (the path is visual
                                // detail), so AT announces which note links here, not a
                                // path read out character by character.
                                aria-label={entry.title || basename(entry.path)}
                              >
                                <span className="notebook-browser-backlink-title">{entry.title || basename(entry.path)}</span>
                                <span className="notebook-browser-backlink-path">{entry.path}</span>
                              </button>
                            </li>
                          ))}
                        </ul>
                      )}
                    </div>
                  )}
                </section>
              </aside>
            )}
          </div>
          {chiefSel && (
            <button
              type="button"
              className="notebook-browser-send-chief"
              style={{ top: chiefSel.top, left: chiefSel.left }}
              // Keep the text selection (and focus) intact through the click so the
              // captured selection isn't collapsed before onClick reads it.
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => void sendSelectionToChief()}
              disabled={sendingToChief}
            >
              {sendingToChief ? 'Sending…' : 'Send to chief'}
            </button>
          )}
        </div>
      </FocusTrap>
    </div>
  );
}

export interface NotebookHref {
  kind: 'note' | 'fragment' | 'external';
  // For 'note': the notebook-relative path (no leading slash, e.g. "knowledge/areas/foo.md").
  path?: string;
  // For 'fragment': the bare anchor without '#'. For 'note': an optional anchor.
  anchor?: string;
  // For 'external': the original href.
  href?: string;
}

// parseNotebookHref classifies a markdown link target. Root-absolute .md targets
// are in-notebook navigation; '#...' is an in-page anchor; everything else
// (http(s), mailto, relative) is external and opened in the browser.
export function parseNotebookHref(href: string): NotebookHref {
  const trimmed = href.trim();
  if (trimmed.startsWith('#')) {
    return { kind: 'fragment', anchor: trimmed.slice(1) };
  }
  if (trimmed.startsWith('/')) {
    const hashIdx = trimmed.indexOf('#');
    const pathPart = hashIdx === -1 ? trimmed : trimmed.slice(0, hashIdx);
    const anchor = hashIdx === -1 ? undefined : trimmed.slice(hashIdx + 1);
    if (pathPart.toLowerCase().endsWith('.md')) {
      return { kind: 'note', path: pathPart.replace(/^\/+/, ''), anchor };
    }
  }
  return { kind: 'external', href: trimmed };
}

function basename(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return name.endsWith('.md') ? name.slice(0, -3) : name;
}

// A terminal task isn't waiting on a next attempt, so its scheduled time is noise.
const TASK_TERMINAL_STATES = new Set(['done', 'dead']);

// formatNextAttempt renders an RFC3339 next_attempt_at as a short relative phrase
// ("in 2m", "5s ago", "now"). Returns '' for an unparseable/zero timestamp so the
// row can omit it.
function formatNextAttempt(iso: string): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  // The runner stamps a zero time (year <= 1) when there is no scheduled attempt.
  if (new Date(t).getUTCFullYear() <= 1) return '';
  const now = Date.now();
  const deltaSec = Math.round((t - now) / 1000);
  const abs = Math.abs(deltaSec);
  if (abs < 5) return 'now';
  const unit = abs < 60 ? `${abs}s` : abs < 3600 ? `${Math.round(abs / 60)}m` : `${Math.round(abs / 3600)}h`;
  return deltaSec >= 0 ? `in ${unit}` : `${unit} ago`;
}

function NotebookIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 3.5h11a1 1 0 0 1 1 1V20a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z" />
      <path d="M9 3.5V21M12 8h4M12 11.5h4" />
    </svg>
  );
}
