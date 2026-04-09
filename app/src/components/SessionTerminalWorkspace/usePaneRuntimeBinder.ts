import { useCallback, useEffect, useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { ptyAttach, ptyResize, ptySpawn, ptyWrite, type PtyEventPayload, type PtySpawnArgs } from '../../pty/bridge';
import {
  isCommittedGeometryChanged,
  planDeferredGeometryRedraw,
  planGeometryFlush,
  planPendingGeometrySync,
  type PendingGeometrySync,
} from '../../pty/geometryLifecycle';
import { triggerShortcut } from '../../shortcuts/useShortcut';
import { isMacLikePlatform } from '../../shortcuts/platform';
import type { TerminalHandle } from '../Terminal';
import { activeElementSummary, recordPaneRuntimeDebugEvent } from '../../utils/paneRuntimeDebug';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import { recordPtyDecode } from '../../utils/ptyPerf';
import { resetTerminalScrollPin } from '../../utils/terminalScrollPin';
import { recordTerminalRuntimeLog } from '../../utils/terminalRuntimeLog';
import {
  focusTerminalViewport,
  resetTerminalViewport,
  scrollTerminalViewportToTop,
} from '../../utils/terminalViewportActions';
import { snapshotVisibleTerminalContent, type TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';

const MAX_PENDING_TERMINAL_EVENTS = 256;
const RUNTIME_ENSURE_TIMEOUT_MS = 20_000;
const RUNTIME_ENSURE_RETRY_DELAY_MS = 150;
const PTY_GEOMETRY_SETTLE_MS = 90;
const PTY_READY_SETTLE_MS = 60;
const PTY_REDRAW_BOUNCE_DELAY_MS = 16;
const PTY_REDRAW_SETTLE_MS = 120;
const terminalTextEncoder = new TextEncoder();

interface PaneRuntimeSize {
  cols: number;
  rows: number;
}

export interface PaneRuntimeSpec {
  paneId: string;
  runtimeId: string;
  sessionId?: string;
  testSessionId?: string;
  getSpawnArgs: (size: PaneRuntimeSize) => PtySpawnArgs | null;
}

export interface PaneRuntimeBinder {
  setTerminalHandle: (paneId: string, handle: TerminalHandle | null) => void;
  handleTerminalInit: (paneId: string) => (xterm: XTerm) => void;
  handleTerminalReady: (paneId: string) => (xterm: XTerm) => void;
  handleTerminalResize: (paneId: string) => (cols: number, rows: number, options?: { forceRedraw?: boolean; reason?: string }) => void;
  focusPaneWithRetry: (paneId: string, retries?: number) => void;
  fitPane: (paneId: string) => void;
  fitActivePane: () => void;
  typeTextViaPaneInput: (paneId: string, text: string) => boolean;
  isPaneInputFocused: (paneId: string) => boolean;
  scrollPaneToTop: (paneId: string) => boolean;
  getPaneText: (paneId: string) => string;
  getPaneSize: (paneId: string) => PaneRuntimeSize | null;
  getPaneVisibleContent: (paneId: string) => TerminalVisibleContentSnapshot;
  resetPaneTerminal: (paneId: string) => boolean;
  injectPaneBytes: (paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectPaneBase64: (paneId: string, payload: string) => Promise<boolean>;
  drainPaneTerminal: (paneId: string) => Promise<boolean>;
}

interface PaneWriteState {
  writeChain: Promise<void>;
  lastLoggedAt: number;
  writeCount: number;
  bytes: number;
  totalWriteCount: number;
  lastWriteAt: number;
  lastSeq?: number;
}

function decodePtyBytes(payload: string): Uint8Array {
  const startedAt = performance.now();
  const binaryStr = atob(payload);
  const bytes = Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
  recordPtyDecode(bytes.length, performance.now() - startedAt);
  return bytes;
}

function encodePtyBytes(bytes: Uint8Array): string {
  let binary = '';
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    const chunk = bytes.subarray(offset, Math.min(offset + chunkSize, bytes.length));
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

function snapshotTerminalText(terminal: XTerm | null): string {
  const buffer = terminal?.buffer.active;
  if (!buffer) {
    return '';
  }

  const lines: string[] = [];
  for (let i = 0; i < buffer.length; i++) {
    const line = buffer.getLine(i);
    if (line) {
      lines.push(line.translateToString(true));
    }
  }
  return lines.join('\n');
}

function runtimeEnsureErrorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

function isRetryableRuntimeEnsureError(error: unknown): boolean {
  const message = runtimeEnsureErrorMessage(error).toLowerCase();
  return (
    message.includes('pty backend is not configured') ||
    message.includes('websocket not connected') ||
    message.includes('attach session timed out') ||
    message.includes('spawn session timed out')
  );
}

function waitForRuntimeEnsureRetry(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

export function installTerminalKeyHandler(sendToPty: (data: string) => void) {
  return (ev: KeyboardEvent) => {
    const accel = isMacLikePlatform() ? ev.metaKey : (ev.metaKey || ev.ctrlKey);
    if (ev.type === 'keydown' && accel && !ev.altKey) {
      if (!ev.shiftKey && ev.key.toLowerCase() === 't') {
        return !triggerShortcut('terminal.new');
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitVertical');
      }
      if (ev.shiftKey && ev.key.toLowerCase() === 'd') {
        return !triggerShortcut('terminal.splitHorizontal');
      }
      if (ev.shiftKey && ev.key.toLowerCase() === 'z') {
        return !triggerShortcut('terminal.toggleZoom');
      }
      if (ev.shiftKey && ev.key === 'Enter') {
        return !triggerShortcut('terminal.toggleMaximize');
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 'w') {
        triggerShortcut('terminal.close');
        return false;
      }
    }
    if (ev.key === 'Enter' && ev.shiftKey && !ev.ctrlKey && !ev.altKey) {
      if (ev.type === 'keydown') {
        sendToPty('\n');
      }
      return false;
    }
    return true;
  };
}

export function usePaneRuntimeBinder(
  panes: PaneRuntimeSpec[],
  activePaneId: string,
  eventRouter: PaneRuntimeEventRouter,
): PaneRuntimeBinder {
  const paneByIdRef = useRef<Map<string, PaneRuntimeSpec>>(new Map());
  // Always-current snapshot of panes for use in child effects (which run
  // before parent effects that update paneByIdRef).  Setting a ref during
  // render is safe — it's just a property assignment, no allocation or
  // side effects.
  const panesRef = useRef(panes);
  panesRef.current = panes;
  const terminalHandlesRef = useRef<Map<string, TerminalHandle>>(new Map());
  const xtermsRef = useRef<Map<string, XTerm>>(new Map());
  const inputSubscriptionsRef = useRef<Map<string, { dispose: () => void }>>(new Map());
  const pendingTerminalEventsRef = useRef<Map<string, PtyEventPayload[]>>(new Map());
  const pendingEnsuresRef = useRef<Map<string, Promise<void>>>(new Map());
  const ensuredRuntimeIdsRef = useRef<Set<string>>(new Set());
  const cachedSpawnArgsRef = useRef<Map<string, PtySpawnArgs | null>>(new Map());
  const pendingUnmountCleanupRef = useRef<Map<string, number>>(new Map());
  const runtimeBindingDisposersRef = useRef<Map<string, { paneId: string; testSessionId?: string; dispose: () => void }>>(new Map());
  const paneWriteStatesRef = useRef<Map<string, PaneWriteState>>(new Map());
  const pendingGeometrySyncRef = useRef<Map<string, PendingGeometrySync<XTerm>>>(new Map());
  const pendingGeometryTimersRef = useRef<Map<string, number>>(new Map());
  const lastCommittedGeometryRef = useRef<Map<string, PaneRuntimeSize>>(new Map());
  const pendingRuntimeHydrationRef = useRef<Map<string, { xterm: XTerm; reason: string }>>(new Map());
  const remountHydrationArmedPaneIdsRef = useRef<Set<string>>(new Set());

  const getCurrentPane = useCallback((paneId: string): PaneRuntimeSpec | undefined => {
    return paneByIdRef.current.get(paneId) || panesRef.current.find((pane) => pane.paneId === paneId);
  }, []);

  const appendPendingTerminalEvent = useCallback((paneId: string, event: PtyEventPayload) => {
    const events = pendingTerminalEventsRef.current.get(paneId) || [];
    if (events.length >= MAX_PENDING_TERMINAL_EVENTS) {
      events.shift();
    }
    events.push(event);
    pendingTerminalEventsRef.current.set(paneId, events);
  }, []);

  const getPaneWriteState = useCallback((paneId: string): PaneWriteState => {
    let state = paneWriteStatesRef.current.get(paneId);
    if (!state) {
      const now = performance.now();
      state = {
        writeChain: Promise.resolve(),
        lastLoggedAt: now,
        writeCount: 0,
        bytes: 0,
        totalWriteCount: 0,
        lastWriteAt: 0,
      };
      paneWriteStatesRef.current.set(paneId, state);
    }
    return state;
  }, []);

  const queuePaneWriteTask = useCallback((
    paneId: string,
    task: () => Promise<void> | void,
  ) => {
    const state = getPaneWriteState(paneId);
    const next = state.writeChain.then(
      () => task(),
      () => task(),
    );
    state.writeChain = next.catch((error) => {
      console.error('[PaneRuntimeBinder] Terminal write task failed:', error);
    });
    return next;
  }, [getPaneWriteState]);

  const queueBufferedBytesForReplay = useCallback((paneId: string, bytes: Uint8Array, seq?: number) => {
    const pane = getCurrentPane(paneId);
    if (!pane || bytes.length === 0) {
      return;
    }
    recordTerminalRuntimeLog({
      category: 'binding',
      sessionId: pane.sessionId ?? pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'defer terminal bytes to replay queue',
      details: {
        bytes: bytes.length,
        seq: typeof seq === 'number' ? seq : null,
      },
    });
    appendPendingTerminalEvent(paneId, {
      event: 'data',
      id: pane.runtimeId,
      data: encodePtyBytes(bytes),
      ...(typeof seq === 'number' ? { seq } : {}),
    });
  }, [appendPendingTerminalEvent, getCurrentPane]);

  const recordPaneWriteActivity = useCallback((paneId: string, bytes: number, seq?: number) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }
    const now = performance.now();
    const state = getPaneWriteState(paneId);
    state.writeCount += 1;
    state.bytes += bytes;
    state.totalWriteCount += 1;
    state.lastWriteAt = now;
    if (typeof seq === 'number') {
      state.lastSeq = seq;
    }
    if (now - state.lastLoggedAt < 2000) {
      return;
    }
    recordTerminalRuntimeLog({
      category: 'activity',
      sessionId: pane.sessionId ?? pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'terminal write pipeline heartbeat',
      details: {
        writeCount: state.writeCount,
        bytes: state.bytes,
        lastSeq: state.lastSeq ?? null,
      },
    });
    state.lastLoggedAt = now;
    state.writeCount = 0;
    state.bytes = 0;
  }, [getCurrentPane]);

  const primeSpawnArgsForSize = useCallback((paneId: string, cols: number, rows: number) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }
    cachedSpawnArgsRef.current.set(paneId, pane.getSpawnArgs({ cols, rows }));
  }, [getCurrentPane]);

  const writeToTerminal = useCallback((xterm: XTerm, data: string | Uint8Array): Promise<void> => {
    return new Promise((resolve) => {
      xterm.write(data, resolve);
    });
  }, []);

  const enqueuePaneBytes = useCallback((paneId: string, xterm: XTerm, bytes: Uint8Array, seq?: number) => {
    void queuePaneWriteTask(paneId, async () => {
      const liveXterm = xtermsRef.current.get(paneId);
      if (!liveXterm || liveXterm !== xterm) {
        queueBufferedBytesForReplay(paneId, bytes, seq);
        return;
      }
      await writeToTerminal(liveXterm, bytes);
      recordPaneWriteActivity(paneId, bytes.length, seq);
    });
  }, [queueBufferedBytesForReplay, queuePaneWriteTask, recordPaneWriteActivity, writeToTerminal]);

  const replayPendingTerminalEvent = useCallback((paneId: string, xterm: XTerm, event: PtyEventPayload) => {
    switch (event.event) {
      case 'data':
        enqueuePaneBytes(paneId, xterm, decodePtyBytes(event.data), event.seq);
        break;
      case 'reset':
        void queuePaneWriteTask(paneId, async () => {
          const liveXterm = xtermsRef.current.get(paneId);
          if (!liveXterm || liveXterm !== xterm) {
            appendPendingTerminalEvent(paneId, event);
            return;
          }
          await writeToTerminal(liveXterm, new Uint8Array(0));
          resetTerminalScrollPin(liveXterm);
          liveXterm.reset();
        });
        break;
      case 'exit': {
        const exitBytes = terminalTextEncoder.encode(`\r\n[Process exited with code ${event.code}]\r\n`);
        void queuePaneWriteTask(paneId, async () => {
          const liveXterm = xtermsRef.current.get(paneId);
          if (!liveXterm || liveXterm !== xterm) {
            appendPendingTerminalEvent(paneId, event);
            return;
          }
          await writeToTerminal(liveXterm, exitBytes);
        });
        break;
      }
      case 'error': {
        const errorBytes = terminalTextEncoder.encode(`\r\n[Error: ${event.error}]\r\n`);
        void queuePaneWriteTask(paneId, async () => {
          const liveXterm = xtermsRef.current.get(paneId);
          if (!liveXterm || liveXterm !== xterm) {
            appendPendingTerminalEvent(paneId, event);
            return;
          }
          await writeToTerminal(liveXterm, errorBytes);
        });
        break;
      }
      default:
        break;
    }
  }, [appendPendingTerminalEvent, enqueuePaneBytes, queuePaneWriteTask, writeToTerminal]);

  useEffect(() => {
    paneByIdRef.current = new Map(panes.map((pane) => [pane.paneId, pane]));
    const activeRuntimeIDs = new Set(panes.map((pane) => pane.runtimeId));
    for (const runtimeId of Array.from(ensuredRuntimeIdsRef.current)) {
      if (!activeRuntimeIDs.has(runtimeId)) {
        ensuredRuntimeIdsRef.current.delete(runtimeId);
      }
    }
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      paneId: activePaneId,
      message: 'sync panes',
      details: {
        paneIds: panes.map((pane) => pane.paneId),
        runtimeIds: panes.map((pane) => pane.runtimeId),
      },
    });

    const activePaneIds = new Set(panes.map((pane) => pane.paneId));
    for (const [paneId, sub] of inputSubscriptionsRef.current.entries()) {
      if (activePaneIds.has(paneId)) {
        continue;
      }
      sub.dispose();
      inputSubscriptionsRef.current.delete(paneId);
    }
    for (const paneId of Array.from(terminalHandlesRef.current.keys())) {
      if (!activePaneIds.has(paneId)) {
        terminalHandlesRef.current.delete(paneId);
      }
    }
    for (const paneId of Array.from(xtermsRef.current.keys())) {
      if (!activePaneIds.has(paneId)) {
        xtermsRef.current.delete(paneId);
      }
    }
    for (const [paneId, timeoutId] of pendingUnmountCleanupRef.current.entries()) {
      if (activePaneIds.has(paneId)) {
        continue;
      }
      window.clearTimeout(timeoutId);
      pendingUnmountCleanupRef.current.delete(paneId);
    }
    for (const paneId of Array.from(pendingTerminalEventsRef.current.keys())) {
      if (!activePaneIds.has(paneId)) {
        pendingTerminalEventsRef.current.delete(paneId);
      }
    }
    for (const paneId of Array.from(cachedSpawnArgsRef.current.keys())) {
      if (!activePaneIds.has(paneId)) {
        cachedSpawnArgsRef.current.delete(paneId);
      }
    }
    for (const [paneId, timeoutId] of pendingGeometryTimersRef.current.entries()) {
      if (activePaneIds.has(paneId)) {
        continue;
      }
      window.clearTimeout(timeoutId);
      pendingGeometryTimersRef.current.delete(paneId);
      pendingGeometrySyncRef.current.delete(paneId);
      lastCommittedGeometryRef.current.delete(paneId);
      pendingRuntimeHydrationRef.current.delete(paneId);
    }
    for (const paneId of Array.from(paneWriteStatesRef.current.keys())) {
      if (activePaneIds.has(paneId)) {
        continue;
      }
      paneWriteStatesRef.current.delete(paneId);
    }
  }, [panes]);

  useEffect(() => {
    const desiredRuntimeIds = new Set(panes.map((pane) => pane.runtimeId));

    for (const [runtimeId, registration] of runtimeBindingDisposersRef.current.entries()) {
      if (desiredRuntimeIds.has(runtimeId)) {
        continue;
      }
      registration.dispose();
      runtimeBindingDisposersRef.current.delete(runtimeId);
      recordTerminalRuntimeLog({
        category: 'binding',
        sessionId: registration.testSessionId,
        paneId: registration.paneId,
        runtimeId,
        message: 'dispose stale runtime binding',
      });
    }

    for (const pane of panes) {
      const existing = runtimeBindingDisposersRef.current.get(pane.runtimeId);
      if (existing && existing.paneId === pane.paneId && existing.testSessionId === pane.testSessionId) {
        continue;
      }
      existing?.dispose();

      const dispose = eventRouter.registerBinding({
        sessionId: pane.testSessionId,
        paneId: pane.paneId,
        runtimeId: pane.runtimeId,
        onEvent: (msg) => {
          const xterm = xtermsRef.current.get(pane.paneId);
          if (!xterm) {
            recordPaneRuntimeDebugEvent({
              scope: 'pty',
              sessionId: pane.testSessionId,
              paneId: pane.paneId,
              runtimeId: pane.runtimeId,
              message: 'queue pty event for unmounted pane',
              details: { event: msg.event },
            });
            appendPendingTerminalEvent(pane.paneId, msg);
            return;
          }

          recordPaneRuntimeDebugEvent({
            scope: 'pty',
            sessionId: pane.testSessionId,
            paneId: pane.paneId,
            runtimeId: pane.runtimeId,
            message: 'deliver pty event to pane',
            details: { event: msg.event },
          });
          replayPendingTerminalEvent(pane.paneId, xterm, msg);
        },
      });

      runtimeBindingDisposersRef.current.set(pane.runtimeId, {
        paneId: pane.paneId,
        testSessionId: pane.testSessionId,
        dispose,
      });
      recordTerminalRuntimeLog({
        category: 'binding',
        sessionId: pane.sessionId ?? pane.testSessionId,
        paneId: pane.paneId,
        runtimeId: pane.runtimeId,
        message: 'register pane runtime binding',
      });
    }

  }, [appendPendingTerminalEvent, eventRouter, panes, replayPendingTerminalEvent]);

  useEffect(() => {
    return () => {
      for (const timeoutId of pendingGeometryTimersRef.current.values()) {
        window.clearTimeout(timeoutId);
      }
      pendingGeometryTimersRef.current.clear();
      pendingGeometrySyncRef.current.clear();
      lastCommittedGeometryRef.current.clear();
      pendingRuntimeHydrationRef.current.clear();
      remountHydrationArmedPaneIdsRef.current.clear();
      for (const timeoutId of pendingUnmountCleanupRef.current.values()) {
        window.clearTimeout(timeoutId);
      }
      pendingUnmountCleanupRef.current.clear();
      for (const registration of runtimeBindingDisposersRef.current.values()) {
        registration.dispose();
      }
      runtimeBindingDisposersRef.current.clear();
      for (const sub of inputSubscriptionsRef.current.values()) {
        sub.dispose();
      }
      inputSubscriptionsRef.current.clear();
      terminalHandlesRef.current.clear();
      xtermsRef.current.clear();
      ensuredRuntimeIdsRef.current.clear();
      paneWriteStatesRef.current.clear();
    };
  }, []);

  const setTerminalHandle = useCallback((paneId: string, handle: TerminalHandle | null) => {
    const pendingCleanup = pendingUnmountCleanupRef.current.get(paneId);
    if (pendingCleanup !== undefined) {
      window.clearTimeout(pendingCleanup);
      pendingUnmountCleanupRef.current.delete(paneId);
    }

    if (!handle) {
      const pane = getCurrentPane(paneId);
      const currentXterm = xtermsRef.current.get(paneId);
      const pendingGeometryTimer = pendingGeometryTimersRef.current.get(paneId);
      if (pendingGeometryTimer !== undefined) {
        window.clearTimeout(pendingGeometryTimer);
        pendingGeometryTimersRef.current.delete(paneId);
      }
      pendingGeometrySyncRef.current.delete(paneId);
      pendingRuntimeHydrationRef.current.delete(paneId);
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        paneId,
        message: 'terminal ref cleared',
      });
      if (pane) {
        if (currentXterm && ensuredRuntimeIdsRef.current.has(pane.runtimeId)) {
          remountHydrationArmedPaneIdsRef.current.add(paneId);
        } else {
          remountHydrationArmedPaneIdsRef.current.delete(paneId);
        }
        recordTerminalRuntimeLog({
          category: 'binding',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: 'terminal handle cleared',
        });
      }
      terminalHandlesRef.current.delete(paneId);
      const timeoutId = window.setTimeout(() => {
        recordPaneRuntimeDebugEvent({
          scope: 'binder',
          paneId,
          message: 'terminal cleanup executed',
        });
        pendingUnmountCleanupRef.current.delete(paneId);
        xtermsRef.current.delete(paneId);
        inputSubscriptionsRef.current.get(paneId)?.dispose();
        inputSubscriptionsRef.current.delete(paneId);
      }, 0);
      pendingUnmountCleanupRef.current.set(paneId, timeoutId);
      return;
    }
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      paneId,
      message: 'terminal ref set',
    });
    terminalHandlesRef.current.set(paneId, handle);
    const pane = paneByIdRef.current.get(paneId);
    if (pane) {
      recordTerminalRuntimeLog({
        category: 'binding',
        sessionId: pane.sessionId ?? pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'terminal handle attached',
      });
    }
  }, [getCurrentPane]);

  const ensurePaneRuntime = useCallback(async (paneId: string, xterm: XTerm) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }
    if (ensuredRuntimeIdsRef.current.has(pane.runtimeId)) {
      return;
    }

    const existing = pendingEnsuresRef.current.get(pane.runtimeId);
    if (existing) {
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'reuse in-flight runtime ensure',
      });
      await existing;
      return;
    }

    const promise = (async () => {
      let spawnArgs = cachedSpawnArgsRef.current.get(paneId);
      if (spawnArgs === undefined) {
        spawnArgs = pane.getSpawnArgs({
          cols: xterm.cols > 0 ? xterm.cols : 80,
          rows: xterm.rows > 0 ? xterm.rows : 24,
        });
        cachedSpawnArgsRef.current.set(paneId, spawnArgs);
      }
      if (!spawnArgs) {
        recordPaneRuntimeDebugEvent({
          scope: 'binder',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: 'runtime ensure skipped',
        });
        return;
      }

      if (import.meta.env.DEV && pane.testSessionId) {
        const testWindow = window as Window & {
          __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
        };
        testWindow.__TEST_SESSION_INPUT_EVENTS = testWindow.__TEST_SESSION_INPUT_EVENTS || [];
        testWindow.__TEST_SESSION_INPUT_EVENTS.push({ sessionId: pane.testSessionId, event: 'connect_terminal' });
      }

      const startedAt = performance.now();
      let attempt = 0;

      for (;;) {
        attempt += 1;
        recordPaneRuntimeDebugEvent({
          scope: 'binder',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: attempt === 1 ? 'spawn/attach runtime' : 'retry spawn/attach runtime',
          details: {
            attempt,
            cols: spawnArgs.cols,
            rows: spawnArgs.rows,
            shell: spawnArgs.shell ?? false,
          },
        });
        recordTerminalRuntimeLog({
          category: 'binding',
          event: 'runtime.ensure.requested',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: attempt === 1 ? 'spawn/attach runtime' : 'retry spawn/attach runtime',
          details: {
            attempt,
            cols: spawnArgs.cols,
            rows: spawnArgs.rows,
            shell: spawnArgs.shell ?? false,
          },
        });
        try {
          await ptySpawn({ args: spawnArgs });
          break;
        } catch (error) {
          const elapsedMs = performance.now() - startedAt;
          if (!isRetryableRuntimeEnsureError(error) || elapsedMs >= RUNTIME_ENSURE_TIMEOUT_MS) {
            throw error;
          }
          recordPaneRuntimeDebugEvent({
            scope: 'binder',
            sessionId: pane.sessionId ?? pane.testSessionId,
            paneId,
            runtimeId: pane.runtimeId,
            message: 'runtime ensure retry scheduled',
            details: {
              attempt,
              elapsedMs: Math.round(elapsedMs),
              error: runtimeEnsureErrorMessage(error),
            },
          });
          await waitForRuntimeEnsureRetry(RUNTIME_ENSURE_RETRY_DELAY_MS);
          if (xtermsRef.current.get(paneId) !== xterm) {
            return;
          }
          const livePane = getCurrentPane(paneId);
          if (!livePane || livePane.runtimeId !== pane.runtimeId) {
            return;
          }
        }
      }
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane.sessionId ?? pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'runtime ensured',
      });
      recordTerminalRuntimeLog({
        category: 'binding',
        event: 'runtime.ensured',
        sessionId: pane.sessionId ?? pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'runtime ensured',
      });
      ensuredRuntimeIdsRef.current.add(pane.runtimeId);
    })().finally(() => {
      pendingEnsuresRef.current.delete(pane.runtimeId);
    });

    pendingEnsuresRef.current.set(pane.runtimeId, promise);
    await promise;
  }, [getCurrentPane]);

  const wireTerminal = useCallback((paneId: string, xterm: XTerm) => {
    const previousXterm = xtermsRef.current.get(paneId);
    const isNewMount = previousXterm !== xterm;
    xtermsRef.current.set(paneId, xterm);
    const pane = getCurrentPane(paneId);
    if (pane) {
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane.sessionId ?? pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'wire terminal',
        details: { cols: xterm.cols, rows: xterm.rows },
      });
      recordTerminalRuntimeLog({
        category: 'binding',
        event: 'binding.wire',
        sessionId: pane.sessionId ?? pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'wire xterm to pane runtime',
        details: { cols: xterm.cols, rows: xterm.rows },
      });
      const shouldHydrateRemount = (
        isNewMount &&
        ensuredRuntimeIdsRef.current.has(pane.runtimeId) &&
        remountHydrationArmedPaneIdsRef.current.has(paneId)
      );
      if (shouldHydrateRemount) {
        pendingRuntimeHydrationRef.current.set(paneId, { xterm, reason: 'remount_attach' });
        recordTerminalRuntimeLog({
          category: 'binding',
          event: 'runtime.hydrate.requested',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: 'schedule runtime hydrate for remounted xterm',
          details: { cols: xterm.cols, rows: xterm.rows },
        });
      }
    }
    remountHydrationArmedPaneIdsRef.current.delete(paneId);

    const sendToPty = (data: string) => {
      const currentPane = getCurrentPane(paneId);
      if (!currentPane) {
        return;
      }
      if (import.meta.env.DEV && currentPane.testSessionId) {
        const testWindow = window as Window & {
          __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
        };
        testWindow.__TEST_SESSION_INPUT_EVENTS = testWindow.__TEST_SESSION_INPUT_EVENTS || [];
        testWindow.__TEST_SESSION_INPUT_EVENTS.push({ sessionId: currentPane.testSessionId, event: 'send_to_pty', data });
      }
      recordPaneRuntimeDebugEvent({
        scope: 'input',
        sessionId: currentPane.testSessionId,
        paneId,
        runtimeId: currentPane.runtimeId,
        message: 'send input to pty',
        details: {
          dataPreview: data.slice(0, 20),
          dataLength: data.length,
          ...activeElementSummary(),
        },
      });
      ptyWrite({ id: currentPane.runtimeId, data, source: 'user' }).catch(console.error);
    };

    // Always subscribe onData — even if paneByIdRef doesn't have the pane
    // yet (child effect runs before parent effect). sendToPty resolves the
    // pane from the render-time snapshot until the effect-driven map catches up.
    inputSubscriptionsRef.current.get(paneId)?.dispose();
    inputSubscriptionsRef.current.set(paneId, xterm.onData(sendToPty));
    xterm.attachCustomKeyEventHandler(installTerminalKeyHandler(sendToPty));

    const pendingEvents = pendingTerminalEventsRef.current.get(paneId);
    if (pendingEvents?.length) {
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane?.testSessionId,
        paneId,
        runtimeId: pane?.runtimeId,
        message: 'replay queued pty events',
        details: { count: pendingEvents.length },
      });
      for (const event of pendingEvents) {
        replayPendingTerminalEvent(paneId, xterm, event);
      }
      pendingTerminalEventsRef.current.delete(paneId);
    }
  }, [getCurrentPane, replayPendingTerminalEvent]);

  const requestRuntimeRedraw = useCallback((
    paneId: string,
    cols: number,
    rows: number,
    reason: string,
    axis: 'auto' | 'cols' | 'rows' = 'auto',
  ) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }

    let bounceCols = cols;
    let bounceRows = rows;
    const shouldBounceCols = axis === 'cols' || (axis === 'auto' && cols > 1 && rows <= 1);
    if (shouldBounceCols && cols > 1) {
      bounceCols = cols - 1;
    } else if (rows > 1) {
      bounceRows = rows - 1;
    } else if (cols > 1) {
      bounceCols = cols - 1;
    } else {
      return;
    }

    recordTerminalRuntimeLog({
      category: 'geometry',
      event: 'pty.redraw.requested',
      sessionId: pane.sessionId ?? pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'request PTY redraw bounce',
      details: {
        reason,
        axis,
        cols,
        rows,
        bounceCols,
        bounceRows,
      },
    });

    ptyResize({
      id: pane.runtimeId,
      cols: bounceCols,
      rows: bounceRows,
      reason: `${reason}:bounce`,
    }).catch(console.error);
    window.setTimeout(() => {
      const livePane = getCurrentPane(paneId);
      if (!livePane || livePane.runtimeId !== pane.runtimeId) {
        return;
      }
      ptyResize({
        id: pane.runtimeId,
        cols,
        rows,
        reason: `${reason}:restore`,
      }).catch(console.error);
    }, PTY_REDRAW_BOUNCE_DELAY_MS);
  }, [getCurrentPane]);

  const reportRuntimeEnsureFailure = useCallback((
    paneId: string,
    xterm: XTerm,
    source: 'ready' | 'resize',
    error: unknown,
  ) => {
    const pane = getCurrentPane(paneId);
    if (pane) {
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: `runtime ensure failed from ${source}`,
        details: { error: error instanceof Error ? error.message : String(error) },
      });
    }
    console.error('[PaneRuntimeBinder] Failed to ensure runtime:', error);
    xterm.write(`\r\n[Failed to connect PTY: ${error}]\r\n`);
  }, [getCurrentPane]);

  const flushPendingGeometrySync = useCallback(async (
    paneId: string,
    source: 'ready' | 'resize',
  ) => {
    pendingGeometryTimersRef.current.delete(paneId);
    const pending = pendingGeometrySyncRef.current.get(paneId);
    if (!pending) {
      return;
    }
    pendingGeometrySyncRef.current.delete(paneId);

    const pane = getCurrentPane(paneId);
    const liveXterm = xtermsRef.current.get(paneId);
    if (!pane || !liveXterm || liveXterm !== pending.xterm) {
      return;
    }

    primeSpawnArgsForSize(paneId, pending.cols, pending.rows);

    const lastCommitted = lastCommittedGeometryRef.current.get(paneId);
    const geometryChanged = isCommittedGeometryChanged(lastCommitted, pending);

    recordTerminalRuntimeLog({
      category: 'geometry',
      event: 'pty.geometry.flush',
      sessionId: pane.sessionId ?? pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'flush settled PTY geometry',
      details: {
        reason: pending.reason,
        cols: pending.cols,
        rows: pending.rows,
        forceRedraw: pending.forceRedraw,
        geometryChanged,
        lastCommittedCols: lastCommitted?.cols ?? null,
        lastCommittedRows: lastCommitted?.rows ?? null,
      },
    });

    const spawnArgs = cachedSpawnArgsRef.current.get(paneId);
    const pendingHydration = pendingRuntimeHydrationRef.current.get(paneId);
    const shouldHydrate = pendingHydration?.xterm === pending.xterm;
    const flushPlan = planGeometryFlush({
      runtimeReady: ensuredRuntimeIdsRef.current.has(pane.runtimeId),
      ensureInFlight: pendingEnsuresRef.current.has(pane.runtimeId),
      hydratePending: shouldHydrate,
      pending,
      lastCommitted,
      spawnIsShell: spawnArgs?.shell ?? false,
      source,
    });

    if (flushPlan.kind === 'retry_after_pending_ensure') {
        pendingGeometrySyncRef.current.set(paneId, pending);
        const retryTimeoutId = window.setTimeout(() => {
          void flushPendingGeometrySync(paneId, source);
        }, PTY_REDRAW_BOUNCE_DELAY_MS);
        pendingGeometryTimersRef.current.set(paneId, retryTimeoutId);
        return;
    }

    if (flushPlan.kind === 'ensure_runtime') {
      try {
        await ensurePaneRuntime(paneId, pending.xterm);
      } catch (error) {
        reportRuntimeEnsureFailure(paneId, pending.xterm, source, error);
        return;
      }
      lastCommittedGeometryRef.current.set(paneId, { cols: pending.cols, rows: pending.rows });
      return;
    }

    if (flushPlan.kind === 'hydrate_runtime') {
      try {
        await ptyAttach({
          args: {
            id: pane.runtimeId,
            cols: flushPlan.attach.cols,
            rows: flushPlan.attach.rows,
            shell: spawnArgs?.shell ?? false,
            reason: flushPlan.attach.reason,
            policy: 'same_app_remount',
          },
          forceResizeBeforeAttach: flushPlan.attach.forceResizeBeforeAttach,
        });
      } catch (error) {
        reportRuntimeEnsureFailure(paneId, pending.xterm, source, error);
        return;
      }
      pendingRuntimeHydrationRef.current.delete(paneId);
      lastCommittedGeometryRef.current.set(paneId, flushPlan.nextCommitted);
      return;
    }

    if (flushPlan.kind === 'resize_runtime') {
      ptyResize({
        id: pane.runtimeId,
        cols: flushPlan.resize.cols,
        rows: flushPlan.resize.rows,
        reason: flushPlan.resize.reason,
      }).catch(console.error);
      lastCommittedGeometryRef.current.set(paneId, flushPlan.nextCommitted);
      if (flushPlan.redrawAfterSettle) {
        const redrawAfterSettle = flushPlan.redrawAfterSettle;
        const redrawWriteBaseline = paneWriteStatesRef.current.get(paneId)?.totalWriteCount ?? 0;
        const redrawTimeoutId = window.setTimeout(() => {
          pendingGeometryTimersRef.current.delete(paneId);
          const livePane = getCurrentPane(paneId);
          const currentXterm = xtermsRef.current.get(paneId);
          if (!livePane || livePane.runtimeId !== pane.runtimeId || currentXterm !== pending.xterm) {
            return;
          }
          const writeState = paneWriteStatesRef.current.get(paneId);
          const redrawPlan = planDeferredGeometryRedraw({
            baselineWriteCount: redrawWriteBaseline,
            currentWriteCount: writeState?.totalWriteCount ?? 0,
            reason: `${pending.reason}:resize_redraw`,
            axis: redrawAfterSettle.axis,
          });
          if (redrawPlan.kind === 'skip') {
            recordTerminalRuntimeLog({
              category: 'geometry',
              event: 'pty.redraw.skipped',
              sessionId: pane.sessionId ?? pane.testSessionId,
              paneId,
              runtimeId: pane.runtimeId,
              message: 'skip PTY redraw because runtime emitted fresh output after resize',
              details: {
                reason: redrawPlan.reason,
                axis: redrawPlan.axis,
                redrawWriteBaseline: redrawPlan.baselineWriteCount,
                currentWriteCount: redrawPlan.currentWriteCount,
              },
            });
            return;
          }
          requestRuntimeRedraw(
            paneId,
            pending.cols,
            pending.rows,
            redrawPlan.reason,
            redrawPlan.axis,
          );
        }, PTY_REDRAW_SETTLE_MS);
        pendingGeometryTimersRef.current.set(paneId, redrawTimeoutId);
      }
      return;
    }

    if (flushPlan.kind === 'redraw_only') {
      requestRuntimeRedraw(paneId, flushPlan.redraw.cols, flushPlan.redraw.rows, flushPlan.redraw.reason);
    }
  }, [ensurePaneRuntime, getCurrentPane, primeSpawnArgsForSize, reportRuntimeEnsureFailure, requestRuntimeRedraw]);

  const scheduleGeometrySync = useCallback((
    paneId: string,
    xterm: XTerm,
    cols: number,
    rows: number,
    source: 'ready' | 'resize',
    options?: { forceRedraw?: boolean; reason?: string },
  ) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }

    primeSpawnArgsForSize(paneId, cols, rows);
    const previous = pendingGeometrySyncRef.current.get(paneId);
    const { pending, delayMs } = planPendingGeometrySync(previous, {
      cols,
      rows,
      xterm,
      source,
      reason: options?.reason,
      forceRedraw: options?.forceRedraw,
      readyDelayMs: PTY_READY_SETTLE_MS,
      resizeDelayMs: PTY_GEOMETRY_SETTLE_MS,
    });

    pendingGeometrySyncRef.current.set(paneId, pending);

    const existingTimer = pendingGeometryTimersRef.current.get(paneId);
    if (existingTimer !== undefined) {
      window.clearTimeout(existingTimer);
    }

    recordTerminalRuntimeLog({
      category: 'geometry',
      event: 'pty.geometry.scheduled',
      sessionId: pane.sessionId ?? pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'schedule settled PTY geometry',
      details: {
        source,
        reason: pending.reason,
        cols,
        rows,
        delayMs,
        forceRedraw: pending.forceRedraw,
      },
    });

    const timeoutId = window.setTimeout(() => {
      void flushPendingGeometrySync(paneId, source);
    }, delayMs);
    pendingGeometryTimersRef.current.set(paneId, timeoutId);
  }, [flushPendingGeometrySync, getCurrentPane, primeSpawnArgsForSize]);

  const bindTerminalLifecycle = useCallback((
    paneId: string,
    xterm: XTerm,
    phase: 'init' | 'ready',
  ) => {
    const pane = getCurrentPane(paneId);
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      sessionId: pane?.testSessionId,
      paneId,
      runtimeId: pane?.runtimeId,
      message: phase === 'init' ? 'terminal init' : 'terminal ready',
      details: { cols: xterm.cols, rows: xterm.rows },
    });
    wireTerminal(paneId, xterm);
    const hydratingRemount = pendingRuntimeHydrationRef.current.get(paneId)?.xterm === xterm;
    if (phase === 'init' && hydratingRemount) {
      window.requestAnimationFrame(() => {
        const liveXterm = xtermsRef.current.get(paneId);
        if (liveXterm !== xterm) {
          return;
        }
        terminalHandlesRef.current.get(paneId)?.fit();
      });
      scheduleGeometrySync(paneId, xterm, xterm.cols, xterm.rows, 'ready', {
        reason: 'init_hydrate',
      });
      return;
    }
    if (phase === 'ready') {
      scheduleGeometrySync(paneId, xterm, xterm.cols, xterm.rows, 'ready', { reason: 'ready' });
    }
  }, [getCurrentPane, scheduleGeometrySync, wireTerminal]);

  const handleTerminalInit = useCallback((paneId: string) => (xterm: XTerm) => {
    bindTerminalLifecycle(paneId, xterm, 'init');
  }, [bindTerminalLifecycle]);

  const handleTerminalReady = useCallback((paneId: string) => (xterm: XTerm) => {
    bindTerminalLifecycle(paneId, xterm, 'ready');
  }, [bindTerminalLifecycle]);

  const injectPanePayload = useCallback(async (
    paneId: string,
    payload: Uint8Array | string,
    encoding: 'bytes' | 'base64',
  ) => {
    const xterm = xtermsRef.current.get(paneId);
    if (!xterm) {
      return false;
    }
    const bytes = encoding === 'base64' ? decodePtyBytes(payload as string) : payload as Uint8Array;
    enqueuePaneBytes(paneId, xterm, bytes);
    return true;
  }, [enqueuePaneBytes]);

  const handleTerminalResize = useCallback((paneId: string) => (
    cols: number,
    rows: number,
    options?: { forceRedraw?: boolean; reason?: string },
  ) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      sessionId: pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'resize pane runtime',
      details: {
        cols,
        rows,
        forceRedraw: options?.forceRedraw ?? false,
        reason: options?.reason ?? null,
      },
    });
    const xterm = xtermsRef.current.get(paneId);
    if (xterm) {
      scheduleGeometrySync(paneId, xterm, cols, rows, 'resize', options);
    }
  }, [getCurrentPane, scheduleGeometrySync]);

  const focusPaneWithRetry = useCallback((paneId: string, retries = 20) => {
    const tryFocus = (remaining: number) => {
      const pane = getCurrentPane(paneId);
      const handle = terminalHandlesRef.current.get(paneId);
      const xterm = xtermsRef.current.get(paneId);
      const focusResult = focusTerminalViewport(handle ?? null, xterm);
      if (focusResult === 'handle') {
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus via terminal handle succeeded',
          details: activeElementSummary(),
        });
        recordTerminalRuntimeLog({
          category: 'focus',
          event: 'focus.acquired',
          sessionId: pane?.sessionId ?? pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus acquired via terminal handle',
          details: activeElementSummary(),
        });
        return;
      }
      if (focusResult === 'xterm') {
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus via xterm fallback',
          details: activeElementSummary(),
        });
        recordTerminalRuntimeLog({
          category: 'focus',
          event: 'focus.acquired',
          sessionId: pane?.sessionId ?? pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus acquired via xterm fallback',
          details: activeElementSummary(),
        });
        return;
      }
      if (remaining <= 0) {
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus retries exhausted',
          details: activeElementSummary(),
        });
        return;
      }
      window.setTimeout(() => tryFocus(remaining - 1), 50);
    };
    tryFocus(retries);
  }, [getCurrentPane]);

  const fitPane = useCallback((paneId: string) => {
    terminalHandlesRef.current.get(paneId)?.fit();
  }, []);

  const fitActivePane = useCallback(() => {
    fitPane(activePaneId);
  }, [activePaneId, fitPane]);

  const typeTextViaPaneInput = useCallback((paneId: string, text: string) => {
    const handle = terminalHandlesRef.current.get(paneId);
    return handle?.typeTextViaInput(text) || false;
  }, []);

  const isPaneInputFocused = useCallback((paneId: string) => {
    const handle = terminalHandlesRef.current.get(paneId);
    return handle?.isInputFocused() || false;
  }, []);

  const scrollPaneToTop = useCallback((paneId: string) => {
    return scrollTerminalViewportToTop(xtermsRef.current.get(paneId), resetTerminalScrollPin);
  }, []);

  const getPaneText = useCallback((paneId: string) => {
    return snapshotTerminalText(xtermsRef.current.get(paneId) || null);
  }, []);

  const getPaneSize = useCallback((paneId: string) => {
    const xterm = xtermsRef.current.get(paneId);
    if (!xterm) {
      return null;
    }
    return { cols: xterm.cols, rows: xterm.rows };
  }, []);

  const getPaneVisibleContent = useCallback((paneId: string) => {
    return snapshotVisibleTerminalContent(xtermsRef.current.get(paneId) || null);
  }, []);

  const resetPaneTerminal = useCallback((paneId: string) => {
    const xterm = xtermsRef.current.get(paneId);
    if (!resetTerminalViewport(xterm, resetTerminalScrollPin)) {
      return false;
    }
    pendingTerminalEventsRef.current.delete(paneId);
    return true;
  }, []);

  const injectPaneBytes = useCallback(async (paneId: string, bytes: Uint8Array) => {
    return injectPanePayload(paneId, bytes, 'bytes');
  }, [injectPanePayload]);

  const injectPaneBase64 = useCallback(async (paneId: string, payload: string) => {
    return injectPanePayload(paneId, payload, 'base64');
  }, [injectPanePayload]);

  const drainPaneTerminal = useCallback(async (paneId: string) => {
    const xterm = xtermsRef.current.get(paneId);
    if (!xterm) {
      return false;
    }
    await paneWriteStatesRef.current.get(paneId)?.writeChain;
    return true;
  }, []);

  return {
    setTerminalHandle,
    handleTerminalInit,
    handleTerminalReady,
    handleTerminalResize,
    focusPaneWithRetry,
    fitPane,
    fitActivePane,
    typeTextViaPaneInput,
    isPaneInputFocused,
    scrollPaneToTop,
    getPaneText,
    getPaneSize,
    getPaneVisibleContent,
    resetPaneTerminal,
    injectPaneBytes,
    injectPaneBase64,
    drainPaneTerminal,
  };
}
