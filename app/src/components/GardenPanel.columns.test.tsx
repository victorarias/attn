import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
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
    plot_progress: { total: 1, done: 0, withered: 0, growing: 0, dormant: 0, ready: 1, blocked: 0 },
  });
}

// A nest deep enough that no window holds all of it: garden › crown › middle ›
// inner › fourth › fifth › leaf.
const crown = plot('s-crown1', 'the migration');
const middle = plot('s-mid111', 'one panel', 's-crown1');
const inner = plot('s-inn111', 'its header', 's-mid111');
const fourth = plot('s-fou111', 'the overflow menu', 's-inn111');
const fifth = plot('s-fif111', 'the fold threshold', 's-fou111');
const leaf = seed({ id: 's-leaf11', title: 'the actual work', edges: [{ kind: 'part-of', to: 's-fif111' }] });
const loose = seed({ id: 's-alone1', title: 'unrelated work' });
const world = [crown, middle, inner, fourth, fifth, leaf, loose];

// The panel measures its own box, and happy-dom lays nothing out, so the width
// the rule reads has to be stated.
function atWidth(width: number) {
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(width);
}

function columns() {
  return Array.from(document.querySelectorAll('.garden-column:not(.garden-column--reader)'));
}
function trailSteps() {
  return Array.from(document.querySelectorAll('.garden-trail__step')).map((s) => s.textContent?.trim());
}
function open(title: RegExp | string) {
  fireEvent.click(screen.getByRole('button', { name: title }));
}

// Miller columns are the same walk as the stack with more of it on screen. Two
// rules carry it: how many levels are drawn is decided by width, and the trail
// says only what no visible column already says.
describe('GardenPanel columns', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('draws as many trailing levels as the width holds, and no more', () => {
    atWidth(1200);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    // At the root there is nothing to put beside the list.
    expect(columns()).toHaveLength(1);

    open(/the migration/);
    expect(columns()).toHaveLength(2);

    open(/one panel/);
    open(/its header/);
    // Still two: depth does not add columns, width does.
    expect(columns()).toHaveLength(2);
  });

  it('shows a third level once there is room for it', () => {
    atWidth(1780);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    open(/the migration/);
    open(/one panel/);
    expect(columns()).toHaveLength(3);
  });

  // The trail is not decoration here and it is not a duplicate: a column already
  // says which of its rows you picked, so the trail starts above the leftmost
  // column and stops before the place you are in.
  it('names only the ancestors no column is showing', () => {
    atWidth(1780);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    open(/the migration/);
    open(/one panel/);
    // Root, the crown's children and the panel's children are all on screen, so
    // every ancestor is a selected row and the trail has nothing to add.
    expect(trailSteps()).toEqual(['Garden']);

    open(/its header/);
    // One level fell off the left: the trail picks up exactly that one.
    expect(trailSteps()).toEqual(['Garden', 'the migration']);
  });

  it('folds the trail once it outruns three steps, and opens it again', () => {
    atWidth(1200);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    open(/the migration/);
    open(/one panel/);
    open(/its header/);
    open(/the overflow menu/);
    open(/the fold threshold/);
    expect(trailSteps()).toEqual(['Garden', '…', 'its header', 'the overflow menu']);

    fireEvent.click(screen.getByRole('button', { name: /Show 2 more steps/ }));
    expect(trailSteps()).toEqual([
      'Garden', 'the migration', 'one panel', 'its header', 'the overflow menu',
    ]);
  });

  // Drilling deeper and switching siblings are one gesture. Clicking in a column
  // truncates the walk to that column's level and makes your row its new end.
  it('switches siblings when a row in an earlier column is clicked', () => {
    atWidth(1780);
    const sibling = plot('s-mid222', 'another panel', 's-crown1');
    render(
      <GardenPanel isOpen onClose={vi.fn()} seedsTotal={8} seeds={[...world, sibling]} />,
    );

    open(/the migration/);
    open(/one panel/);
    expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

    // The crown's children are the middle column; picking a different one there
    // replaces the level below rather than pushing another level on.
    const crownChildren = columns()[1];
    fireEvent.click(within(crownChildren as HTMLElement).getByRole('button', { name: /another panel/ }));

    expect(screen.getByRole('heading', { name: 'another panel' })).toBeInTheDocument();
    expect(trailSteps()).toEqual(['Garden']);
  });

  it('climbs one level on escape, wherever the walk is', () => {
    atWidth(1200);
    render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={7} seeds={world} />);

    open(/the migration/);
    open(/one panel/);
    expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByRole('heading', { name: 'the migration' })).toBeInTheDocument();
  });
});
