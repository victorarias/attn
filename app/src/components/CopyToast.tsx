import { useState, useEffect, useCallback } from 'react';
import './CopyToast.css';

interface CopyToastProps {
  message: string | null;
  onDone: () => void;
}

export function CopyToast({ message, onDone }: CopyToastProps) {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (message) {
      setVisible(true);
      const timer = setTimeout(() => {
        setVisible(false);
        setTimeout(onDone, 200); // Wait for fade out
      }, 1500);
      return () => clearTimeout(timer);
    }
  }, [message, onDone]);

  if (!message) return null;

  return (
    <div
      className={`copy-toast ${visible ? 'visible' : ''}`}
      role="status"
      aria-live="polite"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
        <polyline points="20 6 9 17 4 12" />
      </svg>
      <span>{message}</span>
    </div>
  );
}

// Hook to manage toast state
export function useCopyToast() {
  const [message, setMessage] = useState<string | null>(null);

  const showToast = useCallback((msg: string) => {
    setMessage(msg);
  }, []);

  const clearToast = useCallback(() => {
    setMessage(null);
  }, []);

  return { message, showToast, clearToast };
}
