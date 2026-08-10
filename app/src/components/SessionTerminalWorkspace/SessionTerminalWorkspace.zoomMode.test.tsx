import { useState, useSyncExternalStore } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render } from '@testing-library/react';
import { SessionTerminalWorkspace } from './index';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';
import type { TerminalWorkspaceState } from '../../types/workspace';

// Zoom mode is a layout flag — no terminal needed, and the real one drags in
// the Ghostty WASM model.
vi.mock('../GhosttyTerminal', async () => {
  const React = await import('react');
  return {
    GhosttyTerminal: React.forwardRef(function MockTerminal() {
      return null;
    }),
  };
});

function singlePaneWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: 'pane-a', runtimeId: 'rt-a', sessionId: 'session-a', title: 'shell' }],
    layoutTree: { type: 'pane', paneId: 'pane-a' },
  };
}

// Stands in for the daemon session store: a source that pushes new values at
// React from outside the render cycle, the way a WebSocket snapshot burst does.
function createBurstStore() {
  const listeners = new Set<() => void>();
  let version = 0;
  return {
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    getSnapshot() {
      return version;
    },
    push() {
      version += 1;
      for (const listener of listeners) {
        listener();
      }
    },
  };
}

// App's shape around zoom mode: it owns the flag, re-renders on every daemon
// session snapshot, and hands the workspace a fresh inline setter each time.
function renderUnderBurstingParent() {
  const onSetZoomActive = vi.fn();
  const store = createBurstStore();

  function Harness() {
    useSyncExternalStore(store.subscribe, store.getSnapshot);
    const [zoomBySession, setZoomBySession] = useState<Record<string, boolean>>({});
    return (
      <SessionTerminalWorkspace
        workspaceId="workspace-a"
        workspace={singlePaneWorkspace()}
        activePaneId="pane-a"
        fontSize={13}
        enabled
        isActiveSession
        eventRouter={createPaneRuntimeEventRouterController()}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        zoomActive={Boolean(zoomBySession['workspace-a'])}
        onSetZoomActive={(active) => {
          onSetZoomActive(active);
          setZoomBySession((prev) => (
            prev['workspace-a'] === active ? prev : { ...prev, 'workspace-a': active }
          ));
        }}
      />
    );
  }

  const view = render(<Harness />);
  return { ...view, onSetZoomActive, store };
}

function zoomedPaneId(container: HTMLElement) {
  return container
    .querySelector('[data-session-terminal-workspace="workspace-a"]')
    ?.getAttribute('data-zoomed-pane-id') ?? null;
}

describe('SessionTerminalWorkspace zoom mode', () => {
  // Regression: the workspace used to keep its own zoom flag and push it back
  // to App from an effect that depended on App's inline callback. Since App
  // writes what it receives into its own state, every parent render produced
  // another write and another parent render. A burst of daemon session
  // snapshots keeps that cycle from settling and React aborts the tree with
  // error #185 (maximum update depth). Zoom mode now lives in App, so the
  // workspace only ever writes it from the shortcut.
  it('never writes zoom mode back to the parent on its own', () => {
    const { onSetZoomActive, store } = renderUnderBurstingParent();

    expect(onSetZoomActive).not.toHaveBeenCalled();

    for (let i = 0; i < 300; i += 1) {
      act(() => {
        store.push();
      });
    }

    expect(onSetZoomActive).not.toHaveBeenCalled();
  });

  it('toggles zoom mode through the parent', () => {
    const { container, onSetZoomActive } = renderUnderBurstingParent();

    expect(zoomedPaneId(container)).toBe('');

    fireEvent.keyDown(window, { key: 'z', metaKey: true, shiftKey: true });

    expect(onSetZoomActive.mock.calls).toEqual([[true]]);
    expect(zoomedPaneId(container)).toBe('pane-a');

    fireEvent.keyDown(window, { key: 'z', metaKey: true, shiftKey: true });

    expect(onSetZoomActive.mock.calls).toEqual([[true], [false]]);
    expect(zoomedPaneId(container)).toBe('');
  });

  it('keeps zoom mode across a burst of session updates', () => {
    const { container, store } = renderUnderBurstingParent();

    fireEvent.keyDown(window, { key: 'z', metaKey: true, shiftKey: true });
    expect(zoomedPaneId(container)).toBe('pane-a');

    for (let i = 0; i < 300; i += 1) {
      act(() => {
        store.push();
      });
    }

    expect(zoomedPaneId(container)).toBe('pane-a');
  });
});
