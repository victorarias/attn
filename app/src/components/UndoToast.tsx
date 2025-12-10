// app/src/components/UndoToast.tsx
import { useState, useEffect, useCallback } from 'react';
import { useMuteStore } from '../store/mutes';
import './UndoToast.css';

export function UndoToast() {
  const [visible, setVisible] = useState(false);
  const [message, setMessage] = useState('');
  const [countdown, setCountdown] = useState(5);
  const { undoStack, processUndo } = useMuteStore();

  // Watch for new mutes by tracking the latest item's timestamp
  const latestItem = undoStack[undoStack.length - 1];
  useEffect(() => {
    if (latestItem) {
      const itemType = latestItem.type === 'pr' ? 'PR' : 'Repository';
      setMessage(`${itemType} muted`);
      setVisible(true);
      setCountdown(5);
    }
  }, [latestItem?.timestamp]);

  // Countdown timer
  useEffect(() => {
    if (!visible) return;

    const timer = setInterval(() => {
      setCountdown(prev => {
        if (prev <= 1) {
          setVisible(false);
          return 5;
        }
        return prev - 1;
      });
    }, 1000);

    return () => clearInterval(timer);
  }, [visible]);

  const handleUndo = useCallback(() => {
    const undone = processUndo();
    if (undone) {
      setVisible(false);
    }
  }, [processUndo]);

  if (!visible) return null;

  return (
    <div className="undo-toast">
      <span className="toast-message">{message}</span>
      <button className="toast-undo-btn" onClick={handleUndo}>
        Undo ({countdown}s)
      </button>
    </div>
  );
}
