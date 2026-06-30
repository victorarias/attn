import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { deriveNudgeMode, SidebarNudgeBar, HeaderNudgeIndicator } from './NudgeIndicator';

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

  it('lets the user deliver on demand even on a working session they are focused on', () => {
    // "Always click on the nudge on demand": the focused session reads as the clickable
    // paused chip in every deliverable state, including working — the click is explicit
    // intent, not the idle-gated auto-countdown.
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'working', isActive: true }),
    ).toBe('paused');
    // Off-screen it stays a static marker (no auto-countdown while working, nothing to
    // click without selecting it first).
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'working', isActive: false }),
    ).toBe('marker');
  });

  it('keeps a pending_approval session as a non-clickable marker', () => {
    // The one state a click must never reach: typing the doorbell's trailing Enter into
    // a y/n approval prompt could answer it. So even the focused session stays a marker.
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'pending_approval', isActive: true }),
    ).toBe('marker');
  });

  it('offers the clickable paused chip for an at-rest unknown session the user is on', () => {
    // codex commonly rests in 'unknown' (its turn-end classifier can't find a
    // transcript). An explicit click is unambiguous intent, so the selected session
    // must read as paused (clickable) rather than a dead marker.
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'unknown', isActive: true }),
    ).toBe('paused');
    // ...but only when it's the session you're looking at; otherwise it's a marker
    // (there is no auto-countdown for unknown, so no clickable affordance off-screen).
    expect(
      deriveNudgeMode({ ticketUnread: true, state: 'unknown', isActive: false }),
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
    const onRowPointerDown = vi.fn();
    render(
      <div onClick={onRowClick} onPointerDown={onRowPointerDown}>
        <SidebarNudgeBar mode="paused" onTrigger={onTrigger} />
      </div>,
    );
    const button = screen.getByRole('button');
    // The session row is a drag handle (handleSessionPointerDown); a press on the
    // deliver-now button must not reach it or a sloppy click would start a drag.
    fireEvent.pointerDown(button);
    expect(onRowPointerDown).not.toHaveBeenCalled();
    fireEvent.click(button);
    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(onRowClick).not.toHaveBeenCalled();
  });
});

describe('HeaderNudgeIndicator', () => {
  it('shows an incoming-nudge chip when counting', () => {
    const { container } = render(<HeaderNudgeIndicator mode="counting" firesAt={FIRES_AT} />);
    expect(container.querySelector('.nudge-header--counting')).not.toBeNull();
    expect(container.querySelector('.nudge-header-track')).not.toBeNull();
    expect(screen.getByText('Incoming ticket nudge…')).toBeTruthy();
    expect(container.querySelector('button')).toBeNull();
  });

  it('shows an unread-activity marker', () => {
    const { container } = render(<HeaderNudgeIndicator mode="marker" />);
    expect(container.querySelector('.nudge-header--marker')).not.toBeNull();
    expect(screen.getByText('Unread ticket activity')).toBeTruthy();
  });

  it('shows a deliver-now button when paused and triggers without bubbling to the pane', () => {
    const onTrigger = vi.fn();
    const onPaneClick = vi.fn();
    const onPanePointerDown = vi.fn();
    render(
      <div onClick={onPaneClick} onPointerDown={onPanePointerDown}>
        <HeaderNudgeIndicator mode="paused" onTrigger={onTrigger} />
      </div>,
    );
    const button = screen.getByRole('button', { name: /deliver/i });
    // In a split the pane header is a leaf-drag handle (beginLeafDrag); a press on the
    // deliver-now chip must not reach it or a sloppy click would relocate the pane.
    fireEvent.pointerDown(button);
    expect(onPanePointerDown).not.toHaveBeenCalled();
    fireEvent.click(button);
    expect(onTrigger).toHaveBeenCalledTimes(1);
    expect(onPaneClick).not.toHaveBeenCalled();
  });
});
