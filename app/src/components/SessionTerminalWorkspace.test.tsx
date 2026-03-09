import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionTerminalWorkspace } from './SessionTerminalWorkspace';
import { MAIN_TERMINAL_PANE_ID, createDefaultPanelState } from '../types/workspace';

const { registeredShortcuts } = vi.hoisted(() => ({
  registeredShortcuts: new Map<string, () => void>(),
}));

vi.mock('../pty/bridge', () => ({
  listenPtyEvents: vi.fn(() => Promise.resolve(() => {})),
  ptyResize: vi.fn(() => Promise.resolve()),
  ptySpawn: vi.fn(() => Promise.resolve()),
  ptyWrite: vi.fn(() => Promise.resolve()),
}));

vi.mock('./Terminal', () => ({
  Terminal: () => <div data-testid="mock-terminal" />,
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
    vi.useRealTimers();
  });

  it('retries focus for the main pane until the terminal handle is ready', () => {
    vi.useFakeTimers();
    const focusMainPane = vi
      .fn<() => boolean>()
      .mockReturnValueOnce(false)
      .mockReturnValueOnce(false)
      .mockReturnValue(true);

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        cwd="/tmp/repo"
        panel={createDefaultPanelState()}
        fontSize={14}
        mainPane={<div>Main terminal</div>}
        focusMainPane={focusMainPane}
        enabled
        isActiveSession
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(focusMainPane).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(50);
    expect(focusMainPane).toHaveBeenCalledTimes(2);

    vi.advanceTimersByTime(50);
    expect(focusMainPane).toHaveBeenCalledTimes(3);
  });

  it('focuses the main Claude pane immediately on mouse down', () => {
    const focusMainPane = vi.fn<() => boolean>(() => true);
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        cwd="/tmp/repo"
        panel={{
          ...createDefaultPanelState(),
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
          activePaneId: MAIN_TERMINAL_PANE_ID,
        }}
        fontSize={14}
        mainPane={<div>Main terminal</div>}
        focusMainPane={focusMainPane}
        enabled
        isActiveSession
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    fireEvent.mouseDown(screen.getByText('Main terminal'));

    expect(onFocusPane).toHaveBeenCalledWith(MAIN_TERMINAL_PANE_ID);
    expect(focusMainPane).toHaveBeenCalled();
  });

  it('moves focus to the pane below on cmd+alt+down', () => {
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        cwd="/tmp/repo"
        panel={{
          activePaneId: 'top-right',
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
        fontSize={14}
        mainPane={<div>Main terminal</div>}
        focusMainPane={vi.fn(() => true)}
        enabled
        isActiveSession
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
        cwd="/tmp/repo"
        panel={{
          activePaneId: 'right',
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
        fontSize={14}
        mainPane={<div>Main terminal</div>}
        focusMainPane={vi.fn(() => true)}
        enabled
        isActiveSession
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
