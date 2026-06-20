// A lazy filesystem tree over the daemon's generic fs surface. It renders the real
// folder hierarchy under the root: the root's immediate children on mount, and each
// directory's children only when it is expanded (one fs_list per opened node — the
// tree never walks the whole disk up front). Files are selectable; directories
// toggle open/closed. An fs_changed bump re-lists the root and every open directory
// so external/agent edits surface without losing the user's expansion state.
//
// This component is purely presentational over the injected `listDir` — it knows
// nothing about the websocket — so it is unit-testable with a mock and reusable
// wherever a filesystem tree is needed (the notebook browser is the first consumer).

import { type CSSProperties, useCallback, useEffect, useRef, useState } from 'react';
import type { FsEntry } from '../../hooks/useDaemonSocket';
import './FileTree.css';

interface FileTreeProps {
  // List one directory's immediate children. '' = the root directory itself.
  listDir: (path: string) => Promise<FsEntry[]>;
  // The currently selected FILE path (root-relative), highlighted in the tree.
  selectedPath: string | null;
  // A file node was activated (clicked). Directories never call this — they toggle.
  onSelectFile: (path: string) => void;
  // Bumped whenever fs content changes, so the tree re-lists its open directories.
  changeSignal?: number;
}

// The root directory's key in the children/expanded/loading maps. Kept distinct from
// any real entry path (entry paths are always non-empty) so the root never collides.
const ROOT = '';

export function FileTree({ listDir, selectedPath, onSelectFile, changeSignal = 0 }: FileTreeProps) {
  // Per-directory listing cache (key = directory path, ROOT for the root). Presence
  // of a key means that directory has been listed at least once.
  const [children, setChildren] = useState<Map<string, FsEntry[]>>(new Map());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Map<string, string>>(new Map());

  // Avoid setting state after unmount (a slow list resolving post-close).
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const markLoading = useCallback((dir: string, on: boolean) => {
    setLoading((prev) => {
      const next = new Set(prev);
      if (on) next.add(dir);
      else next.delete(dir);
      return next;
    });
  }, []);

  // List one directory and fold the result into the caches. Errors are recorded per
  // directory (so one failed node does not blank the rest); a successful list clears
  // any prior error for that directory.
  const loadDir = useCallback(
    async (dir: string) => {
      markLoading(dir, true);
      try {
        const entries = await listDir(dir);
        if (!mountedRef.current) return;
        setChildren((prev) => new Map(prev).set(dir, entries));
        setErrors((prev) => {
          if (!prev.has(dir)) return prev;
          const next = new Map(prev);
          next.delete(dir);
          return next;
        });
      } catch (err) {
        if (!mountedRef.current) return;
        setErrors((prev) =>
          new Map(prev).set(dir, err instanceof Error ? err.message : 'Could not list this folder'),
        );
      } finally {
        if (mountedRef.current) markLoading(dir, false);
      }
    },
    [listDir, markLoading],
  );

  // List the root on mount.
  useEffect(() => {
    void loadDir(ROOT);
    // loadDir is stable for a stable listDir; we intentionally list once on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadDir]);

  // Re-list the root and every open directory when fs content changes, preserving
  // expansion. (Skip the initial 0 so this does not double-list on mount.)
  useEffect(() => {
    if (changeSignal === 0) return;
    void loadDir(ROOT);
    for (const dir of expanded) void loadDir(dir);
    // expanded is read as a live snapshot; we don't want to re-run when it changes,
    // only when the change signal bumps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [changeSignal, loadDir]);

  // Toggle a directory open/closed. List it on first open (no cached listing yet);
  // a re-open reuses the cache. `expanded`/`children` are read from the closure
  // (both are deps), so the load decision is made outside any setState updater —
  // updaters must stay pure (StrictMode invokes them twice).
  const toggleDir = useCallback(
    (dir: string) => {
      const isOpen = expanded.has(dir);
      setExpanded((prev) => {
        const next = new Set(prev);
        if (next.has(dir)) next.delete(dir);
        else next.add(dir);
        return next;
      });
      if (!isOpen && !children.has(dir)) void loadDir(dir);
    },
    [expanded, children, loadDir],
  );

  // Render one directory's entries at the given depth. Recurses into open
  // subdirectories. Returns null when the directory has not been listed yet.
  const renderDir = (dir: string, depth: number) => {
    const entries = children.get(dir);
    const isLoading = loading.has(dir);
    const error = errors.get(dir);

    if (error) {
      return (
        <li className="file-tree-state file-tree-error" style={indent(depth)} role="treeitem">
          {error}
        </li>
      );
    }
    if (entries === undefined) {
      return isLoading ? (
        <li className="file-tree-state" style={indent(depth)} role="treeitem">
          Loading…
        </li>
      ) : null;
    }
    if (entries.length === 0) {
      return (
        <li className="file-tree-state file-tree-empty" style={indent(depth)} role="treeitem">
          Empty
        </li>
      );
    }
    return entries.map((entry) =>
      entry.isDir ? (
        <li key={entry.path} role="none">
          <button
            type="button"
            role="treeitem"
            aria-expanded={expanded.has(entry.path)}
            className="file-tree-row file-tree-dir"
            style={indent(depth)}
            onClick={() => toggleDir(entry.path)}
            title={entry.path}
          >
            <span className={`file-tree-chevron${expanded.has(entry.path) ? ' is-open' : ''}`} aria-hidden="true">
              ▸
            </span>
            <span className="file-tree-name">{entry.name}</span>
          </button>
          {expanded.has(entry.path) && (
            <ul role="group" className="file-tree-group">
              {renderDir(entry.path, depth + 1)}
            </ul>
          )}
        </li>
      ) : (
        <li key={entry.path} role="none">
          <button
            type="button"
            role="treeitem"
            aria-current={entry.path === selectedPath ? 'true' : undefined}
            className={`file-tree-row file-tree-file${entry.path === selectedPath ? ' is-selected' : ''}`}
            style={indent(depth)}
            onClick={() => onSelectFile(entry.path)}
            title={entry.path}
          >
            <span className="file-tree-name">{entry.name}</span>
          </button>
        </li>
      ),
    );
  };

  const rootEntries = children.get(ROOT);
  const rootLoading = loading.has(ROOT);
  const rootError = errors.get(ROOT);

  return (
    <ul className="file-tree" role="tree" aria-label="Files">
      {rootError ? (
        <li className="file-tree-state file-tree-error" role="treeitem">
          {rootError}
        </li>
      ) : rootEntries === undefined ? (
        rootLoading ? (
          <li className="file-tree-state" role="treeitem">
            Loading…
          </li>
        ) : null
      ) : rootEntries.length === 0 ? (
        <li className="file-tree-state file-tree-empty" role="treeitem">
          This folder is empty.
        </li>
      ) : (
        renderDir(ROOT, 0)
      )}
    </ul>
  );
}

// Indent a row by its depth. Kept as inline padding (not a per-depth class) so the
// tree nests to any depth without a fixed ladder of CSS rules.
function indent(depth: number): CSSProperties {
  return { paddingLeft: `${8 + depth * 14}px` };
}
