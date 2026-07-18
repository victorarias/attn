import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
import { parseOutline } from './notebook/outline';
import './NotebookBrowser.css';

// NotebookSurface is the full Notebook body — file tree, live editor, context rail,
// fold handles, tasks panel, and all the load/save/send-to-chief logic. It renders
// in two shapes:
//   - `modal`: wrapped in the dialog shell (overlay + focus trap + header/Close),
//     the fullscreen Notebook (NotebookBrowser owns this wrapper).
//   - `tile`: bare, to live inside a workspace tile beside terminals.
// The variants share every behavior; only the outer frame (and the modal-only
// header chrome) differ. The surface stays MOUNTED while a modal is closed
// (state/refs survive a close→reopen), so it gates its work on `active` rather
// than unmounting.
export interface NotebookSurfaceProps {
  // Which frame to render (see above). 'modal' draws the dialog shell + header;
  // 'tile' draws a bare surface for a workspace tile.
  variant: 'modal' | 'tile';
  // Whether the surface is live. The modal passes its isOpen (the surface stays
  // mounted but idle while closed); a tile is always active once mounted. Gates the
  // on-open file selection, the live-refresh reload, and the tasks fetch.
  active: boolean;
  // The file to open first. Tiles persist it; the modal uses it when another
  // surface opens a specific Notebook file.
  initialPath?: string | null;
  // Modal close (persist-then-close). A tile has no Close button, so it omits this.
  onClose?: () => void;
  // Called when a tile opens a file (a real path — never the cleared/no-selection
  // state), so the parent can persist the path to the tile's params. Modal ignores
  // it (its selection isn't persisted).
  onOpenFile?: (path: string) => void;
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
  // Read an image asset's bytes (base64) for the live editor's inline image widget.
  // Only consulted for a non-direct src (not http(s)/data:/protocol-relative).
  readAsset: (path: string) => Promise<FsReadAssetResult>;
  // Backlinks ("Linked from") for a markdown note. Notebook-specific (walks .md link
  // graphs), so it is only consulted for .md files. Optional: a caller that omits it
  // (an off-root tile — see NotebookTile) gets no backlinks rail at all; the surface
  // stays root-unaware and simply renders the affordances it's handed.
  backlinksNotebook?: (path: string) => Promise<NotebookEntry[]>;
  // Hand a highlighted selection to the daemon to deliver to the chief of staff
  // (appends to the chief inbox note + best-effort live PTY nudge). The UI never
  // messages the chief directly. sourcePath is the note the selection came from.
  // Optional: a caller that omits it (an off-root tile) gets no floating "Send to
  // chief" button at all.
  sendToChief?: (selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>;
  // Increments whenever an fs_changed event arrives, so an open browser re-lists the
  // tree (handled by FileTree) and reloads the open file (covering agent and external
  // writes).
  changeSignal?: number;
  // Walk the whole vault (flat list of notes, with titles) for the in-tile fuzzy
  // finder. Tile-only: when provided, a tile gains its Cmd+P finder; the modal
  // omits it (it navigates via the tree), so it's optional.
  listFiles?: () => Promise<NotebookEntry[]>;
  // The chief-pulse state for the modal top-bar indicator: true = a chief-of-staff
  // session is working, false = a chief exists but is idle, undefined = no chief
  // session at all (the indicator is hidden). Modal-only chrome; tiles omit it.
  chiefActive?: boolean;
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

export function NotebookSurface({
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
}: NotebookSurfaceProps) {
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
  // The body container, observed by the tile's responsive auto-fold. Its width is
  // independent of how the inner panes fold, so folding can't shrink the observed
  // box and oscillate.
  const bodyRef = useRef<HTMLDivElement>(null);
  // Imperative handle to the live editor, so the context rail's outline can scroll
  // the editor to a heading (navigation that originates outside the editor).
  const editorRef = useRef<LiveMarkdownEditorHandle>(null);
  // The right context rail's two sections fold independently; both open by default so
  // the outline and backlinks are visible without a click. Local UI state only.
  const [outlineOpen, setOutlineOpen] = useState(true);
  const [backlinksOpen, setBacklinksOpen] = useState(true);
  // Whether the live editor's in-CodeMirror search panel (⌘F) is currently open.
  // Drives a dedicated escape-stack entry so the first Esc closes just the panel.
  const [searchOpen, setSearchOpen] = useState(false);
  // Whole-pane edge-rail folds (manual). Tri-state per side: null = follow the auto
  // default, true/false = an explicit user override. The auto default is a hardcoded
  // `false` here — stage 7 PR3 (tile auto-fold) swaps it for a width-driven value, so
  // the fold derivation changes but the toggle handlers never do. Folded panes drop
  // to 0 width but stay mounted (CodeMirror/scroll state survives).
  const [treeOverride, setTreeOverride] = useState<boolean | null>(null);
  const [railOverride, setRailOverride] = useState<boolean | null>(null);
  // The auto side of the fold seam. A modal folds nothing automatically (manual
  // only, unchanged); a tile folds its rail then its tree as it narrows. A manual
  // override still wins (`override ?? auto`), so this only changes what auto means.
  const { treeAutoFold, railAutoFold } = useTileAutoFold(bodyRef, variant === 'tile');
  const treeFolded = treeOverride === null ? treeAutoFold : treeOverride;
  const railFolded = railOverride === null ? railAutoFold : railOverride;
  // --- Fuzzy finder (Cmd+P) ---
  // The finder lists the whole vault. Both surfaces get one as long as listFiles is
  // provided (a tile reads it from context; the fullscreen modal is handed it
  // explicitly) — omitting listFiles disables the finder entirely. The index walks on
  // mount and refreshes on fs changes, but only while the surface is actually showing:
  // a tile is always live, a modal only when open, so a closed-but-mounted modal never
  // walks the vault.
  const finderEnabled = !!listFiles;
  const finderActive = variant === 'tile' || active;
  const [finderOpen, setFinderOpen] = useState(false);
  const { files: finderFiles, loading: finderLoading } = useNotebookFileIndex(listFiles, changeSignal, finderEnabled && finderActive);
  // Where to return focus when the finder closes. Captured on open so closing the
  // finder lands focus back where it was (the editor, usually) rather than letting
  // it fall to <body> — which would strand Cmd+P, since the re-summon keydown is
  // scoped to the surface container and only fires when focus is inside it.
  const finderReturnFocusRef = useRef<HTMLElement | null>(null);
  const openFinder = useCallback(() => {
    finderReturnFocusRef.current = document.activeElement as HTMLElement | null;
    setFinderOpen(true);
  }, []);
  // Summon the finder with Cmd+P from inside the surface. Scoped to this container's
  // keydown so a tile only responds when focus is in THIS tile (two tiles don't fight
  // over one global binding), and the modal responds only while it's the focused
  // surface; preventDefault stops the WebView's print dialog. Shift is excluded —
  // Cmd+Shift+P is the global attention dock.
  const handleSurfaceKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'p') {
      event.preventDefault();
      event.stopPropagation();
      if (finderEnabled) openFinder();
    }
  }, [finderEnabled, openFinder]);
  // On close, restore focus inside the surface so Cmd+P keeps working: back to the
  // element the finder opened over if it's still in this surface, else the surface
  // container itself. (Runs only on a true→false transition, never on mount.)
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
  // The kind/type pill in the note header: a markdown note's frontmatter `type`
  // (defaulting to "note"), or "text" for a plain-text file. Parsed off the loaded
  // content (not the live draft) so it doesn't churn on every keystroke. Self-
  // contained (calls fileKind itself) so it can sit above the !active early return.
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

  // Closing with unsaved edits persists them first; if that write conflicts (or
  // errors) we keep the modal open so the conflict banner can be reconciled, rather
  // than losing the buffer behind the close. dirtyRef short-circuits the clean case
  // to an immediate close (no write, no await).
  const requestClose = useCallback(async () => {
    if (dirtyRef.current) {
      const outcome = await persistRef.current();
      if (outcome === 'conflict' || outcome === 'error') return;
    }
    onClose?.();
  }, [onClose]);
  const handleEscape = useCallback(() => void requestClose(), [requestClose]);

  // Esc closes the modal. The finder, when open over the modal, needs a higher-
  // priority Esc that closes the finder FIRST: the escape stack is a capture-phase
  // window listener, so it beats the finder input's own onKeyDown and would otherwise
  // collapse the whole modal. A second entry (pushed after, so LIFO puts it on top)
  // closes just the finder while it's open. (A tile isn't modal — its finder's Esc is
  // handled by the finder input's onKeyDown directly, no stack involved.)
  useEscapeStack(handleEscape, variant === 'modal' && active);
  useEscapeStack(() => setFinderOpen(false), variant === 'modal' && active && finderOpen);
  // The in-editor search panel (⌘F) gets its own higher-priority Esc entry, pushed
  // only while it's open — LIFO puts it above the modal-close entry, so the first
  // Esc closes just the search panel. Not gated on variant: a tile's search panel
  // also closes via the centralized stack (a tile has no modal-close entry to race).
  useEscapeStack(() => { editorRef.current?.closeSearchPanel(); }, active && searchOpen);

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
    // Let the parent persist the opened path (a tile writes it to its params); the
    // modal passes no handler, so its selection isn't persisted.
    onOpenFile?.(path);
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
    // Absent (an off-root tile) is a no-op: no fetch, backlinks stay empty.
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
      // The banner's "Reload from disk" button still has focus at this point; pull it
      // back to the editor so typing works immediately, with no extra click.
      editorRef.current?.focus();
    } catch (err) {
      if (loadSeqRef.current !== seq || selectedPathRef.current !== path) return;
      setSaveError(err instanceof Error ? err.message : 'Could not reload this file');
    }
  }, [readFile]);

  // Live refresh of the OPEN file after an fs_changed event. Unlike loadFile (a
  // navigation: full reset, fresh editor, scroll to top), this keeps the reader in
  // place. Two properties matter:
  //   - It bails WITHOUT touching any state when the file is byte-identical on disk.
  //     fs_changed fires for ANY file under the notebook root, so the open note is
  //     re-read on every unrelated write only to check it — skipping all the setState
  //     when nothing changed is what stops an unrelated edit from disturbing (and
  //     re-scrolling) the note you're reading, and flashing its backlinks.
  //   - On a GENUINE change it applies the new bytes as a minimal edit through the
  //     editor handle, so CodeMirror keeps its scroll/selection anchored instead of
  //     snapping to the top, and re-walks backlinks (the links may have moved).
  const refreshOpenFile = useCallback(async () => {
    const path = selectedPathRef.current;
    // A binary selection shows a placeholder we never read; nothing to reload.
    if (!path || isBinaryPath(path)) return;
    // Freeze (do NOT bump) the load token: a navigation that lands mid-read bumps it,
    // and we then drop this stale refresh rather than stamp it over the new selection.
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
    // Unchanged on disk → nothing to apply. Skipping every setState here is the whole
    // point: the editor's scroll position and selection are never touched.
    if (noteRef.current && fresh.hash === noteRef.current.hash) return;
    // Genuine change: apply it preserving the reader's viewport. A markdown note takes a
    // minimal edit via the editor handle (CM keeps its scroll anchored); a plain-text
    // file — or a not-yet-mounted editor — falls through to the direct value set below.
    if (isMarkdownPath(path)) {
      editorRef.current?.applyExternalContent(fresh.content);
    }
    setNote(fresh);
    setDraft(fresh.content);
    setNoteError(null);
    // Content moved, so links may have too — re-walk backlinks for a markdown note.
    // Absent (an off-root tile) is a no-op: no fetch, backlinks stay empty.
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

  // Hand the captured selection to the daemon for the chief of staff. The daemon
  // appends it to the chief inbox note and best-effort nudges a live chief; the
  // UI only surfaces the outcome and never messages the chief directly.
  const sendSelectionToChief = useCallback(async () => {
    if (!chiefSel || !sendToChief) return;
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

  // On open (modal) / mount (tile), select a sensible first file. The modal keeps the
  // prior selection if it still reads, else probes the preferred entry points, else
  // the first file at the root. A tile seeds from its persisted open-file path. The
  // lazy sidebar lists itself; this only chooses what to show.
  useEffect(() => {
    if (!active) return;
    // Start clean: a transient outcome/selection from a prior session must not
    // reappear on reopen. The [selectedPath] reset can't cover a reopen on the
    // same file (selectedPath doesn't change), so clear it here.
    setChiefStatus(null);
    setChiefSel(null);
    setJustSaved(false);
    let cancelled = false;
    void (async () => {
      if (variant === 'tile') {
        // A tile reopens to its persisted file (if any). A fresh tile (no seed) opens
        // straight into the finder so you can pick a note without hunting the tree.
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
    // Only re-run when (re)activating; navigation is driven by loadFile directly.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  // Live refresh: an fs_changed event refreshes the open file so agent/external writes
  // show up without reopening. The sidebar tree re-lists itself via its own
  // changeSignal prop; here we only refresh the open document — and only when its bytes
  // actually changed (refreshOpenFile no-ops on an unrelated write), keeping the reader
  // anchored where they were.
  useEffect(() => {
    if (!active || changeSignal === 0) return;
    // With unsaved edits, never reload — that would clobber the buffer. On-disk
    // divergence surfaces as a save-time conflict instead.
    if (dirtyRef.current) return;
    void refreshOpenFile();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changeSignal]);

  // Navigating to another file clears the previous file's edit status. Keyed on
  // selectedPath, so it fires on navigation but not on a same-file live reload
  // (which keeps selectedPath unchanged).
  useEffect(() => {
    setConflict(null);
    setSaveError(null);
    setJustSaved(false);
    setChiefSel(null);
    setChiefStatus(null);
    // Navigating away can keep the same CodeMirror view alive (the editor is
    // un-keyed), so an open search panel would survive the switch with no
    // escape-stack entry. Close the panel itself; the open-change callback
    // resets searchOpen when the view is still mounted, and the direct reset
    // covers the unmounted case.
    editorRef.current?.closeSearchPanel();
    setSearchOpen(false);
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

  // Scroll to the current note's heading whose GitHub-style slug matches `anchor`
  // (also accepting an exact raw-text case-insensitive match, for a heading that
  // pre-dates or otherwise doesn't slug-match its own link). No match is a no-op.
  const scrollToAnchor = useCallback((anchor: string) => {
    const wanted = headingSlug(anchor);
    const heading = parseOutline(draftRef.current).find(
      (h) => headingSlug(h.text) === wanted || h.text.toLowerCase() === anchor.toLowerCase(),
    );
    if (heading) editorRef.current?.scrollToPos(heading.pos);
  }, []);

  // Mod-click on a rendered link: an in-notebook target navigates (resolved against
  // the current note's directory); a same-note anchor scrolls to the matching
  // heading; an external target opens in the browser. Cross-note anchors (a link
  // like `other.md#heading`) are not yet handled — loadFile is a fire-and-forget
  // navigation with no way to learn when the new note's content has landed without
  // reshaping it, so a jump there is deferred to a follow-up.
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

  // The editor reports its current selection (or null when collapsed); float the
  // "Send to chief" action over it. Inert when sendToChief is absent (an off-root
  // tile) — never tracks a selection, so the floating button can never render.
  const handleSelectionChange = useCallback((selection: LiveSelection | null) => {
    if (!sendToChief) return;
    setChiefSel(selection);
  }, [sendToChief]);

  // Resolve an inline image's src for the live editor's image widget: strip any
  // #fragment/?query tail (notebookLinkPath — same rule brokenLinks uses), then read
  // the asset's bytes and hand back a data: URI. A non-notebook path (already
  // rejected by notebookLinkPath) or a failed read both resolve to null, which the
  // widget renders as its broken placeholder.
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

  // The outline is a markdown affordance only, derived from the LIVE buffer so it
  // tracks edits as you type. Indexing into `draft` keeps heading positions aligned
  // with what the editor holds, so a jump lands on the right line. (Hook: must run
  // before the !active early return, so it is gated by the path kind, not by render.)
  const selectedIsMarkdown = selectedPath ? isMarkdownPath(selectedPath) : false;
  const outline = useMemo(
    () => (selectedIsMarkdown ? parseOutline(draft) : []),
    [selectedIsMarkdown, draft],
  );

  // A closed modal keeps its state but renders nothing; a tile is always active.
  if (variant === 'modal' && !active) return null;

  const selectedKind = selectedPath ? fileKind(selectedPath) : null;
  const showBinaryPlaceholder = selectedPath !== null && selectedKind === 'binary';
  // The context rail (outline + backlinks) is a markdown-document affordance; a text
  // or binary file shows neither, so it keeps the two-pane layout (no empty rail).
  // Also gated on backlinksNotebook being provided: an off-root tile (NotebookTile)
  // omits it, and the rail's Backlinks section has no other source, so the whole
  // rail (Outline included) is withheld rather than showing a half-capable rail.
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
                          // The button still has focus at this point; pull it back to the
                          // editor so typing works immediately, with no extra click.
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

  const floatingChief = chiefSel && sendToChief ? (
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
        {finderOpen && (
          <NotebookFinder
            files={finderFiles}
            loading={finderLoading}
            onPick={(path) => { void loadFile(path); setFinderOpen(false); }}
            onClose={() => setFinderOpen(false)}
          />
        )}
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
          {finderOpen && (
            <NotebookFinder
              files={finderFiles}
              loading={finderLoading}
              onPick={(path) => { void loadFile(path); setFinderOpen(false); }}
              onClose={() => setFinderOpen(false)}
            />
          )}
        </div>
      </FocusTrap>
    </div>
  );
}

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
