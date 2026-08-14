import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AppViewParamsPrompt } from './AppViewParamsPrompt';

// The field has to be typable the instant it appears. It opens from the command
// menu, and in the packaged app the menu's own focus trap hands focus back to
// the terminal as it closes — which is after this prompt mounts — so the field
// only wins if it holds focus rather than merely asking for it once.

function renderPrompt(overrides: Partial<Parameters<typeof AppViewParamsPrompt>[0]> = {}) {
  const onSubmit = vi.fn();
  const onClose = vi.fn();
  render(
    <AppViewParamsPrompt
      viewTitle="reviewer/approvals"
      label="Which ticket?"
      placeholder="t-1234"
      onSubmit={onSubmit}
      onClose={onClose}
      {...overrides}
    />,
  );
  return { onSubmit, onClose, input: screen.getByTestId('app-view-params-input') };
}

describe('the params prompt', () => {
  it('holds keyboard focus in the field, including against a later steal', async () => {
    const { input } = renderPrompt();
    await waitFor(() => expect(document.activeElement).toBe(input));

    const terminal = document.createElement('button');
    document.body.appendChild(terminal);
    terminal.focus();

    await waitFor(() => expect(document.activeElement).toBe(input));
    terminal.remove();
  });

  it('docks on Enter with what the user typed, trimmed', async () => {
    const { onSubmit, onClose, input } = renderPrompt();
    fireEvent.change(input, { target: { value: '  t-42  ' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSubmit).toHaveBeenCalledWith('t-42');
    expect(onClose).toHaveBeenCalled();
  });

  it('docks with no answer at all, which is the app’s problem to report', async () => {
    const { onSubmit } = renderPrompt();
    fireEvent.click(screen.getByTestId('app-view-params-dock'));
    expect(onSubmit).toHaveBeenCalledWith('');
  });
});
