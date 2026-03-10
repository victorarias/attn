import { fireEvent, render, screen } from '@testing-library/react';
import { forwardRef, useImperativeHandle } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionTerminalWorkspace } from './SessionTerminalWorkspace';
import type { PaneRuntimeEventRouter } from './SessionTerminalWorkspace/paneRuntimeEventRouter';
import { MAIN_TERMINAL_PANE_ID, createDefaultWorkspaceState } from '../types/workspace';

const { registeredShortcuts } = vi.hoisted(() => ({
  registeredShortcuts: new Map<string, () => void>(),
}));

const mockEventRouter: PaneRuntimeEventRouter = {
  registerBinding: vi.fn(() => () => {}),
};

vi.mock('../pty/bridge', () => ({
  listenPtyEvents: vi.fn(() => Promise.resolve(() => {})),
  ptyResize: vi.fn(() => Promise.resolve()),
  ptySpawn: vi.fn(() => Promise.resolve()),
  ptyWrite: vi.fn(() => Promise.resolve()),
}));

const { mockTerminalFocus } = vi.hoisted(() => ({
  mockTerminalFocus: vi.fn(() => true),
}));

vi.mock('./Terminal', () => ({
  Terminal: forwardRef((_props, ref) => {
    useImperativeHandle(ref, () => ({
      terminal: {} as any,
      fit: vi.fn(),
      focus: mockTerminalFocus,
    }));
    return <div data-testid="mock-terminal">Main terminal</div>;
  }),
}));

vi.mock('../shortcuts', () => ({
  useShortcut: vi.fn((id: string, handler: () => void, enabled?: boolean) => {
    if (enabled) {
      registeredShortcuts.set(id, handler);
      return;
    }
    registeredShortcuts.delete(id);
  }),
}));

vi.mock('../shortcuts/useShortcut', () => ({
  triggerShortcut: vi.fn(() => false),
}));

describe('SessionTerminalWorkspace', () => {
  afterEach(() => {
    registeredShortcuts.clear();
    vi.mocked(mockEventRouter.registerBinding).mockClear();
    vi.useRealTimers();
  });

  it('retries focus for the main pane until the terminal handle is ready', () => {
    vi.useFakeTimers();
    mockTerminalFocus
      .mockReset()
      .mockReturnValueOnce(false)
      .mockReturnValueOnce(false)
      .mockReturnValue(true);

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        sessionLabel="Session 1"
        sessionAgent="claude"
        cwd="/tmp/repo"
        workspace={createDefaultWorkspaceState()}
        activePaneId={MAIN_TERMINAL_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        getMainPaneSpawnArgs={vi.fn(() => null)}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(mockTerminalFocus).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(50);
    expect(mockTerminalFocus).toHaveBeenCalledTimes(2);

    vi.advanceTimersByTime(50);
    expect(mockTerminalFocus).toHaveBeenCalledTimes(3);
  });

  it('focuses the main Claude pane immediately on mouse down', () => {
    mockTerminalFocus.mockReset().mockReturnValue(true);
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        sessionLabel="Session 1"
        sessionAgent="claude"
        cwd="/tmp/repo"
        workspace={{
          ...createDefaultWorkspaceState(),
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
              { type: 'pane', paneId: 'pane-shell-1' },
            ],
          },
        }}
        activePaneId={MAIN_TERMINAL_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        getMainPaneSpawnArgs={vi.fn(() => null)}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    fireEvent.mouseDown(screen.getByText('Main terminal'));

    expect(onFocusPane).toHaveBeenCalledWith(MAIN_TERMINAL_PANE_ID);
    expect(mockTerminalFocus).toHaveBeenCalled();
  });

  it('focuses a utility pane immediately on mouse down', () => {
    mockTerminalFocus.mockReset().mockReturnValue(true);
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        sessionLabel="Session 1"
        sessionAgent="claude"
        cwd="/tmp/repo"
        workspace={{
          terminals: [{ id: 'pane-shell-1', ptyId: 'runtime-shell-1', title: 'Shell 1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
              { type: 'pane', paneId: 'pane-shell-1' },
            ],
          },
        }}
        activePaneId={MAIN_TERMINAL_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        getMainPaneSpawnArgs={vi.fn(() => null)}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    fireEvent.mouseDown(screen.getByText('Shell 1'));

    expect(onFocusPane).toHaveBeenCalledWith('pane-shell-1');
    expect(mockTerminalFocus).toHaveBeenCalled();
  });

  it('moves focus to the pane below on cmd+alt+down', () => {
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        sessionLabel="Session 1"
        sessionAgent="claude"
        cwd="/tmp/repo"
        workspace={{
          terminals: [],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
              {
                type: 'split',
                splitId: 'right',
                direction: 'horizontal',
                ratio: 0.5,
                children: [
                  { type: 'pane', paneId: 'top-right' },
                  { type: 'pane', paneId: 'bottom-right' },
                ],
              },
            ],
          },
        }}
        activePaneId="top-right"
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        getMainPaneSpawnArgs={vi.fn(() => null)}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    registeredShortcuts.get('terminal.focusDown')?.();

    expect(onFocusPane).toHaveBeenCalledWith('bottom-right');
  });

  it('navigates to the next session when moving right from the edge', () => {
    const onNavigateOutOfSession = vi.fn();

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        sessionLabel="Session 1"
        sessionAgent="claude"
        cwd="/tmp/repo"
        workspace={{
          terminals: [],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: MAIN_TERMINAL_PANE_ID },
              { type: 'pane', paneId: 'right' },
            ],
          },
        }}
        activePaneId="right"
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        getMainPaneSpawnArgs={vi.fn(() => null)}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={onNavigateOutOfSession}
      />
    );

    registeredShortcuts.get('terminal.focusRight')?.();

    expect(onNavigateOutOfSession).toHaveBeenCalledWith('right');
  });
});
