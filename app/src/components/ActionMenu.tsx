import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { KeyCombos } from './Keycap';
import './ActionMenu.css';

export interface ActionMenuItem {
  id: string;
  title: string;
  description: string;
  keywords?: string[];
  icon: ReactNode;
  shortcut?: string[][];
  run: () => void;
}

interface ActionMenuProps {
  isOpen: boolean;
  actions: ActionMenuItem[];
  onClose: () => void;
}

function actionScore(action: ActionMenuItem, query: string): number {
  if (!query) return 1;
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  const title = action.title.toLowerCase();
  const searchable = [action.title, action.description, ...(action.keywords || [])].join(' ').toLowerCase();
  if (!terms.every((term) => searchable.includes(term))) return 0;
  return terms.reduce((score, term) => {
    if (title.startsWith(term)) return score + 4;
    if (title.includes(term)) return score + 2;
    return score + 1;
  }, 0);
}

export function ActionMenu({ isOpen, actions, onClose }: ActionMenuProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const filteredActions = useMemo(() => actions
    .map((action) => ({ action, score: actionScore(action, query) }))
    .filter(({ score }) => score > 0)
    .sort((left, right) => right.score - left.score), [actions, query]);

  useEscapeStack(onClose, isOpen);

  useEffect(() => {
    if (!isOpen) return;
    setQuery('');
    setSelectedIndex(0);
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [isOpen]);

  useEffect(() => {
    setSelectedIndex(0);
  }, [query]);

  if (!isOpen) return null;

  const runAction = (action: ActionMenuItem) => {
    onClose();
    action.run();
  };

  return (
    <div className="action-menu-overlay" onClick={onClose}>
      <FocusTrap focusTrapOptions={{ allowOutsideClick: true, escapeDeactivates: false }}>
        <div
          className="action-menu"
          role="dialog"
          aria-modal="true"
          aria-label="Action menu"
          onClick={(event) => event.stopPropagation()}
        >
          <div className="action-menu-search">
            <SearchIcon />
            <input
              ref={inputRef}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'ArrowDown') {
                  event.preventDefault();
                  setSelectedIndex((index) => Math.min(index + 1, filteredActions.length - 1));
                } else if (event.key === 'ArrowUp') {
                  event.preventDefault();
                  setSelectedIndex((index) => Math.max(index - 1, 0));
                } else if (event.key === 'Enter') {
                  event.preventDefault();
                  const selected = filteredActions[selectedIndex]?.action;
                  if (selected) runAction(selected);
                }
              }}
              placeholder="Type an action..."
              aria-label="Search actions"
            />
            <span className="action-menu-esc">esc</span>
          </div>
          <div className="action-menu-results" role="listbox">
            {filteredActions.map(({ action }, index) => (
              <button
                key={action.id}
                type="button"
                className={`action-menu-item${index === selectedIndex ? ' is-selected' : ''}`}
                role="option"
                aria-selected={index === selectedIndex}
                onMouseEnter={() => setSelectedIndex(index)}
                onClick={() => runAction(action)}
              >
                <span className="action-menu-icon">{action.icon}</span>
                <span className="action-menu-copy">
                  <strong>{action.title}</strong>
                  <span>{action.description}</span>
                </span>
                {action.shortcut && <KeyCombos combos={action.shortcut} />}
                <span className="action-menu-enter" aria-hidden="true">↵</span>
              </button>
            ))}
            {filteredActions.length === 0 && (
              <div className="action-menu-empty">No matching actions</div>
            )}
          </div>
          <div className="action-menu-footer">
            <span><kbd>↑</kbd><kbd>↓</kbd> navigate</span>
            <span><kbd>↵</kbd> open</span>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}

function SearchIcon() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <circle cx="8.5" cy="8.5" r="5.5" />
      <path d="m12.5 12.5 4 4" />
    </svg>
  );
}
