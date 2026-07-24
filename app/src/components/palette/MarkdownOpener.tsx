import { useEffect, useMemo, useState } from 'react';
import { Palette } from './Palette';
import { finderBasename } from './rank';
import { mergeOpenerFiles, rankOpenerFiles } from './openerRank';
import { isPathQuery, toBrowseInput, descendQuery } from './pathMode';
import { useFilesystemSuggestions } from '../../hooks/useFilesystemSuggestions';
import type { BrowseDirectoryResult } from '../../hooks/useDaemonSocket';

// Markdown only, for now: the pick opens a reader tile, and that tile renders
// markdown. The caller passes this to fs_index and browse_directory so the
// server-side entry cap and the directory listing both apply to markdown alone.
export const OPENER_EXTENSIONS = ['md'];

export interface MarkdownOpenerProps {
  // The tree fuzzy mode searches: the selected session's working directory,
  // falling back to the notebook root when no session is selected. null means
  // neither is known, so only recents are available.
  root: string | null;
  loadRecents: () => Promise<{ path: string; lastAt: string }[]>;
  loadIndex: (root: string) => Promise<{ files: string[]; truncated: boolean }>;
  // Lists one directory for path mode. Omitted, path mode is unavailable and a
  // path query simply matches nothing.
  browseDirectory?: (inputPath: string, endpointId?: string, extensions?: string[]) => Promise<BrowseDirectoryResult>;
  onPick: (absPath: string) => void;
  onClose: () => void;
}

// A row is either a fuzzy/recents result or a path-mode entry. Directories are
// the one thing that is picked without opening anything: picking one rewrites
// the query to descend into it.
interface OpenerRow {
  key: string;
  title: string;
  path: string;
  isDir: boolean;
  /** Absolute for files; for directories, the query to descend with. */
  target: string;
}

// The global ⌘P file opener. Two modes behind one input:
//
//  - Fuzzy (default): recently opened markdown files on an empty query, a fuzzy
//    filter over the workspace's markdown once you type, both in one ranked list.
//  - Path: a query that starts with /, ~, ./ or ../ lists that directory one
//    level at a time, so a file outside the workspace — or one .gitignore hides
//    from the index — is still reachable. Picking a directory descends.
//
// The shared Palette owns the overlay, keyboard navigation, and selection; this
// owns the data, the ranking, and the mode.
//
// Both fuzzy sources load asynchronously and independently: the palette opens on
// whatever it has, so a cold index enumeration never delays ⌘P.
export function MarkdownOpener({ root, loadRecents, loadIndex, browseDirectory, onPick, onClose }: MarkdownOpenerProps) {
  const [query, setQuery] = useState('');
  const [recents, setRecents] = useState<{ path: string; lastAt: string }[]>([]);
  const [indexFiles, setIndexFiles] = useState<string[]>([]);
  const [truncated, setTruncated] = useState(false);
  const [indexLoading, setIndexLoading] = useState(!!root);

  useEffect(() => {
    let cancelled = false;
    void loadRecents()
      .then((files) => { if (!cancelled) setRecents(files); })
      .catch((error) => { console.error('[MarkdownOpener] recent files failed:', error); });
    return () => { cancelled = true; };
  }, [loadRecents]);

  useEffect(() => {
    if (!root) {
      setIndexFiles([]);
      setIndexLoading(false);
      return;
    }
    let cancelled = false;
    setIndexLoading(true);
    void loadIndex(root)
      .then((result) => {
        if (cancelled) return;
        setIndexFiles(result.files);
        setTruncated(result.truncated);
      })
      .catch((error) => {
        if (!cancelled) console.error('[MarkdownOpener] file index failed:', error);
      })
      .finally(() => { if (!cancelled) setIndexLoading(false); });
    return () => { cancelled = true; };
  }, [root, loadIndex]);

  const browseInput = toBrowseInput(query, root);
  const pathMode = isPathQuery(query);
  // Path mode is local-only, like the rest of the opener: no endpoint id.
  const { suggestions, loading: browseLoading, error: browseError } = useFilesystemSuggestions(
    browseInput || '',
    undefined,
    browseDirectory,
    { enabled: pathMode && !!browseInput, extensions: OPENER_EXTENSIONS },
  );

  const candidates = useMemo(
    () => mergeOpenerFiles(recents, root, indexFiles),
    [recents, root, indexFiles],
  );
  const fuzzyRows = useMemo<OpenerRow[]>(
    () => rankOpenerFiles(candidates, query).map((file) => ({
      key: file.absPath,
      title: finderBasename(file.label),
      path: file.label,
      isDir: false,
      target: file.absPath,
    })),
    [candidates, query],
  );
  const pathRows = useMemo<OpenerRow[]>(
    () => suggestions.map((entry) => ({
      key: entry.absPath,
      title: entry.isDir ? `${entry.name}/` : entry.name,
      path: entry.path,
      isDir: entry.isDir,
      target: entry.isDir ? descendQuery(entry.path) : entry.absPath,
    })),
    [suggestions],
  );
  const rows = pathMode ? pathRows : fuzzyRows;

  const emptyLabel = pathMode
    ? pathModeEmptyLabel(browseInput, browseLoading, browseError, !!browseDirectory)
    // The index is capped server-side; say so rather than implying the list is
    // the whole tree.
    : indexLoading
      ? 'Loading files…'
      : query.trim() === ''
        ? 'No recently opened files. Type to search, or a path to browse.'
        : truncated
          ? 'No files match (the index is capped, so some files are missing). Type a path to browse.'
          : 'No files match. Type a path to browse.';

  return (
    <Palette<OpenerRow>
      variant="markdown-opener"
      ariaLabel="Open a markdown file"
      placeholder="Open a markdown file…"
      query={query}
      onQueryChange={setQuery}
      items={rows}
      itemKey={(row) => row.key}
      emptyLabel={emptyLabel}
      onPick={(row) => {
        // Descending is a query rewrite, not an open: the palette stays up and
        // the next listing lands from the same input.
        if (row.isDir) setQuery(row.target);
        else onPick(row.target);
      }}
      onClose={onClose}
      renderItem={(row) => (
        <>
          <span className="palette-option-title markdown-opener-option-title">{row.title}</span>
          <span className="palette-option-path markdown-opener-option-path">{row.path}</span>
        </>
      )}
    />
  );
}

function pathModeEmptyLabel(
  browseInput: string | null,
  loading: boolean,
  error: string | null,
  hasBrowse: boolean,
): string {
  if (!hasBrowse) return 'Browsing paths is unavailable.';
  // A relative query with no workspace root has nothing to resolve against.
  if (!browseInput) return 'No folder to resolve this path against.';
  if (loading) return 'Listing…';
  if (error) return 'That folder could not be listed.';
  return 'Nothing here matches.';
}
