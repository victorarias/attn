import { useCallback, useEffect, useRef } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import type { Session } from '../store/sessions';
import type { Presentation } from '../types/generated';
import type { Ticket } from './useDaemonSocket';
import type { SessionAgent } from '../types/sessionAgent';
import type { TerminalSplitDirection } from '../types/workspace';
import { SHORTCUTS, type ShortcutId, type Combo, isChord, resolveBinding } from '../shortcuts';
import { getGridAutomationHandle, INACTIVE_GRID_STATE } from '../components/grid/gridAutomation';
import { getSettingsAutomationHandle, INACTIVE_SETTINGS_STATE } from '../components/settingsAutomation';
import { getTerminalPerfSnapshot } from '../utils/terminalPerf';
import { readWarmWorkspaceLimit } from '../utils/terminalVirtualization';
import { dumpTerminalGeometry } from '../utils/terminalDiagnosticsLog';
import { clearPtyPerfSnapshot, getPtyPerfSnapshot, recordPtyDecode, recordWsJsonParse } from '../utils/ptyPerf';
import { buildSessionRenderHealth } from '../utils/renderHealth';
import { boundTicketForSession } from '../utils/tickets';
import { collectWorkspaceLayoutDiagnostics, projectWorkspaceBounds } from '../utils/workspaceDiagnostics';
import type { TerminalVisibleContentSnapshot } from '../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../utils/terminalStyleSummary';
import type { BlockStateSnapshot } from '../components/GhosttyTerminal';
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
  createSession: (label: string, cwd: string, id?: string, agent?: SessionAgent, endpointId?: string, yoloMode?: boolean, options?: { chiefOfStaff?: boolean }) => Promise<string>;
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
  fitSessionActivePane: (sessionId: string) => void;
  sendRuntimeInput: (runtimeId: string, data: string, source?: string) => void;
  isRuntimeAttached: (runtimeId: string) => boolean;
  // Ticket detail panel (work-tracker). The mutation actions drive the real
  // panel controls, so the bridge only needs to open/close the panel and read
  // the live ticket rows; openDockPanel above is reused to mount the dock.
  openTicketDetail?: (ticketId: string) => void;
  closeTicketDetail?: () => void;
  tickets?: Ticket[];
  // Presentation notices (pane-header review chips). Read-only for the
  // bridge: the chip DOM is the source of truth for what's actually rendered.
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
    // GhosttyTerminal renders this when it gives up rebuilding the renderer
    // (see the WebGL context-loss recovery give-up path) — exposed so the
    // packaged-app harness can assert recovery actually cleared it, not just
    // that the diagnostics log said so.
    errorVisible: paneElement.querySelector('.ghostty-terminal-error') != null,
  };
}

