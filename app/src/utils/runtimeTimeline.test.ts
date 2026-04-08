import { describe, expect, it } from 'vitest';
import { buildRuntimeTimelineSnapshot } from './runtimeTimeline';
import type { PtyPerfSnapshot } from './ptyPerf';
import type { TerminalPerfSnapshot } from './terminalPerf';
import type { TerminalRuntimeLogEvent } from './terminalRuntimeLog';

function makePtySnapshot(): PtyPerfSnapshot {
  return {
    updatedAt: '2026-04-08T07:00:03.000Z',
    lastEventAt: null,
    lastEventName: null,
    lastEventRuntimeId: null,
    lastEventSeq: null,
    wsMessageCount: 0,
    wsMessageBytes: 0,
    wsJsonParseMs: 0,
    ptyOutputCount: 0,
    ptyOutputBase64Chars: 0,
    ptyJsonParseMs: 0,
    lastPtyOutputAt: null,
    lastPtyOutputRuntimeId: null,
    lastPtyOutputSeq: null,
    commandCount: 0,
    lastCommandAt: null,
    lastCommandName: null,
    lastCommandRuntimeId: null,
    ptyInputCount: 0,
    ptyInputBytes: 0,
    lastPtyInputAt: null,
    lastPtyInputRuntimeId: null,
    decodeCount: 0,
    decodedBytes: 0,
    decodeMs: 0,
    terminalWriteCount: 0,
    terminalWriteBytes: 0,
    terminalWriteCallMs: 0,
    listenerErrorCount: 0,
    lastListenerErrorAt: null,
    lastListenerError: null,
    recentEvents: [
      {
        at: '2026-04-08T07:00:02.000Z',
        kind: 'ws_event',
        event: 'pty_output',
        command: null,
        source: null,
        runtimeId: 'runtime-1',
        seq: 6,
        base64Chars: 48,
        dataBytes: 0,
      },
    ],
  };
}

function makeTerminalSnapshot(): TerminalPerfSnapshot {
  return {
    terminalName: 'shell:runtime-1',
    sessionId: 'session-1',
    paneId: 'pane-1',
    runtimeId: 'runtime-1',
    paneKind: 'shell',
    isActivePane: true,
    isActiveSession: true,
    cols: 58,
    rows: 46,
    bufferLength: 32,
    baseY: 0,
    viewportY: 0,
    scrollbackLimit: 5000,
    renderer: 'webgl',
    visible: true,
    writeQueueChunks: 0,
    writeQueueBytes: 0,
    renderCount: 12,
    writeParsedCount: 11,
    lastRenderAt: 100,
    lastWriteParsedAt: 90,
    lastRenderRange: { start: 0, end: 1 },
    ready: true,
    startup: {
      initialContainer: { width: 400, height: 300 },
      initialCols: 58,
      initialRows: 46,
      firstObservedContainer: { width: 400, height: 300 },
      firstReadySource: 'resize_observer',
      firstReadyAt: 100,
      firstReadyCols: 58,
      firstReadyRows: 46,
      fontEffectAppliedBeforeReady: false,
      skippedInitialFontEffect: true,
    },
    lastResize: null,
    dom: {
      container: { width: 400, height: 300 },
      xterm: { width: 400, height: 300 },
      xtermScreen: { width: 400, height: 300 },
      canvas: { width: 400, height: 300 },
    },
  };
}

