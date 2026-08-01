import { useEffect, useState } from 'react';

/**
 * A wall clock that re-renders on an interval.
 *
 * Ages shown against a timestamp — how long a turn has been owed, say — are
 * read from a clock rather than from a prop, so a row outstanding for an hour
 * does not keep claiming it arrived a minute ago whenever the daemon goes quiet.
 */
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

/** How often turn ages re-read the clock. Shared by the queue band and home. */
export const TURN_AGE_TICK_MS = 30_000;
