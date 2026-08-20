import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import type { Seed } from '../hooks/useDaemonSocket';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'planted',
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

function crown(id: string, title: string, progress: Partial<Seed['plot_progress']> = {}): Seed {
  return seed({
    id,
    title,
    plot_progress: { total: 0, done: 0, withered: 0, growing: 0, dormant: 0, ready: 0, blocked: 0, ...progress },
  });
}

function childOf(crownID: string, overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return seed({ ...overrides, edges: [{ kind: 'part-of', to: crownID }, ...(overrides.edges ?? [])] });
}

// The garden is one space and plots are its only grouping. The panel opens on
// all of it and scopes by walking in, so navigating never asks the daemon
// anything: the push already carries the whole garden.
describe('GardenPanel navigation', () => {
  const shipIt = crown('s-crown1', 'ship the thing', { total: 3, done: 1, growing: 1, ready: 1 });
  const first = childOf('s-crown1', { id: 's-child1', title: 'first step', status: 'harvested' });
  const second = childOf('s-crown1', { id: 's-child2', title: 'second step', ready: true });
  const elsewhere = seed({ id: 's-alone1', title: 'unrelated work' });
  const all = [shipIt, first, second, elsewhere];

  // The root is crowns and loose seeds. A seed inside a crown lives in its
  // plot — listing it at root too reads as two seeds.
  it('opens on crowns and loose seeds, keeping plot children inside their plot', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    expect(screen.getByText('ship the thing')).toBeInTheDocument();
    expect(screen.getByText('unrelated work')).toBeInTheDocument();
    expect(screen.queryByText('first step')).not.toBeInTheDocument();
    expect(screen.queryByText('second step')).not.toBeInTheDocument();
  });

  // A child whose crown missed the push must still be reachable: hidden under
  // an absent crown is hidden everywhere.
  it('lists a child at root when its crown is not in the push', () => {
    const orphan = childOf('s-gone99', { id: 's-orph11', title: 'crown got capped away' });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={9} seeds={[orphan, elsewhere]} />);

    expect(screen.getByText('crown got capped away')).toBeInTheDocument();
  });

  // A crown row carries the count, because that is what a reader scans a list
  // for; the sentence behind it belongs on the crown's own page, where there is
  // room to say where the plot is stuck.
  it('counts a crown s plot on its row and spells it out on its page', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    expect(screen.getByRole('button', { name: /ship the thing/ })).toHaveTextContent('1/3');
    expect(screen.getByRole('button', { name: /unrelated work/ })).not.toHaveTextContent('/');
    expect(screen.queryByText(/1\/3 done/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));
    expect(screen.getByText('1/3 done · 1 growing · 1 ready')).toBeInTheDocument();
  });

  it('drills into a plot and climbs back out', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));

    expect(screen.getByRole('button', { name: /second step/ })).toBeInTheDocument();
    // Scoped: the plot is what is on screen, and the rest of the garden is not.
    expect(screen.queryByRole('button', { name: /unrelated work/ })).not.toBeInTheDocument();
    // The harvested child sits behind the closed toggle, in the plot too.
    expect(screen.queryByRole('button', { name: /first step/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /1 closed/ }));
    expect(screen.getByRole('button', { name: /first step/ })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Garden' }));

    expect(screen.getByRole('button', { name: /unrelated work/ })).toBeInTheDocument();
  });

  // Root to leaf: a plot inside a plot walks in the same way, and every level of
  // the trail is its own way back, so climbing two plots is one click.
  it('walks a plot inside a plot and climbs several levels at once', () => {
    const outer = crown('s-outer1', 'the epic', { total: 1 });
    const middle = childOf('s-outer1', {
      id: 's-mid111',
      title: 'a slice',
      plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 1, blocked: 0 },
    });
    const leaf = childOf('s-mid111', { id: 's-leaf11', title: 'the actual work' });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={3} seeds={[outer, middle, leaf]} />);

    fireEvent.click(screen.getByRole('button', { name: /the epic/ }));
    fireEvent.click(screen.getByRole('button', { name: /a slice/ }));

    expect(screen.getByRole('button', { name: /the actual work/ })).toBeInTheDocument();
    // The middle plot is the place you are in, so it is the page's title and a
    // step of the trail — never also a row in the listing.
    expect(screen.queryByRole('button', { name: /^a slice/ })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'a slice' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Garden' }));

    expect(screen.getByRole('button', { name: /the epic/ })).toBeInTheDocument();
    // At root there is nowhere back to: the trail is gone.
    expect(screen.queryByRole('button', { name: 'Garden' })).not.toBeInTheDocument();
  });

  // A crown that leaves the garden while the panel sits in its plot must not
  // strand the reader in a plot with no way out and nothing in it.
  it('climbs out on its own when the crown it is inside disappears', () => {
    const { rerender } = render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));
    expect(screen.getByRole('button', { name: /second step/ })).toBeInTheDocument();

    rerender(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[elsewhere]} />);

    expect(screen.getByRole('button', { name: /unrelated work/ })).toBeInTheDocument();
  });

  // Crossing plots: following work sideways must not go back through the whole
  // garden to get there.
  it('crosses from one plot into a related one', () => {
    const other = crown('s-crown2', 'the next plot', { total: 1, ready: 1 });
    const linked = childOf('s-crown1', {
      id: 's-child3',
      title: 'holds the next plot up',
      edges: [{ kind: 'blocks', to: 's-crown2' }],
    });
    render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={[shipIt, linked, other, elsewhere]} />,
    );

    fireEvent.click(screen.getByRole('button', { name: /ship the thing/ }));
    fireEvent.click(screen.getByRole('button', { name: /holds the next plot up/ }));
    fireEvent.click(screen.getByRole('button', { name: 'the next plot' }));

    // Inside the crossed-into plot. The trail is the way you came, not the way
    // the garden is filed — so it names the plot you crossed out of, and every
    // step of it is a way back.
    expect(screen.getByRole('heading', { name: 'the next plot' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Garden' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'ship the thing' })).toBeInTheDocument();
  });

  // The push is capped. A list that ends at the cap without saying so reads as
  // the whole garden, which is the silent truncation the house rule forbids.
  it('says what it is not showing when the garden outgrew one push', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1421} seeds={[elsewhere]} />);

    expect(screen.getByText(/holds 1421 seeds/)).toBeInTheDocument();
    expect(screen.getByText(/newest 1/)).toBeInTheDocument();
  });

  it('says nothing about a cap when the whole garden fit', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={4} seeds={all} />);

    expect(screen.queryByText(/holds 4 seeds/)).not.toBeInTheDocument();
  });

  it('names the way in when the garden is empty, and the way into an empty plot', () => {
    const { rerender } = render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={0} seeds={[]} />);

    expect(screen.getByText(/The garden is empty/)).toBeInTheDocument();
    expect(screen.getByText('attn seed plant "what this is"')).toBeInTheDocument();

    const bare = crown('s-crown9', 'nothing in it yet');
    rerender(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={1} seeds={[bare]} />);
    fireEvent.click(screen.getByRole('button', { name: /nothing in it yet/ }));

    expect(screen.getByText(/Nothing planted in this plot yet/)).toBeInTheDocument();
    expect(screen.getByText('attn seed plant "what this is" --part-of s-crown9')).toBeInTheDocument();
  });

  // Reading a seed is drilling into it: one target per row, and what is in there
  // is whatever is in there. The reader no longer has to know whether they want
  // the body or the plot before they know what the seed holds.
  it('opens a seed to its own page, and the trail is the way back', () => {
    const withBody = seed({ id: 's-body11', title: 'has a body', body: 'the plan itself' });
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={2} seeds={[withBody, elsewhere]} />);

    expect(screen.queryByText('the plan itself')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /has a body/ }));

    expect(screen.getByRole('heading', { name: 'has a body' })).toBeInTheDocument();
    expect(screen.getByText('the plan itself')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /unrelated work/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Garden' }));
    expect(screen.getByRole('button', { name: /unrelated work/ })).toBeInTheDocument();
  });
});
