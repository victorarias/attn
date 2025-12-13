import { useEffect, useRef, useCallback, useState } from 'react';
import type {
  Session as GeneratedSession,
  PR as GeneratedPR,
  Worktree as GeneratedWorktree,
  RepoState as GeneratedRepoState,
  WebSocketEvent as GeneratedWebSocketEvent,
  SessionState,
  PRRole,
  HeatState,
} from '../types/generated';

// Re-export types from generated for consumers
// Use type aliases to maintain backward compatibility
export type DaemonSession = GeneratedSession;
export type DaemonPR = GeneratedPR;
export type DaemonWorktree = GeneratedWorktree;
export type RepoState = GeneratedRepoState;
export type DaemonSettings = Record<string, string>;

// Re-export enums and useful types
export { SessionState, PRRole, HeatState };

// Extended WebSocketEvent with action result fields (generated allows extra properties)
type WebSocketEvent = GeneratedWebSocketEvent & {
  // PR action result fields
  action?: string;
  repo?: string;
  number?: number;
  success?: boolean;
  error?: string;
  // Worktree action result fields
  path?: string;
};

export interface RateLimitState {
  resource: string;
  resetAt: Date;
}

// Protocol version - must match daemon's ProtocolVersion
// Increment when making breaking changes to the protocol
const PROTOCOL_VERSION = '6';

interface PRActionResult {
  success: boolean;
  error?: string;
}

interface WorktreeActionResult {
  success: boolean;
  path?: string;
  error?: string;
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onPRsUpdate: (prs: DaemonPR[]) => void;
  onReposUpdate: (repos: RepoState[]) => void;
  onWorktreesUpdate?: (worktrees: DaemonWorktree[]) => void;
  onSettingsUpdate?: (settings: DaemonSettings) => void;
  wsUrl?: string;
}

// Default WebSocket port, can be overridden via VITE_DAEMON_PORT env var
const DEFAULT_WS_URL = `ws://127.0.0.1:${import.meta.env.VITE_DAEMON_PORT || '9849'}/ws`;

// ============================================================================
// Async Pattern Guide
// ============================================================================
//
// Operations use two patterns depending on failure impact:
//
// 1. PROMISE-BASED (for mutations that can fail):
//    - sendPRAction (approve/merge): Can fail due to conflicts, permissions
//    - sendRefreshPRs: Can fail due to rate limits, network
//    - sendCreateWorktree: Can fail due to existing branch, disk space
//    - sendDeleteWorktree: Can fail due to uncommitted changes
//    Pattern: Return Promise, use pendingActionsRef, show loading state
//
// 2. OPTIMISTIC FIRE-AND-FORGET (for toggles that rarely fail):
//    - sendMutePR: Toggle mute state
//    - sendMuteRepo: Toggle repo mute state
//    - sendPRVisited: Clear notification flag
//    - sendSetSetting: Update user preference
//    - sendClearSessions: Dev/admin action
//    - sendUnregisterSession: Session cleanup
//    - sendListWorktrees: Query operation
//    Pattern: Update UI immediately, assume success
//
// Why optimistic? These operations are simple state toggles that only fail
// if the daemon is down (which triggers reconnect anyway). Adding Promise
// handling would add complexity without improving UX.
//
// To convert a fire-and-forget to Promise-based:
// 1. Add a result event in daemon (e.g., "mute_pr_result")
// 2. Add event handling in onmessage switch
// 3. Return Promise and store in pendingActionsRef
// 4. Update caller to handle loading/error states
// ============================================================================

