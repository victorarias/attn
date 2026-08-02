/**
 * Snooze durations, and the arithmetic that turns one into an instant.
 *
 * The client computes the instant, not the daemon. "Tomorrow" and "Monday" are
 * calendar questions that need the user's timezone and their idea of a working
 * day, and the daemon that owns a remote session shares neither — it may not
 * even be in the same country. The wire carries an absolute instant, which
 * crosses endpoints without reinterpretation.
 */

export type SnoozeChoiceId = '30m' | '1h' | '8h' | 'tomorrow' | 'saturday' | 'monday';

export interface SnoozeChoice {
  id: SnoozeChoiceId;
  /** What the menu row says. */
  label: string;
  /**
   * The concrete time it resolves to, shown beside the label. A duration is
   * obvious; "Monday" is not, and a menu that will not say when it means is a
   * menu you have to test to trust.
   */
  detail: (now: Date) => string;
}

/**
 * The hour a day-named snooze wakes at. Not configurable: the choice a user
 * makes is "the start of that day", and a setting for which hour that is would
 * be a preference nobody has ever asked to hold.
 */
export const SNOOZE_WAKE_HOUR = 9;

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;

/** Weekday indices as Date.getDay reports them. */
const SATURDAY = 6;
const MONDAY = 1;

/**
 * The next occurrence of `weekday` at the wake hour, strictly in the future.
 *
 * Strictly is what makes "Saturday" pressed on a Saturday morning mean *next*
 * Saturday rather than a snooze that has already lapsed — the deferral has to
 * defer something.
 */
function nextWeekdayAt(now: Date, weekday: number): Date {
  const target = startOfWakeHour(now);
  let days = (weekday - now.getDay() + 7) % 7;
  if (days === 0 && target.getTime() <= now.getTime()) {
    days = 7;
  }
  target.setDate(target.getDate() + days);
  return target;
}

/** Today at the wake hour, as a Date that setDate can be walked forward on. */
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
 * When a choice wakes, as an absolute instant.
 *
 * Date arithmetic rather than millisecond addition for the day choices, so a
 * daylight-saving boundary still lands at 9am local — adding 24 hours across a
 * DST change lands at 8 or 10.
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
 * Whether a broadcast deadline is still in the future. The daemon leaves a
 * lapsed deadline off the wire, so this is only the client's own guard against
 * a snapshot it is holding while the wake lands.
 */
export function isSnoozed(until: string | undefined, now: number): boolean {
  if (!until) return false;
  const at = Date.parse(until);
  return Number.isFinite(at) && at > now;
}
