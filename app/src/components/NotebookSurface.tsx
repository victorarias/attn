import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import type { FsEntry, FsExistsResult, FsReadAssetResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { useNotebookFileIndex } from '../hooks/useNotebookFileIndex';
import { useTileAutoFold } from '../hooks/useTileAutoFold';
import { notebookLinkPath } from './notebook/brokenLinks';
import { FileTree } from './notebook/FileTree';
import { fileKind, isBinaryPath, isMarkdownPath } from './notebook/fileKind';
import { parseFrontmatter } from './notebook/frontmatter';
import { headingSlug, noteDir, resolveNotebookLink } from './notebook/linkResolver';
import { LiveMarkdownEditor, type LiveMarkdownEditorHandle, type LiveSelection } from './notebook/LiveMarkdownEditor';
import { NotebookFinder } from './notebook/NotebookFinder';
import { registerPaletteClaim } from './palette/paletteClaim';
import { parseOutline } from './notebook/outline';
import './NotebookBrowser.css';

// The full Notebook body — file tree, live editor, context rail, fold handles,
// tasks panel, and the load/save/send-to-chief logic. Rendered either as a
// `modal` (dialog shell, owned by NotebookBrowser) or as a bare `tile`; the two
// share every behavior. A closed modal stays MOUNTED, so the surface gates its
// work on `active` rather than unmounting.
export interface NotebookSurfaceProps {
  // Which frame to render: 'modal' draws the dialog shell + header, 'tile' bare.
  variant: 'modal' | 'tile';
  // Live or idle-but-mounted. Gates on-open selection, live refresh, tasks fetch.
  active: boolean;
  // The file to open first; tiles persist it.
  initialPath?: string | null;
  // Modal close (persist-then-close). A tile has no Close button, so it omits this.
  onClose?: () => void;
  // A tile reports the opened path (never the cleared state) so the parent can
  // persist it; the modal's selection isn't persisted.
  onOpenFile?: (path: string) => void;
  // One directory's immediate children; '' = the notebook root.
  listDir: (path: string) => Promise<FsEntry[]>;
  // Read one file's full bytes + content hash (for hash-CAS edits).
  readFile: (path: string) => Promise<FsReadResult>;
  // Save via hash-CAS: omit baseHash to create-only, pass the loaded hash to edit.
  writeFile: (path: string, content: string, baseHash?: string) => Promise<FsWriteResult>;
  // Existence check (no read) behind the editor's broken-link flags; markdown only.
  existsFile: (path: string) => Promise<FsExistsResult>;
  // Image bytes for the inline image widget; only for non-direct srcs.
  readAsset: (path: string) => Promise<FsReadAssetResult>;
  // Backlinks for a markdown note. Optional: an off-root tile omits it and gets no
  // backlinks rail, keeping this surface root-unaware.
  backlinksNotebook?: (path: string) => Promise<NotebookEntry[]>;
  // Hand a selection to the daemon for the chief of staff; the UI never messages
  // the chief directly. Optional — omitting it removes the floating send button.
  sendToChief?: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  // Increments on every fs_changed, re-listing the tree and reloading the open file.
  changeSignal?: number;
  // Whole-vault walk behind Cmd+P. Optional: omitting it disables the finder.
  listFiles?: () => Promise<NotebookEntry[]>;
  // Chief pulse: working / idle / undefined = no chief (indicator hidden).
  chiefActive?: boolean;
}

// Entry points probed in order when nothing is selected. Probed by direct read:
// the sidebar lists lazily and has no flat catalogue to scan.
const PREFERRED_FIRST = ['knowledge/index.md', 'index.md'];

// Idle time before an autosave fires; coalesces a keystroke burst into one write.
const AUTOSAVE_DELAY_MS = 700;

// Outcome of persisting the buffer. Callers MUST react to 'conflict'/'error':
// dropping one loses the user's edits behind a navigation with no banner shown.
export type PersistOutcome = 'saved' | 'conflict' | 'error' | 'noop';

// Escape hatch for callers that must flush the dirty buffer ahead of a state
// transition they own (NotebookTile's root switcher). The only persistence seam.
export interface NotebookSurfaceHandle {
  // Persists the dirty buffer against its synced base ('noop' when in sync). On
  // 'conflict'/'error' the banner is already up and the caller must abort its
  // transition rather than proceed past the failure.
  flushPendingSave: () => Promise<PersistOutcome>;
}

export const NotebookSurface = forwardRef<NotebookSurfaceHandle, NotebookSurfaceProps>(function NotebookSurface({
  variant,
  active,
  initialPath,
  onClose,
  onOpenFile,
  listDir,
  readFile,
  writeFile,
  existsFile,
  readAsset,
  backlinksNotebook,
  sendToChief,
  changeSignal = 0,
  listFiles,
  chiefActive,
}: NotebookSurfaceProps, ref) {
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [note, setNote] = useState<FsReadResult | null>(null);
  const [noteError, setNoteError] = useState<string | null>(null);
  const [noteLoading, setNoteLoading] = useState(false);
  const [backlinks, setBacklinks] = useState<NotebookEntry[]>([]);
  // Backlinks load independently of, and far slower than, the content: without
  // this flag the panel asserts the PREVIOUS note's links until the walk resolves.
  const [backlinksLoading, setBacklinksLoading] = useState(false);
  // Lets the change-signal effect reload without depending on selectedPath.
  const selectedPathRef = useRef<string | null>(null);
  selectedPathRef.current = selectedPath;
  // Bumped synchronously, so a superseded navigation's slow response is dropped.
  const loadSeqRef = useRef(0);
  // Persists the outgoing buffer before a navigation/close hides it, surfacing a
  // CAS conflict. Via a ref so loadFile need not depend on declaration order.
  const persistRef = useRef<() => Promise<PersistOutcome>>(async () => 'noop');
  // Initial focus target: inside the trap, without preselecting Close.
  const dialogRef = useRef<HTMLDivElement>(null);
  // Observed by the tile's auto-fold; fold-independent, so it cannot oscillate.
  const bodyRef = useRef<HTMLDivElement>(null);
  // Lets the outline scroll the editor to a heading from outside the editor.
  const editorRef = useRef<LiveMarkdownEditorHandle>(null);
  // The rail's two sections fold independently, both open by default.
  const [outlineOpen, setOutlineOpen] = useState(true);
  const [backlinksOpen, setBacklinksOpen] = useState(true);
  // Drives a dedicated escape-stack entry so the first Esc closes just ⌘F.
  const [searchOpen, setSearchOpen] = useState(false);
  // Tri-state per side: null follows the auto default, true/false is an explicit
  // override. A folded pane drops to 0 width but stays mounted, so CodeMirror and
  // scroll state survive.
  const [treeOverride, setTreeOverride] = useState<boolean | null>(null);
  const [railOverride, setRailOverride] = useState<boolean | null>(null);
  // Auto fold: a tile folds rail then tree as it narrows; a manual override wins.
  const { treeAutoFold, railAutoFold } = useTileAutoFold(bodyRef, variant === 'tile');
  const treeFolded = treeOverride === null ? treeAutoFold : treeOverride;
  const railFolded = railOverride === null ? railAutoFold : railOverride;
  // --- Fuzzy finder (Cmd+P) ---
  // Present whenever listFiles is; the index only walks while the surface shows.
  const finderEnabled = !!listFiles;
  const finderActive = variant === 'tile' || active;
  const [finderOpen, setFinderOpen] = useState(false);
  const { files: finderFiles, loading: finderLoading } = useNotebookFileIndex(listFiles, changeSignal, finderEnabled && finderActive);
  // Captured on open: focus falling to <body> would strand Cmd+P, whose keydown
  // is scoped to the surface container.
  const finderReturnFocusRef = useRef<HTMLElement | null>(null);
  const openFinder = useCallback(() => {
    finderReturnFocusRef.current = document.activeElement as HTMLElement | null;
    setFinderOpen(true);
  }, []);
  // Claim ⌘P while focus is inside, so two tiles never fight over one binding.
  useEffect(() => {
    if (!finderEnabled) return;
    return registerPaletteClaim({ container: () => dialogRef.current, open: openFinder });
  }, [finderEnabled, openFinder]);
  // The same summon from the container's own keydown, which is what makes the
  // surface work standalone. preventDefault stops the WebView print dialog, and
  // Shift is excluded because Cmd+Shift+P is the global attention dock.
  const handleSurfaceKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'p') {
      event.preventDefault();
      event.stopPropagation();
      if (finderEnabled) openFinder();
    }
  }, [finderEnabled, openFinder]);
  // Restore focus inside the surface on close, or Cmd+P stops working.
  const finderWasOpenRef = useRef(false);
  useEffect(() => {
    if (finderWasOpenRef.current && !finderOpen) {
      const prev = finderReturnFocusRef.current;
      finderReturnFocusRef.current = null;
      const container = dialogRef.current;
      if (prev && container?.contains(prev)) {
        prev.focus();
      } else {
        container?.focus();
      }
    }
    finderWasOpenRef.current = finderOpen;
  }, [finderOpen]);
  // Parsed off the loaded content, not the live draft, so it doesn't churn per
  // keystroke; self-contained so it can sit above the !active early return.
  const noteType = useMemo(() => {
    if (!note || !selectedPath) return null;
    const kind = fileKind(selectedPath);
    if (kind === 'markdown') {
      const type = parseFrontmatter(note.content)?.fields.type;
      return typeof type === 'string' && type.trim() ? type.trim() : 'note';
    }
    if (kind === 'text') return 'text';
    return null;
  }, [note, selectedPath]);

  // Unsaved edits persist first; a conflict keeps the modal open to reconcile.
  const requestClose = useCallback(async () => {
    if (dirtyRef.current) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    onClose?.();
  }, [onClose]);
  const handleEscape = useCallback(() => void requestClose(), [requestClose]);

  // Esc closes the modal. The stack is a capture-phase window listener, so it
  // beats the finder input's own onKeyDown; a second entry pushed while the finder
  // is open sits on top (LIFO) and closes just the finder.
  useEscapeStack(handleEscape, variant === 'modal' && active);
  useEscapeStack(() => setFinderOpen(false), variant === 'modal' && active && finderOpen);
  // ⌘F pushes its own entry while open, so the first Esc closes just the panel.
  useEscapeStack(() => { editorRef.current?.closeSearchPanel(); }, active && searchOpen);

  // Load `path`; `prefetched` seeds the editor without a second read.
  const loadFile = useCallback(async (path: string, prefetched?: FsReadResult) => {
    // Persist the outgoing buffer first, covering an edit inside the debounce
    // window. A conflicting write ABORTS the navigation so the banner can be
    // reconciled; navigating away would discard the edits silently.
    if (dirtyRef.current && selectedPathRef.current && selectedPathRef.current !== path) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    const seq = ++loadSeqRef.current;
    setSelectedPath(path);
    // A tile persists the opened path; the modal passes no handler.
    onOpenFile?.(path);
    // Loading replaces the content, so a floating send button is now misplaced.
    setChiefSel(null);
    // Drop backlinks when the load STARTS, not when the new walk resolves, or the
    // panel shows the previous selection's links meanwhile.
    setBacklinks([]);
    setBacklinksLoading(false);

    // Never read a binary file: fs_read returns a string, meaningless for bytes.
    if (isBinaryPath(path)) {
      setNote(null);
      setDraft('');
      setNoteError(null);
      setNoteLoading(false);
      return;
    }

    setNoteError(null);
    if (prefetched) {
      // Already read by the caller; a fresh load is never dirty.
      setNote(prefetched);
      setDraft(prefetched.content);
      setNoteLoading(false);
    } else {
      setNoteLoading(true);
      // Content and backlinks load INDEPENDENTLY: one fast read versus a walk of
      // every note. Each guards on the load token so a superseded navigation drops.
      void readFile(path)
        .then((value) => {
          if (loadSeqRef.current !== seq) return;
          setNote(value);
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
    // Markdown only; a failure yields no backlinks and must not blank the file.
    if (isMarkdownPath(path) && backlinksNotebook) {
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
  }, [readFile, backlinksNotebook, onOpenFile]);

  // Bumping loadSeqRef stops a late load resurrecting the just-cleared file.
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
  // `draft` is the live buffer, `note` the value last synced from disk. Dirty =
  // they diverge, and dirty autosaves debounced via hash-CAS against note.hash.
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  // Set when a CAS rejected; carries the on-disk hash so the user can overwrite.
  const [conflict, setConflict] = useState<{ currentHash?: string } | null>(null);
  const [justSaved, setJustSaved] = useState(false);
  // Refs so the persist and live-refresh paths read latest state without deps.
  const draftRef = useRef('');
  draftRef.current = draft;
  const noteRef = useRef<FsReadResult | null>(null);
  noteRef.current = note;
  // Gates autosave and the live reload: a disk reload must not clobber a buffer.
  const dirty = note ? draft !== note.content : false;
  const dirtyRef = useRef(false);
  dirtyRef.current = dirty;

  // --- Send to chief ---
  // Selection plus frozen viewport coords for its floating action button.
  const [chiefSel, setChiefSel] = useState<LiveSelection | null>(null);
  const [sendingToChief, setSendingToChief] = useState(false);
  // A transient outcome line ("Added to chief's inbox" / an error), auto-dismissed.
  const [chiefStatus, setChiefStatus] = useState<{ text: string; error: boolean } | null>(null);

  // Core write: hash-CAS `content` against `baseHash` and reconcile. The outcome
  // returns so callers can react — 'conflict'/'error' must NOT be dropped. An
  // empty baseHash is create-only, recreating a file deleted while edited.
  const writeBuffer = useCallback(async (baseHash: string, content: string): Promise<PersistOutcome> => {
    const path = selectedPathRef.current;
    if (!path) return 'noop';
    // Freeze (never bump) the load token, so a navigation landing mid-write is
    // detected on resolve. The bytes still reach disk and the outcome still
    // returns; we just don't stamp the result onto whatever file is now shown.
    const seq = loadSeqRef.current;
    setSaving(true);
    setSaveError(null);
    try {
      const res = await writeFile(path, content, baseHash || undefined);
      const superseded = loadSeqRef.current !== seq || selectedPathRef.current !== path;
      if (res.conflict) {
        // Diverged on disk: reconcile rather than clobber, if still shown.
        if (!superseded) setConflict({ currentHash: res.currentHash });
        return 'conflict';
      }
      if (!superseded) {
        // Advance the synced base to the bytes written; typing during the save
        // leaves draft ahead, still dirty, so the autosave effect fires again.
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

  // Drives both the debounced autosave and the navigate/close flush, so a
  // conflicting outgoing edit raises the banner instead of being dropped.
  const persist = useCallback(async (): Promise<PersistOutcome> => {
    const current = noteRef.current;
    if (!current) return 'noop';
    const content = draftRef.current;
    if (content === current.content) return 'noop'; // in sync — nothing to persist
    return writeBuffer(current.hash, content);
  }, [writeBuffer]);
  // Indirection so callers declared above the editing block reach latest persist.
  persistRef.current = persist;

  useImperativeHandle(ref, () => ({
    flushPendingSave: () => persistRef.current?.() ?? Promise.resolve('noop'),
  }), []);

  // The conflict banner's "reload from disk" path.
  const reloadFromDisk = useCallback(async () => {
    const path = selectedPathRef.current;
    if (!path) return;
    // Freeze the load token, or a slow reload of A stamps its content onto B.
    const seq = loadSeqRef.current;
    setConflict(null);
    setSaveError(null);
    try {
      const fresh = await readFile(path);
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setNote(fresh);
      setDraft(fresh.content);
      // Pull focus off the banner button so typing works with no extra click.
      editorRef.current?.focus();
    } catch (err) {
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setSaveError(err instanceof Error ? err.message : 'Could not reload this file');
    }
  }, [readFile]);

  // Live refresh of the OPEN file, keeping the reader in place where loadFile
  // resets. Two properties matter: it touches NO state when the bytes are
  // identical (fs_changed fires for any file under the root, so an unrelated write
  // must not re-scroll the note you are reading), and a genuine change is applied
  // as a minimal edit through the editor handle so CodeMirror stays anchored.
  const refreshOpenFile = useCallback(async () => {
    const path = selectedPathRef.current;
    // A binary selection shows a placeholder we never read; nothing to reload.
    if (!path || isBinaryPath(path)) return;
    // Freeze (do NOT bump) the token, so a navigation mid-read drops this refresh.
    const seq = loadSeqRef.current;
    let fresh: FsReadResult;
    try {
      fresh = await readFile(path);
    } catch (err) {
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      // Deleted on disk: report it honestly (the tree drops the node on its own re-list).
      setNote(null);
      setDraft('');
      setNoteError(err instanceof Error ? err.message : 'Could not read this file');
      return;
    }
    if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
    // Unchanged: skipping every setState is the point — scroll and selection hold.
    if (noteRef.current && fresh.hash === noteRef.current.hash) return;
    // Preserve the viewport: markdown takes a minimal edit through the handle.
    if (isMarkdownPath(path)) {
      editorRef.current?.applyExternalContent(fresh.content);
    }
    setNote(fresh);
    setDraft(fresh.content);
    setNoteError(null);
    // Content moved, so links may have; absent (off-root tile) is a no-op.
    if (isMarkdownPath(path) && backlinksNotebook) {
      setBacklinksLoading(true);
      void backlinksNotebook(path)
        .then((entries) => {
          if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
          setBacklinks(entries);
          setBacklinksLoading(false);
        })
        .catch(() => {
          if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
          setBacklinks([]);
          setBacklinksLoading(false);
        });
    }
  }, [readFile, backlinksNotebook]);

  // The daemon appends to the chief inbox; the UI never messages the chief.
  const sendSelectionToChief = useCallback(async () => {
    if (!chiefSel || !sendToChief) return;
    const path = selectedPathRef.current ?? undefined;
    // Freeze the load token so a late outcome doesn't flash on another file.
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

  // First file: the modal keeps a prior selection that still reads, else probes
  // the entry points, else the root's first file; a tile seeds from its path.
  useEffect(() => {
    if (!active) return;
    // A reopen on the SAME file doesn't change selectedPath, so that reset can't
    // clear a stale transient outcome; do it here.
    setChiefStatus(null);
    setChiefSel(null);
    setJustSaved(false);
    let cancelled = false;
    void (async () => {
      if (variant === 'tile') {
        // A fresh tile (no seed) opens straight into the finder.
        const seed = initialPath ?? null;
        if (!seed) {
          if (!cancelled) {
            clearSelection();
            if (finderEnabled) setFinderOpen(true);
          }
          return;
        }
        if (isBinaryPath(seed)) {
          if (!cancelled) void loadFile(seed);
          return;
        }
        try {
          const res = await readFile(seed);
          if (!cancelled) void loadFile(seed, res);
        } catch {
          if (!cancelled) clearSelection();
        }
        return;
      }
      if (initialPath) {
        try {
          const res = await readFile(initialPath);
          if (!cancelled) void loadFile(initialPath, res);
          return;
        } catch {
          // Fall through to the modal's normal entry points.
        }
      }
      // Keep the current selection if it still exists (a reopen on the same file).
      const current = selectedPathRef.current;
      if (current) {
        // Preserved WITHOUT reading: a probe leaks the fs_read the gate prevents.
        if (isBinaryPath(current)) {
          if (!cancelled) void loadFile(current);
          return;
        }
        // Probe by reading, reusing the read to seed the editor.
        try {
          const res = await readFile(current);
          if (!cancelled) void loadFile(current, res);
          return;
        } catch {
          // Fell away while closed; fall through to pick a fresh entry point.
        }
      }
      // First entry point that reads wins, and its read seeds the editor.
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
    // Only re-run when (re)activating; navigation is driven by loadFile directly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  // fs_changed refreshes the open document only, and only when its bytes changed.
  useEffect(() => {
    if (!active || changeSignal === 0) return;
    // Never reload over unsaved edits; divergence surfaces as a save conflict.
    if (dirtyRef.current) return;
    void refreshOpenFile();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changeSignal]);

  // Keyed on selectedPath, so it fires on navigation but not on a live reload.
  useEffect(() => {
    setConflict(null);
    setSaveError(null);
    setJustSaved(false);
    setChiefSel(null);
    setChiefStatus(null);
    // The editor is un-keyed, so an open search panel survives the switch.
    editorRef.current?.closeSearchPanel();
    setSearchOpen(false);
  }, [selectedPath]);

  // Debounced autosave, gated off while loading (the buffer is being re-seeded),
  // while a save is in flight, and while a conflict is unresolved. Every dep change
  // clears the timer, so navigation cannot leave a stale write scheduled.
  useEffect(() => {
    if (!note || noteLoading || saving || conflict) return;
    if (draft === note.content) return; // in sync — nothing to save
    const timer = window.setTimeout(() => {
      void persist();
    }, AUTOSAVE_DELAY_MS);
    return () => window.clearTimeout(timer);
  }, [draft, note, noteLoading, saving, conflict, persist]);

  // Transient confirmation; errors linger a little longer than successes.
  useEffect(() => {
    if (!chiefStatus) return;
    const timer = window.setTimeout(() => setChiefStatus(null), chiefStatus.error ? 6000 : 3000);
    return () => window.clearTimeout(timer);
  }, [chiefStatus]);

  // The button sits at frozen viewport coords, so any geometry change invalidates
  // it. Scroll is captured, not bubbled, or a nested code-block scroller slips
  // past and strands the button over the wrong text.
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

  // The navigation reset fires only on a path change, so without this timer the
  // "Saved" badge sticks while the user keeps reading the same file.
  useEffect(() => {
    if (!justSaved) return;
    const timer = window.setTimeout(() => setJustSaved(false), 2500);
    return () => window.clearTimeout(timer);
  }, [justSaved]);

  // Match by slug, or by raw text for a heading that doesn't slug-match its link.
  const scrollToAnchor = useCallback((anchor: string) => {
    const wanted = headingSlug(anchor);
    const heading = parseOutline(draftRef.current).find(
      (h) => headingSlug(h.text) === wanted || h.text.toLowerCase() === anchor.toLowerCase(),
    );
    if (heading) editorRef.current?.scrollToPos(heading.pos);
  }, []);

  // Mod-click routes: in-notebook target navigates, same-note anchor scrolls,
  // external opens in the browser. Cross-note anchors (`other.md#heading`) are
  // NOT handled: loadFile is fire-and-forget with no signal that content landed.
  const handleFollowLink = useCallback((href: string) => {
    const resolved = resolveNotebookLink(href, noteDir(selectedPathRef.current ?? ''));
    if (resolved.kind === 'note') {
      if (resolved.path === selectedPathRef.current && resolved.anchor) {
        scrollToAnchor(resolved.anchor);
      } else {
        void loadFile(resolved.path);
      }
    } else if (resolved.kind === 'fragment') {
      scrollToAnchor(resolved.anchor);
    } else if (resolved.href) {
      window.open(resolved.href, '_blank', 'noreferrer');
    }
  }, [loadFile]);

  // Inert without sendToChief: no selection tracked, so no floating button.
  const handleSelectionChange = useCallback((selection: LiveSelection | null) => {
    if (!sendToChief) return;
    setChiefSel(selection);
  }, [sendToChief]);

  // Strip the #fragment/?query tail (notebookLinkPath, as brokenLinks does), read
  // the bytes, hand back a data: URI. A non-notebook path or a failed read both
  // resolve to null, which the widget renders as its broken placeholder.
  const resolveImageSrc = useCallback(async (src: string) => {
    const path = notebookLinkPath(src, noteDir(selectedPathRef.current ?? ''));
    if (!path) return null;
    try {
      const asset = await readAsset(path);
      return `data:${asset.mimeType};base64,${asset.dataBase64}`;
    } catch {
      return null;
    }
  }, [readAsset]);

  // Derived from the LIVE buffer so heading positions match what the editor holds.
  // Must run before the !active early return, so it is gated by path kind.
  const selectedIsMarkdown = selectedPath ? isMarkdownPath(selectedPath) : false;
  const outline = useMemo(
    () => (selectedIsMarkdown ? parseOutline(draft) : []),
    [selectedIsMarkdown, draft],
  );

  // A closed modal keeps its state but renders nothing; a tile is always active.
  if (variant === 'modal' && !active) return null;

  const selectedKind = selectedPath ? fileKind(selectedPath) : null;
  const showBinaryPlaceholder = selectedPath !== null && selectedKind === 'binary';
  // Markdown-only, and also gated on backlinksNotebook: an off-root tile omits it,
  // and the whole rail is withheld rather than shown half-capable.
  const showRail = selectedKind === 'markdown' && !!note && !!backlinksNotebook;
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

  const body = (
    <div
      ref={bodyRef}
      className={`notebook-browser-body${showRail ? ' has-rail' : ''}${treeFolded ? ' tree-folded' : ''}${showRail && railFolded ? ' rail-folded' : ''}`}
    >
      {/* `inert` while folded removes the whole pane from the tab order and the
          a11y tree, so a keyboard user can't Tab into the invisible file/task
          controls of a collapsed pane (aria-hidden alone leaves them focusable). */}
      <aside
        className="notebook-browser-list"
        aria-label="Notebook files"
        aria-hidden={treeFolded}
        inert={treeFolded}
      >
        <FileTree
          listDir={listDir}
          selectedPath={selectedPath}
          onSelectFile={(path) => void loadFile(path)}
          changeSignal={changeSignal}
        />
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
                <div className="notebook-browser-document-titlerow">
                  <h2>{basename(note.path)}</h2>
                  {noteType && (
                    <span className={`notebook-browser-kind-badge${noteType === 'journal' ? ' is-journal' : ' is-note'}`}>
                      {noteType}
                    </span>
                  )}
                </div>
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
                    <button
                      type="button"
                      onClick={() => {
                        void (async () => {
                          await writeBuffer(conflict.currentHash ?? '', draft);
                          // Pull focus back to the editor so typing needs no click.
                          editorRef.current?.focus();
                        })();
                      }}
                      disabled={saving}
                    >
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
                    resolveImageSrc={resolveImageSrc}
                    revalidateSignal={changeSignal}
                    notePath={selectedPath ?? ''}
                    ariaLabel="Note"
                    onSearchOpenChange={setSearchOpen}
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
            {finderEnabled ? (
              <>
                <p>Find a note, or pick one from the tree.</p>
                <button
                  type="button"
                  className="notebook-finder-open-button"
                  onClick={openFinder}
                >
                  <span>Find a note</span><kbd>⌘P</kbd>
                </button>
              </>
            ) : (
              <p>Choose a file from the tree to read it.</p>
            )}
          </div>
        )}
      </main>

      {showRail && (
        <aside
          className="notebook-browser-rail"
          aria-label="Context"
          aria-hidden={railFolded}
          inert={railFolded}
        >
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
                          // Accessible name is the title alone: AT would spell
                          // the path out character by character.
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

      <button
        type="button"
        className="notebook-browser-fold notebook-browser-fold-tree"
        aria-label={treeFolded ? 'Show file tree' : 'Hide file tree'}
        aria-expanded={!treeFolded}
        // Keep focus on the editor — a fold should never pull the caret away.
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => setTreeOverride(!treeFolded)}
      >
        {treeFolded ? '›' : '‹'}
      </button>
      {showRail && (
        <button
          type="button"
          className="notebook-browser-fold notebook-browser-fold-rail"
          aria-label={railFolded ? 'Show context rail' : 'Hide context rail'}
          aria-expanded={!railFolded}
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => setRailOverride(!railFolded)}
        >
          {railFolded ? '‹' : '›'}
        </button>
      )}
    </div>
  );

  // One finder shared by both variants: they differ in shell, not in finder.
  const finderOverlay = finderOpen ? (
    <NotebookFinder
      files={finderFiles}
      loading={finderLoading}
      onPick={(path) => { void loadFile(path); setFinderOpen(false); }}
      onClose={() => setFinderOpen(false)}
    />
  ) : null;

  const floatingChief = chiefSel && sendToChief ? (
    <button
      type="button"
      className="notebook-browser-send-chief"
      style={{ top: chiefSel.top, left: chiefSel.left }}
      // Keep the selection intact so onClick reads it uncollapsed.
      onMouseDown={(event) => event.preventDefault()}
      onClick={() => void sendSelectionToChief()}
      disabled={sendingToChief}
    >
      {sendingToChief ? 'Sending…' : 'Send to chief'}
    </button>
  ) : null;

  // Tile: a bare surface that fills its workspace tile (no overlay/focus-trap/header).
  if (variant === 'tile') {
    return (
      <div
        ref={dialogRef}
        tabIndex={-1}
        className="notebook-surface notebook-surface-tile"
        onKeyDown={handleSurfaceKeyDown}
      >
        {body}
        {floatingChief}
        {finderOverlay}
      </div>
    );
  }

  // Modal: the fullscreen dialog shell.
  return (
    <div className="notebook-browser-shell">
      <FocusTrap focusTrapOptions={{ escapeDeactivates: false, initialFocus: () => dialogRef.current ?? false }}>
        <div ref={dialogRef} tabIndex={-1} className="notebook-browser" role="dialog" aria-modal="true" aria-labelledby="notebook-browser-title" onKeyDown={handleSurfaceKeyDown}>
          <header className="notebook-browser-header">
            <div className="notebook-browser-heading">
              <NotebookIcon />
              <div>
                <span className="notebook-browser-eyebrow">Knowledge base</span>
                <h1 id="notebook-browser-title">Notebook</h1>
              </div>
            </div>
            <div className="notebook-browser-chrome">
              {chiefActive !== undefined && (
                <span
                  className={`notebook-browser-chief-pulse${chiefActive ? ' is-active' : ''}`}
                  role="status"
                >
                  <span className="notebook-browser-chief-dot" aria-hidden="true" />
                  chief: {chiefActive ? 'active' : 'idle'}
                </span>
              )}
              <button type="button" className="notebook-browser-close" onClick={() => void requestClose()}>
                <span>Close</span><kbd>esc</kbd>
              </button>
            </div>
          </header>
          {body}
          {floatingChief}
          {finderOverlay}
        </div>
      </FocusTrap>
    </div>
  );
});

function basename(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  return name.endsWith('.md') ? name.slice(0, -3) : name;
}

function NotebookIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 3.5h11a1 1 0 0 1 1 1V20a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z" />
      <path d="M9 3.5V21M12 8h4M12 11.5h4" />
    </svg>
  );
}
