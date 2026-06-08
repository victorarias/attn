import { fireEvent, render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { GridHiddenSessions } from './GridHiddenSessions';

const sessions = [
  { sessionId: 's1', title: 'api server' },
  { sessionId: 's2', title: 'web client' },
];

describe('GridHiddenSessions', () => {
  it('renders nothing when no sessions are hidden', () => {
    const { container } = render(<GridHiddenSessions sessions={[]} onRestore={() => {}} />);
    expect(container.firstChild).toBeNull();
  });

  it('shows the hidden count and opens a list of removed sessions', () => {
    render(<GridHiddenSessions sessions={sessions} onRestore={() => {}} />);
    expect(screen.getByRole('button', { name: '2 hidden' })).toBeTruthy();
    // List is closed until the toggle is clicked.
    expect(screen.queryByText('api server')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: '2 hidden' }));
    expect(screen.getByText('api server')).toBeTruthy();
    expect(screen.getByText('web client')).toBeTruthy();
  });

  it('restores the session whose row is clicked', () => {
    const onRestore = vi.fn();
    render(<GridHiddenSessions sessions={sessions} onRestore={onRestore} />);
    fireEvent.click(screen.getByRole('button', { name: '2 hidden' }));
    fireEvent.click(screen.getByTitle('Restore web client'));
    expect(onRestore).toHaveBeenCalledWith('s2');
  });
});
