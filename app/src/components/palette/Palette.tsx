import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import './Palette.css';

// The shared ⌘P-style overlay shell: a dimmed backdrop, a single text input, and
// a keyboard-driven listbox. It owns the interaction contract — focus on mount,
// Arrow/Enter/Escape, highlight clamping, scroll-into-view, backdrop dismissal,
// and the combobox/listbox ARIA wiring — and knows nothing about what the rows
// mean. Callers own the query (so they can rewrite it, e.g. to descend into a
// directory), the ranking, and how a row renders.
//
// Every element carries both a stable `palette-*` class (styled once, in
// Palette.css) and a `<variant>-*` class, so each caller keeps a hook for its own
// positioning or visual tweaks — and, for the Notebook finder, the class names its
// existing e2e and packaged-app scenarios already select on.
export interface PaletteProps<T> {
  // Class/id namespace for this instance, e.g. "notebook-finder".
  variant: string;
  ariaLabel: string;
  placeholder: string;
  query: string;
  onQueryChange: (query: string) => void;
  items: T[];
  itemKey: (item: T) => string;
  renderItem: (item: T) => ReactNode;
  // Shown in place of the list when items is empty — the caller decides whether
  // that means "still loading" or "nothing matched".
  emptyLabel: string;
  onPick: (item: T) => void;
  onClose: () => void;
  // Escape hatch for keys the shell does not own (Tab-completion, for instance).
  // Return true to signal the key was handled and stop the shell's own handling.
  onKeyDown?: (event: KeyboardEvent<HTMLInputElement>) => boolean;
}

export function Palette<T>({
  variant,
  ariaLabel,
  placeholder,
  query,
  onQueryChange,
  items,
  itemKey,
  renderItem,
  emptyLabel,
  onPick,
  onClose,
  onKeyDown,
}: PaletteProps<T>) {
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  // Clamp the highlight into range (the list shrinks as you type, or as the
  // underlying index refreshes) so Enter never picks a phantom row.
  const activeIndex = items.length === 0 ? -1 : Math.min(selected, items.length - 1);

  // Take focus on mount so typing lands here immediately, not in whatever was
  // focused behind the overlay.
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

  const pick = (item: T | undefined) => {
    if (item !== undefined) onPick(item);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (onKeyDown?.(event)) return;
    switch (event.key) {
      case 'Escape':
        // Stop here: closing the palette must not also bubble to a
        // workspace-level Escape handler (e.g. closing a pane).
        event.preventDefault();
        event.stopPropagation();
        onClose();
        break;
      case 'ArrowDown':
        event.preventDefault();
        setSelected((i) => Math.min(i + 1, items.length - 1));
        break;
      case 'ArrowUp':
        event.preventDefault();
        setSelected((i) => Math.max(i - 1, 0));
        break;
      case 'Enter':
        event.preventDefault();
        pick(items[activeIndex]);
        break;
      default:
        break;
    }
  };

  const listId = `${variant}-list`;

  return (
    <div
      className={`palette ${variant}`}
      role="dialog"
      aria-label={ariaLabel}
      // A click on the dim backdrop (outside the box) dismisses the palette.
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className={`palette-box ${variant}-box`}>
        <input
          ref={inputRef}
          className={`palette-input ${variant}-input`}
          type="text"
          placeholder={placeholder}
          value={query}
          onChange={(event) => onQueryChange(event.target.value)}
          onKeyDown={handleKeyDown}
          role="combobox"
          aria-expanded
          aria-controls={listId}
          aria-activedescendant={activeIndex >= 0 ? `${variant}-opt-${activeIndex}` : undefined}
          spellCheck={false}
          autoComplete="off"
        />
        <ul id={listId} ref={listRef} className={`palette-list ${variant}-list`} role="listbox">
          {items.length === 0 ? (
            <li className={`palette-empty ${variant}-empty`}>{emptyLabel}</li>
          ) : (
            items.map((item, index) => (
              <li
                key={itemKey(item)}
                id={`${variant}-opt-${index}`}
                role="option"
                aria-selected={index === activeIndex}
                className={`palette-option ${variant}-option${index === activeIndex ? ' is-selected' : ''}`}
                onMouseEnter={() => setSelected(index)}
                // mousedown (not click), preventDefault: pick without first yanking
                // focus out of the input (which would flicker before the close).
                onMouseDown={(event) => { event.preventDefault(); pick(item); }}
              >
                {renderItem(item)}
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  );
}
