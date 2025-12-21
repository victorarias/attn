import { useEffect, useRef, useCallback, useState } from 'react';
import type {
  Session as GeneratedSession,
  PR as GeneratedPR,
  Worktree as GeneratedWorktree,
  RepoState as GeneratedRepoState,
  WebSocketEvent as GeneratedWebSocketEvent,
  RecentLocation as GeneratedRecentLocation,
  BranchElement as GeneratedBranch,
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
export type RecentLocation = GeneratedRecentLocation;
export type Branch = GeneratedBranch;
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
  // Branch action result fields
  branch?: string;
};

export interface RateLimitState {
  resource: string;
  resetAt: Date;
}

// Protocol version - must match daemon's ProtocolVersion
// Increment when making breaking changes to the protocol
const PROTOCOL_VERSION = '11';

interface PRActionResult {
  success: boolean;
  error?: string;
}

interface WorktreeActionResult {
  success: boolean;
  path?: string;
  error?: string;
}

interface RecentLocationsResult {
  success: boolean;
  locations: RecentLocation[];
  error?: string;
}

interface BranchesResult {
  success: boolean;
  branches: Branch[];
  error?: string;
}

interface BranchActionResult {
  success: boolean;
  branch?: string;
  error?: string;
}

interface CheckDirtyResult {
  success: boolean;
  dirty?: boolean;
  error?: string;
}

interface StashResult {
  success: boolean;
  error?: string;
}

interface StashPopResult {
  success: boolean;
  conflict?: boolean;
  error?: string;
}

interface CheckAttnStashResult {
  success: boolean;
  found?: boolean;
  stashRef?: string;
  error?: string;
}

interface DefaultBranchResult {
  success: boolean;
  branch?: string;
  error?: string;
}

interface RemoteBranchesResult {
  success: boolean;
  branches: Branch[];
  error?: string;
}

interface GitFileChange {
  path: string;
  status: string;
  additions?: number;
  deletions?: number;
  old_path?: string;
}

export interface GitStatusUpdate {
  directory: string;
  staged: GitFileChange[];
  unstaged: GitFileChange[];
  untracked: GitFileChange[];
  error?: string;
}

export interface FileDiffResult {
  success: boolean;
  original: string;
  modified: string;
  error?: string;
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onPRsUpdate: (prs: DaemonPR[]) => void;
  onReposUpdate: (repos: RepoState[]) => void;
  onWorktreesUpdate?: (worktrees: DaemonWorktree[]) => void;
  onSettingsUpdate?: (settings: DaemonSettings) => void;
  onGitStatusUpdate?: (status: GitStatusUpdate) => void;
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
  onGitStatusUpdate,
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
            // Note: worktrees may be undefined due to Go's omitempty on empty arrays
            onWorktreesUpdate?.(data.worktrees || []);
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

