import type { PtyPerfSnapshot } from './ptyPerf';
import type { TerminalPerfSnapshot } from './terminalPerf';
import type { TerminalRuntimeLogEvent } from './terminalRuntimeLog';
import { isSuspiciousTerminalSize } from './terminalDebug';

export interface RuntimeTimelineMilestones {
  attachRequestedAt: string | null;
  attachResultAt: string | null;
  replayAppliedAt: string | null;
  firstLiveOutputAt: string | null;
  firstInputAt: string | null;
  firstFocusAt: string | null;
  terminalMountedAt: string | null;
  terminalReadyAt: string | null;
  firstRenderAt: string | null;
  firstWriteParsedAt: string | null;
  runtimeEnsuredAt: string | null;
  lastResizeAt: string | null;
  lastRedrawAt: string | null;
}

export interface RuntimeTimelineLatencies {
  attachToReplayMs: number | null;
  attachToFirstLiveOutputMs: number | null;
  inputToFirstLiveOutputMs: number | null;
  focusToFirstInputMs: number | null;
  readyToFirstLiveOutputMs: number | null;
}

export interface RuntimeTimelineSummary {
  runtimeId: string;
  sessionId: string | null;
  paneId: string | null;
  paneKind: 'main' | 'shell' | null;
  milestones: RuntimeTimelineMilestones;
  latencies: RuntimeTimelineLatencies;
  flags: {
    replayBeforeFirstLiveOutput: boolean;
    inputBeforeFirstLiveOutput: boolean;
    hasAttachReplay: boolean;
    terminalReady: boolean;
    terminalVisible: boolean | null;
  };
  counters: {
    eventCount: number;
    liveOutputCount: number;
    inputCount: number;
    resizeCount: number;
    redrawCount: number;
  };
  transport: {
    replayKind: string | null;
    lastSeq: number | null;
    terminalWriteBytes: number;
    ptyInputBytes: number;
    ptyOutputBase64Chars: number;
  };
  geometry: {
    recentPtyResizes: Array<{
      at: string;
      cols: number | null;
      rows: number | null;
      reason: string | null;
    }>;
    ptyResizeBursts: Array<{
      startedAt: string;
      endedAt: string;
      durationMs: number;
      count: number;
      minCols: number | null;
      maxCols: number | null;
      minRows: number | null;
      maxRows: number | null;
      finalCols: number | null;
      finalRows: number | null;
      suspiciousCount: number;
      reasons: string[];
    }>;
  };
  terminal: Pick<
    TerminalPerfSnapshot,
    | 'terminalName'
    | 'renderer'
    | 'ready'
    | 'visible'
    | 'cols'
    | 'rows'
    | 'writeQueueChunks'
    | 'writeQueueBytes'
    | 'renderCount'
    | 'writeParsedCount'
    | 'lastRenderAt'
    | 'lastWriteParsedAt'
    | 'lastResize'
    | 'dom'
  > | null;
}

export interface RuntimeTimelineSnapshot {
  capturedAt: string;
  runtimeCount: number;
  runtimes: RuntimeTimelineSummary[];
  recentEvents: TerminalRuntimeLogEvent[];
}

interface RuntimeTimelineBuilderInput {
  events: TerminalRuntimeLogEvent[];
  terminals: TerminalPerfSnapshot[];
  pty: PtyPerfSnapshot;
  runtimeIds?: Set<string> | null;
}

interface MutableRuntimeTimelineSummary extends RuntimeTimelineSummary {
  _eventIndex: number;
}

const PTY_RESIZE_BURST_GAP_MS = 180;
const MAX_RECENT_PTY_RESIZES = 12;
const MAX_PTY_RESIZE_BURSTS = 4;

function filterRuntimeTimelineEvents(
  events: TerminalRuntimeLogEvent[],
  runtimeIds: Set<string> | null,
) {
  const filtered: TerminalRuntimeLogEvent[] = [];
  let previousAt = '';
  let outOfOrder = false;

  for (const event of events) {
    if (runtimeIds && runtimeIds.size > 0) {
      if (typeof event.runtimeId !== 'string' || !runtimeIds.has(event.runtimeId)) {
        continue;
      }
    }
    filtered.push(event);
    if (previousAt && event.at.localeCompare(previousAt) < 0) {
      outOfOrder = true;
    }
    previousAt = event.at;
  }

  if (outOfOrder) {
    filtered.sort((left, right) => left.at.localeCompare(right.at));
  }

  return filtered;
}

function asMs(earlier: string | null, later: string | null): number | null {
  if (!earlier || !later) {
    return null;
  }
  const earlierMs = Date.parse(earlier);
  const laterMs = Date.parse(later);
  if (!Number.isFinite(earlierMs) || !Number.isFinite(laterMs)) {
    return null;
  }
  return Math.max(0, laterMs - earlierMs);
}

