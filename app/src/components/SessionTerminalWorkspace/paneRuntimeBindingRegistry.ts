import { Terminal as XTerm } from '@xterm/xterm';
import type { PtyEventPayload } from '../../pty/bridge';
import type { PaneRuntimeEventRouter } from './paneRuntimeEventRouter';

export interface PaneRuntimeBindingSpec {
  paneId: string;
  runtimeId: string;
  testSessionId?: string;
  sessionId?: string;
}

export interface PaneRuntimeBindingRegistration {
  paneId: string;
  testSessionId?: string;
  dispose: () => void;
}

interface SyncPaneRuntimeBindingsArgs {
  panes: PaneRuntimeBindingSpec[];
  eventRouter: PaneRuntimeEventRouter;
  registrations: Map<string, PaneRuntimeBindingRegistration>;
  getXterm: (paneId: string) => XTerm | undefined;
  onUnmountedPaneEvent: (pane: PaneRuntimeBindingSpec, msg: PtyEventPayload) => void;
  onMountedPaneEvent: (pane: PaneRuntimeBindingSpec, xterm: XTerm, msg: PtyEventPayload) => void;
  onDisposeStaleBinding: (registration: PaneRuntimeBindingRegistration, runtimeId: string) => void;
  onRegisterBinding: (pane: PaneRuntimeBindingSpec) => void;
}

export function syncPaneRuntimeBindings({
  panes,
  eventRouter,
  registrations,
  getXterm,
  onUnmountedPaneEvent,
  onMountedPaneEvent,
  onDisposeStaleBinding,
  onRegisterBinding,
}: SyncPaneRuntimeBindingsArgs): void {
  const desiredRuntimeIds = new Set(panes.map((pane) => pane.runtimeId));

  for (const [runtimeId, registration] of registrations.entries()) {
    if (desiredRuntimeIds.has(runtimeId)) {
      continue;
    }
    registration.dispose();
    registrations.delete(runtimeId);
    onDisposeStaleBinding(registration, runtimeId);
  }

  for (const pane of panes) {
    const existing = registrations.get(pane.runtimeId);
    if (existing && existing.paneId === pane.paneId && existing.testSessionId === pane.testSessionId) {
      continue;
    }
    existing?.dispose();

    const dispose = eventRouter.registerBinding({
      sessionId: pane.testSessionId,
      paneId: pane.paneId,
      runtimeId: pane.runtimeId,
      onEvent: (msg) => {
        const xterm = getXterm(pane.paneId);
        if (!xterm) {
          onUnmountedPaneEvent(pane, msg);
          return;
        }
        onMountedPaneEvent(pane, xterm, msg);
      },
    });

    registrations.set(pane.runtimeId, {
      paneId: pane.paneId,
      testSessionId: pane.testSessionId,
      dispose,
    });
    onRegisterBinding(pane);
  }
}

export function disposePaneRuntimeBindings(
  registrations: Map<string, PaneRuntimeBindingRegistration>,
): void {
  for (const registration of registrations.values()) {
    registration.dispose();
  }
  registrations.clear();
}
