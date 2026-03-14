import { act, fireEvent, render, screen } from '@testing-library/react';
import { forwardRef, useImperativeHandle } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionTerminalWorkspace } from './SessionTerminalWorkspace';
import type { PaneRuntimeEventRouter } from './SessionTerminalWorkspace/paneRuntimeEventRouter';
import { MAIN_TERMINAL_PANE_ID, createDefaultWorkspaceState, type TerminalWorkspaceState } from '../types/workspace';

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

const { mockTerminalFit } = vi.hoisted(() => ({
  mockTerminalFit: vi.fn(),
}));

vi.mock('./Terminal', () => ({
  Terminal: forwardRef((_props, ref) => {
    useImperativeHandle(ref, () => ({
      terminal: {} as any,
      fit: mockTerminalFit,
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
    mockTerminalFit.mockReset();
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

  it('applies stored split ratios in the rendered layout', () => {
    const { container } = render(
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
            ratio: 0.3,
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
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    const split = container.querySelector('[data-split-id="root"]');
    const firstChild = container.querySelector('[data-split-id="root"] [data-split-child-index="0"]') as HTMLElement | null;
    const secondChild = container.querySelector('[data-split-id="root"] [data-split-child-index="1"]') as HTMLElement | null;

    expect(split).toHaveAttribute('data-split-ratio', '0.300');
    expect(firstChild?.style.flexGrow).toBe('0.3');
    expect(secondChild?.style.flexGrow).toBe('0.7');
  });

  it('lets zoom arm before splitting and applies once a split exists', () => {
    const { container, rerender } = render(
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

    act(() => {
      registeredShortcuts.get('terminal.toggleZoom')?.();
    });

    expect(container.querySelector('[data-session-terminal-workspace="session-1"]')).toHaveAttribute('data-zoomed-pane-id', MAIN_TERMINAL_PANE_ID);
    expect(container.querySelector('[data-split-id="root"]')).toBeNull();

    rerender(
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
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(container.querySelector('[data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.760');
  });

  it('zooms the active pane across nested splits and retargets when focus changes', () => {
    const workspace: TerminalWorkspaceState = {
      terminals: [
        { id: 'top-right', ptyId: 'runtime-top-right', title: 'Top Right' },
        { id: 'bottom-right', ptyId: 'runtime-bottom-right', title: 'Bottom Right' },
      ],
      layoutTree: {
        type: 'split' as const,
        splitId: 'root',
        direction: 'vertical' as const,
        ratio: 0.5,
        children: [
          { type: 'pane' as const, paneId: MAIN_TERMINAL_PANE_ID },
          {
            type: 'split' as const,
            splitId: 'right',
            direction: 'horizontal' as const,
            ratio: 0.5,
            children: [
              { type: 'pane' as const, paneId: 'top-right' },
              { type: 'pane' as const, paneId: 'bottom-right' },
            ] as const,
          },
        ] as const,
      },
    };

    const { container, rerender } = render(
      <SessionTerminalWorkspace
        sessionId="session-1"
        sessionLabel="Session 1"
        sessionAgent="claude"
        cwd="/tmp/repo"
        workspace={workspace}
        activePaneId="bottom-right"
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

    expect(container.querySelector('[data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.500');
    expect(container.querySelector('[data-split-id="right"]')).toHaveAttribute('data-split-ratio', '0.500');

    act(() => {
      registeredShortcuts.get('terminal.toggleZoom')?.();
    });

    expect(container.querySelector('[data-session-terminal-workspace="session-1"]')).toHaveAttribute('data-zoomed-pane-id', 'bottom-right');
    expect(container.querySelector('[data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.240');
    expect(container.querySelector('[data-split-id="right"]')).toHaveAttribute('data-split-ratio', '0.240');

    rerender(
      <SessionTerminalWorkspace
        sessionId="session-1"
        sessionLabel="Session 1"
        sessionAgent="claude"
        cwd="/tmp/repo"
        workspace={workspace}
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

    expect(container.querySelector('[data-session-terminal-workspace="session-1"]')).toHaveAttribute('data-zoomed-pane-id', MAIN_TERMINAL_PANE_ID);
    expect(container.querySelector('[data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.760');
    expect(container.querySelector('[data-split-id="right"]')).toHaveAttribute('data-split-ratio', '0.500');
  });

  it('re-fits the active pane when the workspace topology changes after closing a split', () => {
    vi.useFakeTimers();
    mockTerminalFit.mockReset();

    const { rerender } = render(
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
        activePaneId="pane-shell-1"
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

    vi.runAllTimers();
    mockTerminalFit.mockClear();

    rerender(
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

    vi.runAllTimers();

    expect(mockTerminalFit).toHaveBeenCalled();
  });
});
