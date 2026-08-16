// app/src/components/EventBusSettings.tsx
//
// The durable event bus, surfaced in Settings › Event Bus: what the log holds,
// which fact classes are writing to it and how fast, which consumers are reading
// it and how far behind they are, and what is wrong with any of it.
//
// This exists because a producer bug once wrote two thirds of the log for a week
// and was found by accident. Nothing in attn showed what the bus was doing.
//
// Every number here is computed by the daemon — the same bus.Status that
// `attn bus status` renders — so the two surfaces cannot disagree. In
// particular the health list arrives as finished sentences: this component
// renders them, it does not decide what counts as unhealthy.
import { useCallback, useEffect, useRef, useState } from 'react';
import type { BusConsumerStatus, BusStatus } from '../hooks/daemonBusEvents';
import './EventBusSettings.css';

interface EventBusSettingsProps {
  getBusStatus: () => Promise<BusStatus>;
  setConsumerEnabled: (consumer: string, enabled: boolean) => Promise<{ consumer: string }>;
}

// How often the page re-reads while it is open. The daemon answers with one
// aggregate pass over the whole log — 209ms at 945k rows, measured — so this is
// deliberately slow: attn runs all day, and a diagnostics page must not cost
// battery to leave open. The component only mounts while its section is
// selected, so closing Settings or navigating away stops it.
const REFRESH_MS = 30_000;

// Producers below this share are the long tail of a healthy log (35 of 50
// classes in production hold 0.3% between them). They are collapsed rather than
// dropped — the count and their combined share are always stated, and one click
// shows them.
const QUIET_SHARE = 0.005;

const formatBytes = (n: number): string => {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
};

const formatCount = (n: number): string => n.toLocaleString();

const formatRate = (n: number): string => (n >= 10 ? n.toFixed(0) : n.toFixed(1));

/** Renders a span the way someone says it: "8d", "3h", "12m", "40s". */
const formatSpan = (seconds: number): string => {
  if (!Number.isFinite(seconds) || seconds <= 0) return '';
  if (seconds >= 48 * 3600) return `${Math.round(seconds / 86400)}d`;
  if (seconds >= 3600) return `${Math.round(seconds / 3600)}h`;
  if (seconds >= 60) return `${Math.round(seconds / 60)}m`;
  return `${Math.round(seconds)}s`;
};

/**
 * Renders a configured limit exactly, where formatSpan renders an observation:
 * "1h", "1m30s", "45s". A limit rounded to "2m" when it was set to 1m30s names a
 * number the reader cannot check their own value against. Mirrors limitDuration
 * in internal/bus.
 */
const formatLimit = (seconds: number): string => {
  if (!Number.isFinite(seconds) || seconds <= 0) return '';
  const whole = Math.round(seconds);
  const h = Math.floor(whole / 3600);
  const m = Math.floor((whole % 3600) / 60);
  const s = whole % 60;
  if (h > 0) return s === 0 ? (m === 0 ? `${h}h` : `${h}h${m}m`) : `${h}h${m}m${s}s`;
  if (m > 0) return s === 0 ? `${m}m` : `${m}m${s}s`;
  return `${s}s`;
};

/** Age of an RFC3339 stamp, as a span. Empty for an absent or unparseable one. */
const formatAge = (iso: string, now: number): string => {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  return formatSpan((now - t) / 1000);
};

