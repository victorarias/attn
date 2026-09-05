import { useCallback, useEffect, useRef } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import type { Session } from '../store/sessions';
import type { Presentation } from '../types/generated';
import type { SessionAgent } from '../types/sessionAgent';
import type { TerminalSplitDirection } from '../types/workspace';
import { SHORTCUTS, type ShortcutId, type Combo, isChord } from '../shortcuts/registry';
import { resolveBinding } from '../shortcuts/resolver';
import { getGridAutomationHandle, INACTIVE_GRID_STATE } from '../components/grid/gridAutomation';
import {
  getAutomationFormAutomationHandle,
  type AutomationFormAutomationState,
} from '../components/automations/automationFormAutomation';
import {
  getMarkdownAnnotationsAutomationHandle,
  INACTIVE_MARKDOWN_ANNOTATIONS_STATE,
} from '../components/MarkdownReader/annotations/annotationsAutomation';
import { getSettingsAutomationHandle, INACTIVE_SETTINGS_STATE } from '../components/settingsAutomation';
import { getAutoModeAutomationHandle, INACTIVE_AUTOMODE_STATE } from '../components/autoModeAutomation';
import { useConversationsStore } from '../store/conversations';
import { getTerminalPerfSnapshot } from '../utils/terminalPerf';
import { readWarmWorkspaceLimit } from '../utils/terminalVirtualization';
import { dumpTerminalGeometry } from '../utils/terminalDiagnosticsLog';
import { clearPtyPerfSnapshot, getPtyPerfSnapshot, recordPtyDecode, recordWsJsonParse } from '../utils/ptyPerf';
import { buildSessionRenderHealth } from '../utils/renderHealth';
import { collectWorkspaceLayoutDiagnostics, projectWorkspaceBounds } from '../utils/workspaceDiagnostics';
import type { TerminalVisibleContentSnapshot } from '../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../utils/terminalStyleSummary';
import type { BlockStateSnapshot, PlacementStateSnapshot } from '../components/GhosttyTerminal';
import { isPresentWindowAction } from './usePresentAutomationBridge';

const UI_AUTOMATION_REQUEST_EVENT = 'attn://ui-automation/request';
const UI_AUTOMATION_RESPONSE_EVENT = 'attn://ui-automation/response';
const UI_AUTOMATION_READY_EVENT = 'attn://ui-automation/ready';

