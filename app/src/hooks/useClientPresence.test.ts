import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PRESENCE_HEARTBEAT_MS, useClientPresence, type ClientPresenceReport } from './useClientPresence';

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true });
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('useClientPresence', () => {
  let sent: ClientPresenceReport[];
  let send: (presence: ClientPresenceReport) => void;

  beforeEach(() => {
    vi.useFakeTimers();
    sent = [];
    send = (presence) => { sent.push(presence); };
    setVisibility('visible');
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('reports as soon as the socket is usable', () => {
    renderHook(() => useClientPresence(send, { dashboardVisible: true, connected: true }));

    expect(sent).toEqual([{ visible: true, dashboardVisible: true }]);
  });

  // The daemon expires a report it has not heard repeated, so a window that
  // goes quiet has to keep saying it is there or generation stops for a user
  // who is still reading.
  it('keeps heartbeating while nothing changes', () => {
    renderHook(() => useClientPresence(send, { dashboardVisible: true, connected: true }));
    sent.length = 0;

    act(() => { vi.advanceTimersByTime(PRESENCE_HEARTBEAT_MS * 2 + 1); });

    expect(sent).toHaveLength(2);
  });

  it('says nothing while the socket is down', () => {
    renderHook(() => useClientPresence(send, { dashboardVisible: true, connected: false }));
    act(() => { vi.advanceTimersByTime(PRESENCE_HEARTBEAT_MS * 2); });

    expect(sent).toEqual([]);
  });

  it('reports the moment the window is hidden rather than waiting for the heartbeat', () => {
    renderHook(() => useClientPresence(send, { dashboardVisible: true, connected: true }));
    sent.length = 0;

    act(() => { setVisibility('hidden'); });

    expect(sent).toEqual([{ visible: false, dashboardVisible: true }]);
  });

  it('reports leaving the dashboard, which is the difference between the two live tiers', () => {
    const { rerender } = renderHook(
      ({ dashboardVisible }) => useClientPresence(send, { dashboardVisible, connected: true }),
      { initialProps: { dashboardVisible: true } },
    );
    sent.length = 0;

    rerender({ dashboardVisible: false });

    expect(sent).toEqual([{ visible: true, dashboardVisible: false }]);
  });

  // Zero would claim the user just typed. A window that has seen no input has
  // nothing to say about idleness, and the daemon reads the absence that way.
  it('omits idle time until it has seen input', () => {
    renderHook(() => useClientPresence(send, { dashboardVisible: false, connected: true }));
    expect(sent[0]).not.toHaveProperty('idleSeconds');
    sent.length = 0;

    act(() => {
      window.dispatchEvent(new Event('keydown'));
      vi.advanceTimersByTime(PRESENCE_HEARTBEAT_MS + 1);
    });

    expect(sent[sent.length - 1]?.idleSeconds).toBeGreaterThanOrEqual(0);
  });

  // Every keystroke says the same thing, so a burst must not become a burst of
  // messages — but a stretch of silence followed by input has to be reported
  // without waiting out the heartbeat.
  it('collapses a burst of input into a single report', () => {
    renderHook(() => useClientPresence(send, { dashboardVisible: false, connected: true }));
    sent.length = 0;

    act(() => { vi.advanceTimersByTime(11_000); });
    act(() => {
      for (let i = 0; i < 20; i += 1) {
        window.dispatchEvent(new Event('keydown'));
      }
    });

    expect(sent).toHaveLength(1);
  });
});
