import { fireEvent, render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar, type FooterShortcut } from './Sidebar';
import { getVisualSessionOrder, groupSessionsByDirectory } from '../utils/sessionGrouping';

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
  const visualOrder = getVisualSessionOrder(sessions);
  return {
    sessionGroups: groupSessionsByDirectory(sessions),
    visualOrder,
    visualIndexBySessionId: new Map(visualOrder.map((session, index) => [session.id, index])),
  };
}

const baseProps = {
  selectedId: null,
  collapsed: false,
  headerActions: [],
  footerShortcuts: undefined as FooterShortcut[] | undefined,
  onSelectSession: () => {},
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

  it('renders session shortcuts in grouped visual order', () => {
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

    expect(screen.getByTestId('sidebar-session-a1')).toHaveTextContent('⌘1');
    expect(screen.getByTestId('sidebar-session-a2')).toHaveTextContent('⌘2');
    expect(screen.getByTestId('sidebar-session-b1')).toHaveTextContent('⌘3');
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

    expect(screen.getByText('gpu-box')).toBeInTheDocument();
    expect(screen.getByTestId('close-session-remote-1')).toBeInTheDocument();
    expect(screen.getByTestId('reload-session-remote-1')).toBeInTheDocument();
  });
});