function readBuildEnv(value: string | undefined): string | null {
  if (typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

const APP_BUILD_IDENTITY = {
  version: readBuildEnv(import.meta.env.VITE_ATTN_BUILD_VERSION),
  sourceFingerprint: readBuildEnv(import.meta.env.VITE_ATTN_SOURCE_FINGERPRINT),
  gitCommit: readBuildEnv(import.meta.env.VITE_ATTN_GIT_COMMIT),
  buildTime: readBuildEnv(import.meta.env.VITE_ATTN_BUILD_TIME),
};

interface AutomationRequest {
  request_id: string;
  action: string;
  payload?: Record<string, unknown> | null;
}

interface AutomationResponse {
  request_id: string;
  ok: boolean;
  result?: unknown;
  error?: string;
}

interface UseUiAutomationBridgeArgs {
  sessions: Session[];
  activeSessionId: string | null;
  daemonReady?: boolean;
  connectionError?: string | null;
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  createSession: (label: string, cwd: string, id?: string, agent?: SessionAgent, endpointId?: string, yoloMode?: boolean, options?: { chiefOfStaff?: boolean; resumeConversationFile?: string }) => Promise<string>;
  selectSession: (sessionId: string) => void;
  selectWorkspace: (workspaceId: string) => void;
  moveWorkspaceLeafToWorkspace: (
    sourceWorkspaceId: string,
    targetWorkspaceId: string,
    leafId: string,
    options?: { anchorId?: string; edge?: 'left' | 'right' | 'top' | 'bottom'; ratio?: number },
  ) => Promise<unknown>;
  closeSession: (sessionId: string) => Promise<void>;
  reloadSession?: (sessionId: string, size?: { cols: number; rows: number }) => Promise<void>;
  setSetting?: (key: string, value: string) => void;
  openDockPanel?: (panelId: string) => void;
  openShortcutEditor?: () => void;
  splitPane: (sessionId: string, targetPaneId: string, direction: TerminalSplitDirection) => Promise<unknown>;
  closePane: (sessionId: string, paneId: string) => Promise<unknown>;
  focusPane: (sessionId: string, paneId: string) => void;
  typeInSessionPaneViaUI: (sessionId: string, paneId: string, text: string) => boolean;
  isSessionPaneInputFocused: (sessionId: string, paneId: string) => boolean;
  scrollSessionPaneToTop: (sessionId: string, paneId: string) => boolean;
  getPaneText: (sessionId: string, paneId: string) => string;
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null;
  getPaneVisibleContent: (sessionId: string, paneId: string) => TerminalVisibleContentSnapshot;
  getPaneVisibleStyleSummary: (sessionId: string, paneId: string) => TerminalVisibleStyleSnapshot;
  getPaneBlockState: (sessionId: string, paneId: string) => BlockStateSnapshot | null;
  getPanePlacementState: (sessionId: string, paneId: string) => PlacementStateSnapshot | null;
  fitSessionActivePane: (sessionId: string) => void;
  sendRuntimeInput: (runtimeId: string, data: string, source?: string) => void;
  isRuntimeAttached: (runtimeId: string) => boolean;
  openAutomationsPanel?: () => void;
  presentationNotices?: Presentation[];
  resetSessionPaneTerminal: (sessionId: string, paneId: string) => boolean;
  injectSessionPaneBytes: (sessionId: string, paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectSessionPaneBase64: (sessionId: string, paneId: string, payload: string) => Promise<boolean>;
  drainSessionPaneTerminal: (sessionId: string, paneId: string) => Promise<boolean>;
}

function nextAnimationFrame() {
  return new Promise<void>((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      resolve();
    };
    const timeoutId = window.setTimeout(finish, 50);
    window.requestAnimationFrame(() => {
      window.clearTimeout(timeoutId);
      finish();
    });
  });
}

// ResizeObserver follow runs after layout, before paint: a read forced between
// them sees a gap no frame ever shows. A task after the frame reads the painted state.
async function afterNextPaint() {
  await nextAnimationFrame();
  await new Promise<void>((resolve) => { window.setTimeout(resolve, 0); });
}

function waitForBenchmarkDelay(delayMs: number) {
  return new Promise<void>((resolve) => {
    window.setTimeout(resolve, delayMs);
  });
}

// Output paints are paced at 30 Hz, so wait for a paint after the seed rather than a fixed interval.
async function waitForSeededPaint(
  readPerf: () => { renderCount: number } | undefined,
  renderCountBeforeSeed: number,
  maxFrames = 120,
): Promise<void> {
  for (let frame = 0; frame < maxFrames; frame += 1) {
    if ((readPerf()?.renderCount ?? 0) > renderCountBeforeSeed) return;
    await nextAnimationFrame();
  }
}

async function waitForPaneWriteQuiescence(
  readWriteCount: () => number | null,
  quietFrames = 3,
  maxFrames = 120,
): Promise<void> {
  let last = readWriteCount();
  let quiet = 0;
  for (let frame = 0; frame < maxFrames && quiet < quietFrames; frame += 1) {
    await nextAnimationFrame();
    const current = readWriteCount();
    quiet = current === last ? quiet + 1 : 0;
    last = current;
  }
}

// The awaits must stay sequential: nextAnimationFrame() registers its callback when it
// is constructed, so building them all up front queues them onto the same frame.
async function settleUi(frames = 2) {
  for (let index = 0; index < frames; index += 1) {
    await nextAnimationFrame();
  }
}

function resolvePaneId(
  session: Session | undefined,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
  paneId?: unknown
) {
  if (!session) {
    throw new Error('Session not found');
  }
  if (typeof paneId === 'string' && paneId.length > 0) {
    return paneId;
  }
  return getActivePaneIdForSession(session);
}

function resolveRuntimeId(session: Session, paneId: string): string {
  const agent = session.workspace.agents.find((entry) => entry.id === paneId);
  if (agent?.runtimeId) {
    return agent.runtimeId;
  }
  throw new Error(`No runtime found for pane ${paneId}`);
}

function resolvePaneOwnerSessionId(session: Session, paneId: string): string {
  return session.workspace.agents.find((entry) => entry.id === paneId)?.sessionId || session.id;
}

function resolveWorkspaceViewSessionId(session: Session, sessions: Session[], activeSessionId: string | null): string {
  const activeSession = activeSessionId ? sessions.find((entry) => entry.id === activeSessionId) : null;
  if (activeSession?.workspaceId && activeSession.workspaceId === session.workspaceId) {
    return activeSession.id;
  }
  return session.id;
}

function paneEntries(session: Session) {
  return session.workspace.agents.map((agent) => ({
      paneId: agent.id,
      runtimeId: agent.runtimeId,
      sessionId: agent.sessionId,
      kind: 'agent',
      title: agent.title || 'Session',
    }));
}

function serializeWorkspaceModel(
  session: Session,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
) {
  return {
    activePaneId: getActivePaneIdForSession(session),
    daemonActivePaneId: session.daemonActivePaneId,
    panes: paneEntries(session),
    layoutTree: session.workspace.layoutTree,
    layout: collectWorkspaceLayoutDiagnostics(session.workspace.layoutTree),
    sessionPaneCount: session.workspace.agents.length,
  };
}

function serializeSession(session: Session, getActivePaneIdForSession: (session: Session | undefined | null) => string) {
  const workspace = serializeWorkspaceModel(session, getActivePaneIdForSession);
  return {
    id: session.id,
    label: session.label,
    state: session.state,
    cwd: session.cwd,
    workspaceId: session.workspaceId,
    agent: session.agent,
    activePaneId: workspace.activePaneId,
    daemonActivePaneId: workspace.daemonActivePaneId,
    panes: workspace.panes,
    workspace,
  };
}

function summarizeSession(
  session: Session,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
) {
  return {
    id: session.id,
    label: session.label,
    cwd: session.cwd,
    state: session.state,
    agent: session.agent,
    activePaneId: getActivePaneIdForSession(session),
    daemonActivePaneId: session.daemonActivePaneId,
    sessionPaneCount: session.workspace.agents.length,
  };
}

function rectSnapshot(element: Element | null) {
  if (!(element instanceof HTMLElement)) {
    return null;
  }
  const rect = element.getBoundingClientRect();
  return {
    x: Math.round(rect.x),
    y: Math.round(rect.y),
    width: Math.round(rect.width),
    height: Math.round(rect.height),
  };
}

function boxFromRect(rect: { width: number; height: number } | null | undefined) {
  if (!rect) {
    return null;
  }
  return {
    width: rect.width,
    height: rect.height,
  };
}

function parseDataFlag(value: string | null | undefined) {
  if (value === '1' || value === 'true') {
    return true;
  }
  if (value === '0' || value === 'false') {
    return false;
  }
  return null;
}

function elementMetrics(element: Element | null) {
  if (!(element instanceof HTMLElement)) {
    return null;
  }
  const style = getComputedStyle(element);
  return {
    bounds: rectSnapshot(element),
    clientWidth: element.clientWidth,
    clientHeight: element.clientHeight,
    scrollWidth: element.scrollWidth,
    scrollHeight: element.scrollHeight,
    offsetWidth: element.offsetWidth,
    offsetHeight: element.offsetHeight,
    display: style.display,
    visibility: style.visibility,
  };
}

function getSessionWorkspaceRoot(workspaceId: string) {
  const root = document.querySelector(`[data-session-terminal-workspace="${workspaceId}"]`);
  return root instanceof HTMLElement ? root : null;
}

function collectWorkspaceShellMetrics(sessionId: string) {
  const workspaceRoot = getSessionWorkspaceRoot(sessionId);
  const terminalWrapper = workspaceRoot?.closest('.terminal-wrapper') ?? null;
  const terminalMainArea = terminalWrapper?.closest('.terminal-main-area') ?? null;
  const terminalPane = terminalMainArea?.closest('.terminal-pane') ?? null;
  const viewContainer = terminalPane?.closest('.view-container') ?? null;

  return {
    viewContainer: elementMetrics(viewContainer),
    terminalPane: elementMetrics(terminalPane),
    terminalMainArea: elementMetrics(terminalMainArea),
    terminalWrapper: elementMetrics(terminalWrapper),
    workspaceRoot: elementMetrics(workspaceRoot),
  };
}

function collectWorkspaceViewState(sessionId: string) {
  const workspaceRoot = getSessionWorkspaceRoot(sessionId);
  return {
    sessionVisible: parseDataFlag(workspaceRoot?.dataset.sessionVisible),
    activePaneId: workspaceRoot?.dataset.activePaneId || null,
    activeLeafId: workspaceRoot?.dataset.activeLeafId || null,
    zoomedPaneId: workspaceRoot?.dataset.zoomedPaneId || null,
    maximizedPaneId: workspaceRoot?.dataset.maximizedPaneId || null,
  };
}

function collectSplitDomMetrics(sessionId: string) {
  const workspaceRoot = getSessionWorkspaceRoot(sessionId);
  if (!workspaceRoot) {
    return [];
  }
  return Array.from(workspaceRoot.querySelectorAll('[data-split-id]'))
    .filter((element): element is HTMLElement => element instanceof HTMLElement)
    .map((element) => {
      const childElements = Array.from(element.children)
        .filter((child): child is HTMLElement => child instanceof HTMLElement && child.matches('[data-split-child-index]'));
      const firstChild = childElements.find((child) => child.dataset.splitChildIndex === '0') ?? null;
      const secondChild = childElements.find((child) => child.dataset.splitChildIndex === '1') ?? null;
      return {
        splitId: element.dataset.splitId || '',
        path: element.dataset.splitPath || '',
        direction: element.dataset.splitDirection || '',
        ratio: element.dataset.splitRatio ? Number.parseFloat(element.dataset.splitRatio) : null,
        dom: elementMetrics(element),
        firstChild: {
          path: firstChild instanceof HTMLElement ? firstChild.dataset.splitChildPath || '' : '',
          dom: elementMetrics(firstChild),
        },
        secondChild: {
          path: secondChild instanceof HTMLElement ? secondChild.dataset.splitChildPath || '' : '',
          dom: elementMetrics(secondChild),
        },
      };
    });
}

function collectPaneDomMetrics(paneElement: Element | null) {
  if (!(paneElement instanceof HTMLElement)) {
    return null;
  }
  const paneBody = paneElement.querySelector('.workspace-pane-body');
  const terminalContainer = paneElement.querySelector('.terminal-container');
  const terminalSurface = paneElement.querySelector('.ghostty-terminal');
  const canvas = paneElement.querySelector('.ghostty-terminal canvas');

  return {
    paneBody: elementMetrics(paneBody),
    terminalContainer: elementMetrics(terminalContainer),
    terminalSurface: elementMetrics(terminalSurface),
    canvas: elementMetrics(canvas),
    errorVisible: paneElement.querySelector('.ghostty-terminal-error') != null,
  };
}

async function captureDomScreenshotData(selector?: string) {
  // An unresolved selector must NOT fall back to #root: that re-introduces the WebGL-canvas
  // serialization hang while hiding the real cause (the element asked for is not mounted).
  let target: HTMLElement;
  if (selector) {
    const selected = document.querySelector(selector);
    if (!(selected instanceof HTMLElement)) {
      throw new Error(`Screenshot selector not found in DOM: ${selector}`);
    }
    target = selected;
  } else {
    const root = document.getElementById('root') || document.body;
    if (!(root instanceof HTMLElement)) {
      throw new Error('Screenshot target not found');
    }
    target = root;
  }

  const { toPng } = await import('html-to-image');
  const backgroundColor = getComputedStyle(document.body).backgroundColor || '#111111';

  // A running CSS animation in the cloned subtree can leave html-to-image's serialized SVG
  // <image> in a never-settled load state in WebKit, so toPng hangs until the caller times out.
  const freeze = document.createElement('style');
  freeze.textContent =
    '*,*::before,*::after{animation:none!important;transition:none!important;}';
  document.head.appendChild(freeze);
  void document.body.offsetHeight;

  let dataUrl: string;
  try {
    dataUrl = await toPng(target, {
      cacheBust: true,
      pixelRatio: 1,
      backgroundColor,
      // Embedding @font-face resources fetches each font and can hang indefinitely.
      skipFonts: true,
    });
  } finally {
    freeze.remove();
  }
  return {
    source: 'web',
    bounds: rectSnapshot(target),
    pngBase64: dataUrl.replace(/^data:image\/png;base64,/, ''),
  };
}

function collectVisualSnapshot(
  sessions: Session[],
  activeSessionId: string | null,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
  getPaneText: (sessionId: string, paneId: string) => string,
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null,
  getPaneVisibleContent: (sessionId: string, paneId: string) => TerminalVisibleContentSnapshot,
  isRuntimeAttached: (runtimeId: string) => boolean,
  options?: {
    includePaneText?: boolean;
    sessionIds?: Set<string> | null;
  },
) {
  const includePaneText = options?.includePaneText !== false;
  const filteredSessions = options?.sessionIds
    ? sessions.filter((session) => options.sessionIds?.has(session.id))
    : sessions;
  return {
    activeSessionId,
    activeElement: {
      tag: document.activeElement?.tagName || null,
      className: (document.activeElement as HTMLElement | null)?.className || null,
      ariaLabel: (document.activeElement as HTMLElement | null)?.getAttribute?.('aria-label') || null,
      text: document.activeElement?.textContent?.slice(0, 120) || '',
    },
    sessions: filteredSessions.map((session) => {
      const workspaceModel = serializeWorkspaceModel(session, getActivePaneIdForSession);
      const activePaneId = workspaceModel.activePaneId;
      const paneIds = workspaceModel.panes.map((pane) => pane.paneId);
      const runtimeIdByPaneId = new Map(
        workspaceModel.panes.map((pane) => [pane.paneId, pane.runtimeId] as const),
      );
      const workspaceId = session.workspaceId;
      const workspaceDom = collectWorkspaceShellMetrics(workspaceId);
      const workspaceView = collectWorkspaceViewState(workspaceId);
      const rootBounds = workspaceDom.workspaceRoot?.bounds;
      const paneLayoutById = new Map(
        workspaceModel.layout.panes.map((pane) => [
          pane.paneId,
          {
            path: pane.path,
            depth: pane.depth,
            normalizedBounds: pane.bounds,
            projectedBounds: rootBounds
              ? projectWorkspaceBounds(pane.bounds, rootBounds.width, rootBounds.height)
              : null,
          },
        ]),
      );
      const sidebarItem = document.querySelector(
        `[data-testid="sidebar-session-${session.id}"]`
      );
      return {
        id: session.id,
        label: session.label,
        activePaneId,
        daemonActivePaneId: session.daemonActivePaneId,
        workspace: {
          model: workspaceModel,
          view: workspaceView,
          dom: workspaceDom,
          splits: collectSplitDomMetrics(workspaceId),
        },
        sidebarItem: sidebarItem instanceof HTMLElement
          ? {
              text: sidebarItem.textContent || '',
              bounds: rectSnapshot(sidebarItem),
            }
          : null,
        workspaceBounds: workspaceDom.workspaceRoot?.bounds ?? null,
        panes: paneIds.map((paneId) => {
          const ownerSessionId = workspaceModel.panes.find((pane) => pane.paneId === paneId)?.sessionId || session.id;
          const paneElement = document.querySelector(
            `[data-pane-session-id="${ownerSessionId}"][data-pane-id="${paneId}"]`
          );
          const modelLayout = paneLayoutById.get(paneId) ?? null;
          const runtimeId = runtimeIdByPaneId.get(paneId) ?? null;
          return {
            paneId,
            sessionId: ownerSessionId,
            runtimeId,
            runtimeAttached: runtimeId ? isRuntimeAttached(runtimeId) : false,
            active: activePaneId === paneId,
            kind: 'agent',
            path: paneElement instanceof HTMLElement ? paneElement.dataset.panePath || null : null,
            bounds: rectSnapshot(paneElement),
            className: paneElement instanceof HTMLElement ? paneElement.className : null,
            dom: collectPaneDomMetrics(paneElement),
            layout: modelLayout,
            visibleContent: getPaneVisibleContent(session.id, paneId),
            text: includePaneText ? getPaneText(session.id, paneId) : '',
            size: getPaneSize(session.id, paneId),
          };
        }),
      };
    }),
  };
}

function collectSessionUiState(
  sessions: Session[],
  activeSessionId: string | null,
  sessionId: string,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
) {
  const session = sessions.find((entry) => entry.id === sessionId);
  if (!session) {
    return {
      sessionId,
      exists: false,
      selected: false,
      sidebarItem: null,
      workspaceBounds: null,
      agentPaneBounds: null,
      activePaneId: null,
      daemonActivePaneId: null,
      label: null,
      cwd: null,
    };
  }

  const sidebarItem = document.querySelector(
    `[data-testid="sidebar-session-${session.id}"]`
  );
  const firstAgentPaneId = session.workspace.agents[0]?.id || '';
  const firstAgentPane = firstAgentPaneId
    ? document.querySelector(`[data-pane-session-id="${session.id}"][data-pane-id="${firstAgentPaneId}"]`)
    : null;
  const settlingChip = firstAgentPane?.querySelector('[data-testid="settling-indicator"]') ?? null;
  const settlingFill = firstAgentPane?.querySelector('.settling-header-track-fill') ?? null;
  const workspaceId = session.workspaceId;
  const workspaceDom = collectWorkspaceShellMetrics(workspaceId);
  const workspaceView = collectWorkspaceViewState(workspaceId);
  const workspaceModel = serializeWorkspaceModel(session, getActivePaneIdForSession);

  return {
    sessionId,
    exists: true,
    selected: activeSessionId === session.id,
    label: session.label,
    cwd: session.cwd,
    activePaneId: getActivePaneIdForSession(session),
    daemonActivePaneId: session.daemonActivePaneId,
    sidebarItem: sidebarItem instanceof HTMLElement
      ? {
          text: sidebarItem.textContent || '',
          bounds: rectSnapshot(sidebarItem),
          automation: sidebarItem.querySelector('.automation-provenance')?.textContent?.trim() || '',
          pullRequest: sidebarItem.querySelector('.sidebar-session-pr')?.textContent?.trim() || '',
        }
      : null,
    workspaceBounds: workspaceDom.workspaceRoot?.bounds ?? null,
    workspace: {
      model: workspaceModel,
      view: workspaceView,
      dom: workspaceDom,
      splits: collectSplitDomMetrics(workspaceId),
    },
    agentPaneBounds: rectSnapshot(firstAgentPane),
    paneAutomation: firstAgentPane?.querySelector('.automation-provenance')?.textContent?.trim() || '',
    settling: settlingChip instanceof HTMLElement
      ? {
          text: settlingChip.textContent?.trim() || '',
          held: settlingChip.classList.contains('settling-header--held'),
          frozenBar: Boolean(settlingFill instanceof HTMLElement
            && settlingFill.classList.contains('settling-track-fill--held')),
        }
      : null,
  };
}

function collectRenderHealthSnapshot(
  sessions: Session[],
  activeSessionId: string | null,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
  getPaneText: (sessionId: string, paneId: string) => string,
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null,
  getPaneVisibleContent: (sessionId: string, paneId: string) => TerminalVisibleContentSnapshot,
  isSessionPaneInputFocused: (sessionId: string, paneId: string) => boolean,
  isRuntimeAttached: (runtimeId: string) => boolean,
  options?: {
    sessionIds?: Set<string> | null;
  },
) {
  const visualSnapshot = collectVisualSnapshot(
    sessions,
    activeSessionId,
    getActivePaneIdForSession,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    isRuntimeAttached,
    {
      includePaneText: false,
      sessionIds: options?.sessionIds || null,
    },
  );
  const terminalPerf = getTerminalPerfSnapshot();
  const filteredSessions = visualSnapshot.sessions || [];

  const sessionHealth = filteredSessions.map((session) => {
    const terminalsByPaneId = new Map(
      terminalPerf
        .filter((terminal) => (session.panes || []).some((pane) => pane.sessionId === terminal.sessionId && pane.paneId === terminal.paneId))
        .map((terminal) => {
          return [terminal.paneId || '', terminal] as const;
        }),
    );

    return buildSessionRenderHealth({
      sessionId: session.id,
      label: session.label,
      activePaneId: session.activePaneId,
      selected: activeSessionId === session.id,
      panes: (session.panes || []).map((pane) => {
        const terminal = terminalsByPaneId.get(pane.paneId) || null;
        return {
          paneId: pane.paneId,
          kind: pane.kind as 'agent',
          active: pane.active,
          inputFocused: isSessionPaneInputFocused(pane.sessionId || session.id, pane.paneId),
          size: pane.size,
          paneBounds: boxFromRect(pane.bounds),
          projectedBounds: boxFromRect(pane.layout?.projectedBounds),
          paneBodyBounds: boxFromRect(pane.dom?.paneBody?.bounds),
          terminalContainerBounds: boxFromRect(pane.dom?.terminalContainer?.bounds),
          terminalSurfaceBounds: boxFromRect(pane.dom?.terminalSurface?.bounds),
          canvasBounds: boxFromRect(pane.dom?.canvas?.bounds),
          terminal,
        };
      }),
    });
  });

  let warningPaneCount = 0;
  let errorPaneCount = 0;
  let paneCount = 0;
  for (const session of sessionHealth) {
    warningPaneCount += session.summary.warningPaneCount;
    errorPaneCount += session.summary.errorPaneCount;
    paneCount += session.summary.paneCount;
  }

  return {
    activeSessionId,
    capturedAt: new Date().toISOString(),
    summary: {
      sessionCount: sessionHealth.length,
      paneCount,
      warningPaneCount,
      errorPaneCount,
    },
    sessions: sessionHealth,
  };
}

function collectSessionRuntimeIds(sessions: Session[]) {
  const runtimeIds = new Set<string>();
  for (const session of sessions) {
    for (const agent of session.workspace.agents) {
      if (agent.runtimeId) {
        runtimeIds.add(agent.runtimeId);
      }
    }
  }
  return runtimeIds;
}

function summarizePtyRecentTraffic(
  recentEvents: ReturnType<typeof getPtyPerfSnapshot>['recentEvents'],
  runtimeIds: Set<string> | null,
) {
  const relevantEvents = runtimeIds && runtimeIds.size > 0
    ? recentEvents.filter((event) => typeof event.runtimeId === 'string' && runtimeIds.has(event.runtimeId))
    : recentEvents.slice();
  const foreignEvents = runtimeIds && runtimeIds.size > 0
    ? recentEvents.filter((event) => typeof event.runtimeId === 'string' && !runtimeIds.has(event.runtimeId))
    : [];

  const summarizeByRuntime = (events: typeof recentEvents) => {
    const byRuntime = new Map<string, {
      runtimeId: string;
      eventCount: number;
      wsEventCount: number;
      commandCount: number;
      ptyOutputCount: number;
      ptyInputCount: number;
      outputBase64Chars: number;
      inputBytes: number;
      lastAt: string | null;
      lastSeq: number | null;
      sources: Set<string>;
    }>();

    for (const event of events) {
      const runtimeId = event.runtimeId;
      if (!runtimeId) {
        continue;
      }

      let summary = byRuntime.get(runtimeId);
      if (!summary) {
        summary = {
          runtimeId,
          eventCount: 0,
          wsEventCount: 0,
          commandCount: 0,
          ptyOutputCount: 0,
          ptyInputCount: 0,
          outputBase64Chars: 0,
          inputBytes: 0,
          lastAt: null,
          lastSeq: null,
          sources: new Set<string>(),
        };
        byRuntime.set(runtimeId, summary);
      }

      summary.eventCount += 1;
      summary.lastAt = event.at || summary.lastAt;
      summary.lastSeq = typeof event.seq === 'number' ? event.seq : summary.lastSeq;

      if (event.kind === 'ws_event') {
        summary.wsEventCount += 1;
        if (event.event === 'pty_output') {
          summary.ptyOutputCount += 1;
          summary.outputBase64Chars += event.base64Chars;
        }
      } else {
        summary.commandCount += 1;
        if (event.command === 'pty_input') {
          summary.ptyInputCount += 1;
          summary.inputBytes += event.dataBytes;
        }
        if (event.source) {
          summary.sources.add(event.source);
        }
      }
    }

    return Array.from(byRuntime.values())
      .map((summary) => ({
        ...summary,
        sources: Array.from(summary.sources).sort(),
      }))
      .sort((left, right) => {
        if (left.eventCount !== right.eventCount) {
          return right.eventCount - left.eventCount;
        }
        return (right.lastAt || '').localeCompare(left.lastAt || '');
      });
  };

  return {
    runtimeIds: runtimeIds ? Array.from(runtimeIds).sort() : [],
    relevantEventCount: relevantEvents.length,
    foreignEventCount: foreignEvents.length,
    relevantRuntimes: summarizeByRuntime(relevantEvents).slice(0, 8),
    foreignRuntimes: summarizeByRuntime(foreignEvents).slice(0, 8),
    recentRelevantEvents: relevantEvents.slice(-24),
    recentForeignEvents: foreignEvents.slice(-24),
  };
}

function dispatchCombo(combo: Combo) {
  const event = new KeyboardEvent('keydown', {
    key: combo.key,
    code: combo.code,
    metaKey: !!combo.meta,
    ctrlKey: !!combo.ctrl,
    altKey: !!combo.alt,
    shiftKey: !!combo.shift,
    bubbles: true,
    cancelable: true,
  });
  window.dispatchEvent(event);
}

function dispatchShortcutEvent(shortcutId: ShortcutId) {
  if (!Object.prototype.hasOwnProperty.call(SHORTCUTS, shortcutId)) {
    throw new Error(`Unknown shortcut: ${shortcutId}`);
  }
  const binding = resolveBinding(shortcutId);
  if (!binding) {
    throw new Error(`Shortcut is unbound: ${shortcutId}`);
  }
  if (isChord(binding)) {
    dispatchCombo(binding.leader);
    dispatchCombo(binding.then);
    return;
  }
  dispatchCombo(binding);
}

function findActivePaneCanvas(): HTMLCanvasElement | null {
  const workspace = document.querySelector('[data-session-terminal-workspace][data-session-visible="1"]');
  const activePaneId = workspace?.getAttribute('data-active-pane-id');
  if (!workspace || !activePaneId) {
    return null;
  }
  const paneElement = workspace.querySelector(`[data-pane-id="${activePaneId}"]`);
  const canvas = paneElement?.querySelector('canvas');
  return canvas instanceof HTMLCanvasElement ? canvas : null;
}

function clickPaneElement(sessionId: string, paneId: string) {
  const element = document.querySelector(
    `[data-pane-session-id="${sessionId}"][data-pane-id="${paneId}"]`
  );
  if (!(element instanceof HTMLElement)) {
    throw new Error(`Pane element not found for ${sessionId}:${paneId}`);
  }
  // A folded pane is only its expand button; that button stops mousedown.
  const expand = element.querySelector('.workspace-suspended-leaf');
  if (element.dataset.paneSuspended === 'true' && expand instanceof HTMLElement) {
    expand.click();
    return;
  }

  element.dispatchEvent(new MouseEvent('mousedown', {
    bubbles: true,
    cancelable: true,
    view: window,
  }));
  element.dispatchEvent(new MouseEvent('mouseup', {
    bubbles: true,
    cancelable: true,
    view: window,
  }));
  element.dispatchEvent(new MouseEvent('click', {
    bubbles: true,
    cancelable: true,
    view: window,
  }));
}

function wheelPaneElement(sessionId: string, paneId: string, deltaY: number, deltaMode: number) {
  const paneElement = document.querySelector(
    `[data-pane-session-id="${sessionId}"][data-pane-id="${paneId}"]`
  );
  const terminal = paneElement?.querySelector('.terminal-container');
  if (!(terminal instanceof HTMLElement)) {
    throw new Error(`Terminal element not found for ${sessionId}:${paneId}`);
  }
  const rect = terminal.getBoundingClientRect();
  terminal.dispatchEvent(new WheelEvent('wheel', {
    bubbles: true,
    cancelable: true,
    view: window,
    clientX: rect.left + rect.width / 2,
    clientY: rect.top + rect.height / 2,
    deltaY,
    deltaMode,
  }));
}

// Cell math must use the canvas (grid) rect, not the container rect: the container is
// larger by the fit remainder, so proportional division clicks the wrong row.
function paneTerminalGrid(sessionId: string, paneId: string) {
  const paneElement = document.querySelector(
    `[data-pane-session-id="${sessionId}"][data-pane-id="${paneId}"]`
  );
  const terminal = paneElement?.querySelector('.terminal-container');
  if (!(terminal instanceof HTMLElement)) {
    throw new Error(`Terminal element not found for ${sessionId}:${paneId}`);
  }
  const canvas = terminal.querySelector('canvas');
  const gridRect = (canvas instanceof HTMLElement ? canvas : terminal).getBoundingClientRect();
  return { terminal, gridRect };
}

function paneCellPoint(
  gridRect: DOMRect,
  size: { cols: number; rows: number },
  cell: { col: number; row: number },
) {
  return {
    clientX: gridRect.left + ((cell.col + 0.5) / Math.max(1, size.cols)) * gridRect.width,
    clientY: gridRect.top + ((cell.row + 0.5) / Math.max(1, size.rows)) * gridRect.height,
  };
}

function clickPaneCell(
  sessionId: string,
  paneId: string,
  size: { cols: number; rows: number },
  cell: { col: number; row: number },
) {
  const { terminal, gridRect } = paneTerminalGrid(sessionId, paneId);
  const point = paneCellPoint(gridRect, size, cell);
  terminal.dispatchEvent(new MouseEvent('mousedown', {
    bubbles: true,
    cancelable: true,
    view: window,
    button: 0,
    buttons: 1,
    detail: 1,
    ...point,
  }));
  terminal.dispatchEvent(new MouseEvent('mouseup', {
    bubbles: true,
    cancelable: true,
    view: window,
    button: 0,
    detail: 1,
    ...point,
  }));
  terminal.dispatchEvent(new MouseEvent('click', {
    bubbles: true,
    cancelable: true,
    view: window,
    button: 0,
    detail: 1,
    ...point,
  }));
}

function paneCellRect(
  sessionId: string,
  paneId: string,
  size: { cols: number; rows: number },
  cell: { col: number; row: number },
) {
  const { gridRect } = paneTerminalGrid(sessionId, paneId);
  const point = paneCellPoint(gridRect, size, cell);
  return {
    centerX: point.clientX,
    centerY: point.clientY,
    innerWidth: window.innerWidth,
    innerHeight: window.innerHeight,
  };
}

function annotationSurfaceState() {
  const popup = document.querySelector('[data-testid="annotation-popup"]');
  const panel = document.querySelector('[data-testid="annotation-panel"]');
  const notice = document.querySelector('[data-testid="annotation-notice"]');
  const text = (root: Element | null | undefined, selector: string) =>
    root?.querySelector(selector)?.textContent?.trim() ?? '';
  const box = (node: Element | null) => {
    if (!node) return null;
    const rect = node.getBoundingClientRect();
    return { left: rect.left, top: rect.top, width: rect.width, height: rect.height };
  };
  return {
    popupOpen: Boolean(popup),
    popupRect: box(popup),
    panelRect: box(panel),
    viewport: { width: window.innerWidth, height: window.innerHeight },
    paneRects: Array.from(document.querySelectorAll('.terminal-container.ghostty-terminal'))
      .map((pane) => box(pane))
      .filter((rect): rect is NonNullable<typeof rect> => rect !== null),
    popupQuote: text(popup, '.anno-popup-quote'),
    popupQuoteRect: box(popup?.querySelector('.anno-popup-quote') ?? null),
    popupDraft: (popup?.querySelector('.anno-popup-text') as HTMLTextAreaElement | null)?.value ?? null,
    popupCommentRect: box(popup?.querySelector('.anno-popup-text') ?? null),
    popupDragHandleRect: box(popup?.querySelector('.anno-popup-drag-handle') ?? null),
    commentFocused: Boolean(
      popup
      && popup.querySelector('.anno-popup-text')
      && document.activeElement === popup.querySelector('.anno-popup-text'),
    ),
    labels: Array.from(popup?.querySelectorAll('.anno-popup-label') ?? [])
      .map((button) => button.getAttribute('aria-label') ?? ''),
    panelOpen: Boolean(panel),
    annotations: Array.from(panel?.querySelectorAll('.anno-card') ?? []).map((card) => ({
      emoji: text(card, '.anno-card-chip'),
      quote: text(card, '.anno-card-quote'),
      comment: text(card, '.anno-card-comment'),
      rect: box(card),
      removeRect: box(card.querySelector('.anno-card-remove')),
    })),
    note: (panel?.querySelector('.anno-panel-note') as HTMLTextAreaElement | null)?.value ?? null,
    footer: text(panel, '.anno-panel-foot'),
    notice: notice?.textContent?.trim() ?? null,
    noticeRect: box(notice),
  };
}

function terminalContextMenuState() {
  const menu = document.querySelector('[data-testid="terminal-context-menu"]');
  if (!menu) {
    return { open: false, items: [], innerWidth: window.innerWidth, innerHeight: window.innerHeight };
  }
  const items = Array.from(menu.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).map((element) => {
    const rect = element.getBoundingClientRect();
    return {
      id: element.getAttribute('data-testid')?.replace('terminal-context-menu-', '') ?? '',
      disabled: element.disabled,
      centerX: rect.left + rect.width / 2,
      centerY: rect.top + rect.height / 2,
    };
  });
  return { open: true, items, innerWidth: window.innerWidth, innerHeight: window.innerHeight };
}

function hoverPaneCell(
  sessionId: string,
  paneId: string,
  size: { cols: number; rows: number },
  cell: { col: number; row: number },
  meta: boolean,
  alt = false,
): HTMLElement {
  const { terminal, gridRect } = paneTerminalGrid(sessionId, paneId);
  if (alt) {
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Alt', altKey: true, bubbles: true }));
  }
  terminal.dispatchEvent(new MouseEvent('mousemove', {
    bubbles: true,
    cancelable: true,
    view: window,
    metaKey: meta,
    altKey: alt,
    ...paneCellPoint(gridRect, size, cell),
  }));
  // Returns the element, not its cursor: hover state routes through React, so the computed
  // style here is still the pre-move one. The caller reads it after settling.
  return terminal;
}

function dragPaneSelection(
  sessionId: string,
  paneId: string,
  size: { cols: number; rows: number },
  start: { col: number; row: number },
  end: { col: number; row: number },
  modifiers: { altKey?: boolean } = {},
) {
  const { terminal, gridRect } = paneTerminalGrid(sessionId, paneId);
  const startPoint = paneCellPoint(gridRect, size, start);
  const endPoint = paneCellPoint(gridRect, size, end);
  const altKey = modifiers.altKey === true;
  terminal.dispatchEvent(new MouseEvent('mousedown', {
    bubbles: true,
    cancelable: true,
    view: window,
    button: 0,
    buttons: 1,
    altKey,
    ...startPoint,
  }));
  terminal.dispatchEvent(new MouseEvent('mousemove', {
    bubbles: true,
    cancelable: true,
    view: window,
    buttons: 1,
    altKey,
    ...endPoint,
  }));
  terminal.dispatchEvent(new MouseEvent('mouseup', {
    bubbles: true,
    cancelable: true,
    view: window,
    button: 0,
    altKey,
    ...endPoint,
  }));
}

function dragLeafHeader(leafId: string, dropFracX: number, dropFracY: number) {
  const leaf = document.querySelector(`[data-pane-id="${leafId}"]`);
  const header = leaf?.querySelector('.workspace-pane-header, .workspace-dock-tile-header');
  if (!(header instanceof HTMLElement)) {
    throw new Error(`Draggable leaf header not found for ${leafId}`);
  }
  const container = header.closest('.session-terminal-panes');
  if (!(container instanceof HTMLElement)) {
    throw new Error(`Panes container not found for leaf ${leafId}`);
  }
  const headerRect = header.getBoundingClientRect();
  const containerRect = container.getBoundingClientRect();
  const clamp01 = (n: number) => Math.min(1, Math.max(0, n));
  const startX = headerRect.left + headerRect.width / 2;
  const startY = headerRect.top + headerRect.height / 2;
  const dropX = containerRect.left + clamp01(dropFracX) * containerRect.width;
  const dropY = containerRect.top + clamp01(dropFracY) * containerRect.height;

  const fire = (
    type: string,
    target: EventTarget,
    clientX: number,
    clientY: number,
    extra: PointerEventInit,
  ) => {
    target.dispatchEvent(new PointerEvent(type, {
      bubbles: true,
      cancelable: true,
      view: window,
      pointerId: 1,
      pointerType: 'mouse',
      isPrimary: true,
      clientX,
      clientY,
      ...extra,
    }));
  };

  fire('pointerdown', header, startX, startY, { button: 0, buttons: 1 });
  fire('pointermove', window, dropX, dropY, { buttons: 1 });
  fire('pointerup', window, dropX, dropY, { button: 0, buttons: 0 });

  return { startX, startY, dropX, dropY };
}

async function dragSplitDivider(
  workspaceId: string,
  splitId: string,
  deltaPx: number,
  steps: number,
) {
  const workspaceRoot = getSessionWorkspaceRoot(workspaceId);
  const separator = Array.from(workspaceRoot?.querySelectorAll('[role="separator"][data-split-id]') ?? [])
    .find((element): element is HTMLElement => (
      element instanceof HTMLElement && element.dataset.splitId === splitId
    ));
  if (!separator) {
    throw new Error(`Split divider not found for ${splitId}`);
  }
  const direction = separator.getAttribute('aria-orientation') === 'horizontal'
    ? 'horizontal'
    : 'vertical';
  const rect = separator.getBoundingClientRect();
  const startX = rect.left + rect.width / 2;
  const startY = rect.top + rect.height / 2;
  const pointerId = 1;
  const moveCount = Math.max(1, Math.round(steps));
  const fire = (
    type: string,
    target: EventTarget,
    clientX: number,
    clientY: number,
    extra: PointerEventInit,
  ) => {
    target.dispatchEvent(new PointerEvent(type, {
      bubbles: true,
      cancelable: true,
      view: window,
      pointerId,
      pointerType: 'mouse',
      isPrimary: true,
      clientX,
      clientY,
      ...extra,
    }));
  };

  fire('pointerdown', separator, startX, startY, { button: 0, buttons: 1 });
  for (let index = 1; index <= moveCount; index += 1) {
    const progress = index / moveCount;
    const clientX = direction === 'vertical' ? startX + deltaPx * progress : startX;
    const clientY = direction === 'horizontal' ? startY + deltaPx * progress : startY;
    fire('pointermove', window, clientX, clientY, { buttons: 1 });
    await nextAnimationFrame();
  }
  const endX = direction === 'vertical' ? startX + deltaPx : startX;
  const endY = direction === 'horizontal' ? startY + deltaPx : startY;
  fire('pointerup', window, endX, endY, { button: 0, buttons: 0 });
  await settleUi(2);

  return {
    splitId,
    direction,
    startX,
    startY,
    endX,
    endY,
    steps: moveCount,
    splits: collectSplitDomMetrics(workspaceId),
  };
}

function clickElement(element: HTMLElement) {
  clickElementWithModifiers(element);
}

interface ClickModifiers {
  meta?: boolean;
  ctrl?: boolean;
  shift?: boolean;
  alt?: boolean;
}

function clickElementWithModifiers(element: HTMLElement, modifiers?: ClickModifiers) {
  const rect = element.getBoundingClientRect();
  const clientX = rect.x + rect.width / 2;
  const clientY = rect.y + rect.height / 2;
  const init: MouseEventInit = {
    bubbles: true,
    cancelable: true,
    view: window,
    clientX,
    clientY,
    metaKey: modifiers?.meta ?? false,
    ctrlKey: modifiers?.ctrl ?? false,
    shiftKey: modifiers?.shift ?? false,
    altKey: modifiers?.alt ?? false,
  };
  // Pointer events first, in the order a real browser fires them: anything listening for
  // pointerdown is otherwise invisible and the scenario reads as passing.
  const pointerInit: PointerEventInit = { ...init, pointerId: 1, pointerType: 'mouse', isPrimary: true };
  element.dispatchEvent(new PointerEvent('pointerdown', pointerInit));
  element.dispatchEvent(new MouseEvent('mousedown', init));
  element.dispatchEvent(new PointerEvent('pointerup', pointerInit));
  element.dispatchEvent(new MouseEvent('mouseup', init));
  element.dispatchEvent(new MouseEvent('click', init));
}

function setInputValue(element: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    'value',
  )?.set;
  if (!setter) {
    throw new Error('Unable to resolve input value setter');
  }
  setter.call(element, value);
  element.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
}

// Bypass React's value-tracker via the native prototype setter, then fire both `input`
// and `change` so the component's onChange runs exactly as a user edit would.
function setControlValue(
  element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement,
  value: string,
) {
  const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), 'value')?.set;
  if (!setter) {
    throw new Error('Unable to resolve control value setter');
  }
  setter.call(element, value);
  element.dispatchEvent(new Event('input', { bubbles: true, cancelable: true }));
  element.dispatchEvent(new Event('change', { bubbles: true, cancelable: true }));
}

