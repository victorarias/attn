// app/src/components/KeyCaptureInput.tsx
// Records a single key combo for the shortcut editor. While recording it
// suspends the global shortcut dispatcher and captures the next keystroke,
// translating it into a ShortcutDef. The parent owns "which row is recording"
// so only one capture is ever active.

import { useEffect, useRef, useState } from 'react';
import { ShortcutDef } from '../shortcuts/registry';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import { eventToBinding, isRiskyBinding } from '../shortcuts/resolver';
import { setShortcutCaptureSuspended } from '../shortcuts/useShortcut';
import { KeyCombo } from './Keycap';
import './KeyCaptureInput.css';

interface KeyCaptureInputProps {
  binding: ShortcutDef | null;
  recording: boolean;
  onStart: () => void;
  onCapture: (def: ShortcutDef) => void;
  onCancel: () => void;
}

export function KeyCaptureInput({
  binding,
  recording,
  onStart,
  onCapture,
  onCancel,
}: KeyCaptureInputProps) {
  const [error, setError] = useState<string | null>(null);
  const onCaptureRef = useRef(onCapture);
  onCaptureRef.current = onCapture;
  const onCancelRef = useRef(onCancel);
  onCancelRef.current = onCancel;

  useEffect(() => {
    if (!recording) {
      setError(null);
      return;
    }
    setShortcutCaptureSuspended(true);
    const handle = (e: KeyboardEvent) => {
      e.preventDefault();
      e.stopPropagation();
      if (e.key === 'Escape') {
        onCancelRef.current();
        return;
      }
      const result = eventToBinding(e);
      if (result.kind === 'ignored') return;
      if (result.kind === 'error') {
        setError(result.message);
        return;
      }
      onCaptureRef.current(result.def);
    };
    window.addEventListener('keydown', handle, true);
    return () => {
      window.removeEventListener('keydown', handle, true);
      setShortcutCaptureSuspended(false);
    };
  }, [recording]);

  if (recording) {
    return (
      <span className="key-capture key-capture--recording">
        <span className="key-capture-prompt">
          Press keys…
          <span className="key-capture-esc">Esc to cancel</span>
        </span>
        {error && <span className="key-capture-error">{error}</span>}
      </span>
    );
  }

  return (
    <button
      type="button"
      className={`key-capture-button${binding && isRiskyBinding(binding) ? ' key-capture-button--risky' : ''}`}
      onClick={onStart}
      title={binding && isRiskyBinding(binding)
        ? 'No ⌘/⌥ modifier — this may collide with typing in the terminal'
        : 'Click to rebind'}
    >
      {binding
        ? <KeyCombo tokens={shortcutTokens(binding)} />
        : <span className="key-capture-empty">Unassigned</span>}
    </button>
  );
}
