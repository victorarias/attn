import { useEffect, useRef, useCallback, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '@tauri-apps/api/core';
import type {
  Session as GeneratedSession,
  ChiefOfStaffDispatch as GeneratedChiefOfStaffDispatch,
  Workspace as GeneratedWorkspaceSnapshot,
  PR as GeneratedPR,
  Worktree as GeneratedWorktree,
  PluginInfo as GeneratedPluginInfo,
  PluginIssue as GeneratedPluginIssue,
  GitOperation as GeneratedGitOperation,
  Endpoint as GeneratedEndpoint,
  RepoState as GeneratedRepoState,
  AuthorState as GeneratedAuthorState,
  WebSocketEvent as GeneratedWebSocketEvent,
  RecentLocation as GeneratedRecentLocation,
  Comment as GeneratedComment,
  ReviewLoopRun as GeneratedReviewLoopRun,
  ReviewLoopInteraction as GeneratedReviewLoopInteraction,
  WarningElement as GeneratedWarning,
  WorkspaceContext as GeneratedWorkspaceContext,
  SessionState,
  PRRole,
  HeatState,
} from '../types/generated';
import {
  emitPtyEvent,
  setPtyBackend,
  type PtyAttachArgs,
  type PtyAttachPolicy,
  type PtySpawnArgs,
} from '../pty/bridge';
import {
  classifyAttachReplay,
  createAttachRequestContext,
  enqueuePendingAttachOutput,
  planAttachResultEffects,
  planAttachedRuntimeGeometry,
  planLivePtyOutput,
  type AttachResultData,
  type AttachRequestContext,
} from '../pty/attachPlanning';
import {
  normalizeAttachPolicy,
  spawnPtyRuntime,
} from '../pty/runtimeLifecycle';
import { createPtyTransportState } from '../pty/transportState';
import { parseLayoutJSON, tileContentKey, tileIdsFromLayoutJSON, type TerminalDockEdge, type TileContentState } from '../types/workspace';
import { isSuspiciousTerminalSize } from '../utils/terminalDebug';
import { collectWorkspaceLayoutDiagnostics } from '../utils/workspaceDiagnostics';
import { recordDiag, recordLayout } from '../utils/terminalDiagnosticsLog';
import { recordPtyCommand, recordWsBinaryPtyOutput, recordWsJsonParse } from '../utils/ptyPerf';
import { decodeBinaryPtyFrame } from '../pty/binaryPtyFrame';
import { resolveDaemonWebSocketURL, type DaemonEndpointProfile } from '../utils/daemonEndpoint';
import { BUILD_PROFILE, daemonProfileMatches, fetchDaemonHealthProfile, profileMismatchMessage } from '../utils/buildProfile';
import { controlBrowserHost, serializeBrowserControlResultMessage } from '../browser/host';

// Short names for daemon payloads used throughout the app.
export type DaemonSession = GeneratedSession;
export type ChiefOfStaffDispatch = GeneratedChiefOfStaffDispatch;
export type DaemonWorkspace = GeneratedWorkspaceSnapshot;
export type DaemonPR = GeneratedPR;
export type DaemonWorktree = GeneratedWorktree;
export type DaemonPlugin = GeneratedPluginInfo;
export type DaemonPluginIssue = GeneratedPluginIssue;
export type DaemonGitOperation = GeneratedGitOperation;
export type DaemonEndpoint = GeneratedEndpoint;
export type RepoState = GeneratedRepoState;
export type AuthorState = GeneratedAuthorState;
export type RecentLocation = GeneratedRecentLocation;
export type ReviewLoopState = GeneratedReviewLoopRun;
export type ReviewLoopInteraction = GeneratedReviewLoopInteraction;
export type DaemonSettings = Record<string, string>;
export type DaemonWarning = GeneratedWarning;
export type DaemonWorkspaceContext = GeneratedWorkspaceContext;
export interface DirectoryEntry {
  name: string;
  path: string;
}
export interface PathInspection {
  input_path: string;
  resolved_path: string;
  home_path?: string;
  exists: boolean;
  is_directory: boolean;
  repo_root?: string;
}
export type { DaemonEndpointProfile };

// Re-export enums and useful types
export { SessionState, PRRole, HeatState };

// Extended WebSocketEvent with action result fields (generated allows extra properties)
type WebSocketEvent = GeneratedWebSocketEvent & {
  id?: string;
  endpoint?: GeneratedEndpoint;
  endpoints?: GeneratedEndpoint[];
  workspace?: GeneratedWorkspaceSnapshot;
  workspace_id?: string;
  source_workspace_id?: string;
  target_workspace_id?: string;
  leaf_id?: string;
  final_leaf_id?: string;
  split_id?: string;
  tile_id?: string;
  data?: string;
  seq?: number;
  scrollback?: string;
  scrollback_truncated?: boolean;
  replay_segments?: Array<{ cols: number; rows: number; data: string }>;
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
  name?: string;
  priority?: number;
  endpoint_id?: string;
  request_id?: string;
  selector?: string;
  home_path?: string;
  directory?: string;
  input_path?: string;
  entries?: DirectoryEntry[];
  inspection?: PathInspection;
  plugins?: DaemonPlugin[];
  issues?: DaemonPluginIssue[];
  dispatches?: ChiefOfStaffDispatch[];
  github_hosts?: string[];
  contexts?: DaemonWorkspaceContext[];
  // Legacy review event fields
  review_id?: string;
  session_id?: string;
  loop_id?: string;
  content?: string;
  review_loop_run?: ReviewLoopState;
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
export const PROTOCOL_VERSION = '100';
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
  endpoint_id?: string;
  error?: string;
  forceable?: boolean;
  reason_kind?: string;
}

export type DeleteWorktreeOptions = {
  force?: boolean;
};

export type WorktreeActionError = Error & {
  forceable?: boolean;
  reason_kind?: string;
};

interface PluginActionResult {
  success: boolean;
  name?: string;
  error?: string;
}

export interface PluginListResult {
  plugins: DaemonPlugin[];
  issues: DaemonPluginIssue[];
}

// Read-only seed for an observer (grid tile): the session's current rendered
// screen plus the sequence watermark to dedup the live firehose against.
export interface ScreenSnapshotResult {
  // base64 ANSI repaint of the visible frame, or undefined if no fresh frame
  // exists yet (e.g. the session has produced no output).
  screenSnapshot?: string;
  screenCols?: number;
  screenRows?: number;
  lastSeq: number;
}

interface RecentLocationsResult {
  success: boolean;
  locations: RecentLocation[];
  endpoint_id?: string;
  home_path?: string;
  error?: string;
}

export interface BrowseDirectoryResult {
  success: boolean;
  input_path: string;
  directory: string;
  entries: DirectoryEntry[];
  endpoint_id?: string;
  home_path?: string;
  error?: string;
}

export interface InspectPathResult {
  success: boolean;
  inspection?: PathInspection;
  endpoint_id?: string;
  error?: string;
}

interface FetchRemotesResult {
  success: boolean;
  error?: string;
}

interface EnsureRepoResult {
  success: boolean;
  cloned?: boolean;
  error?: string;
}

interface EndpointActionResult {
  success: boolean;
  endpoint_id?: string;
  error?: string;
}

interface SpawnResult {
  success: boolean;
  error?: string;
}

type AttachResult = AttachResultData & {
  success: boolean;
  error?: string;
  screen_cursor_x?: number;
  screen_cursor_y?: number;
  screen_cursor_visible?: boolean;
  pid?: number;
  running?: boolean;
};

interface RepoInfo {
  repo: string;
  current_branch: string;
  current_commit_hash: string;
  current_commit_time: string;
  default_branch: string;
  worktrees: DaemonWorktree[];
}

interface RepoInfoResult {
  success: boolean;
  info?: RepoInfo;
  endpoint_id?: string;
  error?: string;
}

interface ReviewLoopActionResult {
  success: boolean;
  state: ReviewLoopState | null;
}

interface WorkspaceActionResult {
  success: boolean;
  error?: string;
  final_leaf_id?: string;
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
  mode?: string;
  limited?: boolean;
  limited_reason?: string;
  duration_ms?: number;
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

export interface SessionExitInfo {
  id: string;
  exitCode: number;
  signal?: string;
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onChiefOfStaffDispatchesUpdate?: (dispatches: ChiefOfStaffDispatch[]) => void;
  onWorkspacesUpdate: (workspaces: DaemonWorkspace[]) => void;
  onPRsUpdate: (prs: DaemonPR[]) => void;
  onEndpointsUpdate?: (endpoints: DaemonEndpoint[]) => void;
  onPluginsUpdate?: (plugins: DaemonPlugin[], issues: DaemonPluginIssue[]) => void;
  onGitHubHostsUpdate?: (hosts: string[]) => void;
  onReposUpdate: (repos: RepoState[]) => void;
  onAuthorsUpdate: (authors: AuthorState[]) => void;
  onWorktreesUpdate?: (worktrees: DaemonWorktree[]) => void;
  onSettingsUpdate?: (settings: DaemonSettings) => void;
  onSettingError?: (message: string) => void;
  onGitStatusUpdate?: (status: GitStatusUpdate) => void;
  onReviewLoopUpdate?: (state: ReviewLoopState | null) => void;
  onSessionExited?: (info: SessionExitInfo) => void;
  endpoint?: DaemonEndpointProfile;
  wsUrl?: string;
}

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

function workspaceRuntimeIDs(workspaces: DaemonWorkspace[]): Set<string> {
  const ids = new Set<string>();
  for (const workspace of workspaces) {
    for (const pane of workspace.layout?.panes || []) {
      if (typeof pane.runtime_id === 'string' && pane.runtime_id.length > 0) {
        ids.add(pane.runtime_id);
      }
    }
  }
  return ids;
}

function pruneWorkspacesBySessions(
  _sessions: DaemonSession[],
  workspaces: DaemonWorkspace[],
): DaemonWorkspace[] {
  return workspaces;
}

function invalidateWorkspaceLayoutsForSession(
  workspaces: DaemonWorkspace[],
  sessionID: string,
): DaemonWorkspace[] {
  let changed = false;
  const nextWorkspaces = workspaces.map((workspace) => {
    const referencesSession = (workspace.layout?.panes || []).some(
      (pane) => pane.session_id === sessionID || pane.runtime_id === sessionID,
    );
    if (!referencesSession) {
      return workspace;
    }
    changed = true;
    return { ...workspace, layout: undefined };
  });
  return changed ? nextWorkspaces : workspaces;
}

function workspacesIncludeRuntimeID(workspaces: DaemonWorkspace[], runtimeID: string): boolean {
  for (const workspace of workspaces) {
    for (const pane of workspace.layout?.panes || []) {
      if (pane.runtime_id === runtimeID) {
        return true;
      }
    }
  }
  return false;
}

function upsertEndpointByID(endpoints: DaemonEndpoint[], endpoint: DaemonEndpoint): DaemonEndpoint[] {
  const index = endpoints.findIndex((entry) => entry.id === endpoint.id);
  if (index === -1) {
    return [...endpoints, endpoint];
  }
  const updated = [...endpoints];
  updated[index] = endpoint;
  return updated;
}

function upsertWorkspaceByID(workspaces: DaemonWorkspace[], workspace: DaemonWorkspace): DaemonWorkspace[] {
  const index = workspaces.findIndex((entry) => entry.id === workspace.id);
  if (index === -1) {
    return [...workspaces, workspace];
  }
  const updated = [...workspaces];
  const existing = updated[index];
  updated[index] = {
    ...existing,
    ...workspace,
    layout: workspace.layout ?? existing.layout,
  };
  return updated;
}

function workspaceActionKey(action: string, workspaceId: string, entityId?: string, requestId?: string): string {
  return requestId
    ? `workspace:${action}:${workspaceId}:request:${requestId}`
    : `workspace:${action}:${workspaceId}:${entityId || ''}`;
}

function isValidWorkspaceActionResult(data: WebSocketEvent): data is WebSocketEvent & {
  action: string;
  workspace_id: string;
} {
  return Boolean(data.action && data.workspace_id);
}

function pruneTileContentsForWorkspace(
  contents: Record<string, TileContentState>,
  workspaceId: string,
  activeTileIds: string[] = [],
): Record<string, TileContentState> {
  const prefix = `${workspaceId}::`;
  const activeKeys = new Set(activeTileIds.map((tileId) => tileContentKey(workspaceId, tileId)));
  let changed = false;
  const next: Record<string, TileContentState> = {};
  for (const [key, value] of Object.entries(contents)) {
    if (key.startsWith(prefix) && !activeKeys.has(key)) {
      changed = true;
      continue;
    }
    next[key] = value;
  }
  return changed ? next : contents;
}

function pruneTileContentsForWorkspaces(
  contents: Record<string, TileContentState>,
  workspaces: DaemonWorkspace[],
): Record<string, TileContentState> {
  const activeKeys = new Set<string>();
  for (const workspace of workspaces) {
    for (const tileId of tileIdsFromLayoutJSON(workspace.layout?.layout_json || '')) {
      activeKeys.add(tileContentKey(workspace.id, tileId));
    }
  }
  let changed = false;
  const next: Record<string, TileContentState> = {};
  for (const [key, value] of Object.entries(contents)) {
    if (!activeKeys.has(key)) {
      changed = true;
      continue;
    }
    next[key] = value;
  }
  return changed ? next : contents;
}

function requestTileContentsForWorkspaces(ws: WebSocket, workspaces: DaemonWorkspace[]) {
  if (ws.readyState !== WebSocket.OPEN) return;
  for (const workspace of workspaces) {
    for (const tileId of tileIdsFromLayoutJSON(workspace.layout?.layout_json || '', 'markdown')) {
      ws.send(JSON.stringify({ cmd: 'workspace_tile_content_get', workspace_id: workspace.id, tile_id: tileId }));
    }
  }
}

const ATTACH_RETRY_TIMEOUT_MS = 3_000;
const ATTACH_RETRY_DELAY_MS = 150;
const GIT_METADATA_TIMEOUT_MS = 30 * 60_000;
const GIT_DIFF_TIMEOUT_MS = 10 * 60_000;
const GIT_WORKTREE_TIMEOUT_MS = 30 * 60_000;
const GIT_NETWORK_TIMEOUT_MS = 30 * 60_000;
const GIT_CLONE_TIMEOUT_MS = 90 * 60_000;
const GITHUB_REFRESH_TIMEOUT_MS = 5 * 60_000;
const WORKSPACE_SESSIONS_CAPABILITY = 'workspace_sessions';
const BROWSER_HOST_CAPABILITY = 'browser_host';
const BINARY_PTY_OUTPUT_CAPABILITY = 'binary_pty_output';

export function isTransientAttachError(error: unknown): boolean {
  const message = error instanceof Error ? error.message.toLowerCase() : String(error).toLowerCase();
  if (message.includes('websocket not connected') || message.includes('daemon is recovering')) {
    return false;
  }
  if (message.includes('dial unix') && message.includes('no such file or directory')) {
    return true;
  }
  return (
    message.includes('connection refused') ||
    message.includes('resource temporarily unavailable') ||
    message.includes('broken pipe') ||
    message.includes('i/o timeout')
  );
}

function waitForAttachRetry(delayMs: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, delayMs);
  });
}

