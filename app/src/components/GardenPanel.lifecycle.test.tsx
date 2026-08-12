import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
    step_slug: overrides.title,
    workspace_id: 'ws-1',
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    ready: false,
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  };
}

// Slice 2's contract for the panel: what state a seed is in and who holds it,
// without expanding anything, and moving as the daemon pushes. A state that is
// only visible after a click is a state nobody watching the garden sees.
describe('GardenPanel lifecycle', () => {
  it('shows a seed s state and its tender in the row', () => {
    const growing = seed({
      id: 's-grow11',
      title: 'being worked on',
      status: 'growing',
      tender_member: 'trellis',
      tender_session: 'sess-a',
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[growing]} workspaceId="ws-1" />);

    expect(screen.getByText('growing')).toBeInTheDocument();
    expect(screen.getByText('tended by trellis')).toBeInTheDocument();
  });

  // A session id is not a pretty name, but "somebody holds this" is the fact the
  // panel owes the reader — an unnamed tender must not read as unclaimed.
  it('falls back to the claiming session when there is no member', () => {
    const growing = seed({
      id: 's-grow22',
      title: 'claimed by a session',
      status: 'growing',
      tender_session: 'sess-b',
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[growing]} workspaceId="ws-1" />);

    expect(screen.getByText('tended by sess-b')).toBeInTheDocument();
  });

  it('says nothing about a tender when nobody holds the seed', () => {
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[seed({ id: 's-idle11', title: 'unclaimed' })]}
        workspaceId="ws-1"
      />,
    );

    expect(screen.queryByText(/tended by/)).not.toBeInTheDocument();
  });

  // The panel holds no fetch: a state change arrives as a new push, and the row
  // re-renders from it. This is the whole live contract.
  it('follows a seed through its life as the pushes arrive', () => {
    const planted = seed({ id: 's-life11', title: 'a whole life' });
    const { rerender } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[planted]} workspaceId="ws-1" />,
    );
    expect(screen.getByText('planted')).toBeInTheDocument();

    rerender(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[{ ...planted, status: 'growing', tender_member: 'trellis', rev: 2 }]}
        workspaceId="ws-1"
      />,
    );
    expect(screen.getByText('growing')).toBeInTheDocument();
    expect(screen.getByText('tended by trellis')).toBeInTheDocument();

    rerender(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[{ ...planted, status: 'harvested', reason: 'shipped it', rev: 3 }]}
        workspaceId="ws-1"
      />,
    );
    expect(screen.getByText('harvested')).toBeInTheDocument();
    expect(screen.queryByText(/tended by/)).not.toBeInTheDocument();
  });

  it('shows why a seed closed once it is opened', () => {
    const harvested = seed({
      id: 's-done11',
      title: 'finished',
      status: 'harvested',
      reason: 'shipped it',
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[harvested]} workspaceId="ws-1" />);

    expect(screen.queryByText('shipped it')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('finished'));
    expect(screen.getByText('shipped it')).toBeInTheDocument();
  });
});
