import { Terminal as XTerm } from '@xterm/xterm';
import type { TerminalHandle } from '../Terminal';
import type { PaneRuntimeLifecycleRegistry } from './paneRuntimeLifecycleState';

interface PruneInactivePaneStateArgs {
  activePaneIds: Set<string>;
  terminalHandles: Map<string, TerminalHandle>;
  xterms: Map<string, XTerm>;
  paneRuntimeLifecycle: PaneRuntimeLifecycleRegistry<XTerm>;
}

export function pruneInactivePaneState({
  activePaneIds,
  terminalHandles,
  xterms,
  paneRuntimeLifecycle,
}: PruneInactivePaneStateArgs): void {
  for (const paneId of Array.from(terminalHandles.keys())) {
    if (!activePaneIds.has(paneId)) {
      terminalHandles.delete(paneId);
    }
  }
  for (const paneId of Array.from(xterms.keys())) {
    if (!activePaneIds.has(paneId)) {
      xterms.delete(paneId);
    }
  }
  for (const [paneId] of paneRuntimeLifecycle.entries()) {
    if (!activePaneIds.has(paneId)) {
      paneRuntimeLifecycle.dispose(paneId);
    }
  }
}