          case 'recent_locations_result': {
            const pending = pendingActionsRef.current.get('get_recent_locations');
            if (pending) {
              pendingActionsRef.current.delete('get_recent_locations');
              if (data.success) {
                pending.resolve({
                  success: true,
                  locations: data.recent_locations || [],
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to get recent locations'));
              }
            }
            break;
          }

          case 'branches_result': {
            const pending = pendingActionsRef.current.get('list_branches');
            if (pending) {
              pendingActionsRef.current.delete('list_branches');
              if (data.success) {
                pending.resolve({
                  success: true,
                  branches: data.branches || [],
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to list branches'));
              }
            }
            break;
          }

          case 'delete_branch_result': {
            const pending = pendingActionsRef.current.get('delete_branch');
            if (pending) {
              pendingActionsRef.current.delete('delete_branch');
              if (data.success) {
                pending.resolve({ success: true, branch: data.branch });
              } else {
                pending.reject(new Error(data.error || 'Failed to delete branch'));
              }
            }
            break;
          }

          case 'switch_branch_result': {
            const pending = pendingActionsRef.current.get('switch_branch');
            if (pending) {
              pendingActionsRef.current.delete('switch_branch');
              if (data.success) {
                pending.resolve({ success: true, branch: data.branch });
              } else {
                pending.reject(new Error(data.error || 'Failed to switch branch'));
              }
            }
            break;
          }

          case 'create_branch_result': {
            const pending = pendingActionsRef.current.get('create_branch');
            if (pending) {
              pendingActionsRef.current.delete('create_branch');
              if (data.success) {
                pending.resolve({ success: true, branch: data.branch });
              } else {
                pending.reject(new Error(data.error || 'Failed to create branch'));
              }
            }
            break;
          }

          case 'check_dirty_result': {
            const pending = pendingActionsRef.current.get('check_dirty');
            if (pending) {
              pendingActionsRef.current.delete('check_dirty');
              if (data.success) {
                pending.resolve({ success: true, dirty: data.dirty });
              } else {
                pending.reject(new Error(data.error || 'Failed to check dirty state'));
              }
            }
            break;
          }

          case 'stash_result': {
            const pending = pendingActionsRef.current.get('stash');
            if (pending) {
              pendingActionsRef.current.delete('stash');
              if (data.success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error(data.error || 'Failed to stash'));
              }
            }
            break;
          }

          case 'stash_pop_result': {
            const pending = pendingActionsRef.current.get('stash_pop');
            if (pending) {
              pendingActionsRef.current.delete('stash_pop');
              if (data.success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error(data.error || 'Failed to pop stash'));
              }
            }
            break;
          }

          case 'check_attn_stash_result': {
            const pending = pendingActionsRef.current.get('check_attn_stash');
            if (pending) {
              pendingActionsRef.current.delete('check_attn_stash');
              if (data.success) {
                pending.resolve({ success: true, found: data.found, stashRef: data.stash_ref });
              } else {
                pending.reject(new Error(data.error || 'Failed to check stash'));
              }
            }
            break;
          }

          case 'commit_wip_result': {
            const pending = pendingActionsRef.current.get('commit_wip');
            if (pending) {
              pendingActionsRef.current.delete('commit_wip');
              if (data.success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error(data.error || 'Failed to commit WIP'));
              }
            }
            break;
          }

          case 'get_default_branch_result': {
            const pending = pendingActionsRef.current.get('get_default_branch');
            if (pending) {
              pendingActionsRef.current.delete('get_default_branch');
              if (data.success) {
                pending.resolve({ success: true, branch: data.branch });
              } else {
                pending.reject(new Error(data.error || 'Failed to get default branch'));
              }
            }
            break;
          }

          case 'fetch_remotes_result': {
            const pending = pendingActionsRef.current.get('fetch_remotes');
            if (pending) {
              pendingActionsRef.current.delete('fetch_remotes');
              if (data.success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error(data.error || 'Failed to fetch remotes'));
              }
            }
            break;
          }

          case 'list_remote_branches_result': {
            const pending = pendingActionsRef.current.get('list_remote_branches');
            if (pending) {
              pendingActionsRef.current.delete('list_remote_branches');
              if (data.success) {
                pending.resolve({ success: true, branches: data.branches || [] });
              } else {
                pending.reject(new Error(data.error || 'Failed to list remote branches'));
              }
            }
            break;
          }

          case 'git_status_update':
            if (data.directory) {
              onGitStatusUpdate?.({
                directory: data.directory,
                staged: data.staged || [],
                unstaged: data.unstaged || [],
                untracked: data.untracked || [],
                error: data.error,
              });
            }
            break;

          case 'file_diff_result': {
            const pending = pendingActionsRef.current.get('get_file_diff');
            if (pending) {
              pendingActionsRef.current.delete('get_file_diff');
              if (data.success) {
                pending.resolve({
                  success: true,
                  original: data.original || '',
                  modified: data.modified || '',
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to get diff'));
              }
            }
            break;
          }
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
  }, [wsUrl, onSessionsUpdate, onPRsUpdate, onReposUpdate, onWorktreesUpdate, onSettingsUpdate, onGitStatusUpdate]);

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

