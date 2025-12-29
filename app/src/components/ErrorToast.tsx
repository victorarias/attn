import { useState, useEffect, useCallback } from 'react';
import './ErrorToast.css';

interface ErrorToastProps {
  message: string | null;
  onDone: () => void;
}

export function ErrorToast({ message, onDone }: ErrorToastProps) {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (message) {
      setVisible(true);
      const timer = setTimeout(() => {
        setVisible(false);
        setTimeout(onDone, 200); // Wait for fade out
      }, 3000); // Show error for 3 seconds (longer than copy toast)
      return () => clearTimeout(timer);
    }
  }, [message, onDone]);

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
  const [message, setMessage] = useState<string | null>(null);

  const showError = useCallback((msg: string) => {
    setMessage(msg);
  }, []);

  const clearError = useCallback(() => {
    setMessage(null);
  }, []);

  return { message, showError, clearError };
}
