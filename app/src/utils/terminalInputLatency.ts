import { getPtyPerfSnapshot } from './ptyPerf';
import { recordTerminalIncident } from './terminalDiagnosticsLog';

export const TERMINAL_INPUT_SLOW_MS = 250;
export const TERMINAL_INPUT_PROBE_INTERVAL_MS = 2_000;
const TERMINAL_INPUT_PROBE_TIMEOUT_MS = 5_000;
const TERMINAL_INPUT_INCIDENT_COOLDOWN_MS = 30_000;
const HEALTHY_KEY_SAMPLE_INTERVAL_MS = 1_000;
const SAMPLE_RING_LIMIT = 64;
const MAX_REASONABLE_EVENT_DELAY_MS = 5 * 60_000;

export interface TerminalInputContext {
  runtimeId: string;
  sessionId?: string;
  paneId?: string;
}

export interface TerminalInputLatencySample {
  at: number;
  stage: 'key_event_queue' | 'pty_round_trip';
  runtimeId: string;
  latencyMs: number;
  daemonWriteMs?: number;
  success?: boolean;
}

interface PendingProbe {
  runtimeId: string;
  startedAt: number;
}

interface IncidentEpisode {
  reportedAt: number;
  suppressed: number;
  maxSuppressedLatencyMs: number;
}

declare global {
  interface Window {
    __ATTN_TERMINAL_INPUT_LATENCY_DUMP?: () => TerminalInputLatencySample[];
  }
}

const samples: TerminalInputLatencySample[] = [];
const runtimeContexts = new Map<string, TerminalInputContext>();
const lastProbeAt = new Map<string, number>();
const pendingProbes = new Map<string, PendingProbe>();
const pendingProbeByRuntime = new Map<string, string>();
const lastHealthyKeySampleAt = new Map<string, number>();
const incidentEpisodes = new Map<string, IncidentEpisode>();
let probeSequence = 0;

function exposeGlobals(): void {
  if (typeof window === 'undefined') return;
  window.__ATTN_TERMINAL_INPUT_LATENCY_DUMP = () => samples.map((sample) => ({ ...sample }));
}

function roundedMs(value: number): number {
  return Math.round(value * 10) / 10;
}

function pushSample(sample: TerminalInputLatencySample): void {
  exposeGlobals();
  samples.push(sample);
  if (samples.length > SAMPLE_RING_LIMIT) {
    samples.splice(0, samples.length - SAMPLE_RING_LIMIT);
  }
}

function ptyContext(runtimeId: string): Record<string, unknown> {
  const snapshot = getPtyPerfSnapshot();
  return {
    updatedAt: snapshot.updatedAt,
    lastEventAt: snapshot.lastEventAt,
    lastEventName: snapshot.lastEventName,
    lastEventRuntimeId: snapshot.lastEventRuntimeId,
    lastCommandAt: snapshot.lastCommandAt,
    ptyInputCount: snapshot.ptyInputCount,
    ptyInputBytes: snapshot.ptyInputBytes,
    lastPtyInputAt: snapshot.lastPtyInputAt,
    ptyOutputCount: snapshot.ptyOutputCount,
    lastPtyOutputAt: snapshot.lastPtyOutputAt,
    listenerErrorCount: snapshot.listenerErrorCount,
    recentRuntimeEvents: snapshot.recentEvents
      .filter((event) => event.runtimeId === runtimeId)
      .slice(-12),
  };
}

function maybeRecordIncident(
  context: TerminalInputContext,
  reason: string,
  latencyMs: number,
  detail: Record<string, unknown>,
  wallNow = Date.now(),
): void {
  const episodeKey = `${context.runtimeId}:${reason}`;
  const episode = incidentEpisodes.get(episodeKey);
  if (episode && wallNow - episode.reportedAt < TERMINAL_INPUT_INCIDENT_COOLDOWN_MS) {
    episode.suppressed += 1;
    episode.maxSuppressedLatencyMs = Math.max(episode.maxSuppressedLatencyMs, latencyMs);
    return;
  }

  const suppressed = episode?.suppressed ?? 0;
  const maxSuppressedLatencyMs = episode?.maxSuppressedLatencyMs ?? 0;
  incidentEpisodes.set(episodeKey, {
    reportedAt: wallNow,
    suppressed: 0,
    maxSuppressedLatencyMs: 0,
  });
  recordTerminalIncident(
    context.paneId ?? context.runtimeId,
    context.sessionId,
    reason,
    {
      runtimeId: context.runtimeId,
      latencyMs: roundedMs(latencyMs),
      ...(suppressed > 0 ? {
        suppressed,
        maxSuppressedLatencyMs: roundedMs(maxSuppressedLatencyMs),
      } : {}),
      ...detail,
      pty: ptyContext(context.runtimeId),
    },
    wallNow,
  );
}

// DOM event timestamps are normally relative to performance.timeOrigin, but
// older WebKit builds may report epoch milliseconds. Normalize both forms and
// reject incompatible clocks rather than manufacturing a giant delay.
export function terminalEventQueueDelayMs(
  eventTimestamp: number,
  monotonicNow: number,
  timeOrigin: number,
): number | null {
  if (![eventTimestamp, monotonicNow, timeOrigin].every(Number.isFinite)) return null;
  const eventAt = eventTimestamp > monotonicNow + MAX_REASONABLE_EVENT_DELAY_MS
    ? eventTimestamp - timeOrigin
    : eventTimestamp;
  const delay = monotonicNow - eventAt;
  if (delay < 0 || delay > MAX_REASONABLE_EVENT_DELAY_MS) return null;
  return delay;
}

