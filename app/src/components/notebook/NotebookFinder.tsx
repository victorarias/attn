import { useMemo, useState } from 'react';
import type { NotebookEntry } from '../../hooks/useDaemonSocket';
import { Palette } from '../palette/Palette';
import { finderBasename, rankFiles } from '../palette/rank';

// The in-tile fuzzy file finder: a Cmd+P-style overlay scoped INSIDE one notebook
// tile (not a global portal), so two tiles each summon their own. It owns its
// query and ranks the tile's file index live; the shared Palette owns the overlay
// chrome, keyboard navigation, and selection. Picking a note hands its path up
// (the surface opens + persists it). Esc closes it.
export function NotebookFinder({
  files,
  loading,
  onPick,
  onClose,
}: {
  files: NotebookEntry[];
  loading: boolean;
  onPick: (path: string) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState('');
  const results = useMemo(() => rankFiles(files, query), [files, query]);

  return (
    <Palette
      variant="notebook-finder"
      ariaLabel="Find a note"
      placeholder="Find a note…"
      query={query}
      onQueryChange={setQuery}
      items={results}
      itemKey={(entry) => entry.path}
      emptyLabel={loading ? 'Loading notes…' : 'No notes match.'}
      onPick={(entry) => onPick(entry.path)}
      onClose={onClose}
      renderItem={(entry) => (
        <>
          <span className="palette-option-title notebook-finder-option-title">
            {entry.title || finderBasename(entry.path)}
          </span>
          <span className="palette-option-path notebook-finder-option-path">{entry.path}</span>
        </>
      )}
    />
  );
}
