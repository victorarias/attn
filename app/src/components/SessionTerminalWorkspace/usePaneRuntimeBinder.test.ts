import { describe, expect, it, vi, beforeEach } from 'vitest';
import { installTerminalKeyHandler } from './usePaneRuntimeBinder';

const { mockTriggerShortcut, mockIsMacLikePlatform } = vi.hoisted(() => ({
  mockTriggerShortcut: vi.fn(() => false),
  mockIsMacLikePlatform: vi.fn(() => true),
}));

vi.mock('../../shortcuts/useShortcut', () => ({
  triggerShortcut: mockTriggerShortcut,
}));

vi.mock('../../shortcuts/platform', () => ({
  isMacLikePlatform: mockIsMacLikePlatform,
}));

describe('installTerminalKeyHandler', () => {
  beforeEach(() => {
    mockTriggerShortcut.mockReset().mockReturnValue(false);
    mockIsMacLikePlatform.mockReset().mockReturnValue(true);
  });

  it('swallows cmd+w even when no pane close shortcut is registered', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', { key: 'w', metaKey: true });

    expect(handler(event)).toBe(false);
    expect(mockTriggerShortcut).toHaveBeenCalledWith('terminal.close');
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('leaves ctrl+w alone on macOS so shells can erase the previous word', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', { key: 'w', ctrlKey: true });

    expect(handler(event)).toBe(true);
    expect(mockTriggerShortcut).not.toHaveBeenCalled();
    expect(sendToPty).not.toHaveBeenCalled();
  });
});
