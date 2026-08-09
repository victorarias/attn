/**
 * Snooze durations and the arithmetic turning one into an instant. The client
 * computes it: "tomorrow" needs the user's timezone, which a remote daemon does
 * not share. The wire carries an absolute instant.
 */

export type SnoozeChoiceId = '30m' | '1h' | '8h' | 'tomorrow' | 'saturday' | 'monday';

export interface SnoozeChoice {
  id: SnoozeChoiceId;
  label: string;
  /** The concrete time it resolves to, shown beside the label. */
  detail: (now: Date) => string;
}

/** The hour a day-named snooze wakes at. Deliberately not configurable. */
export const SNOOZE_WAKE_HOUR = 9;

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;

/** Weekday indices as Date.getDay reports them. */
const SATURDAY = 6;
const MONDAY = 1;

// Strictly in the future, so "Saturday" pressed on a Saturday morning means
// next Saturday rather than an already-lapsed snooze.
function nextWeekdayAt(now: Date, weekday: number): Date {
  const target = startOfWakeHour(now);
  let days = (weekday - now.getDay() + 7) % 7;
  if (days === 0 && target.getTime() <= now.getTime()) {
    days = 7;
  }
  target.setDate(target.getDate() + days);
  return target;
}

function startOfWakeHour(now: Date): Date {
  const at = new Date(now.getTime());
  at.setHours(SNOOZE_WAKE_HOUR, 0, 0, 0);
  return at;
}

function tomorrowAtWakeHour(now: Date): Date {
  const at = startOfWakeHour(now);
  at.setDate(at.getDate() + 1);
  return at;
}

/**
 * When a choice wakes, as an absolute instant. Day choices use Date arithmetic,
 * not millisecond addition: +24h across a DST boundary lands at 8am or 10am.
 */
export function snoozeInstant(choice: SnoozeChoiceId, now: Date): Date {
  switch (choice) {
    case '30m':
      return new Date(now.getTime() + 30 * MINUTE);
    case '1h':
      return new Date(now.getTime() + HOUR);
    case '8h':
      return new Date(now.getTime() + 8 * HOUR);
    case 'tomorrow':
      return tomorrowAtWakeHour(now);
    case 'saturday':
      return nextWeekdayAt(now, SATURDAY);
    case 'monday':
      return nextWeekdayAt(now, MONDAY);
  }
}

/** A wake instant as the user reads it: a clock time, plus the day when it is not today. */
export function formatWakeTime(until: string | undefined, now: number): string {
  if (!until) return '';
  const at = new Date(until);
  if (Number.isNaN(at.getTime())) return '';
  const time = at.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  const today = new Date(now);
  if (at.toDateString() === today.toDateString()) return time;
  const tomorrow = new Date(now);
  tomorrow.setDate(tomorrow.getDate() + 1);
  if (at.toDateString() === tomorrow.toDateString()) return `tomorrow ${time}`;
  return `${at.toLocaleDateString(undefined, { weekday: 'short' })} ${time}`;
}

export const SNOOZE_CHOICES: SnoozeChoice[] = [
  { id: '30m', label: 'For 30 minutes', detail: (now) => clockTime(snoozeInstant('30m', now)) },
  { id: '1h', label: 'For an hour', detail: (now) => clockTime(snoozeInstant('1h', now)) },
  { id: '8h', label: 'For 8 hours', detail: (now) => clockTime(snoozeInstant('8h', now)) },
  { id: 'tomorrow', label: 'Until tomorrow', detail: (now) => dayAndTime(snoozeInstant('tomorrow', now)) },
  { id: 'saturday', label: 'Until Saturday', detail: (now) => dayAndTime(snoozeInstant('saturday', now)) },
  { id: 'monday', label: 'Until Monday', detail: (now) => dayAndTime(snoozeInstant('monday', now)) },
];

function clockTime(at: Date): string {
  return at.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
}

function dayAndTime(at: Date): string {
  return `${at.toLocaleDateString(undefined, { weekday: 'short' })} ${clockTime(at)}`;
}

/**
 * Whether a broadcast deadline is still in the future — the client's guard for a
 * snapshot held while the wake lands; the daemon never sends a lapsed deadline.
 */
export function isSnoozed(until: string | undefined, now: number): boolean {
  if (!until) return false;
  const at = Date.parse(until);
  return Number.isFinite(at) && at > now;
}
