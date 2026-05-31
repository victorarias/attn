import { beforeEach, describe, expect, it, vi } from 'vitest';
import { installTerminalKeyHandler } from './terminalKeyHandler';
import { triggerShortcut } from '../../shortcuts/useShortcut';

vi.mock('../../shortcuts/useShortcut', () => ({
  triggerShortcut: vi.fn(),
}));

describe('installTerminalKeyHandler', () => {
  beforeEach(() => {
    vi.mocked(triggerShortcut).mockReset();
  });

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

  it('routes Cmd+T to new-workspace shortcut when terminal owns the key event', () => {
    vi.mocked(triggerShortcut).mockReturnValue(true);
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: 't',
      metaKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(triggerShortcut).toHaveBeenCalledWith('session.newWorkspace');
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('routes Cmd+Shift+N to new horizontal session when terminal owns the key event', () => {
    vi.mocked(triggerShortcut).mockReturnValue(true);
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: 'N',
      metaKey: true,
      shiftKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(triggerShortcut).toHaveBeenCalledWith('session.newHorizontal');
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('routes Cmd+number to workspace selection when terminal owns the key event', () => {
    vi.mocked(triggerShortcut).mockReturnValue(true);
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: '™',
      code: 'Digit2',
      metaKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(triggerShortcut).toHaveBeenCalledWith('workspace.select2');
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('routes Cmd+number by key when code is unavailable', () => {
    vi.mocked(triggerShortcut).mockReturnValue(true);
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: '3',
      metaKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(triggerShortcut).toHaveBeenCalledWith('workspace.select3');
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('falls back to session close for Cmd+W when no split-pane close handler is active', () => {
    vi.mocked(triggerShortcut).mockImplementation((shortcut) => shortcut === 'session.close');
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: 'w',
      metaKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(triggerShortcut).toHaveBeenNthCalledWith(1, 'terminal.close');
    expect(triggerShortcut).toHaveBeenNthCalledWith(2, 'session.close');
    expect(sendToPty).not.toHaveBeenCalled();
  });
});
