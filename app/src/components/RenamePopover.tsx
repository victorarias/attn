import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './RenamePopover.css';

interface RenamePopoverProps {
  /** Current name, shown pre-selected so typing replaces and ArrowRight appends. */
  initialValue: string;
  /** Accessible label / header, e.g. "Rename session". */
  label: string;
  /** Viewport-relative anchor; the popover opens just below it. */
  anchor: { top: number; left: number };
  onSubmit: (value: string) => Promise<void>;
  onClose: () => void;
}

const VIEWPORT_MARGIN = 8;

export function RenamePopover({ initialValue, label, anchor, onSubmit, onClose }: RenamePopoverProps) {
  const [value, setValue] = useState(initialValue);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [position, setPosition] = useState(anchor);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEscapeStack(onClose, true);

  // Select-all on mount: typing replaces the whole name, ArrowRight collapses to
  // the end to append, ArrowLeft to the start — all native input behavior.
  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    input.select();
  }, []);

  // Keep the popover on-screen by clamping against the viewport once measured.
  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    let top = anchor.top;
    let left = anchor.left;
    if (left + rect.width > window.innerWidth - VIEWPORT_MARGIN) {
      left = Math.max(VIEWPORT_MARGIN, window.innerWidth - rect.width - VIEWPORT_MARGIN);
    }
    if (top + rect.height > window.innerHeight - VIEWPORT_MARGIN) {
      top = Math.max(VIEWPORT_MARGIN, anchor.top - rect.height - 8);
    }
    setPosition({ top, left });
  }, [anchor]);

  // Dismiss on outside click. Deferred a tick so the click that opened the
  // popover doesn't immediately close it.
  useEffect(() => {
    const handleMouseDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        onClose();
      }
    };
    const id = window.setTimeout(() => document.addEventListener('mousedown', handleMouseDown), 0);
    return () => {
      window.clearTimeout(id);
      document.removeEventListener('mousedown', handleMouseDown);
    };
  }, [onClose]);

  const handleSubmit = async () => {
    const trimmed = value.trim();
    if (!trimmed) {
      setError('Name cannot be empty');
      return;
    }
    if (trimmed === initialValue.trim()) {
      onClose();
      return;
    }
    setIsSaving(true);
    setError(null);
    try {
      await onSubmit(trimmed);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Rename failed');
      setIsSaving(false);
    }
  };

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      void handleSubmit();
    }
    // Escape is handled by useEscapeStack (capture phase).
  };

  return (
    <div
      ref={containerRef}
      className="rename-popover"
      style={{ top: position.top, left: position.left }}
      role="dialog"
      aria-label={label}
      onKeyDown={handleKeyDown}
    >
      <input
        ref={inputRef}
        className="rename-popover-input"
        value={value}
        onChange={(event) => {
          setValue(event.target.value);
          if (error) setError(null);
        }}
        disabled={isSaving}
        spellCheck={false}
        aria-label={label}
      />
      {error ? <div className="rename-popover-error">{error}</div> : null}
      <div className="rename-popover-actions">
        <button
          type="button"
          className="rename-popover-btn cancel"
          onClick={onClose}
          disabled={isSaving}
        >
          Cancel
        </button>
        <button
          type="button"
          className="rename-popover-btn save"
          onClick={handleSubmit}
          disabled={isSaving || !value.trim()}
        >
          {isSaving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  );
}