async function captureDomScreenshotData(selector?: string) {
  // An optional selector scopes the capture to a subtree. This avoids serializing
  // the whole #root (which holds the WebGL terminal canvas html-to-image cannot
  // serialize quickly and times out on) when you only need one panel.
  //
  // If a selector is given but does not resolve, DO NOT silently fall back to
  // #root: that fallback re-introduces the terminal-canvas hang while hiding the
  // real cause (the element you asked for is not mounted). Fail fast with a clear
  // message instead so the caller learns the panel is not on screen.
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

  // Freeze CSS animations/transitions for the duration of the capture. Two reasons:
  // (1) WebKit can leave html-to-image's serialized SVG <image> in a never-settled
  //     load state when the cloned subtree has a running `animation: ... infinite`
  //     (e.g. the live "Current step" spinner), so toPng hangs until the caller
  //     times out. A static completed panel captures instantly; a live one stalls.
  // (2) A screenshot should be a stable frame, not a random mid-animation tick.
  const freeze = document.createElement('style');
  freeze.textContent =
    '*,*::before,*::after{animation:none!important;transition:none!important;}';
  document.head.appendChild(freeze);
  // Force a style/layout flush so the freeze takes effect before we serialize.
  void document.body.offsetHeight;

  let dataUrl: string;
  try {
    dataUrl = await toPng(target, {
      cacheBust: true,
      pixelRatio: 1,
      backgroundColor,
      // Embedding @font-face resources fetches each font and can hang indefinitely
      // (the capture then times out). Skip it — captured text falls back to system
      // fonts, which is fine for verification screenshots.
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
  // Resolve through overrides so automation exercises the live binding. A chord
  // fires as two synchronous keystrokes (leader then follow), which the shared
  // chord state machine pairs up.
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

// Finds the WebGL canvas of the pane currently visible on screen — the one
// workspace root marked visible, and within it the pane its own
// data-active-pane-id names. Used by the lose_webgl_context harness action to
// verify context-loss recovery without needing a sessionId/paneId round trip.
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

// Resolves a pane's terminal container plus the rect of its CELL GRID. The
// grid rect is the canvas, which the renderer sizes to exactly
// cols*cellWidth x rows*cellHeight; the container is taller/wider by the
// fit remainder. Cell math must use the grid rect — proportional division of
// the container rect drifts downward and clicks the wrong row near the
// bottom of the pane (the app's own hit-test divides the canvas rect by the
// renderer's real cell metrics).
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

// Page-coordinate center of a terminal cell plus the window inner size, so
// native (HID) drivers can convert to window-relative click positions.
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
): string {
  const { terminal, gridRect } = paneTerminalGrid(sessionId, paneId);
  terminal.dispatchEvent(new MouseEvent('mousemove', {
    bubbles: true,
    cancelable: true,
    view: window,
    metaKey: meta,
    ...paneCellPoint(gridRect, size, cell),
  }));
  return getComputedStyle(terminal).cursor;
}

function dragPaneSelection(
  sessionId: string,
  paneId: string,
  size: { cols: number; rows: number },
  start: { col: number; row: number },
  end: { col: number; row: number },
) {
  const { terminal, gridRect } = paneTerminalGrid(sessionId, paneId);
  const startPoint = paneCellPoint(gridRect, size, start);
  const endPoint = paneCellPoint(gridRect, size, end);
  terminal.dispatchEvent(new MouseEvent('mousedown', {
    bubbles: true,
    cancelable: true,
    view: window,
    button: 0,
    buttons: 1,
    ...startPoint,
  }));
  terminal.dispatchEvent(new MouseEvent('mousemove', {
    bubbles: true,
    cancelable: true,
    view: window,
    buttons: 1,
    ...endPoint,
  }));
  terminal.dispatchEvent(new MouseEvent('mouseup', {
    bubbles: true,
    cancelable: true,
    view: window,
    button: 0,
    ...endPoint,
  }));
}

// dragLeafHeader synthesizes a leaf re-dock drag: pointerdown on the leaf's
// draggable header, then pointermove/pointerup on window at a drop point given
// as a fraction of the panes container. This exercises the real beginLeafDrag →
// computeDockTarget → onMoveLeaf path without a physical OS drag.
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

  // pointerdown bubbles to React's onPointerDown (beginLeafDrag); move/up reach
  // the window listeners beginLeafDrag registers.
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

// Set the value of a real form control (input/textarea/select) the way React's
// controlled-component machinery expects: bypass React's value-tracker via the
// native prototype setter, then fire both `input` (text controls) and `change`
// (selects) so the component's onChange runs exactly as a user edit would.
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

// Click an element by data-testid the way clickPaneElement clicks a pane: a full
// mousedown/mouseup/click sequence so handlers that listen to any of them fire.
function clickTestId(testid: string) {
  const element = document.querySelector(`[data-testid="${testid}"]`);
  if (!(element instanceof HTMLElement)) {
    throw new Error(`Element not found: [data-testid="${testid}"]`);
  }
  for (const type of ['mousedown', 'mouseup', 'click'] as const) {
    element.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, view: window }));
  }
}