export function useDaemonSocket({
  onSessionsUpdate,
  onPRsUpdate,
  onReposUpdate,
  onWorktreesUpdate,
  onSettingsUpdate,
  wsUrl = DEFAULT_WS_URL,
}: UseDaemonSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const sessionsRef = useRef<DaemonSession[]>([]);
  const prsRef = useRef<DaemonPR[]>([]);
  const reposRef = useRef<RepoState[]>([]);
  const settingsRef = useRef<DaemonSettings>({});
  const reconnectTimeoutRef = useRef<number | null>(null);
  const reconnectDelayRef = useRef<number>(1000); // Start with 1s, exponential backoff
  const pendingActionsRef = useRef<Map<string, { resolve: (result: any) => void; reject: (error: Error) => void }>>(new Map());
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [hasReceivedInitialState, setHasReceivedInitialState] = useState(false);
  const [rateLimit, setRateLimit] = useState<RateLimitState | null>(null);

  const connect = useCallback(() => {
    if (wsRef.current?.readyState === WebSocket.OPEN) return;

    const ws = new WebSocket(wsUrl);

    ws.onopen = () => {
      console.log('[Daemon] WebSocket connected');
      setConnectionError(null);
      reconnectDelayRef.current = 1000; // Reset to 1s on successful connect
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
            if (data.settings) {
              settingsRef.current = data.settings;
              onSettingsUpdate?.(data.settings);
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

          case 'settings_updated':
            if (data.settings) {
              settingsRef.current = data.settings;
              onSettingsUpdate?.(data.settings);
            }
            break;

          case 'pr_action_result':
            if (data.action && data.repo && data.number !== undefined) {
              const key = `${data.repo}#${data.number}:${data.action}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve({ success: true });
                } else {
                  pending.reject(new Error(data.error || 'PR action failed'));
                }
              }
            }
            break;

          case 'refresh_prs_result': {
            const pending = pendingActionsRef.current.get('refresh_prs');
            if (pending) {
              pendingActionsRef.current.delete('refresh_prs');
              if (data.success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error(data.error || 'Refresh failed'));
              }
            }
            break;
          }

          case 'worktrees_updated':
            if (data.worktrees) {
              onWorktreesUpdate?.(data.worktrees);
            }
            break;

          case 'worktree_created':
          case 'worktree_deleted':
            // These events are informational, UI updates handled via worktrees_updated
            break;

          case 'create_worktree_result':
          case 'delete_worktree_result':
            // Handle async result for pending worktree actions
            const actionKey = `worktree_${data.event}`;
            const pendingAction = pendingActionsRef.current.get(actionKey);
            if (pendingAction) {
              pendingActionsRef.current.delete(actionKey);
              if (data.success) {
                pendingAction.resolve({ success: true, path: data.path });
              } else {
                pendingAction.reject(new Error(data.error || 'Worktree action failed'));
              }
            }
            break;

          case 'rate_limited':
            if (data.rate_limit_resource && data.rate_limit_reset_at) {
              const resetAt = new Date(data.rate_limit_reset_at);
              // Only set if reset is in the future
              if (resetAt > new Date()) {
                setRateLimit({
                  resource: data.rate_limit_resource,
                  resetAt,
                });
                // Auto-clear when reset time passes
                const msUntilReset = resetAt.getTime() - Date.now();
                setTimeout(() => {
                  setRateLimit(null);
                }, msUntilReset + 1000); // Add 1s buffer
              }
            }
            break;
        }
      } catch (err) {
        console.error('[Daemon] Parse error:', err);
      }
    };

    ws.onclose = () => {
      const delay = reconnectDelayRef.current;
      console.log(`[Daemon] WebSocket disconnected, reconnecting in ${delay}ms...`);
      wsRef.current = null;
      reconnectTimeoutRef.current = window.setTimeout(connect, delay);
      // Exponential backoff: 1s -> 1.5s -> 2.25s -> ... -> max 5s
      reconnectDelayRef.current = Math.min(delay * 1.5, 5000);
    };

    ws.onerror = (err) => {
      console.error('[Daemon] WebSocket error:', err);
      ws.close();
    };

    wsRef.current = ws;
  }, [wsUrl, onSessionsUpdate, onPRsUpdate, onReposUpdate, onWorktreesUpdate, onSettingsUpdate]);

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
      pendingActionsRef.current.set(key, { resolve, reject });

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
      pendingActionsRef.current.set(key, { resolve, reject });

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

  // Unregister a single session from daemon
  const sendUnregisterSession = useCallback((sessionId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({ cmd: 'unregister', id: sessionId }));
  }, []);

  // Mark a PR as visited (clears HasNewChanges flag)
  const sendPRVisited = useCallback((prId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Optimistic update - clear has_new_changes immediately
    const updatedPRs = prsRef.current.map(pr =>
      pr.id === prId ? { ...pr, has_new_changes: false } : pr
    );
    prsRef.current = updatedPRs;
    onPRsUpdate(updatedPRs);

    ws.send(JSON.stringify({ cmd: 'pr_visited', id: prId }));
  }, [onPRsUpdate]);

  const sendListWorktrees = useCallback((mainRepo: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'list_worktrees', main_repo: mainRepo }));
  }, []);

  const sendCreateWorktree = useCallback((mainRepo: string, branch: string, path?: string): Promise<WorktreeActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const actionKey = 'worktree_create_worktree_result';
      pendingActionsRef.current.set(actionKey, { resolve, reject });

      // Set timeout for action
      setTimeout(() => {
        if (pendingActionsRef.current.has(actionKey)) {
          pendingActionsRef.current.delete(actionKey);
          reject(new Error('Create worktree timed out'));
        }
      }, 30000);

      ws.send(JSON.stringify({
        cmd: 'create_worktree',
        main_repo: mainRepo,
        branch,
        ...(path && { path }),
      }));
    });
  }, []);

  const sendDeleteWorktree = useCallback((path: string): Promise<WorktreeActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const actionKey = 'worktree_delete_worktree_result';
      pendingActionsRef.current.set(actionKey, { resolve, reject });

      // Set timeout for action
      setTimeout(() => {
        if (pendingActionsRef.current.has(actionKey)) {
          pendingActionsRef.current.delete(actionKey);
          reject(new Error('Delete worktree timed out'));
        }
      }, 30000);

      ws.send(JSON.stringify({
        cmd: 'delete_worktree',
        path,
      }));
    });
  }, []);

  // Set a setting value with optimistic update
  const sendSetSetting = useCallback((key: string, value: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Optimistic update
    settingsRef.current = { ...settingsRef.current, [key]: value };
    onSettingsUpdate?.(settingsRef.current);

    ws.send(JSON.stringify({ cmd: 'set_setting', key, value }));
  }, [onSettingsUpdate]);

  return {
    isConnected: wsRef.current?.readyState === WebSocket.OPEN,
    connectionError,
    hasReceivedInitialState,
    settings: settingsRef.current,
    rateLimit,
    sendPRAction,
    sendMutePR,
    sendMuteRepo,
    sendRefreshPRs,
    sendClearSessions,
    sendUnregisterSession,
    sendPRVisited,
    sendListWorktrees,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendSetSetting,
  };
}
