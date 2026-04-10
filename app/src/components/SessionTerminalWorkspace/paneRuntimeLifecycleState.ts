import type { PtyEventPayload, PtySpawnArgs } from '../../pty/bridge';
import type { PendingGeometrySync } from '../../pty/geometryLifecycle';

export interface PaneRuntimeSize {
  cols: number;
  rows: number;
}

export interface PaneWriteState {
  writeChain: Promise<void>;
  lastLoggedAt: number;
  writeCount: number;
  bytes: number;
  totalWriteCount: number;
  lastWriteAt: number;
  lastSeq?: number;
}

export type SameAppRemountState<TXterm = unknown> =
  | { stage: 'armed' }
  | { stage: 'hydrating'; xterm: TXterm };

export interface PaneRuntimeLifecycleState<TXterm = unknown> {
  spawnArgs?: PtySpawnArgs | null;
  pendingGeometrySync?: PendingGeometrySync<TXterm>;
  pendingGeometryTimerId?: number;
  pendingTerminalEvents?: PtyEventPayload[];
  lastCommittedGeometry?: PaneRuntimeSize;
  sameAppRemount?: SameAppRemountState<TXterm>;
  inputSubscription?: { dispose: () => void };
  inputSuppressionDepth?: number;
  writeState?: PaneWriteState;
}

export interface PaneRuntimeLifecycleRegistry<TXterm = unknown> {
  get: (paneId: string) => PaneRuntimeLifecycleState<TXterm> | undefined;
  ensure: (paneId: string) => PaneRuntimeLifecycleState<TXterm>;
  entries: () => IterableIterator<[string, PaneRuntimeLifecycleState<TXterm>]>;
  getWriteState: (paneId: string) => PaneWriteState;
  appendPendingTerminalEvent: (paneId: string, event: PtyEventPayload, maxEvents: number) => void;
  takePendingTerminalEvents: (paneId: string) => PtyEventPayload[];
  clearPendingTerminalEvents: (paneId: string) => void;
  clearGeometryTimer: (paneId: string) => void;
  replaceInputSubscription: (paneId: string, subscription: { dispose: () => void }) => void;
  clearInputSubscription: (paneId: string) => void;
  isInputSuppressed: (paneId: string) => boolean;
  runWithInputSuppressed: <T>(paneId: string, task: () => Promise<T> | T) => Promise<T>;
  dispose: (paneId: string) => void;
  disposeAll: () => void;
}

export function createPaneRuntimeLifecycleRegistry<TXterm>(): PaneRuntimeLifecycleRegistry<TXterm> {
  const states = new Map<string, PaneRuntimeLifecycleState<TXterm>>();

  const get = (paneId: string) => states.get(paneId);

  const ensure = (paneId: string) => {
    let state = states.get(paneId);
    if (!state) {
      state = {};
      states.set(paneId, state);
    }
    return state;
  };

  const clearGeometryTimer = (paneId: string) => {
    const state = states.get(paneId);
    if (state?.pendingGeometryTimerId !== undefined) {
      window.clearTimeout(state.pendingGeometryTimerId);
      delete state.pendingGeometryTimerId;
    }
  };

  const clearInputSubscription = (paneId: string) => {
    const state = states.get(paneId);
    state?.inputSubscription?.dispose();
    if (state) {
      delete state.inputSubscription;
    }
  };

  return {
    get,
    ensure,
    entries: () => states.entries(),
    getWriteState: (paneId: string) => {
      const state = ensure(paneId);
      if (state.writeState === undefined) {
        const now = performance.now();
        state.writeState = {
          writeChain: Promise.resolve(),
          lastLoggedAt: now,
          writeCount: 0,
          bytes: 0,
          totalWriteCount: 0,
          lastWriteAt: 0,
        };
      }
      return state.writeState;
    },
    appendPendingTerminalEvent: (paneId: string, event: PtyEventPayload, maxEvents: number) => {
      const state = ensure(paneId);
      const events = state.pendingTerminalEvents || [];
      if (events.length >= maxEvents) {
        events.shift();
      }
      events.push(event);
      state.pendingTerminalEvents = events;
    },
    takePendingTerminalEvents: (paneId: string) => {
      const state = states.get(paneId);
      const events = state?.pendingTerminalEvents || [];
      if (state) {
        delete state.pendingTerminalEvents;
      }
      return events;
    },
    clearPendingTerminalEvents: (paneId: string) => {
      const state = states.get(paneId);
      if (state) {
        delete state.pendingTerminalEvents;
      }
    },
    clearGeometryTimer,
    replaceInputSubscription: (paneId: string, subscription: { dispose: () => void }) => {
      const state = ensure(paneId);
      state.inputSubscription?.dispose();
      state.inputSubscription = subscription;
    },
    clearInputSubscription,
    isInputSuppressed: (paneId: string) => {
      return (states.get(paneId)?.inputSuppressionDepth ?? 0) > 0;
    },
    runWithInputSuppressed: async <T>(paneId: string, task: () => Promise<T> | T) => {
      const state = ensure(paneId);
      state.inputSuppressionDepth = (state.inputSuppressionDepth ?? 0) + 1;
      try {
        return await task();
      } finally {
        const nextDepth = (state.inputSuppressionDepth ?? 1) - 1;
        if (nextDepth > 0) {
          state.inputSuppressionDepth = nextDepth;
        } else {
          delete state.inputSuppressionDepth;
        }
      }
    },
    dispose: (paneId: string) => {
      clearGeometryTimer(paneId);
      clearInputSubscription(paneId);
      states.delete(paneId);
    },
    disposeAll: () => {
      for (const paneId of Array.from(states.keys())) {
        clearGeometryTimer(paneId);
        clearInputSubscription(paneId);
      }
      states.clear();
    },
  };
}
