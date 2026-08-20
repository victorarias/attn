import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenFrame, type FrameRect } from './GardenFrame';
import { useGardenWalk } from '../store/gardenWalk';
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
const world = [seed({ id: 's-alone1', title: 'unrelated work' })];
const dockRect: FrameRect = { top: 0, left: 1110, width: 560, height: 1008 };

function frameEl() {
  return screen.getByTestId('garden-frame');
}
function props(mode: 'closed' | 'dock' | 'full') {
  return {
    mode,
    dockRect,
    onToggleFrame: vi.fn(),
    onEscapeFloor: vi.fn(),
    onClose: vi.fn(),
    seeds: world,
    seedsTotal: world.length,
  };
}

describe('GardenFrame', () => {
  beforeEach(() => useGardenWalk.setState({ trail: [] }));
  afterEach(() => vi.restoreAllMocks());

  // The reason the frame exists rather than two surfaces: promotion moves the
  // box, it does not build a new one. Every piece of panel state that survives
  // — the trail, the open seed, the scroll offset, focus, and whatever the
  // panel grows next — survives because this node is never replaced.
  it('carries the very same panel across the promotion', () => {
    const { rerender } = render(<GardenFrame {...props('dock')} />);
    const docked = document.querySelector('.garden-panel');
    expect(docked).not.toBeNull();

    rerender(<GardenFrame {...props('full')} />);
    expect(document.querySelector('.garden-panel')).toBe(docked);

    rerender(<GardenFrame {...props('dock')} />);
    expect(document.querySelector('.garden-panel')).toBe(docked);
  });

  it('takes the rectangle the dock reserved, and the window when promoted', () => {
    vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(1670);
    vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(1008);
    const { rerender } = render(<GardenFrame {...props('dock')} />);
    expect(frameEl().style.left).toBe('1110px');
    expect(frameEl().style.width).toBe('560px');

    rerender(<GardenFrame {...props('full')} />);
    // The window, minus the 12px gutter every fullscreen surface leaves.
    expect(frameEl().style.left).toBe('12px');
    expect(frameEl().style.width).toBe('1646px');
    expect(frameEl().style.height).toBe('984px');
  });

  // Closed is a state of the frame, not an absence of it: it keeps its dock
  // rectangle and slides out, which is what lets it slide back in with the
  // reader's place intact.
  it('stays mounted and inert when closed', () => {
    render(<GardenFrame {...props('closed')} />);
    expect(frameEl()).toHaveClass('is-closed');
    expect(frameEl().getAttribute('aria-hidden')).toBe('true');
    expect(document.querySelector('.garden-panel')).toBeNull();
  });

  it('is a modal only while it holds the window', () => {
    const { rerender } = render(<GardenFrame {...props('dock')} />);
    expect(frameEl().getAttribute('aria-modal')).toBeNull();

    rerender(<GardenFrame {...props('full')} />);
    expect(frameEl().getAttribute('aria-modal')).toBe('true');
  });

  it('renders nothing at all until the dock has been measured', () => {
    render(<GardenFrame {...props('dock')} dockRect={null} />);
    expect(screen.queryByTestId('garden-frame')).toBeNull();
  });

  // Two views over one garden, and the switch between them belongs to the frame
  // so that neither view owns the other. The dock has no room for four columns
  // of cards, so it is only offered in the window — and the promotion itself
  // always lands on the list, because the gesture says "this, bigger" and
  // arriving somewhere else would say the opposite.
  describe('the list/board switch', () => {
    const board = { moveSeed: vi.fn(), noteSeed: vi.fn(), loaded: true };

    it('is offered in the window and not in the dock', () => {
      const { rerender } = render(<GardenFrame {...props('dock')} {...board} />);
      expect(screen.queryByRole('group', { name: 'Garden view' })).toBeNull();

      rerender(<GardenFrame {...props('full')} {...board} />);
      expect(screen.getByRole('group', { name: 'Garden view' })).toBeInTheDocument();
    });

    it('promotes onto the list, and hands the board back to it on the way down', () => {
      const { rerender } = render(<GardenFrame {...props('full')} {...board} />);
      expect(document.querySelector('.garden-panel')).not.toBeNull();

      fireEvent.click(screen.getByRole('button', { name: 'board' }));
      expect(document.querySelector('.garden-panel')).toBeNull();
      expect(document.querySelector('.garden-board')).not.toBeNull();

      rerender(<GardenFrame {...props('dock')} {...board} />);
      expect(document.querySelector('.garden-board')).toBeNull();
      expect(document.querySelector('.garden-panel')).not.toBeNull();
    });
  });
});
