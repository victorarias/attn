import { Terminal as XTerm } from '@xterm/xterm';
import type { PtySpawnArgs } from '../../pty/bridge';
import type { PaneRuntimeLifecycleRegistry, PaneRuntimeSize } from './paneRuntimeLifecycleState';
import { isRetryableRuntimeEnsureError, runtimeEnsureErrorMessage, waitForRuntimeEnsureRetry } from './paneRuntimeTerminalUtils';

export interface EnsurePaneRuntimeSpec {
  paneId: string;
  runtimeId: string;
  sessionId?: string;
  testSessionId?: string;
  getSpawnArgs: (size: PaneRuntimeSize) => PtySpawnArgs | null;
}

interface EnsurePaneRuntimeArgs {
  paneId: string;
  xterm: XTerm;
  pane: EnsurePaneRuntimeSpec;
  pendingEnsures: Map<string, Promise<void>>;
  ensuredRuntimeIds: Set<string>;
  lifecycle: PaneRuntimeLifecycleRegistry<XTerm>;
  spawnRuntime: (args: PtySpawnArgs) => Promise<void>;
  getLiveXterm: (paneId: string) => XTerm | undefined;
  getCurrentPane: (paneId: string) => EnsurePaneRuntimeSpec | undefined;
  runtimeEnsureTimeoutMs: number;
  retryDelayMs: number;
  onReuseInFlight: () => void;
  onSkip: () => void;
  onAttempt: (attempt: number, spawnArgs: PtySpawnArgs) => void;
  onRetry: (attempt: number, elapsedMs: number, error: string) => void;
  onEnsured: () => void;
}

export async function ensurePaneRuntimeWithRetry({
  paneId,
  xterm,
  pane,
  pendingEnsures,
  ensuredRuntimeIds,
  lifecycle,
  spawnRuntime,
  getLiveXterm,
  getCurrentPane,
  runtimeEnsureTimeoutMs,
  retryDelayMs,
  onReuseInFlight,
  onSkip,
  onAttempt,
  onRetry,
  onEnsured,
}: EnsurePaneRuntimeArgs): Promise<void> {
  if (ensuredRuntimeIds.has(pane.runtimeId)) {
    return;
  }

  const existing = pendingEnsures.get(pane.runtimeId);
  if (existing) {
    onReuseInFlight();
    await existing;
    return;
  }

  const promise = (async () => {
    let spawnArgs = lifecycle.get(paneId)?.spawnArgs;
    if (spawnArgs === undefined) {
      spawnArgs = pane.getSpawnArgs({
        cols: xterm.cols > 0 ? xterm.cols : 80,
        rows: xterm.rows > 0 ? xterm.rows : 24,
      });
      lifecycle.ensure(paneId).spawnArgs = spawnArgs;
    }
    if (!spawnArgs) {
      onSkip();
      return;
    }

    const startedAt = performance.now();
    let attempt = 0;

    for (;;) {
      attempt += 1;
      onAttempt(attempt, spawnArgs);
      try {
        await spawnRuntime(spawnArgs);
        break;
      } catch (error) {
        const elapsedMs = performance.now() - startedAt;
        if (!isRetryableRuntimeEnsureError(error) || elapsedMs >= runtimeEnsureTimeoutMs) {
          throw error;
        }
        onRetry(attempt, elapsedMs, runtimeEnsureErrorMessage(error));
        await waitForRuntimeEnsureRetry(retryDelayMs);
        if (getLiveXterm(paneId) !== xterm) {
          return;
        }
        const livePane = getCurrentPane(paneId);
        if (!livePane || livePane.runtimeId !== pane.runtimeId) {
          return;
        }
      }
    }

    ensuredRuntimeIds.add(pane.runtimeId);
    onEnsured();
  })().finally(() => {
    pendingEnsures.delete(pane.runtimeId);
  });

  pendingEnsures.set(pane.runtimeId, promise);
  await promise;
}
