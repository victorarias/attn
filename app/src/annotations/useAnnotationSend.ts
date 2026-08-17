import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useShortcut } from '../shortcuts/useShortcut';
import type { ShortcutId } from '../shortcuts/registry';

export type AnnotationSendResult =
  | { kind: 'sent' }
  | { kind: 'skipped' }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string };

export type AnnotationSendOutcome<T extends AnnotationSendResult> =
  | { kind: 'sending' }
  | T
  | { kind: 'error'; message: string }
  | null;

interface UseAnnotationSendOptions<T extends AnnotationSendResult> {
  send: () => T | null | Promise<T | null>;
  shortcutId: ShortcutId;
  enabled: boolean;
  sentClearMs: number;
}

export function useAnnotationSend<T extends AnnotationSendResult>({
  send,
  shortcutId,
  enabled,
  sentClearMs,
}: UseAnnotationSendOptions<T>) {
  const sendRef = useRef(send);
  const sendingRef = useRef(false);
  const [outcome, setOutcome] = useState<AnnotationSendOutcome<T>>(null);

  useLayoutEffect(() => {
    sendRef.current = send;
  }, [send]);

  const runSend = useCallback((action: () => T | null | Promise<T | null>) => {
    if (sendingRef.current) {
      return;
    }
    sendingRef.current = true;

    let result: T | null | Promise<T | null>;
    try {
      result = action();
    } catch (error) {
      sendingRef.current = false;
      setOutcome({
        kind: 'error',
        message: error instanceof Error ? error.message : 'Send failed',
      });
      return;
    }

    if (result === null) {
      sendingRef.current = false;
      return;
    }
    if (!(result instanceof Promise)) {
      sendingRef.current = false;
      setOutcome(result);
      return;
    }

    setOutcome({ kind: 'sending' });
    void result
      .then((next) => {
        if (next !== null) {
          setOutcome(next);
        }
      })
      .catch((error: unknown) => {
        setOutcome({
          kind: 'error',
          message: error instanceof Error ? error.message : 'Send failed',
        });
      })
      .finally(() => {
        sendingRef.current = false;
      });
  }, []);

  const sendNow = useCallback(() => runSend(sendRef.current), [runSend]);
  const sendAlternative = useCallback(
    (alternative: () => T | null | Promise<T | null>) => runSend(alternative),
    [runSend],
  );

  useShortcut(shortcutId, sendNow, enabled);

  useEffect(() => {
    if (outcome?.kind !== 'sent') {
      return;
    }
    const timer = window.setTimeout(() => setOutcome(null), sentClearMs);
    return () => window.clearTimeout(timer);
  }, [outcome, sentClearMs]);

  const clearOutcome = useCallback(() => setOutcome(null), []);
  return { outcome, send: sendNow, sendAlternative, clearOutcome };
}
