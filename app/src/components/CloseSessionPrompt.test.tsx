import { fireEvent, render, screen } from '../test/utils';
import { describe, expect, it, vi } from 'vitest';
import { CloseSessionPrompt } from './CloseSessionPrompt';

describe('CloseSessionPrompt', () => {
  it('confirms with Enter, Space, and Y', () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();

    const { rerender } = render(
      <CloseSessionPrompt
        isVisible
        sessionLabel="repo-a"
        splitCount={2}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );

    const dialog = screen.getByRole('dialog');
    fireEvent.keyDown(dialog, { key: 'Enter' });
    expect(onConfirm).toHaveBeenCalledTimes(1);

    rerender(
      <CloseSessionPrompt
        isVisible
        sessionLabel="repo-a"
        splitCount={2}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );
    fireEvent.keyDown(screen.getByRole('dialog'), { key: ' ' });
    expect(onConfirm).toHaveBeenCalledTimes(2);

    rerender(
      <CloseSessionPrompt
        isVisible
        sessionLabel="repo-a"
        splitCount={2}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'y' });
    expect(onConfirm).toHaveBeenCalledTimes(3);
    expect(onCancel).not.toHaveBeenCalled();
  });

  it('cancels with N and Escape', () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();

    const { rerender } = render(
      <CloseSessionPrompt
        isVisible
        sessionLabel="repo-a"
        splitCount={1}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'n' });
    expect(onCancel).toHaveBeenCalledTimes(1);

    rerender(
      <CloseSessionPrompt
        isVisible
        sessionLabel="repo-a"
        splitCount={1}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('uses the focused button for Enter and Space', () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();

    render(
      <CloseSessionPrompt
        isVisible
        sessionLabel="repo-a"
        splitCount={1}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    );

    const dialog = screen.getByRole('dialog');
    const cancelButton = screen.getByRole('button', { name: 'Keep Session' });
    cancelButton.focus();

    fireEvent.keyDown(dialog, { key: 'Enter' });
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();

    fireEvent.keyDown(dialog, { key: ' ' });
    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
