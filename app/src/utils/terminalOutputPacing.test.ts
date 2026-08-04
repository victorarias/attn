import { describe, expect, it } from 'vitest';
import {
  TERMINAL_OUTPUT_FRAME_MS,
  TERMINAL_OUTPUT_IDLE_RESET_MS,
  terminalOutputDelayMs,
} from './terminalOutputPacing';

describe('terminalOutputDelayMs', () => {
  it('paints the first output after idle without an added timer delay', () => {
    expect(terminalOutputDelayMs(100, null)).toBe(0);
    expect(terminalOutputDelayMs(100, 100 - TERMINAL_OUTPUT_IDLE_RESET_MS)).toBe(0);
  });

  it('paces continuous output to the configured frame interval', () => {
    expect(terminalOutputDelayMs(110, 100)).toBeCloseTo(TERMINAL_OUTPUT_FRAME_MS - 10);
    expect(terminalOutputDelayMs(100 + TERMINAL_OUTPUT_FRAME_MS, 100)).toBe(0);
  });

  it('never returns a negative delay', () => {
    expect(terminalOutputDelayMs(200, 100)).toBe(0);
  });
});
