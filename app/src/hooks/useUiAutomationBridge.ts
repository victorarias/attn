import { useCallback, useEffect } from 'react';
import { emit, listen } from '@tauri-apps/api/event';
import { isTauri } from '@tauri-apps/api/core';
import type { Session } from '../store/sessions';
import { MAIN_TERMINAL_PANE_ID } from '../store/sessions';
import type { SessionAgent } from '../types/sessionAgent';
import type { TerminalSplitDirection } from '../types/workspace';
import { SHORTCUTS, type ShortcutId } from '../shortcuts';
import { getTerminalPerfSnapshot } from '../utils/terminalPerf';
import { getReviewPerfSnapshot } from '../utils/reviewPerf';
import { getAllResizeEvents } from '../utils/terminalDebug';
import { clearPtyPerfSnapshot, getPtyPerfSnapshot, recordPtyDecode, recordWsJsonParse } from '../utils/ptyPerf';

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
  resetSessionPaneTerminal: (sessionId: string, paneId: string) => boolean;
  injectSessionPaneBytes: (sessionId: string, paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectSessionPaneBase64: (sessionId: string, paneId: string, payload: string) => Promise<boolean>;
  drainSessionPaneTerminal: (sessionId: string, paneId: string) => Promise<boolean>;
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
  options?: { includeMemory?: boolean },
) {
  const browserMemory = options?.includeMemory === false
    ? {
        performanceMemory: null,
        userAgentSpecificMemory: null,
        userAgentSpecificMemoryError: null,
      }
    : await getBrowserMemorySnapshot();
  const totalPaneCount = sessions.reduce(
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
      count: sessions.length,
      activeSessionId,
      totalPaneCount,
      items: sessions.map((session) => ({
        id: session.id,
        label: session.label,
        state: session.state,
        activePaneId: getActivePaneIdForSession(session),
        shellPaneCount: session.workspace.terminals.length,
      })),
    },
    browserMemory,
    terminals: getTerminalPerfSnapshot(),
    review: getReviewPerfSnapshot(),
    paneDebugEventCount: window.__ATTN_PANE_DEBUG_DUMP?.().length || 0,
    resizeEventCount: getAllResizeEvents().length,
    pty: getPtyPerfSnapshot(),
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
      case 'capture_perf_snapshot': {
        const settleFrames = typeof payload.settleFrames === 'number' ? payload.settleFrames : 2;
        const includeMemory = payload.includeMemory !== false;
        await settleUi(settleFrames);
        return capturePerfSnapshot(
          sessions,
          activeSessionId,
          getActivePaneIdForSession,
          { includeMemory },
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
