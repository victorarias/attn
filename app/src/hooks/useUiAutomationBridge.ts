import { useCallback, useEffect } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { isTauri } from '@tauri-apps/api/core';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import type { SessionAgent } from '../types/sessionAgent';
import type { TerminalSplitDirection } from '../types/workspace';
import { SHORTCUTS, type ShortcutId } from '../shortcuts';

const UI_AUTOMATION_REQUEST_EVENT = 'attn://ui-automation/request';
const UI_AUTOMATION_RESPONSE_EVENT = 'attn://ui-automation/response';
const UI_AUTOMATION_READY_EVENT = 'attn://ui-automation/ready';

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
  getActivePaneIdForSession: (session: Session | undefined | null) => string;
  createSession: (label: string, cwd: string, id?: string, agent?: SessionAgent) => Promise<string>;
  selectSession: (sessionId: string) => void;
  closeSession: (sessionId: string) => void;
  splitPane: (sessionId: string, targetPaneId: string, direction: TerminalSplitDirection) => Promise<unknown>;
  closePane: (sessionId: string, paneId: string) => Promise<unknown>;
  focusPane: (sessionId: string, paneId: string) => void;
  typeInSessionPaneViaUI: (sessionId: string, paneId: string, text: string) => boolean;
  isSessionPaneInputFocused: (sessionId: string, paneId: string) => boolean;
  getPaneText: (sessionId: string, paneId: string) => string;
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null;
  fitSessionActivePane: (sessionId: string) => void;
  sendRuntimeInput: (runtimeId: string, data: string, source?: string) => void;
}

function nextAnimationFrame() {
  return new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve()));
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

function serializeSession(session: Session, getActivePaneIdForSession: (session: Session | undefined | null) => string) {
  return {
    id: session.id,
    label: session.label,
    state: session.state,
    cwd: session.cwd,
    agent: session.agent,
    activePaneId: getActivePaneIdForSession(session),
    daemonActivePaneId: session.daemonActivePaneId,
    panes: [
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
    ],
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

function collectVisualSnapshot(
  sessions: Session[],
  activeSessionId: string | null,
  getActivePaneIdForSession: (session: Session | undefined | null) => string,
  getPaneText: (sessionId: string, paneId: string) => string,
  getPaneSize: (sessionId: string, paneId: string) => { cols: number; rows: number } | null,
) {
  return {
    activeSessionId,
    activeElement: {
      tag: document.activeElement?.tagName || null,
      className: (document.activeElement as HTMLElement | null)?.className || null,
      ariaLabel: (document.activeElement as HTMLElement | null)?.getAttribute?.('aria-label') || null,
      text: document.activeElement?.textContent?.slice(0, 120) || '',
    },
    sessions: sessions.map((session) => {
      const activePaneId = getActivePaneIdForSession(session);
      const paneIds = [MAIN_TERMINAL_PANE_ID, ...session.workspace.terminals.map((terminal) => terminal.id)];
      return {
        id: session.id,
        label: session.label,
        activePaneId,
        daemonActivePaneId: session.daemonActivePaneId,
        workspaceBounds: rectSnapshot(
          document.querySelector(`[data-session-terminal-workspace="${session.id}"]`)
        ),
        panes: paneIds.map((paneId) => {
          const paneElement = document.querySelector(
            `[data-pane-session-id="${session.id}"][data-pane-id="${paneId}"]`
          );
          return {
            paneId,
            active: activePaneId === paneId,
            kind: paneId === MAIN_TERMINAL_PANE_ID ? 'main' : 'shell',
            bounds: rectSnapshot(paneElement),
            className: paneElement instanceof HTMLElement ? paneElement.className : null,
            text: getPaneText(session.id, paneId),
            size: getPaneSize(session.id, paneId),
          };
        }),
      };
    }),
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

export function useUiAutomationBridge({
  sessions,
  activeSessionId,
  getActivePaneIdForSession,
  createSession,
  selectSession,
  closeSession,
  splitPane,
  closePane,
  focusPane,
  typeInSessionPaneViaUI,
  isSessionPaneInputFocused,
  getPaneText,
  getPaneSize,
  fitSessionActivePane,
  sendRuntimeInput,
}: UseUiAutomationBridgeArgs) {
  const handleAutomationRequest = useCallback(async (request: AutomationRequest) => {
    const payload = request.payload || {};

    switch (request.action) {
      case 'ping':
        return { pong: true };
      case 'get_state':
        return {
          activeSessionId,
          sessions: sessions.map((session) => serializeSession(session, getActivePaneIdForSession)),
        };
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
        if (!cwd) {
          throw new Error('create_session requires cwd');
        }
        const sessionId = await createSession(label, cwd, undefined, agent);
        await settleUi();
        fitSessionActivePane(sessionId);
        return { sessionId };
      }
      case 'close_session': {
        const sessionId = typeof payload.sessionId === 'string' ? payload.sessionId : '';
        if (!sessionId) {
          throw new Error('close_session requires sessionId');
        }
        closeSession(sessionId);
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
        );
        return {
          sessionId,
          paneId,
          inputFocused: isSessionPaneInputFocused(sessionId, paneId),
          activePaneId: getActivePaneIdForSession(session),
          pane: snapshot.sessions[0]?.panes.find((pane) => pane.paneId === paneId) || null,
        };
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
      case 'capture_structured_snapshot':
        return collectVisualSnapshot(
          sessions,
          activeSessionId,
          getActivePaneIdForSession,
          getPaneText,
          getPaneSize,
        );
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
    typeInSessionPaneViaUI,
    isSessionPaneInputFocused,
    getActivePaneIdForSession,
    getPaneSize,
    getPaneText,
    selectSession,
    sendRuntimeInput,
    sessions,
    splitPane,
  ]);

  useEffect(() => {
    if (!isTauri() || import.meta.env.VITE_UI_AUTOMATION !== '1') {
      return;
    }

    void emit(UI_AUTOMATION_READY_EVENT, { ready: true });
    const unlistenPromise = listen<AutomationRequest>(UI_AUTOMATION_REQUEST_EVENT, async (event) => {
      const request = event.payload;
      let response: AutomationResponse;
      try {
        const result = await handleAutomationRequest(request);
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
  }, [handleAutomationRequest]);
}
