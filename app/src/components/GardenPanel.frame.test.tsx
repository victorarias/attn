import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { GardenPanel } from './GardenPanel';
import { useGardenWalk } from '../store/gardenWalk';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
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

const crown = plot('s-crown1', 'the migration');
const middle = plot('s-mid111', 'one panel', 's-crown1');
const leaf = seed({ id: 's-leaf11', title: 'the actual work', edges: [{ kind: 'part-of', to: 's-mid111' }] });
const world = [crown, middle, leaf];

// The stub in test/setup.ts never calls anyone back, and the whole point here is
// what happens *while* the box changes size — so this file installs one that can
// be driven.
const observers: Array<{ target: Element; cb: ResizeObserverCallback }> = [];
class DrivableResizeObserver {
  constructor(private cb: ResizeObserverCallback) {}
  observe(target: Element) { observers.push({ target, cb: this.cb }); }
  unobserve() {}
  disconnect() {
    for (let i = observers.length - 1; i >= 0; i--) {
      if (observers[i].cb === this.cb) observers.splice(i, 1);
    }
  }
}

let boxWidth = 0;
function widen(width: number) {
  boxWidth = width;
  act(() => {
    for (const { target, cb } of [...observers]) {
      cb([{ target, contentRect: { width } } as unknown as ResizeObserverEntry], {} as ResizeObserver);
    }
  });
}

function open(title: RegExp | string) {
  fireEvent.click(screen.getByRole('button', { name: title }));
}
function columns() {
  return document.querySelectorAll('.garden-column:not(.garden-column--reader)');
}

describe('GardenPanel in its frame', () => {
  beforeEach(() => {
    observers.length = 0;
    boxWidth = 0;
    useGardenWalk.setState({ trail: [] });
    vi.stubGlobal('ResizeObserver', DrivableResizeObserver);
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(() => boxWidth);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    _resetEscapeStackForTest();
  });

  // The whole promotion rests on this: the renderer follows the box, so growing
  // the box IS the promotion. Nobody passes the panel a mode.
  describe('the walk follows the width, not the caller', () => {
    it('stacks in a dock-sized box and columns in a window-sized one', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      expect(document.querySelector('.garden-panel.is-columns')).toBeNull();

      widen(1400);
      expect(document.querySelector('.garden-panel.is-columns')).not.toBeNull();

      widen(520);
      expect(document.querySelector('.garden-panel.is-columns')).toBeNull();
    });

    // Crossing the threshold replaces the element being measured, so the ref's
    // cleanup has to release the old observer. One panel, one observer, however
    // many times it crosses.
    it('leaves no observer behind when the renderer changes', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      expect(observers).toHaveLength(1);

      widen(1400);
      expect(observers).toHaveLength(1);

      widen(520);
      expect(observers).toHaveLength(1);
    });

    it("keeps the reader's place across the crossing, both ways", () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      open(/the migration/);
      open(/one panel/);
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

      widen(1400);
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

      widen(520);
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();
    });

    it('adds the third column only once the box is wide enough for it', () => {
      boxWidth = 1200;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      open(/the migration/);
      open(/one panel/);
      expect(columns()).toHaveLength(2);

      widen(1780);
      expect(columns()).toHaveLength(3);
    });
  });

  // Escape goes down exactly one level. The order is LIFO and easy to get
  // backwards — the floor must be reached only when nothing is left above it.
  describe('the escape ladder', () => {
    function ladder() {
      const onEscapeFloor = vi.fn();
      const onClose = vi.fn();
      boxWidth = 520;
      render(
        <GardenPanel
          isOpen
          onClose={onClose}
          onEscapeFloor={onEscapeFloor}
          seedsTotal={world.length}
          seeds={world}
        />,
      );
      return { onEscapeFloor, onClose };
    }
    const escape = () => fireEvent.keyDown(window, { key: 'Escape' });

    it('clears the query, then climbs the trail, then hands the frame down', () => {
      const { onEscapeFloor, onClose } = ladder();
      open(/the migration/);
      open(/one panel/);
      const field = screen.getByRole('combobox') as HTMLInputElement;
      fireEvent.change(field, { target: { value: 'work' } });

      escape();
      expect(field.value).toBe('');
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();
      expect(onEscapeFloor).not.toHaveBeenCalled();

      escape();
      expect(screen.getByRole('heading', { name: 'the migration' })).toBeInTheDocument();
      expect(onEscapeFloor).not.toHaveBeenCalled();

      escape();
      expect(screen.queryByRole('heading', { name: 'the migration' })).toBeNull();
      expect(onEscapeFloor).not.toHaveBeenCalled();

      escape();
      expect(onEscapeFloor).toHaveBeenCalledTimes(1);
      expect(onClose).not.toHaveBeenCalled();
    });

    // The rungs arm at different moments — the query when you type, the climb
    // when you walk in — and the stack orders itself by when each turns on. Not
    // here: reopening onto a trail the garden already had arms the climb and the
    // floor in one commit, and then nothing but the order they are registered in
    // decides which one Escape reaches.
    it('climbs rather than demotes when both rungs arm at once', () => {
      useGardenWalk.setState({ trail: ['s-crown1', 's-mid111'] });
      const { onEscapeFloor } = ladder();
      expect(screen.getByRole('heading', { name: 'one panel' })).toBeInTheDocument();

      escape();
      expect(screen.getByRole('heading', { name: 'the migration' })).toBeInTheDocument();
      expect(onEscapeFloor).not.toHaveBeenCalled();
    });

    it('leaves Escape alone when there is no frame under it', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      // No onEscapeFloor: the rung is not registered at all, so Escape at the
      // root falls through to whatever is below the garden.
      escape();
      expect(document.querySelector('.garden-panel')).not.toBeNull();
    });
  });

  // The way across is a control in the header, beside the way out, and it says
  // which direction it goes.
  describe('the header control', () => {
    it('is absent unless there is another frame to go to', () => {
      boxWidth = 520;
      render(<GardenPanel isOpen onClose={vi.fn()} seedsTotal={world.length} seeds={world} />);
      expect(screen.queryByLabelText(/expand the garden/i)).toBeNull();
    });

    it('promotes from the dock and returns from the window', () => {
      const onToggleFrame = vi.fn();
      boxWidth = 520;
      const { rerender } = render(
        <GardenPanel
          isOpen
          frame="dock"
          onToggleFrame={onToggleFrame}
          onClose={vi.fn()}
          seedsTotal={world.length}
          seeds={world}
        />,
      );
      fireEvent.click(screen.getByLabelText(/expand the garden/i));
      expect(onToggleFrame).toHaveBeenCalledTimes(1);

      rerender(
        <GardenPanel
          isOpen
          frame="full"
          onToggleFrame={onToggleFrame}
          onClose={vi.fn()}
          seedsTotal={world.length}
          seeds={world}
        />,
      );
      fireEvent.click(screen.getByLabelText(/return the garden to the dock/i));
      expect(onToggleFrame).toHaveBeenCalledTimes(2);
    });
  });
});
