import { fireEvent, render, screen, waitFor } from '../test/utils';
import { describe, expect, it, vi } from 'vitest';
import { RenamePopover } from './RenamePopover';

function renderPopover(overrides: Partial<Parameters<typeof RenamePopover>[0]> = {}) {
  const onSubmit = overrides.onSubmit ?? vi.fn(async () => {});
  const onClose = overrides.onClose ?? vi.fn();
  render(
    <RenamePopover
      initialValue="original-name"
      label="Rename session"
      anchor={{ top: 100, left: 40 }}
      onSubmit={onSubmit}
      onClose={onClose}
    />,
  );
  return { onSubmit, onClose };
}

describe('RenamePopover', () => {
  it('focuses and selects the whole name so typing replaces it', () => {
    renderPopover();
    const input = screen.getByRole('textbox') as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    expect(input.selectionStart).toBe(0);
    expect(input.selectionEnd).toBe('original-name'.length);
  });

  it('submits the trimmed new value on Enter and then closes', async () => {
    const onSubmit = vi.fn(async () => {});
    const onClose = vi.fn();
    renderPopover({ onSubmit, onClose });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '  renamed  ' } });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith('renamed'));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });

  it('rejects an empty name without calling onSubmit', () => {
    const onSubmit = vi.fn(async () => {});
    renderPopover({ onSubmit });

    const input = screen.getByRole('textbox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '   ' } });
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText('Name cannot be empty')).toBeInTheDocument();
  });

  it('closes without submitting when the name is unchanged', () => {
    const onSubmit = vi.fn(async () => {});
    const onClose = vi.fn();
    renderPopover({ onSubmit, onClose });

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Enter' });

    expect(onSubmit).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes on Escape', () => {
    const onClose = vi.fn();
    renderPopover({ onClose });

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
