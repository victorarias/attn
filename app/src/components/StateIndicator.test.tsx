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

  it('renders launching state with emoji', () => {
    render(<StateIndicator state="launching" seed="session-emoji-seed" />);
    const indicator = screen.getByTestId('state-indicator');
    expect(indicator).toHaveClass('state-indicator--launching');
    expect(indicator.textContent?.length).toBeGreaterThan(0);
  });
});
