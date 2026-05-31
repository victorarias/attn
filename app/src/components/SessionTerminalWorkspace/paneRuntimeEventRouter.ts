import { useEffect, useMemo } from 'react';
import { listenPtyEvents, type PtyEventPayload } from '../../pty/bridge';

export interface PaneRuntimeEventBinding {
  sessionId?: string;
  paneId: string;
  runtimeId: string;
  onEvent: (event: PtyEventPayload) => void;
}

export interface PaneRuntimeEventRouter {
  registerBinding: (binding: PaneRuntimeEventBinding) => () => void;
}

interface RegisteredBinding {
  token: symbol;
  binding: PaneRuntimeEventBinding;
}

export interface PaneRuntimeEventRouterController extends PaneRuntimeEventRouter {
  dispose: () => void;
  handleEvent: (event: PtyEventPayload) => void;
}

export function createPaneRuntimeEventRouterController(): PaneRuntimeEventRouterController {
  const bindings = new Map<string, RegisteredBinding>();

  const registerBinding = (binding: PaneRuntimeEventBinding) => {
    const token = Symbol(binding.runtimeId);
    bindings.set(binding.runtimeId, { token, binding });

    return () => {
      const current = bindings.get(binding.runtimeId);
      if (!current || current.token !== token) {
        return;
      }
      bindings.delete(binding.runtimeId);
    };
  };

  const handleEvent = (event: PtyEventPayload) => {
    const match = bindings.get(event.id);
    if (!match) {
      return;
    }
    match.binding.onEvent(event);
  };

  const dispose = () => {
    bindings.clear();
  };

  return {
    registerBinding,
    handleEvent,
    dispose,
  };
}

export function usePaneRuntimeEventRouter(): PaneRuntimeEventRouter {
  const controller = useMemo(() => createPaneRuntimeEventRouterController(), []);

  useEffect(() => {
    let active = true;
    let disposeListener: (() => void) | null = null;

    void listenPtyEvents((event) => {
      controller.handleEvent(event.payload);
    }).then((dispose) => {
      if (!active) {
        dispose();
        return;
      }
      disposeListener = dispose;
    });

    return () => {
      active = false;
      disposeListener?.();
      controller.dispose();
    };
  }, [controller]);

  return controller;
}
