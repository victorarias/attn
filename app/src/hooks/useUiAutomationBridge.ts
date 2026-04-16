import { useCallback, useEffect, useRef } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import type { SessionAgent } from '../types/sessionAgent';
import type { TerminalSplitDirection } from '../types/workspace';
import { SHORTCUTS, type ShortcutId } from '../shortcuts';
import { getTerminalPerfSnapshot } from '../utils/terminalPerf';
import { getReviewPerfSnapshot } from '../utils/reviewPerf';
import { getAllResizeEvents } from '../utils/terminalDebug';
import { clearPtyPerfSnapshot, getPtyPerfSnapshot, recordPtyDecode, recordWsJsonParse } from '../utils/ptyPerf';
import { buildSessionRenderHealth } from '../utils/renderHealth';
import { buildRuntimeTimelineSnapshot } from '../utils/runtimeTimeline';
import { collectWorkspaceLayoutDiagnostics, projectWorkspaceBounds } from '../utils/workspaceDiagnostics';
import { clearTerminalRuntimeLog, setTerminalRuntimeTraceEnabled } from '../utils/terminalRuntimeLog';
import type { TerminalVisibleContentSnapshot } from '../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../utils/terminalStyleSummary';

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
  createSession: (label: string, cwd: string, id?: string, agent?: SessionAgent, endpointId?: string) => Promise<string>;
  selectSession: (sessionId: string) => void;
  closeSession: (sessionId: string) => Promise<void>;
  reloadSession?: (sessionId: string, size?: { cols: number; rows: number }) => Promise<void>;
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
  fitSessionActivePane: (sessionId: string) => void;
  sendRuntimeInput: (runtimeId: string, data: string, source?: string) => void;
  getReviewState?: (repoPath: string, branch: string) => Promise<{ success: boolean; state?: unknown; error?: string }>;
  addComment?: (reviewId: string, filepath: string, lineStart: number, lineEnd: number, content: string) => Promise<{ success: boolean; comment?: unknown }>;
  updateComment?: (commentId: string, content: string) => Promise<{ success: boolean }>;
  resolveComment?: (commentId: string, resolved: boolean) => Promise<{ success: boolean }>;
  deleteComment?: (commentId: string) => Promise<{ success: boolean }>;
  getComments?: (reviewId: string, filepath?: string) => Promise<{ success: boolean; comments?: unknown[] }>;
  startReviewLoop?: (prompt: string, iterationLimit: number, presetId?: string) => Promise<void>;
  stopReviewLoop?: () => Promise<void>;
  getReviewLoopState?: (sessionId: string) => Promise<{ success: boolean; state: unknown | null }>;
  answerReviewLoop?: (loopId: string, interactionId: string, answer: string) => Promise<{ success: boolean; state: unknown | null }>;
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
  if (paneId === MAIN_TERMINAL_PANE_ID) {
    return session.id;
  }
  const terminal = session.workspace.terminals.find((entry) => entry.id === paneId);
  if (!terminal?.ptyId) {
    throw new Error(`No runtime found for pane ${paneId}`);
  }
  return terminal.ptyId;
}

function paneEntries(session: Session) {
  return [
    {
      paneId: MAIN_TERMINAL_PANE_ID,
      runtimeId: session.id,
      kind: 'main',
      title: 'Session',
    },
    ...session.workspace.terminals.map((terminal) => ({
      paneId: terminal.id,
      runtimeId: terminal.ptyId,
      kind: 'shell',
      title: terminal.title,
    })),
  ];
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
    shellPaneCount: session.workspace.terminals.length,
  };
}

