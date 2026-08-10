import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { StateIndicator } from './StateIndicator';

describe('StateIndicator', () => {
  it('renders with default props', () => {
    render(<StateIndicator state="idle" />);
    const indicator = screen.getByTestId('state-indicator');
    expect(indicator).toBeInTheDocument();
  });

  it('applies size classes correctly', () => {
    const { rerender } = render(<StateIndicator state="idle" size="sm" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--sm');

    rerender(<StateIndicator state="idle" size="md" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--md');

    rerender(<StateIndicator state="idle" size="lg" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--lg');
  });

  it('applies state classes correctly', () => {
    const { rerender } = render(<StateIndicator state="idle" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--idle');

    rerender(<StateIndicator state="launching" seed="sess-1" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--launching');

    rerender(<StateIndicator state="working" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--working');

    rerender(<StateIndicator state="waiting_input" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--waiting-input');
  });

  it('applies kind classes correctly', () => {
    const { rerender } = render(<StateIndicator state="idle" kind="session" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--session');

    rerender(<StateIndicator state="idle" kind="pr" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--pr');
  });

  it('uses default size of md', () => {
    render(<StateIndicator state="idle" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--md');
  });

  it('uses default kind of session', () => {
    render(<StateIndicator state="idle" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('state-indicator--session');
  });

  it('applies custom className', () => {
    render(<StateIndicator state="idle" className="custom-class" />);
    expect(screen.getByTestId('state-indicator')).toHaveClass('custom-class');
  });

  it('normalizes waiting_input state to waiting-input CSS class', () => {
    render(<StateIndicator state="waiting_input" />);
    const indicator = screen.getByTestId('state-indicator');
    expect(indicator).toHaveClass('state-indicator--waiting-input');
    expect(indicator).not.toHaveClass('state-indicator--waiting_input');
  });

  it('renders unknown state class', () => {
    render(<StateIndicator state="unknown" />);
    const indicator = screen.getByTestId('state-indicator');
    expect(indicator).toHaveClass('state-indicator--unknown');
  });

  // An unknown badge with no explanation tells the user something is wrong and
  // nothing about what, which is the dead end the resolver's reason replaces.
  it('explains an unknown state from the resolver reason', () => {
    render(<StateIndicator state="unknown" reason="stuck" />);
    const indicator = screen.getByTestId('state-indicator');
    expect(indicator).toHaveAttribute('title', expect.stringContaining('Stuck'));
    expect(indicator.getAttribute('aria-label')).toContain('Stuck');
  });

  it('falls back rather than inventing an explanation for an unnamed reason', () => {
    render(<StateIndicator state="unknown" reason="some_future_clause" />);
    const indicator = screen.getByTestId('state-indicator');
    expect(indicator).not.toHaveAttribute('title');
    expect(indicator).toHaveAttribute('aria-label', 'state unknown');
  });

  // Every other state says what it means by its own name; a tooltip repeating
  // that is noise.
  it('does not explain states that speak for themselves', () => {
    render(<StateIndicator state="working" reason="heartbeat_busy" />);
    expect(screen.getByTestId('state-indicator')).not.toHaveAttribute('title');
  });

  it('renders launching state with emoji', () => {
    render(<StateIndicator state="launching" seed="session-emoji-seed" />);
    const indicator = screen.getByTestId('state-indicator');
    expect(indicator).toHaveClass('state-indicator--launching');
    expect(indicator.textContent?.length).toBeGreaterThan(0);
  });
});
