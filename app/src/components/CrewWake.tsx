import { useCallback, useEffect, useRef, useState, type CSSProperties, type RefObject } from 'react';
import './CrewWake.css';

/**
 * Waking a crew member starts a day of a durable identity and cannot be
 * un-rung, so it takes two deliberate clicks: the first arms, the second wakes.
 * Only the app asks twice — `attn crew wake` stays one step, because typing a
 * command is already the conscious act this is reconstructing for a click.
 */
export type WakePhase = 'rest' | 'armed' | 'breaking';

/**
 * How long an armed wake waits before standing down.
 *
 * A tripwire, not a deadline: a deliberate confirm is a second click on a
 * target the pointer is already over — a few hundred milliseconds — so four
 * seconds is roughly ten times the gesture it must never interrupt, and short
 * enough that an arm you walked away from is gone before you come back to the
 * row. Nobody confirming should ever feel this number exists.
 */
export const WAKE_ARM_TIMEOUT_MS = 4000;

/** How long the confirm flare owns the row before the row is just a row again. */
const WAKE_BREAK_MS = 620;

interface WakeConfirm {
  phase: WakePhase;
  /** Arms on the first call, wakes on the second. */
  trigger: () => void;
  /** Put this on the row: anything inside it counts as still holding the intent. */
  rowRef: RefObject<HTMLDivElement | null>;
}

/**
 * The two-step over one member's wake.
 *
 * An armed wake disarms itself four ways — a click outside the row, focus
 * leaving the row, Escape, and the timeout above — so an arm nobody confirms
 * never wakes anyone. Leaving the row is also what gives the roster single-arm
 * for free: reaching a second member means leaving the first one's row, by
 * pointer or by keyboard.
 */
export function useWakeConfirm(onWake: (() => void) | undefined): WakeConfirm {
  const [phase, setPhase] = useState<WakePhase>('rest');
  const rowRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (phase !== 'armed') return;
    const disarm = () => setPhase('rest');
    const onPointerDown = (event: PointerEvent) => {
      if (rowRef.current?.contains(event.target as Node)) return;
      disarm();
    };
    // Focus is the keyboard's pointer: tabbing to another row is that user
    // leaving this one, and without this the roster's one-arm-at-a-time rule
    // would hold for the mouse and quietly not for the keyboard.
    const onFocusIn = (event: FocusEvent) => {
      if (rowRef.current?.contains(event.target as Node)) return;
      disarm();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') disarm();
    };
    const timer = window.setTimeout(disarm, WAKE_ARM_TIMEOUT_MS);
    document.addEventListener('pointerdown', onPointerDown, true);
    document.addEventListener('focusin', onFocusIn, true);
    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener('pointerdown', onPointerDown, true);
      document.removeEventListener('focusin', onFocusIn, true);
      document.removeEventListener('keydown', onKeyDown, true);
    };
  }, [phase]);

  useEffect(() => {
    if (phase !== 'breaking') return;
    const timer = window.setTimeout(() => setPhase('rest'), WAKE_BREAK_MS);
    return () => window.clearTimeout(timer);
  }, [phase]);

  // Reads `phase` rather than an updater, because sending the wake is a side
  // effect and StrictMode runs an updater twice — which would wake the member
  // twice from the one confirmed click this whole two-step exists to earn.
  const trigger = useCallback(() => {
    if (!onWake) return;
    // The flare is not a target. A click landing in it would otherwise re-arm a
    // row whose wake is already sent, and the click after that would send a
    // second one — the double-wake this whole two-step exists to prevent.
    if (phase === 'breaking') return;
    if (phase !== 'armed') {
      setPhase('armed');
      return;
    }
    setPhase('breaking');
    // The command goes now, not when the flare ends: the animation reports the
    // wake, it does not gate it.
    onWake();
  }, [onWake, phase]);

  return { phase, trigger, rowRef };
}

/** Where the light fans, in degrees off vertical. Upward only — below the sun
 *  is still night, and a ray drawn there would cross the horizon. */
const RAY_ANGLES = [-72, -36, 0, 36, 72];
const HORIZON_Y = 13;
const RAY_INNER = 5;
const RAY_OUTER = 7.6;

function rayPath(angle: number): string {
  const radians = (angle * Math.PI) / 180;
  const point = (radius: number) => [
    (10 + radius * Math.sin(radians)).toFixed(2),
    (HORIZON_Y - radius * Math.cos(radians)).toFixed(2),
  ];
  const [x1, y1] = point(RAY_INNER);
  const [x2, y2] = point(RAY_OUTER);
  return `M${x1} ${y1}L${x2} ${y2}`;
}

/**
 * A sunrise, held.
 *
 * At rest the sun sits on the horizon with its light barely out — dim, low,
 * asleep. Arming raises it clear of the horizon, fans the rays from the top
 * outward, widens the ground beneath it and warms everything to the brand's
 * own sunrise orange, then stops and stays there: the held breath before
 * morning is a still frame, because a loop that never stops repainting is a
 * battery bug on a machine that runs attn all day.
 *
 * Confirming lets it break — the sun leaps, a shockwave of first light rings
 * out, and the row itself takes the wash (`.queue-row--crew[data-crew-wake]`).
 * Standing down runs the whole thing backwards, which is a sunset.
 */
export function CrewWakeSun({ phase }: { phase: WakePhase }) {
  const lit = phase !== 'rest';
  return (
    <svg
      className={`crew-sun crew-sun--${phase}`}
      viewBox="0 0 20 20"
      aria-hidden="true"
      focusable="false"
    >
      {/* Drawn outward from directly beneath the sun, so the ground widens with
          the light rather than being there all along. */}
      <path className="crew-sun-horizon" d={`M10 ${HORIZON_Y}H3`} pathLength={1} />
      <path className="crew-sun-horizon" d={`M10 ${HORIZON_Y}H17`} pathLength={1} />
      {lit && <circle className="crew-sun-glow" cx="10" cy="8.8" r="6.4" />}
      {phase === 'breaking' && <circle className="crew-sun-shock" cx="10" cy="8.8" r="3.4" />}
      <g className="crew-sun-body">
        {RAY_ANGLES.map((angle, index) => (
          <path
            key={angle}
            className="crew-sun-ray"
            // The fan opens from the top ray outward, so the light spreads
            // instead of arriving all at once.
            style={{ '--ray': Math.abs(index - 2) } as CSSProperties}
            d={rayPath(angle)}
            pathLength={1}
          />
        ))}
        <circle className="crew-sun-disc" cx="10" cy={HORIZON_Y} r="3.4" />
      </g>
    </svg>
  );
}
