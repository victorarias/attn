// app/src/components/ShortcutsModal.tsx
// Keyboard shortcuts cheatsheet. Opened with Cmd+/ (ui.showShortcuts).

import { useMemo } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { buildCheatsheet } from '../shortcuts';
import { KeyCombos } from './Keycap';
import './ShortcutsModal.css';

interface ShortcutsModalProps {
  isOpen: boolean;
  onClose: () => void;
}

export function ShortcutsModal({ isOpen, onClose }: ShortcutsModalProps) {
  const categories = useMemo(() => buildCheatsheet(), []);
  useEscapeStack(onClose, isOpen);

  if (!isOpen) return null;

  return (
    <div className="shortcuts-overlay" onClick={onClose}>
      <FocusTrap
        focusTrapOptions={{
          allowOutsideClick: true,
          escapeDeactivates: false,
        }}
      >
        <div
          className="shortcuts-modal"
          onClick={(e) => e.stopPropagation()}
          role="dialog"
          aria-modal="true"
          aria-labelledby="shortcuts-modal-title"
        >
          <div className="shortcuts-header">
            <h2 id="shortcuts-modal-title">Keyboard Shortcuts</h2>
            <button
              className="shortcuts-close"
              onClick={onClose}
              aria-label="Close keyboard shortcuts"
              type="button"
            >
              ×
            </button>
          </div>

          <div className="shortcuts-body">
            {categories.map((category) => (
              <section className="shortcuts-category" key={category.title}>
                <h3 className="shortcuts-category-title">{category.title}</h3>
                <div className="shortcuts-rows">
                  {category.rows.map((row) => (
                    <div className="shortcuts-row" key={row.label}>
                      <span className="shortcuts-row-label">
                        {row.label}
                        {row.note && <span className="shortcuts-row-note">{row.note}</span>}
                      </span>
                      <KeyCombos combos={row.combos} />
                    </div>
                  ))}
                </div>
              </section>
            ))}
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}
