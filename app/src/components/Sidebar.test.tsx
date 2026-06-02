import { fireEvent, render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar, type FooterShortcut } from './Sidebar';
import { buildWorkspaceViewModels, type WorkspaceWithSessions } from '../utils/workspaceViewModels';

function sessionlessWorkspace(): WorkspaceWithSessions<TestSession> {
  return {
    id: 'workspace-/repo/docs',
    title: 'docs',
    directory: '/repo/docs',
    sessions: [],
    firstSessionId: null,
    focusedSessionId: null,
  };
}

interface TestSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  agent?: string;
  branch?: string;
  isWorktree?: boolean;
  cwd?: string;
  endpointId?: string;
  endpointName?: string;
  endpointStatus?: string;
  recoverable?: boolean;
  reviewLoopStatus?: string;
}

function buildSidebarData(sessions: TestSession[]) {
  const viewSessions = sessions.map((session) => ({
    ...session,
    workspaceId: session.cwd ? `workspace-${session.cwd}` : `workspace-${session.id}`,
  }));
  const workspaceIds = new Set<string>();
  const workspaces = buildWorkspaceViewModels(
    viewSessions
      .filter((session) => {
        if (workspaceIds.has(session.workspaceId)) return false;
        workspaceIds.add(session.workspaceId);
        return true;
      })
      .map((session) => ({
        id: session.workspaceId,
        title: session.label,
        directory: session.cwd || session.id,
        muted: false,
      })),
    viewSessions,
  );
  return {
    workspaces,
    visualOrder: workspaces,
    visualIndexByWorkspaceId: new Map(workspaces.map((workspace, index) => [workspace.id, index])),
  };
}

const baseProps = {
  selectedId: null,
  selectedWorkspaceId: null,
  collapsed: false,
  headerActions: [],
  footerShortcuts: undefined as FooterShortcut[] | undefined,
  onSelectSession: () => {},
  onSelectWorkspace: () => {},
  onNewSession: () => {},
  onCloseSession: () => {},
  onReloadSession: () => {},
  onGoToDashboard: () => {},
  onToggleCollapse: () => {},
};

