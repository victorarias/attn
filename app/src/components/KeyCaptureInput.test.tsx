import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { KeyCaptureInput } from './KeyCaptureInput';
import { setShortcutCaptureSuspended } from '../shortcuts/useShortcut';

afterEach(() => setShortcutCaptureSuspended(false));

function setup(overrides: Partial<Parameters<typeof KeyCaptureInput>[0]> = {}) {
  const onCapture = vi.fn();
  const onCaptureChord = vi.fn();
  const onCancel = vi.fn();
  render(
    <KeyCaptureInput
      binding={null}
      recording
      mode="chord"
      onStart={() => {}}
      onStartChord={() => {}}
      onCapture={onCapture}
      onCaptureChord={onCaptureChord}
      onCancel={onCancel}
      {...overrides}
    />,
  );
  return { onCapture, onCaptureChord, onCancel };
}

describe('KeyCaptureInput', () => {
  it('captures a single combo in combo mode', () => {
    const { onCapture } = setup({ mode: 'combo' });
    fireEvent.keyDown(window, { key: 'm', metaKey: true });
    expect(onCapture).toHaveBeenCalledWith({ key: 'm', meta: true });
  });

  it('captures a leader then a follow key as a chord', () => {
    const { onCaptureChord } = setup();
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    // After the leader, the prompt advances to ask for the follow key.
    expect(screen.getByText(/then/)).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'd' });
    expect(onCaptureChord).toHaveBeenCalledWith({
      leader: { key: 'k', meta: true },
      then: { key: 'd' },
    });
  });

  it('rejects a modifier-less leader', () => {
    const { onCaptureChord } = setup();
    fireEvent.keyDown(window, { key: 'a' });
    expect(screen.getByText(/needs a ⌘ or ⌥ modifier/)).toBeInTheDocument();
    expect(onCaptureChord).not.toHaveBeenCalled();
  });

  it('cancels on Escape', () => {
    const { onCancel, onCaptureChord } = setup();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalled();
    expect(onCaptureChord).not.toHaveBeenCalled();
  });

  it('shows a chord-record affordance when not recording', () => {
    render(
      <KeyCaptureInput
        binding={{ key: 'n', meta: true }}
        recording={false}
        mode="combo"
        onStart={() => {}}
        onStartChord={() => {}}
        onCapture={() => {}}
        onCaptureChord={() => {}}
        onCancel={() => {}}
      />,
    );
    expect(screen.getByLabelText('Record a chord')).toBeInTheDocument();
  });
});