function ensureSummary(
  summaries: Map<string, MutableRuntimeTimelineSummary>,
  runtimeId: string,
): MutableRuntimeTimelineSummary {
  const existing = summaries.get(runtimeId);
  if (existing) {
    return existing;
  }

  const created: MutableRuntimeTimelineSummary = {
    runtimeId,
    sessionId: null,
    paneId: null,
    paneKind: null,
    milestones: {
      attachRequestedAt: null,
      attachResultAt: null,
      replayAppliedAt: null,
      firstLiveOutputAt: null,
      firstInputAt: null,
      firstFocusAt: null,
      terminalMountedAt: null,
      terminalReadyAt: null,
      firstRenderAt: null,
      firstWriteParsedAt: null,
      runtimeEnsuredAt: null,
      lastResizeAt: null,
      lastRedrawAt: null,
    },
    latencies: {
      attachToReplayMs: null,
      attachToFirstLiveOutputMs: null,
      inputToFirstLiveOutputMs: null,
      focusToFirstInputMs: null,
      readyToFirstLiveOutputMs: null,
    },
    flags: {
      replayBeforeFirstLiveOutput: false,
      inputBeforeFirstLiveOutput: false,
      hasAttachReplay: false,
      terminalReady: false,
      terminalVisible: null,
    },
    counters: {
      eventCount: 0,
      liveOutputCount: 0,
      inputCount: 0,
      resizeCount: 0,
      redrawCount: 0,
    },
    transport: {
      replayKind: null,
      lastSeq: null,
      terminalWriteBytes: 0,
      ptyInputBytes: 0,
      ptyOutputBase64Chars: 0,
    },
    geometry: {
      recentPtyResizes: [],
      ptyResizeBursts: [],
    },
    terminal: null,
    _eventIndex: 0,
  };
  summaries.set(runtimeId, created);
  return created;
}

function maybeUpdateContext(summary: MutableRuntimeTimelineSummary, event: TerminalRuntimeLogEvent) {
  if (typeof event.sessionId === 'string' && event.sessionId.length > 0) {
    summary.sessionId = event.sessionId;
  }
  if (typeof event.paneId === 'string' && event.paneId.length > 0) {
    summary.paneId = event.paneId;
    summary.paneKind = event.paneId === 'main' ? 'main' : 'shell';
  }
}

function updateMilestone(current: string | null, next: string | undefined, mode: 'first' | 'last' = 'first') {
  if (!next) {
    return current;
  }
  if (!current) {
    return next;
  }
  if (mode === 'last' && Date.parse(next) >= Date.parse(current)) {
    return next;
  }
  return current;
}