function clickTestId(testid: string) {
  const element = document.querySelector(`[data-testid="${testid}"]`);
  if (!(element instanceof HTMLElement)) {
    throw new Error(`Element not found: [data-testid="${testid}"]`);
  }
  for (const type of ['mousedown', 'mouseup', 'click'] as const) {
    element.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, view: window }));
  }
}

async function waitForTestId(testid: string, maxFrames = 120) {
  for (let frame = 0; frame < maxFrames; frame += 1) {
    if (document.querySelector(`[data-testid="${testid}"]`) instanceof HTMLElement) return;
    await nextAnimationFrame();
  }
  throw new Error(`Element not found: [data-testid="${testid}"]`);
}

function frontGardenPanel(): HTMLElement | null {
  const panel = document.querySelector('.garden-panel');
  return panel instanceof HTMLElement ? panel : null;
}

const FRAME_FLIGHT_FRAME_BUDGET = 90;
async function gardenFrameAtRest() {
  let last = -1;
  let still = 0;
  for (let frames = 0; frames < FRAME_FLIGHT_FRAME_BUDGET; frames += 1) {
    await nextAnimationFrame();
    const frame = document.querySelector('.garden-frame');
    const width = frame instanceof HTMLElement ? Math.round(frame.getBoundingClientRect().width) : -1;
    still = width === last ? still + 1 : 0;
    last = width;
    // Three frames unchanged: an eased transition never repeats a width, so one repeat means it landed.
    if (still >= 3) return;
  }
  // The flight is 180ms — about 11 frames at 60Hz — so reaching this means the box never settled.
  throw new Error(
    `the garden frame was still moving after ${FRAME_FLIGHT_FRAME_BUDGET} frames (last width ${last})`,
  );
}

function collectGardenBoardUiState() {
  const board = document.querySelector('.garden-board');
  if (!(board instanceof HTMLElement)) {
    return { present: false };
  }
  const trail = Array.from(board.querySelectorAll('.garden-board__trail-step')).map((step) => ({
    label: step.textContent?.trim() ?? '',
  }));
  const columns = Array.from(board.querySelectorAll('[data-column]')).map((column) => ({
    key: (column as HTMLElement).dataset.column ?? '',
    count: column.querySelector('.garden-board__count')?.textContent?.trim() ?? '',
    collapsed: column.classList.contains('is-collapsed'),
    empty: column.querySelector('.garden-board__empty')?.textContent?.trim() ?? '',
    cards: Array.from(column.querySelectorAll('.garden-card')).map((card) => ({
      id: card.querySelector('.garden-card__id')?.textContent?.trim() ?? '',
      title: card.querySelector('.garden-card__title')?.textContent?.trim() ?? '',
      selected: card.classList.contains('is-selected'),
      plot: Boolean(card.querySelector('.garden-card__drill')),
      armed: card.querySelector('.garden-card__armed')?.textContent?.trim() ?? '',
    })),
    zones: Array.from(column.querySelectorAll('[data-zone]')).map(
      (zone) => (zone as HTMLElement).dataset.zone ?? '',
    ),
  }));
  const compose = board.querySelector('.garden-compose');
  return {
    present: true,
    trail,
    // Root renders no trail at all; inside a plot the nav is Garden + one step
    // per level, so the depth is one less than what it shows.
    depth: Math.max(0, trail.length - 1),
    columns,
    menu: Array.from(board.querySelectorAll('[role="menuitem"]')).map(
      (item) => item.textContent?.trim() ?? '',
    ),
    compose: compose
      ? {
          verb: compose.querySelector('.garden-compose__verb')?.textContent?.trim() ?? '',
          seed: compose.querySelector('.garden-compose__id')?.textContent?.trim() ?? '',
          taking: compose.querySelector('.garden-compose__taking')?.textContent?.trim() ?? '',
        }
      : null,
  };
}

function collectGardenUiState() {
  const panel = frontGardenPanel();
  if (!panel) {
    return { present: false };
  }
  const frameBox = panel.closest('.garden-frame');
  const trail = Array.from(panel.querySelectorAll('.garden-trail__step')).map((step) => ({
    label: step.textContent?.trim() ?? '',
    depth: Number.parseInt(step.getAttribute('data-trail-depth') ?? '-1', 10),
    here: false,
  }));
  // Root renders no head; the trail nav names the place instead ("The garden").
  const here = panel.querySelector('.garden-head__title')?.textContent?.trim()
    || panel.querySelector('.garden-trail__here')?.textContent?.trim()
    || '';
  if (here) trail.push({ label: here, depth: trail.length, here: true });
  const seeds = Array.from(panel.querySelectorAll('.garden-row')).map((row) => ({
    id: row.querySelector('[data-seed-row]')?.getAttribute('data-seed-row') ?? '',
    displayId: row.querySelector('.garden-row__id')?.textContent?.trim() ?? '',
    title: row.querySelector('.garden-row__title')?.textContent?.trim() ?? '',
    status: (row.className.match(/is-(planted|growing|harvested|withered|dormant|unknown)/) ?? [])[1] ?? '',
    signal: row.querySelector('.garden-row__signal')?.textContent?.trim() ?? '',
    armed: row.querySelector('.garden-row__armed')?.textContent?.trim() ?? '',
    tender: row.querySelector('.garden-row__tender')?.textContent?.trim() ?? '',
    plot: row.querySelector('.garden-row__plot')?.textContent?.trim() ?? '',
    home: row.querySelector('.garden-row__home')?.textContent?.trim() ?? '',
    snippet: row.querySelector('.garden-row__snippet')?.textContent?.trim() ?? '',
  }));
  const viewport = panel.querySelector('.garden-viewport');
  const focused = document.activeElement;
  const columnsBox = panel.querySelector('.garden-columns');
  const columns = Array.from(
    panel.querySelectorAll('.garden-column:not(.garden-column--reader):not(.garden-column--results)'),
  ).map((col) => ({
    key: col.getAttribute('data-column') ?? '',
    rows: col.querySelectorAll('[data-seed-row]').length,
    selected: col.querySelector('.garden-row.is-selected [data-seed-row]')?.getAttribute('data-seed-row') ?? '',
    scrollTop: col instanceof HTMLElement ? Math.round(col.scrollTop) : -1,
  }));
  const field = panel.querySelector('.garden-search__input');
  const closed = panel.querySelector('.garden-chrome__scope');
  const activeDescendant = field instanceof HTMLInputElement ? field.getAttribute('aria-activedescendant') : null;
  return {
    present: true,
    frame: frameBox?.classList.contains('is-full') ? 'full' : frameBox ? 'dock' : 'none',
    frameWidth: frameBox instanceof HTMLElement ? Math.round(frameBox.clientWidth) : -1,
    layout: columnsBox ? 'columns' : 'stack',
    panes: columnsBox instanceof HTMLElement ? Number(columnsBox.dataset.panes ?? 0) : 0,
    boxWidth: columnsBox instanceof HTMLElement ? Math.round(columnsBox.clientWidth) : -1,
    columns,
    trail,
    here,
    crown: panel.querySelector('.garden-head__progress')?.textContent?.trim() ?? '',
    empty: panel.querySelector('.garden-empty')?.textContent?.trim() ?? '',
    scrollTop: viewport instanceof HTMLElement ? Math.round(viewport.scrollTop) : -1,
    scrollHeight: viewport instanceof HTMLElement ? viewport.scrollHeight : -1,
    focusedRow: focused instanceof HTMLElement ? focused.getAttribute('data-seed-row') ?? '' : '',
    seeds,
    search: {
      query: field instanceof HTMLInputElement ? field.value : '',
      focused: document.activeElement === field,
      scope: field?.getAttribute('aria-label') ?? '',
      hint: panel.querySelector('.garden-search__meta')?.textContent?.trim() ?? '',
      results: panel.querySelectorAll('#garden-results [data-seed-row]').length,
      activeSeed: activeDescendant?.replace('garden-row-', '') ?? '',
      closedToggle: closed
        ? { label: closed.textContent?.trim() ?? '', on: closed.getAttribute('aria-pressed') === 'true' }
        : null,
      nothing: panel.querySelector('.garden-nothing__line')?.textContent?.trim() ?? '',
      moves: Array.from(panel.querySelectorAll('.garden-nothing__moves button')).map(
        (move) => move.textContent?.trim() ?? '',
      ),
    },
  };
}

function collectGardenSeedPage() {
  const page = document.querySelector('.garden-page');
  if (!(page instanceof HTMLElement)) return { present: false };
  return {
    present: true,
    title: page.querySelector('.garden-head__title')?.textContent?.trim() ?? '',
    meta: page.querySelector('.garden-head__meta')?.textContent?.trim() ?? '',
    body: page.querySelector('.garden-body')?.textContent?.trim() ?? '',
    artifacts: Array.from(page.querySelectorAll('.seed-artifact')).map((item) => ({
      kind: item.querySelector('.seed-artifact__kind')?.textContent?.trim() ?? '',
      primary: item.querySelector('.seed-artifact__primary')?.textContent?.trim() ?? '',
      secondary: item.querySelector('.seed-artifact__secondary')?.textContent?.trim() ?? '',
      gone: Boolean(item.querySelector('.seed-artifact__gone')),
      external: Boolean(item.querySelector('.seed-artifact__leaves')),
    })),
    // Attach and detach notes sit behind the "attachment changes" disclosure
    // and only join this list once garden_expand_seed opens it (bookkeeping: true).
    notes: Array.from(page.querySelectorAll('.garden-log > li')).map((note) => ({
      kind: note.getAttribute('data-kind') ?? '',
      body: note.querySelector('.garden-log__body')?.textContent?.trim() ?? '',
    })),
    bookkeeping: page.querySelector('.garden-closed-toggle')?.textContent?.trim() ?? '',
    resumeAvailable: Boolean(page.querySelector('[data-testid^="seed-resume-"]')),
    handoverAvailable: Boolean(page.querySelector('[data-testid^="seed-handover-"]')),
  };
}

function collectNotificationsUiState() {
  const panel = document.querySelector('.notifications-panel');
  if (!(panel instanceof HTMLElement)) return { present: false, rows: [] };
  return {
    present: true,
    empty: panel.querySelector('.notifications-panel-empty')?.textContent?.trim() ?? '',
    rows: Array.from(panel.querySelectorAll('.notification-row')).map((row) => ({
      title: row.querySelector('.notification-row-title')?.textContent?.trim() ?? '',
      severity: row.querySelector('.notification-sev-tag')?.textContent?.trim() ?? '',
      preview: row.querySelector('.notification-row-preview')?.textContent?.trim() ?? '',
      unread: row.classList.contains('is-unread'),
    })),
  };
}

function collectSeedDocumentState(scope: string, seedId: string) {
  const named = seedId ? `[data-seed-id="${seedId}"]` : '';
  const root = document.querySelector(`${scope} .seed-document${named}`);
  if (!(root instanceof HTMLElement)) {
    return { present: false };
  }
  const tile = root.closest('.workspace-dock-tile');
  const plot = root.querySelector('.seed-document__plot');
  const body = root.querySelector('.md-reader-wrap');
  const log = root.querySelector('.seed-document__ledger');
  return {
    present: true,
    body: body?.textContent?.trim() ?? '',
    children: Array.from(root.querySelectorAll<HTMLElement>('[data-seed-target]')).map((item) => ({
      id: item.dataset.seedTarget ?? '',
      title: item.querySelector('.seed-document__child-title')?.textContent?.trim() ?? '',
    })),
    plotBeforeBody: Boolean(plot && body && (plot.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING)),
    logOpen: log instanceof HTMLDetailsElement && log.open,
    parent: tile?.querySelector('.workspace-dock-tile-seed-parent span:last-child')?.textContent?.trim() ?? '',
    artifacts: Array.from(root.querySelectorAll('.seed-document__artifacts li')).map(
      (item) => item.textContent?.trim() ?? '',
    ),
    notes: Array.from(root.querySelectorAll('.seed-document__notes li')).map((note) => ({
      kind: note.getAttribute('data-kind') ?? '',
      body: note.querySelector('.seed-document__note-body')?.textContent?.trim() ?? '',
    })),
  };
}

