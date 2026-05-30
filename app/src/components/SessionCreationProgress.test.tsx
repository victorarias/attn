import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionCreationProgress } from './SessionCreationProgress';

describe('SessionCreationProgress', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function getBoundingClientRect(this: Element) {
      if (this.classList.contains('worktree-cleanup-compact')) {
        return {
          x: 900,
          y: 680,
          left: 900,
          top: 680,
          right: 1240,
          bottom: 760,
          width: 340,
          height: 80,
          toJSON: () => ({}),
        } as DOMRect;
      }
      return {
        x: 360,
        y: 230,
        left: 360,
        top: 230,
        right: 920,
        bottom: 430,
        width: 560,
        height: 200,
        toJSON: () => ({}),
      } as DOMRect;
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('collapses a running creation job into a compact job', async () => {
    const { container } = render(
      <>
        <button type="button">Other work</button>
        <SessionCreationProgress
          isVisible
          label="feat-async"
          path="/tmp/repo"
          phase="creating_worktree"
        />
      </>,
    );

    expect(screen.getByRole('dialog')).toHaveTextContent('Creating worktree');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(700);
    });

    expect(screen.getByRole('button', { name: /session setup/i })).toHaveFocus();
    expect(container.querySelector('.worktree-cleanup-prompt')).toHaveClass('surface-hidden');

    screen.getByRole('button', { name: 'Other work' }).focus();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(screen.getByRole('button', { name: 'Other work' })).toHaveFocus();
  });

  it('keeps failures reopenable from compact mode', async () => {
    const onDismiss = vi.fn();
    const { rerender } = render(
      <SessionCreationProgress
        isVisible
        label="feat-async"
        path="/tmp/repo"
        phase="creating_worktree"
      />,
    );

    await act(async () => {
      await vi.advanceTimersByTimeAsync(700);
    });

    rerender(
      <SessionCreationProgress
        isVisible
        label="feat-async"
        path="/tmp/repo"
        phase="creating_worktree"
        error="branch already exists"
        onDismiss={onDismiss}
      />,
    );

    const compactJob = screen.getByRole('button', { name: /create failed/i });
    expect(compactJob).toHaveClass('failed');

    fireEvent.click(compactJob);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(400);
    });

    expect(screen.getByRole('alert')).toHaveTextContent('branch already exists');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });
});
