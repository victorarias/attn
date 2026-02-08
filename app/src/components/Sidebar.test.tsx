import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { Sidebar } from './Sidebar';

const baseProps = {
  selectedId: null,
  collapsed: false,
  onSelectSession: () => {},
  onNewSession: () => {},
  onCloseSession: () => {},
  onGoToDashboard: () => {},
  onToggleCollapse: () => {},
};

describe('Sidebar', () => {
  it('uses regular state indicator for codex sessions', () => {
    const { container } = render(
      <Sidebar
        {...baseProps}
        sessions={[{
          id: 's1',
          label: 'codex',
          state: 'working',
          agent: 'codex',
        }]}
      />
    );
    expect(container.querySelector('.state-indicator--working')).toBeTruthy();
    expect(container.querySelector('.state-indicator--unknown')).toBeFalsy();
  });

  it('shows waiting badge in collapsed sidebar', () => {
    const { container } = render(
      <Sidebar
        {...baseProps}
        collapsed
        sessions={[{
          id: 's1',
          label: 'codex',
          state: 'waiting_input',
          agent: 'codex',
        }]}
      />
    );
    expect(container.querySelector('.mini-badge.unknown')).toBeFalsy();
    expect(container.querySelector('.mini-badge')).toBeTruthy();
    expect(screen.queryByText('?')).not.toBeInTheDocument();
  });
});
