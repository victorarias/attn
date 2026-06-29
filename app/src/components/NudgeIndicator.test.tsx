import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { deriveNudgeMode, SidebarNudgeBar, TileNudgeOverlay } from './NudgeIndicator';

const FIRES_AT = '2999-01-01T00:00:00.000Z'; // far future so the bar is mid-countdown

describe('deriveNudgeMode', () => {
  it('returns null when there is no unread activity and no countdown', () => {
    expect(deriveNudgeMode({ state: 'idle', isActive: false })).toBeNull();
    expect(deriveNudgeMode({ state: 'working', isActive: true })).toBeNull();
  });

  it('counts down for an inactive session with a running countdown', () => {
    expect(
      deriveNudgeMode({ ticketUnread: true, nudgeFiresAt: FIRES_AT, state: 'idle', isActive: false }),
    ).toBe('counting');
  });

  it('pauses the selected idle/waiting session even while a stale fires_at is in flight', () => {
    // A just-selected session: the daemon will clear fires_at a beat later, but the
    // UI must read it as paused immediately, never as a running countdown.
    expect(
      deriveNudgeMode({ ticketUnread: true, nudgeFiresAt: FIRES_AT, state: 'idle', isActive: true }),
    ).toBe('paused');
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'waiting_input', isActive: true }),
    ).toBe('paused');
  });

  it('shows a static marker for a working+unread session (no countdown while working)', () => {
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'working', isActive: true }),
    ).toBe('marker');
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'working', isActive: false }),
    ).toBe('marker');
  });

  it('falls back to the marker for the post-fire transient (unread, idle, inactive, no fires_at)', () => {
    // The timer entry is gone but the session has not flipped to working yet — the
    // marker is the catch-all so the indicator never blinks out.
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'idle', isActive: false }),
    ).toBe('marker');
  });
});

describe('SidebarNudgeBar', () => {
  it('renders a non-interactive bar when counting', () => {
    const { container } = render(<SidebarNudgeBar mode="counting" firesAt={FIRES_AT} />);
    expect(container.querySelector('.nudge-sidebar-bar')).not.toBeNull();
    expect(container.querySelector('button')).toBeNull();
  });

  it('renders a static marker bar', () => {
    const { container } = render(<SidebarNudgeBar mode="marker" />);
    expect(container.querySelector('.nudge-sidebar-bar--marker')).not.toBeNull();
    expect(container.querySelector('button')).toBeNull();
  });

  it('renders a clickable strip when paused and triggers without bubbling to the row', () => {
    const onTrigger = vi.fn();
    const onRowClick = vi.fn();
    render(
      <div onClick={onRowClick}>
        <SidebarNudgeBar mode="paused" onTrigger={onTrigger} />
      </div>,
    );
    fireEvent.click(screen.getByRole('button'));
    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(onRowClick).not.toHaveBeenCalled();
  });
});

describe('TileNudgeOverlay', () => {
  it('shows an incoming-nudge strip when counting', () => {
    const { container } = render(<TileNudgeOverlay mode="counting" firesAt={FIRES_AT} />);
    expect(container.querySelector('.nudge-tile-overlay--counting')).not.toBeNull();
    expect(screen.getByText('Incoming ticket nudge…')).toBeTruthy();
    expect(container.querySelector('button')).toBeNull();
  });

  it('shows an unread-activity marker', () => {
    const { container } = render(<TileNudgeOverlay mode="marker" />);
    expect(container.querySelector('.nudge-tile-overlay--marker')).not.toBeNull();
    expect(screen.getByText('Unread ticket activity')).toBeTruthy();
  });

  it('shows a deliver-now button when paused and triggers without bubbling to the pane', () => {
    const onTrigger = vi.fn();
    const onPaneClick = vi.fn();
    render(
      <div onClick={onPaneClick}>
        <TileNudgeOverlay mode="paused" onTrigger={onTrigger} />
      </div>,
    );
    fireEvent.click(screen.getByRole('button', { name: /deliver/i }));
    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(onPaneClick).not.toHaveBeenCalled();
  });
});
