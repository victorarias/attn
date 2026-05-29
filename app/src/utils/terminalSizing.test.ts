import { describe, expect, it } from 'vitest';
import { getTerminalAnsiPalette } from './terminalSizing';

describe('getTerminalAnsiPalette', () => {
  it('supplies all dark terminal ANSI colors to Ghostty', () => {
    expect(getTerminalAnsiPalette('dark')).toEqual([
      0x000000, 0xcd3131, 0x0dbc79, 0xe5e510,
      0x2472c8, 0xbc3fbc, 0x11a8cd, 0xe5e5e5,
      0x666666, 0xf14c4c, 0x23d18b, 0xf5f543,
      0x3b8eea, 0xd670d6, 0x29b8db, 0xffffff,
    ]);
  });

  it('supplies all light terminal ANSI colors to Ghostty', () => {
    expect(getTerminalAnsiPalette('light')).toHaveLength(16);
    expect(getTerminalAnsiPalette('light')[4]).toBe(0x0451a5);
  });
});
