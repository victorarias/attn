import { useCallback, useEffect, useRef } from 'react';
import type { KeyboardEvent } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './CloseSessionPrompt.css';

interface CloseSessionPromptProps {
  isVisible: boolean;
  sessionLabel: string;
  splitCount: number;
  onConfirm: () => void;
  onCancel: () => void;
}

export function CloseSessionPrompt({
  isVisible,
  sessionLabel,
  splitCount,
  onConfirm,
  onCancel,
}: CloseSessionPromptProps) {
  const confirmRef = useRef<HTMLButtonElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEscapeStack(onCancel, isVisible);

  const handleKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    const buttons = [confirmRef.current, cancelRef.current].filter(Boolean) as HTMLButtonElement[];
    const key = event.key.toLowerCase();
    const active = document.activeElement as HTMLButtonElement | null;

    if (key === 'n') {
      event.preventDefault();
      onCancel();
      return;
    }

    if (key === 'y') {
      event.preventDefault();
      onConfirm();
      return;
    }

    if (event.key === 'Enter' || event.key === ' ' || event.key === 'Space' || event.key === 'Spacebar') {
      event.preventDefault();
      if (active === cancelRef.current) {
        onCancel();
        return;
      }
      onConfirm();
      return;
    }

    if (event.key === 'Tab') {
      if (buttons.length === 0) return;
      const currentIndex = Math.max(0, buttons.indexOf(active || buttons[0]));
      const delta = event.shiftKey ? -1 : 1;
      const nextIndex = (currentIndex + delta + buttons.length) % buttons.length;
      buttons[nextIndex]?.focus();
      event.preventDefault();
      return;
    }

    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') {
      return;
    }

    if (buttons.length === 0) return;
    const currentIndex = Math.max(0, buttons.indexOf(active || buttons[0]));
    const delta = event.key === 'ArrowRight' ? 1 : -1;
    const nextIndex = (currentIndex + delta + buttons.length) % buttons.length;
    buttons[nextIndex]?.focus();
    event.preventDefault();
  }, [onCancel, onConfirm]);

  useEffect(() => {
    if (!isVisible) return;
    const raf = requestAnimationFrame(() => confirmRef.current?.focus());
    return () => cancelAnimationFrame(raf);
  }, [isVisible]);

  if (!isVisible) {
    return null;
  }

  const displayName = sessionLabel.trim() || 'this session';
  const splitLabel = splitCount === 1 ? '1 split terminal' : `${splitCount} split terminals`;

  return (
    <div className="close-session-prompt" role="presentation" onClick={onCancel}>
      <div
        className="close-session-content"
        role="dialog"
        aria-modal="true"
        aria-labelledby="close-session-title"
        aria-describedby="close-session-message"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <div className="close-session-title" id="close-session-title">
          Close session?
        </div>
        <div className="close-session-message" id="close-session-message">
          <span className="close-session-label">{displayName}</span> still has{' '}
          <span className="close-session-count">{splitLabel}</span> open.
          <br />
          Close the session and all split terminals?
        </div>
        <div className="close-session-hint">
          Enter / Space uses the focused button, Y confirms, N / Esc cancel
        </div>
        <div className="close-session-actions">
          <button ref={confirmRef} className="close-session-btn confirm" onClick={onConfirm}>
            Close Session
          </button>
          <button ref={cancelRef} className="close-session-btn cancel" onClick={onCancel}>
            Keep Session
          </button>
        </div>
      </div>
    </div>
  );
}
