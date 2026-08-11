import type { ChildProcess } from 'child_process';
import * as fs from 'fs';
import * as path from 'path';

function processExitReason(code: number | null, signal: NodeJS.Signals | null): string {
  if (signal) return `signal ${signal}`;
  if (code !== null) return `code ${code}`;
  return 'an unknown status';
}

// Daemon startup has no healthy duration contract: a loaded CI runner can delay
// the child without making it unhealthy. Readiness is the socket appearing;
// failure is the child exiting first. The Playwright test budget remains the
// outer tripwire for a process that is alive but genuinely wedged.
export function waitForDaemonSocket(
  proc: ChildProcess,
  socketPath: string,
  getDebugInfo?: () => string,
): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    let settled = false;
    let watcher: fs.FSWatcher | undefined;

    const finish = (complete: () => void) => {
      if (settled) return;
      settled = true;
      proc.off('exit', onExit);
      if (watcher) {
        watcher.off('error', onWatchError);
        watcher.close();
      }
      complete();
    };

    const debugSuffix = () => {
      const debug = getDebugInfo?.().trim();
      return debug ? `\n${debug}` : '';
    };

    const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
      finish(() => reject(new Error(
        `Daemon exited before creating socket ${socketPath} with ${processExitReason(code, signal)}.${debugSuffix()}`,
      )));
    };

    const onWatchError = (error: Error) => {
      finish(() => reject(new Error(
        `Failed to watch for daemon socket ${socketPath}: ${error.message}.${debugSuffix()}`,
      )));
    };

    const checkReady = () => {
      if (fs.existsSync(socketPath)) {
        finish(resolve);
      }
    };

    proc.once('exit', onExit);
    try {
      watcher = fs.watch(path.dirname(socketPath), checkReady);
      watcher.once('error', onWatchError);
    } catch (error) {
      onWatchError(error instanceof Error ? error : new Error(String(error)));
      return;
    }

    // Close both setup races: the child may have exited or created the socket
    // before its listener/watch was installed.
    if (proc.exitCode !== null || proc.signalCode !== null) {
      onExit(proc.exitCode, proc.signalCode);
      return;
    }
    checkReady();
  });
}
