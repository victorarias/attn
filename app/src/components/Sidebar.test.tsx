import { fireEvent, render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { getVisualSessionOrder, groupSessionsByDirectory } from '../utils/sessionGrouping';

interface TestSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  agent?: string;
  branch?: string;
  isWorktree?: boolean;
  cwd?: string;
  recoverable?: boolean;
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
});
