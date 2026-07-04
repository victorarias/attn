import { describe, expect, it } from 'vitest';
import { recoveryDelayMs } from './GhosttyTerminal';

describe('recoveryDelayMs (WebGL context-loss recovery backoff)', () => {
  it('escalates delays across the first three attempts', () => {
    expect(recoveryDelayMs(1)).toBe(250);
    expect(recoveryDelayMs(2)).toBe(1500);
    expect(recoveryDelayMs(3)).toBe(5000);
  });

  it('gives up (null) once the schedule is exhausted', () => {
    expect(recoveryDelayMs(4)).toBeNull();
    expect(recoveryDelayMs(5)).toBeNull();
    expect(recoveryDelayMs(100)).toBeNull();
  });
});