export async function retryTransientAttachRequest<T>(
  request: () => Promise<T>,
  options?: {
    timeoutMs?: number;
    delayMs?: number;
    wait?: (delayMs: number) => Promise<void>;
    onRetry?: (attempt: number, error: unknown, elapsedMs: number) => void;
  },
): Promise<T> {
  const timeoutMs = options?.timeoutMs ?? ATTACH_RETRY_TIMEOUT_MS;
  const delayMs = options?.delayMs ?? ATTACH_RETRY_DELAY_MS;
  const wait = options?.wait ?? waitForAttachRetry;
  const startedAt = Date.now();
  let attempt = 0;

  for (;;) {
    attempt += 1;
    try {
      return await request();
    } catch (error) {
      const elapsedMs = Date.now() - startedAt;
      if (!isTransientAttachError(error) || elapsedMs >= timeoutMs) {
        throw error;
      }
      options?.onRetry?.(attempt, error, elapsedMs);
      await wait(delayMs);
    }
  }
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
//    - sendUnregisterSession: Session cleanup acknowledgment
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
  onChiefOfStaffDispatchesUpdate,
  onWorkspacesUpdate,
  onPRsUpdate,
  onEndpointsUpdate,
  onPluginsUpdate,
  onGitHubHostsUpdate,
  onReposUpdate,
  onAuthorsUpdate,
  onWorktreesUpdate,
  onSettingsUpdate,
  onSettingError,
  onGitStatusUpdate,
  onReviewLoopUpdate,
  onSessionExited,
  endpoint,
  wsUrl,
}: UseDaemonSocketOptions) {
  const resolvedWsUrl = resolveDaemonWebSocketURL({ endpoint, wsUrl });
  const wsRef = useRef<WebSocket | null>(null);
  const sessionsRef = useRef<DaemonSession[]>([]);
  const workspacesRef = useRef<DaemonWorkspace[]>([]);
  const prsRef = useRef<DaemonPR[]>([]);
  const endpointsRef = useRef<DaemonEndpoint[]>([]);
  const reposRef = useRef<RepoState[]>([]);
  const authorsRef = useRef<AuthorState[]>([]);
  const settingsRef = useRef<DaemonSettings>({});
  const callbacksRef = useRef({
    onSessionsUpdate,
    onChiefOfStaffDispatchesUpdate,
    onWorkspacesUpdate,
    onPRsUpdate,
    onEndpointsUpdate,
    onPluginsUpdate,
    onGitHubHostsUpdate,
    onReposUpdate,
    onAuthorsUpdate,
    onWorktreesUpdate,
    onSettingsUpdate,
    onSettingError,
    onGitStatusUpdate,
    onReviewLoopUpdate,
    onSessionExited,
  });
  callbacksRef.current = {
    onSessionsUpdate,
    onChiefOfStaffDispatchesUpdate,
    onWorkspacesUpdate,
    onPRsUpdate,
    onEndpointsUpdate,
    onPluginsUpdate,
    onGitHubHostsUpdate,
    onReposUpdate,
    onAuthorsUpdate,
    onWorktreesUpdate,
    onSettingsUpdate,
    onSettingError,
    onGitStatusUpdate,
    onReviewLoopUpdate,
    onSessionExited,
  };
  const reconnectTimeoutRef = useRef<number | null>(null);
  const reconnectDelayRef = useRef<number>(1000); // Start with 1s, exponential backoff
  const pendingActionsRef = useRef<Map<string, { resolve: (result: any) => void; reject: (error: Error) => void }>>(new Map());
  const requestSequenceRef = useRef(0);
  const pendingOutboundCommandsRef = useRef<string[]>([]);
  const recoveryNoticeTimeoutRef = useRef<number | null>(null);
  const gitStatusSubscriptionRef = useRef<string | null>(null);
  // Branch diff result messages are keyed by directory, not request id. Keep a
  // per-directory pending request so duplicate callers share the same result;
  // the daemon coordinator owns the actual git/process coalescing and snapshot.
  const branchDiffInFlightRef = useRef<Map<string, Promise<BranchDiffFilesResult>>>(new Map());
  const ptyTransportRef = useRef(createPtyTransportState<AttachRequestContext>());
  const canceledAttachIdsRef = useRef(new Set<string>());
  const pendingSessionVisualizedRef = useRef<Set<string>>(new Set());
  const selectedSessionRef = useRef<string | null>(null);
  const selectedWorkspaceRef = useRef<string | null>(null);
  const daemonInstanceIDRef = useRef<string>('');
  const hasReceivedInitialStateRef = useRef(false);
  // Once we detect a profile mismatch, we refuse to operate forever — the
  // user must quit and launch the matching app. Never clears inside the
  // session.
  const profileMismatchRef = useRef<boolean>(false);
  const profileCheckedRef = useRef<boolean>(false);
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [hasReceivedInitialState, setHasReceivedInitialState] = useState(false);
  const [rateLimit, setRateLimit] = useState<RateLimitState | null>(null);
  const [warnings, setWarnings] = useState<DaemonWarning[]>([]);
  const [gitOperations, setGitOperations] = useState<Record<string, DaemonGitOperation>>({});
  // Daemon-served content for docked tiles (markdown files), keyed by
  // tileContentKey. Updated by workspace_tile_content events (reply + live reload).
  const [tileContents, setTileContents] = useState<Record<string, TileContentState>>({});

  // Circuit breaker state for reconnect storms
  const reconnectAttemptsRef = useRef(0);
  const circuitOpenRef = useRef(false);
  const circuitResetTimeoutRef = useRef<number | null>(null);

  const MAX_RECONNECTS_BEFORE_PAUSE = 8;
  const MAX_RECONNECT_DELAY_MS = 5000;
  const RECOVERY_NOTICE = 'Daemon is recovering PTY sessions. Please retry in a moment.';
  const DAEMON_RESTART_NOTICE = 'Restarting daemon...';
  const daemonRestartInProgressRef = useRef(false);

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

  const pruneAttachedPtySessions = useCallback((sessions: DaemonSession[], workspaces: DaemonWorkspace[]) => {
    const attachableIDs = new Set<string>(sessions.map((session) => session.id));
    for (const runtimeID of workspaceRuntimeIDs(workspaces)) {
      attachableIDs.add(runtimeID);
    }
    ptyTransportRef.current.pruneDetachedRuntimes(attachableIDs);
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
      case 'kill_session':
        rejectPendingByPredicate((key) => key.startsWith('pty_kill_'), error);
        return;
      case 'unregister':
        rejectPendingByPredicate((key) => key.startsWith('unregister:'), error);
        return;
      case 'unregister_workspace':
        rejectPendingByPredicate((key) => key.startsWith('unregister_workspace:'), error);
        return;
      case 'wake_dispatch_agent':
        rejectPendingByPredicate((key) => key.startsWith('wake_dispatch_agent:'), error);
        return;
      case 'workspace_layout_add_session_pane':
      case 'workspace_layout_close_pane':
      case 'workspace_layout_focus_pane':
      case 'workspace_layout_rename_pane':
      case 'workspace_layout_set_split_ratio':
      case 'workspace_layout_dock_tile':
      case 'workspace_layout_undock_tile':
      case 'workspace_layout_update_tile':
        rejectPendingByPredicate((key) => key.startsWith(`workspace:${cmd}:`), error);
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
      case 'get_recent_locations':
        rejectPendingByPredicate((key) => key.startsWith('get_recent_locations_'), error);
        return;
      case 'browse_directory':
        rejectPendingByPredicate((key) => key.startsWith('browse_directory_'), error);
        return;
      case 'inspect_path':
        rejectPendingByPredicate((key) => key.startsWith('inspect_path_'), error);
        return;
      case 'create_worktree':
        rejectPendingByPredicate((key) => key.startsWith('worktree_create_worktree_result_'), error);
        return;
      case 'delete_worktree':
        rejectPendingByPredicate((key) => key.startsWith('worktree_delete_worktree_result_'), error);
        return;
      case 'delete_branch':
        rejectPendingByPredicate((key) => key.startsWith('delete_branch_'), error);
        return;
      default:
        rejectPendingByPredicate((key) => key === cmd, error);
    }
  }, [rejectPendingByPredicate]);

  const hasPendingEndpointAction = useCallback(() => {
    for (const key of pendingActionsRef.current.keys()) {
      if (key.startsWith('endpoint_action:')) {
        return true;
      }
    }
    return false;
  }, []);

  const ensureDaemonRunning = useCallback(async () => {
    if (!isTauri()) {
      return;
    }
    try {
      await invoke('ensure_daemon');
    } catch (err) {
      console.error('[Daemon] Failed to ensure daemon is running:', err);
    }
  }, []);

  const flushQueuedCommands = useCallback((ws: WebSocket | null) => {
    if (!ws || ws.readyState !== WebSocket.OPEN || pendingOutboundCommandsRef.current.length === 0) {
      return;
    }
    for (const serialized of pendingOutboundCommandsRef.current) {
      ws.send(serialized);
    }
    pendingOutboundCommandsRef.current = [];
  }, []);

  const nextRequestID = useCallback((prefix: string) => {
    requestSequenceRef.current += 1;
    return `${prefix}:${Date.now()}:${requestSequenceRef.current}`;
  }, []);

  const sendOrQueueCommand = useCallback((payload: Record<string, unknown>, options?: { waitForInitialState?: boolean }) => {
    const serialized = JSON.stringify(payload);
    if (options?.waitForInitialState && !hasReceivedInitialStateRef.current) {
      pendingOutboundCommandsRef.current.push(serialized);
      return;
    }
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(serialized);
      return;
    }
    pendingOutboundCommandsRef.current.push(serialized);
  }, []);

  const connect = useCallback(async () => {
    if (wsRef.current?.readyState === WebSocket.OPEN || wsRef.current?.readyState === WebSocket.CONNECTING) return;
    if (profileMismatchRef.current) {
      // Already detected a mismatch in a previous connect attempt; stay
      // stopped. Reconnect would just bounce off the same check.
      return;
    }

    await ensureDaemonRunning();

    // Verify the daemon's profile matches this build before opening the
    // WebSocket. Only meaningful for non-default builds: the dev bundle
    // (BUILD_PROFILE="dev") refuses to operate on a daemon that reports
    // anything other than "dev". The default/prod build skips the check
    // — a default frontend reaching a dev daemon would need a deliberate
    // port override, and the initial_state protocol-version handshake
    // over the WS still catches the common failure modes. Skipping also
    // keeps existing WebSocket mocks in tests sync with the connect path.
    if (BUILD_PROFILE !== '' && !profileCheckedRef.current) {
      try {
        const health = await fetchDaemonHealthProfile(resolvedWsUrl);
        if (!daemonProfileMatches(health.profile)) {
          profileMismatchRef.current = true;
          setConnectionError(profileMismatchMessage(health.profile));
          circuitOpenRef.current = true;
          return;
        }
        profileCheckedRef.current = true;
      } catch (err) {
        // Health fetch failed (daemon still coming up, network hiccup,
        // older daemon without /health). Don't block the WS connect —
        // the initial_state handshake will surface hard errors, and a
        // real mismatch will be caught on the next try.
        console.warn('[Daemon] profile pre-check failed, proceeding without it:', err);
      }
    }

    let browserHostToken = '';
    if (isTauri()) {
      try {
        browserHostToken = await invoke<string>('get_browser_host_token');
      } catch (error) {
        console.warn('[Daemon] Browser host authentication is unavailable:', error);
      }
    }

    const ws = new WebSocket(resolvedWsUrl);
    // Live PTY output arrives as binary frames (see BINARY_PTY_OUTPUT_CAPABILITY).
    ws.binaryType = 'arraybuffer';

    // Shared by the JSON pty_output event and the binary frame path: queue
    // while an attach for the session is in flight, otherwise dedup by seq and
    // emit to the pane runtime.
    const handleLivePtyOutput = (id: string, seq: number | undefined, payload: string | Uint8Array) => {
      const attachKey = `pty_attach_${id}`;
      if (pendingActionsRef.current.has(attachKey)) {
        // Attach replay is emitted before this queue is drained.
        // Ghostty then serializes those emitted writes in order.
        const queued = enqueuePendingAttachOutput(
          ptyTransportRef.current.getQueuedAttachOutputs(id) || [],
          { data: payload, seq },
          MAX_PENDING_ATTACH_OUTPUTS,
        );
        ptyTransportRef.current.setQueuedAttachOutputs(id, queued);
        return;
      }
      const outputPlan = planLivePtyOutput({
        incomingSeq: typeof seq === 'number' ? seq : undefined,
        lastSeq: ptyTransportRef.current.getLastSeq(id),
      });
      if (outputPlan.shouldDropAsStale) {
        return;
      }
      if (typeof outputPlan.nextSeq === 'number') {
        ptyTransportRef.current.setLastSeq(id, outputPlan.nextSeq);
      }
      emitPtyEvent({ event: 'data', id, data: payload, seq });
    };

    ws.onopen = () => {
      console.log('[Daemon] WebSocket connected');
      daemonRestartInProgressRef.current = false;
      setConnectionError(null);
      reconnectDelayRef.current = 1000; // Reset to 1s on successful connect
      reconnectAttemptsRef.current = 0;
      circuitOpenRef.current = false;
      if (circuitResetTimeoutRef.current) {
        clearTimeout(circuitResetTimeoutRef.current);
        circuitResetTimeoutRef.current = null;
      }

      // Identify ourselves first thing. Sending hello is useful for
      // daemon-side diagnostics (client kind/version in logs).
      ws.send(
        JSON.stringify({
          cmd: 'client_hello',
          client_kind: 'tauri-app',
          version: `protocol-${PROTOCOL_VERSION}`,
          capabilities: [
            WORKSPACE_SESSIONS_CAPABILITY,
            BINARY_PTY_OUTPUT_CAPABILITY,
            ...(browserHostToken ? [BROWSER_HOST_CAPABILITY] : []),
          ],
          browser_host_token: browserHostToken || undefined,
        }),
      );

      if (gitStatusSubscriptionRef.current) {
        ws.send(JSON.stringify({ cmd: 'subscribe_git_status', directory: gitStatusSubscriptionRef.current }));
      }

      if (pendingSessionVisualizedRef.current.size > 0) {
        for (const sessionID of pendingSessionVisualizedRef.current) {
          ws.send(JSON.stringify({ cmd: 'session_visualized', id: sessionID }));
        }
        pendingSessionVisualizedRef.current.clear();
      }
      if (selectedSessionRef.current) {
        ws.send(JSON.stringify({ cmd: 'session_selected', id: selectedSessionRef.current }));
      }
      if (selectedWorkspaceRef.current) {
        ws.send(JSON.stringify({ cmd: 'workspace_selected', workspace_id: selectedWorkspaceRef.current }));
      }
    };

    ws.onmessage = (event) => {
      try {
        if (event.data instanceof ArrayBuffer) {
          const decodeStartedAt = performance.now();
          const frame = decodeBinaryPtyFrame(event.data);
          if (!frame) {
            console.error('[Daemon] Dropping undecodable binary frame', event.data.byteLength);
            return;
          }
          recordWsBinaryPtyOutput(
            event.data.byteLength,
            frame.data.byteLength,
            performance.now() - decodeStartedAt,
            { runtimeId: frame.id, seq: frame.seq },
          );
          handleLivePtyOutput(frame.id, frame.seq, frame.data);
          return;
        }
        const rawText = typeof event.data === 'string' ? event.data : '';
        const parseStartedAt = performance.now();
        const data: WebSocketEvent = JSON.parse(event.data);
        recordWsJsonParse(
          rawText.length,
          performance.now() - parseStartedAt,
          data.event,
          data.event === 'pty_output' && typeof data.data === 'string' ? data.data.length : 0,
          {
            runtimeId: data.id ?? null,
            seq: typeof data.seq === 'number' ? data.seq : null,
          },
        );

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
              ptyTransportRef.current.clearStreamCaches();
            }
            daemonInstanceIDRef.current = data.daemon_instance_id || '';
            // Check protocol version on initial connection
            if (data.protocol_version && data.protocol_version !== PROTOCOL_VERSION) {
              console.error(`[Daemon] Protocol version mismatch: daemon=${data.protocol_version}, client=${PROTOCOL_VERSION}`);
              const daemonVersion = Number(data.protocol_version);
              const clientVersion = Number(PROTOCOL_VERSION);
              const activeSessions = data.sessions?.length || 0;
              if (!Number.isNaN(daemonVersion) && !Number.isNaN(clientVersion) && daemonVersion < clientVersion) {
                if (isTauri()) {
                  setConnectionError(DAEMON_RESTART_NOTICE);
                  if (!daemonRestartInProgressRef.current) {
                    daemonRestartInProgressRef.current = true;
                    console.log(`[Daemon] Restarting older daemon ${data.protocol_version} to match app protocol ${PROTOCOL_VERSION}`);
                    void invoke('ensure_daemon').catch((err) => {
                      console.error('[Daemon] Failed to restart daemon after protocol mismatch:', err);
                      daemonRestartInProgressRef.current = false;
                      setConnectionError(
                        `New daemon version available. Restart when ready (${activeSessions} active sessions may be lost). daemon v${data.protocol_version}, app v${PROTOCOL_VERSION}`
                      );
                      circuitOpenRef.current = true;
                    });
                  }
                  ws.close();
                  return;
                }
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
            callbacksRef.current.onSessionsUpdate(nextSessions);
            callbacksRef.current.onChiefOfStaffDispatchesUpdate?.(data.chief_of_staff_dispatches || []);
            const nextWorkspaces = data.workspaces || [];
            workspacesRef.current = nextWorkspaces;
            callbacksRef.current.onWorkspacesUpdate(nextWorkspaces);
            setTileContents((prev) => pruneTileContentsForWorkspaces(prev, nextWorkspaces));
            pruneAttachedPtySessions(nextSessions, nextWorkspaces);
            const nextPRs = data.prs || [];
            prsRef.current = nextPRs;
            callbacksRef.current.onPRsUpdate(nextPRs);

            const nextEndpoints = data.endpoints || [];
            endpointsRef.current = nextEndpoints;
            callbacksRef.current.onEndpointsUpdate?.(nextEndpoints);

            const nextRepos = data.repos || [];
            reposRef.current = nextRepos;
            callbacksRef.current.onReposUpdate(nextRepos);

            const nextAuthors = data.authors || [];
            authorsRef.current = nextAuthors;
            callbacksRef.current.onAuthorsUpdate(nextAuthors);

            callbacksRef.current.onGitHubHostsUpdate?.(data.github_hosts || []);

            const nextSettings = data.settings || {};
            settingsRef.current = nextSettings;
            callbacksRef.current.onSettingsUpdate?.(nextSettings);
            const nextWarnings = data.warnings || [];
            setWarnings(nextWarnings);
            if (nextWarnings.length > 0 && ws.readyState === WebSocket.OPEN) {
              ws.send(JSON.stringify({ cmd: 'clear_warnings' }));
            }
            hasReceivedInitialStateRef.current = true;
            setHasReceivedInitialState(true);
            flushQueuedCommands(ws);
            requestTileContentsForWorkspaces(ws, nextWorkspaces);
            if (ws.readyState === WebSocket.OPEN) {
              // Re-attach PTY streams only after recovery barrier has lifted and
              // initial_state has arrived.
              const reattachIds = ptyTransportRef.current.listAttachedRuntimeIds();
              if (reattachIds.length > 0) {
                recordDiag({ kind: 'attach', reason: 'recovery_reattach', sessions: reattachIds });
              }
              for (const sessionId of reattachIds) {
                ws.send(JSON.stringify({ cmd: 'attach_session', id: sessionId }));
              }
            }
            break;

          case 'workspace_layout':
          case 'workspace_layout_updated':
            if (data.workspace_layout) {
              const workspaceLayout = data.workspace_layout;
              const workspaceID = workspaceLayout.workspace_id;
              const layoutDiag = collectWorkspaceLayoutDiagnostics(
                parseLayoutJSON(workspaceLayout.layout_json || ''),
              );
              recordLayout(workspaceID, layoutDiag.panes.map((pane) => pane.paneId), layoutDiag.splitCount);
              const nextWorkspaces = workspacesRef.current.map((workspace) => (
                workspace.id === workspaceID
                  ? { ...workspace, layout: workspaceLayout }
                  : workspace
              ));
              workspacesRef.current = nextWorkspaces;
              callbacksRef.current.onWorkspacesUpdate(nextWorkspaces);
              setTileContents((prev) => pruneTileContentsForWorkspace(
                prev,
                workspaceID,
                tileIdsFromLayoutJSON(workspaceLayout.layout_json || ''),
              ));
              pruneAttachedPtySessions(sessionsRef.current, nextWorkspaces);
            }
            break;

          case 'workspace_layout_action_result': {
            if (!isValidWorkspaceActionResult(data)) {
              console.warn('[Daemon] Ignoring malformed workspace action result:', data);
              break;
            }
            const action = data.action;
            const workspaceId = data.workspace_id;
            const entityId = data.leaf_id || data.pane_id || data.split_id || data.tile_id;
            const key = workspaceActionKey(action, workspaceId, entityId, data.request_id);
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if (data.success) {
                pending.resolve({ success: true, final_leaf_id: data.final_leaf_id });
              } else {
                pending.reject(new Error(data.error || 'Workspace action failed'));
              }
            }
            break;
          }

          case 'rename_result': {
            if (typeof data.cmd === 'string' && typeof data.id === 'string') {
              const key = `${data.cmd}:${data.id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve(undefined);
                } else {
                  pending.reject(new Error(data.error || 'Rename failed'));
                }
              }
            }
            break;
          }

          case 'chief_of_staff_result': {
            if (typeof data.session_id === 'string') {
              const key = `chief_of_staff:${data.session_id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve(undefined);
                } else {
                  pending.reject(new Error(data.error || 'Chief of staff update failed'));
                }
              }
            }
            break;
          }

          case 'chief_of_staff_dispatches_updated':
            callbacksRef.current.onChiefOfStaffDispatchesUpdate?.(data.dispatches || []);
            break;

          case 'wake_dispatch_agent_result': {
            if (typeof data.dispatch_id === 'string' && typeof data.request_id === 'string') {
              const key = `wake_dispatch_agent:${data.dispatch_id}:${data.request_id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve(undefined);
                } else {
                  pending.reject(new Error(data.error || 'Wake agent failed'));
                }
              }
            }
            break;
          }

          case 'workspace_tile_content': {
            if (typeof data.workspace_id === 'string' && typeof data.tile_id === 'string') {
              const key = tileContentKey(data.workspace_id, data.tile_id);
              setTileContents((prev) => ({
                ...prev,
                [key]: {
                  path: typeof data.path === 'string' ? data.path : '',
                  content: typeof data.content === 'string' ? data.content : '',
                  error: typeof data.error === 'string' ? data.error : undefined,
                },
              }));
            }
            break;
          }

          case 'browser_control_request': {
            if (
              typeof data.request_id !== 'string'
              || typeof data.workspace_id !== 'string'
              || typeof data.tile_id !== 'string'
              || typeof data.action !== 'string'
            ) {
              console.warn('[Daemon] Ignoring malformed browser control request:', data);
              break;
            }
            const requestId = data.request_id;
            void controlBrowserHost(
              data.workspace_id,
              data.tile_id,
              data.action,
              typeof data.params === 'string' ? data.params : undefined,
              typeof data.selector === 'string' ? data.selector : undefined,
              typeof data.text === 'string' ? data.text : undefined,
            ).then((result) => {
              if (ws.readyState === WebSocket.OPEN) {
                ws.send(serializeBrowserControlResultMessage({
                  cmd: 'browser_control_result',
                  request_id: requestId,
                  success: true,
                  data: result,
                }));
              }
            }).catch((controlError) => {
              if (ws.readyState === WebSocket.OPEN) {
                let response: string;
                try {
                  response = serializeBrowserControlResultMessage({
                    cmd: 'browser_control_result',
                    request_id: requestId,
                    success: false,
                    error: String(controlError),
                  });
                } catch {
                  response = serializeBrowserControlResultMessage({
                    cmd: 'browser_control_result',
                    request_id: requestId,
                    success: false,
                    error: 'browser control failed with an oversized error',
                  });
                }
                ws.send(response);
              }
            });
            break;
          }

          case 'workspace_registered':
          case 'workspace_state_changed':
            if (data.workspace) {
              const key = `register_workspace:${data.workspace.id}`;
              pendingActionsRef.current.get(key)?.resolve(undefined);
              pendingActionsRef.current.delete(key);
              const nextWorkspaces = upsertWorkspaceByID(workspacesRef.current, data.workspace);
              workspacesRef.current = nextWorkspaces;
              callbacksRef.current.onWorkspacesUpdate(nextWorkspaces);
            }
            break;

          case 'workspace_context_changed':
          case 'workspace_context_result':
            break;

          case 'workspace_context_list_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `workspace_context_list:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve(data.contexts || []);
            } else {
              pending.reject(new Error(data.error || 'Workspace context list failed'));
            }
            break;
          }

          case 'workspace_unregistered':
            if (data.workspace) {
              const key = `unregister_workspace:${data.workspace.id}`;
              pendingActionsRef.current.get(key)?.resolve(undefined);
              pendingActionsRef.current.delete(key);
              const nextWorkspaces = workspacesRef.current.filter((workspace) => workspace.id !== data.workspace!.id);
              workspacesRef.current = nextWorkspaces;
              callbacksRef.current.onWorkspacesUpdate(nextWorkspaces);
              setTileContents((prev) => pruneTileContentsForWorkspace(prev, data.workspace!.id));
            }
            break;

          case 'endpoint_status_changed':
            if (data.endpoint) {
              const nextEndpoints = upsertEndpointByID(endpointsRef.current, data.endpoint);
              endpointsRef.current = nextEndpoints;
              callbacksRef.current.onEndpointsUpdate?.(nextEndpoints);
            }
            break;

          case 'endpoints_updated':
            if (data.endpoints) {
              endpointsRef.current = data.endpoints;
              callbacksRef.current.onEndpointsUpdate?.(data.endpoints);
            }
            if (pendingActionsRef.current.has('list_endpoints')) {
              const pending = pendingActionsRef.current.get('list_endpoints');
              pendingActionsRef.current.delete('list_endpoints');
              pending?.resolve(data.endpoints || []);
            }
            break;

          case 'endpoint_action_result': {
            const action = data.action || '';
            const exactKey = data.endpoint_id ? `endpoint_action:${action}:${data.endpoint_id}` : '';
            let pendingKey = exactKey;
            if (!pendingKey || !pendingActionsRef.current.has(pendingKey)) {
              pendingKey = Array.from(pendingActionsRef.current.keys()).find((key) => key.startsWith(`endpoint_action:${action}:`)) || '';
            }
            const pending = pendingKey ? pendingActionsRef.current.get(pendingKey) : undefined;
            if (pending) {
              pendingActionsRef.current.delete(pendingKey);
              if (data.success) {
                pending.resolve({ success: true, endpoint_id: data.endpoint_id });
              } else {
                pending.reject(new Error(data.error || 'Endpoint action failed'));
              }
            }
            break;
          }

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
              if (canceledAttachIdsRef.current.delete(data.id)) {
                pendingActionsRef.current.delete(key);
                ptyTransportRef.current.clearRuntime(data.id);
                pending?.reject(new Error('Attach session canceled'));
                break;
              }
              const attachContext = ptyTransportRef.current.getAttachContext(data.id);
              const replayPlan = classifyAttachReplay(data, attachContext);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve({
                    success: true,
                    scrollback: data.scrollback,
                    scrollback_truncated: data.scrollback_truncated,
                    replay_segments: data.replay_segments,
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
                ptyTransportRef.current.markRuntimeAttached(data.id);
              } else {
                ptyTransportRef.current.clearRuntime(data.id);
              }

              if (data.success) {
                const session = sessionsRef.current.find((entry) => entry.id === data.id);
                const attachEffects = planAttachResultEffects({
                  attachResult: data,
                  replayPlan,
                  previousSeq: ptyTransportRef.current.getLastSeq(data.id),
                  queuedOutputs: ptyTransportRef.current.getQueuedAttachOutputs(data.id),
                  sessionAgent: session?.agent,
                });
                const snapshotGeometry = replayPlan.replayApplied
                  && replayPlan.hasScreenSnapshot
                  && typeof replayPlan.replayCols === 'number'
                  && typeof replayPlan.replayRows === 'number'
                  && (
                    attachContext?.policy === 'relaunch_restore'
                    || replayPlan.replayGeometryMismatch
                  )
                  ? { cols: replayPlan.replayCols, rows: replayPlan.replayRows }
                  : null;
                const relaunchFallbackGeometry = attachContext?.policy === 'relaunch_restore'
                  && typeof data.cols === 'number'
                  && typeof data.rows === 'number'
                  ? { cols: data.cols, rows: data.rows }
                  : null;
                const replayGeometry = snapshotGeometry || relaunchFallbackGeometry;
                if (replayGeometry) {
                  emitPtyEvent({
                    event: 'local_resize',
                    id: data.id,
                    cols: replayGeometry.cols,
                    rows: replayGeometry.rows,
                    source: 'attach_replay',
                  });
                }
                if (attachEffects.shouldReset && attachEffects.resetReason) {
                  emitPtyEvent({
                    event: 'reset',
                    id: data.id,
                    reason: attachEffects.resetReason,
                  });
                }
                ptyTransportRef.current.setLastSeq(data.id, attachEffects.nextSeq);
                if (attachEffects.replayAction.kind === 'screen_snapshot') {
                  emitPtyEvent({
                    event: 'data',
                    id: data.id,
                    data: attachEffects.replayAction.data,
                    source: 'attach_replay',
                    suppressResponses: !replayPlan.respondToTerminalQueries,
                  });
                } else if (attachEffects.replayAction.kind === 'scrollback') {
                  emitPtyEvent({
                    event: 'data',
                    id: data.id,
                    data: attachEffects.replayAction.data,
                    source: 'attach_replay',
                    suppressResponses: !replayPlan.respondToTerminalQueries,
                  });
                }
                const replayWasEmitted = attachEffects.replayAction.kind === 'screen_snapshot'
                  || attachEffects.replayAction.kind === 'scrollback'
                  || attachEffects.replayAction.kind === 'scrollback_segments';
                if (attachEffects.replayAction.kind === 'scrollback_segments') {
                  let replayCols: number | null = null;
                  let replayRows: number | null = null;
                  for (const segment of attachEffects.replayAction.segments) {
                    if (segment.cols !== replayCols || segment.rows !== replayRows) {
                      emitPtyEvent({
                        event: 'local_resize',
                        id: data.id,
                        cols: segment.cols,
                        rows: segment.rows,
                        source: 'attach_replay',
                      });
                      replayCols = segment.cols;
                      replayRows = segment.rows;
                    }
                    if (segment.data) {
                      emitPtyEvent({
                        event: 'data',
                        id: data.id,
                        data: segment.data,
                        source: 'attach_replay',
                        suppressResponses: !replayPlan.respondToTerminalQueries,
                      });
                    }
                  }
                }
                if (attachEffects.queuedOutputsToEmit.length > 0) {
                  ptyTransportRef.current.clearQueuedAttachOutputs(data.id);
                  for (const chunk of attachEffects.queuedOutputsToEmit) {
                    emitPtyEvent({ event: 'data', id: data.id, data: chunk.data, seq: chunk.seq });
                  }
                }
                if (replayWasEmitted) {
                  emitPtyEvent({
                    event: 'replay_complete',
                    id: data.id,
                  });
                }
                if (attachEffects.shouldWarnTruncatedRestore) {
                  emitPtyEvent({
                    event: 'error',
                    id: data.id,
                    error: 'Restore scrollback was truncated; Codex full-screen output may be incomplete.',
                  });
                }
                ptyTransportRef.current.setAttachContext(data.id);
              }
            }
            break;
          }

          case 'get_screen_snapshot_result': {
            if (data.id) {
              const key = `screen_snapshot_${data.id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  const result: ScreenSnapshotResult = {
                    screenSnapshot: data.screen_snapshot_fresh ? data.screen_snapshot : undefined,
                    screenCols: data.screen_cols,
                    screenRows: data.screen_rows,
                    lastSeq: typeof data.last_seq === 'number' ? data.last_seq : 0,
                  };
                  pending.resolve(result);
                } else {
                  // Unsupported / session gone: degrade to live-fill.
                  pending.resolve(null);
                }
              }
            }
            break;
          }

          case 'pty_output': {
            // Local sessions arrive as binary frames instead (the daemon honors
            // our binary_pty_output capability); this JSON event remains for
            // relayed remote-endpoint sessions.
            if (data.id && data.data) {
              handleLivePtyOutput(data.id, data.seq, data.data);
            }
            break;
          }

          case 'session_exited':
            if (data.id) {
              const killKey = `pty_kill_${data.id}`;
              const pendingKill = pendingActionsRef.current.get(killKey);
              if (pendingKill) {
                pendingActionsRef.current.delete(killKey);
                pendingKill.resolve({ success: true });
              }
              ptyTransportRef.current.clearRuntime(data.id);
              emitPtyEvent({
                event: 'exit',
                id: data.id,
                code: data.exit_code ?? 0,
                signal: data.signal,
              });
              if (callbacksRef.current.onSessionExited) {
                callbacksRef.current.onSessionExited({
                  id: data.id,
                  exitCode: data.exit_code ?? 0,
                  signal: data.signal,
                });
              }
            }
            break;

          case 'pty_desync':
            if (data.id) {
              recordDiag({ kind: 'desync', session: data.id, reason: data.reason || 'desync' });
              emitPtyEvent({ event: 'reset', id: data.id, reason: data.reason || 'desync' });
              ptyTransportRef.current.clearRuntimeStream(data.id);
              ws.send(JSON.stringify({ cmd: 'attach_session', id: data.id }));
            }
            break;

          case 'pty_resized':
            if (data.id && data.cols && data.rows) {
              recordDiag({ kind: 'resize', session: data.id, source: 'pty_resized', toCols: data.cols, toRows: data.rows });
              emitPtyEvent({ event: 'local_resize', id: data.id, cols: data.cols, rows: data.rows });
            }
            break;

          case 'session_registered':
            if (data.session) {
              sessionsRef.current = upsertSessionByID(sessionsRef.current, data.session);
              callbacksRef.current.onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'session_unregistered':
            if (data.session) {
              const unregisterKey = `unregister:${data.session.id}`;
              const pendingUnregister = pendingActionsRef.current.get(unregisterKey);
              if (pendingUnregister) {
                pendingActionsRef.current.delete(unregisterKey);
                pendingUnregister.resolve(undefined);
              }
              ptyTransportRef.current.clearRuntime(data.session.id);
              sessionsRef.current = sessionsRef.current.filter(
                (s) => s.id !== data.session!.id
              );
              callbacksRef.current.onSessionsUpdate(sessionsRef.current);
              const layoutsInvalidated = invalidateWorkspaceLayoutsForSession(
                workspacesRef.current,
                data.session.id,
              );
              const nextWorkspaces = pruneWorkspacesBySessions(
                sessionsRef.current,
                layoutsInvalidated,
              );
              if (nextWorkspaces !== workspacesRef.current) {
                workspacesRef.current = nextWorkspaces;
                callbacksRef.current.onWorkspacesUpdate(nextWorkspaces);
              }
              pruneAttachedPtySessions(sessionsRef.current, workspacesRef.current);
            }
            break;

          case 'session_state_changed':
          case 'session_todos_updated':
            if (data.session) {
              sessionsRef.current = upsertSessionByID(sessionsRef.current, data.session);
              callbacksRef.current.onSessionsUpdate(sessionsRef.current);
            }
            break;

          case 'sessions_updated':
            {
              const dedupedSessions = dedupeSessionsByID(data.sessions || []);
              sessionsRef.current = dedupedSessions;
              callbacksRef.current.onSessionsUpdate(dedupedSessions);
              const nextWorkspaces = pruneWorkspacesBySessions(
                dedupedSessions,
                workspacesRef.current,
              );
              if (nextWorkspaces !== workspacesRef.current) {
                workspacesRef.current = nextWorkspaces;
                callbacksRef.current.onWorkspacesUpdate(nextWorkspaces);
              }
              pruneAttachedPtySessions(dedupedSessions, workspacesRef.current);
            }
            break;

          case 'prs_updated':
            if (data.prs) {
              prsRef.current = data.prs;
              callbacksRef.current.onPRsUpdate(data.prs);
            }
            break;

          case 'repos_updated':
            if (data.repos) {
              reposRef.current = data.repos;
              callbacksRef.current.onReposUpdate(data.repos);
            }
            break;

          case 'authors_updated':
            if (data.authors) {
              authorsRef.current = data.authors;
              callbacksRef.current.onAuthorsUpdate(data.authors);
            }
            break;

          case 'settings_updated':
            if (data.settings) {
              settingsRef.current = data.settings;
              callbacksRef.current.onSettingsUpdate?.(data.settings);
            }
            if (data.success === false && data.error) {
              callbacksRef.current.onSettingError?.(data.error);
            }
            break;

          case 'github_hosts_updated':
            callbacksRef.current.onGitHubHostsUpdate?.(data.github_hosts || []);
            break;

          case 'plugins_updated': {
            const plugins = data.plugins || [];
            const issues = data.issues || [];
            callbacksRef.current.onPluginsUpdate?.(plugins, issues);
            const pending = pendingActionsRef.current.get('list_plugins');
            if (pending) {
              pendingActionsRef.current.delete('list_plugins');
              pending.resolve({
                plugins,
                issues,
              } satisfies PluginListResult);
            }
            break;
          }

          case 'plugin_action_result': {
            const action = data.action || '';
            const exactKey = data.name ? `plugin_action:${action}:${data.name}` : '';
            let pendingKey = exactKey;
            if (!pendingKey || !pendingActionsRef.current.has(pendingKey)) {
              pendingKey = Array.from(pendingActionsRef.current.keys()).find((key) => key.startsWith(`plugin_action:${action}:`)) || '';
            }
            const pending = pendingKey ? pendingActionsRef.current.get(pendingKey) : undefined;
            if (pending) {
              pendingActionsRef.current.delete(pendingKey);
              if (data.success) {
                pending.resolve({ success: true, name: data.name });
              } else {
                pending.reject(new Error(data.error || 'Plugin action failed'));
              }
            }
            break;
          }

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
            callbacksRef.current.onWorktreesUpdate?.(data.worktrees || []);
            break;

          case 'worktree_created':
          case 'worktree_deleted':
            // These events are informational, UI updates handled via worktrees_updated
            break;

          case 'git_operation_started':
          case 'git_operation_finished': {
            const operation = data.operation as DaemonGitOperation | undefined;
            if (operation?.id) {
              setGitOperations((current) => ({
                ...current,
                [operation.id]: operation,
              }));
            }
            break;
          }

          case 'create_worktree_result':
          case 'delete_worktree_result':
            // Handle async result for pending worktree actions
            const actionKey = `worktree_${data.event}_${data.endpoint_id || 'local'}`;
            const pendingAction = pendingActionsRef.current.get(actionKey);
            if (pendingAction) {
              pendingActionsRef.current.delete(actionKey);
              if (data.success) {
                pendingAction.resolve({ success: true, path: data.path, endpoint_id: data.endpoint_id });
              } else {
                const error = new Error(data.error || 'Worktree action failed') as WorktreeActionError;
                error.forceable = data.forceable;
                error.reason_kind = data.reason_kind;
                pendingAction.reject(error);
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
            const key = data.request_id
              ? `get_recent_locations_${data.request_id}`
              : `get_recent_locations_${data.endpoint_id || 'local'}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if (data.success) {
                pending.resolve({
                  success: true,
                  locations: data.recent_locations || [],
                  endpoint_id: data.endpoint_id,
                  home_path: data.home_path,
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to get recent locations'));
              }
            }
            break;
          }

          case 'browse_directory_result': {
            const key = data.request_id
              ? `browse_directory_${data.request_id}`
              : `browse_directory_${data.endpoint_id || 'local'}_${data.input_path || ''}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if (data.success) {
                pending.resolve({
                  success: true,
                  input_path: data.input_path || '',
                  directory: data.directory || '',
                  entries: data.entries || [],
                  endpoint_id: data.endpoint_id,
                  home_path: data.home_path,
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to browse directory'));
              }
            }
            break;
          }

          case 'inspect_path_result': {
            const inspection = data.inspection;
            const key = data.request_id
              ? `inspect_path_${data.request_id}`
              : `inspect_path_${data.endpoint_id || 'local'}_${inspection?.input_path || ''}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if (data.success) {
                pending.resolve({
                  success: true,
                  inspection,
                  endpoint_id: data.endpoint_id,
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to inspect path'));
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
              callbacksRef.current.onGitStatusUpdate?.({
                directory: data.directory,
                staged: data.staged || [],
                unstaged: data.unstaged || [],
                untracked: data.untracked || [],
                error: data.error,
                mode: data.mode,
                limited: data.limited,
                limited_reason: data.limited_reason,
                duration_ms: data.duration_ms,
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
            const key = `repo_info_${data.endpoint_id || 'local'}_${repoPath}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if ((data as any).success) {
                pending.resolve({ success: true, info: (data as any).info, endpoint_id: data.endpoint_id });
              } else {
                pending.resolve({ success: false, error: (data as any).error, endpoint_id: data.endpoint_id });
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

          case 'review_loop_result': {
            const action = (data as any).action || 'unknown';
            const sessionId = (data as any).session_id || '';
            const loopId = (data as any).loop_id || (data as any).review_loop_run?.loop_id || '';
            const key = action === 'show' || action === 'answer'
              ? `${action}_review_loop_${loopId}`
              : `${action}_review_loop_${sessionId}`;
            const pending = pendingActionsRef.current.get(key);
            if (pending) {
              pendingActionsRef.current.delete(key);
              if ((data as any).success) {
                pending.resolve({
                  success: true,
                  state: (data as any).review_loop_run ?? null,
                });
              } else {
                pending.reject(new Error((data as any).error || 'Review loop command failed'));
              }
            }
            break;
          }

          case 'review_loop_updated':
            if (callbacksRef.current.onReviewLoopUpdate) {
              callbacksRef.current.onReviewLoopUpdate((data as any).review_loop_run ?? null);
            }
            break;

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
      hasReceivedInitialStateRef.current = false;
      canceledAttachIdsRef.current.clear();

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
  }, [resolvedWsUrl, rejectPendingForCommand, ensureDaemonRunning, showRecoveringNoticeForCommand, flushQueuedCommands, pruneAttachedPtySessions]);

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
    daemonRestartInProgressRef.current = false;
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
        workspace_id: args.workspace_id,
        ...(args.endpoint_id && { endpoint_id: args.endpoint_id }),
        agent: args.shell ? 'shell' : (args.agent || 'codex'),
        cols: args.cols,
        rows: args.rows,
        ...(args.label && { label: args.label }),
        ...(args.resume_session_id && { resume_session_id: args.resume_session_id }),
        ...(args.resume_picker && { resume_picker: args.resume_picker }),
        ...(args.yolo_mode && { yolo_mode: args.yolo_mode }),
        ...(args.executable && { executable: args.executable }),
        ...(args.claude_executable && { claude_executable: args.claude_executable }),
        ...(args.codex_executable && { codex_executable: args.codex_executable }),
        ...(args.copilot_executable && { copilot_executable: args.copilot_executable }),
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Spawn session timed out'));
        }
      }, 30000);
    });
  }, []);

  const sendAttachSession = useCallback((id: string, context?: AttachRequestContext): Promise<AttachResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `pty_attach_${id}`;
      if (context) {
        ptyTransportRef.current.setAttachContext(id, context);
      } else {
        ptyTransportRef.current.setAttachContext(id);
      }
      pendingActionsRef.current.set(key, { resolve, reject });
      recordPtyCommand('attach_session', id);
      ws.send(JSON.stringify({
        cmd: 'attach_session',
        id,
        ...(context?.policy ? { attach_policy: context.policy } : {}),
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          canceledAttachIdsRef.current.delete(id);
          ptyTransportRef.current.clearQueuedAttachOutputs(id);
          ptyTransportRef.current.setAttachContext(id);
          reject(new Error('Attach session timed out'));
        }
      }, 15000);
    });
  }, []);

  const sendAttachSessionWithRetry = useCallback(async (id: string, context?: AttachRequestContext): Promise<AttachResult> => {
    return retryTransientAttachRequest(
      () => sendAttachSession(id, context),
      {
        onRetry: (attempt, error, elapsedMs) => {
        console.warn('[DaemonSocket] Retrying transient attach failure', {
          id,
          attempt,
          elapsedMs,
          error: error instanceof Error ? error.message : String(error),
        });
        },
      },
    );
  }, [sendAttachSession]);

  const sendDetachSession = useCallback((id: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    if (pendingActionsRef.current.has(`pty_attach_${id}`)) {
      canceledAttachIdsRef.current.add(id);
    }
    ptyTransportRef.current.clearRuntime(id);
    recordPtyCommand('detach_session', id);
    ws.send(JSON.stringify({ cmd: 'detach_session', id }));
  }, []);

  const isRuntimeAttached = useCallback((runtimeId: string): boolean => {
    return ptyTransportRef.current.hasAttachedRuntime(runtimeId);
  }, []);

  const sendPtyInput = useCallback((id: string, data: string, source?: string) => {
    const ws = wsRef.current;
    const transportReady = Boolean(ws && ws.readyState === WebSocket.OPEN && hasReceivedInitialStateRef.current);
    if (!transportReady && (source === 'user' || source === 'automation')) {
      console.warn('[DaemonSocket] Queueing PTY input while transport is not ready', {
        id,
        bytes: data.length,
        source: source || 'unknown',
        wsState: ws?.readyState ?? null,
        initialStateReceived: hasReceivedInitialStateRef.current,
      });
    }
    recordPtyCommand('pty_input', id, data.length, source);
    sendOrQueueCommand(
      { cmd: 'pty_input', id, data, ...(source ? { source } : {}) },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  const sendPtyResize = useCallback((id: string, cols: number, rows: number, reason?: string) => {
    const suspiciousResize = isSuspiciousTerminalSize(cols, rows);
    if (suspiciousResize) {
      console.warn('[DaemonSocket] Sending suspicious PTY resize', { id, cols, rows, reason: reason || null });
    }
    recordPtyCommand('pty_resize', id, 0, null);
    sendOrQueueCommand(
      { cmd: 'pty_resize', id, cols, rows },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  const reconcileAttachedRuntimeGeometry = useCallback((
    args: Pick<PtySpawnArgs, 'id' | 'cols' | 'rows' | 'shell'>,
    attachResult: AttachResult,
    options: {
      attachPolicy: PtyAttachPolicy;
      attachContext?: AttachRequestContext;
      requestedGeometryAuthoritative?: boolean;
    },
  ) => {
    const plan = planAttachedRuntimeGeometry(args, attachResult, options);

    if (plan.resizeRequired) {
      sendPtyResize(args.id, plan.requestedCols, plan.requestedRows, 'daemon_known_attach');
    }
  }, [sendPtyResize]);

  const attachExistingRuntime = useCallback(async (
    args: Pick<PtyAttachArgs, 'id' | 'cols' | 'rows' | 'shell' | 'agent' | 'reason'>,
    options: {
      policy: Extract<PtyAttachPolicy, 'relaunch_restore' | 'same_app_remount'>;
      forceResizeBeforeAttach?: boolean;
    },
  ): Promise<void> => {
    const sessionAgent = args.agent
      ?? sessionsRef.current.find((entry) => entry.id === args.id)?.agent
      ?? null;
    const attachContext = createAttachRequestContext({
      ...args,
      agent: sessionAgent,
    }, options.policy);
    if (options?.forceResizeBeforeAttach) {
      sendPtyResize(args.id, args.cols, args.rows, args.reason || 'remount_hydrate');
    }
    const attachResult = await sendAttachSessionWithRetry(
      args.id,
      attachContext,
    );
    reconcileAttachedRuntimeGeometry(args, attachResult, {
      attachPolicy: options.policy,
      attachContext,
      requestedGeometryAuthoritative: options.forceResizeBeforeAttach === true,
    });
  }, [reconcileAttachedRuntimeGeometry, sendAttachSessionWithRetry, sendPtyResize]);

  const sendKillSession = useCallback((id: string, signal?: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `pty_kill_${id}`;
      pendingActionsRef.current.set(key, {
        resolve: () => resolve(),
        reject,
      });

      ws.send(JSON.stringify({ cmd: 'kill_session', id, ...(signal && { signal }) }));

      // Wait for session_exited to avoid kill/spawn races during reload.
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Kill session timed out'));
        }
      }, 3000);
    });
  }, []);

  const sendWorkspaceGet = useCallback((workspaceId: string) => {
    sendOrQueueCommand({ cmd: 'workspace_layout_get', workspace_id: workspaceId }, { waitForInitialState: true });
  }, [sendOrQueueCommand]);

  const sendWorkspaceCommand = useCallback((
    action: string,
    workspaceId: string,
    payload: Record<string, unknown>,
    entityId?: string,
    requestId?: string,
  ): Promise<WorkspaceActionResult> => {
    return new Promise((resolve, reject) => {
      const key = workspaceActionKey(action, workspaceId, entityId, requestId);
      pendingActionsRef.current.set(key, { resolve, reject });
      sendOrQueueCommand(payload, { waitForInitialState: true });

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Workspace action timed out'));
        }
      }, 30000);
    });
  }, [sendOrQueueCommand]);

  const sendWorkspaceAddSessionPane = useCallback((
    workspaceId: string,
    sessionId: string,
    title?: string,
    options: { paneId?: string; targetPaneId?: string; direction?: 'vertical' | 'horizontal' } = {},
  ) => {
    const paneId = options.paneId || `pane-${sessionId}`;
    return sendWorkspaceCommand(
      'workspace_layout_add_session_pane',
      workspaceId,
      {
        cmd: 'workspace_layout_add_session_pane',
        workspace_id: workspaceId,
        pane_id: paneId,
        session_id: sessionId,
        ...(title ? { title } : {}),
        ...(options.targetPaneId ? { target_pane_id: options.targetPaneId } : {}),
        ...(options.direction ? { direction: options.direction } : {}),
      },
      paneId,
    );
  }, [sendWorkspaceCommand]);

  const sendWorkspaceClosePane = useCallback((workspaceId: string, paneId: string) => {
    return sendWorkspaceCommand(
      'workspace_layout_close_pane',
      workspaceId,
      {
        cmd: 'workspace_layout_close_pane',
        workspace_id: workspaceId,
        pane_id: paneId,
      },
      paneId,
    );
  }, [sendWorkspaceCommand]);

  const sendWorkspaceFocusPane = useCallback((workspaceId: string, paneId: string) => {
    return sendWorkspaceCommand(
      'workspace_layout_focus_pane',
      workspaceId,
      {
        cmd: 'workspace_layout_focus_pane',
        workspace_id: workspaceId,
        pane_id: paneId,
      },
      paneId,
    );
  }, [sendWorkspaceCommand]);

  const sendWorkspaceRenamePane = useCallback((workspaceId: string, paneId: string, title: string) => {
    return sendWorkspaceCommand(
      'workspace_layout_rename_pane',
      workspaceId,
      {
        cmd: 'workspace_layout_rename_pane',
        workspace_id: workspaceId,
        pane_id: paneId,
        title,
      },
      paneId,
    );
  }, [sendWorkspaceCommand]);

  const sendWorkspaceSetSplitRatio = useCallback((workspaceId: string, splitId: string, ratio: number) => {
    const requestId = nextRequestID('workspace_split_ratio');
    return sendWorkspaceCommand(
      'workspace_layout_set_split_ratio',
      workspaceId,
      {
        cmd: 'workspace_layout_set_split_ratio',
        workspace_id: workspaceId,
        split_id: splitId,
        ratio,
        request_id: requestId,
      },
      splitId,
      requestId,
    );
  }, [nextRequestID, sendWorkspaceCommand]);

  const sendWorkspaceUndockTile = useCallback((workspaceId: string, tileId: string) => {
    return sendWorkspaceCommand(
      'workspace_layout_undock_tile',
      workspaceId,
      {
        cmd: 'workspace_layout_undock_tile',
        workspace_id: workspaceId,
        tile_id: tileId,
      },
      tileId,
    );
  }, [sendWorkspaceCommand]);

  const sendWorkspaceUpdateTile = useCallback((workspaceId: string, tileId: string, tileParams: string) => {
    const requestId = nextRequestID('workspace_update_tile');
    return sendWorkspaceCommand(
      'workspace_layout_update_tile',
      workspaceId,
      {
        cmd: 'workspace_layout_update_tile',
        workspace_id: workspaceId,
        tile_id: tileId,
        tile_params: tileParams,
        request_id: requestId,
      },
      tileId,
      requestId,
    );
  }, [nextRequestID, sendWorkspaceCommand]);

  // Move an existing leaf (terminal pane or docked tile) beside an anchor leaf.
  // An empty anchorId docks the leaf against the whole workspace (the root). The
  // daemon broadcasts the authoritative layout, so this is effectively
  // fire-and-forget; a rejected move (self-drop, only leaf) just leaves the
  // layout unchanged.
  const sendWorkspaceMoveLeaf = useCallback((
    workspaceId: string,
    leafId: string,
    options: { anchorId?: string; edge?: TerminalDockEdge; ratio?: number } = {},
  ) => {
    return sendWorkspaceCommand(
      'workspace_layout_move_leaf',
      workspaceId,
      {
        cmd: 'workspace_layout_move_leaf',
        workspace_id: workspaceId,
        leaf_id: leafId,
        anchor_id: options.anchorId ?? '',
        edge: options.edge ?? 'right',
        ...(options.ratio != null ? { ratio: options.ratio } : {}),
      },
      leafId,
    );
  }, [sendWorkspaceCommand]);

  const sendWorkspaceMoveLeafToWorkspace = useCallback((
    sourceWorkspaceId: string,
    targetWorkspaceId: string,
    leafId: string,
    options: { anchorId?: string; edge?: TerminalDockEdge; ratio?: number } = {},
  ) => {
    return sendWorkspaceCommand(
      'workspace_layout_move_leaf_to_workspace',
      sourceWorkspaceId,
      {
        cmd: 'workspace_layout_move_leaf_to_workspace',
        source_workspace_id: sourceWorkspaceId,
        target_workspace_id: targetWorkspaceId,
        leaf_id: leafId,
        anchor_id: options.anchorId ?? '',
        edge: options.edge ?? 'left',
        ...(options.ratio != null ? { ratio: options.ratio } : {}),
      },
      leafId,
    );
  }, [sendWorkspaceCommand]);

  // requestTileContent pulls a tile's current content (used on first render).
  // The reply and all subsequent live-reload updates arrive as
  // workspace_tile_content events handled above. Fire-and-forget: no result
  // tracking needed. Queue the pull while disconnected so mounting a tile
  // during reconnect cannot leave it stale.
  const requestTileContent = useCallback((workspaceId: string, tileId: string) => {
    sendOrQueueCommand(
      { cmd: 'workspace_tile_content_get', workspace_id: workspaceId, tile_id: tileId },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  useEffect(() => {
    setPtyBackend({
      spawn: async (args: PtySpawnArgs) => {
        const existingSession = sessionsRef.current.find((session) => session.id === args.id);
        await spawnPtyRuntime(
          args,
          {
            existingSession,
            runtimeKnownToDaemon: Boolean(existingSession)
              || workspacesIncludeRuntimeID(workspacesRef.current, args.id),
            alreadyAttached: ptyTransportRef.current.hasAttachedRuntime(args.id),
          },
          {
            attachExistingRuntime,
            attachFreshRuntime: async (spawnArgs: PtySpawnArgs) => {
              await sendAttachSessionWithRetry(spawnArgs.id, {
                ...createAttachRequestContext(spawnArgs, 'fresh_spawn'),
              });
            },
            spawnRuntime: sendSpawnSession,
            resizeRuntime: sendPtyResize,
            logResumeRecovery: ({ id, agent, recoverable }) => {
              console.log(
                '[DaemonSocket] Recovering session %s (%s) via resume (recoverable=%s)',
                id,
                agent ?? 'unknown',
                String(recoverable),
              );
            },
          },
        );
      },
      attach: async (args: PtyAttachArgs, options?: { forceResizeBeforeAttach?: boolean }) => {
        await attachExistingRuntime({
          ...args,
        }, {
          policy: normalizeAttachPolicy(args.policy),
          forceResizeBeforeAttach: options?.forceResizeBeforeAttach,
        });
      },
      write: async (id: string, data: string, source?: string) => {
        sendPtyInput(id, data, source);
      },
      resize: async (id: string, cols: number, rows: number, reason?: string) => {
        sendPtyResize(id, cols, rows, reason);
      },
      detach: async (id: string) => {
        sendDetachSession(id);
      },
      kill: async (id: string) => {
        sendDetachSession(id);
        await sendKillSession(id);
      },
    });

    return () => {
      setPtyBackend(null);
    };
  }, [attachExistingRuntime, sendAttachSessionWithRetry, sendDetachSession, sendKillSession, sendPtyInput, sendPtyResize, sendSpawnSession]);

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

  // Fetch a session's current screen to seed a grid tile. Resolves null on any
  // failure (disconnected, session gone, or a worker too old to answer) so the
  // observer degrades to live-fill rather than surfacing an error.
  const getScreenSnapshot = useCallback((runtimeId: string): Promise<ScreenSnapshotResult | null> => {
    return new Promise((resolve) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN || !runtimeId) {
        resolve(null);
        return;
      }
      const key = `screen_snapshot_${runtimeId}`;
      // A newer request supersedes any in-flight one for the same runtime.
      pendingActionsRef.current.get(key)?.resolve(null);
      pendingActionsRef.current.set(key, { resolve, reject: () => resolve(null) });
      ws.send(JSON.stringify({ cmd: 'get_screen_snapshot', id: runtimeId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          resolve(null);
        }
      }, 10000);
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
    callbacksRef.current.onPRsUpdate(updatedPRs);

    ws.send(JSON.stringify({ cmd: 'mute_pr', id: prId }));
  }, []);

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
    callbacksRef.current.onReposUpdate(updatedRepos);

    ws.send(JSON.stringify({ cmd: 'mute_repo', repo }));
  }, []);

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
    callbacksRef.current.onAuthorsUpdate(updatedAuthors);

    ws.send(JSON.stringify({ cmd: 'mute_author', author }));
  }, []);

  // Mute a workspace (toggle muted state)
  const sendMuteWorkspace = useCallback((workspaceId: string, endpointId?: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      cmd: 'mute_workspace',
      workspace_id: workspaceId,
      ...(endpointId ? { endpoint_id: endpointId } : {}),
    }));
  }, []);

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

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Refresh timed out'));
        }
      }, GITHUB_REFRESH_TIMEOUT_MS);
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
      }, GITHUB_REFRESH_TIMEOUT_MS);
    });
  }, []);

  // Clear all sessions from daemon
  const sendClearSessions = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({ cmd: 'clear_sessions' }));
  }, []);

  const sendRegisterWorkspace = useCallback((workspaceId: string, title: string, directory: string, endpointId?: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      if (!workspaceId) {
        resolve();
        return;
      }
      const key = `register_workspace:${workspaceId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(), reject });
      sendOrQueueCommand(
        { cmd: 'register_workspace', id: workspaceId, title, directory, ...(endpointId ? { endpoint_id: endpointId } : {}) },
        { waitForInitialState: true },
      );
      window.setTimeout(() => {
        if (!pendingActionsRef.current.has(key)) {
          return;
        }
        pendingActionsRef.current.delete(key);
        reject(new Error(`Workspace registration timed out for ${workspaceId}`));
      }, 10_000);
    });
  }, [sendOrQueueCommand]);

  const sendUnregisterWorkspace = useCallback((workspaceId: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      if (!workspaceId) {
        resolve();
        return;
      }
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `unregister_workspace:${workspaceId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(), reject });
      ws.send(JSON.stringify({ cmd: 'unregister_workspace', id: workspaceId }));
      window.setTimeout(() => {
        if (!pendingActionsRef.current.has(key)) {
          return;
        }
        pendingActionsRef.current.delete(key);
        reject(new Error(`Workspace close timed out for ${workspaceId}`));
      }, 10_000);
    });
  }, []);

  const sendRenameSession = useCallback((sessionId: string, label: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      const trimmed = label.trim();
      if (!sessionId || !trimmed) {
        reject(new Error('Session name cannot be empty'));
        return;
      }
      const key = `rename_session:${sessionId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(), reject });
      sendOrQueueCommand(
        { cmd: 'rename_session', session_id: sessionId, label: trimmed },
        { waitForInitialState: true },
      );
      window.setTimeout(() => {
        if (!pendingActionsRef.current.has(key)) {
          return;
        }
        pendingActionsRef.current.delete(key);
        reject(new Error(`Rename timed out for session ${sessionId}`));
      }, 10_000);
    });
  }, [sendOrQueueCommand]);

  const sendRenameWorkspace = useCallback((workspaceId: string, title: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      const trimmed = title.trim();
      if (!workspaceId || !trimmed) {
        reject(new Error('Workspace name cannot be empty'));
        return;
      }
      const key = `rename_workspace:${workspaceId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(), reject });
      sendOrQueueCommand(
        { cmd: 'rename_workspace', workspace_id: workspaceId, title: trimmed },
        { waitForInitialState: true },
      );
      window.setTimeout(() => {
        if (!pendingActionsRef.current.has(key)) {
          return;
        }
        pendingActionsRef.current.delete(key);
        reject(new Error(`Rename timed out for workspace ${workspaceId}`));
      }, 10_000);
    });
  }, [sendOrQueueCommand]);

  const sendSetChiefOfStaff = useCallback((sessionId: string, chiefOfStaff: boolean): Promise<void> => {
    return new Promise((resolve, reject) => {
      if (!sessionId) {
        reject(new Error('Session is required'));
        return;
      }
      const ws = wsRef.current;
      if (!hasReceivedInitialStateRef.current || !ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `chief_of_staff:${sessionId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(), reject });
      ws.send(JSON.stringify({
        cmd: 'set_chief_of_staff',
        session_id: sessionId,
        chief_of_staff: chiefOfStaff,
      }));
      window.setTimeout(() => {
        if (!pendingActionsRef.current.has(key)) {
          return;
        }
        pendingActionsRef.current.delete(key);
        reject(new Error(`Chief of staff update timed out for session ${sessionId}`));
      }, 10_000);
    });
  }, []);

  const sendWakeDispatchAgent = useCallback((sourceSessionId: string, dispatchId: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      if (!sourceSessionId || !dispatchId) {
        reject(new Error('Chief session and dispatch are required'));
        return;
      }
      const ws = wsRef.current;
      if (!hasReceivedInitialStateRef.current || !ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const keyPrefix = `wake_dispatch_agent:${dispatchId}:`;
      if (Array.from(pendingActionsRef.current.keys()).some((key) => key.startsWith(keyPrefix))) {
        reject(new Error(`Wake agent is already pending for dispatch ${dispatchId}`));
        return;
      }
      const requestId = nextRequestID('wake_dispatch_agent');
      const key = `${keyPrefix}${requestId}`;
      const pending = { resolve: () => resolve(), reject };
      pendingActionsRef.current.set(key, pending);
      ws.send(JSON.stringify({
        cmd: 'wake_dispatch_agent',
        source_session_id: sourceSessionId,
        dispatch_id: dispatchId,
        request_id: requestId,
      }));
      window.setTimeout(() => {
        if (pendingActionsRef.current.get(key) !== pending) {
          return;
        }
        pendingActionsRef.current.delete(key);
        reject(new Error(`Wake agent timed out for dispatch ${dispatchId}`));
      }, 10_000);
    });
  }, [nextRequestID]);

  // Unregister a single session from daemon
  const sendUnregisterSession = useCallback((sessionId: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      if (!sessionId) {
        resolve();
        return;
      }

      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `unregister:${sessionId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(), reject });
      ws.send(JSON.stringify({ cmd: 'unregister', id: sessionId }));

      window.setTimeout(() => {
        if (!pendingActionsRef.current.has(key)) {
          return;
        }
        pendingActionsRef.current.delete(key);
        reject(new Error(`Session close timed out for ${sessionId}`));
      }, 10_000);
    });
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
    callbacksRef.current.onPRsUpdate(updatedPRs);

    ws.send(JSON.stringify({ cmd: 'pr_visited', id: prId }));
  }, []);

  const sendListWorktrees = useCallback((mainRepo: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'list_worktrees', main_repo: mainRepo }));
  }, []);

  const sendCreateWorktree = useCallback((mainRepo: string, branch: string, path?: string, startingFrom?: string, endpointId?: string): Promise<WorktreeActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const actionKey = `worktree_create_worktree_result_${endpointId || 'local'}`;
      pendingActionsRef.current.set(actionKey, { resolve, reject });

      setTimeout(() => {
        if (pendingActionsRef.current.has(actionKey)) {
          pendingActionsRef.current.delete(actionKey);
          reject(new Error('Create worktree timed out'));
        }
      }, GIT_WORKTREE_TIMEOUT_MS);

      ws.send(JSON.stringify({
        cmd: 'create_worktree',
        main_repo: mainRepo,
        branch,
        ...(path && { path }),
        ...(endpointId && { endpoint_id: endpointId }),
        ...(startingFrom && { starting_from: startingFrom }),
      }));
    });
  }, []);

  const sendDeleteWorktree = useCallback((path: string, endpointId?: string, options: DeleteWorktreeOptions = {}): Promise<WorktreeActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const actionKey = `worktree_delete_worktree_result_${endpointId || 'local'}`;
      pendingActionsRef.current.set(actionKey, { resolve, reject });

      setTimeout(() => {
        if (pendingActionsRef.current.has(actionKey)) {
          pendingActionsRef.current.delete(actionKey);
          reject(new Error('Delete worktree timed out'));
        }
      }, GIT_WORKTREE_TIMEOUT_MS);

      ws.send(JSON.stringify({
        cmd: 'delete_worktree',
        path,
        ...(endpointId && { endpoint_id: endpointId }),
        ...(options.force && { force: true }),
      }));
    });
  }, []);

  // Set a setting value with optimistic update
  const sendSetSetting = useCallback((key: string, value: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    // Optimistic update
    settingsRef.current = { ...settingsRef.current, [key]: value };
    callbacksRef.current.onSettingsUpdate?.(settingsRef.current);

    ws.send(JSON.stringify({ cmd: 'set_setting', key, value }));
  }, []);

  const sendListPlugins = useCallback((): Promise<PluginListResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = 'list_plugins';
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'list_plugins' }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('List plugins timed out'));
        }
      }, 10000);
    });
  }, []);

  const sendInstallPlugin = useCallback((source: string): Promise<PluginActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = 'plugin_action:install:pending';
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'install_plugin', source }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Install plugin timed out'));
        }
      }, 60000);
    });
  }, []);

  const sendRemovePlugin = useCallback((name: string): Promise<PluginActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `plugin_action:remove:${name}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'remove_plugin', name }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Remove plugin timed out'));
        }
      }, 30000);
    });
  }, []);

  const sendSetPluginPriority = useCallback((name: string, priority: number): Promise<PluginActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `plugin_action:set_priority:${name}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'set_plugin_priority', name, priority }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Set plugin priority timed out'));
        }
      }, 30000);
    });
  }, []);

  const sendAddEndpoint = useCallback((name: string, sshTarget: string, profile?: string): Promise<EndpointActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      if (hasPendingEndpointAction()) {
        reject(new Error('Another endpoint action is already in progress'));
        return;
      }
      const key = 'endpoint_action:add:pending';
      pendingActionsRef.current.set(key, { resolve, reject });
      const payload: Record<string, unknown> = { cmd: 'add_endpoint', name, ssh_target: sshTarget };
      const trimmed = (profile ?? '').trim();
      if (trimmed !== '') {
        payload.profile = trimmed;
      }
      ws.send(JSON.stringify(payload));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Add endpoint timed out'));
        }
      }, 30000);
    });
  }, [hasPendingEndpointAction]);

  const sendUpdateEndpoint = useCallback((
    endpointId: string,
    updates: { name?: string; ssh_target?: string; enabled?: boolean; profile?: string }
  ): Promise<EndpointActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      if (hasPendingEndpointAction()) {
        reject(new Error('Another endpoint action is already in progress'));
        return;
      }
      const key = `endpoint_action:update:${endpointId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'update_endpoint', endpoint_id: endpointId, ...updates }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Update endpoint timed out'));
        }
      }, 30000);
    });
  }, [hasPendingEndpointAction]);

  const sendRemoveEndpoint = useCallback((endpointId: string): Promise<EndpointActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      if (hasPendingEndpointAction()) {
        reject(new Error('Another endpoint action is already in progress'));
        return;
      }
      const key = `endpoint_action:remove:${endpointId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'remove_endpoint', endpoint_id: endpointId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Remove endpoint timed out'));
        }
      }, 30000);
    });
  }, [hasPendingEndpointAction]);

  const sendSetEndpointRemoteWeb = useCallback((endpointId: string, enabled: boolean): Promise<EndpointActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      if (hasPendingEndpointAction()) {
        reject(new Error('Another endpoint action is already in progress'));
        return;
      }
      const key = `endpoint_action:remote_web:${endpointId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'set_endpoint_remote_web', endpoint_id: endpointId, enabled }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Set endpoint remote web timed out'));
        }
      }, 30000);
    });
  }, [hasPendingEndpointAction]);

  const sendBootstrapEndpoint = useCallback((endpointId: string): Promise<EndpointActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      if (hasPendingEndpointAction()) {
        reject(new Error('Another endpoint action is already in progress'));
        return;
      }
      const key = `endpoint_action:bootstrap:${endpointId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'bootstrap_endpoint', endpoint_id: endpointId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Bootstrap endpoint timed out'));
        }
      }, 60000);
    });
  }, [hasPendingEndpointAction]);

  const sendListEndpoints = useCallback((): Promise<DaemonEndpoint[]> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = 'list_endpoints';
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'list_endpoints' }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('List endpoints timed out'));
        }
      }, 10000);
    });
  }, []);

  const sendListWorkspaceContexts = useCallback((): Promise<DaemonWorkspaceContext[]> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('workspace_context_list');
      const key = `workspace_context_list:${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'workspace_context_list', request_id: requestId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Workspace context list timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  // Get recent locations from daemon
  const sendGetRecentLocations = useCallback((endpointId?: string, limit?: number): Promise<RecentLocationsResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const requestId = nextRequestID('recent_locations');
      const key = `get_recent_locations_${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'get_recent_locations',
        ...(endpointId && { endpoint_id: endpointId }),
        ...(limit && { limit }),
        request_id: requestId,
      }));

      // Timeout after 10 seconds
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get recent locations timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  const sendBrowseDirectory = useCallback((inputPath: string, endpointId?: string): Promise<BrowseDirectoryResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const requestId = nextRequestID('browse_directory');
      const key = `browse_directory_${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'browse_directory',
        input_path: inputPath,
        ...(endpointId && { endpoint_id: endpointId }),
        request_id: requestId,
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Browse directory timed out'));
        }
      }, GIT_METADATA_TIMEOUT_MS);
    });
  }, [nextRequestID]);

  const sendInspectPath = useCallback((path: string, endpointId?: string): Promise<InspectPathResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const requestId = nextRequestID('inspect_path');
      const key = `inspect_path_${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({
        cmd: 'inspect_path',
        path,
        ...(endpointId && { endpoint_id: endpointId }),
        request_id: requestId,
      }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Inspect path timed out'));
        }
      }, GIT_METADATA_TIMEOUT_MS);
    });
  }, [nextRequestID]);

  // Create worktree from existing branch
  const sendCreateWorktreeFromBranch = useCallback((mainRepo: string, branch: string, path?: string): Promise<WorktreeActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const actionKey = 'worktree_create_worktree_result_local';
      pendingActionsRef.current.set(actionKey, { resolve, reject });

      setTimeout(() => {
        if (pendingActionsRef.current.has(actionKey)) {
          pendingActionsRef.current.delete(actionKey);
          reject(new Error('Create worktree from branch timed out'));
        }
      }, GIT_WORKTREE_TIMEOUT_MS);

      ws.send(JSON.stringify({
        cmd: 'create_worktree_from_branch',
        main_repo: mainRepo,
        branch,
        ...(path && { path }),
      }));
    });
  }, []);

  // Fetch all remotes
  const sendFetchRemotes = useCallback((repo: string): Promise<FetchRemotesResult> => {
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
      }, GIT_NETWORK_TIMEOUT_MS);
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

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Ensure repo timed out'));
        }
      }, GIT_CLONE_TIMEOUT_MS);
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

  // Track ordinary UI selection separately from the delayed long-run review
  // signal so `attn open` always targets the session currently shown.
  const sendSessionSelected = useCallback((id: string) => {
    selectedSessionRef.current = id;
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'session_selected', id }));
  }, []);

  const sendWorkspaceSelected = useCallback((workspaceId: string) => {
    selectedWorkspaceRef.current = workspaceId;
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'workspace_selected', workspace_id: workspaceId }));
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
      }, GIT_DIFF_TIMEOUT_MS);
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
      }, GIT_DIFF_TIMEOUT_MS);
    });

    branchDiffInFlightRef.current.set(key, request);
    return request;
  }, []);

  // Get repo info
  const getRepoInfo = useCallback((repo: string, endpointId?: string): Promise<RepoInfoResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }

      const key = `repo_info_${endpointId || 'local'}_${repo}`;
      pendingActionsRef.current.set(key, { resolve, reject });

      ws.send(JSON.stringify({ cmd: 'get_repo_info', repo, ...(endpointId && { endpoint_id: endpointId }) }));

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('get_repo_info timeout'));
        }
      }, GIT_METADATA_TIMEOUT_MS);
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

  const sendStartReviewLoop = useCallback((
    sessionId: string,
    prompt: string,
    iterationLimit: number,
    presetId?: string
  ): Promise<ReviewLoopActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `start_review_loop_${sessionId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({
        cmd: 'start_review_loop',
        session_id: sessionId,
        prompt,
        iteration_limit: iterationLimit,
        ...(presetId ? { preset_id: presetId } : {}),
      }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Start review loop timed out'));
        }
      }, 15000);
    });
  }, []);

  const sendStopReviewLoop = useCallback((sessionId: string): Promise<ReviewLoopActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `stop_review_loop_${sessionId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'stop_review_loop', session_id: sessionId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Stop review loop timed out'));
        }
      }, 15000);
    });
  }, []);

  const getReviewLoopState = useCallback((sessionId: string): Promise<ReviewLoopActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `get_review_loop_${sessionId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'get_review_loop_state', session_id: sessionId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get review loop state timed out'));
        }
      }, 10000);
    });
  }, []);

  const getReviewLoopRun = useCallback((loopId: string): Promise<ReviewLoopActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `show_review_loop_${loopId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'get_review_loop_run', loop_id: loopId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get review loop run timed out'));
        }
      }, 10000);
    });
  }, []);

  const setReviewLoopIterationLimit = useCallback((sessionId: string, iterationLimit: number): Promise<ReviewLoopActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `set_iterations_review_loop_${sessionId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({
        cmd: 'set_review_loop_iteration_limit',
        session_id: sessionId,
        iteration_limit: iterationLimit,
      }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Set review loop iteration limit timed out'));
        }
      }, 10000);
    });
  }, []);

  const answerReviewLoop = useCallback((loopId: string, interactionId: string, answer: string): Promise<ReviewLoopActionResult> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const key = `answer_review_loop_${loopId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({
        cmd: 'answer_review_loop',
        loop_id: loopId,
        interaction_id: interactionId,
        answer,
      }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Answer review loop timed out'));
        }
      }, 15000);
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
    gitOperations,
    clearWarnings,
    retryConnection,
    sendPRAction,
    getScreenSnapshot,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendMuteWorkspace,
    sendRefreshPRs,
    sendFetchPRDetails,
    sendClearSessions,
    sendUnregisterSession,
    sendRegisterWorkspace,
    sendUnregisterWorkspace,
    sendRenameSession,
    sendRenameWorkspace,
    sendSetChiefOfStaff,
    sendWakeDispatchAgent,
    sendPRVisited,
    sendListWorktrees,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendSetSetting,
    sendListPlugins,
    sendInstallPlugin,
    sendRemovePlugin,
    sendSetPluginPriority,
    sendAddEndpoint,
    sendUpdateEndpoint,
    sendRemoveEndpoint,
    sendSetEndpointRemoteWeb,
    sendBootstrapEndpoint,
    sendListEndpoints,
    sendListWorkspaceContexts,
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchRemotes,
    sendEnsureRepo,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendSessionSelected,
    sendWorkspaceSelected,
    sendSessionVisualized,
    sendWorkspaceGet,
    sendWorkspaceAddSessionPane,
    sendWorkspaceClosePane,
    sendWorkspaceFocusPane,
    sendWorkspaceRenamePane,
    sendWorkspaceSetSplitRatio,
    sendWorkspaceUndockTile,
    sendWorkspaceUpdateTile,
    sendWorkspaceMoveLeaf,
    sendWorkspaceMoveLeafToWorkspace,
    tileContents,
    requestTileContent,
    sendRuntimeInput: sendPtyInput,
    isRuntimeAttached,
    sendGetFileDiff,
    sendGetBranchDiffFiles,
    getRepoInfo,
    getReviewState,
    getReviewLoopRun,
    getReviewLoopState,
    markFileViewed,
    sendAddComment,
    sendUpdateComment,
    sendResolveComment,
    sendDeleteComment,
    sendGetComments,
    sendStartReviewLoop,
    sendStopReviewLoop,
    answerReviewLoop,
    setReviewLoopIterationLimit,
  };
}
