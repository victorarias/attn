// app/src/hooks/usePRActions.ts
import { useState, useCallback, useEffect, useRef } from 'react';

interface PRActionState {
  loading: boolean;
  success: boolean;
  error: string | null;
}

type ActionStates = Map<string, PRActionState>;

interface UsePRActionsResult {
  approve: (repo: string, number: number) => Promise<void>;
  merge: (repo: string, number: number, method?: string) => Promise<void>;
  getActionState: (repo: string, number: number, action: string) => PRActionState | undefined;
}

export function usePRActions(wsUrl = 'ws://127.0.0.1:9849/ws'): UsePRActionsResult {
  const [actionStates, setActionStates] = useState<ActionStates>(new Map());
  const wsRef = useRef<WebSocket | null>(null);
  const pendingActions = useRef<Map<string, (result: { success: boolean; error?: string }) => void>>(new Map());

  // Connect to WebSocket
  useEffect(() => {
    const ws = new WebSocket(wsUrl);

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.event === 'pr_action_result') {
          const key = `${data.repo}#${data.number}:${data.action}`;

          // Update state
          setActionStates(prev => {
            const next = new Map(prev);
            next.set(key, {
              loading: false,
              success: data.success,
              error: data.error || null,
            });
            return next;
          });

          // Resolve pending promise
          const resolve = pendingActions.current.get(key);
          if (resolve) {
            resolve({ success: data.success, error: data.error });
            pendingActions.current.delete(key);
          }

          // Clear success state after 2 seconds
          if (data.success) {
            setTimeout(() => {
              setActionStates(prev => {
                const next = new Map(prev);
                next.delete(key);
                return next;
              });
            }, 2000);
          }
        }
      } catch (e) {
        console.error('[PRActions] Parse error:', e);
      }
    };

    wsRef.current = ws;
    return () => ws.close();
  }, [wsUrl]);

  const sendAction = useCallback(async (
    action: string,
    repo: string,
    number: number,
    extra?: object
  ): Promise<void> => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      throw new Error('Not connected');
    }

    const key = `${repo}#${number}:${action}`;

    // Set loading state
    setActionStates(prev => {
      const next = new Map(prev);
      next.set(key, { loading: true, success: false, error: null });
      return next;
    });

    // Create promise for result
    return new Promise((resolve, reject) => {
      pendingActions.current.set(key, (result) => {
        if (result.success) {
          resolve();
        } else {
          reject(new Error(result.error || 'Action failed'));
        }
      });

      ws.send(JSON.stringify({
        cmd: `${action}_pr`,
        repo,
        number,
        ...extra,
      }));

      // Timeout after 30 seconds
      setTimeout(() => {
        if (pendingActions.current.has(key)) {
          pendingActions.current.delete(key);
          setActionStates(prev => {
            const next = new Map(prev);
            next.set(key, { loading: false, success: false, error: 'Timeout' });
            return next;
          });
          reject(new Error('Timeout'));
        }
      }, 30000);
    });
  }, []);

  const approve = useCallback((repo: string, number: number) => {
    return sendAction('approve', repo, number);
  }, [sendAction]);

  const merge = useCallback((repo: string, number: number, method = 'squash') => {
    return sendAction('merge', repo, number, { method });
  }, [sendAction]);

  const getActionState = useCallback((repo: string, number: number, action: string) => {
    return actionStates.get(`${repo}#${number}:${action}`);
  }, [actionStates]);

  return { approve, merge, getActionState };
}
