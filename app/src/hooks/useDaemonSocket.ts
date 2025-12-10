import { useEffect, useRef, useCallback } from 'react';

export interface DaemonSession {
  id: string;
  label: string;
  directory: string;
  tmux_target: string;
  state: 'working' | 'waiting';
  state_since: string;
  todos: string[] | null;
  last_seen: string;
  muted: boolean;
}

export interface DaemonPR {
  id: string;
  repo: string;
  number: number;
  title: string;
  url: string;
  role: 'author' | 'reviewer';
  state: 'working' | 'waiting';
  reason: string;
  last_updated: string;
  muted: boolean;
}

interface WebSocketEvent {
  event: string;
  session?: DaemonSession;
  sessions?: DaemonSession[];
  prs?: DaemonPR[];
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onPRsUpdate: (prs: DaemonPR[]) => void;
  wsUrl?: string;
}

export function useDaemonSocket({
  onSessionsUpdate,
  onPRsUpdate,
  wsUrl = 'ws://127.0.0.1:9849/ws',
}: UseDaemonSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const sessionsRef = useRef<DaemonSession[]>([]);
  const reconnectTimeoutRef = useRef<number | null>(null);

  const connect = useCallback(() => {
    if (wsRef.current?.readyState === WebSocket.OPEN) return;

    const ws = new WebSocket(wsUrl);

    ws.onopen = () => {
      console.log('[Daemon] WebSocket connected');
    };

    ws.onmessage = (event) => {
      try {
        const data: WebSocketEvent = JSON.parse(event.data);

        switch (data.event) {
          case 'initial_state':
            if (data.sessions) {
              sessionsRef.current = data.sessions;
              onSessionsUpdate(data.sessions);
            }
            if (data.prs) {
              onPRsUpdate(data.prs);
            }
            break;

          case 'session_registered':
            if (data.session) {
              sessionsRef.current = [...sessionsRef.current, data.session];
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'session_unregistered':
            if (data.session) {
              sessionsRef.current = sessionsRef.current.filter(
                (s) => s.id !== data.session!.id
              );
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'session_state_changed':
          case 'session_todos_updated':
            if (data.session) {
              sessionsRef.current = sessionsRef.current.map((s) =>
                s.id === data.session!.id ? data.session! : s
              );
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'prs_updated':
            if (data.prs) {
              onPRsUpdate(data.prs);
            }
            break;
        }
      } catch (err) {
        console.error('[Daemon] Parse error:', err);
      }
    };

    ws.onclose = () => {
      console.log('[Daemon] WebSocket disconnected, reconnecting in 3s...');
      wsRef.current = null;
      reconnectTimeoutRef.current = window.setTimeout(connect, 3000);
    };

    ws.onerror = (err) => {
      console.error('[Daemon] WebSocket error:', err);
      ws.close();
    };

    wsRef.current = ws;
  }, [wsUrl, onSessionsUpdate, onPRsUpdate]);

  useEffect(() => {
    connect();

    return () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      wsRef.current?.close();
    };
  }, [connect]);

  return {
    isConnected: wsRef.current?.readyState === WebSocket.OPEN,
  };
}
