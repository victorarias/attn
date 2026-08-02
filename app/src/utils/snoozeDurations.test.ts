import { describe, it, expect } from 'vitest';
import { SNOOZE_WAKE_HOUR, snoozeInstant, isSnoozed } from './snoozeDurations';

/** A local-time Date, so the assertions read in the same clock the code uses. */
function at(year: number, month: number, day: number, hour: number, minute = 0): Date {
  return new Date(year, month - 1, day, hour, minute, 0, 0);
}

describe('snoozeInstant', () => {
  it('adds the plain durations', () => {
    const now = at(2026, 8, 2, 14, 20);
    expect(snoozeInstant('30m', now)).toEqual(at(2026, 8, 2, 14, 50));
    expect(snoozeInstant('1h', now)).toEqual(at(2026, 8, 2, 15, 20));
    expect(snoozeInstant('8h', now)).toEqual(at(2026, 8, 2, 22, 20));
  });

  it('wakes tomorrow at the wake hour, not 24 hours later', () => {
    const now = at(2026, 8, 2, 23, 45);
    expect(snoozeInstant('tomorrow', now)).toEqual(at(2026, 8, 3, SNOOZE_WAKE_HOUR));
  });

  it('crosses a month boundary', () => {
    const now = at(2026, 8, 31, 18, 0);
    expect(snoozeInstant('tomorrow', now)).toEqual(at(2026, 9, 1, SNOOZE_WAKE_HOUR));
  });

  it('crosses a year boundary', () => {
    const now = at(2026, 12, 31, 18, 0);
    expect(snoozeInstant('tomorrow', now)).toEqual(at(2027, 1, 1, SNOOZE_WAKE_HOUR));
  });

  // 2026-08-02 is a Sunday.
  it('finds the next Saturday and the next Monday from a Sunday', () => {
    const sunday = at(2026, 8, 2, 10, 0);
    expect(snoozeInstant('saturday', sunday)).toEqual(at(2026, 8, 8, SNOOZE_WAKE_HOUR));
    expect(snoozeInstant('monday', sunday)).toEqual(at(2026, 8, 3, SNOOZE_WAKE_HOUR));
  });

  it('finds later the same day when the wake hour has not passed', () => {
    const mondayEarly = at(2026, 8, 3, 7, 30);
    expect(snoozeInstant('monday', mondayEarly)).toEqual(at(2026, 8, 3, SNOOZE_WAKE_HOUR));
  });

  // The one that matters: a deferral has to defer something. "Until Monday"
  // pressed on a Monday afternoon means next Monday, not an instant already gone.
  it('skips to next week when the day has already had its wake hour', () => {
    const mondayAfternoon = at(2026, 8, 3, 15, 0);
    expect(snoozeInstant('monday', mondayAfternoon)).toEqual(at(2026, 8, 10, SNOOZE_WAKE_HOUR));
  });

  it('skips a day whose wake hour is exactly now', () => {
    const mondayAtNine = at(2026, 8, 3, SNOOZE_WAKE_HOUR);
    expect(snoozeInstant('monday', mondayAtNine)).toEqual(at(2026, 8, 10, SNOOZE_WAKE_HOUR));
  });

  it('never resolves to the past, whichever choice and whenever it is pressed', () => {
    const choices = ['30m', '1h', '8h', 'tomorrow', 'saturday', 'monday'] as const;
    for (let day = 1; day <= 7; day += 1) {
      for (const hour of [0, 8, 9, 10, 23]) {
        const now = at(2026, 8, day, hour, 30);
        for (const choice of choices) {
          expect(snoozeInstant(choice, now).getTime()).toBeGreaterThan(now.getTime());
        }
      }
    }
  });
});

describe('isSnoozed', () => {
  it('is false without a deadline and for one already past', () => {
    const now = Date.parse('2026-08-02T14:00:00Z');
    expect(isSnoozed(undefined, now)).toBe(false);
    expect(isSnoozed('2026-08-02T13:00:00Z', now)).toBe(false);
    expect(isSnoozed('not a date', now)).toBe(false);
  });

  it('is true for a deadline still ahead', () => {
    const now = Date.parse('2026-08-02T14:00:00Z');
    expect(isSnoozed('2026-08-02T15:00:00Z', now)).toBe(true);
  });
});
