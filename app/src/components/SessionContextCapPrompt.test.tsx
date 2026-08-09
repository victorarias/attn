import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../test/utils';
import { SessionContextCapPrompt } from './SessionContextCapPrompt';

describe('SessionContextCapPrompt', () => {
  it('prefills the current cap and submits a changed value as a number', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(
      <SessionContextCapPrompt
        sessionLabel="trellis"
        currentCap={300000}
        onSubmit={onSubmit}
        onClose={onClose}
      />,
    );

    const input = screen.getByTestId('context-cap-input') as HTMLInputElement;
    expect(input.value).toBe('300000');

    fireEvent.change(input, { target: { value: '800000' } });
    fireEvent.click(screen.getByTestId('context-cap-save'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(800000));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('submits 0 for a blank value so the pin clears', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(
      <SessionContextCapPrompt
        sessionLabel="trellis"
        currentCap={300000}
        onSubmit={onSubmit}
        onClose={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByTestId('context-cap-input'), { target: { value: '' } });
    fireEvent.click(screen.getByTestId('context-cap-save'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(0));
  });

  it('closes without submitting when the value did not change', () => {
    const onSubmit = vi.fn();
    const onClose = vi.fn();
    render(
      <SessionContextCapPrompt
        sessionLabel="trellis"
        currentCap={300000}
        onSubmit={onSubmit}
        onClose={onClose}
      />,
    );

    fireEvent.click(screen.getByTestId('context-cap-save'));

    expect(onSubmit).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('shows the daemon error and stays open when the submit rejects', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('context window cap must be 0 (no cap) or between 10000 and 2000000 tokens; got 5'));
    const onClose = vi.fn();
    render(
      <SessionContextCapPrompt
        sessionLabel="trellis"
        onSubmit={onSubmit}
        onClose={onClose}
      />,
    );

    fireEvent.change(screen.getByTestId('context-cap-input'), { target: { value: '5' } });
    fireEvent.click(screen.getByTestId('context-cap-save'));

    await waitFor(() => expect(screen.getByTestId('context-cap-error').textContent).toContain('between 10000 and 2000000'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('rejects a non-integer locally', async () => {
    const onSubmit = vi.fn();
    render(
      <SessionContextCapPrompt
        sessionLabel="trellis"
        onSubmit={onSubmit}
        onClose={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByTestId('context-cap-input'), { target: { value: '1.5' } });
    fireEvent.click(screen.getByTestId('context-cap-save'));

    await waitFor(() => expect(screen.getByTestId('context-cap-error')).toBeTruthy());
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
