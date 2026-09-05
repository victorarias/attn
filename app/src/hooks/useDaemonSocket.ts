import { handleDelegationDaemonEvent, type DelegationSettingsState, type DelegationModelCatalog } from './daemonDelegationEvents';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';
import type { DelegationPreferences } from '../types/generated';
import { useEffect, useRef, useCallback, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '@tauri-apps/api/core';
import type {
  Session as GeneratedSession,
  Workspace as GeneratedWorkspaceSnapshot,
  PR as GeneratedPR,
  Worktree as GeneratedWorktree,
  PluginInfo as GeneratedPluginInfo,
  AppRegistryEntry as GeneratedAppRegistryEntry,
  ViewElement as GeneratedAppViewInfo,
  PluginIssue as GeneratedPluginIssue,
  GitOperation as GeneratedGitOperation,
  Endpoint as GeneratedEndpoint,
  RepoState as GeneratedRepoState,
  AuthorState as GeneratedAuthorState,
  WebSocketEvent as GeneratedWebSocketEvent,
  RecentLocation as GeneratedRecentLocation,
  WorkflowRun as GeneratedWorkflowRun,
  WarningElement as GeneratedWarning,
  WorkspaceContext as GeneratedWorkspaceContext,
  Task as GeneratedTask,
  Notification as GeneratedNotification,
  Seed as GeneratedSeed,
  SeedArtifact as GeneratedSeedArtifact,
  SeedArtifactReference as GeneratedSeedArtifactReference,
  DelegateResult as GeneratedDelegateResult,
  SeedArtifactTargetResult,
  SeedArtifactTransferResult,
  SeedDocument as GeneratedSeedDocument,
  CrewMember as GeneratedCrewMember,
  MarkdownAnnotation,
  Presentation,
  PresentationRound,
  PresentationComment,
  PresentCommentInput,
  PRRole,
  HeatState,
  AutomationDefinitionSummary,
  AutomationRunSummary,
  AttachBlock as GeneratedAttachBlock,
  GardenReview as GeneratedGardenReview,
  SeedSendToChiefResult as GeneratedSeedSendToChiefResult,
} from '../types/generated';
import type { SessionMessageWindowStatus } from './daemonSessionAnnotationEvents';
import { noteTerminalInputTransport } from '../utils/terminalInputDiagnostics';
import {
  emitPtyEvent,
  setPtyBackend,
  type PtyAttachArgs,
  type PtyAttachPolicy,
  type PtyPixelGeometry,
  type PtySpawnArgs,
} from '../pty/bridge';
import {
  classifyAttachRestore,
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
  isAlreadyExistsError,
} from '../pty/runtimeLifecycle';
import { createPtyTransportState } from '../pty/transportState';
import { enqueuePerKey } from '../pty/attachQueue';
import { parseLayoutJSON, tileContentKey, tileIdsFromLayoutJSON, type TerminalDockEdge, type TileContentState } from '../types/workspace';
import { isSuspiciousTerminalSize } from '../utils/terminalDebug';
import { crewDisplayName } from '../utils/crewName';
import { collectWorkspaceLayoutDiagnostics } from '../utils/workspaceDiagnostics';
import { recordDiag, recordLayout } from '../utils/terminalDiagnosticsLog';
import { recordPtyCommand, recordWsBinaryPtyOutput, recordWsJsonParse } from '../utils/ptyPerf';
import { completeTerminalInputProbe, maybeStartTerminalInputProbe } from '../utils/terminalInputLatency';
import { decodeBinaryFrame } from '../pty/binaryPtyFrame';
import { kittyImageBlobFromResult, kittyImageCache } from '../utils/kittyImageCache';
import { resolveDaemonWebSocketURL, type DaemonEndpointProfile } from '../utils/daemonEndpoint';
import { handleAppDaemonEvent, type AppCommandResult } from './daemonAppEvents';
import { handleBusDaemonEvent, type BusStatus } from './daemonBusEvents';
import {
  handleAutoModeDaemonEvent,
  type AutoModePatternEdit,
  type AutoModeModelCatalog,
  type AutoModePromotion,
  type AutoModeState,
} from './daemonAutoModeEvents';
import { handleFsDaemonEvent } from './daemonFsEvents';
import { handleSeedArtifactDaemonEvent } from './daemonSeedArtifactEvents';
import { handleNotebookDaemonEvent } from './daemonNotebookEvents';
import {
  DocumentSubscriptions,
  documentSubscribePayload,
  type DocumentSubscriber,
} from './daemonDocumentEvents';
import {
  annotationToWire,
  handleSessionAnnotationDaemonEvent,
  type DaemonSessionAnnotation,
  type DaemonSessionMessage,
} from './daemonSessionAnnotationEvents';
import {
  handleMarkdownAnnotationDaemonEvent,
  markdownAnnotationKey,
} from './daemonMarkdownAnnotationEvents';
import type { MarkdownDocumentSource } from '../components/MarkdownReader/documentSource';
import type { MarkdownAnnotationsDestination } from '../components/MarkdownReader/annotations/transport';
import {
  pendingRequestKey,
  sendKeyedRequest as sendLastWriterWinsRequest,
  settlePendingRequest,
  type PendingKeyedRequests,
  type PendingRequests,
} from './daemonPendingRequests';
import { BUILD_PROFILE, daemonProfileMatches, fetchDaemonHealthProfile, profileMismatchMessage } from '../utils/buildProfile';
import { controlBrowserHost, serializeBrowserControlResultMessage } from '../browser/host';
import { useWorkflowRunsStore } from '../store/workflowRuns';
import { useConversationsStore, type AgentPromptMode } from '../store/conversations';
import { useAutoModePushStore } from '../store/autoMode';
import { useAutomationsStore } from '../store/automations';

export type DaemonSession = GeneratedSession;

export interface PastConversation {
  session_id: string;
  file: string;
  cwd: string;
  preview: string;
  modified: number;
  bytes: number;
  live: boolean;
}

export interface PastConversationsResult {
  conversations: PastConversation[];
  truncated: boolean;
}

export type Seed = GeneratedSeed;
export type SeedArtifact = GeneratedSeedArtifact;
export type SeedArtifactReference = GeneratedSeedArtifactReference;
export type SeedDocument = GeneratedSeedDocument;
export interface SeedHandoverOptions {
  seedId: string;
  expectedRev: number;
  expectedTenderSession: string;
  expectedTenderMember: string;
  sourceSessionId?: string;
  handoff?: string;
  agent?: string;
  model?: string;
  effort?: string;
  review?: SeedReviewActionContext;
}
export type SeedHandoverResult = GeneratedDelegateResult;
export interface SeedSendToChiefOptions {
  seedId: string;
  expectedRev: number;
  expectedTenderSession: string;
  expectedTenderMember: string;
  sourceSessionId?: string;
  guidance?: string;
  review?: SeedReviewActionContext;
}
export type SeedSendToChiefResult = GeneratedSeedSendToChiefResult;
export interface SeedReviewActionContext {
  reviewId: string;
  evidenceVersion: string;
}
export interface SeedReviewOverview {
  candidateCount: number;
  review?: GeneratedGardenReview;
}
export type GardenReview = GeneratedGardenReview;
export type GardenReviewItem = GeneratedGardenReview['items'][number];
export type CrewMember = GeneratedCrewMember;
export interface CrewSleepResult {
  member: string;
  sessionId?: string;
  alreadyAsleep: boolean;
  deliveryStatus?: string;
  detail?: string;
}
export type DaemonWorkspace = GeneratedWorkspaceSnapshot;
export type DaemonPR = GeneratedPR;
export type DaemonWorktree = GeneratedWorktree;
export type DaemonPlugin = GeneratedPluginInfo;
export type AppRegistryEntry = GeneratedAppRegistryEntry;
export type AppViewInfo = GeneratedAppViewInfo;
export type DaemonPluginIssue = GeneratedPluginIssue;
export type DaemonGitOperation = GeneratedGitOperation;
export type DaemonEndpoint = GeneratedEndpoint;
export type RepoState = GeneratedRepoState;
export type AuthorState = GeneratedAuthorState;
export type RecentLocation = GeneratedRecentLocation;
export type WorkflowRunState = GeneratedWorkflowRun;
export type DaemonSettings = Record<string, string>;
export type DaemonWarning = GeneratedWarning;
export type DaemonWorkspaceContext = GeneratedWorkspaceContext;
export interface SessionMessageWindow {
  messages: DaemonSessionMessage[];
  status: SessionMessageWindowStatus;
  detail?: string;
  truncated: boolean;
}
export interface SessionAnnotationSet {
  annotations: DaemonSessionAnnotation[];
  note: string;
  generation: number;
}
export interface DirectoryEntry {
  name: string;
  path: string;
  is_dir: boolean;
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

export { PRRole, HeatState };

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
  screen_snapshot?: string;
  screen_rows?: number;
  screen_cols?: number;
  last_seq?: number;
  cols?: number;
  rows?: number;
  pid?: number;
  running?: boolean;
  exit_code?: number;
  signal?: string;
  reason?: string;
  action?: string;
  repo?: string;
  number?: number;
  success?: boolean;
  error?: string;
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
  github_hosts?: string[];
  contexts?: DaemonWorkspaceContext[];
  review_id?: string;
  session_id?: string;
  content?: string;
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

export const PROTOCOL_VERSION = '285';
const MAX_PENDING_ATTACH_OUTPUTS = 512;

const CLIENT_INSTANCE_ID =
  globalThis.crypto?.randomUUID?.() ?? `client-${Math.random().toString(36).slice(2)}`;

export function explainEviction(reason: string, evictedAt: string): string {
  const when = new Date(evictedAt);
  const at = Number.isNaN(when.getTime()) ? '' : ` at ${when.toLocaleTimeString()}`;
  if (reason === 'client too slow') {
    return `Reconnected. The daemon dropped this window${at} because it fell behind on updates.`;
  }
  return `Reconnected. The daemon dropped this window${at}: ${reason}.`;
}

export class AutomationActionTimeoutError extends Error {}

export class AutomationActionError extends Error {
  readonly code: string;
  constructor(message: string, code: string) {
    super(message);
    this.name = 'AutomationActionError';
    this.code = code;
  }
}

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

export interface ScreenSnapshotResult {
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

interface WorkspaceActionResult {
  success: boolean;
  error?: string;
  final_leaf_id?: string;
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

export interface SessionExitInfo {
  id: string;
  exitCode: number;
  signal?: string;
}

export interface NotebookEntry {
  path: string;
  type?: string;
  title?: string;
  summary?: string;
  updated?: string;
  size: number;
}

export interface NotebookReadResult {
  path: string;
  content: string;
  hash: string;
}

export type Task = GeneratedTask;
export type DaemonNotification = GeneratedNotification;

export interface CriticalNotificationState {
  count: number;
  title: string;
}

function readCriticalState(data: Record<string, unknown>): CriticalNotificationState {
  const count = typeof data.unread_critical_count === 'number' ? data.unread_critical_count : 0;
  const title = typeof data.critical_title === 'string' ? data.critical_title : '';
  return { count, title };
}

export interface NotebookWriteResult {
  path: string;
  hash?: string;
  conflict: boolean;
  currentHash?: string;
}

export interface NotebookSendToChiefResult {
  path: string;
  nudged: boolean;
}

export interface FsEntry {
  path: string;
  name: string;
  isDir: boolean;
  size: number;
  modified?: string;
}

export interface FsReadResult {
  path: string;
  content: string;
  hash: string;
}

export interface FsReadAssetResult {
  path: string;
  mimeType: string;
  dataBase64: string;
}

export interface FsWriteResult {
  path: string;
  hash?: string;
  conflict: boolean;
  currentHash?: string;
}

export interface FsRenameResult {
  path: string;
  new_path: string;
}

export interface FsDeleteResult {
  path: string;
}

export interface FsExistsResult {
  path: string;
  exists: boolean;
}

export interface FsWatchResult {
  root: string;
}

export interface FsIndexResult {
  root: string;
  files: string[];
  truncated: boolean;
}

export interface RecentFile {
  path: string;
  source: string;
  lastAt: string;
  count: number;
  sessionId?: string;
}

interface UseDaemonSocketOptions {
  onSessionsUpdate: (sessions: DaemonSession[]) => void;
  onNotebookChanged?: (origin: string, paths: string[]) => void;
  onTasksChanged?: () => void;
  onNotificationsUpdated?: (unreadCount: number, critical: CriticalNotificationState) => void;
  onFsChanged?: (origin: string, paths: string[], root: string) => void;
  onSeedsUpdate?: (seeds: Seed[], total: number) => void;
  onAppsUpdate?: (apps: AppRegistryEntry[]) => void;
  onCrewUpdate?: (members: CrewMember[]) => void;
  onPresentationAdded?: (presentation: Presentation) => void;
  onPresentationUpdated?: (presentation: Presentation) => void;
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
const DEFAULT_REQUEST_TIMEOUT_MS = 10_000;
// Bus status is one aggregate pass over the whole event log. Measured on a copy of production, 209ms
// at 945k rows — so 30s is roughly a hundred times the worst real log.
const BUS_STATUS_TIMEOUT_MS = 30_000;
// An app command is bounded by the daemon, which abandons a handler at 60s and answers with a refusal.
// This sits past it so that refusal is what a view shows, not "timed out".
const APP_COMMAND_TIMEOUT_MS = 75_000;
const GIT_METADATA_TIMEOUT_MS = 30 * 60_000;
const GIT_DIFF_TIMEOUT_MS = 10 * 60_000;
const GIT_WORKTREE_TIMEOUT_MS = 30 * 60_000;
const GIT_NETWORK_TIMEOUT_MS = 30 * 60_000;
const GIT_CLONE_TIMEOUT_MS = 90 * 60_000;
const GITHUB_REFRESH_TIMEOUT_MS = 5 * 60_000;
const WORKSPACE_SESSIONS_CAPABILITY = 'workspace_sessions';
const BROWSER_HOST_CAPABILITY = 'browser_host';
const BINARY_PTY_OUTPUT_CAPABILITY = 'binary_pty_output';
// "Describe images to me": gates the kitty_placements feed. Deliberately not the same bit as
// binary_pty_output, which decides only how a blob TRAVELS — the hub relay takes its pixels as base64.
const KITTY_IMAGES_CAPABILITY = 'kitty_images';

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

export function useDaemonSocket({
  onSessionsUpdate,
  onNotebookChanged,
  onTasksChanged,
  onNotificationsUpdated,
  onFsChanged,
  onSeedsUpdate,
  onAppsUpdate,
  onCrewUpdate,
  onPresentationAdded,
  onPresentationUpdated,
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
    onNotebookChanged,
    onTasksChanged,
    onNotificationsUpdated,
    onFsChanged,
    onSeedsUpdate,
    onAppsUpdate,
    onCrewUpdate,
    onPresentationAdded,
    onPresentationUpdated,
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
    onSessionExited,
  });
  callbacksRef.current = {
    onSessionsUpdate,
    onNotebookChanged,
    onTasksChanged,
    onNotificationsUpdated,
    onFsChanged,
    onSeedsUpdate,
    onAppsUpdate,
    onCrewUpdate,
    onPresentationAdded,
    onPresentationUpdated,
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
    onSessionExited,
  };
  const reconnectTimeoutRef = useRef<number | null>(null);
  const reconnectDelayRef = useRef<number>(1000);
  const pendingActionsRef = useRef<PendingRequests>(new Map());
  const mdAnnotationsPendingRef = useRef<PendingKeyedRequests>(new Map());
  const sessionMessageListenersRef = useRef<Map<string, Set<() => void>>>(new Map());
  const mdAnnotationsGetInflightRef = useRef<Map<string, Promise<{
    annotations: MarkdownAnnotation[];
    generation: number;
  }>>>(new Map());
  const requestSequenceRef = useRef(0);
  const pendingOutboundCommandsRef = useRef<string[]>([]);
  const recoveryNoticeTimeoutRef = useRef<number | null>(null);
  const gitStatusSubscriptionRef = useRef<string | null>(null);
  const docSubscriptionsRef = useRef<DocumentSubscriptions | null>(null);
  const docSubscriptions = (docSubscriptionsRef.current ??= new DocumentSubscriptions());
  const ptyTransportRef = useRef(createPtyTransportState<AttachRequestContext>());
  const canceledAttachIdsRef = useRef(new Set<string>());
  const attachQueueRef = useRef(new Map<string, Promise<unknown>>());
  const selectedSessionRef = useRef<string | null>(null);
  const selectedWorkspaceRef = useRef<string | null>(null);
  const daemonInstanceIDRef = useRef<string>('');
  const hasReceivedInitialStateRef = useRef(false);
  const lastTerminalThemeRef = useRef<{
    foreground: string;
    background: string;
    cursor: string;
    ansi_palette: string[];
  } | null>(null);
  const profileMismatchRef = useRef<boolean>(false);
  const profileCheckedRef = useRef<boolean>(false);
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const [disconnectExplanation, setDisconnectExplanation] = useState<string | null>(null);
  const [connectionGeneration, setConnectionGeneration] = useState(0);
  const [hasReceivedInitialState, setHasReceivedInitialState] = useState(false);
  const [rateLimit, setRateLimit] = useState<RateLimitState | null>(null);
  const [warnings, setWarnings] = useState<DaemonWarning[]>([]);
  const [gitOperations, setGitOperations] = useState<Record<string, DaemonGitOperation>>({});
  const [tileContents, setTileContents] = useState<Record<string, TileContentState>>({});
  const [seedReviewOverview, setSeedReviewOverview] = useState<SeedReviewOverview>({ candidateCount: 0 });

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
      // command_error carries no correlation id, so this fails every registration in flight. Over-rejecting
      // beats the ten-second silent timeout that a failure used to surface as.
      case 'register_workspace':
        rejectPendingByPredicate((key) => key.startsWith('register_workspace:'), error);
        return;
      case 'unregister_workspace':
        rejectPendingByPredicate((key) => key.startsWith('unregister_workspace:'), error);
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
        rejectPendingByPredicate((key) => key === cmd || key.startsWith(`${cmd}:`), error);
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

  const sendKeyedRequest = useCallback(<T,>(
    key: string,
    payload: Record<string, unknown>,
    timeoutMessage: string,
    timeoutMs: number = DEFAULT_REQUEST_TIMEOUT_MS,
  ): Promise<T> => {
    return new Promise<T>((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      pendingActionsRef.current.set(key, { resolve: resolve as (value: unknown) => void, reject });
      ws.send(JSON.stringify(payload));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error(timeoutMessage));
        }
      }, timeoutMs);
    });
  }, []);

  const sendRequest = useCallback(<T,>(
    cmd: string,
    body: Record<string, unknown>,
    timeoutMessage: string,
    timeoutMs?: number,
  ): Promise<T> => {
    const requestId = nextRequestID(cmd);
    return sendKeyedRequest<T>(
      pendingRequestKey(cmd, requestId),
      { cmd, request_id: requestId, ...body },
      timeoutMessage,
      timeoutMs,
    );
  }, [nextRequestID, sendKeyedRequest]);

  const connect = useCallback(async () => {
    if (wsRef.current?.readyState === WebSocket.OPEN || wsRef.current?.readyState === WebSocket.CONNECTING) return;
    if (profileMismatchRef.current) {
      return;
    }

    await ensureDaemonRunning();

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
        console.warn('[Daemon] profile pre-check failed, proceeding without it:', err);
      }
    }

    let browserHostToken = '';
    let clientToken = import.meta.env.VITE_CLIENT_TOKEN ?? '';
    if (isTauri()) {
      try {
        browserHostToken = await invoke<string>('get_browser_host_token');
      } catch (error) {
        console.warn('[Daemon] Browser host authentication is unavailable:', error);
      }
      try {
        clientToken = await invoke<string>('get_client_token');
      } catch (error) {
        console.warn('[Daemon] Client token is unavailable:', error);
      }
    }

    const ws = new WebSocket(resolvedWsUrl);
    ws.binaryType = 'arraybuffer';

    const handleLivePtyOutput = (id: string, seq: number | undefined, payload: string | Uint8Array) => {
      const attachKey = `pty_attach_${id}`;
      if (pendingActionsRef.current.has(attachKey)) {
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
      setConnectionGeneration((prev) => prev + 1);
      reconnectDelayRef.current = 1000;
      reconnectAttemptsRef.current = 0;
      circuitOpenRef.current = false;
      if (circuitResetTimeoutRef.current) {
        clearTimeout(circuitResetTimeoutRef.current);
        circuitResetTimeoutRef.current = null;
      }

      ws.send(
        JSON.stringify({
          cmd: 'client_hello',
          client_kind: 'tauri-app',
          client_id: CLIENT_INSTANCE_ID,
          version: `protocol-${PROTOCOL_VERSION}`,
          capabilities: [
            WORKSPACE_SESSIONS_CAPABILITY,
            BINARY_PTY_OUTPUT_CAPABILITY,
            KITTY_IMAGES_CAPABILITY,
            ...(browserHostToken ? [BROWSER_HOST_CAPABILITY] : []),
          ],
          client_token: clientToken || undefined,
          browser_host_token: browserHostToken || undefined,
        }),
      );

      kittyImageCache.setSender((sessionId, imageId) => {
        if (ws.readyState !== WebSocket.OPEN) return false;
        ws.send(JSON.stringify({ cmd: 'get_kitty_image', id: sessionId, image_id: imageId }));
        return true;
      });

      if (gitStatusSubscriptionRef.current) {
        ws.send(JSON.stringify({ cmd: 'subscribe_git_status', directory: gitStatusSubscriptionRef.current }));
      }

      docSubscriptions.resubscribeAll((payload) => ws.send(JSON.stringify(payload)));

      if (selectedSessionRef.current) {
        ws.send(JSON.stringify({ cmd: 'session_selected', id: selectedSessionRef.current }));
      }

      for (const listeners of sessionMessageListenersRef.current.values()) {
        for (const listener of listeners) listener();
      }
      if (selectedWorkspaceRef.current) {
        ws.send(JSON.stringify({ cmd: 'workspace_selected', workspace_id: selectedWorkspaceRef.current }));
      }
    };

    ws.onmessage = (event) => {
      try {
        if (event.data instanceof ArrayBuffer) {
          const decodeStartedAt = performance.now();
          const frame = decodeBinaryFrame(event.data);
          if (!frame) {
            console.error('[Daemon] Dropping undecodable binary frame', event.data.byteLength);
            return;
          }
          if (frame.kind === 'kitty_image') {
            kittyImageCache.fill({
              sessionId: frame.id,
              imageId: frame.imageId,
              generation: frame.generation,
              width: frame.width,
              height: frame.height,
              format: frame.format,
              pixels: frame.pixels,
            });
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
            useDelegationPreferencesPush.getState().clear();
            if (
              data.daemon_instance_id &&
              daemonInstanceIDRef.current &&
              data.daemon_instance_id !== daemonInstanceIDRef.current
            ) {
              ptyTransportRef.current.clearStreamCaches();
            }
            daemonInstanceIDRef.current = data.daemon_instance_id || '';
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
              circuitOpenRef.current = true;
              ws.close();
              return;
            }
            const nextSessions = dedupeSessionsByID(data.sessions || []);
            sessionsRef.current = nextSessions;
            callbacksRef.current.onSessionsUpdate(nextSessions);
            callbacksRef.current.onSeedsUpdate?.(
              data.seeds || [],
              data.seeds_total ?? (data.seeds || []).length,
            );
            callbacksRef.current.onAppsUpdate?.(data.apps || []);
            callbacksRef.current.onCrewUpdate?.(data.crew || []);
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
            useAutomationsStore.getState().bumpChanged();
            flushQueuedCommands(ws);
            requestTileContentsForWorkspaces(ws, nextWorkspaces);
            if (lastTerminalThemeRef.current && ws.readyState === WebSocket.OPEN) {
              const theme = lastTerminalThemeRef.current;
              ws.send(JSON.stringify({
                cmd: 'set_terminal_theme',
                foreground: theme.foreground,
                background: theme.background,
                cursor: theme.cursor,
                ansi_palette: theme.ansi_palette,
              }));
            }
            if (ws.readyState === WebSocket.OPEN) {
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

          case 'session_context_window_cap_result': {
            if (typeof data.session_id === 'string') {
              const key = `session_context_cap:${data.session_id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve(undefined);
                } else {
                  pending.reject(new Error(data.error || 'Setting the context window cap failed'));
                }
              }
            }
            break;
          }

          case 'crew_updated':
            callbacksRef.current.onCrewUpdate?.(data.members || []);
            break;

          case 'garden_seeds_updated':
            callbacksRef.current.onSeedsUpdate?.(
              data.seeds || [],
              data.total ?? (data.seeds || []).length,
            );
            break;

          case 'garden_review_updated': {
            const review = data.review as GeneratedGardenReview | undefined;
            if (!review) break;
            setSeedReviewOverview((current) => {
              if (current.review && current.review.run.id !== review.run.id) {
                const currentCapturedAt = Date.parse(current.review.run.captured_at);
                const nextCapturedAt = Date.parse(review.run.captured_at);
                if (Number.isFinite(currentCapturedAt)
                  && Number.isFinite(nextCapturedAt)
                  && nextCapturedAt < currentCapturedAt) return current;
              }
              return {
                candidateCount: review.items.filter((item) => item.resolution === 'unresolved').length,
                review,
              };
            });
            break;
          }

          case 'seed_review_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') break;
            const key = pendingRequestKey(`seed_review_${data.operation || 'show'}`, requestId);
            const pending = pendingActionsRef.current.get(key);
            if (!pending) break;
            pendingActionsRef.current.delete(key);
            if (data.success) {
              const overview: SeedReviewOverview = {
                candidateCount: typeof data.candidate_count === 'number' ? data.candidate_count : 0,
                review: data.review as GeneratedGardenReview | undefined,
              };
              setSeedReviewOverview(overview);
              pending.resolve(overview);
            } else {
              pending.reject(new Error(data.error || 'Garden review failed'));
            }
            break;
          }

          case 'seed_review_draft_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') break;
            const key = pendingRequestKey('seed_review_draft', requestId);
            const pending = pendingActionsRef.current.get(key);
            if (!pending) break;
            pendingActionsRef.current.delete(key);
            if (data.success && typeof data.handoff === 'string') pending.resolve(data.handoff);
            else pending.reject(new Error(data.error || 'Drafting the handoff failed'));
            break;
          }

          case 'apps_updated':
            callbacksRef.current.onAppsUpdate?.(data.apps || []);
            break;

          case 'presentation_added':
            if (data.presentation) {
              callbacksRef.current.onPresentationAdded?.(data.presentation);
            }
            break;

          case 'presentation_updated':
            if (data.presentation) {
              callbacksRef.current.onPresentationUpdated?.(data.presentation);
            }
            break;

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

          case 'tasks_changed':
            callbacksRef.current.onTasksChanged?.();
            break;

          case 'notifications_updated':
            callbacksRef.current.onNotificationsUpdated?.(
              typeof data.unread_count === 'number' ? data.unread_count : 0,
              readCriticalState(data),
            );
            break;

          case 'open_markdown_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `open_markdown:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve({ workspaceId: data.workspace_id, tileId: data.tile_id });
            } else {
              pending.reject(new Error(data.error || 'Open markdown failed'));
            }
            break;
          }

          case 'open_seed_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `open_seed:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve({ workspaceId: data.workspace_id, tileId: data.tile_id });
            } else {
              pending.reject(new Error(data.error || 'Open seed failed'));
            }
            break;
          }

          case 'seed_document_get_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `seed_document_get:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success && data.document) {
              pending.resolve(data.document);
            } else {
              pending.reject(new Error(data.error || 'Seed document read failed'));
            }
            break;
          }

          case 'seed_transition_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') break;
            const key = `seed_transition:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) break;
            pendingActionsRef.current.delete(key);
            if (data.success && data.seed) pending.resolve(data.seed);
            else pending.reject(new Error(data.error || 'The move was refused'));
            break;
          }

          case 'seed_note_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') break;
            const key = `seed_note:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) break;
            pendingActionsRef.current.delete(key);
            if (data.success) pending.resolve(data.note);
            else pending.reject(new Error(data.error || 'The note was refused'));
            break;
          }

          case 'recent_files_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `recent_files:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve((data.files || []).map((file: Record<string, unknown>) => ({
                path: file.path as string,
                source: file.source as string,
                lastAt: file.last_at as string,
                count: (file.count as number) ?? 0,
                sessionId: file.session_id as string | undefined,
              })));
            } else {
              pending.reject(new Error(data.error || 'Recent files failed'));
            }
            break;
          }

          case 'seed_resume_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `seed_resume:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success && typeof data.session_id === 'string') {
              pending.resolve({
                sessionId: data.session_id,
                workspaceId: typeof data.workspace_id === 'string' ? data.workspace_id : undefined,
                alreadyRunning: data.already_running === true,
              });
            } else {
              pending.reject(new Error(data.error || 'Resuming the seed failed'));
            }
            break;
          }

          case 'seed_send_to_chief_result': {
            settlePendingRequest(
              pendingActionsRef.current,
              'seed_send_to_chief',
              data,
              (event) => event.result as SeedSendToChiefResult | undefined,
              'Sending the seed to Chief failed',
            );
            break;
          }

          case 'delegate_result': {
            settlePendingRequest(
              pendingActionsRef.current,
              'delegate',
              data,
              (event) => event.result as SeedHandoverResult | undefined,
              'Handover failed',
            );
            break;
          }

          case 'crew_wake_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `crew_wake:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success && typeof data.session_id === 'string') {
              pending.resolve({
                sessionId: data.session_id,
                alreadyAwake: data.already_awake === true,
              });
            } else {
              pending.reject(new Error(data.error || 'Waking the member failed'));
            }
            break;
          }

          case 'crew_sleep_result':
            settlePendingRequest(
              pendingActionsRef.current,
              'crew_sleep',
              data,
              (event): CrewSleepResult | undefined => {
                if (typeof event.member !== 'string') return undefined;
                return {
                  member: event.member,
                  ...(typeof event.session_id === 'string' ? { sessionId: event.session_id } : {}),
                  alreadyAsleep: event.already_asleep === true,
                  ...(typeof event.delivery_status === 'string' ? { deliveryStatus: event.delivery_status } : {}),
                  ...(typeof event.detail === 'string' ? { detail: event.detail } : {}),
                };
              },
              'Asking the member to sleep failed',
            );
            break;

          case 'task_list_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `task_list:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve(data.tasks || []);
            } else {
              pending.reject(new Error(data.error || 'Notebook task list failed'));
            }
            break;
          }

          case 'task_retry_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `task_retry:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve(data.task ?? null);
            } else {
              pending.reject(new Error(data.error || 'Notebook task retry failed'));
            }
            break;
          }

          case 'notification_list_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `notification_list:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve({
                notifications: (data.notifications || []) as DaemonNotification[],
                unreadCount: typeof data.unread_count === 'number' ? data.unread_count : 0,
                critical: readCriticalState(data),
              });
            } else {
              pending.reject(new Error(data.error || 'Notification list failed'));
            }
            break;
          }

          case 'notification_mark_read_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') {
              break;
            }
            const key = `notification_mark_read:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) {
              break;
            }
            pendingActionsRef.current.delete(key);
            if (data.success) {
              pending.resolve(typeof data.unread_count === 'number' ? data.unread_count : 0);
            } else {
              pending.reject(new Error(data.error || 'Notification mark-read failed'));
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

          case 'reload_session_result': {
            if (data.id) {
              const key = `reload_session:${data.id}`;
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve({ success: true });
                } else {
                  pending.reject(new Error(data.error ?? 'Reload failed'));
                }
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
              const restorePlan = classifyAttachRestore(data, attachContext);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if (data.success) {
                  pending.resolve({
                    success: true,
                    snapshot: data.snapshot,
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
                const attachEffects = planAttachResultEffects({
                  attachResult: data,
                  restorePlan,
                  previousSeq: ptyTransportRef.current.getLastSeq(data.id),
                  queuedOutputs: ptyTransportRef.current.getQueuedAttachOutputs(data.id),
                });
                const snapshotGeometry = restorePlan.hasSnapshot
                  && typeof restorePlan.restoreCols === 'number'
                  && typeof restorePlan.restoreRows === 'number'
                  ? { cols: restorePlan.restoreCols, rows: restorePlan.restoreRows }
                  : null;
                const relaunchFallbackGeometry = attachContext?.policy === 'relaunch_restore'
                  && typeof data.cols === 'number'
                  && typeof data.rows === 'number'
                  ? { cols: data.cols, rows: data.rows }
                  : null;
                const restoreGeometry = snapshotGeometry || relaunchFallbackGeometry;
                if (restoreGeometry) {
                  emitPtyEvent({
                    event: 'local_resize',
                    id: data.id,
                    cols: restoreGeometry.cols,
                    rows: restoreGeometry.rows,
                    source: 'attach_restore',
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
                const restoreWasEmitted = attachEffects.restoreAction.kind === 'ghostty_snapshot';
                if (attachEffects.restoreAction.kind === 'ghostty_snapshot') {
                  emitPtyEvent({
                    event: 'restore_snapshot',
                    id: data.id,
                    data: attachEffects.restoreAction.data,
                  });
                  const snapshotBlocks = data.snapshot?.blocks;
                  if (snapshotBlocks && snapshotBlocks.length > 0) {
                    emitPtyEvent({
                      event: 'seed_blocks',
                      id: data.id,
                      blocks: snapshotBlocks.map((b: GeneratedAttachBlock) => ({
                        id: b.id,
                        pending: b.pending,
                        promptRow: b.prompt_row,
                        inputRow: b.input_row,
                        inputCol: b.input_col,
                        outputStartRow: b.output_start_row,
                        endRow: b.end_row,
                        command: b.command,
                        exitCode: b.exit_code,
                      })),
                    });
                  }
                  // Always emitted, including with no placements: a restore resets the model, so absence
                  // here means "this session is showing no images" — otherwise an image would survive it.
                  emitPtyEvent({
                    event: 'seed_placements',
                    id: data.id,
                    placements: data.snapshot?.placements ?? [],
                  });
                }
                if (attachEffects.queuedOutputsToEmit.length > 0) {
                  ptyTransportRef.current.clearQueuedAttachOutputs(data.id);
                  for (const chunk of attachEffects.queuedOutputsToEmit) {
                    emitPtyEvent({ event: 'data', id: data.id, data: chunk.data, seq: chunk.seq });
                  }
                }
                if (restoreWasEmitted) {
                  emitPtyEvent({
                    event: 'restore_complete',
                    id: data.id,
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
                    screenSnapshot: data.screen_snapshot ?? undefined,
                    screenCols: data.screen_cols,
                    screenRows: data.screen_rows,
                    lastSeq: typeof data.last_seq === 'number' ? data.last_seq : 0,
                  };
                  pending.resolve(result);
                } else {
                  pending.resolve(null);
                }
              }
            }
            break;
          }

          case 'pty_output': {
            if (data.id && data.data) {
              handleLivePtyOutput(data.id, data.seq, data.data);
            }
            break;
          }

          case 'pty_input_probe_result': {
            if (data.id && data.probe_id && typeof data.write_duration_us === 'number') {
              completeTerminalInputProbe({
                id: data.id,
                probe_id: data.probe_id,
                success: data.success === true,
                write_duration_us: data.write_duration_us,
                error: data.error,
              });
            }
            break;
          }

          case 'past_conversations_result':
            settlePendingRequest(
              pendingActionsRef.current,
              'list_past_conversations',
              data,
              (event): PastConversationsResult => ({
                conversations: Array.isArray(event.conversations) ? (event.conversations as PastConversation[]) : [],
                truncated: event.truncated === true,
              }),
              'Listing past conversations failed',
            );
            break;

          case 'agent_event': {
            if (data.id && typeof data.kind === 'string') {
              useConversationsStore.getState().applyEnvelope(
                data.id,
                typeof data.seq === 'number' ? data.seq : 0,
                data.kind,
                (data.body ?? {}) as Record<string, unknown>,
              );
            }
            break;
          }

          case 'kitty_placements': {
            if (data.id) {
              emitPtyEvent({
                event: 'placements',
                id: data.id,
                seq: typeof data.seq === 'number' ? data.seq : 0,
                placements: data.placements ?? [],
              });
            }
            break;
          }

          case 'kitty_image_result': {
            if (!data.id || typeof data.image_id !== 'number') break;
            const blob = kittyImageBlobFromResult(data);
            if (blob) {
              kittyImageCache.fill(blob);
            } else {
              kittyImageCache.markFailed(
                data.id,
                data.image_id,
                data.error || 'answer carried no pixels',
              );
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
              useConversationsStore.getState().hostExited(data.id);
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

          case 'runtime_respawned':
            // The daemon replaced the runtime in place; restore its new stream and screen.
            if (data.id) {
              recordDiag({ kind: 'attach', session: data.id, reason: 'runtime_respawned' });
              emitPtyEvent({ event: 'reset', id: data.id, reason: 'respawn' });
              ptyTransportRef.current.clearRuntimeStream(data.id);
              ws.send(JSON.stringify({ cmd: 'attach_session', id: data.id, attach_policy: 'relaunch_restore' }));
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

          case 'get_presentations_result': {
            const pending = pendingActionsRef.current.get('get_presentations');
            if (pending) {
              pendingActionsRef.current.delete('get_presentations');
              if (data.success) {
                pending.resolve(data.presentations || []);
              } else {
                pending.reject(new Error(data.error || 'Failed to load presentations'));
              }
            }
            break;
          }

          case 'get_presentation_round_result': {
            const pending = pendingActionsRef.current.get('get_presentation_round');
            if (pending) {
              pendingActionsRef.current.delete('get_presentation_round');
              if (data.success) {
                pending.resolve({
                  presentation: data.presentation,
                  round: data.round,
                  comments: data.comments || [],
                  repoHeadSha: data.repo_head_sha,
                });
              } else {
                pending.reject(new Error(data.error || 'Failed to load presentation round'));
              }
            }
            break;
          }

          case 'present_submit_round_result': {
            const pending = pendingActionsRef.current.get('present_submit_round');
            if (pending) {
              pendingActionsRef.current.delete('present_submit_round');
              if (data.success) {
                pending.resolve({ roundId: data.round_id });
              } else {
                pending.reject(new Error(data.error || 'Failed to submit presentation round'));
              }
            }
            break;
          }

          case 'present_close_result': {
            const pending = pendingActionsRef.current.get('present_close');
            if (pending) {
              pendingActionsRef.current.delete('present_close');
              if (data.success) {
                pending.resolve({ presentationId: data.presentation_id });
              } else {
                pending.reject(new Error(data.error || 'Failed to close presentation'));
              }
            }
            break;
          }

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
              if (resetAt > new Date()) {
                setRateLimit({
                  resource: data.rate_limit_resource,
                  resetAt,
                });
                const msUntilReset = resetAt.getTime() - Date.now();
                setTimeout(() => {
                  setRateLimit(null);
                }, msUntilReset + 1000);
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
            const key = `get_file_diff_${data.request_id}`;
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

          case 'get_repo_info_result': {
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

          case 'workflow_run_updated': {
            const run = (data as any).run;
            if (run) useWorkflowRunsStore.getState().upsertWorkflowRun(run);
            break;
          }

          case 'workflow_action_result': {
            const action = (data as any).action || '';
            const runId = (data as any).run_id || '';
            const run = (data as any).run ?? null;
            const runs = (data as any).runs ?? [];
            if (run) useWorkflowRunsStore.getState().upsertWorkflowRun(run);
            if (Array.isArray(runs) && runs.length > 0) useWorkflowRunsStore.getState().upsertWorkflowRuns(runs);
            let key: string | null = null;
            if (action === 'get') key = `workflow_run_get_${runId}`;
            else if (action === 'list') key = 'workflow_run_list';
            else if (action === 'cancel') key = `workflow_run_cancel_${runId}`;
            if (key) {
              const pending = pendingActionsRef.current.get(key);
              if (pending) {
                pendingActionsRef.current.delete(key);
                if ((data as any).success) {
                  pending.resolve({ success: true, run, runs });
                } else {
                  pending.reject(new Error((data as any).error || 'Workflow action failed'));
                }
              }
            }
            break;
          }

          case 'automation_apply_result':
          case 'automation_validate_result':
          case 'automation_definitions_result':
          case 'automation_definition_result':
          case 'automation_runs_result':
          case 'automation_run_result':
          case 'automation_set_enabled_result':
          case 'automation_delete_result':
          case 'automation_cleanup_result': {
            const requestId = data.request_id;
            if (typeof requestId !== 'string') break;
            const key = `automation:${requestId}`;
            const pending = pendingActionsRef.current.get(key);
            if (!pending) break;
            pendingActionsRef.current.delete(key);
            if ((data as any).success) {
              pending.resolve(data);
            } else {
              const errorCode = (data as any).error_code;
              pending.reject(
                new AutomationActionError(
                  (data as any).error || 'Automation action failed',
                  typeof errorCode === 'string' ? errorCode : '',
                ),
              );
            }
            break;
          }

          case 'automations_changed': {
            useAutomationsStore.getState().bumpChanged();
            break;
          }

          case 'client_eviction_notice': {
            console.warn(
              `[Daemon] Previous connection was dropped at ${data.evicted_at}: ${data.reason} ` +
              `(${data.undelivered_messages} messages undelivered)`,
            );
            setDisconnectExplanation(
              explainEviction(data.reason ?? 'no reason given', data.evicted_at ?? ''),
            );
            break;
          }

          case 'command_error':
            if (data.error_code === 'unauthorized_client') {
              console.error('[Daemon] Client token refused:', data.error);
              setConnectionError(data.error || 'The daemon refused this client.');
              circuitOpenRef.current = true;
              break;
            }
            if (data.error === 'daemon_recovering') {
              console.debug('[Daemon] Command deferred while daemon recovers:', data.cmd);
              rejectPendingForCommand(data.cmd, 'Daemon is recovering. Please retry in a moment.');
              showRecoveringNoticeForCommand(data.cmd);
              break;
            }
            console.error('[Daemon] Command error:', data.cmd, data.error);
            rejectPendingForCommand(data.cmd, data.error || `Command ${data.cmd || ''} failed`);
            break;

          default: {
            const pending = pendingActionsRef.current;
            if (handleSeedArtifactDaemonEvent(data, pending)) break;
            if (handleFsDaemonEvent(data, { pending, onFsChanged: callbacksRef.current.onFsChanged })) break;
            if (handleNotebookDaemonEvent(data, { pending, onNotebookChanged: callbacksRef.current.onNotebookChanged })) break;
            if (handleMarkdownAnnotationDaemonEvent(data, mdAnnotationsPendingRef.current)) break;
            if (handleSessionAnnotationDaemonEvent(data, pending, (sessionId) => {
              for (const listener of sessionMessageListenersRef.current.get(sessionId) ?? []) {
                listener();
              }
            })) break;
            if (handleBusDaemonEvent(data, pending)) break;
            if (handleAppDaemonEvent(data, pending)) break;
            if (docSubscriptions.handleEvent(data)) break;
            if (handleDelegationDaemonEvent(data, pending)) break;
            if (handleAutoModeDaemonEvent(data, pending)) break;
            break;
          }
        }
      } catch (err) {
        console.error('[Daemon] Parse error:', err);
      }
    };

    ws.onclose = () => {
      wsRef.current = null;
      hasReceivedInitialStateRef.current = false;
      canceledAttachIdsRef.current.clear();
      docSubscriptions.markDisconnected();
      useAutoModePushStore.getState().clear();
      useDelegationPreferencesPush.getState().clear();

      if (circuitOpenRef.current) {
        console.error('[Daemon] Circuit open, not retrying');
        return;
      }

      reconnectAttemptsRef.current++;
      let delay = reconnectDelayRef.current;
      if (reconnectAttemptsRef.current > MAX_RECONNECTS_BEFORE_PAUSE) {
        delay = MAX_RECONNECT_DELAY_MS;
        reconnectDelayRef.current = MAX_RECONNECT_DELAY_MS;
        setConnectionError('Daemon disconnected. Reconnecting...');
        console.error('[Daemon] High reconnect attempts; continuing retries in background');
      } else {
        reconnectDelayRef.current = Math.min(delay * 1.5, MAX_RECONNECT_DELAY_MS);
      }

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
      // Detach handlers before closing: onclose otherwise schedules a fresh reconnect timer synchronously,
      // which would fire after teardown with a stale WebSocket reference.
      const ws = wsRef.current;
      if (ws) {
        ws.onclose = null;
        ws.onerror = null;
        ws.onmessage = null;
        ws.onopen = null;
        ws.close();
      }
    };
  }, [connect]);

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
    const key = `pty_spawn_${args.id}`;
    return sendKeyedRequest<SpawnResult>(key, {
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
      ...(args.resume_conversation_file && { resume_conversation_file: args.resume_conversation_file }),
      ...(args.yolo_mode && { yolo_mode: args.yolo_mode }),
      ...(args.auto_mode !== undefined && { auto_mode: args.auto_mode }),
      ...(args.chief_of_staff && { chief_of_staff: args.chief_of_staff }),
      ...(args.spawned_from && { spawned_from: args.spawned_from }),
      ...(args.executable && { executable: args.executable }),
      ...(args.claude_executable && { claude_executable: args.claude_executable }),
      ...(args.codex_executable && { codex_executable: args.codex_executable }),
      ...(args.copilot_executable && { copilot_executable: args.copilot_executable }),
    }, 'Spawn session timed out', 30000);
  }, [sendKeyedRequest]);

  const sendReloadSession = useCallback((id: string, cols: number, rows: number): Promise<void> => {
    const key = `reload_session:${id}`;
    return sendKeyedRequest<void>(key, { cmd: 'reload_session', id, cols, rows }, 'Reload session timed out', 30000);
  }, [sendKeyedRequest]);

  const sendAttachSessionNow = useCallback((id: string, context?: AttachRequestContext): Promise<AttachResult> => {
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
        ...(context?.policy === 'revive'
          ? { cols: context.requestedCols, rows: context.requestedRows }
          : {}),
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

  // Attaches to the same session must not overlap (see attachQueue.ts): a second in-flight attach
  // overwrites the first's pending entry, resolves with the FIRST attach's result, and misclassifies its replay.
  const sendAttachSession = useCallback((id: string, context?: AttachRequestContext): Promise<AttachResult> => {
    return enqueuePerKey(attachQueueRef.current, id, () => sendAttachSessionNow(id, context));
  }, [sendAttachSessionNow]);

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
    const probeId = maybeStartTerminalInputProbe(id, source);
    if (source === 'user') {
      noteTerminalInputTransport(id, {
        socketState: ws?.readyState ?? null,
        initialStateReceived: hasReceivedInitialStateRef.current,
        probeId,
      });
    }
    recordPtyCommand('pty_input', id, data.length, source);
    sendOrQueueCommand(
      {
        cmd: 'pty_input',
        id,
        data,
        ...(source ? { source } : {}),
        ...(probeId ? { probe_id: probeId } : {}),
      },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  const sendTerminalPointerActivity = useCallback((id: string) => {
    const ws = wsRef.current;
    if (!hasReceivedInitialStateRef.current || !ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    ws.send(JSON.stringify({ cmd: 'terminal_pointer_activity', id }));
  }, []);

  const sendSetClientPresence = useCallback((presence: {
    visible: boolean;
    dashboardVisible: boolean;
    idleSeconds?: number;
  }) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    ws.send(JSON.stringify({
      cmd: 'set_client_presence',
      visible: presence.visible,
      dashboard_visible: presence.dashboardVisible,
      ...(presence.idleSeconds === undefined ? {} : { idle_seconds: presence.idleSeconds }),
    }));
  }, []);

  const sendAgentPrompt = useCallback((id: string, text: string, mode?: AgentPromptMode) => {
    sendOrQueueCommand(
      { cmd: 'agent_prompt', id, input_id: nextRequestID('agent-input'), text, ...(mode ? { mode } : {}) },
      { waitForInitialState: true },
    );
  }, [nextRequestID, sendOrQueueCommand]);

  const sendAgentToolDetail = useCallback((id: string, callId: string, full?: boolean) => {
    sendOrQueueCommand(
      { cmd: 'agent_tool_detail', id, call_id: callId, ...(full ? { full } : {}) },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  const sendAgentClearQueue = useCallback((id: string) => {
    sendOrQueueCommand({ cmd: 'agent_clear_queue', id }, { waitForInitialState: true });
  }, [sendOrQueueCommand]);

  const sendAgentAttach = useCallback((id: string) => {
    sendOrQueueCommand({ cmd: 'agent_attach', id }, { waitForInitialState: true });
  }, [sendOrQueueCommand]);

  const sendAgentHistory = useCallback((id: string, before: string) => {
    sendOrQueueCommand({ cmd: 'agent_history', id, before }, { waitForInitialState: true });
  }, [sendOrQueueCommand]);

  const sendAgentSetModel = useCallback((id: string, model: string) => {
    sendOrQueueCommand({ cmd: 'agent_set_model', id, model }, { waitForInitialState: true });
  }, [sendOrQueueCommand]);

  const sendListPastConversations = useCallback((): Promise<PastConversationsResult> => {
    return sendRequest<PastConversationsResult>(
      'list_past_conversations',
      {},
      'Listing past conversations timed out',
    );
  }, [sendRequest]);

  const sendBusStatusGet = useCallback((): Promise<BusStatus> => {
    return sendRequest<BusStatus>(
      'bus_status_get',
      {},
      'Reading the event bus timed out',
      BUS_STATUS_TIMEOUT_MS,
    );
  }, [sendRequest]);

  const sendBusSetConsumerEnabled = useCallback((
    consumer: string,
    enabled: boolean,
  ): Promise<{ consumer: string }> => {
    return sendRequest<{ consumer: string }>(
      'bus_set_consumer_enabled',
      { consumer, enabled },
      'Changing the consumer timed out',
    );
  }, [sendRequest]);

  const sendDelegationPreferencesGet = useCallback((): Promise<DelegationSettingsState> =>
    sendRequest('delegation_preferences_get', {}, 'Reading delegation preferences timed out'), [sendRequest]);
  const sendDelegationPreferencesSave = useCallback((preferences: DelegationPreferences): Promise<DelegationSettingsState> =>
    sendRequest('delegation_preferences_save', { preferences }, 'Saving delegation preferences timed out'), [sendRequest]);
  const sendDelegationModels = useCallback((harness: string): Promise<DelegationModelCatalog> =>
    sendRequest('delegation_models', { harness }, 'Discovering models timed out', 70_000), [sendRequest]);

  const sendAutoModeGet = useCallback((): Promise<AutoModeState> => {
    return sendRequest<AutoModeState>(
      'automode_get',
      {},
      'Reading auto mode timed out',
    );
  }, [sendRequest]);

  const sendAutoModePromote = useCallback((id: number): Promise<AutoModePromotion> => {
    return sendRequest<AutoModePromotion>(
      'automode_promote',
      { id },
      'Promoting the proposal timed out',
    );
  }, [sendRequest]);

  const sendAutoModeDiscard = useCallback((id: number): Promise<AutoModePromotion> => {
    return sendRequest<AutoModePromotion>(
      'automode_discard',
      { id },
      'Discarding the proposal timed out',
    );
  }, [sendRequest]);

  const sendAutoModePatternAdd = useCallback(
    (list: string, pattern: string): Promise<AutoModePatternEdit> => {
      return sendRequest<AutoModePatternEdit>(
        'automode_pattern_add',
        { list, pattern },
        'Adding the pattern timed out',
      );
    },
    [sendRequest],
  );

  const sendAutoModeModelSet = useCallback(
    (models: string[]): Promise<AutoModePatternEdit> => {
      return sendRequest<AutoModePatternEdit>(
        'automode_model_set',
        { models },
        'Saving the models timed out',
      );
    },
    [sendRequest],
  );

  // pi is asked per provider and each ask spawns a process, so this waits far
  // longer than a command the daemon answers on its own.
  const sendAutoModeModels = useCallback((): Promise<AutoModeModelCatalog> => {
    return sendRequest<AutoModeModelCatalog>(
      'automode_models',
      {},
      'Asking pi which models it can reach timed out',
      30000,
    );
  }, [sendRequest]);

  const sendAutoModeEnvSlot = useCallback(
    (slot: string, values: string[]): Promise<AutoModePatternEdit> => {
      return sendRequest<AutoModePatternEdit>(
        'automode_env_slot',
        { slot, values },
        'Saving what the classifier knows about this machine timed out',
      );
    },
    [sendRequest],
  );

  const sendAutoModeEnvNotes = useCallback(
    (notes: string[]): Promise<AutoModePatternEdit> => {
      return sendRequest<AutoModePatternEdit>(
        'automode_env_notes',
        { notes },
        'Saving your notes about this machine timed out',
      );
    },
    [sendRequest],
  );

  const sendAutoModePatternRemove = useCallback(
    (list: string, pattern: string): Promise<AutoModePatternEdit> => {
      return sendRequest<AutoModePatternEdit>(
        'automode_pattern_remove',
        { list, pattern },
        'Removing the pattern timed out',
      );
    },
    [sendRequest],
  );

  const sendSetTerminalTheme = useCallback((theme: {
    foreground: string;
    background: string;
    cursor: string;
    ansi_palette: string[];
  }) => {
    lastTerminalThemeRef.current = theme;
    sendOrQueueCommand(
      {
        cmd: 'set_terminal_theme',
        foreground: theme.foreground,
        background: theme.background,
        cursor: theme.cursor,
        ansi_palette: theme.ansi_palette,
      },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  /** The subscribe never goes through the outbound queue: `resubscribeAll` would
   * re-send it and the daemon refuses the second id as already open. */
  const subscribeDocuments = useCallback((subscriber: DocumentSubscriber) => {
    const registry = docSubscriptions;
    const id = registry.add(subscriber);
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(documentSubscribePayload(id, subscriber)));
      subscriber.onLive(true);
    } else {
      subscriber.onLive(false);
    }
    return () => {
      if (!registry.remove(id)) return;
      const open = wsRef.current;
      if (open && open.readyState === WebSocket.OPEN) {
        open.send(JSON.stringify({ cmd: 'doc_unsubscribe', subscription_id: id }));
      }
    };
  }, []);

  const sendAppViewCrash = useCallback((report: {
    app: string;
    view: string;
    versionId: number;
    tileId: string;
    error: string;
  }) => {
    sendOrQueueCommand({
      cmd: 'app_view_crash',
      app: report.app,
      view: report.view,
      version_id: report.versionId,
      tile_id: report.tileId,
      error: report.error,
    });
  }, [sendOrQueueCommand]);

  const sendAppCommand = useCallback((app: string, command: string, payload?: unknown): Promise<unknown> => {
    return sendRequest<AppCommandResult>(
      'app_command',
      {
        app,
        command,
        ...(payload === undefined ? {} : { payload: JSON.stringify(payload) }),
      },
      `${app} did not answer the command “${command}”`,
      APP_COMMAND_TIMEOUT_MS,
    ).then((result) => result.value);
  }, [sendRequest]);

  const sendPtyResize = useCallback((
    id: string,
    cols: number,
    rows: number,
    reason?: string,
    pixels?: PtyPixelGeometry,
  ) => {
    const suspiciousResize = isSuspiciousTerminalSize(cols, rows);
    if (suspiciousResize) {
      console.warn('[DaemonSocket] Sending suspicious PTY resize', { id, cols, rows, reason: reason || null });
    }
    recordPtyCommand('pty_resize', id, 0, null);
    const geometry = pixels && pixels.xpixel && pixels.ypixel
      ? { xpixel: Math.round(pixels.xpixel), ypixel: Math.round(pixels.ypixel) }
      : {};
    sendOrQueueCommand(
      { cmd: 'pty_resize', id, cols, rows, ...geometry },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  const reconcileAttachedRuntimeGeometry = useCallback((
    args: Pick<PtyAttachArgs, 'id' | 'cols' | 'rows' | 'shell'>,
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
    args: Pick<PtyAttachArgs, 'id' | 'cols' | 'rows' | 'shell' | 'agent' | 'reason' | 'xpixel' | 'ypixel'>,
    options: {
      policy: Extract<PtyAttachPolicy, 'relaunch_restore' | 'same_app_remount' | 'revive'>;
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
      sendPtyResize(args.id, args.cols, args.rows, args.reason || 'remount_hydrate', {
        xpixel: args.xpixel, ypixel: args.ypixel,
      });
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

      ws.send(JSON.stringify({
        cmd: 'kill_session',
        id,
        ...(signal && { signal }),
      }));

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

  const sendWorkspaceDockTile = useCallback((
    workspaceId: string,
    tileId: string,
    tileKind: string,
    options: { anchorPaneId?: string; edge?: TerminalDockEdge; ratio?: number; tileParams?: string } = {},
  ) => {
    return sendWorkspaceCommand(
      'workspace_layout_dock_tile',
      workspaceId,
      {
        cmd: 'workspace_layout_dock_tile',
        workspace_id: workspaceId,
        anchor_pane_id: options.anchorPaneId ?? '',
        tile_id: tileId,
        tile_kind: tileKind,
        edge: options.edge ?? 'right',
        ...(options.ratio != null ? { ratio: options.ratio } : {}),
        ...(options.tileParams != null ? { tile_params: options.tileParams } : {}),
      },
      tileId,
    );
  }, [sendWorkspaceCommand]);

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

  const sendWorkspaceUpdateTile = useCallback((
    workspaceId: string,
    tileId: string,
    tileParams: string,
    tileSessionId?: string,
  ) => {
    const requestId = nextRequestID('workspace_update_tile');
    return sendWorkspaceCommand(
      'workspace_layout_update_tile',
      workspaceId,
      {
        cmd: 'workspace_layout_update_tile',
        workspace_id: workspaceId,
        tile_id: tileId,
        tile_params: tileParams,
        ...(tileSessionId ? { tile_session_id: tileSessionId } : {}),
        request_id: requestId,
      },
      tileId,
      requestId,
    );
  }, [nextRequestID, sendWorkspaceCommand]);

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

  const sendWorkspaceMoveLeafToNewWorkspace = useCallback((
    sourceWorkspaceId: string,
    leafId: string,
    options: { anchorId?: string; edge?: TerminalDockEdge; ratio?: number } = {},
  ) => {
    return sendWorkspaceCommand(
      'workspace_layout_move_leaf_to_new_workspace',
      sourceWorkspaceId,
      {
        cmd: 'workspace_layout_move_leaf_to_new_workspace',
        source_workspace_id: sourceWorkspaceId,
        leaf_id: leafId,
        anchor_id: options.anchorId ?? '',
        edge: options.edge ?? 'left',
        ...(options.ratio != null ? { ratio: options.ratio } : {}),
      },
      leafId,
    );
  }, [sendWorkspaceCommand]);

  const sendSetWorkspaceRank = useCallback((
    workspaceId: string,
    prevWorkspaceId?: string,
    nextWorkspaceId?: string,
  ) => {
    return sendWorkspaceCommand(
      'set_workspace_rank',
      workspaceId,
      {
        cmd: 'set_workspace_rank',
        workspace_id: workspaceId,
        ...(prevWorkspaceId ? { prev_workspace_id: prevWorkspaceId } : {}),
        ...(nextWorkspaceId ? { next_workspace_id: nextWorkspaceId } : {}),
      },
    );
  }, [sendWorkspaceCommand]);

  const requestTileContent = useCallback((workspaceId: string, tileId: string) => {
    sendOrQueueCommand(
      { cmd: 'workspace_tile_content_get', workspace_id: workspaceId, tile_id: tileId },
      { waitForInitialState: true },
    );
  }, [sendOrQueueCommand]);

  const sendOpenMarkdown = useCallback((path: string, sessionId: string): Promise<{ workspaceId?: string; tileId?: string }> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('open_markdown');
      const key = `open_markdown:${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({
        cmd: 'open_markdown',
        request_id: requestId,
        path,
        ...(sessionId ? { session_id: sessionId } : {}),
      }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Open markdown timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  const sendOpenSeed = useCallback((seedId: string, sessionId = ''): Promise<{ workspaceId?: string; tileId?: string }> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('open_seed');
      const key = `open_seed:${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({
        cmd: 'open_seed',
        request_id: requestId,
        seed_id: seedId,
        ...(sessionId ? { session_id: sessionId } : {}),
      }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Open seed timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  const sendSeedTransition = useCallback(
    (
      seedId: string,
      verb: string,
      reason?: string,
      force?: boolean,
      comment?: string,
      review?: SeedReviewActionContext,
    ): Promise<Seed> => {
      return new Promise((resolve, reject) => {
        const ws = wsRef.current;
        if (!ws || ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocket not connected'));
          return;
        }
        const requestId = nextRequestID('seed_transition');
        const key = `seed_transition:${requestId}`;
        pendingActionsRef.current.set(key, { resolve, reject });
        ws.send(
          JSON.stringify({
            cmd: 'seed_transition',
            request_id: requestId,
            seed_id: seedId,
            verb,
            ...(reason ? { reason } : {}),
            ...(comment ? { comment } : {}),
            ...(force ? { force: true } : {}),
            ...(review ? { review: { review_id: review.reviewId, evidence_version: review.evidenceVersion } } : {}),
          }),
        );
        setTimeout(() => {
          if (pendingActionsRef.current.has(key)) {
            pendingActionsRef.current.delete(key);
            reject(new Error('The move timed out'));
          }
        }, 10000);
      });
    },
    [nextRequestID],
  );

  const sendSeedNote = useCallback((seedId: string, body: string): Promise<void> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('seed_note');
      const key = `seed_note:${requestId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(), reject });
      ws.send(JSON.stringify({ cmd: 'seed_note', request_id: requestId, seed_id: seedId, body }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('The note timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  const sendSeedDocumentGet = useCallback((seedId: string): Promise<SeedDocument> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('seed_document_get');
      const key = `seed_document_get:${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(JSON.stringify({ cmd: 'seed_document_get', request_id: requestId, seed_id: seedId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Seed document read timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  const sendSeedArtifactTarget = useCallback((
    seedId: string,
    relativeTarget: string,
    purpose: 'image' | 'link' | 'artifact',
  ): Promise<SeedArtifactTargetResult> => sendRequest(
    'seed_artifact_target',
    { seed_id: seedId, relative_target: relativeTarget, purpose },
    'Seed artifact target resolution timed out',
  ), [sendRequest]);

  const sendSeedArtifactTransfer = useCallback((input: {
    seedId: string;
    operation: 'move' | 'copy' | 'detach';
    sourcePath?: string;
    filename?: string;
    destinationPath?: string;
    legacyReference?: GeneratedSeedArtifactReference;
  }): Promise<SeedArtifactTransferResult> => sendRequest(
    'seed_artifact_transfer',
    {
      seed_id: input.seedId,
      operation: input.operation,
      ...(input.sourcePath !== undefined && { source_path: input.sourcePath }),
      ...(input.filename !== undefined && { filename: input.filename }),
      ...(input.destinationPath !== undefined && { destination_path: input.destinationPath }),
      ...(input.legacyReference !== undefined && { legacy_reference: input.legacyReference }),
    },
    'Seed artifact transfer timed out',
    5 * 60 * 1000,
  ), [sendRequest]);

  const sendSeedArtifactReferenceDetach = useCallback((
    seedId: string,
    reference: GeneratedSeedArtifactReference,
  ): Promise<void> => sendRequest(
    'seed_note',
    { seed_id: seedId, body: '', kind: 'detach', artifact: reference },
    'Removing the linked artifact timed out',
  ), [sendRequest]);

  useEffect(() => {
    setPtyBackend({
      spawn: async (args: PtySpawnArgs) => {
        if (ptyTransportRef.current.hasAttachedRuntime(args.id)) return;
        try {
          await sendSpawnSession(args);
        } catch (error) {
          if (!isAlreadyExistsError(error)) throw error;
        }
        // The mounted pane owns attachment, including replay of startup output.
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
      resize: async (id: string, cols: number, rows: number, reason?: string, pixels?: PtyPixelGeometry) => {
        sendPtyResize(id, cols, rows, reason, pixels);
      },
      detach: async (id: string) => {
        sendDetachSession(id);
      },
      kill: async (id: string) => {
        sendDetachSession(id);
        await sendKillSession(id);
      },
      reload: async (id: string, cols: number, rows: number) => {
        await sendReloadSession(id, cols, rows);
      },
    });

    return () => {
      setPtyBackend(null);
    };
  }, [attachExistingRuntime, sendAttachSessionWithRetry, sendDetachSession, sendKillSession, sendPtyInput, sendPtyResize, sendReloadSession, sendSpawnSession]);

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

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Timeout'));
        }
      }, 30000);
    });
  }, []);

  const getScreenSnapshot = useCallback((runtimeId: string): Promise<ScreenSnapshotResult | null> => {
    return new Promise((resolve) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN || !runtimeId) {
        resolve(null);
        return;
      }
      const key = `screen_snapshot_${runtimeId}`;
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

  const sendMarkdownAnnotationsCommand = useCallback(<T,>(
    op: 'get' | 'save' | 'clear' | 'submit',
    source: MarkdownDocumentSource,
    extra: Record<string, unknown>,
  ): Promise<T> => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('WebSocket not connected'));
    }
    const key = markdownAnnotationKey(op, source.uri);
    const requestId = crypto.randomUUID();
    const sourceFields = source.kind === 'file'
      ? { source_kind: 'file', workspace_id: source.workspaceId, path: source.path }
      : { source_kind: 'seed', seed_id: source.seedId };
    return sendLastWriterWinsRequest<T>(
      mdAnnotationsPendingRef.current,
      key,
      requestId,
      () => ws.send(JSON.stringify({
        cmd: `markdown_annotations_${op}`,
        document_uri: source.uri,
        ...sourceFields,
        request_id: requestId,
        ...extra,
      })),
      'Timeout',
      10000,
    );
  }, []);

  const getMarkdownAnnotations = useCallback((source: MarkdownDocumentSource) => {
    const inflightKey = source.uri;
    const inflight = mdAnnotationsGetInflightRef.current.get(inflightKey);
    if (inflight) {
      return inflight;
    }
    const promise: Promise<{ annotations: MarkdownAnnotation[]; generation: number }> =
      sendMarkdownAnnotationsCommand<{ annotations: MarkdownAnnotation[]; generation: number }>(
        'get', source, {},
      ).finally(() => {
        if (mdAnnotationsGetInflightRef.current.get(inflightKey) === promise) {
          mdAnnotationsGetInflightRef.current.delete(inflightKey);
        }
      });
    mdAnnotationsGetInflightRef.current.set(inflightKey, promise);
    return promise;
  }, [sendMarkdownAnnotationsCommand]);

  const saveMarkdownAnnotations = useCallback((
    source: MarkdownDocumentSource,
    annotations: MarkdownAnnotation[],
    generation: number,
  ) =>
    sendMarkdownAnnotationsCommand<{ stale: boolean }>(
      'save', source, { annotations, generation },
    ), [sendMarkdownAnnotationsCommand]);

  const clearMarkdownAnnotations = useCallback((source: MarkdownDocumentSource, generation: number) =>
    sendMarkdownAnnotationsCommand<{ generation: number }>(
      'clear', source, { generation },
    ), [sendMarkdownAnnotationsCommand]);

  const submitMarkdownAnnotations = useCallback((
    source: MarkdownDocumentSource,
    destination: MarkdownAnnotationsDestination,
    orphanedIds: string[],
  ) => {
    const destinationFields = destination.kind === 'session'
      ? { target_session_id: destination.sessionId }
      : { target_seed_id: destination.seedId };
    return (
    sendMarkdownAnnotationsCommand<{ status: string; generation?: number; error?: string }>(
      'submit', source, { ...destinationFields, orphaned_ids: orphanedIds },
    ));
  }, [sendMarkdownAnnotationsCommand]);

  const sendMutePR = useCallback((prId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    const updatedPRs = prsRef.current.map(pr =>
      pr.id === prId ? { ...pr, muted: !pr.muted } : pr
    );
    prsRef.current = updatedPRs;
    callbacksRef.current.onPRsUpdate(updatedPRs);

    ws.send(JSON.stringify({ cmd: 'mute_pr', id: prId }));
  }, []);

  const sendMuteRepo = useCallback((repo: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    const existingRepo = reposRef.current.find(r => r.repo === repo);
    let updatedRepos: RepoState[];
    if (existingRepo) {
      updatedRepos = reposRef.current.map(r =>
        r.repo === repo ? { ...r, muted: !r.muted } : r
      );
    } else {
      updatedRepos = [...reposRef.current, { repo, muted: true, collapsed: false }];
    }
    reposRef.current = updatedRepos;
    callbacksRef.current.onReposUpdate(updatedRepos);

    ws.send(JSON.stringify({ cmd: 'mute_repo', repo }));
  }, []);

  const sendMuteAuthor = useCallback((author: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    const existingAuthor = authorsRef.current.find(a => a.author === author);
    let updatedAuthors: AuthorState[];
    if (existingAuthor) {
      updatedAuthors = authorsRef.current.map(a =>
        a.author === author ? { ...a, muted: !a.muted } : a
      );
    } else {
      updatedAuthors = [...authorsRef.current, { author, muted: true }];
    }
    authorsRef.current = updatedAuthors;
    callbacksRef.current.onAuthorsUpdate(updatedAuthors);

    ws.send(JSON.stringify({ cmd: 'mute_author', author }));
  }, []);

  const sendMuteWorkspace = useCallback((workspaceId: string, endpointId?: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      cmd: 'mute_workspace',
      workspace_id: workspaceId,
      ...(endpointId ? { endpoint_id: endpointId } : {}),
    }));
  }, []);

  const sendPinWorkspace = useCallback((workspaceId: string, pinned: boolean) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      cmd: 'pin_workspace',
      workspace_id: workspaceId,
      pinned,
    }));
  }, []);

  const sendPinSession = useCallback((sessionId: string, pinned: boolean) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      cmd: 'pin_session',
      session_id: sessionId,
      pinned,
    }));
  }, []);

  const sendRefreshPRs = useCallback((): Promise<PRActionResult> => {
    const key = 'refresh_prs';
    return sendKeyedRequest<PRActionResult>(key, { cmd: 'refresh_prs' }, 'Refresh timed out', GITHUB_REFRESH_TIMEOUT_MS);
  }, [sendKeyedRequest]);

  const sendFetchPRDetails = useCallback((id: string): Promise<FetchPRDetailsResult> => {
    const key = 'fetch_pr_details';
    return sendKeyedRequest<FetchPRDetailsResult>(key, { cmd: 'fetch_pr_details', id }, 'Fetch PR details timed out', GITHUB_REFRESH_TIMEOUT_MS);
  }, [sendKeyedRequest]);

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

  const sendSetSessionContextWindowCap = useCallback((sessionId: string, cap: number): Promise<void> => {
    if (!sessionId) {
      return Promise.reject(new Error('Session is required'));
    }
    if (!hasReceivedInitialStateRef.current) {
      return Promise.reject(new Error('WebSocket not connected'));
    }
    return sendKeyedRequest<void>(
      `session_context_cap:${sessionId}`,
      { cmd: 'set_session_context_window_cap', session_id: sessionId, cap },
      `Context window cap update timed out for session ${sessionId}`,
      10_000,
    );
  }, [sendKeyedRequest]);

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

  const sendPRVisited = useCallback((prId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

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

  const sendSetSetting = useCallback((key: string, value: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    settingsRef.current = { ...settingsRef.current, [key]: value };
    callbacksRef.current.onSettingsUpdate?.(settingsRef.current);

    ws.send(JSON.stringify({ cmd: 'set_setting', key, value }));
  }, []);

  const sendGetSettings = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'get_settings' }));
  }, []);

  const sendListPlugins = useCallback((): Promise<PluginListResult> => {
    const key = 'list_plugins';
    return sendKeyedRequest<PluginListResult>(key, { cmd: 'list_plugins' }, 'List plugins timed out');
  }, [sendKeyedRequest]);

  const sendInstallPlugin = useCallback((source: string): Promise<PluginActionResult> => {
    const key = 'plugin_action:install:pending';
    return sendKeyedRequest<PluginActionResult>(key, { cmd: 'install_plugin', source }, 'Install plugin timed out', 60000);
  }, [sendKeyedRequest]);

  const sendInstallBundledPlugin = useCallback((name: string): Promise<PluginActionResult> => {
    const key = `plugin_action:install_bundled:${name}`;
    return sendKeyedRequest<PluginActionResult>(key, { cmd: 'install_bundled_plugin', name }, 'Install bundled plugin timed out', 30000);
  }, [sendKeyedRequest]);

  const sendUninstallPlugin = useCallback((name: string): Promise<PluginActionResult> => {
    const key = `plugin_action:uninstall:${name}`;
    return sendKeyedRequest<PluginActionResult>(key, { cmd: 'uninstall_plugin', name }, 'Uninstall plugin timed out', 30000);
  }, [sendKeyedRequest]);

  const sendRemovePlugin = useCallback((name: string): Promise<PluginActionResult> => {
    const key = `plugin_action:remove:${name}`;
    return sendKeyedRequest<PluginActionResult>(key, { cmd: 'remove_plugin', name }, 'Remove plugin timed out', 30000);
  }, [sendKeyedRequest]);

  const sendSetPluginPriority = useCallback((name: string, priority: number): Promise<PluginActionResult> => {
    const key = `plugin_action:set_priority:${name}`;
    return sendKeyedRequest<PluginActionResult>(key, { cmd: 'set_plugin_priority', name, priority }, 'Set plugin priority timed out', 30000);
  }, [sendKeyedRequest]);

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
    const key = 'list_endpoints';
    return sendKeyedRequest<DaemonEndpoint[]>(key, { cmd: 'list_endpoints' }, 'List endpoints timed out');
  }, [sendKeyedRequest]);

  const sendListWorkspaceContexts = useCallback((): Promise<DaemonWorkspaceContext[]> => {
    const requestId = nextRequestID('workspace_context_list');
    const key = `workspace_context_list:${requestId}`;
    return sendKeyedRequest<DaemonWorkspaceContext[]>(key, { cmd: 'workspace_context_list', request_id: requestId }, 'Workspace context list timed out');
  }, [nextRequestID, sendKeyedRequest]);

  const sendNotebookList = useCallback((prefix?: string): Promise<NotebookEntry[]> =>
    sendRequest<NotebookEntry[]>('notebook_list', prefix ? { prefix } : {}, 'Notebook list timed out'), [sendRequest]);

  const sendNotebookRead = useCallback((path: string): Promise<NotebookReadResult> =>
    sendRequest<NotebookReadResult>('notebook_read', { path }, 'Notebook read timed out'), [sendRequest]);

  const sendSessionMessagesGet = useCallback((sessionId: string): Promise<SessionMessageWindow> => {
    return sendRequest('session_messages_get', { session_id: sessionId }, 'Session message fetch timed out');
  }, [sendRequest]);

  const subscribeSessionMessagesChanged = useCallback((sessionId: string, listener: () => void) => {
    let listeners = sessionMessageListenersRef.current.get(sessionId);
    if (!listeners) {
      listeners = new Set();
      sessionMessageListenersRef.current.set(sessionId, listeners);
    }
    listeners.add(listener);
    return () => {
      listeners?.delete(listener);
      if (listeners?.size === 0) sessionMessageListenersRef.current.delete(sessionId);
    };
  }, []);

  const sendSessionAnnotationsGet = useCallback((sessionId: string): Promise<SessionAnnotationSet> => {
    return sendRequest('session_annotations_get', { session_id: sessionId }, 'Session annotation fetch timed out');
  }, [sendRequest]);

  const sendSessionAnnotationsSave = useCallback((
    sessionId: string,
    annotations: readonly DaemonSessionAnnotation[],
    note: string,
    generation: number,
  ): Promise<{ stale: boolean }> => {
    return sendRequest(
      'session_annotations_save',
      { session_id: sessionId, annotations: annotations.map(annotationToWire), note, generation },
      'Session annotation save timed out',
    );
  }, [sendRequest]);

  const sendSessionAnnotationsClear = useCallback((
    sessionId: string,
    generation: number,
  ): Promise<{ generation: number }> => {
    return sendRequest(
      'session_annotations_clear',
      { session_id: sessionId, generation },
      'Session annotation clear timed out',
    );
  }, [sendRequest]);

  const sendSessionAnnotationsSubmit = useCallback((
    sessionId: string,
    text: string,
  ): Promise<{ status: string }> => {
    return sendRequest(
      'session_annotations_submit',
      { session_id: sessionId, text },
      'Session annotation send timed out',
    );
  }, [sendRequest]);

  const sendSeedHandover = useCallback((options: SeedHandoverOptions): Promise<SeedHandoverResult> => {
    const requestId = nextRequestID('seed_handover');
    return sendKeyedRequest<SeedHandoverResult>(
      pendingRequestKey('delegate', requestId),
      {
        cmd: 'delegate',
        request_id: requestId,
        source_session_id: options.sourceSessionId ?? '',
        brief: '',
        handover: {
          seed_id: options.seedId,
          expected_rev: options.expectedRev,
          expected_tender_session: options.expectedTenderSession,
          expected_tender_member: options.expectedTenderMember,
          ...(options.handoff?.trim() ? { handoff: options.handoff.trim() } : {}),
          ...(options.review ? {
            review: {
              review_id: options.review.reviewId,
              evidence_version: options.review.evidenceVersion,
            },
          } : {}),
        },
        ...(options.agent?.trim() ? { agent: options.agent.trim() } : {}),
        ...(options.model?.trim() ? { model: options.model.trim() } : {}),
        ...(options.effort?.trim() ? { effort: options.effort.trim() } : {}),
      },
      'Handover timed out',
      120_000,
    );
  }, [nextRequestID, sendKeyedRequest]);

  const sendSeedToChief = useCallback((options: SeedSendToChiefOptions): Promise<SeedSendToChiefResult> => (
    sendRequest<SeedSendToChiefResult>(
      'seed_send_to_chief',
      {
        source_session_id: options.sourceSessionId ?? '',
        seed_id: options.seedId,
        expected_rev: options.expectedRev,
        expected_tender_session: options.expectedTenderSession,
        expected_tender_member: options.expectedTenderMember,
        ...(options.guidance?.trim() ? { guidance: options.guidance.trim() } : {}),
        ...(options.review ? {
          review: {
            review_id: options.review.reviewId,
            evidence_version: options.review.evidenceVersion,
          },
        } : {}),
      },
      'Sending the seed to Chief timed out',
    )
  ), [sendRequest]);

  const sendSeedResume = useCallback(
    (
      seedId: string,
      review?: SeedReviewActionContext,
    ): Promise<{ sessionId: string; workspaceId?: string; alreadyRunning?: boolean }> => {
      return new Promise((resolve, reject) => {
        const ws = wsRef.current;
        if (!ws || ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocket not connected'));
          return;
        }
        const requestId = nextRequestID('seed_resume');
        const key = `seed_resume:${requestId}`;
        pendingActionsRef.current.set(key, { resolve, reject });
        ws.send(JSON.stringify({
          cmd: 'seed_resume', request_id: requestId, seed_id: seedId,
          ...(review ? { review: { review_id: review.reviewId, evidence_version: review.evidenceVersion } } : {}),
        }));
        setTimeout(() => {
          if (pendingActionsRef.current.has(key)) {
            pendingActionsRef.current.delete(key);
            reject(new Error('Resuming the seed timed out'));
          }
        }, 10000);
      });
    },
    [nextRequestID],
  );

  const sendSeedReviewShow = useCallback((reviewId?: string): Promise<SeedReviewOverview> => (
    sendRequest<SeedReviewOverview>(
      'seed_review_show',
      reviewId ? { review_id: reviewId } : {},
      'Reading Garden review timed out',
    )
  ), [sendRequest]);

  const sendSeedReviewStart = useCallback((): Promise<SeedReviewOverview> => (
    sendRequest<SeedReviewOverview>('seed_review_start', {}, 'Starting Garden review timed out')
  ), [sendRequest]);

  const sendSeedReviewRetry = useCallback((reviewId: string, seedId: string): Promise<SeedReviewOverview> => (
    sendRequest<SeedReviewOverview>(
      'seed_review_retry',
      { review_id: reviewId, seed_id: seedId },
      'Retrying Garden review timed out',
    )
  ), [sendRequest]);

  const sendSeedReviewKeep = useCallback((
    seedId: string,
    review: SeedReviewActionContext,
  ): Promise<SeedReviewOverview> => (
    sendRequest<SeedReviewOverview>(
      'seed_review_keep',
      {
        seed_id: seedId,
        review: { review_id: review.reviewId, evidence_version: review.evidenceVersion },
      },
      'Keeping the seed growing timed out',
    )
  ), [sendRequest]);

  const sendSeedReviewDraft = useCallback((
    seedId: string,
    review: SeedReviewActionContext,
  ): Promise<string> => (
    sendRequest<string>(
      'seed_review_draft',
      {
        seed_id: seedId,
        review: { review_id: review.reviewId, evidence_version: review.evidenceVersion },
      },
      'Drafting the handoff timed out',
      300_000,
    )
  ), [sendRequest]);

  const sendCrewWake = useCallback(
    (member: string): Promise<{ sessionId: string; alreadyAwake: boolean }> => {
      return new Promise((resolve, reject) => {
        const ws = wsRef.current;
        if (!ws || ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocket not connected'));
          return;
        }
        const requestId = nextRequestID('crew_wake');
        const key = `crew_wake:${requestId}`;
        pendingActionsRef.current.set(key, { resolve, reject });
        ws.send(JSON.stringify({ cmd: 'crew_wake', request_id: requestId, member }));
        setTimeout(() => {
          if (pendingActionsRef.current.has(key)) {
            pendingActionsRef.current.delete(key);
            reject(new Error(`Waking ${crewDisplayName(member)} timed out`));
          }
        }, 10000);
      });
    },
    [nextRequestID],
  );

  const sendCrewSleep = useCallback(
    (member: string): Promise<CrewSleepResult> => sendRequest(
      'crew_sleep',
      { member },
      `Asking ${crewDisplayName(member)} to sleep timed out`,
    ),
    [sendRequest],
  );

  const sendTaskList = useCallback((): Promise<Task[]> => {
    const requestId = nextRequestID('task_list');
    const key = `task_list:${requestId}`;
    return sendKeyedRequest<Task[]>(key, { cmd: 'task_list', request_id: requestId }, 'Notebook task list timed out');
  }, [nextRequestID, sendKeyedRequest]);

  const sendTaskRetry = useCallback((taskId: string): Promise<Task | null> => {
    const requestId = nextRequestID('task_retry');
    const key = `task_retry:${requestId}`;
    return sendKeyedRequest<Task | null>(key, {
      cmd: 'task_retry',
      request_id: requestId,
      task_id: taskId,
    }, 'Notebook task retry timed out');
  }, [nextRequestID, sendKeyedRequest]);

  const sendNotificationList = useCallback((): Promise<{
    notifications: DaemonNotification[];
    unreadCount: number;
    critical: CriticalNotificationState;
  }> => {
    const requestId = nextRequestID('notification_list');
    const key = `notification_list:${requestId}`;
    return sendKeyedRequest<{
    notifications: DaemonNotification[];
    unreadCount: number;
    critical: CriticalNotificationState;
  }>(key, { cmd: 'notification_list', request_id: requestId }, 'Notification list timed out');
  }, [nextRequestID, sendKeyedRequest]);

  const sendNotificationMarkRead = useCallback((notificationId?: string): Promise<number> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('notification_mark_read');
      const key = `notification_mark_read:${requestId}`;
      pendingActionsRef.current.set(key, { resolve, reject });
      ws.send(
        JSON.stringify({
          cmd: 'notification_mark_read',
          request_id: requestId,
          ...(notificationId ? { notification_id: notificationId } : {}),
        }),
      );
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Notification mark-read timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  const sendNotebookBacklinks = useCallback((path: string): Promise<NotebookEntry[]> =>
    sendRequest<NotebookEntry[]>('notebook_backlinks', { path }, 'Notebook backlinks timed out'), [sendRequest]);

  const sendNotebookWrite = useCallback((path: string, content: string, baseHash?: string): Promise<NotebookWriteResult> =>
    sendRequest<NotebookWriteResult>('notebook_write', { path, content, ...(baseHash ? { base_hash: baseHash } : {}) }, 'Notebook save timed out'), [sendRequest]);

  const sendNotebookToChief = useCallback((selection: string, sourcePath?: string): Promise<NotebookSendToChiefResult> =>
    sendRequest<NotebookSendToChiefResult>('notebook_send_to_chief', { selection, ...(sourcePath ? { source_path: sourcePath } : {}) }, 'Send to chief timed out'), [sendRequest]);

  const sendFsList = useCallback((path?: string, root?: string): Promise<FsEntry[]> =>
    sendRequest<FsEntry[]>('fs_list', { ...(path ? { path } : {}), ...(root ? { root } : {}) }, 'Filesystem list timed out'), [sendRequest]);

  const sendFsRead = useCallback((path: string, root?: string): Promise<FsReadResult> =>
    sendRequest<FsReadResult>('fs_read', { path, ...(root ? { root } : {}) }, 'Filesystem read timed out'), [sendRequest]);

  const sendFsReadAsset = useCallback((path: string, root?: string): Promise<FsReadAssetResult> =>
    sendRequest<FsReadAssetResult>('fs_read_asset', { path, ...(root ? { root } : {}) }, 'Filesystem asset read timed out'), [sendRequest]);

  const sendFsWrite = useCallback((path: string, content: string, baseHash?: string, root?: string): Promise<FsWriteResult> =>
    sendRequest<FsWriteResult>('fs_write', { path, content, ...(baseHash ? { base_hash: baseHash } : {}), ...(root ? { root } : {}) }, 'Filesystem save timed out'), [sendRequest]);

  const sendFsRename = useCallback((path: string, newPath: string, root?: string): Promise<FsRenameResult> =>
    sendRequest<FsRenameResult>('fs_rename', { path, new_path: newPath, ...(root ? { root } : {}) }, 'Filesystem rename timed out'), [sendRequest]);

  const sendFsDelete = useCallback((path: string, root?: string): Promise<FsDeleteResult> =>
    sendRequest<FsDeleteResult>('fs_delete', { path, ...(root ? { root } : {}) }, 'Filesystem delete timed out'), [sendRequest]);

  const sendFsExists = useCallback((path: string, root?: string): Promise<FsExistsResult> =>
    sendRequest<FsExistsResult>('fs_exists', { path, ...(root ? { root } : {}) }, 'Filesystem exists check timed out'), [sendRequest]);

  const sendFsWatch = useCallback((root?: string): Promise<FsWatchResult> =>
    sendRequest<FsWatchResult>('fs_watch', root ? { root } : {}, 'Filesystem watch timed out'), [sendRequest]);

  const sendFsUnwatch = useCallback((root?: string): Promise<FsWatchResult> =>
    sendRequest<FsWatchResult>('fs_unwatch', root ? { root } : {}, 'Filesystem unwatch timed out'), [sendRequest]);

  const sendFsIndex = useCallback((root?: string, extensions?: string[]): Promise<FsIndexResult> =>
    sendRequest<FsIndexResult>('fs_index', { ...(root ? { root } : {}), ...(extensions && extensions.length > 0 ? { extensions } : {}) }, 'Filesystem index timed out'), [sendRequest]);

  const sendRecentFiles = useCallback((limit?: number, root?: string): Promise<RecentFile[]> => {
    const requestId = nextRequestID('recent_files');
    const key = `recent_files:${requestId}`;
    return sendKeyedRequest<RecentFile[]>(key, {
      cmd: 'recent_files',
      request_id: requestId,
      ...(limit ? { limit } : {}),
      ...(root ? { root } : {}),
    }, 'Recent files timed out');
  }, [nextRequestID, sendKeyedRequest]);

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

      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new Error('Get recent locations timed out'));
        }
      }, 10000);
    });
  }, [nextRequestID]);

  const sendBrowseDirectory = useCallback((inputPath: string, endpointId?: string, extensions?: string[]): Promise<BrowseDirectoryResult> => {
    const requestId = nextRequestID('browse_directory');
    const key = `browse_directory_${requestId}`;
    return sendKeyedRequest<BrowseDirectoryResult>(key, {
      cmd: 'browse_directory',
      input_path: inputPath,
      ...(extensions && extensions.length > 0 ? { extensions } : {}),
      ...(endpointId && { endpoint_id: endpointId }),
      request_id: requestId,
    }, 'Browse directory timed out', GIT_METADATA_TIMEOUT_MS);
  }, [nextRequestID, sendKeyedRequest]);

  const sendInspectPath = useCallback((path: string, endpointId?: string): Promise<InspectPathResult> => {
    const requestId = nextRequestID('inspect_path');
    const key = `inspect_path_${requestId}`;
    return sendKeyedRequest<InspectPathResult>(key, {
      cmd: 'inspect_path',
      path,
      ...(endpointId && { endpoint_id: endpointId }),
      request_id: requestId,
    }, 'Inspect path timed out', GIT_METADATA_TIMEOUT_MS);
  }, [nextRequestID, sendKeyedRequest]);

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

  const sendFetchRemotes = useCallback((repo: string): Promise<FetchRemotesResult> => {
    const key = 'fetch_remotes';
    return sendKeyedRequest<FetchRemotesResult>(key, { cmd: 'fetch_remotes', repo }, 'Fetch remotes timed out', GIT_NETWORK_TIMEOUT_MS);
  }, [sendKeyedRequest]);

  const sendEnsureRepo = useCallback((targetPath: string, cloneUrl: string): Promise<EnsureRepoResult> => {
    const key = 'ensure_repo';
    return sendKeyedRequest<EnsureRepoResult>(key, {
      cmd: 'ensure_repo',
      target_path: targetPath,
      clone_url: cloneUrl,
    }, 'Ensure repo timed out', GIT_CLONE_TIMEOUT_MS);
  }, [sendKeyedRequest]);

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

  const sendUnsubscribeGitStatus = useCallback(() => {
    const hadSubscription = gitStatusSubscriptionRef.current;
    gitStatusSubscriptionRef.current = null;
    if (!hadSubscription) return;

    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    ws.send(JSON.stringify({ cmd: 'unsubscribe_git_status' }));
  }, []);

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

  const sendTriggerNudge = useCallback((sessionId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'trigger_nudge', session_id: sessionId }));
  }, []);

  const sendSettleTurn = useCallback((sessionId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'settle_turn', session_id: sessionId }));
  }, []);

  const sendSnoozeTurn = useCallback((sessionId: string, until: Date) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'snooze_turn', session_id: sessionId, until: until.toISOString() }));
  }, []);

  const sendWakeTurn = useCallback((sessionId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'wake_turn', session_id: sessionId }));
  }, []);

  const sendCancelCountdown = useCallback((sessionId: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ cmd: 'cancel_countdown', session_id: sessionId }));
  }, []);

  const sendGetFileDiff = useCallback((
    directory: string,
    path: string,
    options?: { staged?: boolean; baseRef?: string; headRef?: string }
  ): Promise<FileDiffResult> => {
    // Key by request id, not path: a stale round's late reply racing a new round's request must not
    // clobber the pending promise.
    const requestId = nextRequestID('get_file_diff');
    const key = `get_file_diff_${requestId}`;
    return sendKeyedRequest<FileDiffResult>(key, {
      cmd: 'get_file_diff',
      directory,
      path,
      request_id: requestId,
      ...(options?.staged !== undefined && { staged: options.staged }),
      ...(options?.baseRef && { base_ref: options.baseRef }),
      ...(options?.headRef && { head_ref: options.headRef }),
    }, 'Get file diff timed out', GIT_DIFF_TIMEOUT_MS);
  }, [nextRequestID, sendKeyedRequest]);

  const getRepoInfo = useCallback((repo: string, endpointId?: string): Promise<RepoInfoResult> => {
    const key = `repo_info_${endpointId || 'local'}_${repo}`;
    return sendKeyedRequest<RepoInfoResult>(key, {
      cmd: 'get_repo_info',
      repo,
      ...(endpointId && { endpoint_id: endpointId }),
    }, 'get_repo_info timeout', GIT_METADATA_TIMEOUT_MS);
  }, [sendKeyedRequest]);

  const getWorkflowRun = useCallback((runId: string): Promise<{ success: boolean; run: WorkflowRunState | null }> => {
    const key = `workflow_run_get_${runId}`;
    return sendKeyedRequest<{ success: boolean; run: WorkflowRunState | null }>(key, { cmd: 'workflow_run_get', run_id: runId }, 'Get workflow run timed out');
  }, [sendKeyedRequest]);

  const listWorkflowRuns = useCallback((sessionId?: string): Promise<{ success: boolean; runs: WorkflowRunState[] }> => {
    const key = 'workflow_run_list';
    return sendKeyedRequest<{ success: boolean; runs: WorkflowRunState[] }>(key, {
      cmd: 'workflow_run_list',
      ...(sessionId ? { session_id: sessionId } : {}),
    }, 'List workflow runs timed out');
  }, [sendKeyedRequest]);

  const listAutomationDefinitions = useCallback((): Promise<AutomationDefinitionSummary[]> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('automation_definitions_get');
      const key = `automation:${requestId}`;
      pendingActionsRef.current.set(key, {
        resolve: (result: any) => resolve((result.definitions ?? []) as AutomationDefinitionSummary[]),
        reject,
      });
      ws.send(JSON.stringify({ cmd: 'automation_definitions_get', request_id: requestId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new AutomationActionTimeoutError('List automation definitions timed out'));
        }
      }, 30000);
    });
  }, [nextRequestID]);

  const listAutomationRuns = useCallback((definitionId: string): Promise<AutomationRunSummary[]> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('automation_runs_get');
      const key = `automation:${requestId}`;
      pendingActionsRef.current.set(key, {
        resolve: (result: any) => resolve((result.runs ?? []) as AutomationRunSummary[]),
        reject,
      });
      ws.send(JSON.stringify({ cmd: 'automation_runs_get', definition_id: definitionId, request_id: requestId }));
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new AutomationActionTimeoutError('List automation runs timed out'));
        }
      }, 30000);
    });
  }, [nextRequestID]);

  const setAutomationEnabled = useCallback((definitionId: string, enabled: boolean): Promise<void> => {
    return new Promise((resolve, reject) => {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        reject(new Error('WebSocket not connected'));
        return;
      }
      const requestId = nextRequestID('automation_set_enabled');
      const key = `automation:${requestId}`;
      pendingActionsRef.current.set(key, { resolve: () => resolve(undefined), reject });
      ws.send(
        JSON.stringify({ cmd: 'automation_set_enabled', definition_id: definitionId, enabled, request_id: requestId }),
      );
      setTimeout(() => {
        if (pendingActionsRef.current.has(key)) {
          pendingActionsRef.current.delete(key);
          reject(new AutomationActionTimeoutError('Set automation enabled timed out'));
        }
      }, 30000);
    });
  }, [nextRequestID]);

  const runAutomationNow = useCallback(
    (definitionId: string, requestId: string): Promise<AutomationRunSummary | undefined> => {
      return new Promise((resolve, reject) => {
        const ws = wsRef.current;
        if (!ws || ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocket not connected'));
          return;
        }
        const key = `automation:${requestId}`;
        pendingActionsRef.current.set(key, {
          resolve: (result: any) => resolve(result.run as AutomationRunSummary | undefined),
          reject,
        });
        ws.send(JSON.stringify({ cmd: 'automation_run', definition_id: definitionId, request_id: requestId }));
        setTimeout(() => {
          if (pendingActionsRef.current.has(key)) {
            pendingActionsRef.current.delete(key);
            reject(new AutomationActionTimeoutError('Run automation timed out'));
          }
        }, 30000);
      });
    },
    [],
  );

  const getAutomationDefinition = useCallback(
    (
      definitionId: string,
    ): Promise<{ specYaml: string; specJson: string; definition?: AutomationDefinitionSummary }> => {
      return new Promise((resolve, reject) => {
        const ws = wsRef.current;
        if (!ws || ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocket not connected'));
          return;
        }
        const requestId = nextRequestID('automation_definition_get');
        const key = `automation:${requestId}`;
        pendingActionsRef.current.set(key, {
          resolve: (result: any) =>
            resolve({
              specYaml: result.spec_yaml ?? '',
              specJson: result.spec_json ?? '',
              definition: result.definition ?? undefined,
            }),
          reject,
        });
        ws.send(
          JSON.stringify({ cmd: 'automation_definition_get', definition_id: definitionId, request_id: requestId }),
        );
        setTimeout(() => {
          if (pendingActionsRef.current.has(key)) {
            pendingActionsRef.current.delete(key);
            reject(new AutomationActionTimeoutError('Get automation definition timed out'));
          }
        }, 30000);
      });
    },
    [nextRequestID],
  );

  const applyAutomationDefinition = useCallback(
    (
      definitionYaml: string,
      expectedId: string,
      expectedRevision: number,
    ): Promise<{ definition: AutomationDefinitionSummary; specYaml: string }> => {
      return new Promise((resolve, reject) => {
        const ws = wsRef.current;
        if (!ws || ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocket not connected'));
          return;
        }
        const requestId = nextRequestID('automation_apply');
        const key = `automation:${requestId}`;
        pendingActionsRef.current.set(key, {
          resolve: (result: any) =>
            resolve({
              definition: result.definition,
              specYaml: result.spec_yaml ?? '',
            }),
          reject,
        });
        ws.send(
          JSON.stringify({
            cmd: 'automation_apply',
            definition_yaml: definitionYaml,
            expected_id: expectedId,
            expected_revision: expectedRevision,
            request_id: requestId,
          }),
        );
        setTimeout(() => {
          if (pendingActionsRef.current.has(key)) {
            pendingActionsRef.current.delete(key);
            reject(new AutomationActionTimeoutError('Apply automation timed out'));
          }
        }, 30000);
      });
    },
    [nextRequestID],
  );

  const deleteAutomationDefinition = useCallback(
    (definitionId: string): Promise<void> => {
      return new Promise((resolve, reject) => {
        const ws = wsRef.current;
        if (!ws || ws.readyState !== WebSocket.OPEN) {
          reject(new Error('WebSocket not connected'));
          return;
        }
        const requestId = nextRequestID('automation_delete');
        const key = `automation:${requestId}`;
        pendingActionsRef.current.set(key, { resolve: () => resolve(undefined), reject });
        ws.send(
          JSON.stringify({ cmd: 'automation_delete', definition_id: definitionId, request_id: requestId }),
        );
        setTimeout(() => {
          if (pendingActionsRef.current.has(key)) {
            pendingActionsRef.current.delete(key);
            reject(new AutomationActionTimeoutError('Delete automation timed out'));
          }
        }, 30000);
      });
    },
    [nextRequestID],
  );

  const getPresentations = useCallback((): Promise<Presentation[]> => {
    const key = 'get_presentations';
    return sendKeyedRequest<Presentation[]>(key, { cmd: 'get_presentations' }, 'Get presentations timed out', 30000);
  }, [sendKeyedRequest]);

  const getPresentationRound = useCallback((
    presentationId: string,
    seq?: number,
  ): Promise<{ presentation: Presentation; round: PresentationRound; comments: PresentationComment[]; repoHeadSha?: string }> => {
    const key = 'get_presentation_round';
    return sendKeyedRequest<{ presentation: Presentation; round: PresentationRound; comments: PresentationComment[]; repoHeadSha?: string }>(key, {
      cmd: 'get_presentation_round',
      presentation_id: presentationId,
      ...(seq !== undefined && { seq }),
    }, 'Get presentation round timed out', 30000);
  }, [sendKeyedRequest]);

  const submitPresentationRound = useCallback((input: {
    roundId: string;
    verdict: 'approved' | 'feedback';
    comments: PresentCommentInput[];
    handback: boolean;
  }): Promise<{ roundId: string }> => {
    const key = 'present_submit_round';
    return sendKeyedRequest<{ roundId: string }>(key, {
      cmd: 'present_submit_round',
      round_id: input.roundId,
      verdict: input.verdict,
      comments: input.comments,
      handback: input.handback,
    }, 'Submit presentation round timed out', 30000);
  }, [sendKeyedRequest]);

  const closePresentation = useCallback((presentationId: string): Promise<{ presentationId: string }> => {
    const key = 'present_close';
    return sendKeyedRequest<{ presentationId: string }>(key, { cmd: 'present_close', presentation_id: presentationId, }, 'Close presentation timed out', 30000);
  }, [sendKeyedRequest]);

  const clearWarnings = useCallback(() => {
    setWarnings([]);
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    ws.send(JSON.stringify({ cmd: 'clear_warnings' }));
  }, []);

  const clearDisconnectExplanation = useCallback(() => {
    setDisconnectExplanation(null);
  }, []);

  return {
    isConnected: wsRef.current?.readyState === WebSocket.OPEN,
    connectionError,
    disconnectExplanation,
    clearDisconnectExplanation,
    connectionGeneration,
    hasReceivedInitialState,
    settings: settingsRef.current,
    rateLimit,
    warnings,
    gitOperations,
    clearWarnings,
    retryConnection,
    sendPRAction,
    getScreenSnapshot,
    getMarkdownAnnotations,
    saveMarkdownAnnotations,
    clearMarkdownAnnotations,
    submitMarkdownAnnotations,
    sendMutePR,
    sendMuteRepo,
    sendMuteAuthor,
    sendMuteWorkspace,
    sendPinWorkspace,
    sendPinSession,
    sendRefreshPRs,
    sendFetchPRDetails,
    sendClearSessions,
    sendUnregisterSession,
    sendRegisterWorkspace,
    sendUnregisterWorkspace,
    sendRenameSession,
    sendRenameWorkspace,
    sendSetChiefOfStaff,
    sendSetSessionContextWindowCap,
    sendPRVisited,
    sendListWorktrees,
    sendCreateWorktree,
    sendDeleteWorktree,
    sendSetSetting,
    sendGetSettings,
    sendListPlugins,
    sendInstallPlugin,
    sendInstallBundledPlugin,
    sendUninstallPlugin,
    sendRemovePlugin,
    sendSetPluginPriority,
    sendAddEndpoint,
    sendUpdateEndpoint,
    sendRemoveEndpoint,
    sendSetEndpointRemoteWeb,
    sendBootstrapEndpoint,
    sendListEndpoints,
    sendListWorkspaceContexts,
    sendNotebookList,
    sendNotebookRead,
    sendSessionMessagesGet,
    subscribeSessionMessagesChanged,
    sendSessionAnnotationsGet,
    sendSessionAnnotationsSave,
    sendSessionAnnotationsClear,
    sendSessionAnnotationsSubmit,
    sendSeedHandover,
    sendSeedToChief,
    sendSeedResume,
    seedReviewOverview,
    sendSeedReviewShow,
    sendSeedReviewStart,
    sendSeedReviewRetry,
    sendSeedReviewKeep,
    sendSeedReviewDraft,
    sendCrewWake,
    sendCrewSleep,
    sendTaskList,
    sendTaskRetry,
    sendNotificationList,
    sendNotificationMarkRead,
    sendNotebookBacklinks,
    sendNotebookWrite,
    sendNotebookToChief,
    sendFsList,
    sendFsRead,
    sendFsReadAsset,
    sendFsWrite,
    sendFsRename,
    sendFsDelete,
    sendFsExists,
    sendFsWatch,
    sendFsUnwatch,
    sendFsIndex,
    sendRecentFiles,
    sendGetRecentLocations,
    sendBrowseDirectory,
    sendInspectPath,
    sendCreateWorktreeFromBranch,
    sendFetchRemotes,
    sendEnsureRepo,
    sendSubscribeGitStatus,
    sendUnsubscribeGitStatus,
    sendSessionSelected,
    sendAgentPrompt,
    sendAgentToolDetail,
    sendAgentClearQueue,
    sendAgentAttach,
    sendAgentHistory,
    sendAgentSetModel,
    sendListPastConversations,
    sendBusStatusGet,
    sendDelegationPreferencesGet,
    sendDelegationPreferencesSave,
    sendDelegationModels,
    sendAutoModeGet,
    sendAutoModePromote,
    sendAutoModeDiscard,
    sendAutoModePatternAdd,
    sendAutoModeModelSet,
    sendAutoModeModels,
    sendAutoModeEnvSlot,
    sendAutoModeEnvNotes,
    sendAutoModePatternRemove,
    sendBusSetConsumerEnabled,
    sendTriggerNudge,
    sendSettleTurn,
    sendSnoozeTurn,
    sendWakeTurn,
    sendCancelCountdown,
    sendWorkspaceSelected,
    sendWorkspaceGet,
    sendWorkspaceAddSessionPane,
    sendWorkspaceClosePane,
    sendWorkspaceFocusPane,
    sendWorkspaceRenamePane,
    sendWorkspaceSetSplitRatio,
    sendWorkspaceDockTile,
    sendWorkspaceUndockTile,
    sendWorkspaceUpdateTile,
    sendWorkspaceMoveLeaf,
    sendWorkspaceMoveLeafToWorkspace,
    sendWorkspaceMoveLeafToNewWorkspace,
    sendSetWorkspaceRank,
    tileContents,
    requestTileContent,
    sendOpenMarkdown,
    sendOpenSeed,
    sendSeedDocumentGet,
    sendSeedArtifactTarget,
    sendSeedArtifactTransfer,
    sendSeedArtifactReferenceDetach,
    sendSeedTransition,
    sendSeedNote,
    sendRuntimeInput: sendPtyInput,
    sendTerminalPointerActivity,
    sendSetClientPresence,
    sendSetTerminalTheme,
    sendAppViewCrash,
    sendAppCommand,
    subscribeDocuments,
    isRuntimeAttached,
    sendGetFileDiff,
    getRepoInfo,
    getWorkflowRun,
    listWorkflowRuns,
    listAutomationDefinitions,
    listAutomationRuns,
    setAutomationEnabled,
    runAutomationNow,
    getAutomationDefinition,
    applyAutomationDefinition,
    deleteAutomationDefinition,
    getPresentations,
    getPresentationRound,
    submitPresentationRound,
    closePresentation,
  };
}
