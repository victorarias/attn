import { describe, expect, it } from 'vitest';
import { waitingInputFlash } from './UnifiedGridRenderer';

describe('waitingInputFlash', () => {
  it('produces a repeating flash with a clear dim and bright phase', () => {
    expect(waitingInputFlash(0)).toBeCloseTo(0.25);
    expect(waitingInputFlash(400)).toBeCloseTo(1);
    expect(waitingInputFlash(1_200)).toBeCloseTo(0);
    expect(waitingInputFlash(2_000)).toBeCloseTo(1);
  });
});