function serializeSession(session: Session, getActivePaneIdForSession: (session: Session | undefined | null) => string) {
  const workspace = serializeWorkspaceModel(session, getActivePaneIdForSession);
  return {
    id: session.id,
    label: session.label,
    state: session.state,
    cwd: session.cwd,
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
    shellPaneCount: session.workspace.terminals.length,
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

function getSessionWorkspaceRoot(sessionId: string) {
  const root = document.querySelector(`[data-session-terminal-workspace="${sessionId}"]`);
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
  const xterm = paneElement.querySelector('.xterm');
  const xtermScreen = paneElement.querySelector('.xterm-screen');
  const xtermViewport = paneElement.querySelector('.xterm-viewport');
  const scrollArea = paneElement.querySelector('.xterm-scroll-area');
  const canvas = paneElement.querySelector('.xterm-screen canvas');
  const helperTextarea = paneElement.querySelector('textarea.xterm-helper-textarea');

  return {
    paneBody: elementMetrics(paneBody),
    terminalContainer: elementMetrics(terminalContainer),
    xterm: elementMetrics(xterm),
    xtermScreen: elementMetrics(xtermScreen),
    xtermViewport: elementMetrics(xtermViewport),
    scrollArea: elementMetrics(scrollArea),
    canvas: elementMetrics(canvas),
    helperTextarea: helperTextarea instanceof HTMLTextAreaElement
      ? {
          ...elementMetrics(helperTextarea),
          focused: document.activeElement === helperTextarea,
          disabled: helperTextarea.disabled,
          readOnly: helperTextarea.readOnly,
          valueLength: helperTextarea.value.length,
        }
      : null,
  };
}

async function captureDomScreenshotData() {
  const target = document.getElementById('root') || document.body;
  if (!(target instanceof HTMLElement)) {
    throw new Error('Screenshot target not found');
  }

  const { toPng } = await import('html-to-image');
  const backgroundColor = getComputedStyle(document.body).backgroundColor || '#111111';
  const dataUrl = await toPng(target, {
    cacheBust: true,
    pixelRatio: 1,
    backgroundColor,
  });
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
      const workspaceDom = collectWorkspaceShellMetrics(session.id);
      const workspaceView = collectWorkspaceViewState(session.id);
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
          splits: collectSplitDomMetrics(session.id),
        },
        sidebarItem: sidebarItem instanceof HTMLElement
          ? {
              text: sidebarItem.textContent || '',
              bounds: rectSnapshot(sidebarItem),
            }
          : null,
        workspaceBounds: workspaceDom.workspaceRoot?.bounds ?? null,
        panes: paneIds.map((paneId) => {
          const paneElement = document.querySelector(
            `[data-pane-session-id="${session.id}"][data-pane-id="${paneId}"]`
          );
          const modelLayout = paneLayoutById.get(paneId) ?? null;
          return {
            paneId,
            active: activePaneId === paneId,
            kind: paneId === MAIN_TERMINAL_PANE_ID ? 'main' : 'shell',
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
      mainPaneBounds: null,
      activePaneId: null,
      daemonActivePaneId: null,
      label: null,
      cwd: null,
    };
  }

  const sidebarItem = document.querySelector(
    `[data-testid="sidebar-session-${session.id}"]`
  );
  const mainPane = document.querySelector(
    `[data-pane-session-id="${session.id}"][data-pane-id="${MAIN_TERMINAL_PANE_ID}"]`
  );
  const workspaceDom = collectWorkspaceShellMetrics(session.id);
  const workspaceView = collectWorkspaceViewState(session.id);
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
      splits: collectSplitDomMetrics(session.id),
    },
    mainPaneBounds: rectSnapshot(mainPane),
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
    {
      includePaneText: false,
      sessionIds: options?.sessionIds || null,
    },
  );
  const terminalPerf = getTerminalPerfSnapshot();
  const filteredSessions = visualSnapshot.sessions || [];
  const terminalNames = new Set<string>();

  const sessionHealth = filteredSessions.map((session) => {
    const terminalsByPaneId = new Map(
      terminalPerf
        .filter((terminal) => terminal.sessionId === session.id)
        .map((terminal) => {
          terminalNames.add(terminal.terminalName);
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
          kind: pane.kind as 'main' | 'shell',
          active: pane.active,
          inputFocused: isSessionPaneInputFocused(session.id, pane.paneId),
          size: pane.size,
          paneBounds: boxFromRect(pane.bounds),
          projectedBounds: boxFromRect(pane.layout?.projectedBounds),
          paneBodyBounds: boxFromRect(pane.dom?.paneBody?.bounds),
          terminalContainerBounds: boxFromRect(pane.dom?.terminalContainer?.bounds),
          xtermScreenBounds: boxFromRect(pane.dom?.xtermScreen?.bounds),
          canvasBounds: boxFromRect(pane.dom?.canvas?.bounds),
          helperTextarea: pane.dom?.helperTextarea
            ? {
                focused: pane.dom.helperTextarea.focused,
                disabled: pane.dom.helperTextarea.disabled,
                readOnly: pane.dom.helperTextarea.readOnly,
                width: pane.dom.helperTextarea.bounds?.width ?? null,
                height: pane.dom.helperTextarea.bounds?.height ?? null,
              }
            : null,
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

  const resizeEvents = getAllResizeEvents().filter((event) => terminalNames.has(event.terminalName));

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
    resizeEvents,
  };
}

function collectSessionRuntimeIds(sessions: Session[]) {
  const runtimeIds = new Set<string>();
  for (const session of sessions) {
    runtimeIds.add(session.id);
    for (const terminal of session.workspace.terminals) {
      if (terminal.ptyId) {
        runtimeIds.add(terminal.ptyId);
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

function dispatchShortcutEvent(shortcutId: ShortcutId) {
  const shortcut = SHORTCUTS[shortcutId];
  if (!shortcut) {
    throw new Error(`Unknown shortcut: ${shortcutId}`);
  }
  const shortcutDef = shortcut as {
    key: string;
    meta?: boolean;
    ctrl?: boolean;
    alt?: boolean;
    shift?: boolean;
  };

  const event = new KeyboardEvent('keydown', {
    key: shortcutDef.key,
    metaKey: !!shortcutDef.meta,
    ctrlKey: !!shortcutDef.ctrl,
    altKey: !!shortcutDef.alt,
    shiftKey: !!shortcutDef.shift,
    bubbles: true,
    cancelable: true,
  });
  window.dispatchEvent(event);
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
      targets: [],
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
    targets: targetButtons,
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
  const terminalNames = new Set(terminals.map((terminal) => terminal.terminalName));
  const resizeEvents = terminalNames.size > 0
    ? getAllResizeEvents().filter((event) => terminalNames.has(event.terminalName))
    : getAllResizeEvents();
  const ptySnapshot = getPtyPerfSnapshot();
  const terminalRuntimeTrace = window.__ATTN_TERMINAL_RUNTIME_DUMP?.() || [];
  const browserMemory = options?.includeMemory === false
    ? {
        performanceMemory: null,
        userAgentSpecificMemory: null,
        userAgentSpecificMemoryError: null,
      }
    : await getBrowserMemorySnapshot();
  const totalPaneCount = scopedSessions.reduce(
    (sum, session) => sum + 1 + session.workspace.terminals.length,
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
      xtermCount: document.querySelectorAll('.xterm').length,
      terminalContainerCount: document.querySelectorAll('.terminal-container').length,
      codeMirrorCount: document.querySelectorAll('.cm-editor').length,
      unifiedDiffEditorCount: document.querySelectorAll('.unified-diff-editor').length,
      diffDetailOpen: document.querySelectorAll('.dock-panel--diff-detail, .dock-panel--diffDetail').length > 0,
      diffPanelOpen: document.querySelectorAll('.dock-panel--diff').length > 0,
      reviewLoopOpen: document.querySelectorAll('.dock-panel--review-loop, .dock-panel--reviewLoop').length > 0,
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
        shellPaneCount: session.workspace.terminals.length,
      })),
    },
    browserMemory,
    terminals,
    terminalRuntimeTraceEventCount: terminalRuntimeTrace.length,
    review: getReviewPerfSnapshot(),
    paneDebugEventCount: window.__ATTN_PANE_DEBUG_DUMP?.().length || 0,
    resizeEventCount: resizeEvents.length,
    resizeEvents,
    pty: ptySnapshot,
    ptyFocus: summarizePtyRecentTraffic(
      ptySnapshot.recentEvents,
      scopedRuntimeIds.size > 0 ? scopedRuntimeIds : null,
    ),
    runtimeTimeline: buildRuntimeTimelineSnapshot({
      events: terminalRuntimeTrace,
      terminals,
      pty: ptySnapshot,
      runtimeIds: scopedRuntimeIds.size > 0 ? scopedRuntimeIds : null,
    }),
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

function collectReviewLoopUiState(sessionId: string) {
  const requestedDrawer = document.querySelector(`[data-testid="review-loop-drawer-${sessionId}"]`);
  const fallbackDrawer = document.querySelector('.review-loop-drawer-panel');
  const drawer = requestedDrawer || fallbackDrawer;
  const reviewLoopPanel = document.querySelector('.dock-panel--review-loop, .dock-panel--reviewLoop');
  const summary = drawer?.querySelector('.review-loop-panel-card--summary .review-loop-panel-content');
  const question = drawer?.querySelector('.review-loop-question-text');
  const status = drawer?.querySelector('.review-loop-drawer-status');
  const subtitle = drawer?.querySelector('.review-loop-drawer-subtitle');
  const files = Array.from(drawer?.querySelectorAll('.review-loop-file-list li, .review-loop-change-path') || [])
    .map((node) => node.textContent?.trim() || '')
    .filter(Boolean);

  return {
    sessionId,
    open: Boolean(reviewLoopPanel && drawer),
    drawerBounds: rectSnapshot(drawer || null),
    panelBounds: rectSnapshot(reviewLoopPanel || null),
    drawerTestId: drawer instanceof HTMLElement ? drawer.dataset.testid || drawer.getAttribute('data-testid') || '' : '',
    statusText: status?.textContent?.trim() || '',
    subtitleText: subtitle?.textContent?.trim() || '',
    summaryText: summary?.textContent?.trim() || '',
    questionText: question?.textContent?.trim() || '',
    answerVisible: Boolean(drawer?.querySelector('.review-loop-answer-box')),
    files,
  };
}

export function useUiAutomationBridge({
  sessions,
  activeSessionId,
  daemonReady = true,
  connectionError = null,
  getActivePaneIdForSession,
  createSession,
  selectSession,
  closeSession,
  reloadSession,
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
  fitSessionActivePane,
  sendRuntimeInput,
  getReviewState,
  addComment,
  updateComment,
  resolveComment,
  deleteComment,
  getComments,
  startReviewLoop,
  stopReviewLoop,
  getReviewLoopState,
  answerReviewLoop,
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
          sessions: sessions.map((session) => serializeSession(session, getActivePaneIdForSession)),
        };
      case 'capture_screenshot_data':
        return captureDomScreenshotData();
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
        if (!cwd) {
          throw new Error('create_session requires cwd');
        }
        const sessionId = await createSession(label, cwd, providedSessionId, agent, endpointId);
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
      case 'reload_session': {
        if (!reloadSession) {
          throw new Error('reload_session is not configured');
        }
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('reload_session requires sessionId');
        }
        const size = getPaneSize(sessionId, MAIN_TERMINAL_PANE_ID) || undefined;
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
        selectSession(sessionId);
        focusPane(sessionId, paneId);
        await settleUi();
        return { sessionId, paneId };
      }
      case 'click_pane': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        selectSession(sessionId);
        await settleUi(1);
        clickPaneElement(sessionId, paneId);
        await settleUi(2);
        return { sessionId, paneId };
      }
      case 'scroll_pane_to_top': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        selectSession(sessionId);
        await settleUi(1);
        const success = scrollSessionPaneToTop(sessionId, paneId);
        if (!success) {
          throw new Error(`Failed to scroll pane ${paneId} to top`);
        }
        await settleUi(1);
        return { sessionId, paneId };
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
        const success = typeInSessionPaneViaUI(sessionId, paneId, text);
        if (!success) {
          throw new Error(`Failed to type into pane ${paneId} via UI input`);
        }
        return { sessionId, paneId };
      }
      case 'read_pane_text': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        return {
          sessionId,
          paneId,
          text: getPaneText(sessionId, paneId),
          size: getPaneSize(sessionId, paneId),
        };
      }
      case 'read_pane_style': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        const session = sessions.find((entry) => entry.id === sessionId);
        if (!session) {
          throw new Error('Session not found');
        }
        const paneId = resolvePaneId(session, getActivePaneIdForSession, payload.paneId);
        return {
          sessionId,
          paneId,
          size: getPaneSize(sessionId, paneId),
          style: getPaneVisibleStyleSummary(sessionId, paneId),
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
        );
        return {
          sessionId,
          paneId,
          inputFocused: isSessionPaneInputFocused(sessionId, paneId),
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
          ).sessions[0]?.panes.find((pane) => pane.paneId === paneId) || null,
        };
      }
      case 'review_get_state': {
        if (!getReviewState) {
          throw new Error('review_get_state is not configured');
        }
        const repoPath = typeof payload.repoPath === 'string' ? payload.repoPath : '';
        const branch = typeof payload.branch === 'string' ? payload.branch : '';
        if (!repoPath || !branch) {
          throw new Error('review_get_state requires repoPath and branch');
        }
        return getReviewState(repoPath, branch);
      }
      case 'review_get_comments': {
        if (!getComments) {
          throw new Error('review_get_comments is not configured');
        }
        const reviewId = typeof payload.reviewId === 'string' ? payload.reviewId : '';
        const filepath = typeof payload.filepath === 'string' && payload.filepath.length > 0
          ? payload.filepath
          : undefined;
        if (!reviewId) {
          throw new Error('review_get_comments requires reviewId');
        }
        return getComments(reviewId, filepath);
      }
      case 'review_add_comment': {
        if (!addComment) {
          throw new Error('review_add_comment is not configured');
        }
        const reviewId = typeof payload.reviewId === 'string' ? payload.reviewId : '';
        const filepath = typeof payload.filepath === 'string' ? payload.filepath : '';
        const lineStart = typeof payload.lineStart === 'number' ? payload.lineStart : NaN;
        const lineEnd = typeof payload.lineEnd === 'number' ? payload.lineEnd : NaN;
        const content = typeof payload.content === 'string' ? payload.content : '';
        if (!reviewId || !filepath || !Number.isFinite(lineStart) || !Number.isFinite(lineEnd) || !content) {
          throw new Error('review_add_comment requires reviewId, filepath, lineStart, lineEnd, and content');
        }
        return addComment(reviewId, filepath, lineStart, lineEnd, content);
      }
      case 'review_update_comment': {
        if (!updateComment) {
          throw new Error('review_update_comment is not configured');
        }
        const commentId = typeof payload.commentId === 'string' ? payload.commentId : '';
        const content = typeof payload.content === 'string' ? payload.content : '';
        if (!commentId || !content) {
          throw new Error('review_update_comment requires commentId and content');
        }
        return updateComment(commentId, content);
      }
      case 'review_resolve_comment': {
        if (!resolveComment) {
          throw new Error('review_resolve_comment is not configured');
        }
        const commentId = typeof payload.commentId === 'string' ? payload.commentId : '';
        const resolved = payload.resolved !== false;
        if (!commentId) {
          throw new Error('review_resolve_comment requires commentId');
        }
        return resolveComment(commentId, resolved);
      }
      case 'review_delete_comment': {
        if (!deleteComment) {
          throw new Error('review_delete_comment is not configured');
        }
        const commentId = typeof payload.commentId === 'string' ? payload.commentId : '';
        if (!commentId) {
          throw new Error('review_delete_comment requires commentId');
        }
        return deleteComment(commentId);
      }
      case 'review_loop_start': {
        if (!startReviewLoop) {
          throw new Error('review_loop_start is not configured');
        }
        const prompt = typeof payload.prompt === 'string' ? payload.prompt : '';
        const iterationLimit = typeof payload.iterationLimit === 'number' ? payload.iterationLimit : 1;
        const presetId = typeof payload.presetId === 'string' ? payload.presetId : undefined;
        if (!prompt) {
          throw new Error('review_loop_start requires prompt');
        }
        await startReviewLoop(prompt, iterationLimit, presetId);
        return { ok: true };
      }
      case 'review_loop_stop': {
        if (!stopReviewLoop) {
          throw new Error('review_loop_stop is not configured');
        }
        await stopReviewLoop();
        return { ok: true };
      }
      case 'review_loop_get_state': {
        if (!getReviewLoopState) {
          throw new Error('review_loop_get_state is not configured');
        }
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : activeSessionId;
        if (!sessionId) {
          throw new Error('review_loop_get_state requires sessionId');
        }
        return getReviewLoopState(sessionId);
      }
      case 'review_loop_answer': {
        if (!answerReviewLoop) {
          throw new Error('review_loop_answer is not configured');
        }
        const loopId = typeof payload.loopId === 'string' ? payload.loopId : '';
        const interactionId = typeof payload.interactionId === 'string' ? payload.interactionId : '';
        const answer = typeof payload.answer === 'string' ? payload.answer : '';
        if (!loopId || !interactionId || !answer.trim()) {
          throw new Error('review_loop_answer requires loopId, interactionId, and answer');
        }
        return answerReviewLoop(loopId, interactionId, answer.trim());
      }
      case 'review_loop_ui_state': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : activeSessionId;
        if (!sessionId) {
          throw new Error('review_loop_ui_state requires sessionId');
        }
        return collectReviewLoopUiState(sessionId);
      }
      case 'set_pane_debug': {
        const enabled = payload.enabled !== false;
        window.__ATTN_PANE_DEBUG_ENABLE?.(enabled);
        if (enabled) {
          window.__ATTN_PANE_DEBUG_CLEAR?.();
        }
        return { enabled, file: window.__ATTN_PANE_DEBUG_FILE || null };
      }
      case 'dump_pane_debug':
        return {
          file: window.__ATTN_PANE_DEBUG_FILE || null,
          state: window.__ATTN_PANE_DEBUG_STATE?.() || null,
          events: window.__ATTN_PANE_DEBUG_DUMP?.() || [],
        };
      case 'set_terminal_runtime_trace': {
        const enabled = payload.enabled !== false;
        setTerminalRuntimeTraceEnabled(enabled);
        if (enabled) {
          window.__ATTN_TERMINAL_RUNTIME_CLEAR?.();
        } else {
          clearTerminalRuntimeLog();
        }
        return {
          enabled,
          file: window.__ATTN_TERMINAL_RUNTIME_FILE || null,
        };
      }
      case 'dump_terminal_runtime_trace':
        return {
          file: window.__ATTN_TERMINAL_RUNTIME_FILE || null,
          events: window.__ATTN_TERMINAL_RUNTIME_DUMP?.() || [],
        };
      case 'capture_structured_snapshot':
        return collectVisualSnapshot(
          sessions,
          activeSessionId,
          getActivePaneIdForSession,
          getPaneText,
          getPaneSize,
          getPaneVisibleContent,
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
        const runtimeId = paneId === MAIN_TERMINAL_PANE_ID
          ? session.id
          : session.workspace.terminals.find((entry) => entry.id === paneId)?.ptyId || `bench:${paneId}`;
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
    resetSessionPaneTerminal,
    drainSessionPaneTerminal,
    selectSession,
    sendRuntimeInput,
    sessions,
    splitPane,
  ]);

  const handleAutomationRequestRef = useRef(handleAutomationRequest);
  handleAutomationRequestRef.current = handleAutomationRequest;

  useEffect(() => {
    if (!isTauri() || import.meta.env.VITE_UI_AUTOMATION !== '1') {
      return;
    }

    void emit(UI_AUTOMATION_READY_EVENT, { ready: true });
    const unlistenPromise = listen<AutomationRequest>(UI_AUTOMATION_REQUEST_EVENT, async (event) => {
      const request = event.payload;
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
