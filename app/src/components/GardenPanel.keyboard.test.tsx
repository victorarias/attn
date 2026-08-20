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
    created_at: '2026-08-20T09:00:00Z',
    updated_at: '2026-08-20T09:00:00Z',
    ...overrides,
  };
}
function plot(id: string, title: string, parent?: string): Seed {
  return seed({
    id,
    title,
    edges: parent ? [{ kind: 'part-of', to: parent }] : [],
    plot_progress: { total: 2, done: 0, withered: 0, growing: 0, dormant: 0, ready: 2, blocked: 0 },
  });
}

const crown = plot('s-crown1', 'the migration');
const first = seed({ id: 's-one111', title: 'the first child', edges: [{ kind: 'part-of', to: 's-crown1' }] });
const second = seed({ id: 's-two111', title: 'the second child', edges: [{ kind: 'part-of', to: 's-crown1' }] });
const loose = seed({ id: 's-loose1', title: 'a loose seed' });
const world = [crown, first, second, loose];

function panel(): HTMLElement {
  return screen.getByRole('region', { name: 'The garden' });
}
function focusedRow(): string {
  return (document.activeElement as HTMLElement | null)?.dataset.seedRow ?? '';
}
function row(id: string): HTMLElement {
  return document.querySelector(`[data-seed-row="${id}"]`) as HTMLElement;
}

// The keyboard is the panel's, not the scroll container's: one handler on the
// element that carries the role, delegating to whichever renderer is drawing.
describe('GardenPanel keyboard', () => {
  it('walks the rows of the place it is in, and climbs back out', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);

    fireEvent.keyDown(panel(), { key: 'ArrowDown' });
    expect(focusedRow()).toBe('s-crown1');
    fireEvent.keyDown(panel(), { key: 'ArrowDown' });
    expect(focusedRow()).toBe('s-loose1');
    fireEvent.keyDown(panel(), { key: 'ArrowUp' });
    expect(focusedRow()).toBe('s-crown1');

    fireEvent.click(row('s-crown1'));
    expect(screen.getByRole('heading', { name: 'the migration' })).toBeInTheDocument();
    fireEvent.keyDown(panel(), { key: 'ArrowLeft' });
    expect(screen.queryByRole('heading', { name: 'the migration' })).not.toBeInTheDocument();
  });

  // Right goes in, left comes back out: in columns the arrows mean what they
  // look like, and the column under focus is the one they walk.
  it('walks a column, drills right and climbs left', () => {
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1200);
    render(<GardenPanel layout="columns" isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);

    fireEvent.keyDown(panel(), { key: 'ArrowDown' });
    expect(focusedRow()).toBe('s-crown1');
    fireEvent.keyDown(panel(), { key: 'ArrowRight' });
    expect(screen.getByRole('heading', { name: 'the migration' })).toBeInTheDocument();

    // A second column is on screen now; the arrows walk the one holding focus.
    fireEvent.keyDown(panel(), { key: 'ArrowDown' });
    expect(focusedRow()).toBe('s-one111');
    fireEvent.keyDown(panel(), { key: 'ArrowDown' });
    expect(focusedRow()).toBe('s-two111');

    fireEvent.keyDown(panel(), { key: 'ArrowLeft' });
    expect(screen.queryByRole('heading', { name: 'the migration' })).not.toBeInTheDocument();
  });

  // The arrows in the search field walk the answers. Letting the walk's own
  // handler see them too would drag focus out of the field mid-question.
  it('leaves the arrows alone while they are being typed at the search field', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
    const field = screen.getByRole('combobox');
    fireEvent.change(field, { target: { value: 'child' } });
    field.focus();

    fireEvent.keyDown(field, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(field);
    expect(field.getAttribute('aria-activedescendant')).toBe('garden-row-s-two111');
  });

  // The mirror of the rule above: an empty field is not holding a question, so
  // the arrows belong to the walk. Picking an answer clears the query and
  // leaves focus here, which is the moment this rescues.
  it('hands the arrows back to the walk when the field has no answers to walk', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
    const field = screen.getByRole('combobox');
    field.focus();

    fireEvent.keyDown(field, { key: 'ArrowDown' });
    expect(focusedRow()).toBe('s-crown1');
  });

  it('returns to the search field from anywhere in the panel', () => {
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
    fireEvent.keyDown(panel(), { key: 'ArrowDown' });
    expect(focusedRow()).toBe('s-crown1');

    fireEvent.keyDown(row('s-crown1'), { key: '/' });
    expect(document.activeElement).toBe(screen.getByRole('combobox'));
  });
});
