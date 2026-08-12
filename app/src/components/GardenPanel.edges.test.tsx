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

// Readiness is the daemon's answer — it knows which tender sessions are still
// alive — so the panel renders `ready` rather than recomputing it. What the
// panel does own is the reverse direction of an edge: edges are stored on the
// seed they point from, so "who blocks me" only exists by reading the garden.
describe('GardenPanel edges', () => {
  const blocker = seed({ id: 's-aaa111', title: 'the blocker', ready: true });
  const blocked = seed({
    id: 's-bbb111',
    title: 'the blocked one',
    edges: [{ kind: 'part-of', to: 's-ccc111' }],
  });
  const crown = seed({ id: 's-ccc111', title: 'the crown' });

  function chain(blockerStatus: string): Seed[] {
    return [
      { ...blocker, status: blockerStatus, ready: blockerStatus === 'planted', edges: [{ kind: 'blocks', to: 's-bbb111' }] },
      blocked,
      crown,
    ];
  }

  it('marks the ready seed and says how many block the others', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('planted')} workspaceId="ws-1" />);

    expect(screen.getByText('ready')).toBeInTheDocument();
    expect(screen.getByText('blocked by 1')).toBeInTheDocument();
  });

  // Harvesting the blocker is the whole point of the edge: the dependent stops
  // being blocked at the next read, with nobody clearing anything by hand.
  it('stops counting a harvested blocker', () => {
    const { rerender } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('planted')} workspaceId="ws-1" />,
    );
    expect(screen.getByText('blocked by 1')).toBeInTheDocument();

    rerender(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('harvested')} workspaceId="ws-1" />);

    expect(screen.queryByText('blocked by 1')).not.toBeInTheDocument();
  });

  it('lists a seed’s edges in both directions when opened', () => {
    const { container } = render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={chain('planted')} workspaceId="ws-1" />,
    );

    fireEvent.click(screen.getByText('the blocked one'));

    const relations = container.querySelector('.garden-seed__relations');
    expect(relations?.textContent).toContain('part of');
    expect(relations?.textContent).toContain('the crown');
    expect(relations?.textContent).toContain('blocked by');
    expect(relations?.textContent).toContain('the blocker');
  });

  // A blocker in another workspace still blocks. The panel scopes what it lists
  // but must read edges against the whole push, or a blocked seed reads as free.
  it('counts a blocker from another workspace', () => {
    const away = seed({
      id: 's-ddd111',
      title: 'blocker elsewhere',
      workspace_id: 'ws-2',
      edges: [{ kind: 'blocks', to: 's-bbb111' }],
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[blocked, away]} workspaceId="ws-1" />);

    expect(screen.queryByText('blocker elsewhere')).not.toBeInTheDocument();
    expect(screen.getByText('blocked by 1')).toBeInTheDocument();
  });
});
