import { useEffect, useRef, useCallback, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '@tauri-apps/api/core';
import type {
  Session as GeneratedSession,
  PR as GeneratedPR,
  Worktree as GeneratedWorktree,
  RepoState as GeneratedRepoState,
  AuthorState as GeneratedAuthorState,
  WebSocketEvent as GeneratedWebSocketEvent,
  RecentLocation as GeneratedRecentLocation,
  BranchElement as GeneratedBranch,
  Comment as GeneratedComment,
  ReviewFinding as GeneratedReviewFinding,
  WarningElement as GeneratedWarning,
  SessionState,
  PRRole,
  HeatState,
} from '../types/generated';
import { emitPtyEvent, setPtyBackend, type PtySpawnArgs } from '../pty/bridge';
import { isSuspiciousTerminalSize, isTerminalDebugEnabled } from '../utils/terminalDebug';

// Re-export types from generated for consumers
// Use type aliases to maintain backward compatibility
export type DaemonSession = GeneratedSession;
export type DaemonPR = GeneratedPR;
export type DaemonWorktree = GeneratedWorktree;
export type RepoState = GeneratedRepoState;
export type AuthorState = GeneratedAuthorState;
export type RecentLocation = GeneratedRecentLocation;
export type Branch = GeneratedBranch;
export type ReviewFinding = GeneratedReviewFinding;
export type DaemonSettings = Record<string, string>;
export type DaemonWarning = GeneratedWarning;

// Re-export enums and useful types
export { SessionState, PRRole, HeatState };

// Extended WebSocketEvent with action result fields (generated allows extra properties)
type WebSocketEvent = GeneratedWebSocketEvent & {
  id?: string;
  data?: string;
  seq?: number;
  scrollback?: string;
  scrollback_truncated?: boolean;
  screen_snapshot?: string;
  screen_rows?: number;
  screen_cols?: number;
  screen_cursor_x?: number;
  screen_cursor_y?: number;
  screen_cursor_visible?: boolean;
  screen_snapshot_fresh?: boolean;
  last_seq?: number;
  cols?: number;
  rows?: number;
  pid?: number;
  running?: boolean;
  exit_code?: number;
  signal?: string;
  reason?: string;
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
  // Reviewer streaming event fields
  review_id?: string;
  content?: string;
  finding?: ReviewFinding;
  comment_id?: string;
  tool_use?: {
    name: string;
    input: Record<string, unknown>;
    output: string;
  };
};

export interface RateLimitState {
  resource: string;
  resetAt: Date;
}

// Protocol version - must match daemon's ProtocolVersion
// Increment when making breaking changes to the protocol
const PROTOCOL_VERSION = '32';
const MAX_PENDING_ATTACH_OUTPUTS = 512;

interface PRActionResult {
  success: boolean;
  error?: string;
}

interface FetchPRDetailsResult {
  success: boolean;
  prs?: DaemonPR[];
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

interface EnsureRepoResult {
  success: boolean;
  cloned?: boolean;
  error?: string;
}

interface SpawnResult {
  success: boolean;
  error?: string;
}

interface AttachResult {
  success: boolean;
  error?: string;
  scrollback?: string;
  scrollback_truncated?: boolean;
  screen_snapshot?: string;
  screen_rows?: number;
  screen_cols?: number;
  screen_cursor_x?: number;
  screen_cursor_y?: number;
  screen_cursor_visible?: boolean;
  screen_snapshot_fresh?: boolean;
  last_seq?: number;
  cols?: number;
  rows?: number;
  pid?: number;
  running?: boolean;
}

interface RepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: DaemonWorktree[];
  branches: Branch[];
  fetched_at?: string;
}

interface RepoInfoResult {
  success: boolean;
  info?: RepoInfo;
  error?: string;
}

export interface ReviewState {
  review_id: string;
  repo_path: string;
  branch: string;
  viewed_files: string[];
}

interface ReviewStateResult {
  success: boolean;
  state?: ReviewState;
  error?: string;
}

// Re-export Comment type for consumers
export type ReviewComment = GeneratedComment;

interface AddCommentResult {
  success: boolean;
  comment?: ReviewComment;
  error?: string;
}

interface CommentActionResult {
  success: boolean;
  error?: string;
}

interface GetCommentsResult {
  success: boolean;
  comments?: ReviewComment[];
  error?: string;
}

interface MarkFileViewedResult {
  success: boolean;
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

export interface BranchDiffFile {
  path: string;
  status: string;
  old_path?: string;
  additions?: number;
  deletions?: number;
  has_uncommitted?: boolean;
}

export interface BranchDiffFilesResult {
  success: boolean;
  base_ref: string;
  files: BranchDiffFile[];
  error?: string;
}

// Tool use event data
export interface ReviewToolUse {
  name: string;
  input: Record<string, unknown>;
  output: string;
}

// Unified reviewer event type for ordered rendering
export type ReviewerEvent =
  | { type: 'chunk'; content: string }
  | { type: 'tool_use'; name: string; input: Record<string, unknown>; output: string };

// Reviewer event callbacks
export interface ReviewerCallbacks {
  onReviewStarted?: (reviewId: string) => void;
  onReviewChunk?: (reviewId: string, content: string) => void;
  onReviewFinding?: (reviewId: string, finding: ReviewFinding, comment?: ReviewComment) => void;
  onReviewCommentResolved?: (reviewId: string, commentId: string) => void;
  onReviewToolUse?: (reviewId: string, toolUse: ReviewToolUse) => void;
  onReviewComplete?: (reviewId: string, success: boolean, error?: string) => void;
  onReviewCancelled?: (reviewId: string) => void;
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onPRsUpdate: (prs: DaemonPR[]) => void;
  onReposUpdate: (repos: RepoState[]) => void;
  onAuthorsUpdate: (authors: AuthorState[]) => void;
  onWorktreesUpdate?: (worktrees: DaemonWorktree[]) => void;
  onSettingsUpdate?: (settings: DaemonSettings) => void;
  onSettingError?: (message: string) => void;
  onGitStatusUpdate?: (status: GitStatusUpdate) => void;
  reviewer?: ReviewerCallbacks;
  wsUrl?: string;
}

// Default WebSocket port, can be overridden via VITE_DAEMON_PORT env var
const DEFAULT_WS_URL = `ws://127.0.0.1:${import.meta.env.VITE_DAEMON_PORT || '9849'}/ws`;

function dedupeSessionsByID(sessions: DaemonSession[]): DaemonSession[] {
  const deduped: DaemonSession[] = [];
  const indexByID = new Map<string, number>();
  for (const session of sessions) {
    const existingIndex = indexByID.get(session.id);
    if (existingIndex === undefined) {
      indexByID.set(session.id, deduped.length);
      deduped.push(session);
      continue;
    }
    deduped[existingIndex] = session;
  }
  return deduped;
}

function upsertSessionByID(sessions: DaemonSession[], session: DaemonSession): DaemonSession[] {
  const index = sessions.findIndex((entry) => entry.id === session.id);
  if (index === -1) {
    return [...sessions, session];
  }
  const updated = [...sessions];
  updated[index] = session;
  return updated;
}

function isSessionNotFoundError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return message.toLowerCase().includes('session not found');
}

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
//    - sendMuteAuthor: Toggle author mute state
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
  onAuthorsUpdate,
  onWorktreesUpdate,
  onSettingsUpdate,
  onSettingError,
  onGitStatusUpdate,
  reviewer,
  wsUrl = DEFAULT_WS_URL,
}: UseDaemonSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const sessionsRef = useRef<DaemonSession[]>([]);
  const prsRef = useRef<DaemonPR[]>([]);
  const reposRef = useRef<RepoState[]>([]);
  const authorsRef = useRef<AuthorState[]>([]);
  const settingsRef = useRef<DaemonSettings>({});
  const reconnectTimeoutRef = useRef<number | null>(null);
  const reconnectDelayRef = useRef<number>(1000); // Start with 1s, exponential backoff
  const pendingActionsRef = useRef<Map<string, { resolve: (result: any) => void; reject: (error: Error) => void }>>(new Map());
  const recoveryNoticeTimeoutRef = useRef<number | null>(null);
  const gitStatusSubscriptionRef = useRef<string | null>(null);
  const branchDiffInFlightRef = useRef<Map<string, Promise<BranchDiffFilesResult>>>(new Map());
  const attachedPtySessionsRef = useRef<Set<string>>(new Set());
  const ptySeqRef = useRef<Map<string, number>>(new Map());
  const pendingAttachOutputsRef = useRef<Map<string, Array<{ data: string; seq?: number }>>>(new Map());
  const pendingSessionVisualizedRef = useRef<Set<string>>(new Set());
  const daemonInstanceIDRef = useRef<string>('');
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [hasReceivedInitialState, setHasReceivedInitialState] = useState(false);
  const [rateLimit, setRateLimit] = useState<RateLimitState | null>(null);
  const [warnings, setWarnings] = useState<DaemonWarning[]>([]);

  // Circuit breaker state for reconnect storms
  const reconnectAttemptsRef = useRef(0);
  const circuitOpenRef = useRef(false);
  const circuitResetTimeoutRef = useRef<number | null>(null);

  const MAX_RECONNECTS_BEFORE_PAUSE = 8;
  const MAX_RECONNECT_DELAY_MS = 5000;
  const RECOVERY_NOTICE = 'Daemon is recovering PTY sessions. Please retry in a moment.';

  const showRecoveringNoticeForCommand = useCallback((cmd: string | undefined) => {
    if (!cmd) return;
    const needsNotice = new Set([
      'spawn_session',
      'attach_session',
      'detach_session',
      'pty_input',
      'pty_resize',
      'kill_session',
      'clear_sessions',
      'unregister',
    ]);
    if (!needsNotice.has(cmd)) {
      return;
    }

    setConnectionError(RECOVERY_NOTICE);
    if (recoveryNoticeTimeoutRef.current) {
      clearTimeout(recoveryNoticeTimeoutRef.current);
    }
    recoveryNoticeTimeoutRef.current = window.setTimeout(() => {
      setConnectionError((prev) => (prev === RECOVERY_NOTICE ? null : prev));
      recoveryNoticeTimeoutRef.current = null;
    }, 2000);
  }, []);

  const rejectPendingByPredicate = useCallback((predicate: (key: string) => boolean, error: Error) => {
    for (const [key, pending] of pendingActionsRef.current.entries()) {
      if (!predicate(key)) {
        continue;
      }
      pendingActionsRef.current.delete(key);
      pending.reject(error);
    }
  }, []);

  const rejectPendingForCommand = useCallback((cmd: string | undefined, errorMessage: string) => {
    const error = new Error(errorMessage);
    if (!cmd) {
      return;
    }

    switch (cmd) {
      case 'spawn_session':
        rejectPendingByPredicate((key) => key.startsWith('pty_spawn_'), error);
        return;
      case 'attach_session':
        rejectPendingByPredicate((key) => key.startsWith('pty_attach_'), error);
        return;
      case 'approve_pr':
        rejectPendingByPredicate((key) => key.endsWith(':approve'), error);
        return;
      case 'merge_pr':
        rejectPendingByPredicate((key) => key.endsWith(':merge'), error);
        return;
      case 'get_file_diff':
        rejectPendingByPredicate((key) => key.startsWith('get_file_diff_'), error);
        return;
      case 'get_branch_diff_files':
        rejectPendingByPredicate((key) => key.startsWith('get_branch_diff_files_'), error);
        return;
      case 'get_repo_info':
        rejectPendingByPredicate((key) => key.startsWith('repo_info_'), error);
        return;
      case 'create_worktree':
        rejectPendingByPredicate((key) => key === 'worktree_create_worktree_result', error);
        return;
      case 'delete_worktree':
        rejectPendingByPredicate((key) => key === 'worktree_delete_worktree_result', error);
        return;
      default:
        rejectPendingByPredicate((key) => key === cmd, error);
    }
  }, [rejectPendingByPredicate]);

  const ensureDaemonRunning = useCallback(async () => {
    if (!isTauri()) {
      return;
    }
    try {
      const isRunning = await invoke<boolean>('is_daemon_running');
      if (!isRunning) {
        console.log('[Daemon] Not running during reconnect, starting daemon...');
        await invoke('start_daemon', { prefer_local: import.meta.env.VITE_INSTALL_CHANNEL === 'source' });
      }
    } catch (err) {
      console.error('[Daemon] Failed to ensure daemon is running:', err);
    }
  }, []);

  const connect = useCallback(async () => {
    if (wsRef.current?.readyState === WebSocket.OPEN || wsRef.current?.readyState === WebSocket.CONNECTING) return;

    await ensureDaemonRunning();

    const ws = new WebSocket(wsUrl);

    ws.onopen = () => {
      console.log('[Daemon] WebSocket connected');
      setConnectionError(null);
      reconnectDelayRef.current = 1000; // Reset to 1s on successful connect
      reconnectAttemptsRef.current = 0;
      circuitOpenRef.current = false;
      if (circuitResetTimeoutRef.current) {
        clearTimeout(circuitResetTimeoutRef.current);
        circuitResetTimeoutRef.current = null;
      }

      if (gitStatusSubscriptionRef.current) {
        ws.send(JSON.stringify({ cmd: 'subscribe_git_status', directory: gitStatusSubscriptionRef.current }));
      }

      if (pendingSessionVisualizedRef.current.size > 0) {
        for (const sessionID of pendingSessionVisualizedRef.current) {
          ws.send(JSON.stringify({ cmd: 'session_visualized', id: sessionID }));
        }
        pendingSessionVisualizedRef.current.clear();
      }
    };

    ws.onmessage = (event) => {
      try {
        const data: WebSocketEvent = JSON.parse(event.data);

        switch (data.event) {
          case 'initial_state':
            if (
              data.daemon_instance_id &&
              daemonInstanceIDRef.current &&
              data.daemon_instance_id !== daemonInstanceIDRef.current
            ) {
              // Endpoint identity changed (new daemon instance). Keep the
              // attached session set so we can reattach after initial_state,
              // but clear stream caches to force clean replay.
              ptySeqRef.current.clear();
              pendingAttachOutputsRef.current.clear();
            }
            daemonInstanceIDRef.current = data.daemon_instance_id || '';
            // Check protocol version on initial connection
            if (data.protocol_version && data.protocol_version !== PROTOCOL_VERSION) {
              console.error(`[Daemon] Protocol version mismatch: daemon=${data.protocol_version}, client=${PROTOCOL_VERSION}`);
              const daemonVersion = Number(data.protocol_version);
              const clientVersion = Number(PROTOCOL_VERSION);
              const activeSessions = data.sessions?.length || 0;
              if (!Number.isNaN(daemonVersion) && !Number.isNaN(clientVersion) && daemonVersion < clientVersion) {
                setConnectionError(
                  `New daemon version available. Restart when ready (${activeSessions} active sessions may be lost). daemon v${data.protocol_version}, app v${PROTOCOL_VERSION}`
                );
              } else {
                setConnectionError(`Version mismatch: daemon v${data.protocol_version}, app v${PROTOCOL_VERSION}. Restart/reinstall required.`);
              }
              // Open circuit immediately to prevent reconnection storm
              // Version mismatch won't fix itself - requires manual intervention
              circuitOpenRef.current = true;
              ws.close();
              return;
            }
            const nextSessions = dedupeSessionsByID(data.sessions || []);
            sessionsRef.current = nextSessions;
            onSessionsUpdate(nextSessions);
            const activeIDs = new Set(nextSessions.map((session) => session.id));
            for (const sessionId of attachedPtySessionsRef.current) {
              if (activeIDs.has(sessionId)) {
                continue;
              }
              attachedPtySessionsRef.current.delete(sessionId);
              ptySeqRef.current.delete(sessionId);
            }
            const nextPRs = data.prs || [];
            prsRef.current = nextPRs;
            onPRsUpdate(nextPRs);

            const nextRepos = data.repos || [];
            reposRef.current = nextRepos;
            onReposUpdate(nextRepos);

            const nextAuthors = data.authors || [];
            authorsRef.current = nextAuthors;
            onAuthorsUpdate(nextAuthors);

            const nextSettings = data.settings || {};
            settingsRef.current = nextSettings;
            onSettingsUpdate?.(nextSettings);
            const nextWarnings = data.warnings || [];
            setWarnings(nextWarnings);
            if (nextWarnings.length > 0 && ws.readyState === WebSocket.OPEN) {
              ws.send(JSON.stringify({ cmd: 'clear_warnings' }));
            }
            setHasReceivedInitialState(true);
            if (ws.readyState === WebSocket.OPEN) {
              // Re-attach PTY streams only after recovery barrier has lifted and
              // initial_state has arrived.
              for (const sessionId of attachedPtySessionsRef.current) {
                ws.send(JSON.stringify({ cmd: 'attach_session', id: sessionId }));
              }
            }
            break;

          case 'spawn_result': {
            if (data.id) {
              const key = `pty_spawn_${data.id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve({ success: true });
                } else {
                  pending.reject(new Error(data.error || 'Failed to spawn session'));
                }
              }
            }
            break;
          }

          case 'attach_result': {
            if (data.id) {
              const key = `pty_attach_${data.id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve({
                    success: true,
                    scrollback: data.scrollback,
                    scrollback_truncated: data.scrollback_truncated,
                    screen_snapshot: data.screen_snapshot,
                    screen_rows: data.screen_rows,
                    screen_cols: data.screen_cols,
                    screen_cursor_x: data.screen_cursor_x,
                    screen_cursor_y: data.screen_cursor_y,
                    screen_cursor_visible: data.screen_cursor_visible,
                    screen_snapshot_fresh: data.screen_snapshot_fresh,
                    last_seq: data.last_seq,
                    cols: data.cols,
                    rows: data.rows,
                    pid: data.pid,
                    running: data.running,
                  });
                } else {
                  pending.reject(new Error(data.error || 'Failed to attach session'));
                }
              }

              if (data.success) {
                attachedPtySessionsRef.current.add(data.id);
              } else {
                attachedPtySessionsRef.current.delete(data.id);
                ptySeqRef.current.delete(data.id);
                pendingAttachOutputsRef.current.delete(data.id);
              }

              if (data.success) {
                const hasScreenSnapshot = Boolean(
                  data.screen_snapshot && data.screen_snapshot_fresh !== false
                );
                const shouldReset = hasScreenSnapshot || ptySeqRef.current.has(data.id);
                if (shouldReset) {
                  emitPtyEvent({
                    event: 'reset',
                    id: data.id,
                    reason: hasScreenSnapshot ? 'snapshot_restore' : 'reattach',
                  });
                }
                if (typeof data.last_seq === 'number') {
                  ptySeqRef.current.set(data.id, data.last_seq);
                } else {
                  ptySeqRef.current.set(data.id, 0);
                }
                if (hasScreenSnapshot && data.screen_snapshot) {
                  emitPtyEvent({ event: 'data', id: data.id, data: data.screen_snapshot });
                } else if (data.scrollback) {
                  emitPtyEvent({ event: 'data', id: data.id, data: data.scrollback });
                }
                const queued = pendingAttachOutputsRef.current.get(data.id);
                if (queued && queued.length > 0) {
                  pendingAttachOutputsRef.current.delete(data.id);
                  for (const chunk of queued) {
                    if (typeof chunk.seq === 'number') {
                      const lastSeq = ptySeqRef.current.get(data.id);
                      // Keep seq==lastSeq during attach replay: Session.info().last_seq can race
                      // ahead of the replay payload, and that first live chunk may be missing otherwise.
                      if (typeof lastSeq === 'number' && chunk.seq < lastSeq) {
                        continue;
                      }
                      ptySeqRef.current.set(data.id, chunk.seq);
                    }
                    emitPtyEvent({ event: 'data', id: data.id, data: chunk.data });
                  }
                }
                if (!hasScreenSnapshot && data.scrollback_truncated) {
                  const session = sessionsRef.current.find((entry) => entry.id === data.id);
                  if (session?.agent === 'codex') {
                    emitPtyEvent({
                      event: 'error',
                      id: data.id,
                      error: 'Restore scrollback was truncated; Codex full-screen output may be incomplete.',
                    });
                  }
                }
              }
            }
            break;
          }

          case 'pty_output': {
            if (data.id && data.data) {
              const attachKey = `pty_attach_${data.id}`;
              if (pendingActionsRef.current.has(attachKey)) {
                const queued = pendingAttachOutputsRef.current.get(data.id) || [];
                if (queued.length >= MAX_PENDING_ATTACH_OUTPUTS) {
                  queued.shift();
                }
                queued.push({ data: data.data, seq: data.seq });
                pendingAttachOutputsRef.current.set(data.id, queued);
                break;
              }
              if (typeof data.seq === 'number') {
                const lastSeq = ptySeqRef.current.get(data.id);
                if (typeof lastSeq === 'number' && data.seq <= lastSeq) {
                  break;
                }
                ptySeqRef.current.set(data.id, data.seq);
              }
              emitPtyEvent({ event: 'data', id: data.id, data: data.data });
            }
            break;
          }

          case 'session_exited':
            if (data.id) {
              attachedPtySessionsRef.current.delete(data.id);
              ptySeqRef.current.delete(data.id);
              pendingAttachOutputsRef.current.delete(data.id);
              emitPtyEvent({
                event: 'exit',
                id: data.id,
                code: data.exit_code ?? 0,
                signal: data.signal,
              });
            }
            break;

          case 'pty_desync':
            if (data.id) {
              emitPtyEvent({ event: 'reset', id: data.id, reason: data.reason || 'desync' });
              ptySeqRef.current.delete(data.id);
              ws.send(JSON.stringify({ cmd: 'attach_session', id: data.id }));
            }
            break;

          case 'session_registered':
            if (data.session) {
              sessionsRef.current = upsertSessionByID(sessionsRef.current, data.session);
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'session_unregistered':
            if (data.session) {
              attachedPtySessionsRef.current.delete(data.session.id);
              ptySeqRef.current.delete(data.session.id);
              sessionsRef.current = sessionsRef.current.filter(
                (s) => s.id !== data.session!.id
              );
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'session_state_changed':
          case 'session_todos_updated':
            if (data.session) {
              sessionsRef.current = upsertSessionByID(sessionsRef.current, data.session);
              onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'sessions_updated':
            {
              const dedupedSessions = dedupeSessionsByID(data.sessions || []);
              sessionsRef.current = dedupedSessions;
              onSessionsUpdate(dedupedSessions);
              const activeIDs = new Set(dedupedSessions.map((session) => session.id));
              for (const sessionId of attachedPtySessionsRef.current) {
                if (activeIDs.has(sessionId)) {
                  continue;
                }
                attachedPtySessionsRef.current.delete(sessionId);
                ptySeqRef.current.delete(sessionId);
              }
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

          case 'authors_updated':
            if (data.authors) {
              authorsRef.current = data.authors;
              onAuthorsUpdate(data.authors);
            }
            break;

          case 'settings_updated':
            if (data.settings) {
              settingsRef.current = data.settings;
              onSettingsUpdate?.(data.settings);
            }
            if (data.success === false && data.error) {
              onSettingError?.(data.error);
            }
            break;

          case 'pr_action_result':
            if (data.action && data.id) {
              const key = `${data.id}:${data.action}`;
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

          case 'fetch_pr_details_result': {
            const pending = pendingActionsRef.current.get('fetch_pr_details');
            if (pending) {
              pendingActionsRef.current.delete('fetch_pr_details');
              if (data.success) {
                pending.resolve({ success: true, prs: data.prs || [] });
              } else {
                pending.reject(new Error(data.error || 'Fetch PR details failed'));
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

          case 'ensure_repo_result': {
            const pending = pendingActionsRef.current.get('ensure_repo');
            if (pending) {
              pendingActionsRef.current.delete('ensure_repo');
              if (data.success) {
                pending.resolve({ success: true, cloned: data.cloned });
              } else {
                pending.reject(new Error(data.error || 'Failed to ensure repo'));
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
            // Use path-based key to match the request
            const key = `get_file_diff_${data.path}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
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

          case 'branch_diff_files_result': {
            const key = `get_branch_diff_files_${data.directory}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if (data.success) {
                pending.resolve({
                  success: true,
                  base_ref: data.base_ref || '',
                  files: data.files || [],
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to get branch diff files'));
              }
            }
            break;
          }

          case 'get_repo_info_result': {
            // Extract repo from info to build key
            const repoPath = (data as any).info?.repo || '';
            const key = `repo_info_${repoPath}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if ((data as any).success) {
                pending.resolve({ success: true, info: (data as any).info });
              } else {
                pending.resolve({ success: false, error: (data as any).error });
              }
            }
            break;
          }

          case 'get_review_state_result': {
            const pending = pendingActionsRef.current.get('get_review_state');
            if (pending) {
              pendingActionsRef.current.delete('get_review_state');
              if ((data as any).success) {
                pending.resolve({ success: true, state: (data as any).state });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to get review state'));
              }
            }
            break;
          }

          case 'mark_file_viewed_result': {
            const pending = pendingActionsRef.current.get('mark_file_viewed');
            if (pending) {
              pendingActionsRef.current.delete('mark_file_viewed');
              if ((data as any).success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to mark file viewed'));
              }
            }
            break;
          }

          case 'add_comment_result': {
            const pending = pendingActionsRef.current.get('add_comment');
            if (pending) {
              pendingActionsRef.current.delete('add_comment');
              if ((data as any).success) {
                pending.resolve({ success: true, comment: (data as any).comment });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to add comment'));
              }
            }
            break;
          }

          case 'update_comment_result': {
            const pending = pendingActionsRef.current.get('update_comment');
            if (pending) {
              pendingActionsRef.current.delete('update_comment');
              if ((data as any).success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to update comment'));
              }
            }
            break;
          }

          case 'resolve_comment_result': {
            const pending = pendingActionsRef.current.get('resolve_comment');
            if (pending) {
              pendingActionsRef.current.delete('resolve_comment');
              if ((data as any).success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to resolve comment'));
              }
            }
            break;
          }

          case 'wont_fix_comment_result': {
            const pending = pendingActionsRef.current.get('wont_fix_comment');
            if (pending) {
              pendingActionsRef.current.delete('wont_fix_comment');
              if ((data as any).success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to mark comment as won\'t fix'));
              }
            }
            break;
          }

          case 'delete_comment_result': {
            const pending = pendingActionsRef.current.get('delete_comment');
            if (pending) {
              pendingActionsRef.current.delete('delete_comment');
              if ((data as any).success) {
                pending.resolve({ success: true });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to delete comment'));
              }
            }
            break;
          }

          case 'get_comments_result': {
            const pending = pendingActionsRef.current.get('get_comments');
            if (pending) {
              pendingActionsRef.current.delete('get_comments');
              if ((data as any).success) {
                pending.resolve({ success: true, comments: (data as any).comments || [] });
              } else {
                pending.reject(new Error((data as any).error || 'Failed to get comments'));
              }
            }
            break;
          }

          // Reviewer streaming events
          case 'review_started':
            if (data.review_id && reviewer?.onReviewStarted) {
              reviewer.onReviewStarted(data.review_id);
            }
            break;

          case 'review_chunk':
            if (data.review_id && data.content && reviewer?.onReviewChunk) {
              reviewer.onReviewChunk(data.review_id, data.content);
            }
            break;

          case 'review_finding':
            if (data.review_id && data.finding && reviewer?.onReviewFinding) {
              reviewer.onReviewFinding(data.review_id, data.finding, data.comment);
            }
            break;

          case 'review_comment_resolved':
            if (data.review_id && data.comment_id && reviewer?.onReviewCommentResolved) {
              reviewer.onReviewCommentResolved(data.review_id, data.comment_id);
            }
            break;

          case 'review_tool_use':
            if (data.review_id && data.tool_use && reviewer?.onReviewToolUse) {
              reviewer.onReviewToolUse(data.review_id, data.tool_use);
            }
            break;

          case 'review_complete':
            if (data.review_id && reviewer?.onReviewComplete) {
              reviewer.onReviewComplete(data.review_id, data.success ?? false, data.error);
            }
            break;

          case 'review_cancelled':
            if (data.review_id && reviewer?.onReviewCancelled) {
              reviewer.onReviewCancelled(data.review_id);
            }
            break;

          case 'command_error':
            if (data.error === 'daemon_recovering') {
              console.debug('[Daemon] Command deferred while daemon recovers:', data.cmd);
              rejectPendingForCommand(data.cmd, 'Daemon is recovering. Please retry in a moment.');
              showRecoveringNoticeForCommand(data.cmd);
              break;
            }
            console.error('[Daemon] Command error:', data.cmd, data.error);
            rejectPendingForCommand(data.cmd, data.error || `Command ${data.cmd || ''} failed`);
            break;
        }
      } catch (err) {
        console.error('[Daemon] Parse error:', err);
      }
    };

    ws.onclose = () => {
      wsRef.current = null;

      // Circuit breaker: if open, don't retry
      if (circuitOpenRef.current) {
        console.error('[Daemon] Circuit open, not retrying');
        return;
      }

      reconnectAttemptsRef.current++;
      let delay = reconnectDelayRef.current;
      if (reconnectAttemptsRef.current > MAX_RECONNECTS_BEFORE_PAUSE) {
        // Keep retrying in the background instead of pausing indefinitely.
        delay = MAX_RECONNECT_DELAY_MS;
        reconnectDelayRef.current = MAX_RECONNECT_DELAY_MS;
        setConnectionError('Daemon disconnected. Reconnecting...');
        console.error('[Daemon] High reconnect attempts; continuing retries in background');
      } else {
        reconnectDelayRef.current = Math.min(delay * 1.5, MAX_RECONNECT_DELAY_MS);
      }

      // Normal reconnect with backoff
      console.log(`[Daemon] WebSocket disconnected, reconnecting in ${delay}ms... (attempt ${reconnectAttemptsRef.current}/${MAX_RECONNECTS_BEFORE_PAUSE})`);
      reconnectTimeoutRef.current = window.setTimeout(() => {
        void connect();
      }, delay);
    };

    ws.onerror = (err) => {
      console.error('[Daemon] WebSocket error:', err);
      ws.close();
    };

    wsRef.current = ws;
  }, [wsUrl, onSessionsUpdate, onPRsUpdate, onReposUpdate, onAuthorsUpdate, onWorktreesUpdate, onSettingsUpdate, onSettingError, onGitStatusUpdate, rejectPendingForCommand, ensureDaemonRunning, showRecoveringNoticeForCommand]);

  useEffect(() => {
    void connect();

    return () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      if (circuitResetTimeoutRef.current) {
        clearTimeout(circuitResetTimeoutRef.current);
      }
      if (recoveryNoticeTimeoutRef.current) {
        clearTimeout(recoveryNoticeTimeoutRef.current);
      }
      wsRef.current?.close();
    };
  }, [connect]);

  // Manual retry function for UI
  const retryConnection = useCallback(() => {
    console.log('[Daemon] Manual retry requested');
    if (circuitResetTimeoutRef.current) {
      clearTimeout(circuitResetTimeoutRef.current);
      circuitResetTimeoutRef.current = null;
    }
    circuitOpenRef.current = false;
    reconnectAttemptsRef.current = 0;
    reconnectDelayRef.current = 1000;
    setConnectionError(null);
    void connect();
  }, [connect]);

  const sendSpawnSession = useCallback((args: PtySpawnArgs): Promise<SpawnResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `pty_spawn_${args.id}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'spawn_session',
        id: args.id,
        cwd: args.cwd,
        agent: args.shell ? 'shell' : (args.agent || 'codex'),
        cols: args.cols,
        rows: args.rows,
        ...(args.label && { label: args.label }),
        ...(args.resume_session_id && { resume_session_id: args.resume_session_id }),
        ...(args.resume_picker && { resume_picker: args.resume_picker }),
        ...(args.fork_session && { fork_session: args.fork_session }),
        ...(args.executable && { executable: args.executable }),
        ...(args.claude_executable && { claude_executable: args.claude_executable }),
        ...(args.codex_executable && { codex_executable: args.codex_executable }),
        ...(args.copilot_executable && { copilot_executable: args.copilot_executable }),
        ...(args.pi_executable && { pi_executable: args.pi_executable }),
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Spawn session timed out'));
        }
      }, 30000);
    });
  }, []);

  const sendAttachSession = useCallback((id: string): Promise<AttachResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `pty_attach_${id}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'attach_session', id }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          pendingAttachOutputsRef.current.delete(id);
          reject(new Error('Attach session timed out'));
        }
      }, 15000);
    });
  }, []);

  const sendDetachSession = useCallback((id: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    attachedPtySessionsRef.current.delete(id);
    ptySeqRef.current.delete(id);
    pendingAttachOutputsRef.current.delete(id);
    ws.send(JSON.stringify({ cmd: 'detach_session', id }));
  }, []);

  const sendPtyInput = useCallback((id: string, data: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'pty_input', id, data }));
  }, []);

  const sendPtyResize = useCallback((id: string, cols: number, rows: number) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const suspiciousResize = isSuspiciousTerminalSize(cols, rows);
    if (suspiciousResize) {
      console.warn('[DaemonSocket] Sending suspicious PTY resize', { id, cols, rows });
    } else if (isTerminalDebugEnabled()) {
      console.log('[DaemonSocket] Sending PTY resize', { id, cols, rows });
    }
    ws.send(JSON.stringify({ cmd: 'pty_resize', id, cols, rows }));
  }, []);

  const sendKillSession = useCallback((id: string, signal?: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'kill_session', id, ...(signal && { signal }) }));
  }, []);

  useEffect(() => {
    setPtyBackend({
      spawn: async (args: PtySpawnArgs) => {
        const existingSession = sessionsRef.current.find((session) => session.id === args.id);
        const sessionKnownToDaemon = !!existingSession;

        // For new spawns, prime PTY size before attach.
        // For existing daemon sessions, avoid transient bootstrap resizes
        // (hidden terminals can report placeholder dimensions briefly).
        if (!sessionKnownToDaemon) {
          sendPtyResize(args.id, args.cols, args.rows);
        }

        try {
          await sendAttachSession(args.id);
          return;
        } catch (attachErr) {
          // If daemon already knows this session but PTY is gone, check if it's recoverable.
          // Claude sessions are recovered by resuming the existing session ID.
          // This keeps the first-run contract:
          //   first run: --session-id <id>
          //   recover:   --resume <id>
          if (sessionKnownToDaemon) {
            if (existingSession.agent === 'claude') {
              const resumeArgs: PtySpawnArgs = {
                ...args,
                resume_session_id: args.id,
                resume_picker: null,
                fork_session: null,
              };
              console.log(
                '[DaemonSocket] Recovering session %s via resume (recoverable=%s)',
                args.id,
                String(existingSession.recoverable ?? false),
              );
              try {
                await sendSpawnSession(resumeArgs);
              } catch (spawnErr) {
                const message = spawnErr instanceof Error ? spawnErr.message.toLowerCase() : String(spawnErr).toLowerCase();
                if (!message.includes('already exists')) {
                  throw new Error(
                    'Failed to recover session. Close it and start a new session.'
                  );
                }
              }
              await sendAttachSession(args.id);
              return;
            }
            throw new Error(
              'No live PTY found for this session. It likely ended when the daemon restarted. Close it and start a new session.'
            );
          }
          if (!isSessionNotFoundError(attachErr)) {
            throw attachErr;
          }
        }

        try {
          await sendSpawnSession(args);
        } catch (err) {
          const message = err instanceof Error ? err.message.toLowerCase() : String(err).toLowerCase();
          if (!message.includes('already exists')) {
            throw err;
          }
        }
        await sendAttachSession(args.id);
      },
      write: async (id: string, data: string) => {
        sendPtyInput(id, data);
      },
      resize: async (id: string, cols: number, rows: number) => {
        sendPtyResize(id, cols, rows);
      },
      kill: async (id: string) => {
        attachedPtySessionsRef.current.delete(id);
        ptySeqRef.current.delete(id);
        sendDetachSession(id);
        sendKillSession(id);
      },
    });

    return () => {
      setPtyBackend(null);
    };
  }, [sendAttachSession, sendDetachSession, sendKillSession, sendPtyInput, sendPtyResize, sendSpawnSession]);

  const sendPRAction = useCallback((
    action: 'approve' | 'merge',
    id: string,
    method?: string
  ): Promise<PRActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `${id}:${action}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      const msg = {
        cmd: `${action}_pr`,
        id,
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

  // Mute a PR author with optimistic update
  const sendMuteAuthor = useCallback((author: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Optimistic update - toggle author muted state immediately
    const existingAuthor = authorsRef.current.find(a => a.author === author);
    let updatedAuthors: AuthorState[];
    if (existingAuthor) {
      updatedAuthors = authorsRef.current.map(a =>
        a.author === author ? { ...a, muted: !a.muted } : a
      );
    } else {
      // Author doesn't exist in state yet, add it as muted
      updatedAuthors = [...authorsRef.current, { author, muted: true }];
    }
    authorsRef.current = updatedAuthors;
    onAuthorsUpdate(updatedAuthors);

    ws.send(JSON.stringify({ cmd: 'mute_author', author }));
  }, [onAuthorsUpdate]);

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

  // Fetch PR details (branch, status) for a repo
  const sendFetchPRDetails = useCallback((id: string): Promise<FetchPRDetailsResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'fetch_pr_details';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'fetch_pr_details', id }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Fetch PR details timed out'));
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

    attachedPtySessionsRef.current.delete(sessionId);
    ptySeqRef.current.delete(sessionId);
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

  const sendCreateWorktree = useCallback((mainRepo: string, branch: string, path?: string, startingFrom?: string): Promise<WorktreeActionResult> => {
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
        ...(startingFrom && { starting_from: startingFrom }),
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

  // Ensure repo exists (clone if needed) and fetch remotes
  const sendEnsureRepo = useCallback((targetPath: string, cloneUrl: string): Promise<EnsureRepoResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'ensure_repo';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'ensure_repo',
        target_path: targetPath,
        clone_url: cloneUrl,
      }));

      // Longer timeout for clone operations (2 minutes)
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Ensure repo timed out'));
        }
      }, 120000);
    });
  }, []);

  // Subscribe to git status updates for a directory
  const sendSubscribeGitStatus = useCallback((directory: string) => {
    const previousDirectory = gitStatusSubscriptionRef.current;
    if (previousDirectory === directory) return;

    gitStatusSubscriptionRef.current = directory;
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    if (previousDirectory) {
      ws.send(JSON.stringify({ cmd: 'unsubscribe_git_status' }));
    }
    ws.send(JSON.stringify({ cmd: 'subscribe_git_status', directory }));
  }, []);

  // Unsubscribe from git status updates
  const sendUnsubscribeGitStatus = useCallback(() => {
    const hadSubscription = gitStatusSubscriptionRef.current;
    gitStatusSubscriptionRef.current = null;
    if (!hadSubscription) return;

    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({ cmd: 'unsubscribe_git_status' }));
  }, []);

  // Notify daemon that the user has visualized a session long enough to resume deferred classification.
  const sendSessionVisualized = useCallback((id: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      pendingSessionVisualizedRef.current.add(id);
      return;
    }
    ws.send(JSON.stringify({ cmd: 'session_visualized', id }));
    pendingSessionVisualizedRef.current.delete(id);
  }, []);

  // Get file diff
  // Options: staged (deprecated), baseRef (for PR-like branch diffs)
  const sendGetFileDiff = useCallback((
    directory: string,
    path: string,
    options?: { staged?: boolean; baseRef?: string }
  ): Promise<FileDiffResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      // Use unique key per file to avoid race conditions when multiple files are fetched
      const key = `get_file_diff_${path}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'get_file_diff',
        directory,
        path,
        ...(options?.staged !== undefined && { staged: options.staged }),
        ...(options?.baseRef && { base_ref: options.baseRef }),
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get file diff timed out'));
        }
      }, 10000);
    });
  }, []);

  // Get all files changed between base ref and current working state (PR-like diff)
  const sendGetBranchDiffFiles = useCallback((
    directory: string,
    baseRef?: string
  ): Promise<BranchDiffFilesResult> => {
    const key = `get_branch_diff_files_${directory}`;
    const inFlightRequest = branchDiffInFlightRef.current.get(key);
    if (inFlightRequest) {
      return inFlightRequest;
    }

    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('WebSocket not connected'));
    }

    const request = new Promise<BranchDiffFilesResult>((resolve, reject) => {
      pendingActionsRef.current.set(key, {
        resolve: (result) => {
          branchDiffInFlightRef.current.delete(key);
          resolve(result as BranchDiffFilesResult);
        },
        reject: (error) => {
          branchDiffInFlightRef.current.delete(key);
          reject(error);
        },
      });

      ws.send(JSON.stringify({
        cmd: 'get_branch_diff_files',
        directory,
        ...(baseRef && { base_ref: baseRef }),
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          branchDiffInFlightRef.current.delete(key);
          reject(new Error('Get branch diff files timed out'));
        }
      }, 30000); // 30s timeout for potentially large diffs
    });

    branchDiffInFlightRef.current.set(key, request);
    return request;
  }, []);

  // Get repo info
  const getRepoInfo = useCallback((repo: string): Promise<RepoInfoResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `repo_info_${repo}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'get_repo_info', repo }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('get_repo_info timeout'));
        }
      }, 30000);
    });
  }, []);

  // Get review state for a repo/branch
  const getReviewState = useCallback((repoPath: string, branch: string): Promise<ReviewStateResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'get_review_state';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'get_review_state', repo_path: repoPath, branch }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get review state timed out'));
        }
      }, 10000);
    });
  }, []);

  // Mark a file as viewed/unviewed in a review
  const markFileViewed = useCallback((reviewId: string, filepath: string, viewed: boolean): Promise<MarkFileViewedResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'mark_file_viewed';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'mark_file_viewed', review_id: reviewId, filepath, viewed }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Mark file viewed timed out'));
        }
      }, 10000);
    });
  }, []);

  // Add a comment to a review
  const sendAddComment = useCallback((
    reviewId: string,
    filepath: string,
    lineStart: number,
    lineEnd: number,
    content: string
  ): Promise<AddCommentResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'add_comment';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'add_comment',
        review_id: reviewId,
        filepath,
        line_start: lineStart,
        line_end: lineEnd,
        content,
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Add comment timed out'));
        }
      }, 30000);
    });
  }, []);

  // Update a comment's content
  const sendUpdateComment = useCallback((commentId: string, content: string): Promise<CommentActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'update_comment';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'update_comment',
        comment_id: commentId,
        content,
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Update comment timed out'));
        }
      }, 30000);
    });
  }, []);

  // Resolve or unresolve a comment
  const sendResolveComment = useCallback((commentId: string, resolved: boolean): Promise<CommentActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'resolve_comment';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'resolve_comment',
        comment_id: commentId,
        resolved,
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Resolve comment timed out'));
        }
      }, 30000);
    });
  }, []);

  // Mark or unmark a comment as won't fix
  const sendWontFixComment = useCallback((commentId: string, wontFix: boolean): Promise<CommentActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'wont_fix_comment';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'wont_fix_comment',
        comment_id: commentId,
        wont_fix: wontFix,
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Won\'t fix comment timed out'));
        }
      }, 30000);
    });
  }, []);

  // Delete a comment
  const sendDeleteComment = useCallback((commentId: string): Promise<CommentActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'delete_comment';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'delete_comment',
        comment_id: commentId,
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Delete comment timed out'));
        }
      }, 30000);
    });
  }, []);

  // Get comments for a review, optionally filtered by filepath
  const sendGetComments = useCallback((reviewId: string, filepath?: string): Promise<GetCommentsResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = 'get_comments';
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'get_comments',
        review_id: reviewId,
        ...(filepath && { filepath }),
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get comments timed out'));
        }
      }, 30000);
    });
  }, []);

  // Reviewer agent methods
  const sendStartReview = useCallback((
    reviewId: string,
    repoPath: string,
    branch: string,
    baseBranch: string
  ): void => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      console.error('[Daemon] Cannot start review: WebSocket not connected');
      return;
    }

    ws.send(JSON.stringify({
      cmd: 'start_review',
      review_id: reviewId,
      repo_path: repoPath,
      branch: branch,
      base_branch: baseBranch,
    }));
  }, []);

  const sendCancelReview = useCallback((reviewId: string): void => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      console.error('[Daemon] Cannot cancel review: WebSocket not connected');
      return;
    }

    ws.send(JSON.stringify({
      cmd: 'cancel_review',
      review_id: reviewId,
    }));
  }, []);

  const clearWarnings = useCallback(() => {
    setWarnings([]);
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    ws.send(JSON.stringify({ cmd: 'clear_warnings' }));
  }, []);

  return {
    isConnected: wsRef.current?.readyState === WebSocket.OPEN,
    connectionError,
    hasReceivedInitialState,
    settings: settingsRef.current,
    rateLimit,
    warnings,
    clearWarnings,
    retryConnection,
    sendPRAction,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendRefreshPRs,
    sendFetchPRDetails,
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
    sendEnsureRepo,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendSessionVisualized,
    sendGetFileDiff,
    sendGetBranchDiffFiles,
    getRepoInfo,
    getReviewState,
    markFileViewed,
    sendAddComment,
    sendUpdateComment,
    sendResolveComment,
    sendWontFixComment,
    sendDeleteComment,
    sendGetComments,
    sendStartReview,
    sendCancelReview,
  };
}
