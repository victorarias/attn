// The shape both list panels in Settings repeat: one thing at a time, a busy
// key naming which row is mid-flight, and one error line for whatever came
// back. Written out per action that was four setState calls around every await.

import { useCallback, useState } from 'react';

export interface PanelAction {
  error: string | null;
  /** Refuse before doing anything — a required field left blank. */
  fail: (message: string) => void;
  clearError: () => void;
  /** Which row is mid-flight: an endpoint or plugin id, or null. */
  busyKey: string | null;
  busy: boolean;
  /**
   * Run one action, holding the panel busy under `key` until it settles. The
   * callback owns what success means, so it can clear a form or re-list before
   * the busy mark comes down.
   */
  run: (key: string, fallbackMessage: string, action: () => Promise<void>) => Promise<void>;
}

export function usePanelAction(): PanelAction {
  const [error, setError] = useState<string | null>(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);

  const fail = useCallback((message: string) => setError(message), []);
  const clearError = useCallback(() => setError(null), []);

  const run = useCallback(async (
    key: string,
    fallbackMessage: string,
    action: () => Promise<void>,
  ) => {
    setError(null);
    setBusyKey(key);
    try {
      await action();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : fallbackMessage);
    } finally {
      setBusyKey(null);
    }
  }, []);

  return { error, fail, clearError, busyKey, busy: busyKey !== null, run };
}
