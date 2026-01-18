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
  it('shows unknown indicator for codex session before transcript match', () => {
    const { container } = render(
      <Sidebar
        {...baseProps}
        sessions={[{
          id: 's1',
          label: 'codex',
          state: 'working',
          agent: 'codex',
          transcriptMatched: false,
        }]}
      />
    );
    expect(container.querySelector('.state-indicator--unknown')).toBeTruthy();
  });

  it('shows unknown badge in collapsed sidebar', () => {
    const { container } = render(
      <Sidebar
        {...baseProps}
        collapsed
        sessions={[{
          id: 's1',
          label: 'codex',
          state: 'working',
          agent: 'codex',
          transcriptMatched: false,
        }]}
      />
    );
    expect(container.querySelector('.mini-badge.unknown')).toBeTruthy();
    expect(screen.getByText('?')).toBeInTheDocument();
  });
});
