import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installTerminalKeyHandler } from './terminalKeyHandler';
import { triggerShortcut, hasHandler } from '../../shortcuts/useShortcut';
import { setShortcutOverrides } from '../../shortcuts/resolver';
import { cancelLeader, isLeaderPending } from '../../shortcuts/chordState';

vi.mock('../../shortcuts/useShortcut', () => ({
  triggerShortcut: vi.fn(),
  hasHandler: vi.fn(() => true),
}));

describe('installTerminalKeyHandler', () => {
  beforeEach(() => {
    vi.mocked(triggerShortcut).mockReset();
    vi.mocked(hasHandler).mockReturnValue(true);
  });

  afterEach(() => {
    setShortcutOverrides({});
    cancelLeader();
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

  it('routes Cmd+Q to app quit when terminal owns the key event', () => {
    vi.mocked(triggerShortcut).mockReturnValue(true);
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: 'q',
      metaKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(triggerShortcut).toHaveBeenCalledWith('app.quit');
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('routes Cmd+/ to the shortcuts cheatsheet when terminal owns the key event', () => {
    vi.mocked(triggerShortcut).mockReturnValue(true);
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', {
      key: '/',
      metaKey: true,
    });

    expect(handler(event)).toBe(false);
    expect(triggerShortcut).toHaveBeenCalledWith('ui.showShortcuts');
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

  describe('leader-key chords', () => {
    // Bind a chord (⌘K then D) to an action; the leader collides with no
    // terminal intercept, so the chord layer owns it.
    const CHORD = { leader: { key: 'k', meta: true }, then: { key: 'd' } };

    it('arms the leader and consumes it without leaking to the PTY', () => {
      setShortcutOverrides({ 'dock.diff': CHORD });
      const sendToPty = vi.fn();
      const handler = installTerminalKeyHandler(sendToPty);

      const leader = new KeyboardEvent('keydown', { key: 'k', metaKey: true });
      expect(handler(leader)).toBe(false);
      expect(isLeaderPending()).toBe(true);
      expect(sendToPty).not.toHaveBeenCalled();
      expect(triggerShortcut).not.toHaveBeenCalled();
    });

    it('fires the bound action on the follow key, still consuming it', () => {
      vi.mocked(triggerShortcut).mockReturnValue(true);
      setShortcutOverrides({ 'dock.diff': CHORD });
      const sendToPty = vi.fn();
      const handler = installTerminalKeyHandler(sendToPty);

      handler(new KeyboardEvent('keydown', { key: 'k', metaKey: true }));
      const follow = new KeyboardEvent('keydown', { key: 'd' });
      expect(handler(follow)).toBe(false);
      expect(triggerShortcut).toHaveBeenCalledWith('dock.diff');
      expect(sendToPty).not.toHaveBeenCalled();
      expect(isLeaderPending()).toBe(false);
    });

    it('consumes a non-matching follow key as a cancel, never reaching the PTY', () => {
      setShortcutOverrides({ 'dock.diff': CHORD });
      const sendToPty = vi.fn();
      const handler = installTerminalKeyHandler(sendToPty);

      handler(new KeyboardEvent('keydown', { key: 'k', metaKey: true }));
      const stray = new KeyboardEvent('keydown', { key: 'x' });
      expect(handler(stray)).toBe(false);
      expect(triggerShortcut).not.toHaveBeenCalled();
      expect(sendToPty).not.toHaveBeenCalled();
      expect(isLeaderPending()).toBe(false);
    });

    it('does not arm a leader when no follow action has a handler', () => {
      vi.mocked(hasHandler).mockReturnValue(false);
      setShortcutOverrides({ 'dock.diff': CHORD });
      const sendToPty = vi.fn();
      const handler = installTerminalKeyHandler(sendToPty);

      // ⌘K with no fireable candidate falls through; nothing arms, nothing leaks.
      expect(handler(new KeyboardEvent('keydown', { key: 'k', metaKey: true }))).toBe(true);
      expect(isLeaderPending()).toBe(false);
    });
  });
});