describe('runtimeTimeline', () => {
  it('summarizes attach replay and delayed live output per runtime', () => {
    const events: TerminalRuntimeLogEvent[] = [
      {
        at: '2026-04-08T07:00:00.000Z',
        category: 'transport',
        event: 'pty.attach.requested',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'attach session requested',
      },
      {
        at: '2026-04-08T07:00:00.020Z',
        category: 'transport',
        event: 'pty.attach.result',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'attach session result',
        details: { replayKind: 'screen_snapshot', lastSeq: 5 },
      },
      {
        at: '2026-04-08T07:00:00.021Z',
        category: 'transport',
        event: 'pty.attach.replay_applied',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'attach replay applied',
        details: { replayKind: 'screen_snapshot' },
      },
      {
        at: '2026-04-08T07:00:00.030Z',
        category: 'terminal',
        event: 'terminal.mounted',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'xterm mounted',
      },
      {
        at: '2026-04-08T07:00:00.040Z',
        category: 'terminal',
        event: 'terminal.ready',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'terminal ready',
      },
      {
        at: '2026-04-08T07:00:00.050Z',
        category: 'focus',
        event: 'focus.acquired',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'focus acquired',
      },
      {
        at: '2026-04-08T07:00:00.100Z',
        category: 'input',
        event: 'pty.input.sent',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'pty input sent',
        details: { bytes: 1 },
      },
      {
        at: '2026-04-08T07:00:02.000Z',
        category: 'transport',
        event: 'pty.output.live',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'live pty output',
        details: { seq: 6, bytes: 12 },
      },
    ];

    const snapshot = buildRuntimeTimelineSnapshot({
      events,
      terminals: [makeTerminalSnapshot()],
      pty: makePtySnapshot(),
      runtimeIds: new Set(['runtime-1']),
    });

    expect(snapshot.runtimeCount).toBe(1);
    expect(snapshot.runtimes[0]).toMatchObject({
      runtimeId: 'runtime-1',
      sessionId: 'session-1',
      paneId: 'pane-1',
      paneKind: 'shell',
      flags: {
        replayBeforeFirstLiveOutput: true,
        inputBeforeFirstLiveOutput: true,
        hasAttachReplay: true,
        terminalReady: true,
        terminalVisible: true,
      },
      transport: {
        replayKind: 'screen_snapshot',
        lastSeq: 6,
        ptyOutputBase64Chars: 48,
        ptyInputBytes: 1,
      },
    });
    expect(snapshot.runtimes[0].latencies.attachToReplayMs).toBe(1);
    expect(snapshot.runtimes[0].latencies.attachToFirstLiveOutputMs).toBe(1980);
    expect(snapshot.runtimes[0].latencies.inputToFirstLiveOutputMs).toBe(1900);
    expect(snapshot.runtimes[0].latencies.focusToFirstInputMs).toBe(50);
  });

  it('groups rapid PTY resizes into bursts for geometry debugging', () => {
    const events: TerminalRuntimeLogEvent[] = [
      {
        at: '2026-04-08T07:00:00.000Z',
        category: 'transport',
        event: 'pty.resize.sent',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'pty resize sent',
        details: { cols: 27, rows: 46, reason: 'resize_both' },
      },
      {
        at: '2026-04-08T07:00:00.050Z',
        category: 'transport',
        event: 'pty.resize.sent',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'pty resize sent',
        details: { cols: 30, rows: 46, reason: 'resize_both' },
      },
      {
        at: '2026-04-08T07:00:00.110Z',
        category: 'transport',
        event: 'pty.resize.sent',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'pty resize sent',
        details: { cols: 37, rows: 46, reason: 'resize_both' },
      },
      {
        at: '2026-04-08T07:00:00.500Z',
        category: 'transport',
        event: 'pty.resize.sent',
        sessionId: 'session-1',
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        message: 'pty resize sent',
        details: { cols: 58, rows: 46, reason: 'visibility_flush' },
      },
    ];

    const snapshot = buildRuntimeTimelineSnapshot({
      events,
      terminals: [makeTerminalSnapshot()],
      pty: makePtySnapshot(),
      runtimeIds: new Set(['runtime-1']),
    });

    expect(snapshot.runtimes[0].geometry.recentPtyResizes).toHaveLength(4);
    expect(snapshot.runtimes[0].geometry.ptyResizeBursts).toHaveLength(2);
    expect(snapshot.runtimes[0].geometry.ptyResizeBursts[0]).toMatchObject({
      count: 3,
      durationMs: 110,
      minCols: 27,
      maxCols: 37,
      finalCols: 37,
      finalRows: 46,
      suspiciousCount: 0,
      reasons: ['resize_both'],
    });
    expect(snapshot.runtimes[0].geometry.ptyResizeBursts[1]).toMatchObject({
      count: 1,
      minCols: 58,
      maxCols: 58,
      finalCols: 58,
      reasons: ['visibility_flush'],
    });
  });
});