export function EventBusSettings({ getBusStatus, setConsumerEnabled }: EventBusSettingsProps) {
  const [status, setStatus] = useState<BusStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [showQuiet, setShowQuiet] = useState(false);
  const [pendingConsumer, setPendingConsumer] = useState<string | null>(null);
  // Guards against an older answer landing after a newer one.
  const seqRef = useRef(0);

  const refresh = useCallback(async () => {
    const seq = ++seqRef.current;
    setLoading(true);
    try {
      const next = await getBusStatus();
      if (seqRef.current !== seq) return;
      setStatus(next);
      setError(null);
    } catch (err) {
      if (seqRef.current !== seq) return;
      setError(err instanceof Error ? err.message : 'Could not read the event bus');
    } finally {
      if (seqRef.current === seq) setLoading(false);
    }
  }, [getBusStatus]);

  useEffect(() => {
    void refresh();
    const id = window.setInterval(() => { void refresh(); }, REFRESH_MS);
    return () => window.clearInterval(id);
  }, [refresh]);

  const toggleConsumer = useCallback(async (consumer: BusConsumerStatus) => {
    setPendingConsumer(consumer.name);
    try {
      await setConsumerEnabled(consumer.name, !consumer.enabled);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not change the consumer');
    } finally {
      setPendingConsumer(null);
    }
  }, [setConsumerEnabled, refresh]);

  if (error && !status) {
    return (
      <section className="settings-block">
        {renderIntro()}
        <div className="settings-block-body">
          <div className="bus-state">
            <span className="settings-warning">{error}</span>
            <button type="button" className="settings-action" onClick={() => void refresh()}>
              Try again
            </button>
          </div>
        </div>
      </section>
    );
  }

  if (!status) {
    return (
      <section className="settings-block">
        {renderIntro()}
        <div className="settings-block-body">
          <div className="bus-state" data-testid="bus-loading">Reading the event log…</div>
        </div>
      </section>
    );
  }

  const now = Date.now();
  const loud = status.producers.filter((p) => p.share >= QUIET_SHARE);
  const quiet = status.producers.filter((p) => p.share < QUIET_SHARE);
  const quietEvents = quiet.reduce((sum, p) => sum + p.events, 0);
  const quietShare = quiet.reduce((sum, p) => sum + p.share, 0);
  const shownProducers = showQuiet ? status.producers : loud;

  return (
    <section className="settings-block" data-testid="settings-bus">
      {renderIntro()}
      <div className="settings-block-body">
        {error && <span className="settings-warning">{error}</span>}

        {status.health.length > 0 && (
          <ul className="bus-health" data-testid="bus-health">
            {/* kind+subject identifies a finding: health() emits at most one of
                each kind per consumer, producer, or floor. */}
            {status.health.map((h) => (
              <li key={`${h.kind}:${h.subject}`} className={`bus-health-entry ${h.level}`}>
                <span className={`settings-pill ${h.level === 'error' ? 'bad' : 'warn'}`}>
                  {h.level === 'error' ? 'Error' : 'Warning'}
                </span>
                <span className="bus-health-message">{h.message}</span>
              </li>
            ))}
          </ul>
        )}

        <div className="bus-summary" data-testid="bus-summary">
          <div className="bus-stat">
            <span className="bus-stat-label">Events</span>
            <span className="bus-stat-value">{formatCount(status.rows)}</span>
          </div>
          <div className="bus-stat">
            <span className="bus-stat-label">Weight</span>
            <span className="bus-stat-value">{formatBytes(status.bytes)}</span>
          </div>
          <div className="bus-stat">
            <span className="bus-stat-label">Seq</span>
            <span className="bus-stat-value">{status.earliest}–{status.head}</span>
          </div>
          <div className="bus-stat">
            <span className="bus-stat-label">Oldest</span>
            <span className="bus-stat-value">{formatAge(status.oldestAt, now) || '—'}</span>
          </div>
          <div className="bus-stat">
            <span className="bus-stat-label">Retention</span>
            <span className="bus-stat-value">{formatSpan(status.retentionSeconds)}</span>
          </div>
        </div>

        <div className="bus-section-head">
          <h4>Producers</h4>
          <span className="settings-hint">
            Rates are events per hour over the last {formatSpan(status.recentWindowSeconds)} and{' '}
            {formatSpan(status.baselineWindowSeconds)}. Far fewer subjects than events means a
            producer republishing the same entity rather than describing change.
          </span>
        </div>
        {shownProducers.length === 0 ? (
          <p className="settings-empty">Nothing has been published yet.</p>
        ) : (
          <div className="bus-table bus-producers" data-testid="bus-producers">
            <div className="bus-row bus-head">
              <span>Fact</span>
              <span className="bus-num">Events</span>
              <span className="bus-num">Share</span>
              <span className="bus-num">Subjects</span>
              <span className="bus-num">{formatSpan(status.recentWindowSeconds)}/h</span>
              <span className="bus-num">{formatSpan(status.baselineWindowSeconds)}/h</span>
            </div>
            {shownProducers.map((p) => (
              <div
                key={p.name}
                className={`bus-row${p.surging ? ' surging' : ''}`}
                data-testid={`bus-producer-${p.name}`}
              >
                <span className="bus-name">
                  {p.name}
                  {p.surging && <span className="settings-pill warn">Loud</span>}
                </span>
                <span className="bus-num">{formatCount(p.events)}</span>
                <span className="bus-num">{(p.share * 100).toFixed(1)}%</span>
                <span className="bus-num">{formatCount(p.subjects)}</span>
                <span className="bus-num">{formatRate(p.recent_per_hour)}</span>
                <span className="bus-num">{formatRate(p.baseline_per_hour)}</span>
              </div>
            ))}
          </div>
        )}
        {quiet.length > 0 && (
          <button
            type="button"
            className="bus-more"
            data-testid="bus-toggle-quiet"
            onClick={() => setShowQuiet((v) => !v)}
          >
            {showQuiet
              ? 'Hide the quieter classes'
              : `Show ${quiet.length} quieter class${quiet.length === 1 ? '' : 'es'} — ${formatCount(quietEvents)} events, ${(quietShare * 100).toFixed(1)}% of the log`}
          </button>
        )}

        <div className="bus-section-head">
          <h4>Consumers</h4>
          <span className="settings-hint">
            Disabling stops delivery. Ordinary consumers release retention; installed apps keep
            their cursor and unread backlog until they are enabled or uninstalled.
          </span>
        </div>
        {status.consumers.length === 0 ? (
          <p className="settings-empty" data-testid="bus-no-consumers">
            No durable consumers are registered. Every subscriber on this bus is ephemeral, so
            nothing holds a cursor and nothing pins retention.
          </p>
        ) : (
          <div className="bus-table bus-consumers" data-testid="bus-consumers">
            <div className="bus-row bus-head">
              <span>Consumer</span>
              <span className="bus-num">Cursor</span>
              <span className="bus-num">Lag</span>
              <span className="bus-num">Waiting</span>
              <span />
            </div>
            {status.consumers.map((c) => (
              <div
                key={c.name}
                className={`bus-row${c.enabled ? '' : ' disabled'}`}
                data-testid={`bus-consumer-${c.name}`}
              >
                <span className="bus-name">
                  {c.name}
                  {/* Holding the floor is the system working. Holding it past the
                      tripwire is the thing to act on, so the two never look alike. */}
                  {c.holds_retention_floor && !c.pin_alarm && (
                    <span className="settings-pill" title="Retention stops at this consumer's cursor">
                      Retention floor
                    </span>
                  )}
                  {c.pin_alarm && (
                    <span
                      className="settings-pill bad"
                      title={`Nothing below this consumer's cursor can be trimmed, and it has held that for longer than ${formatLimit(status.pinAlarmSeconds)}`}
                    >
                      Pinning {formatBytes(c.pinned_bytes)}
                    </span>
                  )}
                  {!c.enabled && <span className="settings-pill warn">Disabled</span>}
                  {c.stalled && <span className="settings-pill bad">Stalled</span>}
                  {status.delivering && c.enabled && !c.live && (
                    <span className="settings-pill bad">Not running</span>
                  )}
                  <span className="bus-filter">{c.filter || 'all facts'}</span>
                </span>
                <span className="bus-num">{formatCount(c.cursor)}</span>
                <span className="bus-num">{formatCount(c.lag)}</span>
                <span className="bus-num">{formatAge(c.oldest_unread_at, now) || '—'}</span>
                <span className="bus-row-action">
                  <button
                    type="button"
                    className={`settings-action${c.enabled ? ' danger' : ''}`}
                    data-testid={`bus-consumer-toggle-${c.name}`}
                    disabled={pendingConsumer !== null}
                    onClick={() => void toggleConsumer(c)}
                  >
                    {c.enabled ? 'Disable' : 'Enable'}
                  </button>
                </span>
              </div>
            ))}
          </div>
        )}

        <div className="bus-footer">
          <button
            type="button"
            className="settings-action"
            data-testid="bus-refresh"
            disabled={loading}
            onClick={() => void refresh()}
          >
            {loading ? 'Reading…' : 'Refresh'}
          </button>
          {/* This page always reads the daemon, so `delivering: false` here can
              only mean the daemon runs no durable delivery loops — never the
              CLI's other meaning, that the snapshot came from the database. */}
          <span className="settings-hint">
            {status.delivering
              ? 'Read from the daemon that owns delivery, so a consumer that is registered but not running is visible.'
              : 'Read from the daemon, which is running no durable delivery loops right now, so there is nothing to report as running or stalled.'}
          </span>
        </div>
      </div>
    </section>
  );
}

function renderIntro() {
  return (
    <div className="settings-block-intro">
      <span className="settings-kicker">Event Bus</span>
      <h3>Durable event log</h3>
      <p className="settings-description">
        The spine every state change travels on. This is what it holds, who writes to it, and who
        reads it — the same picture <code>attn bus status</code> prints.
      </p>
    </div>
  );
}
