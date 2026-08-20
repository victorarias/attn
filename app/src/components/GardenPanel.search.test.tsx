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
  return seed({ ...overrides, edges: [{ kind: 'part-of', to: crownID }] });
}

function field(): HTMLInputElement {
  return screen.getByRole('combobox') as HTMLInputElement;
}

function type(text: string) {
  fireEvent.change(field(), { target: { value: text } });
}

function rows(): string[] {
  return screen.queryAllByRole('option').map((row) => row.textContent ?? '');
}

/** The snippet is split into highlighted runs, so read it off its own element. */
function snippet(): string {
  return document.querySelector('.garden-seed__snippet')?.textContent ?? '';
}

function drillIntoTheCrown() {
  fireEvent.click(screen.getByRole('button', { name: 'Open the plot under ship the search' }));
}

// Search and filters are one mechanism: the query line is the only filter state
// the panel has, and every affordance writes into it.
describe('GardenPanel search', () => {
  const shipIt = crown('s-crown1', 'ship the search', { total: 3, done: 1, ready: 1 });
  const wiring = childOf('s-crown1', {
    id: 's-wire01',
    title: 'wire the field',
    body: 'the input is a line of type, not a box',
  });
  const ranking = childOf('s-crown1', { id: 's-rank01', title: 'rank the answers', ready: true });
  const shipped = childOf('s-crown1', { id: 's-done01', title: 'draw the field', status: 'harvested' });
  const elsewhere = seed({ id: 's-else01', title: 'unrelated field work', tender_member: 'hazel' });
  const dropped = seed({ id: 's-drop01', title: 'a dropped idea', status: 'withered' });
  const all = [shipIt, wiring, ranking, shipped, elsewhere, dropped];

  function open(seeds: Seed[] = all) {
    return render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={seeds.length} seeds={seeds} />);
  }

  it('flattens the garden into the answer, out of every plot at once', () => {
    open();
    type('field');

    expect(rows().join(' ')).toContain('wire the field');
    expect(rows().join(' ')).toContain('unrelated field work');
  });

  // The row that matched in the body has to say why it is on screen; the row
  // that matched in its title already does.
  it('shows the line of body that answered the query', () => {
    open();
    type('line of type');

    expect(snippet()).toContain('a line of type, not a box');
  });

  it('finds a seed by its id and by whoever holds it', () => {
    open();
    type('s-rank01');
    expect(rows()).toHaveLength(1);
    expect(rows()[0]).toContain('rank the answers');

    type('tender:hazel');
    expect(rows()).toHaveLength(1);
    expect(rows()[0]).toContain('unrelated field work');
  });

  // A filter nobody implemented must not read as "nothing here matches".
  it('names a filter it does not have rather than answering with an empty list', () => {
    open();
    type('is:done');

    expect(screen.getByText(/no filter called is:done/)).toBeInTheDocument();
  });

  it('offers the values of an operator being typed, and puts one into the query', () => {
    open();
    type('is:');

    fireEvent.click(screen.getByRole('button', { name: 'ready' }));
    expect(field().value).toBe('is:ready ');
  });

  // No results is a state somebody has to design: it names what was asked and
  // where it was asked, and offers the move out.
  it('names the query and the scope when nothing matches', () => {
    open();
    type('nowhere');

    expect(screen.getByText(/Nothing in the garden matches/)).toBeInTheDocument();
    expect(screen.getByText('nowhere')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Clear the search/ }));
    expect(field().value).toBe('');
  });

  describe('the closed lens', () => {
    // A garden keeps everything ever harvested. Closed work is out of the way
    // by default, and the count says exactly how much is out of the way.
    it('is a toggle, not a one-way door', () => {
      open();

      const show = screen.getByRole('button', { name: '1 closed' });
      expect(show).toHaveAttribute('aria-pressed', 'false');
      fireEvent.click(show);

      expect(rows().join(' ')).toContain('a dropped idea');
      const hide = screen.getByRole('button', { name: 'hide 1 closed' });
      expect(hide).toHaveAttribute('aria-pressed', 'true');

      fireEvent.click(hide);
      expect(rows().join(' ')).not.toContain('a dropped idea');
    });

    // The lens is a token in the query line, because a flag beside the query
    // would be a second filter state saying a different thing.
    it('writes itself into the query rather than keeping a flag beside it', () => {
      open();
      fireEvent.click(screen.getByRole('button', { name: '1 closed' }));

      expect(field().value).toBe('is:any');
    });

    // Asking to see closed work is not a search: it re-lenses the level the
    // reader is standing on instead of flattening everything below it.
    it('does not flatten the plots when all it does is widen the lens', () => {
      open();
      fireEvent.click(screen.getByRole('button', { name: '1 closed' }));

      expect(rows().join(' ')).toContain('a dropped idea');
      expect(rows().join(' ')).not.toContain('wire the field');
      expect(rows().join(' ')).not.toContain('draw the field');
    });

    it('counts what a search is hiding, and takes it back when asked', () => {
      open();
      type('field');
      expect(rows().join(' ')).not.toContain('draw the field');


      fireEvent.click(screen.getByRole('button', { name: '1 closed' }));
      expect(field().value).toBe('field is:any');
      expect(rows().join(' ')).toContain('draw the field');
    });

    // With `is:closed` typed out, the query is already the statement; a toggle
    // beside it would only argue with it.
    it('stands down when the query names a lens of its own', () => {
      open();
      type('field is:closed');

      expect(screen.queryByRole('button', { name: /closed$/ })).not.toBeInTheDocument();
    });
  });

  describe('scope', () => {
    // Where the search looks is a question the panel owes an answer to, so both
    // scopes are on screen with their counts and one key between them.
    it('searches the plot you are standing in, and says what the garden holds', () => {
      open();
      drillIntoTheCrown();
      type('field');

      expect(rows()).toHaveLength(1);
      expect(screen.getByRole('button', { name: /\+1 in the whole garden/ })).toBeInTheDocument();
    });

    // Widening is a property of the query, not a move: the trail stays put, so
    // you can widen, look, and go back to what you were doing.
    it('keeps the trail when the search widens out of the plot', () => {
      open();
      drillIntoTheCrown();
      type('field');
      fireEvent.click(screen.getByRole('button', { name: /\+1 in the whole garden/ }));

      expect(rows()).toHaveLength(2);
      expect(screen.getByRole('navigation', { name: /Standing here/ })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /1 in this plot/ })).toBeInTheDocument();
    });

    it('goes back to the plot when the search is cleared', () => {
      open();
      drillIntoTheCrown();
      type('field');
      fireEvent.click(screen.getByRole('button', { name: /\+1 in the whole garden/ }));
      type('');

      expect(rows().join(' ')).not.toContain('unrelated field work');
    });
  });

  describe('the keyboard', () => {
    it('walks the answers with the arrows without leaving the field', () => {
      open();
      type('field');
      const input = field();

      expect(input.getAttribute('aria-activedescendant')).toBe('garden-row-s-wire01');
      fireEvent.keyDown(input, { key: 'ArrowDown' });
      expect(input.getAttribute('aria-activedescendant')).toBe('garden-row-s-else01');
      fireEvent.keyDown(input, { key: 'ArrowUp' });
      expect(input.getAttribute('aria-activedescendant')).toBe('garden-row-s-wire01');
    });

    it('stops at the ends of the answer instead of wrapping past them', () => {
      open();
      type('field');
      const input = field();

      fireEvent.keyDown(input, { key: 'ArrowUp' });
      expect(input.getAttribute('aria-activedescendant')).toBe('garden-row-s-wire01');
      fireEvent.keyDown(input, { key: 'ArrowDown' });
      fireEvent.keyDown(input, { key: 'ArrowDown' });
      expect(input.getAttribute('aria-activedescendant')).toBe('garden-row-s-else01');
    });

    it('starts a new question at its best answer', () => {
      open();
      type('field');
      fireEvent.keyDown(field(), { key: 'ArrowDown' });
      type('field w');

      expect(field().getAttribute('aria-activedescendant')).toBe('garden-row-s-wire01');
    });

    // Escape clears the query before it closes the panel: one key, and it never
    // takes away more than the reader asked it to.
    it('clears the query before it closes the panel', () => {
      const onClose = vi.fn();
      render(<GardenPanel isOpen onClose={onClose} seedsTotal={all.length} seeds={all} />);
      type('field');

      fireEvent.keyDown(window, { key: 'Escape' });
      expect(field().value).toBe('');
      expect(onClose).not.toHaveBeenCalled();
    });

    it('widens the scope with one key, and narrows it back with the same one', () => {
      open();
      drillIntoTheCrown();
      type('field');

      fireEvent.keyDown(field(), { key: 'Enter', altKey: true });
      expect(rows()).toHaveLength(2);
      fireEvent.keyDown(field(), { key: 'Enter', altKey: true });
      expect(rows()).toHaveLength(1);
    });
  });
});