// Serialize what the TicketDetailPanel is actually rendering, for assertions.
// `statusOptions` is the decisive signal that `crashed` is not a manual
// destination; `disabled` exposes the in-flight gating the review fixes added.
function collectTicketDetailUiState() {
  const panel = document.querySelector('[data-testid="ticket-detail-panel"]');
  if (!(panel instanceof HTMLElement)) {
    return { present: false };
  }
  const text = (selector: string) => panel.querySelector(selector)?.textContent?.trim() ?? '';
  const select = panel.querySelector('[data-testid="ticket-status-select"]');
  const statusSelect = select instanceof HTMLSelectElement ? select : null;
  const descriptionInput = panel.querySelector('[data-testid="ticket-description-input"]');
  const editingDescription = descriptionInput instanceof HTMLTextAreaElement;
  const addCommentButton = panel.querySelector('[data-testid="ticket-add-comment"]');
  const saveDescriptionButton = panel.querySelector('[data-testid="ticket-save-description"]');
  return {
    present: true,
    ticketId: text('.ticket-detail-id'),
    title: text('.ticket-detail-title'),
    // The raw status comes from the select; the badge is the human label.
    status: statusSelect ? statusSelect.value : '',
    statusBadge: text('.ticket-status-badge'),
    statusOptions: statusSelect ? Array.from(statusSelect.options).map((option) => option.value) : [],
    editingDescription,
    description: editingDescription
      ? descriptionInput.value
      : text('.ticket-detail-description'),
    activity: Array.from(panel.querySelectorAll('.ticket-activity-entry')).map((entry) => ({
      kind: entry.getAttribute('data-kind') ?? '',
      author: entry.querySelector('.ticket-activity-author')?.textContent?.trim() ?? '',
      move: entry.querySelector('.ticket-activity-move')?.textContent?.trim() ?? '',
      comment: entry.querySelector('.ticket-activity-comment')?.textContent?.trim() ?? '',
    })),
    attachments: Array.from(panel.querySelectorAll('.ticket-attachment')).map((attachment) => ({
      filename: attachment.querySelector('.ticket-attachment-name')?.textContent?.trim() ?? '',
      note: attachment.querySelector('.ticket-attachment-note')?.textContent?.trim() ?? '',
    })),
    canResume: panel.querySelector('[data-testid="ticket-resume"]') instanceof HTMLElement,
    loading: Boolean(panel.querySelector('.ticket-detail-loading')),
    error: text('.ticket-detail-error'),
    actionError: text('.ticket-action-error'),
    disabled: {
      statusSelect: statusSelect ? statusSelect.disabled : null,
      addComment: addCommentButton instanceof HTMLButtonElement ? addCommentButton.disabled : null,
      saveDescription:
        saveDescriptionButton instanceof HTMLButtonElement ? saveDescriptionButton.disabled : null,
    },
  };
}

function getLocationPickerRoot() {
  const root = document.querySelector('[data-testid="location-picker"]');
  return root instanceof HTMLElement ? root : null;
}

