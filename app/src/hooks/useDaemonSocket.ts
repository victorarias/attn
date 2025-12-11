import { useEffect, useRef, useCallback, useState } from 'react';

export interface DaemonSession {
  id: string;
  label: string;
  directory: string;
  state: 'working' | 'waiting_input' | 'idle';
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

export interface RepoState {
  repo: string;
  muted: boolean;
  collapsed: boolean;
}

interface WebSocketEvent {
  event: string;
  protocol_version?: string;
  session?: DaemonSession;
  sessions?: DaemonSession[];
  prs?: DaemonPR[];
  repos?: RepoState[];
  // PR action result fields
  action?: string;
  repo?: string;
  number?: number;
  success?: boolean;
  error?: string;
}

// Protocol version - must match daemon's ProtocolVersion
// Increment when making breaking changes to the protocol
const PROTOCOL_VERSION = '2';

interface PRActionResult {
  success: boolean;
  error?: string;
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onPRsUpdate: (prs: DaemonPR[]) => void;
  onReposUpdate: (repos: RepoState[]) => void;
  wsUrl?: string;
}

// Default WebSocket port, can be overridden via VITE_DAEMON_PORT env var
const DEFAULT_WS_URL = `ws://127.0.0.1:${import.meta.env.VITE_DAEMON_PORT || '9849'}/ws`;

export function useDaemonSocket({
  onSessionsUpdate,
  onPRsUpdate,
  onReposUpdate,
  wsUrl = DEFAULT_WS_URL,
}: UseDaemonSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const sessionsRef = useRef<DaemonSession[]>([]);
  const prsRef = useRef<DaemonPR[]>([]);
  const reposRef = useRef<RepoState[]>([]);
  const reconnectTimeoutRef = useRef<number | null>(null);
  const pendingActionsRef = useRef<Map<string, (result: PRActionResult) => void>>(new Map());
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [hasReceivedInitialState, setHasReceivedInitialState] = useState(false);

  const connect = useCallback(() => {
    if (wsRef.current?.readyState === WebSocket.OPEN) return;

    const ws = new WebSocket(wsUrl);

    ws.onopen = () => {
      console.log('[Daemon] WebSocket connected');
      setConnectionError(null);
    };

    ws.onmessage = (event) => {
      try {
        const data: WebSocketEvent = JSON.parse(event.data);

        switch (data.event) {
          case 'initial_state':
            // Check protocol version on initial connection
            if (data.protocol_version && data.protocol_version !== PROTOCOL_VERSION) {
              console.error(`[Daemon] Protocol version mismatch: daemon=${data.protocol_version}, client=${PROTOCOL_VERSION}`);
              setConnectionError(`Version mismatch: Please run 'make install' (daemon v${data.protocol_version}, app v${PROTOCOL_VERSION})`);
              ws.close();
              return;
            }
            if (data.sessions) {
              sessionsRef.current = data.sessions;
              onSessionsUpdate(data.sessions);
            }
            if (data.prs) {
              prsRef.current = data.prs;
              onPRsUpdate(data.prs);
            }
            if (data.repos) {
              reposRef.current = data.repos;
              onReposUpdate(data.repos);
            }
            setHasReceivedInitialState(true);
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

          case 'sessions_updated':
            if (data.sessions) {
              sessionsRef.current = data.sessions;
              onSessionsUpdate(data.sessions);
            }
            break;

          case 'prs_updated':
            if (data.prs) {
              prsRef.current = data.prs;
              onPRsUpdate(data.prs);
            }
            break;

          case 'repos_updated':
            if (data.repos) {
              reposRef.current = data.repos;
              onReposUpdate(data.repos);
            }
            break;

          case 'pr_action_result':
            if (data.action && data.repo && data.number !== undefined) {
              const key = `${data.repo}#${data.number}:${data.action}`;
              const resolve = pendingActionsRef.current.get(key);
              if (resolve) {
                resolve({ success: data.success ?? false, error: data.error });
                pendingActionsRef.current.delete(key);
              }
            }
            break;

          case 'refresh_prs_result': {
            const resolve = pendingActionsRef.current.get('refresh_prs');
            if (resolve) {
              resolve({ success: data.success ?? false, error: data.error });
              pendingActionsRef.current.delete('refresh_prs');
            }
            break;
          }
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
  }, [wsUrl, onSessionsUpdate, onPRsUpdate, onReposUpdate]);

  useEffect(() => {
    connect();

    return () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      wsRef.current?.close();
    };
  }, [connect]);

  const sendPRAction = useCallback((
    action: 'approve' | 'merge',
    repo: string,
    number: number,
    method?: string
  ): Promise<PRActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `${repo}#${number}:${action}`;
      pendingActionsRef.current.set(key, resolve);

      const msg = {
        cmd: `${action}_pr`,
        repo,
        number,
        ...(method && { method }),
      };
      console.log('[Daemon] Sending PR action:', msg);
      ws.send(JSON.stringify(msg));

      // Timeout after 30 seconds
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Timeout'));
        }
      }, 30000);
    });
  }, []);

  // Mute a PR with optimistic update
  const sendMutePR = useCallback((prId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Optimistic update - toggle PR muted state immediately
    const updatedPRs = prsRef.current.map(pr =>
      pr.id === prId ? { ...pr, muted: !pr.muted } : pr
    );
    prsRef.current = updatedPRs;
    onPRsUpdate(updatedPRs);

    ws.send(JSON.stringify({ cmd: 'mute_pr', id: prId }));
  }, [onPRsUpdate]);

  // Mute a repo with optimistic update
  const sendMuteRepo = useCallback((repo: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Optimistic update - toggle repo muted state immediately
    const existingRepo = reposRef.current.find(r => r.repo === repo);
    let updatedRepos: RepoState[];
    if (existingRepo) {
      updatedRepos = reposRef.current.map(r =>
        r.repo === repo ? { ...r, muted: !r.muted } : r
      );
    } else {
      // Repo doesn't exist in state yet, add it as muted
      updatedRepos = [...reposRef.current, { repo, muted: true, collapsed: false }];
    }
    reposRef.current = updatedRepos;
    onReposUpdate(updatedRepos);

    ws.send(JSON.stringify({ cmd: 'mute_repo', repo }));
  }, [onReposUpdate]);

  // Request daemon to refresh PRs from GitHub
  const sendRefreshPRs = useCallback((): Promise<PRActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'refresh_prs';
      pendingActionsRef.current.set(key, resolve);

      ws.send(JSON.stringify({ cmd: 'refresh_prs' }));

      // Timeout after 30 seconds
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Refresh timed out'));
        }
      }, 30000);
    });
  }, []);

  // Clear all sessions from daemon
  const sendClearSessions = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({ cmd: 'clear_sessions' }));
  }, []);

  return {
    isConnected: wsRef.current?.readyState === WebSocket.OPEN,
    connectionError,
    hasReceivedInitialState,
    sendPRAction,
    sendMutePR,
    sendMuteRepo,
    sendRefreshPRs,
    sendClearSessions,
  };
}
