import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { CriticalNotificationStrip } from './CriticalNotificationStrip';

// The ambient critical surface exists to be impossible to miss while something
// critical is unread, and to be gone the moment nothing is. Both halves matter:
// a surface that lingers after the user has dealt with everything trains them to
// ignore it, which is the same defect as never showing it.
describe('CriticalNotificationStrip', () => {
  it('renders nothing at all when no critical notification is unread', () => {
    const { container } = render(
      <CriticalNotificationStrip count={0} title="" onOpen={vi.fn()} />,
    );
    // Absent, not merely hidden: an emptied surface must not hold sidebar space.
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('appears while a critical notification is unread and names it', () => {
    render(<CriticalNotificationStrip count={1} title="Plugin stopped" onOpen={vi.fn()} />);

    expect(screen.getByRole('button')).toHaveAccessibleName(
      '1 unread critical notification: Plugin stopped. Open notifications.',
    );
    expect(screen.getByText('Plugin stopped')).toBeInTheDocument();
  });

  it('shows the count only once it says more than the title does', () => {
    const { rerender } = render(
      <CriticalNotificationStrip count={1} title="Plugin stopped" onOpen={vi.fn()} />,
    );
    expect(screen.queryByText('1')).toBeNull();

    rerender(<CriticalNotificationStrip count={3} title="Plugin stopped" onOpen={vi.fn()} />);
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByRole('button')).toHaveAccessibleName(
      '3 unread critical notifications, newest: Plugin stopped. Open notifications.',
    );
  });

  it('unmounts when the last critical notification is read', () => {
    const { container, rerender } = render(
      <CriticalNotificationStrip count={2} title="App runtime parked" onOpen={vi.fn()} />,
    );
    expect(screen.getByText('App runtime parked')).toBeInTheDocument();

    // One of the two read: still up, because one is still unread.
    rerender(<CriticalNotificationStrip count={1} title="App runtime parked" onOpen={vi.fn()} />);
    expect(screen.getByText('App runtime parked')).toBeInTheDocument();

    // The last one read: gone.
    rerender(<CriticalNotificationStrip count={0} title="" onOpen={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('opens the notifications panel when clicked', async () => {
    const onOpen = vi.fn();
    render(<CriticalNotificationStrip count={1} title="Plugin stopped" onOpen={onOpen} />);

    await userEvent.click(screen.getByRole('button'));

    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  // A critical notification the daemon could not name is still a critical
  // notification; the surface must not collapse to a blank row.
  it('falls back to a generic label when the title is empty', () => {
    render(<CriticalNotificationStrip count={1} title="" onOpen={vi.fn()} />);
    expect(screen.getByText('Critical notification')).toBeInTheDocument();
  });
});
