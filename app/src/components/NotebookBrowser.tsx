import { createElement, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import FocusTrap from 'focus-trap-react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { NotebookEntry, NotebookReadResult } from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './NotebookBrowser.css';

interface NotebookBrowserProps {
  isOpen: boolean;
  onClose: () => void;
  listNotebook: () => Promise<NotebookEntry[]>;
  readNotebook: (path: string) => Promise<NotebookReadResult>;
  backlinksNotebook: (path: string) => Promise<NotebookEntry[]>;
  // Increments whenever a notebook_changed event arrives, so an open browser
  // re-fetches the tree and the open note (covering agent and external writes).
  changeSignal?: number;
}

// The note shown first when the browser opens with nothing selected, in order of
// preference. /memory/index.md is the distilled map an agent is told to read.
const PREFERRED_FIRST = ['memory/index.md', 'index.md'];

export function NotebookBrowser({
  isOpen,
  onClose,
  listNotebook,
  readNotebook,
  backlinksNotebook,
  changeSignal = 0,
}: NotebookBrowserProps) {
  const [entries, setEntries] = useState<NotebookEntry[]>([]);
  const [listError, setListError] = useState<string | null>(null);
  const [listLoading, setListLoading] = useState(false);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [note, setNote] = useState<NotebookReadResult | null>(null);
  const [noteError, setNoteError] = useState<string | null>(null);
  const [noteLoading, setNoteLoading] = useState(false);
  const [backlinks, setBacklinks] = useState<NotebookEntry[]>([]);
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

  const loadNote = useCallback(async (path: string) => {
    const seq = ++loadSeqRef.current;
    setSelectedPath(path);
    setNoteLoading(true);
    setNoteError(null);
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

  // On open, load the tree and select a sensible first note.
  useEffect(() => {
    if (!isOpen) return;
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
                <span className="notebook-browser-eyebrow">Durable memory</span>
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
                      <span className="notebook-browser-list-marker" data-kind={entry.kind || 'note'} />
                      <span className="notebook-browser-list-copy">
                        <strong>{entry.title || basename(entry.path)}</strong>
                        <span>{entry.path}</span>
                      </span>
                    </button>
                  ))}
                </div>
              ))}
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
                    <h2>{selectedEntry?.title || basename(note.path)}</h2>
                    <p>{note.path}</p>
                  </div>
                  <article className="notebook-browser-markdown">
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
              {!noteLoading && !noteError && !note && (
                <div className="notebook-browser-document-state">
                  <NotebookIcon />
                  <h2>Nothing selected</h2>
                  <p>Choose a note from the list to read it.</p>
                </div>
              )}
            </main>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}

interface NoteGroup {
  label: string;
  entries: NotebookEntry[];
}

const GROUP_ORDER = ['Journal', 'Memory', 'Notebook'];

// groupEntries buckets notes by their top-level directory for the sidebar:
// journal/* -> Journal, memory/* -> Memory, root files -> Notebook.
function groupEntries(entries: NotebookEntry[]): NoteGroup[] {
  const buckets = new Map<string, NotebookEntry[]>();
  for (const entry of entries) {
    const slash = entry.path.indexOf('/');
    const top = slash === -1 ? '' : entry.path.slice(0, slash);
    const label = top === 'journal' ? 'Journal' : top === 'memory' ? 'Memory' : top === '' ? 'Notebook' : capitalize(top);
    const list = buckets.get(label) || [];
    list.push(entry);
    buckets.set(label, list);
  }
  return [...buckets.entries()]
    .map(([label, list]) => ({ label, entries: list }))
    .sort((a, b) => groupRank(a.label) - groupRank(b.label) || a.label.localeCompare(b.label));
}

function groupRank(label: string): number {
  const idx = GROUP_ORDER.indexOf(label);
  return idx === -1 ? GROUP_ORDER.length : idx;
}

export interface NotebookHref {
  kind: 'note' | 'fragment' | 'external';
  // For 'note': the notebook-relative path (no leading slash, e.g. "memory/foo.md").
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

function capitalize(s: string): string {
  return s.length === 0 ? s : s[0].toUpperCase() + s.slice(1);
}

function NotebookIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M6 3.5h11a1 1 0 0 1 1 1V20a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z" />
      <path d="M9 3.5V21M12 8h4M12 11.5h4" />
    </svg>
  );
}
