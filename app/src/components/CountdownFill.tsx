import { useEffect, useRef } from 'react';

/**
 * A one-shot bar animated against an absolute deadline using a CSS transition —
 * no per-tick setInterval, which would re-render every row on every tick. One
 * render, and the browser animates the rest; a hidden or backgrounded tile costs
 * nothing, because a transition on an unpainted element is not composited.
 *
 * `direction` picks which way it reads. 'fill' grows 0% -> 100%, for something
 * arriving. 'drain' shrinks 100% -> 0%, for something running out — a countdown
 * you might want to stop is more legible as time being consumed than as progress
 * being made, and the two directions are what keeps two concurrent countdowns on
 * the same row distinguishable at a glance.
 *
 * A mid-countdown remount restarts the animation from its endpoint but still
 * arrives at the right instant, since only the deadline is known, not the window.
 */
export function CountdownFill({
  firesAt,
  className,
  direction = 'fill',
}: {
  firesAt: string;
  className: string;
  direction?: 'fill' | 'drain';
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const from = direction === 'drain' ? '100%' : '0%';
    const to = direction === 'drain' ? '0%' : '100%';
    const remainingMs = new Date(firesAt).getTime() - Date.now();
    if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
      el.style.transition = 'none';
      el.style.width = to;
      return;
    }
    el.style.transition = 'none';
    el.style.width = from;
    // Force a reflow so the width change below actually animates from `from`.
    void el.offsetWidth;
    el.style.transition = `width ${remainingMs}ms linear`;
    el.style.width = to;
  }, [firesAt, direction]);
  return <div ref={ref} className={className} />;
}
