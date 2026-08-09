import { useEffect, useRef, useState } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './SessionContextCapPrompt.css';

// The action-menu route to a per-session context-window cap: one number input,
// Enter saves, blank (or 0) clears the pin so the session falls back to the
// chief / per-agent default settings. Saving a change reloads the agent in
// place (the cap only applies at launch), which the copy says out loud.
interface SessionContextCapPromptProps {
  sessionLabel: string;
  /** Current pin in tokens; undefined when the session has none. */
  currentCap?: number;
  onSubmit: (cap: number) => Promise<void>;
  onClose: () => void;
}

export function SessionContextCapPrompt({
  sessionLabel,
  currentCap,
  onSubmit,
  onClose,
}: SessionContextCapPromptProps) {
  const [value, setValue] = useState(currentCap ? String(currentCap) : '');
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEscapeStack(onClose, !isSaving);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    input.select();
  }, []);

  const handleSubmit = async () => {
    const trimmed = value.trim();
    const cap = trimmed === '' ? 0 : Number(trimmed);
    if (!Number.isInteger(cap) || cap < 0) {
      setError('Enter a whole number of tokens, or leave blank for no cap');
      return;
    }
    if (cap === (currentCap ?? 0)) {
      onClose();
      return;
    }
    setIsSaving(true);
    setError(null);
    try {
      await onSubmit(cap);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Setting the cap failed');
      setIsSaving(false);
    }
  };

  return (
    <div
      className="context-cap-prompt"
      data-testid="context-cap-prompt"
      role="presentation"
      onClick={isSaving ? undefined : onClose}
    >
      <div
        className="context-cap-content"
        role="dialog"
        aria-modal="true"
        aria-labelledby="context-cap-title"
        aria-describedby="context-cap-message"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="context-cap-title" id="context-cap-title">
          Context window cap
        </div>
        <div className="context-cap-message" id="context-cap-message">
          Cap <span>{sessionLabel}</span>&rsquo;s context window so it compacts at this
          many tokens. Blank means no cap. Saving reloads the agent to apply it:
          the conversation is resumed, but anything mid-turn is lost.
        </div>
        <input
          ref={inputRef}
          className="context-cap-input"
          data-testid="context-cap-input"
          type="number"
          min={0}
          step={1000}
          inputMode="numeric"
          placeholder="e.g. 800000 — blank for no cap"
          value={value}
          onChange={(event) => {
            setValue(event.target.value);
            if (error) setError(null);
          }}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              void handleSubmit();
            }
          }}
          disabled={isSaving}
          aria-label="Context window cap in tokens"
        />
        {error ? <div className="context-cap-error" data-testid="context-cap-error">{error}</div> : null}
        <div className="context-cap-actions">
          <button
            type="button"
            className="cancel"
            data-testid="context-cap-cancel"
            onClick={onClose}
            disabled={isSaving}
          >
            Cancel
          </button>
          <button
            type="button"
            className="confirm"
            data-testid="context-cap-save"
            onClick={handleSubmit}
            disabled={isSaving}
          >
            {isSaving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  );
}
