import { useCallback, useEffect, useMemo, useRef } from 'react';
import { ptyAttach, ptyResize, ptySpawn, ptyWrite, type PtyEventPayload } from '../../pty/bridge';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import type { GhosttyTerminalHandle } from '../GhosttyTerminal';
import type { TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../../utils/terminalStyleSummary';

export interface PaneRuntimeSpec {
  paneId: string;
  runtimeId: string;
  paneKind: 'agent';
  agent?: string;
  sessionId?: string;
  testSessionId?: string;
  getSpawnArgs: (size: { cols: number; rows: number }) => Parameters<typeof ptySpawn>[0]['args'] | null;
}

function decodePtyBytes(payload: string): Uint8Array {
  const binary = atob(payload);
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

export interface GhosttyPaneRuntime {
  setTerminalHandle: (paneId: string, handle: GhosttyTerminalHandle | null) => void;
  handleTerminalReady: (paneId: string) => (terminal: GhosttyTerminalHandle) => void;
  handleTerminalInput: (paneId: string) => (data: string) => void;
  handleTerminalResize: (paneId: string) => (cols: number, rows: number, options?: { reason?: string }) => void;
  focusPane: (paneId: string, retries?: number) => void;
  fitPane: (paneId: string) => void;
  fitActivePane: () => void;
  typeTextViaPaneInput: (paneId: string, text: string) => boolean;
  isPaneInputFocused: (paneId: string) => boolean;
  scrollPaneToTop: (paneId: string) => boolean;
  getPaneText: (paneId: string) => string;
  getPaneSize: (paneId: string) => { cols: number; rows: number } | null;
  getPaneVisibleContent: (paneId: string) => TerminalVisibleContentSnapshot;
  getPaneVisibleStyleSummary: (paneId: string) => TerminalVisibleStyleSnapshot;
  resetPaneTerminal: (paneId: string) => boolean;
  injectPaneBytes: (paneId: string, bytes: Uint8Array) => Promise<boolean>;
  injectPaneBase64: (paneId: string, data: string) => Promise<boolean>;
  drainPaneTerminal: (paneId: string) => Promise<boolean>;
}

export function useGhosttyPaneRuntime(
  panes: PaneRuntimeSpec[],
  activePaneId: string,
  eventRouter: PaneRuntimeEventRouter,
  isActiveSessionRef: { current: boolean },
): GhosttyPaneRuntime {
  const panesRef = useRef(panes);
  const handlesRef = useRef(new Map<string, GhosttyTerminalHandle>());
  const readyRuntimesRef = useRef(new Set<string>());
  const connectingRef = useRef(new Set<string>());
  panesRef.current = panes;

  const paneFor = useCallback((paneId: string) => panesRef.current.find((pane) => pane.paneId === paneId), []);

  const deliverEvent = useCallback((paneId: string, event: PtyEventPayload) => {
    const terminal = handlesRef.current.get(paneId);
    if (!terminal) return;
    const pane = paneFor(paneId);
    if (pane) {
      readyRuntimesRef.current.add(pane.runtimeId);
    }
    switch (event.event) {
      case 'data':
        void terminal.write(decodePtyBytes(event.data), {
          suppressResponses: event.suppressResponses ?? event.source === 'attach_replay',
        });
        break;
      case 'local_resize':
        void terminal.resizeLocal(event.cols, event.rows);
        break;
      case 'reset':
        terminal.reset();
        break;
      case 'exit':
        void terminal.write(`\r\n[Process exited with code ${event.code}]\r\n`);
        break;
      case 'error':
        void terminal.write(`\r\n[Error: ${event.error}]\r\n`);
        break;
      default:
        break;
    }
  }, [paneFor]);

  useEffect(() => {
    const disposers = panes.map((pane) => eventRouter.registerBinding({
      sessionId: pane.testSessionId,
      paneId: pane.paneId,
      runtimeId: pane.runtimeId,
      onEvent: (event) => deliverEvent(pane.paneId, event),
    }));
    return () => {
      disposers.forEach((dispose) => dispose());
    };
  }, [deliverEvent, eventRouter, panes]);

  useEffect(() => {
    const desiredRuntimeIds = new Set(panes.map((pane) => pane.runtimeId));
    for (const runtimeId of readyRuntimesRef.current) {
      if (!desiredRuntimeIds.has(runtimeId)) readyRuntimesRef.current.delete(runtimeId);
    }
  }, [panes]);

  const setTerminalHandle = useCallback((paneId: string, handle: GhosttyTerminalHandle | null) => {
    if (handle) handlesRef.current.set(paneId, handle);
    else handlesRef.current.delete(paneId);
  }, []);

  const handleTerminalReady = useCallback((paneId: string) => async (terminal: GhosttyTerminalHandle) => {
    handlesRef.current.set(paneId, terminal);
    const pane = paneFor(paneId);
    const size = terminal.getSize();
    if (!pane || !size || connectingRef.current.has(pane.runtimeId)) return;
    if (readyRuntimesRef.current.has(pane.runtimeId)) {
      connectingRef.current.add(pane.runtimeId);
      try {
        await ptyAttach({
          args: {
            id: pane.runtimeId,
            cols: size.cols,
            rows: size.rows,
            shell: false,
            agent: pane.agent,
            policy: 'same_app_remount',
          },
        });
      } catch (error) {
        await terminal.write(`\r\n[Failed to reattach PTY: ${String(error)}]\r\n`);
      } finally {
        connectingRef.current.delete(pane.runtimeId);
      }
      return;
    }
    const args = pane.getSpawnArgs(size);
    if (!args) return;
    if (import.meta.env.DEV && pane.testSessionId) {
      const testWindow = window as Window & {
        __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
      };
      testWindow.__TEST_SESSION_INPUT_EVENTS = testWindow.__TEST_SESSION_INPUT_EVENTS || [];
      testWindow.__TEST_SESSION_INPUT_EVENTS.push({ sessionId: pane.testSessionId, event: 'connect_terminal' });
    }
    connectingRef.current.add(pane.runtimeId);
    try {
      await ptySpawn({ args });
      readyRuntimesRef.current.add(pane.runtimeId);
    } catch (error) {
      await terminal.write(`\r\n[Failed to connect PTY: ${String(error)}]\r\n`);
    } finally {
      connectingRef.current.delete(pane.runtimeId);
    }
  }, [paneFor]);

  const handleTerminalInput = useCallback((paneId: string) => (data: string) => {
    const pane = paneFor(paneId);
    if (!pane) return;
    if (import.meta.env.DEV && pane.testSessionId) {
      const testWindow = window as Window & {
        __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
      };
      testWindow.__TEST_SESSION_INPUT_EVENTS = testWindow.__TEST_SESSION_INPUT_EVENTS || [];
      testWindow.__TEST_SESSION_INPUT_EVENTS.push({ sessionId: pane.testSessionId, event: 'send_to_pty', data });
    }
    void ptyWrite({ id: pane.runtimeId, data });
  }, [paneFor]);

  const handleTerminalResize = useCallback((paneId: string) => (cols: number, rows: number, options?: { reason?: string }) => {
    if (!isActiveSessionRef.current) return;
    const pane = paneFor(paneId);
    if (!pane || !readyRuntimesRef.current.has(pane.runtimeId)) return;
    if (isSuspiciousTerminalSize(cols, rows)) return;
    void ptyResize({ id: pane.runtimeId, cols, rows, reason: options?.reason ?? 'ghostty_fit' });
  }, [isActiveSessionRef, paneFor]);

  const get = useCallback((paneId: string) => handlesRef.current.get(paneId), []);
  const emptyContent = useCallback((): TerminalVisibleContentSnapshot => ({
    cols: null, viewportY: null, lineCount: 0, lines: [], lineMetrics: [],
    summary: { nonEmptyLineCount: 0, denseLineCount: 0, charCount: 0, maxLineLength: 0, maxOccupiedColumns: 0, maxOccupiedWidthRatio: 0, medianOccupiedWidthRatio: 0, meanOccupiedWidthRatio: 0, wideLineCount: 0, uniqueTrimmedLineCount: 0, firstNonEmptyLine: null, lastNonEmptyLine: null },
  }), []);
  const emptyStyle = useCallback((): TerminalVisibleStyleSnapshot => ({
    cols: null, rows: null, viewportY: null, lineCount: 0, lines: [],
    summary: { styledCellCount: 0, styledLineCount: 0, boldCellCount: 0, italicCellCount: 0, underlineCellCount: 0, inverseCellCount: 0, fgPaletteCellCount: 0, fgRgbCellCount: 0, bgPaletteCellCount: 0, bgRgbCellCount: 0, uniqueStyleCount: 0 },
  }), []);

  return useMemo(() => ({
    setTerminalHandle,
    handleTerminalReady,
    handleTerminalInput,
    handleTerminalResize,
    focusPane: (paneId: string, retries = 20) => {
      const focus = (remaining: number) => {
        if (get(paneId)?.focus() || remaining <= 0) return;
        window.setTimeout(() => focus(remaining - 1), 50);
      };
      focus(retries);
    },
    fitPane: (paneId: string) => get(paneId)?.fit(),
    fitActivePane: () => get(activePaneId)?.fit(),
    typeTextViaPaneInput: (paneId: string, text: string) => get(paneId)?.typeTextViaInput(text) ?? false,
    isPaneInputFocused: (paneId: string) => get(paneId)?.isInputFocused() ?? false,
    scrollPaneToTop: (paneId: string) => get(paneId)?.scrollToTop() ?? false,
    getPaneText: (paneId: string) => get(paneId)?.getText() ?? '',
    getPaneSize: (paneId: string) => get(paneId)?.getSize() ?? null,
    getPaneVisibleContent: (paneId: string) => get(paneId)?.getVisibleContent() ?? emptyContent(),
    getPaneVisibleStyleSummary: (paneId: string) => get(paneId)?.getVisibleStyleSummary() ?? emptyStyle(),
    resetPaneTerminal: (paneId: string) => { const terminal = get(paneId); if (!terminal) return false; terminal.reset(); return true; },
    injectPaneBytes: async (paneId: string, bytes: Uint8Array) => { const terminal = get(paneId); if (!terminal) return false; await terminal.write(bytes); return true; },
    injectPaneBase64: async (paneId: string, data: string) => { const terminal = get(paneId); if (!terminal) return false; await terminal.write(decodePtyBytes(data)); return true; },
    drainPaneTerminal: async (paneId: string) => { const terminal = get(paneId); if (!terminal) return false; await terminal.drain(); return true; },
  }), [activePaneId, emptyContent, emptyStyle, get, handleTerminalInput, handleTerminalReady, handleTerminalResize, setTerminalHandle]);
}
