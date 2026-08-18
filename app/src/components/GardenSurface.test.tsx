import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GardenSurface } from './GardenSurface';
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

// The fullscreen garden: the surface ⌘⇧T opens, where the ticket board used to
// be. It is the same panel the dock mounts, so what it owns is the shell —
// mounting only while open, and Escape closing it.
describe('GardenSurface', () => {
  const seeds = [seed({ id: 's-full11', title: 'the whole garden' })];

  it('mounts nothing while closed', () => {
    render(<GardenSurface isOpen={false} seeds={seeds} seedsTotal={1} onClose={vi.fn()} />);
    expect(screen.queryByTestId('garden-surface')).not.toBeInTheDocument();
  });

  it('shows the garden and closes on Escape', () => {
    const onClose = vi.fn();
    render(<GardenSurface isOpen seeds={seeds} seedsTotal={1} onClose={onClose} />);

    expect(screen.getByTestId('garden-surface')).toBeInTheDocument();
    expect(screen.getByText('the whole garden')).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });
});
