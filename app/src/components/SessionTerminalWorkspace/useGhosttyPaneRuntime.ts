import { useCallback, useEffect, useMemo, useRef } from 'react';
import { ptyAttach, ptyDetach, ptyResize, ptyWrite, type PtyEventPayload } from '../../pty/bridge';
import { isSuspiciousTerminalSize } from '../../utils/terminalDebug';
import { recordFocus } from '../../utils/terminalDiagnosticsLog';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import type { GhosttyTerminalHandle } from '../GhosttyTerminal';
import type { TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';
import type { TerminalVisibleStyleSnapshot } from '../../utils/terminalStyleSummary';

const HISTORICAL_REPLAY_CHUNK_BYTES = 16 * 1024;

interface TerminalResizeOptions {
  reason?: string;
}

export interface PaneRuntimeSpec {
  paneId: string;
  runtimeId: string;
  paneKind: 'agent';
  agent?: string;
  sessionId?: string;
  testSessionId?: string;
}

function decodePtyBytes(payload: string | Uint8Array): Uint8Array {
  if (typeof payload !== 'string') {
    return payload;
  }
  const binary = atob(payload);
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

export interface GhosttyPaneRuntime {
  setTerminalHandle: (paneId: string, handle: GhosttyTerminalHandle | null) => void;
  handleTerminalReady: (paneId: string) => (terminal: GhosttyTerminalHandle) => Promise<void>;
  handleTerminalInput: (paneId: string) => (data: string) => void;
  handleTerminalResize: (paneId: string) => (cols: number, rows: number, options?: TerminalResizeOptions) => void;
  focusPane: (paneId: string, retries?: number) => void;
  fitPane: (paneId: string) => void;
  fitActivePane: () => void;
  openFindInActivePane: () => void;
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
  terminalsLive = true,
): GhosttyPaneRuntime {
  const panesRef = useRef(panes);
  const handlesRef = useRef(new Map<string, GhosttyTerminalHandle>());
  const readyRuntimesRef = useRef(new Set<string>());
  const attachedRuntimesRef = useRef(new Set<string>());
  const connectingRef = useRef(new Map<string, {
    generation: number;
    promise: Promise<void>;
  }>());
  const attachGenerationRef = useRef(new Map<string, number>());
  const pendingResizeRef = useRef(new Map<string, {
    cols: number;
    rows: number;
    reason: string;
  }>());
  const terminalsLiveRef = useRef(terminalsLive);
  panesRef.current = panes;
  terminalsLiveRef.current = terminalsLive;

  const paneFor = useCallback((paneId: string) => panesRef.current.find((pane) => pane.paneId === paneId), []);
  const cancelRuntimeConnection = useCallback((runtimeId: string) => {
    attachGenerationRef.current.set(
      runtimeId,
      (attachGenerationRef.current.get(runtimeId) ?? 0) + 1,
    );
    attachedRuntimesRef.current.delete(runtimeId);
    pendingResizeRef.current.delete(runtimeId);
    void ptyDetach({ id: runtimeId });
  }, []);

  const deliverEvent = useCallback((paneId: string, event: PtyEventPayload) => {
    const terminal = handlesRef.current.get(paneId);
    if (!terminal) return;
    const pane = paneFor(paneId);
    if (pane) {
      readyRuntimesRef.current.add(pane.runtimeId);
    }
    switch (event.event) {
      case 'data': {
        const bytes = decodePtyBytes(event.data);
        const suppressResponses = event.suppressResponses ?? event.source === 'attach_replay';
        if (event.source === 'attach_replay') {
          for (let offset = 0; offset < bytes.length; offset += HISTORICAL_REPLAY_CHUNK_BYTES) {
            void terminal.write(
              bytes.subarray(offset, offset + HISTORICAL_REPLAY_CHUNK_BYTES),
              {
                suppressResponses,
                yieldBefore: true,
                deferRender: true,
                historicalReplay: true,
              },
            );
          }
          break;
        }
        void terminal.write(bytes, { suppressResponses });
        break;
      }
      case 'local_resize':
        void terminal.resizeLocal(
          event.cols,
          event.rows,
          { historicalReplay: event.source === 'attach_replay' },
        );
        break;
      case 'replay_complete':
        void terminal.drain().then(() => {
          if (isActiveSessionRef.current) {
            terminal.fit();
          }
        });
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
  }, [isActiveSessionRef, paneFor]);

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
    for (const runtimeId of pendingResizeRef.current.keys()) {
      if (!desiredRuntimeIds.has(runtimeId)) pendingResizeRef.current.delete(runtimeId);
    }
    const connectedRuntimeIds = new Set([
      ...attachedRuntimesRef.current,
      ...connectingRef.current.keys(),
    ]);
    for (const runtimeId of connectedRuntimeIds) {
      if (!desiredRuntimeIds.has(runtimeId)) {
        cancelRuntimeConnection(runtimeId);
      }
    }
  }, [cancelRuntimeConnection, panes]);

  useEffect(() => {
    if (terminalsLive) {
      return;
    }
    for (const pane of panesRef.current) {
      cancelRuntimeConnection(pane.runtimeId);
    }
  }, [cancelRuntimeConnection, terminalsLive]);

  useEffect(() => () => {
    for (const pane of panesRef.current) {
      cancelRuntimeConnection(pane.runtimeId);
    }
  }, [cancelRuntimeConnection]);

  const setTerminalHandle = useCallback((paneId: string, handle: GhosttyTerminalHandle | null) => {
    if (handle) {
      handlesRef.current.set(paneId, handle);
      return;
    }
    handlesRef.current.delete(paneId);
  }, []);

  const handleTerminalReady = useCallback((paneId: string) => async (terminal: GhosttyTerminalHandle) => {
    handlesRef.current.set(paneId, terminal);
    const pane = paneFor(paneId);
    if (!pane) return;
    const terminalIsCurrent = () => (
      terminalsLiveRef.current
      && panesRef.current.some((entry) => (
        entry.paneId === paneId && entry.runtimeId === pane.runtimeId
      ))
      && handlesRef.current.get(paneId) === terminal
    );
    while (true) {
      const inFlightAttach = connectingRef.current.get(pane.runtimeId);
      if (!inFlightAttach) break;
      try {
        await inFlightAttach.promise;
      } catch {
        // The owning attach call reports current-generation failures.
      }
      if (!terminalIsCurrent()) return;
    }
    const size = terminal.getSize();
    if (!size || !terminalIsCurrent()) return;
    const attachPolicy = readyRuntimesRef.current.has(pane.runtimeId)
      ? 'same_app_remount'
      : 'fresh_spawn';
    if (import.meta.env.DEV && pane.testSessionId) {
      const testWindow = window as Window & {
        __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
      };
      testWindow.__TEST_SESSION_INPUT_EVENTS = testWindow.__TEST_SESSION_INPUT_EVENTS || [];
      testWindow.__TEST_SESSION_INPUT_EVENTS.push({ sessionId: pane.testSessionId, event: 'connect_terminal' });
    }
    const attachGeneration = (attachGenerationRef.current.get(pane.runtimeId) ?? 0) + 1;
    attachGenerationRef.current.set(pane.runtimeId, attachGeneration);
    const attachPromise = ptyAttach({
      args: {
        id: pane.runtimeId,
        cols: size.cols,
        rows: size.rows,
        shell: false,
        agent: pane.agent,
        policy: attachPolicy,
      },
      forceResizeBeforeAttach: attachPolicy === 'same_app_remount',
    });
    connectingRef.current.set(pane.runtimeId, {
      generation: attachGeneration,
      promise: attachPromise,
    });
    try {
      await attachPromise;
      const attachStillCurrent = attachGenerationRef.current.get(pane.runtimeId) === attachGeneration
        && terminalIsCurrent();
      if (!attachStillCurrent) {
        return;
      }
      readyRuntimesRef.current.add(pane.runtimeId);
      attachedRuntimesRef.current.add(pane.runtimeId);
      const pendingResize = pendingResizeRef.current.get(pane.runtimeId);
      pendingResizeRef.current.delete(pane.runtimeId);
      if (pendingResize && isActiveSessionRef.current) {
        void ptyResize({
          id: pane.runtimeId,
          cols: pendingResize.cols,
          rows: pendingResize.rows,
          reason: pendingResize.reason,
        });
      }
    } catch (error) {
      if (attachGenerationRef.current.get(pane.runtimeId) === attachGeneration) {
        pendingResizeRef.current.delete(pane.runtimeId);
        await terminal.write(`\r\n[Failed to attach PTY: ${String(error)}]\r\n`);
      }
    } finally {
      if (connectingRef.current.get(pane.runtimeId)?.generation === attachGeneration) {
        connectingRef.current.delete(pane.runtimeId);
      }
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

  const handleTerminalResize = useCallback((paneId: string) => (cols: number, rows: number, options?: TerminalResizeOptions) => {
    if (!isActiveSessionRef.current) return;
    const pane = paneFor(paneId);
    if (!pane) return;
    if (isSuspiciousTerminalSize(cols, rows)) return;
    const reason = options?.reason ?? 'ghostty_fit';
    if (!readyRuntimesRef.current.has(pane.runtimeId)) {
      pendingResizeRef.current.set(pane.runtimeId, { cols, rows, reason });
      return;
    }
    void ptyResize({
      id: pane.runtimeId,
      cols,
      rows,
      reason,
    });
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
      recordFocus(paneId, retries);
      const focus = (remaining: number) => {
        if (get(paneId)?.focus() || remaining <= 0) return;
        window.setTimeout(() => focus(remaining - 1), 50);
      };
      focus(retries);
    },
    fitPane: (paneId: string) => get(paneId)?.fit(),
    fitActivePane: () => get(activePaneId)?.fit(),
    openFindInActivePane: () => get(activePaneId)?.openFind(),
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