function collectAutomationsUiState() {
  const panel = document.querySelector('[data-testid="automations-panel"]');
  if (!(panel instanceof HTMLElement)) {
    return { present: false };
  }
  const definitionRows = Array.from(
    panel.querySelectorAll('[data-testid="automation-definition-row"]'),
  );
  const definitions = definitionRows.map((row) => {
    const id = row.getAttribute('data-definition-id') ?? '';
    const toggle = row.querySelector(`[data-testid="automation-toggle-${id}"]`);
    return {
      id,
      name: row.querySelector('.automations-panel__name')?.textContent?.trim() ?? '',
      trigger: row.querySelector('.automations-panel__trigger')?.textContent?.trim() ?? '',
      enabled: toggle instanceof HTMLInputElement ? toggle.checked : false,
      selected: row.classList.contains('is-selected'),
      failed: Boolean(row.querySelector(`[data-testid="automation-failure-badge-${id}"]`)),
      canRunNow: Boolean(row.querySelector(`[data-testid="automation-run-now-${id}"]`)),
      toggleError:
        row.querySelector(`[data-testid="automation-toggle-error-${id}"]`)?.textContent?.trim() ?? '',
      runError:
        row.querySelector(`[data-testid="automation-run-error-${id}"]`)?.textContent?.trim() ?? '',
    };
  });
  const runsSection = panel.querySelector('[data-testid="automations-panel-runs"]');
  const runs = runsSection
    ? Array.from(runsSection.querySelectorAll('[data-testid="automation-run-row"]')).map((row) => ({
        id: row.getAttribute('data-run-id') ?? '',
        state: row.getAttribute('data-state') ?? '',
        navigable: Boolean(row.querySelector('button.automations-panel__run-row-main')),
        automation: row.querySelector('.automation-provenance')?.textContent?.trim() ?? '',
        lastError: row.querySelector('.automations-panel__run-error')?.textContent?.trim() ?? '',
      }))
    : [];
  return {
    present: true,
    empty: Boolean(panel.querySelector('[data-testid="automations-panel-empty"]')),
    error: panel.querySelector('[data-testid="automations-panel-error"]')?.textContent?.trim() ?? '',
    definitions,
    runs,
  };
}

const INACTIVE_AUTOMATION_FORM_STATE: AutomationFormAutomationState = {
  present: false,
  mode: 'create',
  definitionId: null,
  revision: 0,
  status: 'ready',
  loadError: '',
  values: {
    name: '',
    id: '',
    idCustomized: false,
    trigger: 'manual',
    scheduleCron: '',
    continuity: 'fresh',
    catchUp: '',
    repositoriesInclude: [],
    repositoriesExclude: [],
    agent: 'codex',
    model: '',
    effort: '',
    executable: '',
    directoryPath: '',
    repositoryOverrides: [],
    prompt: '',
  },
  errors: {},
  saving: false,
  saveError: '',
  saveErrorCode: '',
  enabled: null,
  compiledSentence: '',
  deleteArmed: false,
};

function collectAutomationFormUiState() {
  return getAutomationFormAutomationHandle()?.getState() ?? INACTIVE_AUTOMATION_FORM_STATE;
}

function collectAutoModeUiState() {
  return getAutoModeAutomationHandle()?.getState() ?? INACTIVE_AUTOMODE_STATE;
}

function getLocationPickerRoot() {
  const root = document.querySelector('[data-testid="location-picker"]');
  return root instanceof HTMLElement ? root : null;
}

function getLocationPickerOverlay() {
  const overlay = document.querySelector('[data-testid="location-picker-overlay"]');
  return overlay instanceof HTMLElement ? overlay : null;
}

function collectSessionsPanelUiState() {
  const root = document.querySelector('.sessions-panel');
  if (!root) {
    return { open: false, scope: '', range: '', workspace: '', repository: '', rows: [], footer: '', canLoadMore: false, state: '' };
  }
  const selectValue = (label: string) => {
    const select = Array.from(root.querySelectorAll('label.sessions-filter'))
      .find((entry) => entry.querySelector('span')?.textContent?.trim() === label)
      ?.querySelector('select');
    return select instanceof HTMLSelectElement ? select.value : '';
  };
  const rows = Array.from(root.querySelectorAll('tbody tr')).map((row) => {
    const cells = Array.from(row.querySelectorAll('td')).map((cell) => cell.textContent?.trim() || '');
    return {
      id: row.querySelector('.sessions-id')?.textContent?.trim() || '',
      label: row.querySelector('.sessions-label')?.textContent?.trim() || '',
      agent: cells[1] || '',
      state: row.querySelector('.sessions-state-chip')?.textContent?.trim() || '',
      workspace: cells[3] || '',
      where: row.querySelector('td:nth-child(5) span[title]')?.textContent?.trim() || '',
      branch: row.querySelector('.sessions-branch')?.textContent?.trim() || '',
      seed: cells[5] || '',
      when: cells[6] || '',
      verdict: cells[7] || '',
      refreshing: !!row.querySelector('.sessions-verdict-refreshing'),
      actions: Array.from(row.querySelectorAll('.sessions-actions button')).map((button) => button.textContent?.trim() || ''),
    };
  });
  const loadMore = Array.from(root.querySelectorAll('.sessions-footer button'))
    .find((button) => (button.textContent || '').startsWith('Load'));
  return {
    open: true,
    scope: Array.from(root.querySelectorAll('.sessions-scope button'))
      .find((button) => button.getAttribute('aria-pressed') === 'true')?.textContent?.trim() || '',
    range: selectValue('When'),
    workspace: selectValue('Workspace'),
    repository: selectValue('Repository'),
    rows,
    footer: root.querySelector('.sessions-footer span')?.textContent?.trim() || '',
    canLoadMore: !!loadMore,
    state: root.querySelector('.sessions-state')?.textContent?.trim() || '',
  };
}

function sessionsPanelRoot(): Element {
  const root = document.querySelector('.sessions-panel');
  if (!root) throw new Error('the Sessions surface is not open');
  return root;
}

function collectMarkdownOpenerUiState() {
  const root = document.querySelector('.markdown-opener');
  if (!root) {
    return { open: false, query: '', rows: [], emptyText: '' };
  }
  const input = root.querySelector('.markdown-opener-input');
  const rows = Array.from(root.querySelectorAll('.markdown-opener-option')).map((row) => ({
    title: row.querySelector('.markdown-opener-option-title')?.textContent?.trim() || '',
    path: row.querySelector('.markdown-opener-option-path')?.textContent?.trim() || '',
    selected: row.getAttribute('aria-selected') === 'true',
  }));
  return {
    open: true,
    query: input instanceof HTMLInputElement ? input.value : '',
    rows,
    emptyText: root.querySelector('.markdown-opener-empty')?.textContent?.trim() || '',
  };
}

function collectLocationPickerUiState() {
  const root = getLocationPickerRoot();
  if (!root) {
    return {
      open: false,
      mode: null,
      title: null,
      pathInputValue: '',
      currentDir: '',
      selectedTarget: null,
      selectedAgent: null,
      targets: [],
      agents: [],
      recents: [],
      directories: [],
      emptyText: '',
      repoOptions: null,
    };
  }

  const title = root.querySelector('[data-testid="location-picker-title"]');
  const pathInput = root.querySelector('[data-testid="location-picker-path-input"]');
  const breadcrumb = root.querySelector('[data-testid="location-picker-breadcrumb-path"]');
  const empty = root.querySelector('[data-testid="location-picker-empty"]');
  const targetButtons = Array.from(root.querySelectorAll('.picker-endpoint-controls button'))
    .filter((button): button is HTMLButtonElement => button instanceof HTMLButtonElement)
    .map((button) => ({
      label: button.querySelector('.endpoint-option-name')?.textContent?.trim() || '',
      meta: button.querySelector('.endpoint-option-meta')?.textContent?.trim() || '',
      endpointId: button.dataset.endpointId || null,
      active: button.classList.contains('active'),
      disabled: button.disabled,
    }));
  const agentButtons = Array.from(root.querySelectorAll('.agent-option'))
    .filter((button): button is HTMLButtonElement => button instanceof HTMLButtonElement)
    .map((button) => ({
      label: button.querySelector('.agent-option-name')?.textContent?.trim() || '',
      shortcut: button.querySelector('.agent-shortcut')?.textContent?.trim() || '',
      active: button.classList.contains('active'),
      disabled: button.disabled,
    }));

  const pickerItems = Array.from(root.querySelectorAll('[data-testid^="location-picker-item-"]'))
    .filter((node): node is HTMLElement => node instanceof HTMLElement)
    .map((item) => ({
      index: Number.parseInt(item.dataset.index || '-1', 10),
      kind: item.dataset.kind || '',
      path: item.dataset.path || '',
      name: item.querySelector('.picker-name')?.textContent?.trim() || '',
      detail: item.querySelector('.picker-path')?.textContent?.trim() || '',
      selected: item.classList.contains('selected'),
    }))
    .sort((left, right) => left.index - right.index);

  const repoOptionsRoot = root.querySelector('[data-testid="repo-options"]');
  const repoOptions = repoOptionsRoot instanceof HTMLElement
    ? {
        items: Array.from(repoOptionsRoot.querySelectorAll('[data-testid^="repo-option-"]'))
          .filter((node): node is HTMLElement => node instanceof HTMLElement)
          .map((item) => ({
            index: Number.parseInt(item.dataset.optionIndex || '-1', 10),
            kind: item.dataset.optionKind || '',
            name: item.querySelector('.repo-option-name')?.textContent?.trim() || '',
            detail: item.querySelector('.repo-option-detail')?.textContent?.trim() || '',
            selected: item.classList.contains('selected'),
          }))
          .sort((left, right) => left.index - right.index),
        newWorktree: (() => {
          const form = repoOptionsRoot.querySelector('[data-testid="repo-new-worktree-form"]');
          if (!(form instanceof HTMLElement)) {
            return null;
          }
          const currentRadio = form.querySelector('[data-testid="repo-new-worktree-start-current"]');
          const defaultRadio = form.querySelector('[data-testid="repo-new-worktree-start-default"]');
          const input = form.querySelector('[data-testid="repo-new-worktree-input"]');
          return {
            visible: true,
            name: input instanceof HTMLInputElement ? input.value : '',
            startingBranch: currentRadio instanceof HTMLInputElement && currentRadio.checked
              ? 'current'
              : defaultRadio instanceof HTMLInputElement && defaultRadio.checked
                ? 'default'
                : null,
          };
        })(),
      }
    : null;

  return {
    open: true,
    mode: repoOptions ? 'repo-options' : 'path-input',
    title: title?.textContent?.trim() || '',
    pathInputValue: pathInput instanceof HTMLInputElement ? pathInput.value : '',
    currentDir: breadcrumb?.textContent?.trim() || '',
    selectedTarget: targetButtons.find((button) => button.active)?.label || null,
    selectedAgent: agentButtons.find((button) => button.active)?.label || null,
    targets: targetButtons,
    agents: agentButtons,
    recents: pickerItems.filter((item) => item.kind === 'recent'),
    directories: pickerItems.filter((item) => item.kind === 'directory'),
    emptyText: empty?.textContent?.trim() || '',
    repoOptions,
  };
}

async function getBrowserMemorySnapshot() {
  const performanceWithMemory = performance as typeof performance & {
    memory?: {
      usedJSHeapSize?: number;
      totalJSHeapSize?: number;
      jsHeapSizeLimit?: number;
    };
    measureUserAgentSpecificMemory?: () => Promise<{
      bytes: number;
      breakdown?: Array<{ bytes: number; attribution?: Array<{ scope?: string; url?: string }> }>;
    }>;
  };

  let performanceMemory: Record<string, number> | null = null;
  if (performanceWithMemory.memory) {
    performanceMemory = {
      usedJSHeapSize: performanceWithMemory.memory.usedJSHeapSize || 0,
      totalJSHeapSize: performanceWithMemory.memory.totalJSHeapSize || 0,
      jsHeapSizeLimit: performanceWithMemory.memory.jsHeapSizeLimit || 0,
    };
  }

  let userAgentSpecificMemory: { bytes: number; breakdownCount: number } | null = null;
  let userAgentSpecificMemoryError: string | null = null;
  if (typeof performanceWithMemory.measureUserAgentSpecificMemory === 'function') {
    try {
      const result = await Promise.race([
        performanceWithMemory.measureUserAgentSpecificMemory(),
        new Promise((_, reject) => {
          window.setTimeout(() => reject(new Error('measureUserAgentSpecificMemory timed out')), 400);
        }),
      ]) as {
        bytes: number;
        breakdown?: Array<{ bytes: number; attribution?: Array<{ scope?: string; url?: string }> }>;
      };
      userAgentSpecificMemory = {
        bytes: result.bytes,
        breakdownCount: result.breakdown?.length || 0,
      };
    } catch (error) {
      userAgentSpecificMemoryError = error instanceof Error ? error.message : String(error);
    }
  }

  return {
    performanceMemory,
    userAgentSpecificMemory,
    userAgentSpecificMemoryError,
  };
}

async function capturePerfSnapshot(
  sessions: Session[],
  activeSessionId: string | null,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
  options?: { includeMemory?: boolean; sessionIds?: Set<string> | null },
) {
  const scopedSessions = options?.sessionIds
    ? sessions.filter((session) => options.sessionIds?.has(session.id))
    : sessions;
  const scopedSessionIds = new Set(scopedSessions.map((session) => session.id));
  const scopedRuntimeIds = collectSessionRuntimeIds(scopedSessions);
  const allTerminalPerf = getTerminalPerfSnapshot();
  const terminals = options?.sessionIds
    ? allTerminalPerf.filter((terminal) => terminal.sessionId && scopedSessionIds.has(terminal.sessionId))
    : allTerminalPerf;
  const ptySnapshot = getPtyPerfSnapshot();
  const browserMemory = options?.includeMemory === false
    ? {
        performanceMemory: null,
        userAgentSpecificMemory: null,
        userAgentSpecificMemoryError: null,
      }
    : await getBrowserMemorySnapshot();
  const totalPaneCount = scopedSessions.reduce(
    (sum, session) => sum + session.workspace.agents.length,
    0,
  );
  return {
    capturedAt: new Date().toISOString(),
    location: window.location.href,
    window: {
      innerWidth: window.innerWidth,
      innerHeight: window.innerHeight,
      devicePixelRatio: window.devicePixelRatio,
    },
    document: {
      totalElements: document.querySelectorAll('*').length,
      terminalSurfaceCount: document.querySelectorAll('.ghostty-terminal').length,
      terminalContainerCount: document.querySelectorAll('.terminal-container').length,
      diffViewCount: document.querySelectorAll('.diff-view').length,
    },
    sessions: {
      count: scopedSessions.length,
      activeSessionId: scopedSessionIds.size === 0 || scopedSessionIds.has(activeSessionId || '')
        ? activeSessionId
        : null,
      totalPaneCount,
      items: scopedSessions.map((session) => ({
        id: session.id,
        label: session.label,
        state: session.state,
        activePaneId: getActivePaneIdForSession(session),
        sessionPaneCount: session.workspace.agents.length,
      })),
    },
    browserMemory,
    terminals,
    pty: ptySnapshot,
    ptyFocus: summarizePtyRecentTraffic(
      ptySnapshot.recentEvents,
      scopedRuntimeIds.size > 0 ? scopedRuntimeIds : null,
    ),
  };
}

function buildBenchmarkBytes(chunkBytes: number, payload: 'scroll' | 'progress'): Uint8Array {
  const safeChunkBytes = Math.max(64, Math.floor(chunkBytes));
  let output = '';
  let lineNumber = 0;
  while (output.length < safeChunkBytes) {
    output += payload === 'progress'
      ? `\r\x1b[2Kp ${String(lineNumber).padStart(6, '0')} xx`
      : `bench ${String(lineNumber).padStart(6, '0')} ${'x'.repeat(112)}\r\n`;
    lineNumber += 1;
  }
  return new TextEncoder().encode(output.slice(0, safeChunkBytes));
}

function buildBenchmarkProgressSeed(cols: number, rows: number): Uint8Array {
  const width = Math.max(1, cols - 1);
  let output = '';
  for (let row = 0; row < rows; row += 1) {
    const prefix = `${String(row).padStart(3, '0')} `;
    output += `\x1b[${row + 1};1H${(prefix + '.'.repeat(width)).slice(0, width)}`;
  }
  output += '\x1b[1;1H';
  return new TextEncoder().encode(output);
}

function encodeBytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  const chunkSize = 0x8000;
  for (let index = 0; index < bytes.length; index += chunkSize) {
    const slice = bytes.subarray(index, index + chunkSize);
    binary += String.fromCharCode(...slice);
  }
  return btoa(binary);
}

function decodeBase64ToBytes(payload: string): Uint8Array {
  const startedAt = performance.now();
  const binaryStr = atob(payload);
  const bytes = Uint8Array.from(binaryStr, (char) => char.charCodeAt(0));
  recordPtyDecode(bytes.length, performance.now() - startedAt);
  return bytes;
}

function concatByteChunks(chunks: Uint8Array[]): Uint8Array {
  const totalBytes = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
  const combined = new Uint8Array(totalBytes);
  let offset = 0;
  for (const chunk of chunks) {
    combined.set(chunk, offset);
    offset += chunk.length;
  }
  return combined;
}