  // Get recent locations from daemon
  const sendGetRecentLocations = useCallback((limit?: number): Promise<RecentLocationsResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'get_recent_locations';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'get_recent_locations', ...(limit && { limit }) }));

      // Timeout after 10 seconds
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get recent locations timed out'));
        }
      }, 10000);
    });
  }, []);

  // List available branches (not checked out in any worktree)
  const sendListBranches = useCallback((mainRepo: string): Promise<BranchesResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'list_branches';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'list_branches', main_repo: mainRepo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('List branches timed out'));
        }
      }, 10000);
    });
  }, []);

  // Delete a branch
  const sendDeleteBranch = useCallback((mainRepo: string, branch: string, force: boolean = false): Promise<BranchActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'delete_branch';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'delete_branch', main_repo: mainRepo, branch, force }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Delete branch timed out'));
        }
      }, 30000);
    });
  }, []);

  // Switch main repo to a different branch
  const sendSwitchBranch = useCallback((mainRepo: string, branch: string): Promise<BranchActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'switch_branch';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'switch_branch', main_repo: mainRepo, branch }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Switch branch timed out'));
        }
      }, 30000);
    });
  }, []);

  // Create a new branch (without checking it out)
  const sendCreateBranch = useCallback((mainRepo: string, branch: string): Promise<BranchActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'create_branch';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'create_branch', main_repo: mainRepo, branch }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Create branch timed out'));
        }
      }, 30000);
    });
  }, []);

  // Create worktree from existing branch
  const sendCreateWorktreeFromBranch = useCallback((mainRepo: string, branch: string, path?: string): Promise<WorktreeActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const actionKey = 'worktree_create_worktree_result';
      pendingActionsRef.current.set(actionKey, { resolve, reject });

      setTimeout(() => {
        if (pendingActionsRef.current.has(actionKey)) {
          pendingActionsRef.current.delete(actionKey);
          reject(new Error('Create worktree from branch timed out'));
        }
      }, 30000);

      ws.send(JSON.stringify({
        cmd: 'create_worktree_from_branch',
        main_repo: mainRepo,
        branch,
        ...(path && { path }),
      }));
    });
  }, []);

  // Check if repo has uncommitted changes
  const sendCheckDirty = useCallback((repo: string): Promise<CheckDirtyResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'check_dirty';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'check_dirty', repo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Check dirty timed out'));
        }
      }, 10000);
    });
  }, []);

  // Stash changes with message
  const sendStash = useCallback((repo: string, message: string): Promise<StashResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'stash';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'stash', repo, message }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Stash timed out'));
        }
      }, 30000);
    });
  }, []);

  // Pop stash
  const sendStashPop = useCallback((repo: string): Promise<StashPopResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'stash_pop';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'stash_pop', repo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Stash pop timed out'));
        }
      }, 30000);
    });
  }, []);

  // Check for attn-created stash
  const sendCheckAttnStash = useCallback((repo: string, branch: string): Promise<CheckAttnStashResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'check_attn_stash';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'check_attn_stash', repo, branch }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Check stash timed out'));
        }
      }, 10000);
    });
  }, []);

  // Commit all changes as WIP
  const sendCommitWIP = useCallback((repo: string): Promise<StashResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'commit_wip';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'commit_wip', repo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Commit WIP timed out'));
        }
      }, 30000);
    });
  }, []);

  // Get default branch name
  const sendGetDefaultBranch = useCallback((repo: string): Promise<DefaultBranchResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'get_default_branch';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'get_default_branch', repo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get default branch timed out'));
        }
      }, 10000);
    });
  }, []);

  // Fetch all remotes
  const sendFetchRemotes = useCallback((repo: string): Promise<StashResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'fetch_remotes';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'fetch_remotes', repo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Fetch remotes timed out'));
        }
      }, 60000); // Longer timeout for network operations
    });
  }, []);

  // List remote branches
  const sendListRemoteBranches = useCallback((repo: string): Promise<RemoteBranchesResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'list_remote_branches';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'list_remote_branches', repo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('List remote branches timed out'));
        }
      }, 10000);
    });
  }, []);

  // Subscribe to git status updates for a directory
  const sendSubscribeGitStatus = useCallback((directory: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({ cmd: 'subscribe_git_status', directory }));
  }, []);

  // Unsubscribe from git status updates
  const sendUnsubscribeGitStatus = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({ cmd: 'unsubscribe_git_status' }));
  }, []);

  // Get file diff
  const sendGetFileDiff = useCallback((directory: string, path: string, staged?: boolean): Promise<FileDiffResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'get_file_diff';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'get_file_diff',
        directory,
        path,
        ...(staged !== undefined && { staged }),
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get file diff timed out'));
        }
      }, 10000);
    });
  }, []);

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
    sendGetRecentLocations,
    sendListBranches,
    sendDeleteBranch,
    sendSwitchBranch,
    sendCreateBranch,
    sendCreateWorktreeFromBranch,
    sendCheckDirty,
    sendStash,
    sendStashPop,
    sendCheckAttnStash,
    sendCommitWIP,
    sendGetDefaultBranch,
    sendFetchRemotes,
    sendListRemoteBranches,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendGetFileDiff,
  };
}
