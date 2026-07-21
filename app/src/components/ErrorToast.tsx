import { useState, useEffect, useCallback } from 'react';
import './ErrorToast.css';

interface ErrorToastProps {
  message: string | null;
  durationMs?: number;
  onDone: () => void;
}

export function ErrorToast({ message, durationMs = 3000, onDone }: ErrorToastProps) {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (message) {
      setVisible(true);
      const timer = setTimeout(() => {
        setVisible(false);
        setTimeout(onDone, 200); // Wait for fade out
      }, durationMs);
      return () => clearTimeout(timer);
    }
  }, [durationMs, message, onDone]);

  if (!message) return null;

  return (
    <div
      className={`error-toast ${visible ? 'visible' : ''}`}
      role="alert"
      aria-live="assertive"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
        <circle cx="12" cy="12" r="10" />
        <line x1="15" y1="9" x2="9" y2="15" />
        <line x1="9" y1="9" x2="15" y2="15" />
      </svg>
      <span>{message}</span>
    </div>
  );
}

// Hook to manage toast state
export function useErrorToast() {
  const [toast, setToast] = useState<{ message: string; durationMs: number } | null>(null);

  const showError = useCallback((message: string, options?: { durationMs?: number }) => {
    setToast({ message, durationMs: options?.durationMs ?? 3000 });
  }, []);

  const clearError = useCallback(() => {
    setToast(null);
  }, []);

  return { message: toast?.message ?? null, durationMs: toast?.durationMs ?? 3000, showError, clearError };
}
