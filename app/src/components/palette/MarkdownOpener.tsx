import { useEffect, useMemo, useState } from 'react';
import { Palette } from './Palette';
import { finderBasename } from './rank';
import { mergeOpenerFiles, rankOpenerFiles, type OpenerFile } from './openerRank';

// Markdown only, for now: the pick opens a reader tile, and that tile renders
// markdown. The caller passes this to fs_index so the server-side entry cap
// applies to markdown alone.
export const OPENER_EXTENSIONS = ['md'];

export interface MarkdownOpenerProps {
  // The tree fuzzy mode searches: the selected session's working directory,
  // falling back to the notebook root when no session is selected. null means
  // neither is known, so only recents are available.
  root: string | null;
  loadRecents: () => Promise<{ path: string; lastAt: string }[]>;
  loadIndex: (root: string) => Promise<{ files: string[]; truncated: boolean }>;
  onPick: (absPath: string) => void;
  onClose: () => void;
}

// The global ⌘P file opener: recently opened markdown files on an empty query,
// a fuzzy filter over the workspace's markdown once you type, both in one
// ranked list. The shared Palette owns the overlay, keyboard navigation, and
// selection; this owns the data and the ranking.
//
// Both sources load asynchronously and independently: the palette opens on
// whatever it has, so a cold index enumeration never delays ⌘P.
export function MarkdownOpener({ root, loadRecents, loadIndex, onPick, onClose }: MarkdownOpenerProps) {
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

  const candidates = useMemo(
    () => mergeOpenerFiles(recents, root, indexFiles),
    [recents, root, indexFiles],
  );
  const results = useMemo(() => rankOpenerFiles(candidates, query), [candidates, query]);

  // The index is capped server-side; say so rather than implying the list is
  // the whole tree.
  const emptyLabel = indexLoading
    ? 'Loading files…'
    : query.trim() === ''
      ? 'No recently opened files. Type to search.'
      : truncated
        ? 'No files match (the index is capped, so some files are missing).'
        : 'No files match.';

  return (
    <Palette<OpenerFile>
      variant="markdown-opener"
      ariaLabel="Open a markdown file"
      placeholder="Open a markdown file…"
      query={query}
      onQueryChange={setQuery}
      items={results}
      itemKey={(file) => file.absPath}
      emptyLabel={emptyLabel}
      onPick={(file) => onPick(file.absPath)}
      onClose={onClose}
      renderItem={(file) => (
        <>
          <span className="palette-option-title markdown-opener-option-title">
            {finderBasename(file.label)}
          </span>
          <span className="palette-option-path markdown-opener-option-path">{file.label}</span>
        </>
      )}
    />
  );
}
