import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { HeaderSettlingIndicator, SidebarSettlingBar } from './SettlingIndicator';
import { CountdownFill } from './CountdownFill';

const FIRES_AT = '2999-01-01T00:00:00.000Z'; // far future, so the bar is mid-countdown
const ALREADY_PASSED = '2000-01-01T00:00:00.000Z';

describe('HeaderSettlingIndicator', () => {
  it('says what is happening in words, not in a number', () => {
    render(<HeaderSettlingIndicator firesAt={FIRES_AT} />);
    expect(screen.getByTestId('settling-indicator')).toHaveTextContent('Settling…');
    // The countdown is deliberately visual: nothing on the chip counts seconds.
    expect(screen.getByTestId('settling-indicator').textContent).not.toMatch(/\d/);
  });

  it('names the key that stops it, on the chip itself', () => {
    // The whole point of the pill is that a settle is never silent; a user who
    // reads it must not then have to go hunting for how to stop it.
    const { container } = render(<HeaderSettlingIndicator firesAt={FIRES_AT} />);
    expect(container.querySelector('.countdown-cancel-hint-key')?.textContent).toBe('⌘.');
    expect(screen.getByText('keep')).toBeTruthy();
  });

  it('cancels on click, and does not let the click reach the row underneath', () => {
    const onCancel = vi.fn();
    const onRowClick = vi.fn();
    render(
      <div onClick={onRowClick}>
        <HeaderSettlingIndicator firesAt={FIRES_AT} onCancel={onCancel} />
      </div>,
    );

    fireEvent.click(screen.getByTestId('settling-indicator'));

    expect(onCancel).toHaveBeenCalledTimes(1);
    // Selecting a session because you kept its turn would be a second, unasked-for
    // action — and in a split, the header is a drag handle.
    expect(onRowClick).not.toHaveBeenCalled();
  });

  it('does not start a pointer drag on the pane header', () => {
    const onPointerDown = vi.fn();
    render(
      <div onPointerDown={onPointerDown}>
        <HeaderSettlingIndicator firesAt={FIRES_AT} />
      </div>,
    );

    fireEvent.pointerDown(screen.getByTestId('settling-indicator'));

    expect(onPointerDown).not.toHaveBeenCalled();
  });
});

describe('SidebarSettlingBar', () => {
  it('renders a bar and no text — the row is small and this is only an announcement', () => {
    render(<SidebarSettlingBar firesAt={FIRES_AT} />);
    const bar = screen.getByTestId('settling-sidebar-bar');
    expect(bar).toBeInTheDocument();
    expect(bar.textContent).toBe('');
  });
});

describe('CountdownFill', () => {
  it('drains from full toward empty, which is what makes it read as time running out', () => {
    const { container } = render(
      <CountdownFill firesAt={FIRES_AT} className="probe" direction="drain" />,
    );
    const el = container.querySelector('.probe') as HTMLElement;
    // The effect sets the final width plus a linear transition to it; the browser
    // animates the rest, so nothing here ticks per frame.
    expect(el.style.width).toBe('0%');
    expect(el.style.transition).toMatch(/^width \d+ms linear$/);
  });

  it('fills toward full in the other direction, so a nudge and a settle never look alike', () => {
    const { container } = render(
      <CountdownFill firesAt={FIRES_AT} className="probe" direction="fill" />,
    );
    const el = container.querySelector('.probe') as HTMLElement;
    expect(el.style.width).toBe('100%');
  });

  it('snaps to the end with no animation when the deadline has already passed', () => {
    // A remount after the fact must not animate a countdown that is over.
    const { container } = render(
      <CountdownFill firesAt={ALREADY_PASSED} className="probe" direction="drain" />,
    );
    const el = container.querySelector('.probe') as HTMLElement;
    expect(el.style.width).toBe('0%');
    expect(el.style.transition).toBe('none');
  });

  it('does the same for an unparseable deadline rather than animating forever', () => {
    const { container } = render(
      <CountdownFill firesAt="not-a-date" className="probe" direction="fill" />,
    );
    const el = container.querySelector('.probe') as HTMLElement;
    expect(el.style.width).toBe('100%');
    expect(el.style.transition).toBe('none');
  });
});
