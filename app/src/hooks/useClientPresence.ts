import { useCallback, useEffect, useRef } from 'react';

/**
 * Reports whether the user can see this window, so the daemon can decide
 * whether generating session activity lines is worth anything. Nobody looking
 * means nothing generated at all — the daemon treats a missing report as away,
 * which is why this heartbeats rather than latching a flag.
 *
 * The client reports facts only: visible, showing the dashboard, and how long
 * since the last input anywhere in the app. What those mean — the tiers, the
 * intervals, the idle limit — is the daemon's, so changing the policy never
 * needs an app release.
 */

// How often a healthy client repeats itself. The daemon believes a report for
// 90s, so three heartbeats fit inside that window: a single dropped frame or a
// blocked event loop never expires a window that is genuinely being watched.
export const PRESENCE_HEARTBEAT_MS = 30_000;

// The floor between input-triggered reports. Input fires constantly while
// someone is typing, and every report says the same thing; this keeps a burst
// of keystrokes down to one message while still ending an idle stretch
// promptly, instead of waiting out the heartbeat.
const PRESENCE_INPUT_REPORT_FLOOR_MS = 10_000;

const INPUT_EVENTS = ['pointerdown', 'keydown', 'wheel', 'touchstart'] as const;

export interface ClientPresenceReport {
  visible: boolean;
  dashboardVisible: boolean;
  idleSeconds?: number;
}

export function useClientPresence(
  sendSetClientPresence: (presence: ClientPresenceReport) => void,
  options: { dashboardVisible: boolean; connected: boolean },
) {
  const { dashboardVisible, connected } = options;
  // What `report` reads, kept in refs so the callback stays stable across the
  // long-lived listeners and the heartbeat. Synced in an effect rather than
  // during render: a render can be replayed or discarded, and a write from one
  // that never commits would leak into a report.
  const sendRef = useRef(sendSetClientPresence);
  const dashboardRef = useRef(dashboardVisible);
  useEffect(() => {
    sendRef.current = sendSetClientPresence;
    dashboardRef.current = dashboardVisible;
  }, [sendSetClientPresence, dashboardVisible]);

  const lastInputAtRef = useRef<number | null>(null);
  const lastReportAtRef = useRef(0);

  const report = useCallback(() => {
    const now = Date.now();
    lastReportAtRef.current = now;
    const lastInputAt = lastInputAtRef.current;
    sendRef.current({
      // A hidden window is one nobody is reading, whether it is minimized,
      // behind another app, or on a space the user left.
      visible: typeof document === 'undefined' ? true : document.visibilityState === 'visible',
      dashboardVisible: dashboardRef.current,
      // Omitted, not zeroed, when this window has seen no input at all: zero
      // would claim the user just typed.
      ...(lastInputAt === null ? {} : { idleSeconds: (now - lastInputAt) / 1000 }),
    });
  }, []);

  // Report on every change to what is being reported, and on a heartbeat so a
  // window that goes quiet still proves it is there.
  useEffect(() => {
    if (!connected) return;
    report();
    const timer = window.setInterval(report, PRESENCE_HEARTBEAT_MS);
    return () => window.clearInterval(timer);
  }, [connected, dashboardVisible, report]);

  useEffect(() => {
    if (!connected) return;
    const onVisibility = () => report();
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('focus', onVisibility);
    window.addEventListener('blur', onVisibility);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener('focus', onVisibility);
      window.removeEventListener('blur', onVisibility);
    };
  }, [connected, report]);

  useEffect(() => {
    if (!connected) return;
    const onInput = () => {
      lastInputAtRef.current = Date.now();
      if (Date.now() - lastReportAtRef.current >= PRESENCE_INPUT_REPORT_FLOOR_MS) {
        report();
      }
    };
    for (const name of INPUT_EVENTS) {
      window.addEventListener(name, onInput, { capture: true, passive: true });
    }
    return () => {
      for (const name of INPUT_EVENTS) {
        window.removeEventListener(name, onInput, { capture: true });
      }
    };
  }, [connected, report]);
}
