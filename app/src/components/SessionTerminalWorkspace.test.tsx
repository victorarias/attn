import { act, fireEvent, render, screen } from '@testing-library/react';
import { forwardRef, useEffect, useImperativeHandle } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionTerminalWorkspace } from './SessionTerminalWorkspace';
import type { PaneRuntimeEventRouter } from './SessionTerminalWorkspace/paneRuntimeEventRouter';
import { type TerminalWorkspaceState } from '../types/workspace';
const SESSION_PANE_ID = 'pane-session';
function createSingleAgentWorkspace(): TerminalWorkspaceState {
  return {
    agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }],
    layoutTree: { type: 'pane', paneId: SESSION_PANE_ID },
  };
}

function createSplitWorkspace(ratio = 0.5): TerminalWorkspaceState {
  return {
    agents: [
      { id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' },
      { id: 'pane-session-2', runtimeId: 'session-2', sessionId: 'session-2', title: 'Session 2' },
    ],
    layoutTree: {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio,
      children: [
        { type: 'pane', paneId: SESSION_PANE_ID },
        { type: 'pane', paneId: 'pane-session-2' },
      ],
    },
  };
}

const { registeredShortcuts } = vi.hoisted(() => ({
  registeredShortcuts: new Map<string, () => void>(),
}));

const mockEventRouter: PaneRuntimeEventRouter = {
  registerBinding: vi.fn(() => () => {}),
};

const { mockPtyAttach, mockPtyResize, mockPtyWrite } = vi.hoisted(() => ({
  mockPtyAttach: vi.fn(() => Promise.resolve()),
  mockPtyResize: vi.fn(() => Promise.resolve()),
  mockPtyWrite: vi.fn(() => Promise.resolve()),
}));

vi.mock('../pty/bridge', () => ({
  listenPtyEvents: vi.fn(() => Promise.resolve(() => {})),
  ptyAttach: mockPtyAttach,
  ptyResize: mockPtyResize,
  ptyWrite: mockPtyWrite,
}));

const { mockTerminalFocus } = vi.hoisted(() => ({
  mockTerminalFocus: vi.fn(() => true),
}));

const { mockTerminalFit } = vi.hoisted(() => ({
  mockTerminalFit: vi.fn(),
}));

const { renderedTerminalProps } = vi.hoisted(() => ({
  renderedTerminalProps: new Map<string, any>(),
}));

const { terminalLifecycleCounts } = vi.hoisted(() => ({
  terminalLifecycleCounts: new Map<string, { mounts: number; unmounts: number }>(),
}));

vi.mock('./GhosttyTerminal', () => ({
  GhosttyTerminal: forwardRef((props: any, ref) => {
    renderedTerminalProps.set(props.debugName, props);
    useEffect(() => {
      const current = terminalLifecycleCounts.get(props.debugName) || { mounts: 0, unmounts: 0 };
      terminalLifecycleCounts.set(props.debugName, {
        mounts: current.mounts + 1,
        unmounts: current.unmounts,
      });
      return () => {
        const latest = terminalLifecycleCounts.get(props.debugName) || { mounts: 0, unmounts: 0 };
        terminalLifecycleCounts.set(props.debugName, {
          mounts: latest.mounts,
          unmounts: latest.unmounts + 1,
        });
      };
    }, [props.debugName]);
    useImperativeHandle(ref, () => ({
      fit: mockTerminalFit,
      focus: mockTerminalFocus,
      typeTextViaInput: vi.fn(() => true),
      isInputFocused: vi.fn(() => false),
      write: vi.fn(() => Promise.resolve()),
      resizeLocal: vi.fn(),
      reset: vi.fn(),
      scrollToTop: vi.fn(() => true),
      getText: vi.fn(() => ''),
      getSize: vi.fn(() => ({ cols: 80, rows: 24 })),
      getVisibleContent: vi.fn(),
      getVisibleStyleSummary: vi.fn(),
      drain: vi.fn(() => Promise.resolve()),
    }));
    const label = typeof props.debugName === 'string' && props.debugName.startsWith('utility:')
      ? props.debugName.split(':')[2]
      : 'session pane';
    return <div data-testid="mock-terminal">{label}</div>;
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

function createMockTerminal() {
  return {
    fit: vi.fn(),
    focus: vi.fn(() => true),
    typeTextViaInput: vi.fn(() => true),
    isInputFocused: vi.fn(() => false),
    write: vi.fn(() => Promise.resolve()),
    resizeLocal: vi.fn(),
    reset: vi.fn(),
    scrollToTop: vi.fn(() => true),
    getText: vi.fn(() => ''),
    getSize: vi.fn(() => ({ cols: 80, rows: 24 })),
    getVisibleContent: vi.fn(),
    getVisibleStyleSummary: vi.fn(),
    drain: vi.fn(() => Promise.resolve()),
  };
}

describe('SessionTerminalWorkspace', () => {
  afterEach(() => {
    registeredShortcuts.clear();
    renderedTerminalProps.clear();
    terminalLifecycleCounts.clear();
    vi.mocked(mockEventRouter.registerBinding).mockClear();
    mockTerminalFit.mockReset();
    mockPtyAttach.mockReset();
    mockPtyResize.mockReset();
    mockPtyWrite.mockReset();
    vi.useRealTimers();
  });

  it('focuses the agent pane exactly once on active session render — no retries', () => {
    vi.useFakeTimers();
    // Focus fails — but focusActivePane should not retry; retries belong to the
    // init/ready path (focusPaneIfCurrentlyActive) which runs from Terminal callbacks.
    mockTerminalFocus.mockReset().mockReturnValue(false);

    render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={createSingleAgentWorkspace()}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(mockTerminalFocus).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(200);
    // Still exactly 1 — no retry chains started
    expect(mockTerminalFocus).toHaveBeenCalledTimes(1);
  });

  it('focuses the main Claude pane immediately on mouse down', () => {
    mockTerminalFocus.mockReset().mockReturnValue(true);
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          ...createSingleAgentWorkspace(),
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    fireEvent.mouseDown(screen.getByText('session pane'));

    expect(onFocusPane).toHaveBeenCalledWith(SESSION_PANE_ID);
    expect(mockTerminalFocus).toHaveBeenCalled();
  });

  it('focuses a session pane immediately on mouse down', () => {
    mockTerminalFocus.mockReset().mockReturnValue(true);
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={onFocusPane}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    fireEvent.mouseDown(screen.getAllByText('session pane')[1]);

    expect(onFocusPane).toHaveBeenCalledWith('pane-session-1');
    expect(mockTerminalFocus).toHaveBeenCalled();
  });

  it('moves focus to the pane below on cmd+alt+down', () => {
    const onFocusPane = vi.fn();

    render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, ],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
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
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, ],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'right' },
            ],
          },
        }}
        activePaneId="right"
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
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
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.3,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    const split = container.querySelector('[data-split-id="root"]');
    const firstChild = container.querySelector('[data-split-id="root"] [data-split-child-index="0"]') as HTMLElement | null;
    const secondChild = container.querySelector('[data-split-id="root"] [data-split-child-index="1"]') as HTMLElement | null;
    const mainPane = container.querySelector(`[data-pane-id="${SESSION_PANE_ID}"]`);
    const sessionPane = container.querySelector('[data-pane-id="pane-session-1"]');

    expect(split).toHaveAttribute('data-split-ratio', '0.300');
    expect(split).toHaveAttribute('data-split-path', 'root');
    expect((split as HTMLElement | null)?.style.gridTemplateColumns).toBe('minmax(0, 0.3fr) minmax(0, 0.7fr)');
    expect(firstChild).toHaveAttribute('data-split-child-path', 'root/0');
    expect(secondChild).toHaveAttribute('data-split-child-path', 'root/1');
    expect(mainPane).toHaveAttribute('data-pane-path', 'root/0');
    expect(sessionPane).toHaveAttribute('data-pane-path', 'root/1');
  });

  it('rolls back an optimistic split ratio when persistence fails', async () => {
    const onResizeSplit = vi.fn(() => Promise.reject(new Error('persist failed')));
    const { container } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[
          { id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" },
          { id: "session-2", label: "Session 2", agent: "claude", cwd: "/tmp/repo" },
        ]}
        workspace={createSplitWorkspace()}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onResizeSplit={onResizeSplit}
      />
    );
    const panes = container.querySelector('.session-terminal-panes') as HTMLElement;
    vi.spyOn(panes, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, right: 1000, bottom: 500, width: 1000, height: 500, x: 0, y: 0, toJSON: () => {},
    });

    fireEvent.pointerDown(screen.getByRole('separator'), { button: 0, clientX: 500, clientY: 250 });
    fireEvent.pointerUp(window, { clientX: 800, clientY: 250 });
    expect(container.querySelector('.workspace-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.800');

    await act(async () => {
      await Promise.resolve();
    });

    expect(onResizeSplit).toHaveBeenCalledWith('root', 0.8);
    expect(container.querySelector('.workspace-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.500');
  });

  it('accepts an authoritative split ratio that supersedes an optimistic resize', () => {
    const onResizeSplit = vi.fn(() => new Promise<void>(() => {}));
    const workspace = createSplitWorkspace();
    const { container, rerender } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[
          { id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" },
          { id: "session-2", label: "Session 2", agent: "claude", cwd: "/tmp/repo" },
        ]}
        workspace={workspace}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onResizeSplit={onResizeSplit}
      />
    );
    const panes = container.querySelector('.session-terminal-panes') as HTMLElement;
    vi.spyOn(panes, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, right: 1000, bottom: 500, width: 1000, height: 500, x: 0, y: 0, toJSON: () => {},
    });

    fireEvent.pointerDown(screen.getByRole('separator'), { button: 0, clientX: 500, clientY: 250 });
    fireEvent.pointerUp(window, { clientX: 800, clientY: 250 });
    expect(container.querySelector('.workspace-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.800');

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[
          { id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" },
          { id: "session-2", label: "Session 2", agent: "claude", cwd: "/tmp/repo" },
        ]}
        workspace={createSplitWorkspace(0.6)}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
        onResizeSplit={onResizeSplit}
      />
    );

    expect(container.querySelector('.workspace-layout-metadata [data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.600');
  });

  it('releases selection locks when divider and panel drags are cancelled', () => {
    const { container, rerender } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[
          { id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" },
          { id: "session-2", label: "Session 2", agent: "claude", cwd: "/tmp/repo" },
        ]}
        workspace={createSplitWorkspace()}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );
    const panes = container.querySelector('.session-terminal-panes') as HTMLElement;
    vi.spyOn(panes, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, right: 1000, bottom: 500, width: 1000, height: 500, x: 0, y: 0, toJSON: () => {},
    });

    fireEvent.pointerDown(screen.getByRole('separator'), { button: 0, clientX: 500, clientY: 250 });
    expect(document.body.style.userSelect).toBe('none');
    fireEvent.pointerCancel(window);
    expect(document.body.style.userSelect).toBe('');

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root-panel',
            direction: 'vertical',
            ratio: 0.7,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'panel', panelId: 'panel-md', panelKind: 'markdown', panelParams: '/tmp/notes.md' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    fireEvent.pointerDown(container.querySelector('.workspace-dock-panel-header') as HTMLElement, { button: 0, clientX: 750, clientY: 250 });
    expect(document.body.style.userSelect).toBe('none');
    fireEvent.blur(window);
    expect(document.body.style.userSelect).toBe('');
  });

  it('keeps the session pane mounted when the split topology changes around it', () => {
    const { rerender } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "codex", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "codex", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' },
            { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' },
            { id: 'pane-session-2', runtimeId: 'runtime-session-2', title: "Session", sessionId: 'session-1' },
          ],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.7,
            children: [
              {
                type: 'split',
                splitId: 'main-branch',
                direction: 'vertical',
                ratio: 0.5,
                children: [
                  { type: 'pane', paneId: SESSION_PANE_ID },
                  { type: 'pane', paneId: 'pane-session-2' },
                ],
              },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "codex", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(terminalLifecycleCounts.get('agent:Session 1:codex:session-1')?.mounts).toBeGreaterThan(0);
  });

  it('lets zoom arm before splitting and applies once a split exists', () => {
    const { container, rerender } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={createSingleAgentWorkspace()}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    act(() => {
      registeredShortcuts.get('terminal.toggleZoom')?.();
    });

    expect(container.querySelector('[data-session-terminal-workspace="workspace-session-1"]')).toHaveAttribute('data-zoomed-pane-id', SESSION_PANE_ID);
    expect(container.querySelector('[data-split-id="root"]')).toBeNull();

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
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
      agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' },
        { id: 'top-right', runtimeId: 'runtime-top-right', title: "Session", sessionId: 'session-1' },
        { id: 'bottom-right', runtimeId: 'runtime-bottom-right', title: "Session", sessionId: 'session-1' },
      ],
      layoutTree: {
        type: 'split' as const,
        splitId: 'root',
        direction: 'vertical' as const,
        ratio: 0.5,
        children: [
          { type: 'pane' as const, paneId: SESSION_PANE_ID },
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
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={workspace}
        activePaneId="bottom-right"
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
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

    expect(container.querySelector('[data-session-terminal-workspace="workspace-session-1"]')).toHaveAttribute('data-zoomed-pane-id', 'bottom-right');
    expect(container.querySelector('[data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.240');
    expect(container.querySelector('[data-split-id="right"]')).toHaveAttribute('data-split-ratio', '0.240');

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={workspace}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(container.querySelector('[data-session-terminal-workspace="workspace-session-1"]')).toHaveAttribute('data-zoomed-pane-id', SESSION_PANE_ID);
    expect(container.querySelector('[data-split-id="root"]')).toHaveAttribute('data-split-ratio', '0.760');
    expect(container.querySelector('[data-split-id="right"]')).toHaveAttribute('data-split-ratio', '0.500');
  });

  it('re-fits the active pane when the workspace topology changes after closing a split', () => {
    vi.useFakeTimers();
    mockTerminalFit.mockReset();

    const { rerender } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId="pane-session-1"
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
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
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={createSingleAgentWorkspace()}
        activePaneId={SESSION_PANE_ID}
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    vi.runAllTimers();

    expect(mockTerminalFit).toHaveBeenCalled();
  });

  it('re-fits every visible pane when an inactive split session becomes active', () => {
    mockTerminalFit.mockReset();

    const workspace: TerminalWorkspaceState = {
      agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
      layoutTree: {
        type: 'split',
        splitId: 'root',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', paneId: SESSION_PANE_ID },
          { type: 'pane', paneId: 'pane-session-1' },
        ],
      },
    };

    const { rerender } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={workspace}
        activePaneId="pane-session-1"
        fontSize={14}
        enabled
        isActiveSession={false}
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(mockTerminalFit).not.toHaveBeenCalled();

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={workspace}
        activePaneId="pane-session-1"
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(mockTerminalFit).toHaveBeenCalledTimes(2);
  });

  it('waits until the session view is visible before refitting and focusing the active pane', () => {
    mockTerminalFit.mockReset();
    mockTerminalFocus.mockReset().mockReturnValue(true);

    const workspace: TerminalWorkspaceState = {
      agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
      layoutTree: {
        type: 'split',
        splitId: 'root',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', paneId: SESSION_PANE_ID },
          { type: 'pane', paneId: 'pane-session-1' },
        ],
      },
    };

    const { rerender } = render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={workspace}
        activePaneId="pane-session-1"
        fontSize={14}
        enabled
        isActiveSession
        isSessionViewVisible={false}
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(mockTerminalFit).not.toHaveBeenCalled();
    expect(mockTerminalFocus).not.toHaveBeenCalled();

    rerender(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo" }]}
        workspace={workspace}
        activePaneId="pane-session-1"
        fontSize={14}
        enabled
        isActiveSession
        isSessionViewVisible
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    expect(mockTerminalFit).toHaveBeenCalledTimes(2);
    expect(mockTerminalFocus).toHaveBeenCalled();
  });

  it('attaches the selected session pane runtime on terminal ready', async () => {
    vi.useFakeTimers();
    render(
      <SessionTerminalWorkspace
        workspaceId="workspace-session-1"
        workspaceSessions={[{ id: "session-1", label: "Session 1", agent: "claude", cwd: "/tmp/repo", endpointId: "ep-remote" }]}
        workspace={{
          agents: [{ id: SESSION_PANE_ID, runtimeId: 'session-1', sessionId: 'session-1', title: 'Session 1' }, { id: 'pane-session-1', runtimeId: 'runtime-session-1', title: "Session", sessionId: 'session-1' }],
          layoutTree: {
            type: 'split',
            splitId: 'root',
            direction: 'vertical',
            ratio: 0.5,
            children: [
              { type: 'pane', paneId: SESSION_PANE_ID },
              { type: 'pane', paneId: 'pane-session-1' },
            ],
          },
        }}
        activePaneId="pane-session-1"
        fontSize={14}
        enabled
        isActiveSession
        eventRouter={mockEventRouter}
        onSplitPane={vi.fn()}
        onClosePane={vi.fn()}
        onFocusPane={vi.fn()}
        onNavigateOutOfSession={vi.fn()}
      />
    );

    const sessionPaneProps = renderedTerminalProps.get('agent:Session 1:claude:session-1');
    expect(sessionPaneProps).toBeDefined();

    await act(async () => {
      sessionPaneProps.onReady(createMockTerminal() as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-session-1',
        cols: 80,
        rows: 24,
        shell: false,
        agent: 'claude',
        policy: 'fresh_spawn',
      },
      forceResizeBeforeAttach: false,
    });
  });
});