export function useUiAutomationBridge({
  sessions,
  activeSessionId,
  daemonReady = true,
  connectionError = null,
  getActivePaneIdForSession,
  createSession,
  selectSession,
  selectWorkspace,
  moveWorkspaceLeafToWorkspace,
  closeSession,
  reloadSession,
  setSetting,
  openDockPanel,
  openShortcutEditor,
  splitPane,
  closePane,
  focusPane,
  typeInSessionPaneViaUI,
  isSessionPaneInputFocused,
  scrollSessionPaneToTop,
  getPaneText,
  getPaneSize,
  getPaneVisibleContent,
  getPaneVisibleStyleSummary,
  getPaneBlockState,
  getPanePlacementState,
  fitSessionActivePane,
  sendRuntimeInput,
  isRuntimeAttached,
  openAutomationsPanel,
  presentationNotices,
  resetSessionPaneTerminal,
  injectSessionPaneBytes,
  injectSessionPaneBase64,
  drainSessionPaneTerminal,
}: UseUiAutomationBridgeArgs) {
  const handleAutomationRequest = useCallback(async (request: AutomationRequest) => {
    const payload = request.payload || {};

    switch (request.action) {
      case 'ping':
        return { pong: true };
      case 'get_state':
        return {
          activeSessionId,
          daemonReady,
          connectionError,
          appBuild: APP_BUILD_IDENTITY,
          gridActive: typeof document !== 'undefined' && document.querySelector('.grid-view') != null,
          sessions: sessions.map((session) => serializeSession(session, getActivePaneIdForSession)),
        };
      case 'dismiss_whats_new': {
        // A fresh profile's one-time What's New modal sits above the workspace and swallows native
        // HID clicks. Dismiss it the way a user can: a backdrop click (persists "seen").
        const overlay = document.querySelector('.whats-new-overlay');
        if (overlay instanceof HTMLElement) {
          clickElement(overlay);
          await settleUi();
          return { dismissed: true };
        }
        return { dismissed: false };
      }
      case 'markdown_get_annotations_state': {
        return (
          getMarkdownAnnotationsAutomationHandle()?.getState() ??
          INACTIVE_MARKDOWN_ANNOTATIONS_STATE
        );
      }
      case 'grid_get_state':
        return getGridAutomationHandle()?.getState() ?? INACTIVE_GRID_STATE;
      case 'grid_get_tile_text': {
        const handle = getGridAutomationHandle();
        if (!handle) throw new Error('grid is not active');
        const runtimeId = typeof payload.runtimeId === 'string' ? payload.runtimeId : null;
        if (!runtimeId) {
          throw new Error('grid_get_tile_text requires runtimeId');
        }
        return { runtimeId, text: handle.getTileText(runtimeId) };
      }
      case 'grid_zoom': {
        const handle = getGridAutomationHandle();
        if (!handle) throw new Error('grid is not active');
        const runtimeId = typeof payload.runtimeId === 'string' ? payload.runtimeId : null;
        handle.zoom(runtimeId);
        return { requested: runtimeId, zoomedId: handle.getState().zoomedId };
      }
      case 'grid_send_text': {
        const handle = getGridAutomationHandle();
        if (!handle) throw new Error('grid is not active');
        const text = typeof payload.text === 'string' ? payload.text : null;
        if (text === null) throw new Error('grid_send_text requires text');
        const sent = handle.sendText(text);
        await settleUi();
        return { sent, zoomedId: handle.getState().zoomedId };
      }
      case 'settings_get_state':
        return getSettingsAutomationHandle()?.getState() ?? INACTIVE_SETTINGS_STATE;
      case 'settings_select_section': {
        const sectionId = typeof payload.sectionId === 'string' ? payload.sectionId : null;
        if (!sectionId) throw new Error('settings_select_section requires sectionId');
        const handle = getSettingsAutomationHandle();
        if (!handle || !handle.getState().open) throw new Error('settings modal is not open');
        handle.selectSection(sectionId);
        await settleUi(2);
        // Re-read through the module getter: SettingsModal re-registers a fresh handle on each
        // render, so a captured handle still closes over the pre-selection section.
        return getSettingsAutomationHandle()?.getState() ?? INACTIVE_SETTINGS_STATE;
      }
      case 'capture_screenshot_data':
        return captureDomScreenshotData(
          typeof payload.selector === 'string' ? payload.selector : undefined,
        );
      case 'dom_click': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        if (!selector) throw new Error('dom_click requires selector');
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_click selector not found in DOM: ${selector}`);
        }
        const modifiers = (payload.modifiers ?? {}) as ClickModifiers;
        clickElementWithModifiers(element, modifiers);
        await settleUi(2);
        return { clicked: true, bounds: rectSnapshot(element) };
      }
      case 'dom_focus': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        if (!selector) throw new Error('dom_focus requires selector');
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_focus selector not found in DOM: ${selector}`);
        }
        element.focus();
        await settleUi(2);
        if (document.activeElement !== element) {
          throw new Error(`dom_focus target did not take focus: ${selector}`);
        }
        return { focused: true, tag: element.tagName };
      }
      case 'dom_terminal_key': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        const key = typeof payload.key === 'string' ? payload.key : null;
        const code = typeof payload.code === 'string' ? payload.code : null;
        if (!selector || !key || !code) {
          throw new Error('dom_terminal_key requires selector, key, and code');
        }
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_terminal_key selector not found in DOM: ${selector}`);
        }
        const modifiers = (payload.modifiers ?? {}) as ClickModifiers;
        const init: KeyboardEventInit = {
          bubbles: true,
          cancelable: true,
          key,
          code,
          location: typeof payload.location === 'number' ? payload.location : 0,
          metaKey: modifiers.meta ?? false,
          ctrlKey: modifiers.ctrl ?? false,
          shiftKey: modifiers.shift ?? false,
          altKey: modifiers.alt ?? false,
        };
        element.focus();
        element.dispatchEvent(new KeyboardEvent('keydown', init));
        if (payload.repeat === true) {
          element.dispatchEvent(new KeyboardEvent('keydown', { ...init, repeat: true }));
        }
        element.dispatchEvent(new KeyboardEvent('keyup', init));
        await settleUi(2);
        return { dispatched: true, repeat: payload.repeat === true };
      }
      case 'dom_terminal_paste': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        const text = typeof payload.text === 'string' ? payload.text : '';
        const image = payload.image === true;
        if (!selector || (!text && !image)) {
          throw new Error('dom_terminal_paste requires selector and text or image');
        }
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_terminal_paste selector not found in DOM: ${selector}`);
        }
        const event = new ClipboardEvent('paste', { bubbles: true, cancelable: true });
        Object.defineProperty(event, 'clipboardData', {
          value: {
            getData: (type: string) => type === 'text/plain' ? text : '',
            items: image ? [{ kind: 'file', type: 'image/png' }] : [],
          },
        });
        element.focus();
        element.dispatchEvent(event);
        await settleUi(2);
        return { dispatched: true, defaultPrevented: event.defaultPrevented };
      }
      case 'dom_compose_text': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        const text = typeof payload.text === 'string' ? payload.text : null;
        if (!selector || text === null) {
          throw new Error('dom_compose_text requires selector and text');
        }
        const phase = payload.phase ?? 'complete';
        if (phase !== 'complete' && phase !== 'start' && phase !== 'end') {
          throw new Error('dom_compose_text phase must be complete, start, or end');
        }
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_compose_text selector not found in DOM: ${selector}`);
        }
        element.focus();
        if (phase !== 'end') {
          element.dispatchEvent(new CompositionEvent('compositionstart', {
            bubbles: true, cancelable: true, data: '',
          }));
          element.dispatchEvent(new CompositionEvent('compositionupdate', {
            bubbles: true, cancelable: true, data: text,
          }));
        }
        if (phase !== 'start') {
          element.dispatchEvent(new CompositionEvent('compositionend', {
            bubbles: true, cancelable: true, data: text,
          }));
        }
        await settleUi(2);
        return { composed: true, text };
      }
      case 'dom_text': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        if (!selector) throw new Error('dom_text requires selector');
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_text selector not found in DOM: ${selector}`);
        }
        return { text: (element.textContent ?? '').replace(/\s+/g, ' ').trim() };
      }
      case 'dom_scroll_into_view': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        if (!selector) throw new Error('dom_scroll_into_view requires selector');
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_scroll_into_view selector not found in DOM: ${selector}`);
        }
        element.scrollIntoView({ block: 'center', behavior: 'auto' });
        await settleUi(2);
        return { scrolled: true, bounds: rectSnapshot(element) };
      }
      case 'dom_key': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        const key = typeof payload.key === 'string' ? payload.key : null;
        if (!selector) throw new Error('dom_key requires selector');
        if (!key) throw new Error('dom_key requires key');
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_key selector not found in DOM: ${selector}`);
        }
        const modifiers = (payload.modifiers ?? {}) as ClickModifiers;
        element.focus();
        const init: KeyboardEventInit = {
          key,
          bubbles: true,
          cancelable: true,
          metaKey: modifiers.meta ?? false,
          ctrlKey: modifiers.ctrl ?? false,
          shiftKey: modifiers.shift ?? false,
          altKey: modifiers.alt ?? false,
        };
        const delivered = element.dispatchEvent(new KeyboardEvent('keydown', init));
        element.dispatchEvent(new KeyboardEvent('keyup', init));
        await settleUi(3);
        return { key, handled: !delivered };
      }
      case 'dom_hover': {
        // Both the pointer and mouse families are dispatched (handlers here come from either), and
        // enter/leave do not bubble, so the selector must name the element that actually listens.
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        if (!selector) throw new Error('dom_hover requires selector');
        const leave = payload.leave === true;
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`dom_hover selector not found in DOM: ${selector}`);
        }
        const rect = element.getBoundingClientRect();
        const init: PointerEventInit = {
          bubbles: false,
          cancelable: true,
          composed: true,
          pointerId: 1,
          pointerType: 'mouse',
          clientX: rect.left + rect.width / 2,
          clientY: rect.top + rect.height / 2,
        };
        if (leave) {
          element.dispatchEvent(new PointerEvent('pointerleave', init));
          element.dispatchEvent(new MouseEvent('mouseleave', init));
          element.dispatchEvent(new PointerEvent('pointerout', { ...init, bubbles: true }));
        } else {
          element.dispatchEvent(new PointerEvent('pointerover', { ...init, bubbles: true }));
          element.dispatchEvent(new PointerEvent('pointerenter', init));
          element.dispatchEvent(new MouseEvent('mouseenter', init));
        }
        await settleUi(2);
        return { hovered: !leave, bounds: rectSnapshot(element) };
      }
      case 'drag_dom': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        const dx = typeof payload.dx === 'number' ? payload.dx : 0;
        const dy = typeof payload.dy === 'number' ? payload.dy : 0;
        if (!selector) throw new Error('drag_dom requires selector');
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLElement)) {
          throw new Error(`drag_dom selector not found in DOM: ${selector}`);
        }
        const rect = element.getBoundingClientRect();
        const from = { clientX: rect.left + rect.width / 2, clientY: rect.top + rect.height / 2 };
        element.dispatchEvent(new MouseEvent('mousedown', {
          bubbles: true, cancelable: true, view: window, button: 0, buttons: 1, ...from,
        }));
        // Two moves: a drag that arms on the first and applies on later ones would otherwise look like it worked.
        for (const step of [0.5, 1]) {
          window.dispatchEvent(new MouseEvent('mousemove', {
            bubbles: true, cancelable: true, view: window, buttons: 1,
            clientX: from.clientX + dx * step,
            clientY: from.clientY + dy * step,
          }));
          await settleUi(1);
        }
        window.dispatchEvent(new MouseEvent('mouseup', {
          bubbles: true, cancelable: true, view: window, button: 0,
          clientX: from.clientX + dx,
          clientY: from.clientY + dy,
        }));
        await settleUi(2);
        return { dragged: selector, from, dx, dy, bounds: rectSnapshot(element) };
      }
      case 'dom_type': {
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        const text = typeof payload.text === 'string' ? payload.text : null;
        if (!selector) throw new Error('dom_type requires selector');
        if (text === null) throw new Error('dom_type requires text');
        const element = document.querySelector(selector);
        if (!element) {
          throw new Error(`dom_type selector not found in DOM: ${selector}`);
        }
        if (element instanceof HTMLInputElement) {
          setInputValue(element, text);
        } else if (element instanceof HTMLTextAreaElement) {
          setControlValue(element, text);
        } else {
          throw new Error(`dom_type target is not an input or textarea: ${selector}`);
        }
        await settleUi(2);
        return { typed: text };
      }
      case 'dom_select': {
        // Selects need their own verb: dom_type goes through the input value setter, which a <select> ignores.
        const selector = typeof payload.selector === 'string' ? payload.selector : null;
        const value = typeof payload.value === 'string' ? payload.value : null;
        if (!selector) throw new Error('dom_select requires selector');
        if (value === null) throw new Error('dom_select requires value');
        const element = document.querySelector(selector);
        if (!(element instanceof HTMLSelectElement)) {
          throw new Error(`dom_select target is not a select: ${selector}`);
        }
        const offered = Array.from(element.options).map((option) => option.value);
        if (!offered.includes(value)) {
          throw new Error(`dom_select value ${value} is not offered by ${selector}; options: ${offered.join(', ')}`);
        }
        setControlValue(element, value);
        await settleUi(2);
        return { selected: value };
      }
      case 'get_window_bounds': {
        if (!isTauri()) {
          return null;
        }
        const appWindow = getCurrentWindow();
        const [scaleFactor, outerPosition, outerSize, minimized] = await Promise.all([
          appWindow.scaleFactor(),
          appWindow.outerPosition(),
          appWindow.outerSize(),
          appWindow.isMinimized(),
        ]);
        const logicalPosition = outerPosition.toLogical(scaleFactor);
        const logicalSize = outerSize.toLogical(scaleFactor);
        return {
          scaleFactor,
          minimized,
          logicalBounds: {
            x: logicalPosition.x,
            y: logicalPosition.y,
            width: logicalSize.width,
            height: logicalSize.height,
          },
        };
      }
      case 'list_sessions':
        return {
          activeSessionId,
          sessions: sessions.map((session) => summarizeSession(session, getActivePaneIdForSession)),
        };
      case 'find_session': {
        const cwd = typeof payload.cwd === 'string' ? payload.cwd : '';
        const label = typeof payload.label === 'string' ? payload.label : '';
        const session = sessions.find((entry) => {
          if (cwd && entry.cwd !== cwd) return false;
          if (label && entry.label !== label) return false;
          return true;
        });
        return session ? serializeSession(session, getActivePaneIdForSession) : null;
      }
      case 'home_get_state': {
        const visible = document.querySelector('.view-container.visible');
        const banner = visible?.querySelector('[data-testid="all-settled"]') ?? null;
        const follow = visible?.querySelector('[data-testid="follow-next-turn"] input');
        const snoozedGroup = visible?.querySelector('[data-testid="session-group-snoozed"]') ?? null;
        const snoozedHeader = snoozedGroup?.querySelector('[data-testid="session-group-snoozed-header"]');
        const readPRSection = (testId: string) => {
          const section = visible?.querySelector(`[data-testid="${testId}"]`) ?? null;
          if (!section) return null;
          return {
            count: section.querySelector('.pr-section-count')?.textContent?.trim() || '',
            rows: Array.from(section.querySelectorAll('[data-testid="pr-card"]')).map((row) => ({
              number: row.querySelector('.pr-number')?.textContent?.trim() || '',
              repo: row.querySelector('.pr-repo-inline')?.textContent?.trim() || '',
              title: row.querySelector('.pr-title')?.textContent?.trim() || '',
            })),
            repoGroups: Array.from(section.querySelectorAll('.pr-repo-group .repo-name'))
              .map((name) => name.textContent?.trim() || ''),
          };
        };
        return {
          onScreen: Boolean(visible?.querySelector('.dashboard')),
          allSettled: Boolean(banner),
          detail: banner?.querySelector('.all-settled-detail')?.textContent?.trim() || '',
          followNextTurn: follow instanceof HTMLInputElement ? follow.checked : null,
          shortcutFooter: Boolean(visible?.querySelector('.dashboard-footer')),
          snoozed: {
            present: Boolean(snoozedGroup),
            header: snoozedHeader?.textContent?.trim() || '',
            expanded: snoozedHeader?.getAttribute('aria-expanded') === 'true',
            rows: Array.from(snoozedGroup?.querySelectorAll('.session-row') || []).map((row) => ({
              id: (row.getAttribute('data-testid') || '').slice('session-'.length),
              label: row.querySelector('.session-name')?.textContent?.trim() || '',
              state: row.getAttribute('data-state') || '',
              wake: row.querySelector('.session-wake-at')?.textContent?.trim() || '',
              canWake: Boolean(row.querySelector('.session-wake-btn')),
            })),
          },
          // Read from the groups that mark themselves, not from the testid prefix: the turn band
          // and the snoozed section share it and are not state groups.
          stateGroupSessionIds: Array.from(
            visible?.querySelectorAll('[data-session-group="state"] .session-row') || [],
          ).map((row) => (row.getAttribute('data-testid') || '').slice('session-'.length)),
          prs: {
            yours: readPRSection('pr-section-yours'),
            review: readPRSection('pr-section-review'),
          },
        };
      }
      case 'queue_get_state': {
        const band = document.querySelector('[data-testid="sidebar-queue"]');
        const readRow = (row: Element, prefix: string) => {
          const open = row.querySelector('.queue-row-select');
          return {
            id: (row.getAttribute('data-testid') || '').slice(prefix.length),
            label: row.querySelector('.session-label')?.textContent?.trim() || '',
            state: row.getAttribute('data-state') || '',
            workspaceId: row.getAttribute('data-workspace-id') || '',
            age: row.querySelector('.queue-row-age')?.textContent?.trim() || '',
            wake: row.querySelector('.queue-row-wake-at')?.textContent?.trim() || '',
            selected: row.classList.contains('selected'),
            open: open
              ? {
                tag: open.tagName,
                label: open.getAttribute('aria-label') || '',
                focused: document.activeElement === open,
              }
              : null,
          };
        };
        const chiefRow = band?.querySelector('[data-testid^="queue-chief-"]');
        const snoozedSection = document.querySelector('[data-testid="sidebar-snoozed"]');
        const snoozedHeader = snoozedSection?.querySelector('[data-testid="snoozed-section-header"]');
        const automationGroups = Array.from(document.querySelectorAll('[data-automation-id]'));
        return {
          present: Boolean(band),
          empty: Boolean(band?.querySelector('[data-testid="queue-empty"]')),
          chief: chiefRow ? readRow(chiefRow, 'queue-chief-') : null,
          turns: Array.from(band?.querySelectorAll('[data-testid^="queue-turn-"]') || [])
            .map((row) => readRow(row, 'queue-turn-')),
          settled: Array.from(band?.querySelectorAll('[data-testid^="queue-settled-"]') || [])
            .map((row) => readRow(row, 'queue-settled-')),
          pinned: Array.from(band?.querySelectorAll('[data-testid^="queue-pinned-"]') || [])
            .map((row) => readRow(row, 'queue-pinned-')),
          crew: Array.from(band?.querySelectorAll('.queue-row--crew[data-crew-member]') || [])
            .map((row) => ({
              member: row.getAttribute('data-crew-member') || '',
              state: row.getAttribute('data-crew-state') || '',
            })),
          snoozed: {
            present: Boolean(snoozedSection),
            header: snoozedHeader?.textContent?.trim() || '',
            expanded: snoozedHeader?.getAttribute('aria-expanded') === 'true',
            rows: Array.from(snoozedSection?.querySelectorAll('[data-testid^="queue-snoozed-"]') || [])
              .map((row) => readRow(row, 'queue-snoozed-')),
          },
          automations: automationGroups.map((group) => ({
            id: group.getAttribute('data-automation-id') || '',
            header: group.querySelector('.automation-session-header')?.textContent?.trim() || '',
            expanded: group.querySelector('.automation-session-header')?.getAttribute('aria-expanded') === 'true',
            sessionIds: Array.from(group.querySelectorAll('[data-testid^="sidebar-session-"]'))
              .map((row) => (row.getAttribute('data-testid') || '').slice('sidebar-session-'.length)),
          })),
          treeSessionIds: Array.from(document.querySelectorAll('.session-list [data-testid^="sidebar-session-"]'))
            .map((row) => (row.getAttribute('data-testid') || '').slice('sidebar-session-'.length)),
          treeWorkspaceIds: Array.from(document.querySelectorAll('.session-list [data-testid^="sidebar-workspace-"]'))
            .map((group) => (group.getAttribute('data-testid') || '').slice('sidebar-workspace-'.length)),
        };
      }
      case 'chief_of_staff_get_state':
        return {
          sessions: sessions.map((session) => {
            const row = document.querySelector(`[data-testid="sidebar-session-${session.id}"]`);
            const chiefOfStaff = Boolean(row?.querySelector('.chief-of-staff-badge'));
            return {
              id: session.id,
              label: session.label,
              chiefOfStaff,
              sidebarBadge: chiefOfStaff,
            };
          }),
          actionsOpen: Boolean(document.querySelector('.session-actions-popover')),
          transferPrompt: document.querySelector('[data-testid="chief-transfer-prompt"]')?.textContent?.trim() || null,
        };
      case 'chief_of_staff_open_actions': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('chief_of_staff_open_actions requires sessionId');
        }
        const button = document.querySelector(`[data-testid="session-actions-${sessionId}"]`);
        if (!(button instanceof HTMLElement)) {
          throw new Error(`Session actions not found for ${sessionId}`);
        }
        clickElement(button);
        await settleUi(2);
        return { sessionId };
      }
      case 'chief_of_staff_toggle': {
        const button = document.querySelector('[data-testid="chief-of-staff-session-action"]');
        if (!(button instanceof HTMLElement)) {
          throw new Error('Chief of staff action is not open');
        }
        clickElement(button);
        await settleUi(2);
        return { requested: true };
      }
      case 'chief_of_staff_confirm_transfer': {
        const button = document.querySelector('[data-testid="chief-transfer-confirm"]');
        if (!(button instanceof HTMLElement)) {
          throw new Error('Chief of staff transfer prompt is not open');
        }
        clickElement(button);
        await settleUi(2);
        return { requested: true };
      }
      case 'chief_of_staff_cancel_transfer': {
        const button = document.querySelector('[data-testid="chief-transfer-cancel"]');
        if (!(button instanceof HTMLElement)) {
          throw new Error('Chief of staff transfer prompt is not open');
        }
        clickElement(button);
        await settleUi(2);
        return { requested: true };
      }
      case 'create_session': {
        const cwd = typeof payload.cwd === 'string' ? payload.cwd : '';
        const label = typeof payload.label === 'string' && payload.label.length > 0
          ? payload.label
          : (cwd.split('/').pop() || 'session');
        const agent = typeof payload.agent === 'string' ? payload.agent : undefined;
        const providedSessionId = typeof payload.sessionId === 'string' && payload.sessionId.length > 0
          ? payload.sessionId
          : undefined;
        const endpointId = typeof payload.endpoint_id === 'string' && payload.endpoint_id.length > 0
          ? payload.endpoint_id
          : undefined;
        const chiefOfStaff = payload.chief_of_staff === true;
        const resumeConversationFile = typeof payload.resume_conversation_file === 'string'
          && payload.resume_conversation_file.length > 0
          ? payload.resume_conversation_file
          : undefined;
        if (!cwd) {
          throw new Error('create_session requires cwd');
        }
        const sessionId = await createSession(label, cwd, providedSessionId, agent, endpointId, undefined, {
          chiefOfStaff,
          resumeConversationFile,
        });
        await settleUi();
        window.setTimeout(() => {
          fitSessionActivePane(sessionId);
        }, 50);
        return { sessionId };
      }
      case 'close_session': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('close_session requires sessionId');
        }
        await closeSession(sessionId);
        await settleUi();
        return { sessionId };
      }
      case 'set_setting': {
        if (!setSetting) {
          throw new Error('set_setting is not configured');
        }
        const key = typeof payload.key === 'string' ? payload.key : '';
        const value = typeof payload.value === 'string' ? payload.value : '';
        if (!key) {
          throw new Error('set_setting requires key');
        }
        setSetting(key, value);
        await settleUi();
        return { key, value };
      }
      case 'set_warm_workspace_limit': {
        const setter = (window as Window & { attnSetWarmWorkspaces?: (n: number) => number }).attnSetWarmWorkspaces;
        if (!setter) {
          throw new Error('attnSetWarmWorkspaces is not available');
        }
        const requested = payload.limit;
        if (typeof requested !== 'number' || !Number.isFinite(requested)) {
          throw new Error('set_warm_workspace_limit requires a numeric limit');
        }
        const limit = setter(requested);
        await settleUi();
        const virtualizedPanes = document.querySelectorAll('[data-testid^="pane-virtualized-"]').length;
        return { limit, virtualizedPanes };
      }
      case 'get_warm_workspace_limit': {
        const limit = readWarmWorkspaceLimit();
        const virtualizedPanes = document.querySelectorAll('[data-testid^="pane-virtualized-"]').length;
        return { limit, virtualizedPanes };
      }
      case 'dump_terminal_geometry': {
        const snapshots = dumpTerminalGeometry();
        return { snapshots };
      }
      case 'lose_webgl_context': {
        const canvas = findActivePaneCanvas();
        if (!canvas) {
          throw new Error('No active pane canvas found');
        }
        const extension = canvas.getContext('webgl2')?.getExtension('WEBGL_lose_context');
        if (!extension) {
          throw new Error('WEBGL_lose_context extension is unavailable on the active pane canvas');
        }
        extension.loseContext();
        return { ok: true };
      }
      case 'reload_session': {
        if (!reloadSession) {
          throw new Error('reload_session is not configured');
        }
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('reload_session requires sessionId');
        }
        const session = sessions.find((entry) => entry.id === sessionId);
        const paneId = session?.workspace.agents.find((agent) => agent.sessionId === sessionId)?.id;
        const size = paneId ? getPaneSize(sessionId, paneId) || undefined : undefined;
        await reloadSession(sessionId, size);
        await settleUi();
        return { sessionId };
      }
      case 'select_session': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('select_session requires sessionId');
        }
        selectSession(sessionId);
        await settleUi();
        return { sessionId };
      }
      case 'open_dock_panel': {
        const panelId = typeof payload.panelId === 'string' ? payload.panelId : '';
        if (!panelId) {
          throw new Error('open_dock_panel requires panelId');
        }
        if (!openDockPanel) {
          throw new Error('open_dock_panel is not available');
        }
        openDockPanel(panelId);
        await settleUi();
        return { panelId };
      }
      case 'get_workspace': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : activeSessionId;
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        return serializeSession(session, getActivePaneIdForSession);
      }
      case 'get_session_ui_state': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('get_session_ui_state requires sessionId');
        }
        return collectSessionUiState(
          sessions,
          activeSessionId,
          sessionId,
          getActivePaneIdForSession,
        );
      }
      case 'session_seed_chip_get_state': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('session_seed_chip_get_state requires sessionId');
        }
        const chip = document.querySelector(`[data-testid="seed-chip-${sessionId}"]`);
        if (!(chip instanceof HTMLElement)) {
          return { present: false };
        }
        return {
          present: true,
          title: chip.querySelector('.pane-seed-chip-title')?.textContent?.trim() ?? '',
          id: chip.getAttribute('data-seed-id') ?? '',
          hint: chip.getAttribute('aria-label') ?? '',
          status: chip.getAttribute('data-status') ?? '',
          animationsRunning: chip.getAnimations({ subtree: true }).filter((animation) => animation.playState === 'running').length,
          unread: Boolean(chip.querySelector(`[data-testid="seed-chip-unread-${sessionId}"]`)),
        };
      }
      case 'select_workspace': {
        const workspaceId = typeof payload.workspaceId === 'string' ? payload.workspaceId : '';
        if (!workspaceId) {
          throw new Error('select_workspace requires workspaceId');
        }
        selectWorkspace(workspaceId);
        await settleUi();
        return { workspaceId };
      }
      case 'app_view_get_state': {
        const scope = typeof payload.workspaceId === 'string' && payload.workspaceId
          ? document.querySelector(`[data-session-terminal-workspace="${payload.workspaceId}"]`)
          : document;
        const hosts = Array.from(scope?.querySelectorAll('[data-app-view-host]') ?? []);
        return {
          hosts: hosts.map((host) => ({
            view: host.getAttribute('data-app-view-host') || '',
            tileId: host.getAttribute('data-app-view-tile') || '',
            stale: host.getAttribute('data-app-view-stale') === '1',
            badge: host.querySelector('.app-tile-host-badge')?.textContent?.trim() || '',
            placeholder: host.querySelector('[data-app-view-placeholder]')
              ?.getAttribute('data-app-view-placeholder') || '',
            text: host.textContent?.trim() || '',
          })),
        };
      }
      case 'get_workspace_ui_state': {
        const workspaceId = typeof payload.workspaceId === 'string' ? payload.workspaceId : '';
        if (!workspaceId) {
          throw new Error('get_workspace_ui_state requires workspaceId');
        }
        const surface = document.querySelector(`[data-session-terminal-workspace="${workspaceId}"]`);
        const wrapper = surface?.closest('.terminal-wrapper') ?? null;
        const tileIds = surface
          ? Array.from(surface.querySelectorAll('[data-pane-kind="tile"]'))
            .map((node) => node.getAttribute('data-pane-id') || '')
            .filter(Boolean)
          : [];
        const paneCount = surface
          ? surface.querySelectorAll('[data-pane-kind="agent"]').length
          : 0;
        const tileTitles = surface
          ? Array.from(surface.querySelectorAll('.workspace-dock-tile-title'))
            .map((node) => node.textContent?.trim() || '')
          : [];
        const activeBody = document.activeElement;
        const tileBodyFocused = Boolean(
          surface
            && activeBody instanceof HTMLElement
            && activeBody.classList.contains('workspace-dock-tile-body')
            && surface.contains(activeBody),
        );
        return {
          workspaceId,
          rendered: Boolean(surface),
          active: Boolean(wrapper?.classList.contains('active')),
          sessionVisible: surface?.getAttribute('data-session-visible') === '1',
          tileIds,
          paneCount,
          tileTitles,
          tileBodyFocused,
        };
      }
      case 'get_browser_focus_state':
        return {
          label: await invoke<string | null>('browser_host_focus_state'),
        };
      case 'sessions_get_state':
        return collectSessionsPanelUiState();
      case 'sessions_set_filter': {
        const root = sessionsPanelRoot();
        const { scope, range, workspace, repository, from, to } = payload as {
          scope?: string; range?: string; workspace?: string; repository?: string; from?: string; to?: string;
        };
        if (scope) {
          const button = Array.from(root.querySelectorAll('.sessions-scope button'))
            .find((entry) => entry.textContent?.trim() === scope);
          if (!(button instanceof HTMLElement)) throw new Error(`no ${scope} scope button`);
          clickElement(button);
          await settleUi(2);
        }
        const setSelect = (label: string, value: string) => {
          const select = Array.from(root.querySelectorAll('label.sessions-filter'))
            .find((entry) => entry.querySelector('span')?.textContent?.trim() === label)
            ?.querySelector('select');
          if (!(select instanceof HTMLSelectElement)) throw new Error(`no ${label} filter`);
          setControlValue(select, value);
        };
        if (range !== undefined) setSelect('When', range);
        if (workspace !== undefined) setSelect('Workspace', workspace);
        if (repository !== undefined) setSelect('Repository', repository);
        for (const [label, value] of [['From', from], ['To', to]] as const) {
          if (value === undefined) continue;
          const input = Array.from(root.querySelectorAll('.sessions-custom-range label'))
            .find((entry) => entry.querySelector('span')?.textContent?.trim() === label)
            ?.querySelector('input');
          if (!(input instanceof HTMLInputElement)) throw new Error(`no ${label} date input`);
          setControlValue(input, value);
        }
        await settleUi(3);
        return collectSessionsPanelUiState();
      }
      case 'sessions_load_more': {
        const button = Array.from(sessionsPanelRoot().querySelectorAll('.sessions-footer button'))
          .find((entry) => (entry.textContent || '').startsWith('Load'));
        if (!(button instanceof HTMLElement)) throw new Error('nothing older to load');
        clickElement(button);
        await settleUi(4);
        return collectSessionsPanelUiState();
      }
      case 'sessions_row_action': {
        const { sessionId, action } = payload as { sessionId: string; action: string };
        const row = Array.from(sessionsPanelRoot().querySelectorAll('tbody tr'))
          .find((entry) => entry.querySelector('.sessions-id')?.textContent?.trim() === sessionId);
        if (!row) throw new Error(`no row for session ${sessionId}`);
        const button = Array.from(row.querySelectorAll('.sessions-actions button'))
          .find((entry) => entry.textContent?.trim() === action);
        if (!(button instanceof HTMLElement)) throw new Error(`row ${sessionId} offers no ${action}`);
        clickElement(button);
        await settleUi(3);
        return collectSessionsPanelUiState();
      }
      case 'markdown_opener_get_state':
        return collectMarkdownOpenerUiState();
      case 'location_picker_get_state':
        return collectLocationPickerUiState();
      case 'location_picker_open': {
        if (!getLocationPickerRoot()) {
          const button = document.querySelector('[aria-label="New Session"]');
          if (!(button instanceof HTMLElement)) {
            throw new Error('New Session button not found');
          }
          clickElement(button);
        }
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_close': {
        const overlay = getLocationPickerOverlay();
        if (overlay) {
          overlay.dispatchEvent(new MouseEvent('click', {
            bubbles: true,
            cancelable: true,
            view: window,
          }));
          await settleUi(2);
        }
        return collectLocationPickerUiState();
      }
      case 'location_picker_set_target': {
        const root = getLocationPickerRoot();
        if (!root) {
          throw new Error('Location picker is not open');
        }
        const endpointId = typeof payload.endpointId === 'string' ? payload.endpointId : '';
        const endpointName = typeof payload.endpointName === 'string' ? payload.endpointName : '';
        const local = payload.local === true;
        const buttons = Array.from(root.querySelectorAll('.picker-endpoint-controls button'))
          .filter((button): button is HTMLButtonElement => button instanceof HTMLButtonElement);
        const button = buttons.find((candidate) => {
          if (local) {
            return candidate.getAttribute('data-testid') === 'location-picker-target-local';
          }
          if (endpointId && candidate.dataset.endpointId === endpointId) {
            return true;
          }
          if (endpointName) {
            return candidate.querySelector('.endpoint-option-name')?.textContent?.trim() === endpointName;
          }
          return false;
        });
        if (!button) {
          throw new Error('Requested location picker target not found');
        }
        clickElement(button);
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_set_path': {
        const input = document.querySelector('[data-testid="location-picker-path-input"]');
        if (!(input instanceof HTMLInputElement)) {
          throw new Error('Location picker path input not found');
        }
        const value = typeof payload.value === 'string' ? payload.value : '';
        input.focus();
        setInputValue(input, value);
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_submit_path': {
        const input = document.querySelector('[data-testid="location-picker-path-input"]');
        if (!(input instanceof HTMLInputElement)) {
          throw new Error('Location picker path input not found');
        }
        input.focus();
        input.dispatchEvent(new KeyboardEvent('keydown', {
          key: 'Enter',
          bubbles: true,
          cancelable: true,
        }));
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_select_path_item': {
        const index = typeof payload.index === 'number' ? payload.index : NaN;
        if (!Number.isFinite(index)) {
          throw new Error('location_picker_select_path_item requires index');
        }
        const item = document.querySelector(`[data-testid="location-picker-item-${Math.floor(index)}"]`);
        if (!(item instanceof HTMLElement)) {
          throw new Error(`Location picker item ${index} not found`);
        }
        clickElement(item);
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_select_repo_option': {
        const index = typeof payload.index === 'number' ? payload.index : NaN;
        if (!Number.isFinite(index)) {
          throw new Error('location_picker_select_repo_option requires index');
        }
        const option = document.querySelector(`[data-testid="repo-option-${Math.floor(index)}"]`);
        if (!(option instanceof HTMLElement)) {
          throw new Error(`Repo option ${index} not found`);
        }
        clickElement(option);
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_set_new_worktree_name': {
        const input = document.querySelector('[data-testid="repo-new-worktree-input"]');
        if (!(input instanceof HTMLInputElement)) {
          throw new Error('New worktree input not found');
        }
        const value = typeof payload.value === 'string' ? payload.value : '';
        input.focus();
        setInputValue(input, value);
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_set_new_worktree_starting_branch': {
        const mode = payload.mode === 'default' ? 'default' : 'current';
        const selector = mode === 'default'
          ? '[data-testid="repo-new-worktree-start-default"]'
          : '[data-testid="repo-new-worktree-start-current"]';
        const radio = document.querySelector(selector);
        if (!(radio instanceof HTMLInputElement)) {
          throw new Error(`New worktree ${mode} radio not found`);
        }
        clickElement(radio);
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'location_picker_submit_new_worktree': {
        const input = document.querySelector('[data-testid="repo-new-worktree-input"]');
        if (!(input instanceof HTMLInputElement)) {
          throw new Error('New worktree input not found');
        }
        input.focus();
        input.dispatchEvent(new KeyboardEvent('keydown', {
          key: 'Enter',
          bubbles: true,
          cancelable: true,
        }));
        await settleUi(2);
        return collectLocationPickerUiState();
      }
      case 'split_pane': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const direction = payload.direction === 'horizontal' ? 'horizontal' : 'vertical';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const targetPaneId = resolvePaneId(session, getActivePaneIdForSession, payload.targetPaneId);
        await splitPane(sessionId, targetPaneId, direction);
        return { sessionId, targetPaneId, direction };
      }
      case 'close_pane': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const paneId = typeof payload.paneId === 'string' ? payload.paneId : '';
        if (!sessionId || !paneId) {
          throw new Error('close_pane requires sessionId and paneId');
        }
        await closePane(sessionId, paneId);
        return { sessionId, paneId };
      }
      case 'focus_pane': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        selectSession(sessionId);
        focusPane(viewSessionId, paneId);
        await settleUi();
        return { sessionId, paneId, viewSessionId };
      }
      case 'click_pane': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const ownerSessionId = resolvePaneOwnerSessionId(session, paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        selectSession(sessionId);
        await settleUi(1);
        clickPaneElement(ownerSessionId, paneId);
        await settleUi(2);
        return { sessionId, paneId, ownerSessionId, viewSessionId };
      }
      case 'scroll_pane_to_top': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        selectSession(sessionId);
        await settleUi(1);
        const success = scrollSessionPaneToTop(viewSessionId, paneId);
        if (!success) {
          throw new Error(`Failed to scroll pane ${paneId} to top`);
        }
        await settleUi(1);
        return { sessionId, paneId, viewSessionId };
      }
      case 'wheel_pane': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const ownerSessionId = resolvePaneOwnerSessionId(session, paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const deltaY = typeof payload.deltaY === 'number' ? payload.deltaY : 0;
        const deltaMode = typeof payload.deltaMode === 'number' ? payload.deltaMode : WheelEvent.DOM_DELTA_PIXEL;
        selectSession(sessionId);
        await settleUi(1);
        wheelPaneElement(ownerSessionId, paneId, deltaY, deltaMode);
        await settleUi(2);
        return { sessionId, paneId, ownerSessionId, viewSessionId, deltaY, deltaMode };
      }
      case 'click_pane_cell': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const size = getPaneSize(viewSessionId, paneId);
        const cell = payload.cell as { col?: unknown; row?: unknown } | undefined;
        if (!size || typeof cell?.col !== 'number' || typeof cell?.row !== 'number') {
          throw new Error('click_pane_cell requires pane size and a numeric cell');
        }
        selectSession(sessionId);
        await settleUi(1);
        clickPaneCell(viewSessionId, paneId, size, { col: cell.col, row: cell.row });
        await settleUi(2);
        return { sessionId, paneId, viewSessionId };
      }
      case 'hover_pane_cell': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const size = getPaneSize(viewSessionId, paneId);
        const cell = payload.cell as { col?: unknown; row?: unknown } | undefined;
        if (!size || typeof cell?.col !== 'number' || typeof cell?.row !== 'number') {
          throw new Error('hover_pane_cell requires pane size and a numeric cell');
        }
        selectSession(sessionId);
        await settleUi(1);
        const hovered = hoverPaneCell(
          viewSessionId,
          paneId,
          size,
          { col: cell.col, row: cell.row },
          payload.meta === true,
          payload.alt === true,
        );
        await settleUi(2);
        return { sessionId, paneId, viewSessionId, cursor: getComputedStyle(hovered).cursor };
      }
      case 'get_pane_cell_rect': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const size = getPaneSize(viewSessionId, paneId);
        const cell = payload.cell as { col?: unknown; row?: unknown } | undefined;
        if (!size || typeof cell?.col !== 'number' || typeof cell?.row !== 'number') {
          throw new Error('get_pane_cell_rect requires pane size and a numeric cell');
        }
        return {
          sessionId,
          paneId,
          viewSessionId,
          ...paneCellRect(viewSessionId, paneId, size, { col: cell.col, row: cell.row }),
        };
      }
      case 'get_terminal_context_menu_state': {
        return terminalContextMenuState();
      }
      case 'get_annotation_state': {
        return annotationSurfaceState();
      }
      case 'drag_pane_selection': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const size = getPaneSize(viewSessionId, paneId);
        const start = payload.start as { col?: unknown; row?: unknown } | undefined;
        const end = payload.end as { col?: unknown; row?: unknown } | undefined;
        if (!size
          || typeof start?.col !== 'number' || typeof start?.row !== 'number'
          || typeof end?.col !== 'number' || typeof end?.row !== 'number') {
          throw new Error('drag_pane_selection requires pane size and numeric start/end cells');
        }
        selectSession(sessionId);
        await settleUi(1);
        const altKey = payload.altKey === true;
        dragPaneSelection(
          viewSessionId,
          paneId,
          size,
          { col: start.col, row: start.row },
          { col: end.col, row: end.row },
          { altKey },
        );
        await settleUi(2);
        return { sessionId, paneId, viewSessionId, start, end, altKey };
      }
      case 'drag_leaf': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const leafId = typeof payload.leafId === 'string' ? payload.leafId : '';
        if (!leafId) {
          throw new Error('drag_leaf requires leafId');
        }
        const dropFracX = typeof payload.dropFracX === 'number' ? payload.dropFracX : 0.5;
        const dropFracY = typeof payload.dropFracY === 'number' ? payload.dropFracY : 0.5;
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        selectSession(sessionId);
        await settleUi(2);
        const points = dragLeafHeader(leafId, dropFracX, dropFracY);
        await settleUi(2);
        return { sessionId, leafId, viewSessionId, dropFracX, dropFracY, ...points };
      }
      case 'drag_split': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const splitId = typeof payload.splitId === 'string' ? payload.splitId : '';
        const deltaPx = typeof payload.deltaPx === 'number' ? payload.deltaPx : 0;
        const steps = typeof payload.steps === 'number' ? payload.steps : 8;
        if (!splitId || !Number.isFinite(deltaPx) || !Number.isFinite(steps)) {
          throw new Error('drag_split requires splitId and numeric deltaPx/steps');
        }
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        selectSession(sessionId);
        await settleUi(2);
        const result = await dragSplitDivider(session.workspaceId, splitId, deltaPx, steps);
        return { sessionId, viewSessionId, workspaceId: session.workspaceId, ...result };
      }
      case 'move_workspace_leaf': {
        const sourceWorkspaceId = typeof payload.sourceWorkspaceId === 'string' ? payload.sourceWorkspaceId : '';
        const targetWorkspaceId = typeof payload.targetWorkspaceId === 'string' ? payload.targetWorkspaceId : '';
        const leafId = typeof payload.leafId === 'string' ? payload.leafId : '';
        if (!sourceWorkspaceId || !targetWorkspaceId || !leafId) {
          throw new Error('move_workspace_leaf requires sourceWorkspaceId, targetWorkspaceId, and leafId');
        }
        const edge = payload.edge === 'right' || payload.edge === 'top' || payload.edge === 'bottom'
          ? payload.edge
          : 'left';
        const anchorId = typeof payload.anchorId === 'string' ? payload.anchorId : '';
        const ratio = typeof payload.ratio === 'number' ? payload.ratio : undefined;
        const result = await moveWorkspaceLeafToWorkspace(sourceWorkspaceId, targetWorkspaceId, leafId, { anchorId, edge, ratio });
        await settleUi(4);
        return result;
      }
      case 'dispatch_shortcut': {
        const shortcutId = typeof payload.shortcutId === 'string' ? payload.shortcutId as ShortcutId : null;
        if (!shortcutId) {
          throw new Error('dispatch_shortcut requires shortcutId');
        }
        dispatchShortcutEvent(shortcutId);
        await settleUi(2);
        return { shortcutId };
      }
      case 'shortcut_binding': {
        const shortcutId = typeof payload.shortcutId === 'string' ? payload.shortcutId as ShortcutId : null;
        if (!shortcutId || !Object.prototype.hasOwnProperty.call(SHORTCUTS, shortcutId)) {
          throw new Error(`shortcut_binding requires a known shortcutId, got ${JSON.stringify(payload.shortcutId)}`);
        }
        return { shortcutId, binding: resolveBinding(shortcutId) };
      }
      case 'open_shortcut_editor': {
        if (!openShortcutEditor) {
          throw new Error('openShortcutEditor handler is not wired');
        }
        openShortcutEditor();
        await settleUi(2);
        return { opened: true };
      }
      case 'write_pane': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const text = typeof payload.text === 'string' ? payload.text : '';
        const submit = payload.submit !== false;
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        if (!text) {
          throw new Error('write_pane requires text');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const runtimeId = resolveRuntimeId(session, paneId);
        sendRuntimeInput(runtimeId, text, 'automation');
        if (submit) {
          sendRuntimeInput(runtimeId, '\r', 'automation');
        }
        return { sessionId, paneId, runtimeId };
      }
      case 'type_pane_via_ui': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const text = typeof payload.text === 'string' ? payload.text : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        if (!text) {
          throw new Error('type_pane_via_ui requires text');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const success = typeInSessionPaneViaUI(viewSessionId, paneId, text);
        if (!success) {
          throw new Error(`Failed to type into pane ${paneId} via UI input`);
        }
        return { sessionId, paneId, viewSessionId };
      }
      case 'read_pane_text': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        return {
          sessionId,
          paneId,
          viewSessionId,
          text: getPaneText(viewSessionId, paneId),
          size: getPaneSize(viewSessionId, paneId),
        };
      }
      case 'read_pane_style': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        return {
          sessionId,
          paneId,
          viewSessionId,
          size: getPaneSize(viewSessionId, paneId),
          style: getPaneVisibleStyleSummary(viewSessionId, paneId),
        };
      }
      case 'get_pane_block_state': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const blockState = getPaneBlockState(viewSessionId, paneId);
        // Stable response shape: available=false ("no live terminal handle") is not "no blocks".
        return {
          sessionId,
          paneId,
          viewSessionId,
          available: blockState !== null,
          ...blockState,
        };
      }
      case 'get_pane_placement_state': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const viewSessionId = resolveWorkspaceViewSessionId(session, sessions, activeSessionId);
        const placementState = getPanePlacementState(viewSessionId, paneId);
        return {
          sessionId,
          paneId,
          viewSessionId,
          available: placementState !== null,
          ...placementState,
        };
      }
      case 'get_pane_state': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const snapshot = collectVisualSnapshot(
          [session],
          activeSessionId,
          getActivePaneIdForSession,
          getPaneText,
          getPaneSize,
          getPaneVisibleContent,
          isRuntimeAttached,
        );
        return {
          sessionId,
          paneId,
          inputFocused: isSessionPaneInputFocused(resolveWorkspaceViewSessionId(session, sessions, activeSessionId), paneId),
          activePaneId: getActivePaneIdForSession(session),
          pane: snapshot.sessions[0]?.panes.find((pane) => pane.paneId === paneId) || null,
          renderHealth: collectRenderHealthSnapshot(
            [session],
            activeSessionId,
            getActivePaneIdForSession,
            getPaneText,
            getPaneSize,
            getPaneVisibleContent,
            isSessionPaneInputFocused,
            isRuntimeAttached,
          ).sessions[0]?.panes.find((pane) => pane.paneId === paneId) || null,
        };
      }
      // SPIKE-ONLY. Replays a recorded envelope stream into a live pane: a conversation session
      // cannot reach the harness's stub provider through pi's static catalog (`getModel`).
      case 'conversation_replay_envelopes': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const rows = Array.isArray(payload.envelopes) ? payload.envelopes : [];
        if (!sessionId || rows.length === 0) {
          throw new Error('conversation_replay_envelopes requires sessionId and a non-empty envelopes array');
        }
        const apply = useConversationsStore.getState().applyEnvelope;
        void (async () => {
          for (const entry of rows as Array<Record<string, unknown>>) {
            const afterMs = typeof entry.afterMs === 'number' ? entry.afterMs : 0;
            if (afterMs > 0) await new Promise((done) => { window.setTimeout(done, afterMs); });
            if (typeof entry.kind !== 'string') continue;
            apply(
              sessionId,
              typeof entry.seq === 'number' ? entry.seq : 0,
              entry.kind,
              (entry.body ?? {}) as Record<string, unknown>,
            );
          }
        })();
        return { replaying: rows.length };
      }
      case 'conversation_scroll_to': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const root = sessionId
          ? document.querySelector(`[data-testid="conversation-pane-${sessionId}"]`)
          : document.querySelector('.conversation-pane');
        const list = root?.querySelector('[data-testid="conversation-messages"]');
        if (!(list instanceof HTMLElement)) throw new Error('conversation transcript not found');
        const fromBottom = typeof payload.fromBottom === 'number' ? payload.fromBottom : null;
        list.scrollTop = fromBottom === null
          ? Number(payload.scrollTop ?? 0)
          : list.scrollHeight - list.clientHeight - fromBottom;
        list.dispatchEvent(new Event('scroll', { bubbles: true }));
        return {
          scrollTop: Math.round(list.scrollTop),
          fromBottom: Math.round(list.scrollHeight - list.scrollTop - list.clientHeight),
        };
      }
      case 'conversation_get_state': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        await afterNextPaint();
        const root = sessionId
          ? document.querySelector(`[data-testid="conversation-pane-${sessionId}"]`)
          : document.querySelector('.conversation-pane');
        if (!root) {
          throw new Error(`conversation pane not found${sessionId ? ` for session ${sessionId}` : ''}`);
        }
        const input = root.querySelector('[data-testid="conversation-input"]');
        const textarea = input instanceof HTMLTextAreaElement ? input : null;
        const messages = Array.from(root.querySelectorAll('.conversation-message')).map((node) => {
          const element = node as HTMLElement;
          const rendered = element.querySelector('.conversation-message-text');
          return {
            id: (element.dataset.testid || '').replace('conversation-message-', ''),
            role: element.dataset.role || '',
            streaming: element.dataset.streaming === 'true',
            text: rendered?.textContent || '',
            html: rendered?.innerHTML || '',
            blocks: {
              headings: rendered?.querySelectorAll('h1, h2, h3, h4, h5, h6').length ?? 0,
              tables: rendered?.querySelectorAll('table').length ?? 0,
              codeBlocks: rendered?.querySelectorAll('pre').length ?? 0,
              listItems: rendered?.querySelectorAll('li').length ?? 0,
              links: rendered?.querySelectorAll('a[href]').length ?? 0,
              diagrams: rendered?.querySelectorAll('.markdown-mermaid, .markdown-mermaid-loading').length ?? 0,
              pendingDiagrams: rendered?.querySelectorAll('[data-testid="markdown-diagram-pending"]').length ?? 0,
            },
          };
        });
        const list = root.querySelector('[data-testid="conversation-messages"]');
        const scroll = list instanceof HTMLElement
          ? {
            scrollTop: Math.round(list.scrollTop),
            scrollHeight: Math.round(list.scrollHeight),
            clientHeight: Math.round(list.clientHeight),
            fromBottom: Math.round(list.scrollHeight - list.scrollTop - list.clientHeight),
          }
          : null;
        const firstMessage = root.querySelector('.conversation-message');
        const column = list instanceof HTMLElement && firstMessage instanceof HTMLElement
          ? {
            available: Math.round(list.clientWidth),
            message: Math.round(firstMessage.getBoundingClientRect().width),
          }
          : null;
        const queued = Array.from(root.querySelectorAll('[data-testid="conversation-queued"]')).map((node) => ({
          kind: node.querySelector('.conversation-queued-label')?.textContent || '',
          text: node.querySelector('.conversation-queued-text')?.textContent || '',
        }));
        const tools = Array.from(root.querySelectorAll('.conversation-tool')).map((node) => {
          const element = node as HTMLElement;
          const body = element.querySelector('[data-testid="conversation-tool-body"]');
          return {
            callId: (element.dataset.testid || '').replace('conversation-tool-', ''),
            name: element.dataset.toolName || '',
            status: element.dataset.toolStatus || '',
            summary: element.querySelector('.conversation-tool-summary')?.textContent || '',
            error: element.querySelector('[data-testid="conversation-tool-error"]')?.textContent || '',
            expanded: element.dataset.expanded === 'true',
            waiting: Boolean(body?.querySelector('[data-testid="conversation-tool-waiting"]')),
            output: body?.querySelector('[data-testid="conversation-tool-output"]')?.textContent || '',
            hasPatch: Boolean(body?.querySelector('[data-testid="conversation-tool-patch"]')),
            fullOutputAvailable: Boolean(body?.querySelector('[data-testid="conversation-tool-full"]')),
            detailError: body?.querySelector('[data-testid="conversation-tool-detail-error"]')?.textContent || '',
          };
        });
        const notices = Array.from(root.querySelectorAll('.conversation-notice')).map((node) => {
          const element = node as HTMLElement;
          return {
            id: (element.dataset.testid || '').replace('conversation-notice-', ''),
            level: element.dataset.level || '',
            done: element.dataset.done === 'true',
            text: element.textContent || '',
          };
        });
        const modelPicker = root.querySelector('[data-testid="conversation-model"]');
        const model = modelPicker instanceof HTMLSelectElement ? modelPicker : null;
        const send = root.querySelector('[data-testid="conversation-send"]');
        const earlier = root.querySelector('[data-testid="conversation-load-earlier"]');
        return {
          sessionId,
          messages,
          scroll,
          column,
          tools,
          notices,
          queued,
          loadEarlierAvailable: Boolean(earlier),
          loadingHistory: Boolean(earlier instanceof HTMLButtonElement && earlier.disabled),
          historyDropped: Number(
            root.querySelector('[data-testid="conversation-history-dropped"]')?.getAttribute('data-dropped') || 0,
          ),
          model: model?.value || '',
          models: model ? Array.from(model.options).flatMap((option) => (option.value ? [option.value] : [])) : [],
          queueClearAvailable: Boolean(root.querySelector('[data-testid="conversation-queue-clear"]')),
          inputDisabled: Boolean(textarea?.disabled),
          placeholder: textarea?.placeholder || '',
          draft: textarea?.value || '',
          sendLabel: send?.textContent || '',
          followUpAvailable: Boolean(root.querySelector('[data-testid="conversation-follow-up"]')),
          recoverable: Boolean(root.querySelector('[data-testid="conversation-recoverable"]')),
          reloadAvailable: Boolean(root.querySelector('[data-testid="conversation-reload"]')),
        };
      }
      case 'garden_get_state':
        return collectGardenUiState();
      case 'notifications_get_state':
        return collectNotificationsUiState();
      case 'garden_board_get_state':
        return collectGardenBoardUiState();
      case 'garden_toggle_frame': {
        const control = frontGardenPanel()?.querySelector('.garden-chrome__frame');
        if (!(control instanceof HTMLElement)) {
          throw new Error('the garden is not open, so there is no frame to change');
        }
        control.click();
        await settleUi(2);
        await gardenFrameAtRest();
        await afterNextPaint();
        return collectGardenUiState();
      }
      case 'garden_open_plot':
      case 'garden_open_seed': {
        const seedId = typeof payload.seedId === 'string' ? payload.seedId : '';
        if (!seedId) throw new Error('garden_open_plot requires seedId');
        const row = frontGardenPanel()?.querySelector(`[data-seed-row="${seedId}"]`);
        if (!(row instanceof HTMLElement)) {
          throw new Error(`no row for ${seedId} (the panel is not showing it)`);
        }
        row.click();
        await settleUi(3);
        return collectGardenUiState();
      }
      case 'garden_search': {
        const query = typeof payload.query === 'string' ? payload.query : null;
        if (query === null) throw new Error('garden_search requires query');
        const field = frontGardenPanel()?.querySelector('.garden-search__input');
        if (!(field instanceof HTMLInputElement)) {
          throw new Error('the garden panel is not open, so there is nothing to search');
        }
        field.focus();
        setInputValue(field, query);
        await settleUi(2);
        return collectGardenUiState();
      }
      case 'garden_search_key': {
        const key = typeof payload.key === 'string' ? payload.key : null;
        if (!key) throw new Error('garden_search_key requires key');
        const field = frontGardenPanel()?.querySelector('.garden-search__input');
        if (!(field instanceof HTMLInputElement)) {
          throw new Error('the garden panel is not open, so there is nothing to walk');
        }
        field.focus();
        field.dispatchEvent(
          new KeyboardEvent('keydown', {
            key,
            altKey: Boolean(payload.altKey),
            metaKey: Boolean(payload.metaKey),
            bubbles: true,
            cancelable: true,
          }),
        );
        await settleUi(2);
        return collectGardenUiState();
      }
      case 'garden_climb_to': {
        const depth = typeof payload.depth === 'number' ? payload.depth : 0;
        const step = frontGardenPanel()?.querySelector(`[data-trail-depth="${depth}"]`);
        if (!(step instanceof HTMLElement)) {
          throw new Error(`no trail step at depth ${depth}`);
        }
        step.click();
        await settleUi(3);
        return collectGardenUiState();
      }
      case 'garden_expand_seed': {
        const seedId = typeof payload.seedId === 'string' ? payload.seedId : '';
        if (!seedId) throw new Error('garden_expand_seed requires seedId');
        const row = frontGardenPanel()?.querySelector(`[data-seed-row="${seedId}"]`);
        if (row instanceof HTMLElement) {
          row.click();
          await settleUi(3);
        }
        if (payload.bookkeeping === true) {
          const toggle = document.querySelector('.garden-page .garden-closed-toggle[aria-expanded="false"]');
          if (toggle instanceof HTMLElement) {
            toggle.click();
            await settleUi(1);
          }
        }
        return collectGardenSeedPage();
      }
      case 'garden_seed_page':
        return collectGardenSeedPage();
      case 'garden_press': {
        const key = typeof payload.key === 'string' ? payload.key : '';
        if (!key) throw new Error('garden_press requires key');
        const times = typeof payload.times === 'number' ? Math.max(1, Math.floor(payload.times)) : 1;
        const target = document.activeElement instanceof HTMLElement
          ? document.activeElement
          : frontGardenPanel()?.querySelector('.garden-viewport, .garden-columns');
        if (!(target instanceof HTMLElement)) throw new Error('the garden is not on screen');
        for (let press = 0; press < times; press += 1) {
          target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
          await settleUi(1);
        }
        await settleUi(2);
        return collectGardenUiState();
      }
      case 'garden_scroll': {
        const column = typeof payload.column === 'string' ? payload.column : '';
        const panel = frontGardenPanel();
        const viewport = column
          ? panel?.querySelector(`.garden-column[data-column="${column}"]`)
          : panel?.querySelector('.garden-viewport, .garden-column--reader');
        if (!(viewport instanceof HTMLElement)) {
          throw new Error(column ? `no column ${column} on screen` : 'the garden is not on screen');
        }
        const top = typeof payload.top === 'number' ? payload.top : null;
        const by = typeof payload.by === 'number' ? payload.by : 0;
        viewport.scrollTo({
          top: top === null ? viewport.scrollTop + by : top,
          behavior: payload.smooth === true ? 'smooth' : 'auto',
        });
        await settleUi(payload.smooth === true ? 40 : 3);
        return collectGardenUiState();
      }
      case 'garden_resume_seed': {
        const seedId = typeof payload.seedId === 'string' ? payload.seedId : '';
        if (!seedId) throw new Error('garden_resume_seed requires seedId');
        await waitForTestId(`seed-resume-${seedId}`);
        clickTestId(`seed-resume-${seedId}`);
        await settleUi(3);
        return { ok: true };
      }
      case 'garden_handover_seed': {
        const seedId = typeof payload.seedId === 'string' ? payload.seedId : '';
        const handoff = typeof payload.handoff === 'string' ? payload.handoff : '';
        if (!seedId) throw new Error('garden_handover_seed requires seedId');
        await waitForTestId(`seed-handover-${seedId}`);
        clickTestId(`seed-handover-${seedId}`);
        await settleUi(2);
        const form = frontGardenPanel()?.querySelector('.garden-handover');
        const field = form?.querySelector('textarea');
        const submit = form?.querySelector('button[type="submit"]');
        if (!(field instanceof HTMLTextAreaElement) || !(submit instanceof HTMLButtonElement)) {
          throw new Error(`Handover for ${seedId} needs placement or is not ready to submit`);
        }
        setControlValue(field, handoff);
        submit.click();
        await settleUi(3);
        return { ok: true };
      }
      case 'seed_document_get_state': {
        const scope = typeof payload.selector === 'string' && payload.selector
          ? payload.selector
          : '.workspace-dock-tile';
        // A workspace can hold more than one seed tile, and an older one is
        // still mounted: name the seed rather than taking the first tile.
        const seedId = typeof payload.seedId === 'string' ? payload.seedId : '';
        return collectSeedDocumentState(scope, seedId);
      }
      case 'automations_open_panel': {
        if (!openAutomationsPanel) {
          throw new Error('automations_open_panel is not configured');
        }
        openAutomationsPanel();
        await settleUi(3);
        return collectAutomationsUiState();
      }
      case 'automations_get_state':
        return collectAutomationsUiState();
      case 'automations_toggle_enabled': {
        const definitionId = typeof payload.definitionId === 'string' ? payload.definitionId : '';
        if (!definitionId) {
          throw new Error('automations_toggle_enabled requires definitionId');
        }
        clickTestId(`automation-toggle-${definitionId}`);
        await settleUi(3);
        return collectAutomationsUiState();
      }
      case 'automations_run_now': {
        const definitionId = typeof payload.definitionId === 'string' ? payload.definitionId : '';
        if (!definitionId) {
          throw new Error('automations_run_now requires definitionId');
        }
        clickTestId(`automation-run-now-${definitionId}`);
        await settleUi(3);
        return collectAutomationsUiState();
      }
      case 'automations_select_definition': {
        const definitionId = typeof payload.definitionId === 'string' ? payload.definitionId : '';
        if (!definitionId) {
          throw new Error('automations_select_definition requires definitionId');
        }
        clickTestId(`automation-definition-select-${definitionId}`);
        await settleUi(3);
        return collectAutomationsUiState();
      }
      case 'automation_form_open': {
        const definitionId = typeof payload.definitionId === 'string' ? payload.definitionId : '';
        clickTestId(definitionId ? `automation-edit-${definitionId}` : 'automation-new');
        await settleUi(3);
        return collectAutomationFormUiState();
      }
      case 'automation_form_get_state':
        return collectAutomationFormUiState();
      case 'automation_form_set_values': {
        const values = payload.values;
        if (!values || typeof values !== 'object') {
          throw new Error('automation_form_set_values requires values');
        }
        const handle = getAutomationFormAutomationHandle();
        if (!handle) throw new Error('automation form is not mounted');
        handle.setValues(values as Partial<AutomationFormAutomationState['values']>);
        await settleUi(2);
        return collectAutomationFormUiState();
      }
      case 'automation_form_submit': {
        const handle = getAutomationFormAutomationHandle();
        if (!handle) throw new Error('automation form is not mounted');
        handle.submit();
        await settleUi(3);
        return collectAutomationFormUiState();
      }
      case 'automation_form_click': {
        const button = typeof payload.button === 'string' ? payload.button : '';
        let testid: string;
        switch (button) {
          case 'save':
            testid = 'automation-form-save';
            break;
          case 'cancel':
            testid = 'automation-form-cancel';
            break;
          case 'close':
            testid = 'automation-form-close';
            break;
          case 'reload':
            testid = 'automation-form-reload';
            break;
          case 'delete':
            testid = 'automation-form-delete';
            break;
          default:
            throw new Error(`automation_form_click: unknown button "${button}"`);
        }
        clickTestId(testid);
        await settleUi(3);
        return collectAutomationFormUiState();
      }
      case 'automode_get_state':
        return collectAutoModeUiState();
      case 'automode_environment_slot': {
        const slot = typeof payload.slot === 'string' ? payload.slot : null;
        if (!slot) throw new Error('automode_environment_slot requires slot');
        const values = Array.isArray(payload.values)
          ? payload.values.filter((v): v is string => typeof v === 'string')
          : null;
        if (values === null) throw new Error('automode_environment_slot requires values');
        const handle = getAutoModeAutomationHandle();
        if (!handle) throw new Error('the auto mode settings pane is not mounted');
        await handle.setEnvironmentSlot(slot, values);
        await settleUi(3);
        return collectAutoModeUiState();
      }
      case 'automode_environment_edit': {
        const slot = typeof payload.slot === 'string' ? payload.slot : '';
        if (!slot) throw new Error('automode_environment_edit requires slot');
        clickTestId(`automode-slot-edit-${slot}`);
        await settleUi(3);
        return collectAutoModeUiState();
      }
      case 'click_nudge_trigger': {
        // The deliver-now trigger renders only in NudgeIndicator's paused mode. The header chip and
        // the sidebar button issue the same trigger_nudge; 'tile' is a legacy alias for the header.
        const requested = typeof payload.surface === 'string' ? payload.surface : 'any';
        const header = document.querySelector('.nudge-header-trigger');
        const sidebar = document.querySelector('[aria-label="Deliver the pending ticket nudge now"]');
        const wantsHeader = requested === 'header' || requested === 'tile';
        const target =
          requested === 'sidebar' ? sidebar : wantsHeader ? header : (header ?? sidebar);
        if (!(target instanceof HTMLElement)) {
          throw new Error(
            `Nudge trigger button not found (surface=${requested}); the session must be selected, have unread ticket activity, and not be stopped at an approval prompt`,
          );
        }
        clickElement(target);
        await settleUi(2);
        return { clicked: true, surface: target === header ? 'header' : 'sidebar' };
      }
      case 'capture_structured_snapshot':
        return collectVisualSnapshot(
          sessions,
          activeSessionId,
          getActivePaneIdForSession,
          getPaneText,
          getPaneSize,
          getPaneVisibleContent,
          isRuntimeAttached,
          {
            includePaneText: payload.includePaneText !== false,
            sessionIds: Array.isArray(payload.sessionIds)
              ? new Set(payload.sessionIds.filter((value): value is string => typeof value === 'string'))
              : null,
          },
        );
      case 'capture_render_health':
        return collectRenderHealthSnapshot(
          sessions,
          activeSessionId,
          getActivePaneIdForSession,
          getPaneText,
          getPaneSize,
          getPaneVisibleContent,
          isSessionPaneInputFocused,
          isRuntimeAttached,
          {
            sessionIds: Array.isArray(payload.sessionIds)
              ? new Set(payload.sessionIds.filter((value): value is string => typeof value === 'string'))
              : null,
          },
        );
      case 'capture_perf_snapshot': {
        const settleFrames = typeof payload.settleFrames === 'number' ? payload.settleFrames : 2;
        const includeMemory = payload.includeMemory !== false;
        const sessionIds = Array.isArray(payload.sessionIds)
          ? new Set(payload.sessionIds.filter((value): value is string => typeof value === 'string'))
          : null;
        await settleUi(settleFrames);
        return capturePerfSnapshot(
          sessions,
          activeSessionId,
          getActivePaneIdForSession,
          { includeMemory, sessionIds },
        );
      }
      case 'clear_perf_counters':
        clearPtyPerfSnapshot();
        return { ok: true };
      case 'benchmark_pty_transport': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : activeSessionId;
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session || !sessionId) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        const mode = payload.mode === 'bytes' || payload.mode === 'base64' || payload.mode === 'json_base64'
          ? payload.mode
          : 'json_base64';
        const chunkBytes = typeof payload.chunkBytes === 'number' ? payload.chunkBytes : 16 * 1024;
        const chunkCount = typeof payload.chunkCount === 'number' ? payload.chunkCount : 128;
        const benchmarkPayload = payload.payload === 'progress' ? 'progress' : 'scroll';
        const flushEvery = typeof payload.flushEvery === 'number' && payload.flushEvery > 0
          ? Math.floor(payload.flushEvery)
          : 1;
        const interChunkDelayMs = typeof payload.interChunkDelayMs === 'number'
          ? Math.max(0, payload.interChunkDelayMs)
          : 0;
        const runtimeId =
          session.workspace.agents.find((entry) => entry.id === paneId)?.runtimeId ||
          `bench:${paneId}`;
        const bytes = buildBenchmarkBytes(chunkBytes, benchmarkPayload);
        const base64Payload = encodeBytesToBase64(bytes);

        selectSession(sessionId);
        focusPane(sessionId, paneId);
        await settleUi(2);
        if (!resetSessionPaneTerminal(sessionId, paneId)) {
          throw new Error(`Pane terminal not ready for ${paneId}`);
        }
        const paneTerminalPerf = () => getTerminalPerfSnapshot().find(
          (terminal) => terminal.runtimeId === runtimeId || (
            terminal.sessionId === sessionId && terminal.paneId === paneId
          ),
        );

        if (benchmarkPayload === 'progress') {
          await waitForPaneWriteQuiescence(() => paneTerminalPerf()?.writeParsedCount ?? null);
          await drainSessionPaneTerminal(sessionId, paneId);
          await settleUi(2);

          const beforeSeed = paneTerminalPerf();
          if (!beforeSeed) {
            throw new Error(`Pane terminal disappeared before seeding ${paneId}`);
          }
          const seed = buildBenchmarkProgressSeed(beforeSeed.cols, beforeSeed.rows);
          if (!(await injectSessionPaneBytes(sessionId, paneId, seed))) {
            throw new Error(`Failed to seed progress fixture in pane ${paneId}`);
          }
          if (!(await drainSessionPaneTerminal(sessionId, paneId))) {
            throw new Error(`Failed to drain progress fixture in pane ${paneId}`);
          }
          // Output paints are paced at 30 Hz: wait for a paint that actually covers the seed, or the
          // measured writes overwrite a fixture whose dense surface never rendered.
          await waitForSeededPaint(paneTerminalPerf, beforeSeed.renderCount);
          await settleUi(2);
        }
        await settleUi(2);
        clearPtyPerfSnapshot();
        const rendererBefore = paneTerminalPerf();
        if (
          benchmarkPayload === 'progress'
          && rendererBefore
          && rendererBefore.modelPrintable < rendererBefore.cols * rendererBefore.rows * 0.5
        ) {
          throw new Error(
            `Progress fixture is not dense: ${rendererBefore.modelPrintable} printable cells in ${rendererBefore.cols}x${rendererBefore.rows}`,
          );
        }

        const startedAt = performance.now();
        let bufferedByteChunks: Uint8Array[] = [];
        const flushBufferedBytes = async () => {
          if (bufferedByteChunks.length === 0) {
            return;
          }
          const combined = concatByteChunks(bufferedByteChunks);
          bufferedByteChunks = [];
          const ok = await injectSessionPaneBytes(sessionId, paneId, combined);
          if (!ok) {
            throw new Error(`Failed to inject buffered bytes into pane ${paneId}`);
          }
        };
        for (let index = 0; index < chunkCount; index += 1) {
          if (mode === 'bytes') {
            if (flushEvery === 1) {
              const ok = await injectSessionPaneBytes(sessionId, paneId, bytes);
              if (!ok) {
                throw new Error(`Failed to inject bytes into pane ${paneId}`);
              }
            } else {
              bufferedByteChunks.push(bytes);
              if (bufferedByteChunks.length >= flushEvery) {
                await flushBufferedBytes();
              }
            }
            if (interChunkDelayMs > 0) {
              await waitForBenchmarkDelay(interChunkDelayMs);
            }
            continue;
          }

          if (mode === 'base64') {
            if (flushEvery === 1) {
              const ok = await injectSessionPaneBase64(sessionId, paneId, base64Payload);
              if (!ok) {
                throw new Error(`Failed to inject base64 payload into pane ${paneId}`);
              }
            } else {
              bufferedByteChunks.push(decodeBase64ToBytes(base64Payload));
              if (bufferedByteChunks.length >= flushEvery) {
                await flushBufferedBytes();
              }
            }
            if (interChunkDelayMs > 0) {
              await waitForBenchmarkDelay(interChunkDelayMs);
            }
            continue;
          }

          const raw = JSON.stringify({
            event: 'pty_output',
            id: runtimeId,
            data: base64Payload,
            seq: index,
          });
          const parseStartedAt = performance.now();
          const parsed = JSON.parse(raw) as { id: string; data: string };
        recordWsJsonParse(raw.length, performance.now() - parseStartedAt, 'pty_output', parsed.data.length);
          if (flushEvery === 1) {
            const ok = await injectSessionPaneBase64(sessionId, paneId, parsed.data);
            if (!ok) {
              throw new Error(`Failed to replay parsed payload into pane ${paneId}`);
            }
          } else {
            bufferedByteChunks.push(decodeBase64ToBytes(parsed.data));
            if (bufferedByteChunks.length >= flushEvery) {
              await flushBufferedBytes();
            }
          }
          if (interChunkDelayMs > 0) {
            await waitForBenchmarkDelay(interChunkDelayMs);
          }
        }
        await flushBufferedBytes();
        if (!(await drainSessionPaneTerminal(sessionId, paneId))) {
          throw new Error(`Failed to drain pane ${paneId}`);
        }

        await settleUi(2);
        const totalMs = performance.now() - startedAt;
        const rendererAfter = paneTerminalPerf();
        return {
          sessionId,
          paneId,
          runtimeId,
          mode,
          payload: benchmarkPayload,
          flushEvery,
          interChunkDelayMs,
          chunkBytes: bytes.length,
          chunkCount,
          totalPayloadBytes: bytes.length * chunkCount,
          totalMs,
          throughputMiBPerSec: totalMs > 0
            ? ((bytes.length * chunkCount) / (1024 * 1024)) / (totalMs / 1000)
            : null,
          pty: getPtyPerfSnapshot(),
          renderer: rendererBefore && rendererAfter
            ? {
                renderCount: rendererAfter.renderCount - rendererBefore.renderCount,
                cpuSubmitMs: rendererAfter.renderCpuTotalMs - rendererBefore.renderCpuTotalMs,
                lastFrameMs: rendererAfter.lastRenderCpuMs,
                fullPaintCount: rendererAfter.renderFullCount - rendererBefore.renderFullCount,
                partialPaintCount: rendererAfter.renderPartialCount - rendererBefore.renderPartialCount,
                rowsPainted: rendererAfter.renderRowsPainted - rendererBefore.renderRowsPainted,
                submittedQuads: rendererAfter.renderSubmittedQuads - rendererBefore.renderSubmittedQuads,
                retainedRowVertexBytes: rendererAfter.renderRetainedRowVertexBytes,
                retainedStagingBytes: rendererAfter.renderRetainedStagingBytes,
                fixturePrintable: rendererBefore.modelPrintable,
                finalModelPrintable: rendererAfter.modelPrintable,
                finalPaintQuads: rendererAfter.lastPaintQuads,
                scheduledRequests: rendererAfter.scheduledRenderRequests - rendererBefore.scheduledRenderRequests,
                scheduledCoalesced: rendererAfter.scheduledRenderCoalesced - rendererBefore.scheduledRenderCoalesced,
                scheduledDeferred: rendererAfter.scheduledRenderDeferred - rendererBefore.scheduledRenderDeferred,
                writeParsedCount: rendererAfter.writeParsedCount - rendererBefore.writeParsedCount,
              }
            : null,
          pane: {
            size: getPaneSize(sessionId, paneId),
            textLength: getPaneText(sessionId, paneId).length,
          },
        };
      }
      case 'present_get_state': {
        const notices = (presentationNotices || []).map((presentation) => ({
          id: presentation.id,
          sessionId: presentation.session_id,
          title: presentation.title,
          status: presentation.status,
          latestRoundSeq: presentation.latest_round_seq,
          latestRoundSubmitted: presentation.latest_round_submitted,
        }));
        const chips = Array.from(document.querySelectorAll('.presentation-chip')).map((element) => ({
          presentationId: element.getAttribute('data-presentation-id') || '',
          sessionId: element.getAttribute('data-session-id') || '',
          title: element.getAttribute('title') || '',
        }));
        return { notices, chips };
      }
      case 'present_click_chip': {
        const presentationId = typeof payload.presentationId === 'string' ? payload.presentationId : '';
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!presentationId && !sessionId) {
          throw new Error('present_click_chip requires presentationId or sessionId');
        }
        const selector = presentationId
          ? `.presentation-chip[data-presentation-id="${presentationId}"]`
          : `.presentation-chip[data-session-id="${sessionId}"]`;
        const chip = document.querySelector<HTMLElement>(selector);
        if (!chip) {
          throw new Error(`present_click_chip: no chip found for ${selector}`);
        }
        chip.click();
        return { clicked: true, presentationId: chip.getAttribute('data-presentation-id') || '' };
      }
      default:
        throw new Error(`Unknown automation action: ${request.action}`);
    }
  }, [
    activeSessionId,
    closePane,
    createSession,
    closeSession,
    fitSessionActivePane,
    focusPane,
    injectSessionPaneBase64,
    injectSessionPaneBytes,
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    getActivePaneIdForSession,
    getPaneSize,
    getPaneText,
    getPaneBlockState,
    getPanePlacementState,
    openDockPanel,
    openShortcutEditor,
    presentationNotices,
    resetSessionPaneTerminal,
    drainSessionPaneTerminal,
    selectSession,
    selectWorkspace,
    sendRuntimeInput,
    sessions,
    splitPane,
  ]);

  const handleAutomationRequestRef = useRef(handleAutomationRequest);
  handleAutomationRequestRef.current = handleAutomationRequest;

  useEffect(() => {
    // Runtime gate injected by the Rust shell; the rule lives in
    // app/src-tauri/src/profile.rs::automation_enabled.
    const automationEnabled =
      typeof window !== 'undefined' && (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED === true;
    if (!isTauri() || !automationEnabled) {
      return;
    }

    void emit(UI_AUTOMATION_READY_EVENT, { ready: true });
    const unlistenPromise = listen<AutomationRequest>(UI_AUTOMATION_REQUEST_EVENT, async (event) => {
      const request = event.payload;
      // The automation server broadcasts to ALL webview windows and resolves on the first response,
      // so exactly one listener answers; present_window_* belongs to usePresentAutomationBridge.
      if (isPresentWindowAction(request.action)) {
        return;
      }
      let response: AutomationResponse;
      try {
        const result = await handleAutomationRequestRef.current(request);
        response = {
          request_id: request.request_id,
          ok: true,
          result,
        };
      } catch (error) {
        response = {
          request_id: request.request_id,
          ok: false,
          error: error instanceof Error ? error.message : String(error),
        };
      }
      await emit(UI_AUTOMATION_RESPONSE_EVENT, response);
    });

    return () => {
      void unlistenPromise.then((unlisten) => unlisten());
    };
  }, []);
}
