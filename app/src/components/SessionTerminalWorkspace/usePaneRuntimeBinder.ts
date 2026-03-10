import { useCallback, useEffect, useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { ptyResize, ptySpawn, ptyWrite, type PtyEventPayload, type PtySpawnArgs } from '../../pty/bridge';
import { triggerShortcut } from '../../shortcuts/useShortcut';
import type { TerminalHandle } from '../Terminal';
import { activeElementSummary, recordPaneRuntimeDebugEvent } from '../../utils/paneRuntimeDebug';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';

const MAX_PENDING_TERMINAL_EVENTS = 256;

interface PaneRuntimeSize {
  cols: number;
  rows: number;
}

export interface PaneRuntimeSpec {
  paneId: string;
  runtimeId: string;
  testSessionId?: string;
  getSpawnArgs: (size: PaneRuntimeSize) => PtySpawnArgs | null;
}

export interface PaneRuntimeBinder {
  setTerminalHandle: (paneId: string, handle: TerminalHandle | null) => void;
  handleTerminalInit: (paneId: string) => (xterm: XTerm) => void;
  handleTerminalReady: (paneId: string) => (xterm: XTerm) => void;
  handleTerminalResize: (paneId: string) => (cols: number, rows: number) => void;
  focusPaneWithRetry: (paneId: string, retries?: number) => void;
  fitPane: (paneId: string) => void;
  fitActivePane: () => void;
  typeTextViaPaneInput: (paneId: string, text: string) => boolean;
  isPaneInputFocused: (paneId: string) => boolean;
  getPaneText: (paneId: string) => string;
  getPaneSize: (paneId: string) => PaneRuntimeSize | null;
}

function decodePtyBytes(payload: string): Uint8Array {
  const binaryStr = atob(payload);
  return Uint8Array.from(binaryStr, (c) => c.charCodeAt(0));
}

function writePtyEventToTerminal(terminal: XTerm, msg: PtyEventPayload) {
  switch (msg.event) {
    case 'data': {
      const bytes = decodePtyBytes(msg.data);
      const termWithUtf8 = terminal as XTerm & { writeUtf8?: (data: Uint8Array) => void };
      if (typeof termWithUtf8.writeUtf8 === 'function') {
        termWithUtf8.writeUtf8(bytes);
      } else {
        terminal.write(new TextDecoder('utf-8').decode(bytes));
      }
      break;
    }
    case 'reset':
      terminal.reset();
      break;
    case 'exit':
      terminal.write(`\r\n[Process exited with code ${msg.code}]\r\n`);
      break;
    case 'error':
      terminal.write(`\r\n[Error: ${msg.error}]\r\n`);
      break;
    default:
      break;
  }
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

function installTerminalKeyHandler(sendToPty: (data: string) => void) {
  return (ev: KeyboardEvent) => {
    const accel = ev.metaKey || ev.ctrlKey;
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
      if (ev.shiftKey && ev.key === 'Enter') {
        return !triggerShortcut('terminal.toggleMaximize');
      }
      if (!ev.shiftKey && ev.key.toLowerCase() === 'w') {
        return !triggerShortcut('terminal.close');
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
  eventRouter: PaneRuntimeEventRouter
): PaneRuntimeBinder {
  const paneByIdRef = useRef<Map<string, PaneRuntimeSpec>>(new Map());
  const terminalHandlesRef = useRef<Map<string, TerminalHandle>>(new Map());
  const xtermsRef = useRef<Map<string, XTerm>>(new Map());
  const inputSubscriptionsRef = useRef<Map<string, { dispose: () => void }>>(new Map());
  const pendingTerminalEventsRef = useRef<Map<string, PtyEventPayload[]>>(new Map());
  const pendingEnsuresRef = useRef<Map<string, Promise<void>>>(new Map());
  const cachedSpawnArgsRef = useRef<Map<string, PtySpawnArgs | null>>(new Map());
  const cachedTerminalTextRef = useRef<Map<string, string>>(new Map());
  const pendingUnmountCleanupRef = useRef<Map<string, number>>(new Map());
  const runtimeBindingDisposersRef = useRef<Map<string, { paneId: string; testSessionId?: string; dispose: () => void }>>(new Map());

  useEffect(() => {
    paneByIdRef.current = new Map(panes.map((pane) => [pane.paneId, pane]));
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
    for (const paneId of Array.from(cachedTerminalTextRef.current.keys())) {
      if (!activePaneIds.has(paneId)) {
        cachedTerminalTextRef.current.delete(paneId);
      }
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
            const events = pendingTerminalEventsRef.current.get(pane.paneId) || [];
            if (events.length >= MAX_PENDING_TERMINAL_EVENTS) {
              events.shift();
            }
            events.push(msg);
            pendingTerminalEventsRef.current.set(pane.paneId, events);
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
          writePtyEventToTerminal(xterm, msg);
        },
      });

      runtimeBindingDisposersRef.current.set(pane.runtimeId, {
        paneId: pane.paneId,
        testSessionId: pane.testSessionId,
        dispose,
      });
    }
  }, [eventRouter, panes]);

  useEffect(() => {
    return () => {
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
    };
  }, []);

  const setTerminalHandle = useCallback((paneId: string, handle: TerminalHandle | null) => {
    const pendingCleanup = pendingUnmountCleanupRef.current.get(paneId);
    if (pendingCleanup !== undefined) {
      window.clearTimeout(pendingCleanup);
      pendingUnmountCleanupRef.current.delete(paneId);
    }

    if (!handle) {
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        paneId,
        message: 'terminal ref cleared',
      });
      const currentXterm = xtermsRef.current.get(paneId);
      const currentText = snapshotTerminalText(currentXterm || null);
      if (currentText) {
        cachedTerminalTextRef.current.set(paneId, currentText);
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
  }, []);

  const ensurePaneRuntime = useCallback(async (paneId: string, xterm: XTerm) => {
    const pane = paneByIdRef.current.get(paneId);
    if (!pane) {
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
          sessionId: pane.testSessionId,
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

      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'spawn/attach runtime',
        details: {
          cols: spawnArgs.cols,
          rows: spawnArgs.rows,
          shell: spawnArgs.shell ?? false,
        },
      });
      await ptySpawn({ args: spawnArgs });
      pendingTerminalEventsRef.current.delete(paneId);
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'runtime ensured',
      });
    })().finally(() => {
      pendingEnsuresRef.current.delete(pane.runtimeId);
    });

    pendingEnsuresRef.current.set(pane.runtimeId, promise);
    await promise;
  }, []);

  const wireTerminal = useCallback((paneId: string, xterm: XTerm) => {
    xtermsRef.current.set(paneId, xterm);
    const pane = paneByIdRef.current.get(paneId);
    if (!pane) {
      return;
    }
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      sessionId: pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'wire terminal',
      details: { cols: xterm.cols, rows: xterm.rows },
    });

    const sendToPty = (data: string) => {
      if (import.meta.env.DEV && pane.testSessionId) {
        const testWindow = window as Window & {
          __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
        };
        testWindow.__TEST_SESSION_INPUT_EVENTS = testWindow.__TEST_SESSION_INPUT_EVENTS || [];
        testWindow.__TEST_SESSION_INPUT_EVENTS.push({ sessionId: pane.testSessionId, event: 'send_to_pty', data });
      }
      recordPaneRuntimeDebugEvent({
        scope: 'input',
        sessionId: pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'send input to pty',
        details: {
          dataPreview: data.slice(0, 20),
          dataLength: data.length,
          ...activeElementSummary(),
        },
      });
      ptyWrite({ id: pane.runtimeId, data, source: 'user' }).catch(console.error);
    };

    inputSubscriptionsRef.current.get(paneId)?.dispose();
    inputSubscriptionsRef.current.set(paneId, xterm.onData(sendToPty));
    xterm.attachCustomKeyEventHandler(installTerminalKeyHandler(sendToPty));

    const cachedText = cachedTerminalTextRef.current.get(paneId);
    if (cachedText && snapshotTerminalText(xterm).trim().length === 0) {
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'hydrate terminal from cached text',
        details: { textLength: cachedText.length },
      });
      xterm.write(cachedText.replace(/\n/g, '\r\n'));
    }
  }, []);

  const handleTerminalInit = useCallback((paneId: string) => (xterm: XTerm) => {
    const pane = paneByIdRef.current.get(paneId);
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      sessionId: pane?.testSessionId,
      paneId,
      runtimeId: pane?.runtimeId,
      message: 'terminal init',
      details: { cols: xterm.cols, rows: xterm.rows },
    });
    wireTerminal(paneId, xterm);
  }, [wireTerminal]);

  const handleTerminalReady = useCallback((paneId: string) => (xterm: XTerm) => {
    const pane = paneByIdRef.current.get(paneId);
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      sessionId: pane?.testSessionId,
      paneId,
      runtimeId: pane?.runtimeId,
      message: 'terminal ready',
      details: { cols: xterm.cols, rows: xterm.rows },
    });
    wireTerminal(paneId, xterm);
    void ensurePaneRuntime(paneId, xterm).catch((error) => {
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        sessionId: pane?.testSessionId,
        paneId,
        runtimeId: pane?.runtimeId,
        message: 'runtime ensure failed',
        details: { error: error instanceof Error ? error.message : String(error) },
      });
      console.error('[PaneRuntimeBinder] Failed to ensure runtime:', error);
      xterm.write(`\r\n[Failed to connect PTY: ${error}]\r\n`);
    });
  }, [ensurePaneRuntime, wireTerminal]);

  const handleTerminalResize = useCallback((paneId: string) => (cols: number, rows: number) => {
    const pane = paneByIdRef.current.get(paneId);
    if (!pane) {
      return;
    }
    recordPaneRuntimeDebugEvent({
      scope: 'binder',
      sessionId: pane.testSessionId,
      paneId,
      runtimeId: pane.runtimeId,
      message: 'resize pane runtime',
      details: { cols, rows },
    });
    ptyResize({ id: pane.runtimeId, cols, rows }).catch(console.error);
  }, []);

  const focusPaneWithRetry = useCallback((paneId: string, retries = 20) => {
    const tryFocus = (remaining: number) => {
      const pane = paneByIdRef.current.get(paneId);
      const handle = terminalHandlesRef.current.get(paneId);
      if (handle?.terminal && handle.focus()) {
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus via terminal handle succeeded',
          details: activeElementSummary(),
        });
        return;
      }
      const xterm = xtermsRef.current.get(paneId);
      if (xterm) {
        xterm.focus();
        recordPaneRuntimeDebugEvent({
          scope: 'focus',
          sessionId: pane?.testSessionId,
          paneId,
          runtimeId: pane?.runtimeId,
          message: 'focus via xterm fallback',
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
  }, []);

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
    getPaneText,
    getPaneSize,
  };
}
