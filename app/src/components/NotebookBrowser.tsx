import { createElement, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import FocusTrap from 'focus-trap-react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { NotebookEntry, NotebookReadResult, NotebookSendToChiefResult, NotebookTask, NotebookWriteResult } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
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
  // The dialog container is the deliberate initial focus target so keyboard/AT
  // users land inside the modal (engaging the focus trap) without auto-selecting
  // the Close button. Tab from here moves to the first interactive control.
  const dialogRef = useRef<HTMLDivElement>(null);

  useEscapeStack(onClose, isOpen);

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
    } else {
      setNote(null);
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
    setNoteError(null);
    setNoteLoading(false);
    setBacklinks([]);
  }, []);

  // --- Editing ---
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  // Set when the on-disk note changed since the editor loaded it; carries the
  // current on-disk hash so the user can choose to overwrite it.
  const [conflict, setConflict] = useState<{ currentHash?: string } | null>(null);
  const [justSaved, setJustSaved] = useState(false);
  // The hash the draft is edited against, captured on entering edit mode and held
  // independently of live `note` updates so an external change during editing is
  // detected as a save-time conflict rather than silently overwritten.
  const editBaseHashRef = useRef('');
  // Lets the live-refresh effect see "are we editing?" without re-subscribing.
  const editingRef = useRef(false);
  editingRef.current = editing;

  // --- Send to chief ---
  // The current rendered-markdown selection and where to float its action button
  // (viewport coords from the selection rect). Cleared on navigation/scroll/edit.
  const [chiefSel, setChiefSel] = useState<{ text: string; top: number; left: number } | null>(null);
  const [sendingToChief, setSendingToChief] = useState(false);
  // A transient outcome line ("Added to chief's inbox" / an error), auto-dismissed.
  const [chiefStatus, setChiefStatus] = useState<{ text: string; error: boolean } | null>(null);

  const startEditing = useCallback(() => {
    if (!note) return;
    setDraft(note.content);
    editBaseHashRef.current = note.hash;
    setConflict(null);
    setSaveError(null);
    setJustSaved(false);
    // The rendered article (and its selection) is replaced by the textarea, so
    // drop any floating "Send to chief" button left over from the view.
    setChiefSel(null);
    setEditing(true);
  }, [note]);

  const cancelEditing = useCallback(() => {
    setEditing(false);
    setConflict(null);
    setSaveError(null);
  }, []);

  // Save the draft against baseHash (hash-CAS). An empty baseHash is a create-only
  // write — used to recreate a note that was deleted on disk while being edited.
  const saveDraft = useCallback(async (baseHash: string) => {
    const path = selectedPathRef.current;
    if (!path) return;
    // Freeze the load token so a navigation that happens while this save is in
    // flight can be detected when it resolves (mirrors loadNote's staleness
    // guard; loadNote/clearSelection both bump loadSeqRef, saveDraft does not).
    const seq = loadSeqRef.current;
    setSaving(true);
    setSaveError(null);
    try {
      const res = await writeNotebook(path, draft, baseHash || undefined);
      // The write to `path` completed correctly against its own bytes, but if the
      // user navigated away (or cleared the selection) while it was in flight, the
      // result now applies to a note that is no longer shown. Bail before stamping
      // this note's content/conflict/status onto the now-selected note.
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      if (res.conflict) {
        // The note diverged on disk; let the user reconcile rather than clobber.
        setConflict({ currentHash: res.currentHash });
        return;
      }
      // Saved: reflect the new content+hash locally and leave edit mode. The
      // origin=ui broadcast also refreshes the view, so this is just immediacy.
      setConflict(null);
      setNote({ path, content: draft, hash: res.hash ?? '' });
      editBaseHashRef.current = res.hash ?? '';
      setEditing(false);
      setJustSaved(true);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Could not save this note');
    } finally {
      setSaving(false);
    }
  }, [draft, writeNotebook]);

  // Discard the draft and load the current on-disk version into the editor.
  const reloadFromDisk = useCallback(async () => {
    const path = selectedPathRef.current;
    if (!path) return;
    setConflict(null);
    setSaveError(null);
    try {
      const fresh = await readNotebook(path);
      setDraft(fresh.content);
      editBaseHashRef.current = fresh.hash;
      setNote(fresh);
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Could not reload this note');
    }
  }, [readNotebook]);

  // Capture the rendered-markdown selection on mouseup so the "Send to chief"
  // button can float over it. An empty/collapsed selection clears the button.
  const captureSelection = useCallback(() => {
    const sel = window.getSelection();
    const text = sel ? sel.toString().trim() : '';
    if (!text || !sel || sel.rangeCount === 0) {
      setChiefSel(null);
      return;
    }
    const rect = sel.getRangeAt(0).getBoundingClientRect();
    setChiefSel({ text, top: rect.top, left: rect.left + rect.width / 2 });
  }, []);

  // Hand the captured selection to the daemon for the chief of staff. The daemon
  // appends it to the chief inbox note and best-effort nudges a live chief; the
  // UI only surfaces the outcome and never messages the chief directly.
  const sendSelectionToChief = useCallback(async () => {
    if (!chiefSel) return;
    const path = selectedPathRef.current ?? undefined;
    // Freeze the load token (as saveDraft does) so an outcome that resolves after
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
      // While the user is editing, refresh the tree but never reload or clear the
      // open note — that would clobber the draft or yank the editor away. On-disk
      // divergence is surfaced as a save-time conflict instead.
      if (editingRef.current) return;
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

  // Fetch tasks when the Tasks section is opened (and the browser is open). A bump
  // of taskChangeSignal while it's open refetches via the effect below.
  useEffect(() => {
    if (!isOpen || !tasksOpen) return;
    void refreshTasks();
  }, [isOpen, tasksOpen, refreshTasks]);

  // Live refresh: a notebook_tasks_changed broadcast refetches the task list while
  // the section is open so runner transitions show without reopening it.
  useEffect(() => {
    if (!isOpen || !tasksOpen || taskChangeSignal === 0) return;
    void refreshTasks();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskChangeSignal]);

  // Drop the staleness token when the browser closes so an in-flight tasks fetch
  // can't stamp rows onto a reopened panel.
  useEffect(() => {
    if (isOpen) return;
    tasksSeqRef.current += 1;
    setTasksLoading(false);
  }, [isOpen]);

  // Navigating to another note discards any in-progress edit and clears its
  // status. Keyed on selectedPath, so it fires on navigation but not on a
  // same-note live reload (which keeps selectedPath unchanged).
  useEffect(() => {
    setEditing(false);
    setConflict(null);
    setSaveError(null);
    setJustSaved(false);
    setChiefSel(null);
    setChiefStatus(null);
  }, [selectedPath]);

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
  const markdownComponents = useMemo(
    () => notebookMarkdownComponents((path) => void loadNote(path)),
    [loadNote],
  );

  if (!isOpen) return null;

  const selectedEntry = entries.find((e) => e.path === selectedPath) || null;

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
            <button type="button" className="notebook-browser-close" onClick={onClose}>
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
                      {!editing && chiefStatus && (
                        <span
                          className={`notebook-browser-chief-status${chiefStatus.error ? ' is-error' : ''}`}
                          role="status"
                        >
                          {chiefStatus.text}
                        </span>
                      )}
                      {!editing && justSaved && (
                        <span className="notebook-browser-saved" role="status">Saved</span>
                      )}
                      {!editing && (
                        <button type="button" className="notebook-browser-edit-btn" onClick={startEditing}>
                          Edit
                        </button>
                      )}
                    </div>
                  </div>
                  {editing ? (
                    <div className="notebook-browser-editor">
                      <textarea
                        className="notebook-browser-editor-area"
                        aria-label="Edit note"
                        value={draft}
                        onChange={(event) => setDraft(event.target.value)}
                        spellCheck={false}
                        autoFocus
                      />
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
                            <button type="button" onClick={() => void saveDraft(conflict.currentHash ?? '')} disabled={saving}>
                              Overwrite anyway
                            </button>
                          </div>
                        </div>
                      )}
                      {saveError && (
                        <p className="notebook-browser-editor-error" role="alert">{saveError}</p>
                      )}
                      <div className="notebook-browser-editor-actions">
                        <button
                          type="button"
                          className="notebook-browser-editor-save"
                          onClick={() => void saveDraft(editBaseHashRef.current)}
                          disabled={saving}
                        >
                          {saving ? 'Saving…' : 'Save'}
                        </button>
                        <button type="button" onClick={cancelEditing} disabled={saving}>
                          Cancel
                        </button>
                      </div>
                    </div>
                  ) : (
                    <>
                      <article className="notebook-browser-markdown" onMouseUp={captureSelection}>
                        <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
                          {note.content || '_This note is empty._'}
                        </ReactMarkdown>
                      </article>
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

// notebookMarkdownComponents renders headings with slug ids (so #anchors resolve)
// and routes link clicks: in-notebook links navigate via onNavigate, external
// links open in a new tab, fragments scroll.
function notebookMarkdownComponents(onNavigate: (path: string) => void): Components {
  const slugCounts = new Map<string, number>();
  const heading = (level: number) => ({ children }: { children?: ReactNode }) => {
    const base = slug(textOf(children));
    const count = slugCounts.get(base) ?? 0;
    slugCounts.set(base, count + 1);
    const id = count === 0 ? base : `${base}-${count}`;
    return createElement(`h${level}`, { id }, children);
  };
  return {
    h1: heading(1),
    h2: heading(2),
    h3: heading(3),
    h4: heading(4),
    h5: heading(5),
    h6: heading(6),
    a({ href, children }) {
      if (!href) return <span>{children}</span>;
      const target = parseNotebookHref(href);
      if (target.kind === 'note' && target.path) {
        const path = target.path;
        return (
          <a
            href={href}
            className="notebook-link"
            title={`/${path}`}
            onClick={(event) => {
              event.preventDefault();
              onNavigate(path);
            }}
          >
            {children}
          </a>
        );
      }
      if (target.kind === 'fragment') {
        return <a href={href}>{children}</a>;
      }
      return (
        <a href={href} target="_blank" rel="noreferrer">{children}</a>
      );
    },
  };
}

function textOf(node: ReactNode): string {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join('');
  if (typeof node === 'object' && 'props' in node) {
    const props = (node as { props?: { children?: ReactNode } }).props;
    return textOf(props?.children);
  }
  return '';
}

function slug(text: string): string {
  return text.toLowerCase().trim().replace(/[^\w\s-]/g, '').replace(/\s+/g, '-');
}

function basename(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return name.endsWith('.md') ? name.slice(0, -3) : name;
}

// A terminal task isn't waiting on a next attempt, so its scheduled time is noise.
const TASK_TERMINAL_STATES = new Set(['done', 'dead']);

// formatNextAttempt renders an RFC3339 next_attempt_at as a short relative phrase
// ("in 2m", "5s ago", "now"). Returns '' for an unparseable/zero timestamp so the
// row can omit it. now is injectable for deterministic tests.
function formatNextAttempt(iso: string, now: number = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  // The runner stamps a zero time (year <= 1) when there is no scheduled attempt.
  if (new Date(t).getUTCFullYear() <= 1) return '';
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
