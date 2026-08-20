import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { Seed } from '../types/generated';
import { GardenPanel } from './GardenPanel';

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-plan11',
    title: 'Open this plan',
    body: '',
    status: 'growing',
    step_slug: 'open-this-plan',
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    ready: false,
    rev: 1,
    created_at: '2026-08-15T08:00:00Z',
    updated_at: '2026-08-15T08:00:00Z',
    ...overrides,
  };
}

// The ticket cutover left crashed and failed tickets on a board the garden era
// gives nobody a reason to open, so the panel points at them. Driven purely by
// the count: a garden with nothing stranded looks exactly as it did.
describe('GardenPanel stranded-ticket notice', () => {
  it('says nothing when no ticket is stranded', () => {
    render(
      <GardenPanel isOpen onClose={() => {}} seeds={[seed()]} seedsTotal={1} strandedTickets={0} />
    );
    expect(screen.queryByTestId('garden-stranded-tickets')).toBeNull();
  });

  it('names the count and the command that reaches them', () => {
    render(
      <GardenPanel isOpen onClose={() => {}} seeds={[seed()]} seedsTotal={1} strandedTickets={4} />
    );
    const notice = screen.getByTestId('garden-stranded-tickets');
    expect(notice.textContent).toContain('4 crashed or failed tickets');
    expect(notice.textContent).toContain('attn ticket list');
  });

  it('shows the notice over an empty garden, where lost work is read as lost', () => {
    render(<GardenPanel isOpen onClose={() => {}} seeds={[]} seedsTotal={0} strandedTickets={1} />);
    const notice = screen.getByTestId('garden-stranded-tickets');
    expect(notice.textContent).toContain('1 crashed or failed ticket');
    expect(screen.getByText(/The garden is empty/)).toBeTruthy();
  });
});