function getLocationPickerOverlay() {
  const overlay = document.querySelector('[data-testid="location-picker-overlay"]');
  return overlay instanceof HTMLElement ? overlay : null;
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

function buildBenchmarkBytes(chunkBytes: number): Uint8Array {
  const safeChunkBytes = Math.max(64, Math.floor(chunkBytes));
  const linePayloadWidth = 112;
  let output = '';
  let lineNumber = 0;
  while (output.length < safeChunkBytes) {
    output += `bench ${String(lineNumber).padStart(6, '0')} ${'x'.repeat(linePayloadWidth)}\r\n`;
    lineNumber += 1;
  }
  return new TextEncoder().encode(output.slice(0, safeChunkBytes));
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
  fitSessionActivePane,
  sendRuntimeInput,
  isRuntimeAttached,
  openTicketDetail,
  closeTicketDetail,
  tickets,
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
        // Re-read through the module getter rather than the captured handle:
        // SettingsModal re-registers a fresh handle on each render (its
        // useEffect depends on selectedSection), so the captured handle's
        // getState still closes over the pre-selection section.
        return getSettingsAutomationHandle()?.getState() ?? INACTIVE_SETTINGS_STATE;
      }
      case 'capture_screenshot_data':
        return captureDomScreenshotData(
          typeof payload.selector === 'string' ? payload.selector : undefined,
        );
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
        if (!cwd) {
          throw new Error('create_session requires cwd');
        }
        const sessionId = await createSession(label, cwd, providedSessionId, agent, endpointId, undefined, { chiefOfStaff });
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
        // Drives window.attnSetWarmWorkspaces (terminal virtualization warm-set
        // size) so the perf harness can A/B retained RSS at different warm
        // limits. Returns the applied limit plus the count of virtualized
        // (torn-down) panes so the harness can verify virtualization engaged.
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
        // Read-only companion to set_warm_workspace_limit: returns the current
        // warm-workspace limit (default-aware, read from localStorage) so the
        // perf harness can capture it before a sweep and restore it afterward
        // instead of leaking the last swept value into the next run.
        const limit = readWarmWorkspaceLimit();
        const virtualizedPanes = document.querySelectorAll('[data-testid^="pane-virtualized-"]').length;
        return { limit, virtualizedPanes };
      }
      case 'dump_terminal_geometry': {
        // Same-moment app-side read of every mounted pane's model grid, cell
        // metrics, clientWidth/Height, and DOM-truth canvas rects — avoids the
        // cross-clock ambiguity of comparing a harness screenshot's timestamp
        // against a separately-read disk dump.
        const snapshots = dumpTerminalGeometry();
        return { snapshots };
      }
      case 'lose_webgl_context': {
        // Deliberately kills the active pane's WebGL context so the packaged
        // app harness can verify GhosttyTerminal's context-loss auto-recovery
        // (epoch rebuild + backoff) end to end, without needing a real GPU
        // fault to reproduce it.
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
      case 'select_workspace': {
        // Mirrors the sidebar row click and ⌘1–9 (both call selectWorkspace).
        // Activates a workspace by id, including a tile-only one that has no
        // session to route selection through.
        const workspaceId = typeof payload.workspaceId === 'string' ? payload.workspaceId : '';
        if (!workspaceId) {
          throw new Error('select_workspace requires workspaceId');
        }
        selectWorkspace(workspaceId);
        await settleUi();
        return { workspaceId };
      }
      case 'get_workspace_ui_state': {
        // Workspace-centric DOM query (the session UI-state verbs key off a
        // session id, which a tile-only workspace lacks). Reports whether the
        // workspace's terminal surface is mounted, whether it is the active
        // (visible) one, and which leaves it is rendering.
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
        const cursor = hoverPaneCell(
          viewSessionId,
          paneId,
          size,
          { col: cell.col, row: cell.row },
          payload.meta === true,
        );
        await settleUi(2);
        return { sessionId, paneId, viewSessionId, cursor };
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
        dragPaneSelection(
          viewSessionId,
          paneId,
          size,
          { col: start.col, row: start.row },
          { col: end.col, row: end.row },
        );
        await settleUi(2);
        return { sessionId, paneId, viewSessionId, start, end };
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
        // Stable response shape: callers can distinguish "pane has no live
        // terminal handle" (available=false) from "pane has no blocks".
        return {
          sessionId,
          paneId,
          viewSessionId,
          available: blockState !== null,
          ...(blockState ?? {}),
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
      // --- Ticket detail panel (work-tracker) ------------------------------
      // Read-only board snapshot (foundation for the slice-5 board scenario).
      case 'ticket_list':
        return {
          tickets: (tickets ?? []).map((ticket) => ({
            id: ticket.id,
            title: ticket.title,
            status: ticket.status,
            assignee: ticket.assignee,
            last_agent_id: ticket.last_agent_id,
            cwd: ticket.cwd,
          })),
        };
      // Open the detail panel for the ticket bound to a delegated session
      // (assignee == session id), mirroring the "from a session" entry point.
      case 'ticket_open_via_dashboard': {
        if (!openTicketDetail) {
          throw new Error('ticket_open_via_dashboard is not configured');
        }
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('ticket_open_via_dashboard requires sessionId');
        }
        const boundTicket = boundTicketForSession(tickets ?? [], sessionId);
        if (!boundTicket) {
          throw new Error(`No ticket is bound to session ${sessionId}`);
        }
        openTicketDetail(boundTicket.id);
        await settleUi(3);
        return collectTicketDetailUiState();
      }
      // Programmatic open (mirrors handleOpenTicketDetail) for when there is no
      // dispatch row to click through.
      case 'ticket_open_detail': {
        if (!openTicketDetail) {
          throw new Error('ticket_open_detail is not configured');
        }
        const ticketId = typeof payload.ticketId === 'string' ? payload.ticketId : '';
        if (!ticketId) {
          throw new Error('ticket_open_detail requires ticketId');
        }
        openTicketDetail(ticketId);
        await settleUi(3);
        return collectTicketDetailUiState();
      }
      case 'ticket_close_detail': {
        if (!closeTicketDetail) {
          throw new Error('ticket_close_detail is not configured');
        }
        closeTicketDetail();
        await settleUi(2);
        return { ok: true };
      }
      case 'ticket_detail_get_state':
        return collectTicketDetailUiState();
      // Drive the real status <select>. Reject a value the panel does not offer
      // (e.g. `crashed`) the same way the UI does — the option simply isn't there.
      case 'ticket_set_status': {
        const status = typeof payload.status === 'string' ? payload.status : '';
        if (!status) {
          throw new Error('ticket_set_status requires status');
        }
        const select = document.querySelector('[data-testid="ticket-status-select"]');
        if (!(select instanceof HTMLSelectElement)) {
          throw new Error('Ticket status select not found (panel not open or actions not wired)');
        }
        const options = Array.from(select.options).map((option) => option.value);
        if (!options.includes(status)) {
          throw new Error(`Status "${status}" is not a selectable destination (options: ${options.join(', ')})`);
        }
        setControlValue(select, status);
        await settleUi(3);
        return collectTicketDetailUiState();
      }
      case 'ticket_submit_comment': {
        const comment = typeof payload.comment === 'string' ? payload.comment : '';
        if (!comment) {
          throw new Error('ticket_submit_comment requires comment');
        }
        const input = document.querySelector('[data-testid="ticket-comment-input"]');
        if (!(input instanceof HTMLTextAreaElement)) {
          throw new Error('Ticket comment input not found');
        }
        input.focus();
        setControlValue(input, comment);
        await settleUi(1);
        clickTestId('ticket-add-comment');
        await settleUi(3);
        return collectTicketDetailUiState();
      }
      case 'ticket_edit_description': {
        if (typeof payload.description !== 'string') {
          throw new Error('ticket_edit_description requires description');
        }
        // Enter edit mode first if the textarea is not already showing.
        if (!document.querySelector('[data-testid="ticket-description-input"]')) {
          clickTestId('ticket-edit-description');
          await settleUi(2);
        }
        const input = document.querySelector('[data-testid="ticket-description-input"]');
        if (!(input instanceof HTMLTextAreaElement)) {
          throw new Error('Ticket description input not found');
        }
        input.focus();
        setControlValue(input, payload.description);
        await settleUi(1);
        clickTestId('ticket-save-description');
        await settleUi(3);
        return collectTicketDetailUiState();
      }
      case 'ticket_resume': {
        clickTestId('ticket-resume');
        await settleUi(2);
        return { ok: true };
      }
      case 'click_nudge_trigger': {
        // The "deliver now" trigger renders only in NudgeIndicator's paused mode
        // (the session is selected, has unread ticket activity, and is not stopped at
        // an approval prompt — the deliver-on-demand chip shows in every other state).
        // The pane-header chip button and the sidebar-row button both issue the same
        // trigger_nudge command; prefer the header button, fall back to the sidebar.
        // 'tile' is accepted as a legacy alias for the header surface.
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
        const flushEvery = typeof payload.flushEvery === 'number' && payload.flushEvery > 0
          ? Math.floor(payload.flushEvery)
          : 1;
        const runtimeId =
          session.workspace.agents.find((entry) => entry.id === paneId)?.runtimeId ||
          `bench:${paneId}`;
        const bytes = buildBenchmarkBytes(chunkBytes);
        const base64Payload = encodeBytesToBase64(bytes);

        selectSession(sessionId);
        focusPane(sessionId, paneId);
        await settleUi(2);
        if (!resetSessionPaneTerminal(sessionId, paneId)) {
          throw new Error(`Pane terminal not ready for ${paneId}`);
        }
        clearPtyPerfSnapshot();

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
        }
        await flushBufferedBytes();
        if (!(await drainSessionPaneTerminal(sessionId, paneId))) {
          throw new Error(`Failed to drain pane ${paneId}`);
        }

        await settleUi(2);
        const totalMs = performance.now() - startedAt;
        return {
          sessionId,
          paneId,
          runtimeId,
          mode,
          flushEvery,
          chunkBytes: bytes.length,
          chunkCount,
          totalPayloadBytes: bytes.length * chunkCount,
          totalMs,
          throughputMiBPerSec: totalMs > 0
            ? ((bytes.length * chunkCount) / (1024 * 1024)) / (totalMs / 1000)
            : null,
          pty: getPtyPerfSnapshot(),
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
    // Gate flipped from compile-time `VITE_UI_AUTOMATION` to a runtime
    // global injected by the Rust shell (`append_invoke_initialization_script`).
    // The Rust-side decision lives in `app/src-tauri/src/profile.rs::automation_enabled`
    // and applies the same rule as the workspace UI: explicit
    // ATTN_AUTOMATION wins, otherwise dev profile defaults on.
    const automationEnabled =
      typeof window !== 'undefined' && (window as { __ATTN_AUTOMATION_ENABLED?: boolean }).__ATTN_AUTOMATION_ENABLED === true;
    if (!isTauri() || !automationEnabled) {
      return;
    }

    void emit(UI_AUTOMATION_READY_EVENT, { ready: true });
    const unlistenPromise = listen<AutomationRequest>(UI_AUTOMATION_REQUEST_EVENT, async (event) => {
      const request = event.payload;
      // The Rust automation server (ui_automation.rs) broadcasts every request
      // to ALL webview windows and resolves on the FIRST response with a
      // matching request_id — so exactly one listener must answer any given
      // request. present_window_* actions belong to the present window's OWN
      // bridge (usePresentAutomationBridge); ignore them here without
      // emitting a response so that bridge's answer is the only one.
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
