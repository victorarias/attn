import { describe, expect, it, vi } from 'vitest';
import { installTerminalKeyHandler } from './terminalKeyHandler';

describe('installTerminalKeyHandler', () => {
  it('sends Shift+Tab as terminal reverse-tab input', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: 'Tab',
      shiftKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(sendToPty).toHaveBeenCalledWith('\x1b[Z');
  });

  it('leaves plain Tab for Ghostty to encode', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: 'Tab',
    });

    expect(handler(event)).toBe(true);
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('handles WebKit reverse-tab key names', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: 'ISO_Left_Tab',
      shiftKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(sendToPty).toHaveBeenCalledWith('\x1b[Z');
  });
});
