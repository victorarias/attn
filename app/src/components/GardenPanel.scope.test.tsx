import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
    step_slug: overrides.title,
    workspace_id: '',
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

// The push carries the whole garden — every workspace — because scoping in the
// client is what makes switching workspaces free. That only holds if the panel
// actually scopes: push the whole garden and render it unfiltered and the user
// sees another workspace's work as their own.
describe('GardenPanel scope', () => {
  const here = seed({ id: 's-here11', title: 'planted here', workspace_id: 'ws-1' });
  const elsewhere = seed({ id: 's-away11', title: 'planted elsewhere', workspace_id: 'ws-2' });
  const unplaced = seed({ id: 's-none11', title: 'planted with no workspace' });

  // The push is capped. A list that ends at the cap without saying so reads as
  // the whole garden, which is the silent truncation the house rule forbids: the
  // reader has to be told the limit was hit and by how much.
  it('says what it is not showing when the garden outgrew one push', () => {
    render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={1421} seeds={[here]} workspaceId="ws-1" />,
    );

    expect(screen.getByText(/holds 1421 seeds/)).toBeInTheDocument();
    expect(screen.getByText(/newest 1/)).toBeInTheDocument();
  });

  it('says nothing about a cap when the whole garden fit', () => {
    render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={[here, elsewhere, unplaced]} workspaceId="ws-1" />,
    );

    expect(screen.queryByText(/holds/)).not.toBeInTheDocument();
  });

  it('shows only this workspace, and the whole garden on request', () => {
    render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={[here, elsewhere, unplaced]} workspaceId="ws-1" />,
    );

    expect(screen.getByText('planted here')).toBeInTheDocument();
    expect(screen.queryByText('planted elsewhere')).not.toBeInTheDocument();
    expect(screen.queryByText('planted with no workspace')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'This workspace' }));

    expect(screen.getByText('planted elsewhere')).toBeInTheDocument();
    expect(screen.getByText('planted with no workspace')).toBeInTheDocument();
  });

  // On the dashboard there is no workspace to scope to. Filtering by an absent id
  // would render an empty garden and read as "nothing is planted".
  it('shows the whole garden when there is no workspace to scope to', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[here, elsewhere]} workspaceId={null} />);

    expect(screen.getByText('planted here')).toBeInTheDocument();
    expect(screen.getByText('planted elsewhere')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'This workspace' })).not.toBeInTheDocument();
  });

  it('names the way in when the workspace has nothing planted', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[elsewhere]} workspaceId="ws-1" />);

    expect(screen.getByText(/Nothing planted in this workspace yet/)).toBeInTheDocument();
    expect(screen.getByText(/attn seed plant/)).toBeInTheDocument();
  });

  it('opens one seed to its body without hiding the rest', () => {
    const withBody = seed({
      id: 's-body11',
      title: 'has a body',
      workspace_id: 'ws-1',
      body: 'the plan itself',
    });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[withBody, here]} workspaceId="ws-1" />);

    expect(screen.queryByText('the plan itself')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('has a body'));

    expect(screen.getByText('the plan itself')).toBeInTheDocument();
    expect(screen.getByText('planted here')).toBeInTheDocument();
  });
});
