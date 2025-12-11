// app/src/components/UndoToast.tsx
import { useState, useEffect, useCallback, useRef } from 'react';
import { useDaemonContext } from '../contexts/DaemonContext';
import './UndoToast.css';

export function UndoToast() {
  const [visible, setVisible] = useState(false);
  const [message, setMessage] = useState('');
  const [countdown, setCountdown] = useState(5);
  const { lastMuted, clearLastMuted, sendMutePR, sendMuteRepo } = useDaemonContext();
  const lastTimestampRef = useRef<number | null>(null);

  // Watch for new mutes by tracking the lastMuted timestamp
  useEffect(() => {
    if (lastMuted && lastMuted.timestamp !== lastTimestampRef.current) {
      lastTimestampRef.current = lastMuted.timestamp;
      const itemType = lastMuted.type === 'pr' ? 'PR' : 'Repository';
      setMessage(`${itemType} muted`);
      setVisible(true);
      setCountdown(5);
    }
  }, [lastMuted]);

  // Countdown timer
  useEffect(() => {
    if (!visible) return;

    const timer = setInterval(() => {
      setCountdown(prev => {
        if (prev <= 1) {
          setVisible(false);
          clearLastMuted();
          return 5;
        }
        return prev - 1;
      });
    }, 1000);

    return () => clearInterval(timer);
  }, [visible, clearLastMuted]);

  const handleUndo = useCallback(() => {
    if (lastMuted) {
      // Toggle the mute back (unmute)
      if (lastMuted.type === 'pr') {
        sendMutePR(lastMuted.id);
      } else {
        sendMuteRepo(lastMuted.id);
      }
      clearLastMuted();
      setVisible(false);
    }
  }, [lastMuted, sendMutePR, sendMuteRepo, clearLastMuted]);

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
