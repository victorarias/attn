import { useEffect, useMemo, useRef, useState } from 'react';
import type { NotebookEntry } from '../../hooks/useDaemonSocket';
import { finderBasename, rankNotebookFiles } from './finderRank';

// The in-tile fuzzy file finder: a Cmd+P-style overlay scoped INSIDE one notebook
// tile (not a global portal), so two tiles each summon their own. It owns its
// query, ranks the tile's file index live, and drives keyboard selection; picking
// a note hands its path up (the surface opens + persists it). Esc closes it.
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
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  const results = useMemo(() => rankNotebookFiles(files, query), [files, query]);
  // Clamp the highlight into range (the index shrinks as you type, or as the file
  // index refreshes) so Enter never picks a phantom row.
  const activeIndex = results.length === 0 ? -1 : Math.min(selected, results.length - 1);

  // Take focus on mount so typing lands in the finder immediately, not the editor.
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // Reset the highlight to the top whenever the query changes (best match first).
  useEffect(() => {
    setSelected(0);
  }, [query]);

  // Keep the highlighted row visible as arrow-key navigation moves it.
  useEffect(() => {
    if (activeIndex < 0) return;
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [activeIndex]);

  const pick = (entry: NotebookEntry | undefined) => {
    if (entry) onPick(entry.path);
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    switch (event.key) {
      case 'Escape':
        // Stop here: closing the finder must not also bubble to a workspace-level
        // Escape handler (e.g. closing a pane).
        event.preventDefault();
        event.stopPropagation();
        onClose();
        break;
      case 'ArrowDown':
        event.preventDefault();
        setSelected((i) => Math.min(i + 1, results.length - 1));
        break;
      case 'ArrowUp':
        event.preventDefault();
        setSelected((i) => Math.max(i - 1, 0));
        break;
      case 'Enter':
        event.preventDefault();
        pick(results[activeIndex]);
        break;
      default:
        break;
    }
  };

  return (
    <div
      className="notebook-finder"
      role="dialog"
      aria-label="Find a note"
      // A click on the dim backdrop (outside the box) dismisses the finder.
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className="notebook-finder-box">
        <input
          ref={inputRef}
          className="notebook-finder-input"
          type="text"
          placeholder="Find a note…"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={onKeyDown}
          role="combobox"
          aria-expanded
          aria-controls="notebook-finder-list"
          aria-activedescendant={activeIndex >= 0 ? `nb-finder-opt-${activeIndex}` : undefined}
          spellCheck={false}
          autoComplete="off"
        />
        <ul id="notebook-finder-list" ref={listRef} className="notebook-finder-list" role="listbox">
          {results.length === 0 ? (
            <li className="notebook-finder-empty">{loading ? 'Loading notes…' : 'No notes match.'}</li>
          ) : (
            results.map((entry, index) => (
              <li
                key={entry.path}
                id={`nb-finder-opt-${index}`}
                role="option"
                aria-selected={index === activeIndex}
                className={`notebook-finder-option${index === activeIndex ? ' is-selected' : ''}`}
                onMouseEnter={() => setSelected(index)}
                // mousedown (not click), preventDefault: pick without first yanking
                // focus out of the input (which would flicker before the close).
                onMouseDown={(event) => { event.preventDefault(); pick(entry); }}
              >
                <span className="notebook-finder-option-title">{entry.title || finderBasename(entry.path)}</span>
                <span className="notebook-finder-option-path">{entry.path}</span>
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  );
}
