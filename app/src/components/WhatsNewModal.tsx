// app/src/components/WhatsNewModal.tsx
// One-time announcement of the workspace UX. Shown once per release via
// useWhatsNew(); "View all shortcuts" hands off to the cheatsheet.

import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { shortcutTokens } from '../shortcuts';
import { KeyCombos } from './Keycap';
import './WhatsNewModal.css';

interface WhatsNewModalProps {
  isOpen: boolean;
  onClose: () => void;
  onViewShortcuts: () => void;
}

interface Highlight {
  title: string;
  body: string;
  combos: string[][];
  // The biggest behavior change — rendered as a callout so it's hard to miss.
  flagged?: boolean;
}

const HIGHLIGHTS: Highlight[] = [
  {
    flagged: true,
    title: '⌘N opens a session inside this workspace',
    body: 'This is the big change. ⌘N used to open a separate session with its own row in the sidebar. Now it adds a session to the workspace you’re already in. Want a new sidebar row instead? Press ⌘T to start a new workspace.',
    combos: [shortcutTokens('session.new'), shortcutTokens('session.newWorkspace')],
  },
  {
    title: 'The sidebar lists workspaces',
    body: 'Each row in the sidebar is now a workspace — a group of related sessions and terminals — instead of a single session.',
    combos: [],
  },
  {
    title: 'Several sessions in one workspace',
    body: 'A workspace can hold more than one session or terminal at once. Add another next to the current one, or split it sideways.',
    combos: [shortcutTokens('session.newHorizontal')],
  },
  {
    title: 'Shells live here too',
    body: 'Open a plain terminal the same way you open an agent — pick it from the new-session dialog and it sits in the workspace like anything else.',
    combos: [shortcutTokens('session.new')],
  },
  {
    title: 'Move between panes',
    body: 'Use the arrow keys to move focus around the panes. Keep going past an edge and you land in the next workspace.',
    combos: [['⌘', '⌥', '←↑→↓']],
  },
];

export function WhatsNewModal({ isOpen, onClose, onViewShortcuts }: WhatsNewModalProps) {
  useEscapeStack(onClose, isOpen);

  if (!isOpen) return null;

  return (
    <div className="whats-new-overlay" onClick={onClose}>
      <FocusTrap
        focusTrapOptions={{
          allowOutsideClick: true,
          escapeDeactivates: false,
        }}
      >
        <div
          className="whats-new-modal"
          onClick={(e) => e.stopPropagation()}
          role="dialog"
          aria-modal="true"
          aria-labelledby="whats-new-title"
        >
          <div className="whats-new-header">
            <div className="whats-new-eyebrow">What's new</div>
            <h2 id="whats-new-title">attn is organized around workspaces</h2>
            <button
              className="whats-new-close"
              onClick={onClose}
              aria-label="Close what's new"
              type="button"
            >
              ×
            </button>
          </div>

          <div className="whats-new-body">
            {HIGHLIGHTS.map((highlight) => (
              <section
                className={`whats-new-item${highlight.flagged ? ' whats-new-item--key' : ''}`}
                key={highlight.title}
              >
                <div className="whats-new-item-head">
                  <h3>
                    {highlight.flagged && <span className="whats-new-tag">Changed</span>}
                    {highlight.title}
                  </h3>
                  {highlight.combos.length > 0 && (
                    <span className="whats-new-keys">
                      <KeyCombos combos={highlight.combos} />
                    </span>
                  )}
                </div>
                <p>{highlight.body}</p>
              </section>
            ))}
          </div>

          <div className="whats-new-footer">
            <button
              className="whats-new-link"
              onClick={onViewShortcuts}
              type="button"
            >
              View all shortcuts →
            </button>
            <button
              className="whats-new-primary"
              onClick={onClose}
              type="button"
            >
              Got it
            </button>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}
