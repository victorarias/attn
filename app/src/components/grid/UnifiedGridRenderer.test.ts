import { describe, expect, it } from 'vitest';
import { scheduledPulse, waitingInputFlash } from './UnifiedGridRenderer';

describe('waitingInputFlash', () => {
  it('produces a repeating flash with a clear dim and bright phase', () => {
    expect(waitingInputFlash(0)).toBeCloseTo(0.25);
    expect(waitingInputFlash(400)).toBeCloseTo(1);
    expect(waitingInputFlash(1_200)).toBeCloseTo(0);
    expect(waitingInputFlash(2_000)).toBeCloseTo(1);
  });
});

describe('scheduledPulse', () => {
  it('is a gentle wave bounded in [0,1] that breathes over a slow period', () => {
    expect(scheduledPulse(0)).toBeCloseTo(0.5);
    expect(scheduledPulse(800)).toBeCloseTo(1); // quarter of the 3.2s period
    expect(scheduledPulse(1_600)).toBeCloseTo(0.5);
    expect(scheduledPulse(2_400)).toBeCloseTo(0); // three-quarters
  });

  it('breathes slower than the waiting_input flash', () => {
    // waiting_input peaks by 400ms; scheduled is still rising past 600ms.
    expect(scheduledPulse(600)).toBeLessThan(1);
    expect(scheduledPulse(600)).toBeGreaterThan(0.5);
  });
});