export function noteTerminalKeyEvent(
  event: KeyboardEvent,
  context: TerminalInputContext,
  monotonicNow = performance.now(),
  wallNow = Date.now(),
): void {
  runtimeContexts.set(context.runtimeId, context);
  const delay = terminalEventQueueDelayMs(event.timeStamp, monotonicNow, performance.timeOrigin);
  if (delay === null) return;

  const lastHealthyAt = lastHealthyKeySampleAt.get(context.runtimeId) ?? -Infinity;
  if (delay >= TERMINAL_INPUT_SLOW_MS || monotonicNow - lastHealthyAt >= HEALTHY_KEY_SAMPLE_INTERVAL_MS) {
    pushSample({
      at: wallNow,
      stage: 'key_event_queue',
      runtimeId: context.runtimeId,
      latencyMs: roundedMs(delay),
    });
    if (delay < TERMINAL_INPUT_SLOW_MS) {
      lastHealthyKeySampleAt.set(context.runtimeId, monotonicNow);
    }
  }
  if (delay >= TERMINAL_INPUT_SLOW_MS) {
    maybeRecordIncident(context, 'terminal_key_event_delay', delay, {
      thresholdMs: TERMINAL_INPUT_SLOW_MS,
    }, wallNow);
  }
}

function expirePendingProbe(runtimeId: string, monotonicNow: number, wallNow: number): void {
  const probeId = pendingProbeByRuntime.get(runtimeId);
  if (!probeId) return;
  const pending = pendingProbes.get(probeId);
  if (!pending || monotonicNow - pending.startedAt < TERMINAL_INPUT_PROBE_TIMEOUT_MS) return;
  pendingProbes.delete(probeId);
  pendingProbeByRuntime.delete(runtimeId);
  const latencyMs = monotonicNow - pending.startedAt;
  const context = runtimeContexts.get(runtimeId) ?? { runtimeId };
  maybeRecordIncident(context, 'pty_input_probe_timeout', latencyMs, {
    thresholdMs: TERMINAL_INPUT_PROBE_TIMEOUT_MS,
  }, wallNow);
}

export function maybeStartTerminalInputProbe(
  runtimeId: string,
  source: string | undefined,
  monotonicNow = performance.now(),
  wallNow = Date.now(),
): string | undefined {
  if (source !== 'user') return undefined;
  expirePendingProbe(runtimeId, monotonicNow, wallNow);
  if (pendingProbeByRuntime.has(runtimeId)) return undefined;
  const lastAt = lastProbeAt.get(runtimeId);
  if (lastAt !== undefined && monotonicNow - lastAt < TERMINAL_INPUT_PROBE_INTERVAL_MS) return undefined;

  probeSequence += 1;
  const probeId = `${wallNow.toString(36)}-${probeSequence.toString(36)}`;
  lastProbeAt.set(runtimeId, monotonicNow);
  pendingProbes.set(probeId, { runtimeId, startedAt: monotonicNow });
  pendingProbeByRuntime.set(runtimeId, probeId);
  return probeId;
}

export interface TerminalInputProbeResult {
  id: string;
  probe_id: string;
  success: boolean;
  write_duration_us: number;
  error?: string;
}

export function completeTerminalInputProbe(
  result: TerminalInputProbeResult,
  monotonicNow = performance.now(),
  wallNow = Date.now(),
): void {
  const pending = pendingProbes.get(result.probe_id);
  if (!pending) return;
  pendingProbes.delete(result.probe_id);
  pendingProbeByRuntime.delete(pending.runtimeId);

  const latencyMs = Math.max(0, monotonicNow - pending.startedAt);
  const daemonWriteMs = Math.max(0, result.write_duration_us / 1_000);
  pushSample({
    at: wallNow,
    stage: 'pty_round_trip',
    runtimeId: pending.runtimeId,
    latencyMs: roundedMs(latencyMs),
    daemonWriteMs: roundedMs(daemonWriteMs),
    success: result.success,
  });

  const context = runtimeContexts.get(pending.runtimeId) ?? { runtimeId: pending.runtimeId };
  if (!result.success) {
    maybeRecordIncident(context, 'pty_input_probe_failed', latencyMs, {
      daemonWriteMs: roundedMs(daemonWriteMs),
      error: result.error ?? 'PTY input failed',
    }, wallNow);
    return;
  }
  if (latencyMs >= TERMINAL_INPUT_SLOW_MS) {
    maybeRecordIncident(context, 'pty_input_round_trip_delay', latencyMs, {
      thresholdMs: TERMINAL_INPUT_SLOW_MS,
      daemonWriteMs: roundedMs(daemonWriteMs),
      transportAndUiMs: roundedMs(Math.max(0, latencyMs - daemonWriteMs)),
      resultRuntimeId: result.id,
    }, wallNow);
  }
}

export function forgetTerminalInputLatencyRuntime(runtimeId: string): void {
  runtimeContexts.delete(runtimeId);
  lastProbeAt.delete(runtimeId);
  lastHealthyKeySampleAt.delete(runtimeId);

  const probeId = pendingProbeByRuntime.get(runtimeId);
  if (probeId) pendingProbes.delete(probeId);
  pendingProbeByRuntime.delete(runtimeId);

  const episodePrefix = `${runtimeId}:`;
  for (const episodeKey of incidentEpisodes.keys()) {
    if (episodeKey.startsWith(episodePrefix)) incidentEpisodes.delete(episodeKey);
  }
}

export function resetTerminalInputLatencyForTests(): void {
  samples.length = 0;
  runtimeContexts.clear();
  lastProbeAt.clear();
  pendingProbes.clear();
  pendingProbeByRuntime.clear();
  lastHealthyKeySampleAt.clear();
  incidentEpisodes.clear();
  probeSequence = 0;
}