describe('Sidebar', () => {
  it('uses regular state indicator for codex sessions', () => {
    const sessions: TestSession[] = [{
      id: 's1',
      label: 'codex',
      state: 'working',
      agent: 'codex',
    }];
    const { container } = render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
      />
    );
    expect(container.querySelector('.state-indicator--working')).toBeTruthy();
    expect(container.querySelector('.state-indicator--unknown')).toBeFalsy();
  });

  it('shows waiting badge in collapsed sidebar', () => {
    const sessions: TestSession[] = [{
      id: 's1',
      label: 'codex',
      state: 'waiting_input',
      agent: 'codex',
    }];
    const { container } = render(
      <Sidebar
        {...baseProps}
        collapsed
        {...buildSidebarData(sessions)}
      />
    );
    expect(container.querySelector('.mini-badge.unknown')).toBeFalsy();
    expect(container.querySelector('.mini-badge')).toBeTruthy();
    expect(screen.queryByText('?')).not.toBeInTheDocument();
  });

  it('fires reload callback when reload button is clicked', () => {
    const sessions: TestSession[] = [{
      id: 's1',
      label: 'claude',
      state: 'idle',
      agent: 'claude',
    }];
    const onReloadSession = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        onReloadSession={onReloadSession}
        {...buildSidebarData(sessions)}
      />
    );

    fireEvent.click(screen.getByTestId('reload-session-s1'));
    expect(onReloadSession).toHaveBeenCalledWith('s1');
  });

  it('fires close callback when close button is clicked', () => {
    const sessions: TestSession[] = [{
      id: 's1',
      label: 'claude',
      state: 'idle',
      agent: 'claude',
    }];
    const onCloseSession = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        onCloseSession={onCloseSession}
        {...buildSidebarData(sessions)}
      />
    );

    fireEvent.click(screen.getByTestId('close-session-s1'));
    expect(onCloseSession).toHaveBeenCalledWith('s1');
  });

  it('shows review loop indicator for sessions with loop state', () => {
    const sessions: TestSession[] = [{
      id: 's1',
      label: 'claude',
      state: 'idle',
      agent: 'claude',
      reviewLoopStatus: 'running',
    }];
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
      />
    );

    expect(screen.getByLabelText('Review loop running')).toBeInTheDocument();
  });

  it('renders workspace shortcuts in workspace visual order', () => {
    const sessions: TestSession[] = [
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
      { id: 'a2', label: 'A2', state: 'idle', cwd: '/repo/a' },
    ];
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
      />
    );

    expect(screen.getByTestId('sidebar-workspace-workspace-/repo/a')).toHaveTextContent('⌘1');
    expect(screen.getByTestId('sidebar-workspace-workspace-/repo/b')).toHaveTextContent('⌘2');
    expect(screen.getByTestId('sidebar-session-a1')).not.toHaveTextContent('⌘1');
  });

  it('hides empty workspaces from the sidebar and shortcut order', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    const emptyWorkspace: WorkspaceWithSessions<TestSession> = {
      id: 'workspace-/repo/empty',
      title: 'empty',
      directory: '/repo/empty',
      sessions: [],
      firstSessionId: null,
      focusedSessionId: null,
    };
    const visualOrder = [emptyWorkspace, ...sidebarData.visualOrder];
    render(
      <Sidebar
        {...baseProps}
        workspaces={visualOrder}
        visualOrder={visualOrder}
        visualIndexByWorkspaceId={new Map(visualOrder.map((workspace, index) => [workspace.id, index]))}
      />
    );

    expect(screen.queryByTestId('sidebar-workspace-workspace-/repo/empty')).not.toBeInTheDocument();
    expect(screen.getByTestId('sidebar-workspace-workspace-/repo/a')).toHaveTextContent('⌘1');
    expect(screen.getByTestId('sidebar-workspace-workspace-/repo/b')).toHaveTextContent('⌘2');
  });

  it('hides sessionless workspaces by default and reveals them when showSessionless is set', () => {
    const sidebarData = buildSidebarData([{ id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' }]);
    const all = [...sidebarData.visualOrder, sessionlessWorkspace()];
    const indexMap = new Map(all.map((workspace, index) => [workspace.id, index]));

    const { rerender } = render(
      <Sidebar {...baseProps} workspaces={all} visualOrder={all} visualIndexByWorkspaceId={indexMap} />
    );
    expect(screen.queryByTestId('sidebar-workspace-workspace-/repo/docs')).not.toBeInTheDocument();

    rerender(
      <Sidebar {...baseProps} workspaces={all} visualOrder={all} visualIndexByWorkspaceId={indexMap} showSessionless />
    );
    expect(screen.getByTestId('sidebar-workspace-workspace-/repo/docs')).toBeInTheDocument();
  });

  it('marks sessionless workspaces with a neutral indicator instead of a state dot', () => {
    const sidebarData = buildSidebarData([{ id: 'a1', label: 'A1', state: 'working', cwd: '/repo/a' }]);
    const all = [...sidebarData.visualOrder, sessionlessWorkspace()];
    render(
      <Sidebar
        {...baseProps}
        workspaces={all}
        visualOrder={all}
        visualIndexByWorkspaceId={new Map(all.map((workspace, index) => [workspace.id, index]))}
        showSessionless
      />
    );

    const sessionlessGroup = screen.getByTestId('sidebar-workspace-workspace-/repo/docs');
    expect(sessionlessGroup.querySelector('.workspace-neutral-indicator')).toBeTruthy();
    expect(sessionlessGroup.querySelector('.state-indicator')).toBeFalsy();

    // A real session workspace still shows its state dot.
    const sessionGroup = screen.getByTestId('sidebar-workspace-workspace-/repo/a');
    expect(sessionGroup.querySelector('.state-indicator')).toBeTruthy();
    expect(sessionGroup.querySelector('.workspace-neutral-indicator')).toBeFalsy();
  });

  it('invokes onToggleShowSessionless when the panel-only switch is clicked', () => {
    const onToggleShowSessionless = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
        onToggleShowSessionless={onToggleShowSessionless}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    fireEvent.click(screen.getByTestId('toggle-show-sessionless'));

    expect(onToggleShowSessionless).toHaveBeenCalledTimes(1);
  });

  it('keeps display options visible after selecting a mode', () => {
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData([])}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' }));
    fireEvent.click(screen.getByRole('button', { name: 'tight' }));

    expect(screen.getByRole('dialog', { name: 'Sidebar settings' })).toBeInTheDocument();
  });

  it('renders dock action hints alongside custom footer shortcuts when provided', () => {
    render(
      <Sidebar
        {...baseProps}
        headerActions={[{
          id: 'diff',
          title: 'Diff',
          shortcutHint: '⌘⇧G diff',
          onClick: () => {},
          icon: null,
        }]}
        footerShortcuts={[
          { label: '⌘D split v' },
          { label: '⌘⇧D split h' },
          { label: '⌘⇧Z zoom', active: true },
          { label: '⌘⌥←↑→↓ pane' },
        ]}
        {...buildSidebarData([])}
      />
    );

    expect(screen.getByText('⌘⇧G diff')).toBeInTheDocument();
    expect(screen.getByText('⌘D split v')).toBeInTheDocument();
    expect(screen.getByText('⌘⇧D split h')).toBeInTheDocument();
    expect(screen.getByText('⌘⇧Z zoom')).toBeInTheDocument();
    expect(screen.getByText('⌘⇧Z zoom')).toHaveAttribute('data-active', 'true');
    expect(screen.getByText('⌘⌥←↑→↓ pane')).toBeInTheDocument();
    expect(screen.getByText('⌘⇧B sidebar')).toBeInTheDocument();
  });

  it('shows endpoint badge and renders actions for remote sessions', () => {
    const sessions: TestSession[] = [{
      id: 'remote-1',
      label: 'codex',
      state: 'idle',
      endpointId: 'ep-1',
      endpointName: 'gpu-box',
      endpointStatus: 'connected',
    }];
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
      />
    );

    expect(screen.getAllByText('gpu-box')).toHaveLength(2);
    expect(screen.getByTestId('close-session-remote-1')).toBeInTheDocument();
    expect(screen.getByTestId('reload-session-remote-1')).toBeInTheDocument();
  });

  it('mutes workspaces instead of individual sessions', () => {
    const sidebarData = buildSidebarData([
      { id: 's1', label: 'active', state: 'idle', cwd: '/repo/active' },
      { id: 's2', label: 'quiet', state: 'waiting_input', cwd: '/repo/quiet' },
    ]);
    const mutedWorkspace = {
      ...sidebarData.workspaces[1],
      muted: true,
    };
    const onMuteWorkspace = vi.fn();

    render(
      <Sidebar
        {...baseProps}
        workspaces={[sidebarData.workspaces[0]]}
        visualOrder={[sidebarData.workspaces[0]]}
        visualIndexByWorkspaceId={new Map([[sidebarData.workspaces[0].id, 0]])}
        mutedWorkspaces={[mutedWorkspace]}
        mutedExpanded
        onMuteWorkspace={onMuteWorkspace}
      />
    );

    expect(screen.queryByTestId('mute-session-s1')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('mute-workspace-workspace-/repo/active'));
    expect(onMuteWorkspace).toHaveBeenCalledWith('workspace-/repo/active', undefined);

    expect(screen.getByText('Muted Workspaces (1)')).toBeInTheDocument();
    expect(screen.getByTestId('sidebar-muted-workspace-workspace-/repo/quiet')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Unmute workspace quiet' }));
    expect(onMuteWorkspace).toHaveBeenCalledWith('workspace-/repo/quiet', undefined);
  });
});
