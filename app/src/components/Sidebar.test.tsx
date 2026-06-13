import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar, type DockItem } from './Sidebar';
import { buildWorkspaceViewModels, type WorkspaceWithSessions } from '../utils/workspaceViewModels';

function sessionlessWorkspace(): WorkspaceWithSessions<TestSession> {
  return {
    id: 'workspace-/repo/docs',
    title: 'docs',
    directory: '/repo/docs',
    sessions: [],
    children: [],
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
  chiefOfStaff?: boolean;
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

function workspaceWithBrowserTile(): WorkspaceWithSessions<TestSession> {
  const session: TestSession & { workspaceId: string } = {
    id: 's1',
    label: 'shell',
    state: 'idle',
    workspaceId: 'workspace-browser',
  };
  return buildWorkspaceViewModels(
    [{
      id: 'workspace-browser',
      title: 'browser',
      directory: '/repo/browser',
      layout: {
        layout_json: JSON.stringify({
          type: 'split',
          split_id: 'split-root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', pane_id: 'pane-s1' },
            { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://www.google.com' },
          ],
        }),
        panes: [{ pane_id: 'pane-s1', session_id: 's1' }],
      },
    }],
    [session],
  )[0];
}

const baseProps = {
  selectedId: null,
  selectedWorkspaceId: null,
  collapsed: false,
  headerActions: [],
  dockItems: undefined as DockItem[] | undefined,
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

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('reload-session-action'));
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

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('close-session-action'));
    expect(onCloseSession).toHaveBeenCalledWith('s1');
  });

  it('renders browser tiles in layout order and exposes tile actions', () => {
    const workspace = workspaceWithBrowserTile();
    const onSelectTile = vi.fn();
    const onCloseTile = vi.fn();
    const onReloadTile = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        workspaces={[workspace]}
        visualOrder={[workspace]}
        visualIndexByWorkspaceId={new Map([[workspace.id, 0]])}
        onSelectTile={onSelectTile}
        onCloseTile={onCloseTile}
        onReloadTile={onReloadTile}
      />
    );

    const tile = screen.getByTestId('sidebar-tile-workspace-browser-tile-browser');
    expect(tile).toHaveTextContent('www.google.com');
    expect(screen.getByTestId('sidebar-session-s1').compareDocumentPosition(tile) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    fireEvent.click(tile);
    expect(onSelectTile).toHaveBeenCalledWith('workspace-browser', 'tile-browser');

    fireEvent.click(screen.getByTestId('reload-tile-workspace-browser-tile-browser'));
    expect(onReloadTile).toHaveBeenCalledWith('workspace-browser', 'tile-browser');

    fireEvent.click(screen.getByTestId('close-tile-workspace-browser-tile-browser'));
    expect(onCloseTile).toHaveBeenCalledWith('workspace-browser', 'tile-browser');
  });

  it('marks same-endpoint workspace rows as leaf drag targets', () => {
    const sessions: TestSession[] = [
      { id: 's1', label: 'source', state: 'idle', cwd: '/repo/source' },
      { id: 's2', label: 'target', state: 'idle', cwd: '/repo/target' },
    ];
    const onWorkspaceDragEnter = vi.fn();
    const onWorkspaceDragDrop = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...buildSidebarData(sessions)}
        leafDrag={{ sourceWorkspaceId: 'workspace-/repo/source' }}
        dragHoverWorkspaceId="workspace-/repo/target"
        onWorkspaceDragEnter={onWorkspaceDragEnter}
        onWorkspaceDragDrop={onWorkspaceDragDrop}
      />
    );

    const source = screen.getByTestId('sidebar-workspace-workspace-/repo/source');
    const target = screen.getByTestId('sidebar-workspace-workspace-/repo/target');
    expect(source).toHaveClass('workspace-group--drag-disabled');
    expect(target).toHaveClass('workspace-group--drag-entering');

    fireEvent.pointerEnter(target);
    fireEvent.pointerUp(target);
    expect(onWorkspaceDragEnter).toHaveBeenCalledWith(expect.objectContaining({ id: 'workspace-/repo/target' }));
    expect(onWorkspaceDragDrop).toHaveBeenCalledWith(expect.objectContaining({ id: 'workspace-/repo/target' }));
  });

  it('reorders a workspace by dragging its header onto an insertion seam', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
      { id: 'c1', label: 'C1', state: 'idle', cwd: '/repo/c' },
    ]);
    const onWorkspaceReorder = vi.fn();
    const onSelectWorkspace = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...sidebarData}
        onSelectWorkspace={onSelectWorkspace}
        onWorkspaceReorder={onWorkspaceReorder}
      />,
    );

    const sourceGroup = screen.getByTestId('sidebar-workspace-workspace-/repo/a');
    const header = sourceGroup.querySelector('.workspace-group-header') as HTMLElement;

    // No seams until a drag arms.
    expect(screen.queryByTestId('workspace-reorder-seam-0')).not.toBeInTheDocument();

    // Press, then cross the activation threshold to arm the reorder.
    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 80 });

    // Seams now exist: one before each of the three workspaces plus a trailing one.
    const seam2 = screen.getByTestId('workspace-reorder-seam-2');
    expect(seam2).toBeInTheDocument();
    expect(screen.getByTestId('workspace-reorder-seam-3')).toBeInTheDocument();

    // Drop A onto the seam between B (index 1) and C (index 2).
    fireEvent.pointerEnter(seam2);
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 10, clientY: 120 });

    expect(onWorkspaceReorder).toHaveBeenCalledWith({
      workspaceId: 'workspace-/repo/a',
      prevWorkspaceId: 'workspace-/repo/b',
      nextWorkspaceId: 'workspace-/repo/c',
    });
    // The arming drag suppresses the trailing selection click.
    expect(onSelectWorkspace).not.toHaveBeenCalled();
  });

  it('treats a sub-threshold header press as a plain selection click', () => {
    const sidebarData = buildSidebarData([
      { id: 'a1', label: 'A1', state: 'idle', cwd: '/repo/a' },
      { id: 'b1', label: 'B1', state: 'idle', cwd: '/repo/b' },
    ]);
    const onWorkspaceReorder = vi.fn();
    const onSelectWorkspace = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        {...sidebarData}
        onSelectWorkspace={onSelectWorkspace}
        onWorkspaceReorder={onWorkspaceReorder}
      />,
    );

    const header = screen
      .getByTestId('sidebar-workspace-workspace-/repo/a')
      .querySelector('.workspace-group-header') as HTMLElement;

    fireEvent.pointerDown(header, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(window, { pointerId: 1, clientX: 12, clientY: 11 });
    fireEvent.pointerUp(window, { pointerId: 1, clientX: 12, clientY: 11 });
    fireEvent.click(header);

    expect(onWorkspaceReorder).not.toHaveBeenCalled();
    expect(onSelectWorkspace).toHaveBeenCalledWith('workspace-/repo/a');
    expect(screen.queryByTestId('workspace-reorder-seam-0')).not.toBeInTheDocument();
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
      children: [],
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

  it('invokes onToggleShowSessionless when the tile-only switch is clicked', () => {
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

  it('renders config-driven dock items, marks active ones, and fires actions on click', () => {
    const onDiff = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        dockItems={[
          { id: 'dock.diff', label: 'diff', keys: '⌘⇧G', onClick: onDiff },
          { id: 'terminal.toggleZoom', label: 'zoom', keys: '⌘⇧Z', active: true },
          { id: 'session.toggleSidebar', label: 'sidebar', keys: '⌘⇧B' },
        ]}
        {...buildSidebarData([])}
      />
    );

    // Actionable item is a button and fires its handler.
    const diff = screen.getByRole('button', { name: /diff/ });
    expect(diff).toBeInTheDocument();
    fireEvent.click(diff);
    expect(onDiff).toHaveBeenCalledTimes(1);

    // Informational items render their keys + label.
    expect(screen.getByText('zoom')).toBeInTheDocument();
    expect(screen.getByText('zoom').closest('.shortcut-hint')).toHaveAttribute('data-active', 'true');
    expect(screen.getByText('sidebar')).toBeInTheDocument();
    expect(screen.getByText('⌘⇧B')).toBeInTheDocument();
  });

  it('hides dock items behind the collapse toggle and toggles via the header button', () => {
    const onToggle = vi.fn();
    const { rerender } = render(
      <Sidebar
        {...baseProps}
        dockItems={[{ id: 'dock.diff', label: 'diff', keys: '⌘⇧G' }]}
        dockCollapsed={false}
        onToggleDockCollapsed={onToggle}
        {...buildSidebarData([])}
      />
    );
    expect(screen.getByText('diff')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Hide dock' }));
    expect(onToggle).toHaveBeenCalledTimes(1);

    rerender(
      <Sidebar
        {...baseProps}
        dockItems={[{ id: 'dock.diff', label: 'diff', keys: '⌘⇧G' }]}
        dockCollapsed={true}
        onToggleDockCollapsed={onToggle}
        {...buildSidebarData([])}
      />
    );
    expect(screen.queryByText('diff')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Show dock' })).toBeInTheDocument();
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
    fireEvent.click(screen.getByTestId('session-actions-remote-1'));
    expect(screen.getByTestId('close-session-action')).toBeInTheDocument();
    expect(screen.getByTestId('reload-session-action')).toBeInTheDocument();
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

  it('renames a session through the pencil trigger and popover', async () => {
    const sessions: TestSession[] = [{ id: 's1', label: 'claude', state: 'idle', agent: 'claude' }];
    const onRenameSession = vi.fn(async () => {});
    render(
      <Sidebar
        {...baseProps}
        onRenameSession={onRenameSession}
        {...buildSidebarData(sessions)}
      />
    );

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('rename-session-action'));
    const input = screen.getByRole('textbox') as HTMLInputElement;
    expect(input.value).toBe('claude');
    fireEvent.change(input, { target: { value: 'renamed-session' } });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    await waitFor(() => expect(onRenameSession).toHaveBeenCalledWith('s1', 'renamed-session'));
  });

  it('renames a workspace through the pencil trigger and popover', async () => {
    const sessions: TestSession[] = [{ id: 's1', label: 'claude', state: 'idle', agent: 'claude' }];
    const onRenameWorkspace = vi.fn(async () => {});
    render(
      <Sidebar
        {...baseProps}
        onRenameWorkspace={onRenameWorkspace}
        {...buildSidebarData(sessions)}
      />
    );

    fireEvent.click(screen.getByTestId('rename-workspace-workspace-s1'));
    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'renamed-workspace' } });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    await waitFor(() => expect(onRenameWorkspace).toHaveBeenCalledWith('workspace-s1', 'renamed-workspace'));
  });

  it('shows the chief role and requests removal from the session menu', () => {
    const sessions: TestSession[] = [{
      id: 's1',
      label: 'coordinator',
      state: 'working',
      chiefOfStaff: true,
    }];
    const onChangeChiefOfStaff = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        onChangeChiefOfStaff={onChangeChiefOfStaff}
        {...buildSidebarData(sessions)}
      />
    );

    expect(screen.getByLabelText('Chief of staff')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('chief-of-staff-session-action'));

    expect(onChangeChiefOfStaff).toHaveBeenCalledWith('s1', false);
  });

  it('requests promotion from the session menu', () => {
    const sessions: TestSession[] = [{ id: 's1', label: 'worker', state: 'idle' }];
    const onChangeChiefOfStaff = vi.fn();
    render(
      <Sidebar
        {...baseProps}
        onChangeChiefOfStaff={onChangeChiefOfStaff}
        {...buildSidebarData(sessions)}
      />
    );

    fireEvent.click(screen.getByTestId('session-actions-s1'));
    fireEvent.click(screen.getByTestId('chief-of-staff-session-action'));

    expect(onChangeChiefOfStaff).toHaveBeenCalledWith('s1', true);
  });
});