export function buildRuntimeTimelineSnapshot({
  events,
  terminals,
  pty,
  runtimeIds = null,
}: RuntimeTimelineBuilderInput): RuntimeTimelineSnapshot {
  const filteredEvents = filterRuntimeTimelineEvents(events, runtimeIds);

  const summaries = new Map<string, MutableRuntimeTimelineSummary>();

  for (const event of filteredEvents) {
    const runtimeId = typeof event.runtimeId === 'string' ? event.runtimeId : '';
    if (!runtimeId) {
      continue;
    }
    const summary = ensureSummary(summaries, runtimeId);
    maybeUpdateContext(summary, event);
    summary.counters.eventCount += 1;
    summary._eventIndex += 1;

    switch (event.event) {
      case 'pty.attach.requested':
        summary.milestones.attachRequestedAt = updateMilestone(summary.milestones.attachRequestedAt, event.at, 'last');
        break;
      case 'pty.attach.result':
        summary.milestones.attachResultAt = updateMilestone(summary.milestones.attachResultAt, event.at, 'last');
        if (typeof event.details?.lastSeq === 'number') {
          summary.transport.lastSeq = event.details.lastSeq;
        }
        if (typeof event.details?.replayKind === 'string' && event.details.replayKind.length > 0) {
          summary.transport.replayKind = event.details.replayKind;
        }
        break;
      case 'pty.attach.replay_applied':
        summary.milestones.replayAppliedAt = updateMilestone(summary.milestones.replayAppliedAt, event.at, 'first');
        summary.flags.hasAttachReplay = true;
        if (typeof event.details?.replayKind === 'string' && event.details.replayKind.length > 0) {
          summary.transport.replayKind = event.details.replayKind;
        }
        break;
      case 'pty.output.live':
        summary.milestones.firstLiveOutputAt = updateMilestone(summary.milestones.firstLiveOutputAt, event.at, 'first');
        summary.counters.liveOutputCount += 1;
        if (typeof event.details?.seq === 'number') {
          summary.transport.lastSeq = event.details.seq;
        }
        break;
      case 'pty.input.sent':
        summary.milestones.firstInputAt = updateMilestone(summary.milestones.firstInputAt, event.at, 'first');
        summary.counters.inputCount += 1;
        if (typeof event.details?.bytes === 'number') {
          summary.transport.ptyInputBytes += Math.max(0, event.details.bytes);
        }
        break;
      case 'pty.resize.sent':
        summary.milestones.lastResizeAt = updateMilestone(summary.milestones.lastResizeAt, event.at, 'last');
        summary.counters.resizeCount += 1;
        summary.geometry.recentPtyResizes.push({
          at: event.at,
          cols: typeof event.details?.cols === 'number' ? event.details.cols : null,
          rows: typeof event.details?.rows === 'number' ? event.details.rows : null,
          reason: typeof event.details?.reason === 'string' ? event.details.reason : null,
        });
        if (summary.geometry.recentPtyResizes.length > MAX_RECENT_PTY_RESIZES) {
          summary.geometry.recentPtyResizes.splice(
            0,
            summary.geometry.recentPtyResizes.length - MAX_RECENT_PTY_RESIZES,
          );
        }
        break;
      case 'pty.redraw.requested':
        summary.milestones.lastRedrawAt = updateMilestone(summary.milestones.lastRedrawAt, event.at, 'last');
        summary.counters.redrawCount += 1;
        break;
      case 'terminal.mounted':
        summary.milestones.terminalMountedAt = updateMilestone(summary.milestones.terminalMountedAt, event.at, 'first');
        break;
      case 'terminal.ready':
        summary.milestones.terminalReadyAt = updateMilestone(summary.milestones.terminalReadyAt, event.at, 'first');
        summary.flags.terminalReady = true;
        break;
      case 'terminal.first_render':
        summary.milestones.firstRenderAt = updateMilestone(summary.milestones.firstRenderAt, event.at, 'first');
        break;
      case 'terminal.first_write_parsed':
        summary.milestones.firstWriteParsedAt = updateMilestone(summary.milestones.firstWriteParsedAt, event.at, 'first');
        break;
      case 'focus.acquired':
        summary.milestones.firstFocusAt = updateMilestone(summary.milestones.firstFocusAt, event.at, 'first');
        break;
      case 'runtime.ensured':
        summary.milestones.runtimeEnsuredAt = updateMilestone(summary.milestones.runtimeEnsuredAt, event.at, 'first');
        break;
      default:
        break;
    }
  }

  for (const terminal of terminals) {
    const runtimeId = terminal.runtimeId || '';
    if (!runtimeId) {
      continue;
    }
    if (runtimeIds && runtimeIds.size > 0 && !runtimeIds.has(runtimeId)) {
      continue;
    }
    const summary = ensureSummary(summaries, runtimeId);
    summary.sessionId = terminal.sessionId || summary.sessionId;
    summary.paneId = terminal.paneId || summary.paneId;
    summary.paneKind = terminal.paneKind || summary.paneKind;
    summary.flags.terminalReady = terminal.ready;
    summary.flags.terminalVisible = terminal.visible;
    summary.terminal = {
      terminalName: terminal.terminalName,
      renderer: terminal.renderer,
      ready: terminal.ready,
      visible: terminal.visible,
      cols: terminal.cols,
      rows: terminal.rows,
      writeQueueChunks: terminal.writeQueueChunks,
      writeQueueBytes: terminal.writeQueueBytes,
      renderCount: terminal.renderCount,
      writeParsedCount: terminal.writeParsedCount,
      lastRenderAt: terminal.lastRenderAt,
      lastWriteParsedAt: terminal.lastWriteParsedAt,
      lastResize: terminal.lastResize,
      dom: terminal.dom,
    };
  }

  const relevantPtyEvents = runtimeIds && runtimeIds.size > 0
    ? pty.recentEvents.filter((event) => typeof event.runtimeId === 'string' && runtimeIds.has(event.runtimeId))
    : pty.recentEvents;
  for (const event of relevantPtyEvents) {
    if (!event.runtimeId) {
      continue;
    }
    const summary = ensureSummary(summaries, event.runtimeId);
    if (event.kind === 'ws_event' && event.event === 'pty_output') {
      summary.transport.ptyOutputBase64Chars += Math.max(0, event.base64Chars);
      if (typeof event.seq === 'number') {
        summary.transport.lastSeq = event.seq;
      }
    }
  }

  const runtimes = Array.from(summaries.values())
    .map((summary) => {
      const bursts: RuntimeTimelineSummary['geometry']['ptyResizeBursts'] = [];
      let currentBurst: RuntimeTimelineSummary['geometry']['ptyResizeBursts'][number] | null = null;
      for (const resize of summary.geometry.recentPtyResizes) {
        if (!currentBurst) {
          currentBurst = {
            startedAt: resize.at,
            endedAt: resize.at,
            durationMs: 0,
            count: 1,
            minCols: resize.cols,
            maxCols: resize.cols,
            minRows: resize.rows,
            maxRows: resize.rows,
            finalCols: resize.cols,
            finalRows: resize.rows,
            suspiciousCount:
              resize.cols !== null && resize.rows !== null && isSuspiciousTerminalSize(resize.cols, resize.rows)
                ? 1
                : 0,
            reasons: resize.reason ? [resize.reason] : [],
          };
          continue;
        }

        const gapMs = Date.parse(resize.at) - Date.parse(currentBurst.endedAt);
        if (!Number.isFinite(gapMs) || gapMs > PTY_RESIZE_BURST_GAP_MS) {
          bursts.push(currentBurst);
          currentBurst = {
            startedAt: resize.at,
            endedAt: resize.at,
            durationMs: 0,
            count: 1,
            minCols: resize.cols,
            maxCols: resize.cols,
            minRows: resize.rows,
            maxRows: resize.rows,
            finalCols: resize.cols,
            finalRows: resize.rows,
            suspiciousCount:
              resize.cols !== null && resize.rows !== null && isSuspiciousTerminalSize(resize.cols, resize.rows)
                ? 1
                : 0,
            reasons: resize.reason ? [resize.reason] : [],
          };
          continue;
        }

        currentBurst.endedAt = resize.at;
        currentBurst.durationMs = Math.max(0, Date.parse(currentBurst.endedAt) - Date.parse(currentBurst.startedAt));
        currentBurst.count += 1;
        currentBurst.finalCols = resize.cols;
        currentBurst.finalRows = resize.rows;
        if (resize.cols !== null) {
          currentBurst.minCols = currentBurst.minCols === null ? resize.cols : Math.min(currentBurst.minCols, resize.cols);
          currentBurst.maxCols = currentBurst.maxCols === null ? resize.cols : Math.max(currentBurst.maxCols, resize.cols);
        }
        if (resize.rows !== null) {
          currentBurst.minRows = currentBurst.minRows === null ? resize.rows : Math.min(currentBurst.minRows, resize.rows);
          currentBurst.maxRows = currentBurst.maxRows === null ? resize.rows : Math.max(currentBurst.maxRows, resize.rows);
        }
        if (resize.cols !== null && resize.rows !== null && isSuspiciousTerminalSize(resize.cols, resize.rows)) {
          currentBurst.suspiciousCount += 1;
        }
        if (resize.reason && !currentBurst.reasons.includes(resize.reason)) {
          currentBurst.reasons.push(resize.reason);
        }
      }
      if (currentBurst) {
        bursts.push(currentBurst);
      }
      summary.geometry.ptyResizeBursts = bursts.slice(-MAX_PTY_RESIZE_BURSTS);
      summary.latencies.attachToReplayMs = asMs(summary.milestones.attachResultAt, summary.milestones.replayAppliedAt);
      summary.latencies.attachToFirstLiveOutputMs = asMs(summary.milestones.attachResultAt, summary.milestones.firstLiveOutputAt);
      summary.latencies.inputToFirstLiveOutputMs = asMs(summary.milestones.firstInputAt, summary.milestones.firstLiveOutputAt);
      summary.latencies.focusToFirstInputMs = asMs(summary.milestones.firstFocusAt, summary.milestones.firstInputAt);
      summary.latencies.readyToFirstLiveOutputMs = asMs(summary.milestones.terminalReadyAt, summary.milestones.firstLiveOutputAt);
      summary.flags.replayBeforeFirstLiveOutput = Boolean(
        summary.milestones.replayAppliedAt &&
        (
          !summary.milestones.firstLiveOutputAt ||
          Date.parse(summary.milestones.replayAppliedAt) < Date.parse(summary.milestones.firstLiveOutputAt)
        )
      );
      summary.flags.inputBeforeFirstLiveOutput = Boolean(
        summary.milestones.firstInputAt &&
        (
          !summary.milestones.firstLiveOutputAt ||
          Date.parse(summary.milestones.firstInputAt) < Date.parse(summary.milestones.firstLiveOutputAt)
        )
      );
      summary.transport.terminalWriteBytes = summary.terminal?.writeQueueBytes || 0;
      const { _eventIndex, ...publicSummary } = summary;
      return publicSummary;
    })
    .sort((left, right) => {
      const leftAt = left.milestones.firstLiveOutputAt
        || left.milestones.attachResultAt
        || left.milestones.attachRequestedAt
        || '';
      const rightAt = right.milestones.firstLiveOutputAt
        || right.milestones.attachResultAt
        || right.milestones.attachRequestedAt
        || '';
      return rightAt.localeCompare(leftAt);
    });

  return {
    capturedAt: new Date().toISOString(),
    runtimeCount: runtimes.length,
    runtimes,
    recentEvents: filteredEvents.slice(-160),
  };
}
