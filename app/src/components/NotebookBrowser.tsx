import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import type { NotebookEntry, NotebookReadResult, NotebookSendToChiefResult, NotebookTask, NotebookWriteResult } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { LiveMarkdownEditor, type LiveSelection } from './notebook/LiveMarkdownEditor';
import './NotebookBrowser.css';

interface NotebookBrowserProps {
  isOpen: boolean;
  onClose: () => void;
  listNotebook: () => Promise<NotebookEntry[]>;
  readNotebook: (path: string) => Promise<NotebookReadResult>;
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  // Save an edited note (hash-CAS). Omit baseHash to create-only; pass the note's
  // loaded hash to edit. Resolves with the outcome, including a conflict to reconcile.
  writeNotebook: (path: string, content: string, baseHash?: string) => Promise<NotebookWriteResult>;
  // Hand a highlighted selection to the daemon to deliver to the chief of staff
  // (appends to the chief inbox note + best-effort live PTY nudge). The UI never
  // messages the chief directly. sourcePath is the note the selection came from.
  sendToChief: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  // Increments whenever a notebook_changed event arrives, so an open browser
  // re-fetches the tree and the open note (covering agent and external writes).
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

// The note shown first when the browser opens with nothing selected, in order of
// preference. knowledge/index.md is the distilled map an agent is told to read.
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
  listNotebook,
  readNotebook,
  backlinksNotebook,
  writeNotebook,
  sendToChief,
  changeSignal = 0,
  listTasks,
  retryTask,
  taskChangeSignal = 0,
}: NotebookBrowserProps) {
  const [entries, setEntries] = useState<NotebookEntry[]>([]);
  const [listError, setListError] = useState<string | null>(null);
  const [listLoading, setListLoading] = useState(false);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [note, setNote] = useState<NotebookReadResult | null>(null);
  const [noteError, setNoteError] = useState<string | null>(null);
  const [noteLoading, setNoteLoading] = useState(false);
  const [backlinks, setBacklinks] = useState<NotebookEntry[]>([]);
  // --- Tasks panel (durable runner) ---
  const [tasks, setTasks] = useState<NotebookTask[]>([]);
  const [tasksError, setTasksError] = useState<string | null>(null);
  const [tasksLoading, setTasksLoading] = useState(false);
  // The Tasks section is collapsible; collapsed by default so it doesn't crowd the
  // note list. Opening it (or a taskChangeSignal bump) triggers a refetch.
  const [tasksOpen, setTasksOpen] = useState(false);
  // Task ids whose Retry click is in flight, so their button can disable without
  // optimistically mutating the row (the broadcast-driven refetch reflects truth).
  const [retryingIds, setRetryingIds] = useState<Set<string>>(new Set());
  // Monotonic load token for the tasks fetch: a slow response from a superseded
  // fetch (panel closed/refetched) is dropped instead of stamping stale rows.
  const tasksSeqRef = useRef(0);
  // selectedPath drives loads; this ref lets the change-signal effect reload the
  // current note without depending on (and re-running for) selectedPath itself.
  const selectedPathRef = useRef<string | null>(null);
  selectedPathRef.current = selectedPath;
  // Monotonic load token, bumped synchronously at the start of every loadNote so
  // a slow response from a superseded navigation is dropped. (A render-synced ref
  // can't do this — it only updates on commit, after the await may have resolved.)
  const loadSeqRef = useRef(0);
  // Persists the outgoing note's unsaved buffer before a navigation/close replaces or
  // hides it — surfacing a CAS conflict rather than dropping it. Assigned below (after
  // the editing state/refs exist) and invoked via a ref so loadNote — declared above
  // the editing block — doesn't depend on declaration order. A no-op until assigned.
  const persistRef = useRef<() => Promise<PersistOutcome>>(async () => 'noop');
  // The dialog container is the deliberate initial focus target so keyboard/AT
  // users land inside the modal (engaging the focus trap) without auto-selecting
  // the Close button. Tab from here moves to the first interactive control.
  const dialogRef = useRef<HTMLDivElement>(null);

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

  // Returns the fetched entries, or null if the fetch FAILED (distinct from an
  // empty notebook). Callers must not treat a failed refresh as "the notebook is
  // now empty" — that would, e.g., clear the open note on a transient WS hiccup.
  const refreshList = useCallback(async (): Promise<NotebookEntry[] | null> => {
    setListLoading(true);
    try {
      const next = await listNotebook();
      setEntries(next);
      setListError(null);
      return next;
    } catch (err) {
      setListError(err instanceof Error ? err.message : 'Could not load the notebook');
      return null;
    } finally {
      setListLoading(false);
    }
  }, [listNotebook]);

  // Fetch the durable runner's task list. A transient WS failure surfaces an error
  // rather than silently wiping the rows (mirrors refreshList). The stale-guard
  // drops a response that resolved after a newer fetch (or a panel close).
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

  const loadNote = useCallback(async (path: string) => {
    // Persist any unsaved buffer on the note we're leaving before we replace it
    // (covers an edit made in the <debounce window of the autosave timer). If that
    // write conflicts (the note changed on disk) we ABORT the navigation and stay
    // put so the conflict banner can be reconciled — navigating away would discard
    // the buffer and the user's edits would vanish silently.
    if (dirtyRef.current && selectedPathRef.current && selectedPathRef.current !== path) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    const seq = ++loadSeqRef.current;
    setSelectedPath(path);
    setNoteLoading(true);
    setNoteError(null);
    // Loading replaces the rendered content (navigation or a same-note live
    // reload), so any floating "Send to chief" button is now mispositioned — drop
    // it. (Navigation also clears it via the [selectedPath] effect, but a
    // same-path reload does not change selectedPath, so clear here too.)
    setChiefSel(null);
    // Fetch content and backlinks together; a backlinks failure must not blank
    // the note, so it is tolerated independently.
    const [readResult, backlinkResult] = await Promise.allSettled([
      readNotebook(path),
      backlinksNotebook(path),
    ]);
    // Ignore a stale response if a newer navigation superseded this one.
    if (loadSeqRef.current !== seq) return;
    if (readResult.status === 'fulfilled') {
      setNote(readResult.value);
      // Seed the live editor buffer from disk; a fresh load is never dirty.
      setDraft(readResult.value.content);
    } else {
      setNote(null);
      setDraft('');
      setNoteError(readResult.reason instanceof Error ? readResult.reason.message : 'Could not read this note');
    }
    setBacklinks(backlinkResult.status === 'fulfilled' ? backlinkResult.value : []);
    setNoteLoading(false);
  }, [readNotebook, backlinksNotebook]);

  // Drop the current selection and return the document pane to its empty state.
  // Bumping loadSeqRef invalidates any in-flight loadNote so a response that
  // resolves after this clear cannot resurrect the just-cleared note's content.
  const clearSelection = useCallback(() => {
    loadSeqRef.current += 1;
    setSelectedPath(null);
    setNote(null);
    setDraft('');
    setNoteError(null);
    setNoteLoading(false);
    setBacklinks([]);
  }, []);

  // --- Editing (single live surface; no view/edit toggle) ---
  // `draft` is the live editor buffer; `note` holds the value last synced from disk
  // (its content + hash). The note is "dirty" when draft diverges from note.content;
  // dirty edits autosave (debounced) via hash-CAS against note.hash.
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  // Set when an autosave's hash-CAS rejected because the note changed on disk since
  // it was loaded; carries the current on-disk hash so the user can overwrite it.
  const [conflict, setConflict] = useState<{ currentHash?: string } | null>(null);
  const [justSaved, setJustSaved] = useState(false);
  // Refs let the navigation/close persist and the live-refresh guard read the latest
  // draft / synced note / dirty state without re-subscribing every effect to them.
  const draftRef = useRef('');
  draftRef.current = draft;
  const noteRef = useRef<NotebookReadResult | null>(null);
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
  // create-only write, used to recreate a note deleted on disk while it was edited.
  const writeBuffer = useCallback(async (baseHash: string, content: string): Promise<PersistOutcome> => {
    const path = selectedPathRef.current;
    if (!path) return 'noop';
    // Freeze the load token so a navigation that lands while this write is in flight
    // is detected on resolve (mirrors loadNote's staleness guard; loadNote/
    // clearSelection bump loadSeqRef, writeBuffer does not). The bytes still reach
    // disk either way — we just don't stamp this note's result onto whatever note is
    // now shown. The OUTCOME still returns so the caller can react.
    const seq = loadSeqRef.current;
    setSaving(true);
    setSaveError(null);
    try {
      const res = await writeNotebook(path, content, baseHash || undefined);
      const superseded = loadSeqRef.current !== seq || selectedPathRef.current !== path;
      if (res.conflict) {
        // The note diverged on disk; let the user reconcile rather than clobber.
        // (Only surface it if this note is still shown — see `superseded`.)
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
        setSaveError(err instanceof Error ? err.message : 'Could not save this note');
      }
      return 'error';
    } finally {
      setSaving(false);
    }
  }, [writeNotebook]);

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
  // Indirection so loadNote/requestClose (declared above the editing block) can invoke
  // the latest persist without a forward declaration.
  persistRef.current = persist;

  // Discard the local buffer and reload the current on-disk version (the conflict
  // reconcile "reload from disk" path).
  const reloadFromDisk = useCallback(async () => {
    const path = selectedPathRef.current;
    if (!path) return;
    // Freeze the load token so a navigation that lands while this read is in flight
    // is detected on resolve (mirrors loadNote/writeBuffer). Without it, a slow reload
    // of note A could stamp A's content/hash onto note B after the user moved on.
    const seq = loadSeqRef.current;
    setConflict(null);
    setSaveError(null);
    try {
      const fresh = await readNotebook(path);
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setNote(fresh);
      setDraft(fresh.content);
    } catch (err) {
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setSaveError(err instanceof Error ? err.message : 'Could not reload this note');
    }
  }, [readNotebook]);

  // Hand the captured selection to the daemon for the chief of staff. The daemon
  // appends it to the chief inbox note and best-effort nudges a live chief; the
  // UI only surfaces the outcome and never messages the chief directly.
  const sendSelectionToChief = useCallback(async () => {
    if (!chiefSel) return;
    const path = selectedPathRef.current ?? undefined;
    // Freeze the load token (as writeBuffer does) so an outcome that resolves after
    // the user navigated away doesn't flash on the now-selected note.
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

  // On open, load the tree and select a sensible first note.
  useEffect(() => {
    if (!isOpen) return;
    // Start clean: a transient outcome/selection from a prior session must not
    // reappear on reopen. The [selectedPath] reset can't cover a reopen on the
    // same note (selectedPath doesn't change), so clear it here.
    setChiefStatus(null);
    setChiefSel(null);
    setJustSaved(false);
    let cancelled = false;
    void (async () => {
      const next = await refreshList();
      if (cancelled || next === null) return;
      const current = selectedPathRef.current;
      if (current && next.some((e) => e.path === current)) {
        void loadNote(current);
        return;
      }
      const preferred = PREFERRED_FIRST.find((p) => next.some((e) => e.path === p));
      const first = preferred ?? next[0]?.path ?? null;
      if (first) {
        void loadNote(first);
      } else {
        clearSelection();
      }
    })();
    return () => { cancelled = true; };
    // Only re-run when opening; navigation is driven by loadNote directly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen]);

  // Live refresh: a notebook_changed event re-fetches the tree and the open note
  // so agent/external writes show up without reopening.
  useEffect(() => {
    if (!isOpen || changeSignal === 0) return;
    let cancelled = false;
    void (async () => {
      const next = await refreshList();
      // A failed refresh (null) is NOT an empty notebook: leave the open note
      // alone rather than mistaking a transient WS hiccup for a deletion.
      if (cancelled || next === null) return;
      // With unsaved edits, refresh the tree but never reload or clear the open
      // note — that would clobber the buffer. On-disk divergence surfaces as a
      // save-time conflict instead. (A clean buffer reloads to pick up the change.)
      if (dirtyRef.current) return;
      const current = selectedPathRef.current;
      if (current && next.some((e) => e.path === current)) {
        void loadNote(current);
      } else if (current) {
        // The open note vanished from the tree — an external delete (the watcher
        // surfaces those). Don't keep rendering its now-stale content; clear the
        // selection so the document pane returns to the empty state.
        clearSelection();
      }
    })();
    return () => { cancelled = true; };
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

  // Navigating to another note clears the previous note's edit status. Keyed on
  // selectedPath, so it fires on navigation but not on a same-note live reload
  // (which keeps selectedPath unchanged).
  useEffect(() => {
    setConflict(null);
    setSaveError(null);
    setJustSaved(false);
    setChiefSel(null);
    setChiefStatus(null);
  }, [selectedPath]);

  // Debounced autosave: once the buffer diverges from the synced note, persist it
  // via hash-CAS against note.hash after a short idle. Gated off while a note is
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
  // it on a timer so it doesn't linger while the user keeps reading the same note
  // (the navigation-reset effect above only fires on a path change, not on a
  // same-note live reload, so without this the badge would stick indefinitely).
  useEffect(() => {
    if (!justSaved) return;
    const timer = window.setTimeout(() => setJustSaved(false), 2500);
    return () => window.clearTimeout(timer);
  }, [justSaved]);

  const grouped = useMemo(() => groupEntries(entries), [entries]);

  // Mod-click on a rendered link: in-notebook .md targets navigate; external
  // targets open in the browser; fragments are ignored (no in-editor anchor jump).
  const handleFollowLink = useCallback((href: string) => {
    const target = parseNotebookHref(href);
    if (target.kind === 'note' && target.path) {
      void loadNote(target.path);
    } else if (target.kind === 'external' && target.href) {
      window.open(target.href, '_blank', 'noreferrer');
    }
  }, [loadNote]);

  // The editor reports its current selection (or null when collapsed); float the
  // "Send to chief" action over it.
  const handleSelectionChange = useCallback((selection: LiveSelection | null) => {
    setChiefSel(selection);
  }, []);

  if (!isOpen) return null;

  const selectedEntry = entries.find((e) => e.path === selectedPath) || null;
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

          <div className="notebook-browser-body">
            <aside className="notebook-browser-list" aria-label="Notebook notes">
              {listLoading && entries.length === 0 && (
                <div className="notebook-browser-list-state">Loading notes…</div>
              )}
              {listError && (
                <div className="notebook-browser-list-state">
                  <span>{listError}</span>
                  <button type="button" onClick={() => void refreshList()}>Try again</button>
                </div>
              )}
              {!listError && entries.length === 0 && !listLoading && (
                <div className="notebook-browser-list-state">
                  <span>No notes yet.</span>
                  <span className="notebook-browser-list-hint">The chief of staff and agents write here.</span>
                </div>
              )}
              {grouped.map((group) => (
                <div className="notebook-browser-group" key={group.label}>
                  <div className="notebook-browser-group-label">{group.label}</div>
                  {group.entries.map((entry) => (
                    <button
                      type="button"
                      key={entry.path}
                      className={`notebook-browser-list-item${entry.path === selectedPath ? ' is-selected' : ''}`}
                      onClick={() => void loadNote(entry.path)}
                      title={entry.path}
                    >
                      <span className="notebook-browser-list-marker" data-type={entry.type || 'note'} />
                      <span className="notebook-browser-list-copy">
                        <strong>{entry.title || basename(entry.path)}</strong>
                        <span>{entry.path}</span>
                      </span>
                    </button>
                  ))}
                </div>
              ))}

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
                <div className="notebook-browser-document-state">Loading note…</div>
              )}
              {!noteLoading && noteError && (
                <div className="notebook-browser-document-state">
                  <NotebookIcon />
                  <h2>Note unavailable</h2>
                  <p>{noteError}</p>
                </div>
              )}
              {!noteError && note && (
                <>
                  <div className="notebook-browser-document-meta">
                    <div className="notebook-browser-document-titles">
                      <h2>{selectedEntry?.title || basename(note.path)}</h2>
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
                            ? 'This note changed on disk since you opened it.'
                            : 'This note was deleted on disk since you opened it.'}
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
                      <LiveMarkdownEditor
                        value={draft}
                        onChange={setDraft}
                        onFollowLink={handleFollowLink}
                        onSelectionChange={handleSelectionChange}
                        ariaLabel="Note"
                      />
                    </div>
                  </div>
                  <section className="notebook-browser-backlinks" aria-label="Backlinks">
                    <h3>Linked from {backlinks.length > 0 ? `(${backlinks.length})` : ''}</h3>
                    {backlinks.length === 0 ? (
                      <p className="notebook-browser-backlinks-empty">No other note links here.</p>
                    ) : (
                      <ul>
                        {backlinks.map((entry) => (
                          <li key={entry.path}>
                            <button type="button" onClick={() => void loadNote(entry.path)} title={entry.path}>
                              {entry.title || basename(entry.path)}
                            </button>
                          </li>
                        ))}
                      </ul>
                    )}
                  </section>
                </>
              )}
              {!noteLoading && !noteError && !note && (
                <div className="notebook-browser-document-state">
                  <NotebookIcon />
                  <h2>Nothing selected</h2>
                  <p>Choose a note from the list to read it.</p>
                </div>
              )}
            </main>
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

interface NoteGroup {
  label: string;
  entries: NotebookEntry[];
}

const GROUP_ORDER = ['Journal', 'Projects', 'Areas', 'Resources', 'Archive', 'Knowledge', 'Notebook'];

// groupEntries buckets notes for the sidebar by their top-level path, mapping the
// PARA knowledge layout to section labels:
//   journal/*             -> Journal
//   knowledge/projects/*  -> Projects
//   knowledge/areas/*     -> Areas
//   knowledge/resources/* -> Resources
//   knowledge/archive/*   -> Archive
//   knowledge/*           -> Knowledge (e.g. knowledge/index.md, no PARA subdir)
//   root files            -> Notebook
function groupEntries(entries: NotebookEntry[]): NoteGroup[] {
  const buckets = new Map<string, NotebookEntry[]>();
  for (const entry of entries) {
    const label = groupLabel(entry.path);
    const list = buckets.get(label) || [];
    list.push(entry);
    buckets.set(label, list);
  }
  return [...buckets.entries()]
    .map(([label, list]) => ({ label, entries: list }))
    .sort((a, b) => groupRank(a.label) - groupRank(b.label) || a.label.localeCompare(b.label));
}

function groupLabel(path: string): string {
  if (path.startsWith('journal/')) return 'Journal';
  if (path.startsWith('knowledge/projects/')) return 'Projects';
  if (path.startsWith('knowledge/areas/')) return 'Areas';
  if (path.startsWith('knowledge/resources/')) return 'Resources';
  if (path.startsWith('knowledge/archive/')) return 'Archive';
  if (path.startsWith('knowledge/')) return 'Knowledge';
  return 'Notebook';
}

function groupRank(label: string): number {
  const idx = GROUP_ORDER.indexOf(label);
  return idx === -1 ? GROUP_ORDER.length : idx;
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
