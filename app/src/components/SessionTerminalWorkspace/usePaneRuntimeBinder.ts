import { useCallback, useEffect, useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { ptyAttach, ptyResize, ptySpawn, ptyWrite, type PtyEventPayload, type PtySpawnArgs } from '../../pty/bridge';
import {
  isCommittedGeometryChanged,
  planGeometryFlush,
  planPendingGeometrySync,
} from '../../pty/geometryLifecycle';
import type { TerminalHandle } from '../Terminal';
import { activeElementSummary, recordPaneRuntimeDebugEvent } from '../../utils/paneRuntimeDebug';
import {
  createPaneRuntimeControls,
} from './paneRuntimeControls';
import { pruneInactivePaneState } from './paneRuntimeActivePaneCleanup';
import {
  disposePaneRuntimeBindings,
  syncPaneRuntimeBindings,
} from './paneRuntimeBindingRegistry';
import { ensurePaneRuntimeWithRetry } from './paneRuntimeEnsure';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import {
  createPaneRuntimeLifecycleRegistry,
  type PaneRuntimeSize,
} from './paneRuntimeLifecycleState';
import {
  decodePtyBytes,
  encodePtyBytes,
  installTerminalKeyHandler,
  writeToTerminal,
} from './paneRuntimeTerminalUtils';
import { resetTerminalScrollPin } from '../../utils/terminalScrollPin';
import { recordTerminalRuntimeLog } from '../../utils/terminalRuntimeLog';
import type { TerminalVisibleContentSnapshot } from '../../utils/terminalVisibleContent';

const MAX_PENDING_TERMINAL_EVENTS = 256;
const RUNTIME_ENSURE_TIMEOUT_MS = 20_000;
const RUNTIME_ENSURE_RETRY_DELAY_MS = 150;
const PTY_GEOMETRY_SETTLE_MS = 90;
const PTY_READY_SETTLE_MS = 60;
const PTY_REDRAW_BOUNCE_DELAY_MS = 16;
const terminalTextEncoder = new TextEncoder();
export { installTerminalKeyHandler };

interface TerminalReplyDetails {
  containsEscape: boolean;
  recognizedTerminalReply: boolean;
  replyKinds: Array<'da1' | 'cpr' | 'osc10' | 'osc11'>;
  cprResponses: Array<{ row: number; col: number }>;
}

function classifyTerminalReply(data: string): TerminalReplyDetails {
  const replyKinds = new Set<'da1' | 'cpr' | 'osc10' | 'osc11'>();
  const cprResponses: Array<{ row: number; col: number }> = [];

  if (/\x1b\[\?[0-9;]*c/.test(data)) {
    replyKinds.add('da1');
  }

  for (const match of data.matchAll(/\x1b\[(\d+);(\d+)R/g)) {
    replyKinds.add('cpr');
    cprResponses.push({
      row: Number(match[1]),
      col: Number(match[2]),
    });
  }

  for (const match of data.matchAll(/\x1b\](10|11);[\s\S]*?(?:\x07|\x1b\\)/g)) {
    replyKinds.add(match[1] === '10' ? 'osc10' : 'osc11');
  }

  return {
    containsEscape: data.includes('\x1b'),
    recognizedTerminalReply: replyKinds.size > 0,
    replyKinds: Array.from(replyKinds),
    cprResponses,
  };
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
  handleTerminalResize: (paneId: string) => (cols: number, rows: number, options?: { reason?: string }) => void;
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
  const pendingEnsuresRef = useRef<Map<string, Promise<void>>>(new Map());
  const ensuredRuntimeIdsRef = useRef<Set<string>>(new Set());
  const paneRuntimeLifecycle = useRef(createPaneRuntimeLifecycleRegistry<XTerm>()).current;
  const runtimeBindingDisposersRef = useRef<Map<string, { paneId: string; testSessionId?: string; dispose: () => void }>>(new Map());

  const getCurrentPane = useCallback((paneId: string): PaneRuntimeSpec | undefined => {
    return paneByIdRef.current.get(paneId) || panesRef.current.find((pane) => pane.paneId === paneId);
  }, []);

  const appendPendingTerminalEvent = useCallback((paneId: string, event: PtyEventPayload) => {
    paneRuntimeLifecycle.appendPendingTerminalEvent(paneId, event, MAX_PENDING_TERMINAL_EVENTS);
  }, [paneRuntimeLifecycle]);

  const queuePaneWriteTask = useCallback((
    paneId: string,
    task: () => Promise<void> | void,
  ) => {
    const state = paneRuntimeLifecycle.getWriteState(paneId);
    const next = state.writeChain.then(
      () => task(),
      () => task(),
    );
    state.writeChain = next.catch((error) => {
      console.error('[PaneRuntimeBinder] Terminal write task failed:', error);
    });
    return next;
  }, [paneRuntimeLifecycle]);

  const queueBufferedBytesForReplay = useCallback((
    paneId: string,
    bytes: Uint8Array,
    seq?: number,
    options?: { source?: Extract<PtyEventPayload, { event: 'data' }>['source'] },
  ) => {
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
      ...(options?.source ? { source: options.source } : {}),
    });
  }, [appendPendingTerminalEvent, getCurrentPane]);

  const recordPaneWriteActivity = useCallback((paneId: string, bytes: number, seq?: number) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }
    const now = performance.now();
    const state = paneRuntimeLifecycle.getWriteState(paneId);
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
  }, [getCurrentPane, paneRuntimeLifecycle]);

  const primeSpawnArgsForSize = useCallback((paneId: string, cols: number, rows: number) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }
    paneRuntimeLifecycle.ensure(paneId).spawnArgs = pane.getSpawnArgs({ cols, rows });
  }, [getCurrentPane, paneRuntimeLifecycle]);

  const enqueuePaneBytes = useCallback((
    paneId: string,
    xterm: XTerm,
    bytes: Uint8Array,
    seq?: number,
    options?: { suppressInput?: boolean },
  ) => {
    void queuePaneWriteTask(paneId, async () => {
      const liveXterm = xtermsRef.current.get(paneId);
      if (!liveXterm || liveXterm !== xterm) {
        queueBufferedBytesForReplay(paneId, bytes, seq, {
          source: options?.suppressInput ? 'attach_replay' : undefined,
        });
        return;
      }
      if (options?.suppressInput) {
        await paneRuntimeLifecycle.runWithInputSuppressed(paneId, () => writeToTerminal(liveXterm, bytes));
      } else {
        await writeToTerminal(liveXterm, bytes);
      }
      recordPaneWriteActivity(paneId, bytes.length, seq);
    });
  }, [paneRuntimeLifecycle, queueBufferedBytesForReplay, queuePaneWriteTask, recordPaneWriteActivity]);

  const replayPendingTerminalEvent = useCallback((paneId: string, xterm: XTerm, event: PtyEventPayload) => {
    switch (event.event) {
      case 'data':
        enqueuePaneBytes(paneId, xterm, decodePtyBytes(event.data), event.seq, {
          suppressInput: event.source === 'attach_replay',
        });
        break;
      case 'local_resize':
        void queuePaneWriteTask(paneId, async () => {
          const liveXterm = xtermsRef.current.get(paneId);
          if (!liveXterm || liveXterm !== xterm) {
            appendPendingTerminalEvent(paneId, event);
            return;
          }
          liveXterm.resize(event.cols, event.rows);
        });
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
    pruneInactivePaneState({
      activePaneIds,
      terminalHandles: terminalHandlesRef.current,
      xterms: xtermsRef.current,
      paneRuntimeLifecycle,
    });
  }, [activePaneId, paneRuntimeLifecycle, panes]);

  useEffect(() => {
    syncPaneRuntimeBindings({
      panes,
      eventRouter,
      registrations: runtimeBindingDisposersRef.current,
      getXterm: (paneId) => xtermsRef.current.get(paneId),
      onUnmountedPaneEvent: (pane, msg) => {
        recordPaneRuntimeDebugEvent({
          scope: 'pty',
          sessionId: pane.testSessionId,
          paneId: pane.paneId,
          runtimeId: pane.runtimeId,
          message: 'queue pty event for unmounted pane',
          details: { event: msg.event },
        });
        appendPendingTerminalEvent(pane.paneId, msg);
      },
      onMountedPaneEvent: (pane, xterm, msg) => {
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
      onDisposeStaleBinding: (registration, runtimeId) => {
        recordTerminalRuntimeLog({
          category: 'binding',
          sessionId: registration.testSessionId,
          paneId: registration.paneId,
          runtimeId,
          message: 'dispose stale runtime binding',
        });
      },
      onRegisterBinding: (pane) => {
        recordTerminalRuntimeLog({
          category: 'binding',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId: pane.paneId,
          runtimeId: pane.runtimeId,
          message: 'register pane runtime binding',
        });
      },
    });
  }, [appendPendingTerminalEvent, eventRouter, panes, replayPendingTerminalEvent]);

  useEffect(() => {
    return () => {
      paneRuntimeLifecycle.disposeAll();
      disposePaneRuntimeBindings(runtimeBindingDisposersRef.current);
      terminalHandlesRef.current.clear();
      xtermsRef.current.clear();
      ensuredRuntimeIdsRef.current.clear();
    };
  }, [paneRuntimeLifecycle]);

  const setTerminalHandle = useCallback((paneId: string, handle: TerminalHandle | null) => {
    if (!handle) {
      const pane = getCurrentPane(paneId);
      const currentXterm = xtermsRef.current.get(paneId);
      paneRuntimeLifecycle.clearGeometryTimer(paneId);
      const lifecycleState = paneRuntimeLifecycle.get(paneId);
      if (lifecycleState) {
        delete lifecycleState.pendingGeometrySync;
        delete lifecycleState.sameAppRemount;
      }
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        paneId,
        message: 'terminal ref cleared',
      });
      if (pane) {
        if (currentXterm && ensuredRuntimeIdsRef.current.has(pane.runtimeId)) {
          paneRuntimeLifecycle.ensure(paneId).sameAppRemount = { stage: 'armed' };
        } else {
          const state = paneRuntimeLifecycle.get(paneId);
          if (state) {
            delete state.sameAppRemount;
          }
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
      recordPaneRuntimeDebugEvent({
        scope: 'binder',
        paneId,
        message: 'terminal cleanup executed',
      });
      xtermsRef.current.delete(paneId);
      paneRuntimeLifecycle.clearInputSubscription(paneId);
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
  }, [getCurrentPane, paneRuntimeLifecycle]);

  const ensurePaneRuntime = useCallback(async (paneId: string, xterm: XTerm) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }
    if (import.meta.env.DEV && pane.testSessionId) {
      const testWindow = window as Window & {
        __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string }>;
      };
      testWindow.__TEST_SESSION_INPUT_EVENTS = testWindow.__TEST_SESSION_INPUT_EVENTS || [];
      testWindow.__TEST_SESSION_INPUT_EVENTS.push({ sessionId: pane.testSessionId, event: 'connect_terminal' });
    }

    await ensurePaneRuntimeWithRetry({
      paneId,
      xterm,
      pane,
      pendingEnsures: pendingEnsuresRef.current,
      ensuredRuntimeIds: ensuredRuntimeIdsRef.current,
      lifecycle: paneRuntimeLifecycle,
      spawnRuntime: async (spawnArgs) => {
        await ptySpawn({ args: spawnArgs });
      },
      getLiveXterm: (livePaneId) => xtermsRef.current.get(livePaneId),
      getCurrentPane,
      runtimeEnsureTimeoutMs: RUNTIME_ENSURE_TIMEOUT_MS,
      retryDelayMs: RUNTIME_ENSURE_RETRY_DELAY_MS,
      onReuseInFlight: () => {
        recordPaneRuntimeDebugEvent({
          scope: 'binder',
          sessionId: pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: 'reuse in-flight runtime ensure',
        });
      },
      onSkip: () => {
        recordPaneRuntimeDebugEvent({
          scope: 'binder',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: 'runtime ensure skipped',
        });
      },
      onAttempt: (attempt, spawnArgs) => {
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
      },
      onRetry: (attempt, elapsedMs, error) => {
        recordPaneRuntimeDebugEvent({
          scope: 'binder',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: 'runtime ensure retry scheduled',
          details: {
            attempt,
            elapsedMs: Math.round(elapsedMs),
            error,
          },
        });
      },
      onEnsured: () => {
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
      },
    });
  }, [getCurrentPane, paneRuntimeLifecycle]);

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
        paneRuntimeLifecycle.get(paneId)?.sameAppRemount?.stage === 'armed'
      );
      if (shouldHydrateRemount) {
        paneRuntimeLifecycle.ensure(paneId).sameAppRemount = { stage: 'hydrating', xterm };
        recordTerminalRuntimeLog({
          category: 'binding',
          event: 'runtime.hydrate.requested',
          sessionId: pane.sessionId ?? pane.testSessionId,
          paneId,
          runtimeId: pane.runtimeId,
          message: 'schedule runtime hydrate for remounted xterm',
          details: { cols: xterm.cols, rows: xterm.rows },
        });
      } else {
        const state = paneRuntimeLifecycle.get(paneId);
        if (state) {
          delete state.sameAppRemount;
        }
      }
    }

    const sendToPty = (data: string) => {
      const currentPane = getCurrentPane(paneId);
      if (!currentPane) {
        return;
      }
      const replyDetails = {
        dataPreview: data.slice(0, 20),
        dataLength: data.length,
        ...classifyTerminalReply(data),
      };
      if (paneRuntimeLifecycle.isInputSuppressed(paneId)) {
        recordPaneRuntimeDebugEvent({
          scope: 'input',
          sessionId: currentPane.testSessionId,
          paneId,
          runtimeId: currentPane.runtimeId,
          message: 'drop terminal-generated input during replay restore',
          details: replyDetails,
        });
        if (replyDetails.containsEscape) {
          recordTerminalRuntimeLog({
            category: 'input',
            event: 'terminal.reply.suppressed',
            sessionId: currentPane.sessionId ?? currentPane.testSessionId,
            paneId,
            runtimeId: currentPane.runtimeId,
            message: 'suppress terminal-generated reply during replay restore',
            details: replyDetails,
          });
        }
        return;
      }
      if (replyDetails.recognizedTerminalReply) {
        recordTerminalRuntimeLog({
          category: 'input',
          event: 'terminal.reply.forwarded',
          sessionId: currentPane.sessionId ?? currentPane.testSessionId,
          paneId,
          runtimeId: currentPane.runtimeId,
          message: 'forward terminal-generated reply to pty',
          details: replyDetails,
        });
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
        details: () => ({
          dataPreview: data.slice(0, 20),
          dataLength: data.length,
          ...activeElementSummary(),
        }),
      });
      ptyWrite({ id: currentPane.runtimeId, data, source: 'user' }).catch(console.error);
    };

    // Always subscribe onData — even if paneByIdRef doesn't have the pane
    // yet (child effect runs before parent effect). sendToPty resolves the
    // pane from the render-time snapshot until the effect-driven map catches up.
    paneRuntimeLifecycle.replaceInputSubscription(paneId, xterm.onData(sendToPty));
    xterm.attachCustomKeyEventHandler(installTerminalKeyHandler(sendToPty));

    const pendingEvents = paneRuntimeLifecycle.takePendingTerminalEvents(paneId);
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
    }
  }, [getCurrentPane, paneRuntimeLifecycle, replayPendingTerminalEvent]);

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
    paneRuntimeLifecycle.clearGeometryTimer(paneId);
    const lifecycleState = paneRuntimeLifecycle.get(paneId);
    const pending = lifecycleState?.pendingGeometrySync;
    if (!pending) {
      return;
    }
    delete lifecycleState.pendingGeometrySync;

    const pane = getCurrentPane(paneId);
    const liveXterm = xtermsRef.current.get(paneId);
    if (!pane || !liveXterm || liveXterm !== pending.xterm) {
      return;
    }

    primeSpawnArgsForSize(paneId, pending.cols, pending.rows);

    const lastCommitted = lifecycleState?.lastCommittedGeometry;
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
        geometryChanged,
        lastCommittedCols: lastCommitted?.cols ?? null,
        lastCommittedRows: lastCommitted?.rows ?? null,
      },
    });

    const spawnArgs = lifecycleState?.spawnArgs;
    const sameAppRemountState = lifecycleState?.sameAppRemount;
    const shouldHydrate = sameAppRemountState?.stage === 'hydrating' && sameAppRemountState.xterm === pending.xterm;
    const flushPlan = planGeometryFlush({
      runtimeReady: ensuredRuntimeIdsRef.current.has(pane.runtimeId),
      ensureInFlight: pendingEnsuresRef.current.has(pane.runtimeId),
      hydratePending: shouldHydrate,
      pending,
      lastCommitted,
      source,
    });

    if (flushPlan.kind === 'retry_after_pending_ensure') {
        paneRuntimeLifecycle.ensure(paneId).pendingGeometrySync = pending;
        const retryTimeoutId = window.setTimeout(() => {
          void flushPendingGeometrySync(paneId, source);
        }, PTY_REDRAW_BOUNCE_DELAY_MS);
        paneRuntimeLifecycle.ensure(paneId).pendingGeometryTimerId = retryTimeoutId;
        return;
    }

    if (flushPlan.kind === 'ensure_runtime') {
      try {
        await ensurePaneRuntime(paneId, pending.xterm);
      } catch (error) {
        reportRuntimeEnsureFailure(paneId, pending.xterm, source, error);
        return;
      }
      paneRuntimeLifecycle.ensure(paneId).lastCommittedGeometry = { cols: pending.cols, rows: pending.rows };
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
      const state = paneRuntimeLifecycle.ensure(paneId);
      delete state.sameAppRemount;
      state.lastCommittedGeometry = flushPlan.nextCommitted;
      return;
    }

    if (flushPlan.kind === 'resize_runtime') {
      ptyResize({
        id: pane.runtimeId,
        cols: flushPlan.resize.cols,
        rows: flushPlan.resize.rows,
        reason: flushPlan.resize.reason,
      }).catch(console.error);
      paneRuntimeLifecycle.ensure(paneId).lastCommittedGeometry = flushPlan.nextCommitted;
      return;
    }

  }, [ensurePaneRuntime, getCurrentPane, paneRuntimeLifecycle, primeSpawnArgsForSize, reportRuntimeEnsureFailure]);

  const scheduleGeometrySync = useCallback((
    paneId: string,
    xterm: XTerm,
    cols: number,
    rows: number,
    source: 'ready' | 'resize',
    options?: { reason?: string },
  ) => {
    const pane = getCurrentPane(paneId);
    if (!pane) {
      return;
    }

    primeSpawnArgsForSize(paneId, cols, rows);
    const lifecycleState = paneRuntimeLifecycle.ensure(paneId);
    const previous = lifecycleState.pendingGeometrySync;
    const sameAppRemountState = lifecycleState.sameAppRemount;
    const hydratingRemount = sameAppRemountState?.stage === 'hydrating' && sameAppRemountState.xterm === xterm;
    const runtimeReady = ensuredRuntimeIdsRef.current.has(pane.runtimeId);
    const sameGeometryAsCommitted = !isCommittedGeometryChanged(lifecycleState.lastCommittedGeometry, { cols, rows });

    if (runtimeReady && !hydratingRemount && sameGeometryAsCommitted) {
      paneRuntimeLifecycle.clearGeometryTimer(paneId);
      delete lifecycleState.pendingGeometrySync;
      recordTerminalRuntimeLog({
        category: 'geometry',
        event: 'pty.geometry.skipped_same_size',
        sessionId: pane.sessionId ?? pane.testSessionId,
        paneId,
        runtimeId: pane.runtimeId,
        message: 'skip settled PTY geometry for unchanged size',
        details: {
          source,
          reason: options?.reason ?? null,
          cols,
          rows,
          lastCommittedCols: lifecycleState.lastCommittedGeometry?.cols ?? null,
          lastCommittedRows: lifecycleState.lastCommittedGeometry?.rows ?? null,
        },
      });
      return;
    }

    const { pending, delayMs } = planPendingGeometrySync(previous, {
      cols,
      rows,
      xterm,
      source,
      reason: options?.reason,
      readyDelayMs: PTY_READY_SETTLE_MS,
      resizeDelayMs: PTY_GEOMETRY_SETTLE_MS,
    });

    lifecycleState.pendingGeometrySync = pending;
    paneRuntimeLifecycle.clearGeometryTimer(paneId);

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
      },
    });

    const timeoutId = window.setTimeout(() => {
      void flushPendingGeometrySync(paneId, source);
    }, delayMs);
    lifecycleState.pendingGeometryTimerId = timeoutId;
  }, [flushPendingGeometrySync, getCurrentPane, paneRuntimeLifecycle, primeSpawnArgsForSize]);

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
    const sameAppRemountState = paneRuntimeLifecycle.get(paneId)?.sameAppRemount;
    const hydratingRemount = sameAppRemountState?.stage === 'hydrating' && sameAppRemountState.xterm === xterm;
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
  }, [getCurrentPane, paneRuntimeLifecycle, scheduleGeometrySync, wireTerminal]);

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
    options?: { reason?: string },
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
        reason: options?.reason ?? null,
      },
    });
    const xterm = xtermsRef.current.get(paneId);
    if (xterm) {
      scheduleGeometrySync(paneId, xterm, cols, rows, 'resize', options);
    }
  }, [getCurrentPane, scheduleGeometrySync]);

  const controls = createPaneRuntimeControls({
    activePaneId,
    getCurrentPane,
    getTerminalHandle: (paneId) => terminalHandlesRef.current.get(paneId),
    getXterm: (paneId) => xtermsRef.current.get(paneId),
    clearPendingTerminalEvents: (paneId) => paneRuntimeLifecycle.clearPendingTerminalEvents(paneId),
    injectPanePayload,
    drainPaneWriteChain: (paneId) => paneRuntimeLifecycle.get(paneId)?.writeState?.writeChain,
  });

  return {
    setTerminalHandle,
    handleTerminalInit,
    handleTerminalReady,
    handleTerminalResize,
    ...controls,
  };
}
