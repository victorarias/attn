import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import { WorktreeCleanupPrompt } from './WorktreeCleanupPrompt';

const defaultProps = {
  isVisible: true,
  worktreePath: '/tmp/repo/.worktrees/feature-a',
  branchName: 'feature-a',
  onKeep: vi.fn(),
  onDelete: vi.fn(),
  onAlwaysKeep: vi.fn(),
};

describe('WorktreeCleanupPrompt', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    Element.prototype.getBoundingClientRect = vi.fn(function getBoundingClientRect(this: Element) {
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
    _resetEscapeStackForTest();
  });

  it('collapses a slow delete into a keyboard-focusable compact job', async () => {
    const { container, rerender } = render(<WorktreeCleanupPrompt {...defaultProps} />);

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    rerender(<WorktreeCleanupPrompt {...defaultProps} isDeleting />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(700);
    });

    const compactJob = screen.getByRole('button', { name: /cleanup running/i });
    expect(compactJob).toHaveFocus();
    expect(container.querySelector('.worktree-cleanup-prompt')).toHaveClass('surface-hidden');

    fireEvent.click(compactJob);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(400);
    });

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(container.querySelector('.worktree-cleanup-prompt')).not.toHaveClass('surface-hidden');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500);
    });
    expect(container.querySelector('.worktree-cleanup-prompt')).not.toHaveClass('surface-hidden');
  });

  it('keeps a failed delete resumable from compact mode', async () => {
    const { rerender } = render(<WorktreeCleanupPrompt {...defaultProps} isDeleting />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(700);
    });
    rerender(
      <WorktreeCleanupPrompt
        {...defaultProps}
        deleteError="git worktree remove failed: contains modified files"
      />
    );

    const compactJob = screen.getByRole('button', { name: /delete failed/i });
    expect(compactJob).toHaveClass('failed');

    fireEvent.click(compactJob);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(400);
    });

    const retryButton = screen.getByRole('button', { name: 'Retry delete' });
    expect(retryButton).toHaveFocus();
    expect(screen.getByRole('alert')).toHaveTextContent('contains modified files');
  });

  it('supports arrow-key navigation between available actions', async () => {
    render(<WorktreeCleanupPrompt {...defaultProps} />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });
    const keepButton = screen.getByRole('button', { name: 'Keep' });
    expect(keepButton).toHaveFocus();

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'ArrowRight' });
    expect(screen.getByRole('button', { name: 'Delete worktree' })).toHaveFocus();

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'ArrowRight' });
    expect(screen.getByRole('button', { name: 'Always keep' })).toHaveFocus();
  });

  it('keeps Tab trapped on the dialog while delete is running', async () => {
    render(<WorktreeCleanupPrompt {...defaultProps} isDeleting />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(50);
    });

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveFocus();

    fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(dialog).toHaveFocus();
  });
});
