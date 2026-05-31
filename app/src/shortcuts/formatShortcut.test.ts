// app/src/shortcuts/formatShortcut.test.ts
import { describe, it, expect } from 'vitest';
import { formatShortcut, shortcutTokens, modifierTokens } from './formatShortcut';

describe('formatShortcut', () => {
  it('renders modifiers as Mac glyphs in ⌘ ⌥ ⇧ order', () => {
    expect(formatShortcut('session.new')).toBe('⌘N');
    expect(formatShortcut('session.newHorizontal')).toBe('⌘⇧N');
    expect(formatShortcut('session.newWorkspace')).toBe('⌘T');
    expect(formatShortcut('terminal.focusLeft')).toBe('⌘⌥←');
  });

  it('renders the close-session binding as ⌘W (guards against ⌘⇧W drift)', () => {
    expect(formatShortcut('session.close')).toBe('⌘W');
  });

  it('maps non-printable keys to symbols', () => {
    expect(formatShortcut('terminal.toggleMaximize')).toBe('⌘⇧⏎');
    expect(formatShortcut('terminal.focusDown')).toBe('⌘⌥↓');
  });

  it('exposes tokens as one entry per keycap', () => {
    expect(shortcutTokens('session.newHorizontal')).toEqual(['⌘', '⇧', 'N']);
    expect(modifierTokens('terminal.focusLeft')).toEqual(['⌘', '⌥']);
  });
});
