import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'growing',
    step_slug: overrides.title,
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

// The way back to a delegate that is no longer on screen. The garden replaced
// the ticket board, and the board's resume affordance was the one thing a
// panel drill had no answer for: without this, a seed whose tender session was
// closed is a dead end.
describe('GardenPanel reopen', () => {
  it('reopens the tender of a drilled seed', () => {
    const onResumeSeed = vi.fn();
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[seed({ id: 's-tend11', title: 'held work', tender_session: 'sess-a' })]}
        onResumeSeed={onResumeSeed}
      />,
    );

    fireEvent.click(screen.getByText('held work'));
    fireEvent.click(screen.getByTestId('seed-reopen-s-tend11'));

    expect(onResumeSeed).toHaveBeenCalledWith('s-tend11');
  });

  it('offers no reopen on a seed nobody tends', () => {
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seedsTotal={1}
        seeds={[seed({ id: 's-idle11', title: 'unheld work', status: 'planted' })]}
        onResumeSeed={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByText('unheld work'));

    expect(screen.queryByTestId('seed-reopen-s-idle11')).not.toBeInTheDocument();
  });
});
