import { beforeEach, describe, expect, it, vi } from 'vitest';

const { recordTerminalIncident } = vi.hoisted(() => ({
  recordTerminalIncident: vi.fn(),
}));

vi.mock('./terminalDiagnosticsLog', () => ({ recordTerminalIncident }));

import {
  completeTerminalInputProbe,
  maybeStartTerminalInputProbe,
  noteTerminalKeyEvent,
  resetTerminalInputLatencyForTests,
  terminalEventQueueDelayMs,
} from './terminalInputLatency';

describe('terminal input latency diagnostics', () => {
  beforeEach(() => {
    resetTerminalInputLatencyForTests();
    recordTerminalIncident.mockClear();
  });

  it('normalizes both monotonic and epoch DOM event timestamps', () => {
    expect(terminalEventQueueDelayMs(1_000, 1_275, performance.timeOrigin)).toBe(275);
    expect(terminalEventQueueDelayMs(performance.timeOrigin + 1_000, 1_275, performance.timeOrigin)).toBe(275);
    expect(terminalEventQueueDelayMs(-10_000_000, 1_275, performance.timeOrigin)).toBeNull();
  });

  it('keeps healthy sampled round trips in memory without writing an incident', () => {
    const probeId = maybeStartTerminalInputProbe('runtime-1', 'user', 1_000, 10_000);
    expect(probeId).toBeTruthy();
    expect(maybeStartTerminalInputProbe('runtime-1', 'user', 1_100, 10_100)).toBeUndefined();
    expect(maybeStartTerminalInputProbe('runtime-2', 'automation', 1_100, 10_100)).toBeUndefined();

    completeTerminalInputProbe({
      id: 'runtime-1',
      probe_id: probeId!,
      success: true,
      write_duration_us: 4_000,
    }, 1_080, 10_080);

    expect(recordTerminalIncident).not.toHaveBeenCalled();
    expect(window.__ATTN_TERMINAL_INPUT_LATENCY_DUMP?.()).toContainEqual({
      at: 10_080,
      stage: 'pty_round_trip',
      runtimeId: 'runtime-1',
      latencyMs: 80,
      daemonWriteMs: 4,
      success: true,
    });
  });

  it('records a slow round trip with the daemon share of the delay', () => {
    const probeId = maybeStartTerminalInputProbe('runtime-slow', 'user', 2_000, 20_000)!;
    completeTerminalInputProbe({
      id: 'runtime-slow',
      probe_id: probeId,
      success: true,
      write_duration_us: 175_000,
    }, 2_400, 20_400);

    expect(recordTerminalIncident).toHaveBeenCalledWith(
      'runtime-slow',
      undefined,
      'pty_input_round_trip_delay',
      expect.objectContaining({
        runtimeId: 'runtime-slow',
        latencyMs: 400,
        daemonWriteMs: 175,
        transportAndUiMs: 225,
      }),
      20_400,
    );
  });

  it('records one key-queue incident per cooldown and reports suppressed repeats', () => {
    const context = { runtimeId: 'runtime-key', sessionId: 'session-key', paneId: 'pane-key' };
    const epoch = performance.timeOrigin;
    noteTerminalKeyEvent({ timeStamp: epoch + 1_000 } as KeyboardEvent, context, 1_300, 30_000);
    noteTerminalKeyEvent({ timeStamp: epoch + 1_100 } as KeyboardEvent, context, 1_500, 31_000);

    expect(recordTerminalIncident).toHaveBeenCalledTimes(1);

    noteTerminalKeyEvent({ timeStamp: epoch + 2_000 } as KeyboardEvent, context, 2_350, 61_000);
    expect(recordTerminalIncident).toHaveBeenCalledTimes(2);
    expect(recordTerminalIncident).toHaveBeenLastCalledWith(
      'pane-key',
      'session-key',
      'terminal_key_event_delay',
      expect.objectContaining({ suppressed: 1, maxSuppressedLatencyMs: 400 }),
      61_000,
    );
  });
});
