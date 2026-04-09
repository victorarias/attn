import { describe, expect, it, vi } from 'vitest';
import {
  focusTerminalViewport,
  resetTerminalViewport,
  scrollTerminalViewportToTop,
} from './terminalViewportActions';

describe('terminalViewportActions', () => {
  it('prefers terminal handle focus before xterm fallback', () => {
    const handle = {
      terminal: { focus: vi.fn() },
      focus: vi.fn(() => true),
    };
    const xterm = { focus: vi.fn() };

    expect(focusTerminalViewport(handle, xterm)).toBe('handle');
    expect(handle.focus).toHaveBeenCalledOnce();
    expect(xterm.focus).not.toHaveBeenCalled();
  });

  it('falls back to xterm focus when handle focus fails', () => {
    const handle = {
      terminal: { focus: vi.fn() },
      focus: vi.fn(() => false),
    };
    const xterm = { focus: vi.fn() };

    expect(focusTerminalViewport(handle, xterm)).toBe('xterm');
    expect(xterm.focus).toHaveBeenCalledOnce();
  });

  it('scrolls to top and resets scroll pin together', () => {
    const xterm = { scrollToTop: vi.fn() };
    const resetScrollPin = vi.fn();

    expect(scrollTerminalViewportToTop(xterm, resetScrollPin)).toBe(true);
    expect(xterm.scrollToTop).toHaveBeenCalledOnce();
    expect(resetScrollPin).toHaveBeenCalledWith(xterm);
  });

  it('resets terminal after clearing scroll pin', () => {
    const xterm = { reset: vi.fn() };
    const resetScrollPin = vi.fn();

    expect(resetTerminalViewport(xterm, resetScrollPin)).toBe(true);
    expect(resetScrollPin).toHaveBeenCalledWith(xterm);
    expect(xterm.reset).toHaveBeenCalledOnce();
  });
});
