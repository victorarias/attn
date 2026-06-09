import { useEffect, useRef } from 'react';
import { useEscapeStack } from '../hooks/useEscapeStack';
import './ChiefOfStaffTransferPrompt.css';

interface ChiefOfStaffTransferPromptProps {
  isVisible: boolean;
  currentLabel: string;
  targetLabel: string;
  isSaving: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ChiefOfStaffTransferPrompt({
  isVisible,
  currentLabel,
  targetLabel,
  isSaving,
  onConfirm,
  onCancel,
}: ChiefOfStaffTransferPromptProps) {
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEscapeStack(onCancel, isVisible && !isSaving);

  useEffect(() => {
    if (!isVisible) return;
    const raf = requestAnimationFrame(() => confirmRef.current?.focus());
    return () => cancelAnimationFrame(raf);
  }, [isVisible]);

  if (!isVisible) {
    return null;
  }

  return (
    <div
      className="chief-transfer-prompt"
      data-testid="chief-transfer-prompt"
      role="presentation"
      onClick={isSaving ? undefined : onCancel}
    >
      <div
        className="chief-transfer-content"
        role="dialog"
        aria-modal="true"
        aria-labelledby="chief-transfer-title"
        aria-describedby="chief-transfer-message"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="chief-transfer-title" id="chief-transfer-title">
          Transfer chief of staff role?
        </div>
        <div className="chief-transfer-message" id="chief-transfer-message">
          <span>{currentLabel}</span> currently holds the role. Move it to <span>{targetLabel}</span>?
          <br />
          Both sessions will keep running.
        </div>
        <div className="chief-transfer-actions">
          <button
            type="button"
            className="cancel"
            data-testid="chief-transfer-cancel"
            onClick={onCancel}
            disabled={isSaving}
          >
            Cancel
          </button>
          <button
            ref={confirmRef}
            type="button"
            className="confirm"
            data-testid="chief-transfer-confirm"
            onClick={onConfirm}
            disabled={isSaving}
          >
            {isSaving ? 'Transferring…' : 'Transfer role'}
          </button>
        </div>
      </div>
    </div>
  );
}
