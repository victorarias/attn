import { useEffect, useState } from 'react';
import { WorkflowRun, WorkflowRunStatus, WorkflowAgentCallStatus, Call } from '../types/generated';
import './WorkflowRunView.css';

interface WorkflowRunViewProps {
  run: WorkflowRun | null;
  onClose?: () => void;
}

const TERMINAL_CALL_STATUSES: ReadonlySet<string> = new Set<WorkflowAgentCallStatus>([
  WorkflowAgentCallStatus.Ok,
  WorkflowAgentCallStatus.Skipped,
  WorkflowAgentCallStatus.Errored,
]);

type Tone = 'idle' | 'running' | 'completed' | 'failed' | 'canceled';

function toneForStatus(status: WorkflowRunStatus | null): Tone {
  switch (status) {
    case WorkflowRunStatus.Running:
      return 'running';
    case WorkflowRunStatus.Completed:
      return 'completed';
    case WorkflowRunStatus.Failed:
      return 'failed';
    case WorkflowRunStatus.Canceled:
      return 'canceled';
    default:
      return 'idle';
  }
}

function statusLabel(status: WorkflowRunStatus | null): string {
  switch (status) {
    case WorkflowRunStatus.Running:
      return 'Running';
    case WorkflowRunStatus.Completed:
      return 'Completed';
    case WorkflowRunStatus.Failed:
      return 'Failed';
    case WorkflowRunStatus.Canceled:
      return 'Canceled';
    default:
      return 'Idle';
  }
}

function formatElapsed(ms: number): string {
  const totalSec = Math.floor(ms / 1000);
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

function basename(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const idx = trimmed.lastIndexOf('/');
  return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}

function titleForRun(run: WorkflowRun): string {
  if (run.script_path) {
    return basename(run.script_path) || run.script_path;
  }
  return 'Workflow Run';
}

export function WorkflowRunView({ run, onClose }: WorkflowRunViewProps) {
  // Self-driven clock so an in-flight call's elapsed time ticks up between
  // daemon broadcasts. Gated strictly on a running run and cleaned up on
  // unmount/status-change so it never leaks a timer past completion. Declared
  // before the null guard to keep hook order stable.
  const runStatus = run?.status ?? null;
  const [now, setNow] = useState<number>(() => Date.now());
  useEffect(() => {
    if (runStatus !== WorkflowRunStatus.Running) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [runStatus]);

  if (run === null) {
    return (
      <div className="workflow-run-view" data-testid="workflow-run-view">
        <div className="workflow-run-view__header">
          <span className="workflow-run-view__kicker">Workflow</span>
          {onClose && (
            <button type="button" className="workflow-run-view__hide" onClick={onClose}>
              Hide
            </button>
          )}
        </div>
        <p className="workflow-run-view__empty">No workflow run selected.</p>
      </div>
    );
  }

  const tone = toneForStatus(run.status);
  const calls: Call[] = run.agent_calls ?? [];
  const total = calls.length;
  const done = calls.filter((call) => TERMINAL_CALL_STATUSES.has(call.status)).length;
  const running = calls.find((call) => call.status === WorkflowAgentCallStatus.Running) ?? null;
  const runningElapsedMs =
    running && running.started_at ? Math.max(0, now - Date.parse(running.started_at)) : null;

  return (
    <div className="workflow-run-view" data-testid="workflow-run-view">
      <div className="workflow-run-view__header">
        <span className="workflow-run-view__kicker">Workflow</span>
        {onClose && (
          <button type="button" className="workflow-run-view__hide" onClick={onClose}>
            Hide
          </button>
        )}
      </div>

      <div className="workflow-run-view__title-row">
        <h2 className="workflow-run-view__title">{titleForRun(run)}</h2>
        <span className={`workflow-run-view__badge workflow-run-view__badge--${tone}`}>
          {statusLabel(run.status)}
        </span>
      </div>

      <div className="workflow-run-view__subtitle">
        <span className="workflow-run-view__meta">phase: {run.phase || '—'}</span>
        {run.harness && (
          <span className="workflow-run-view__meta">harness: {run.harness}</span>
        )}
      </div>

      <div className="workflow-run-view__progress">
        <span className="workflow-run-view__progress-count">
          {done}/{total} calls
        </span>
        {running && (
          <span className="workflow-run-view__progress-running">
            · running #{running.ordinal}
          </span>
        )}
      </div>

      {running && (
        <div className="workflow-run-view__current" data-testid="workflow-current-step">
          <span className="workflow-run-view__spinner" aria-hidden="true" />
          <span className="workflow-run-view__current-label">
            {running.label || `call ${running.ordinal}`}
          </span>
          {(running.phase || run.phase) && (
            <span className="workflow-run-view__current-phase">
              {running.phase || run.phase}
            </span>
          )}
          {running.resolved_model && (
            <span className="workflow-run-view__current-model">{running.resolved_model}</span>
          )}
          {runningElapsedMs !== null && (
            <span className="workflow-run-view__elapsed">{formatElapsed(runningElapsedMs)}</span>
          )}
        </div>
      )}

      <ul className="workflow-run-view__calls">
        {calls.map((call) => {
          const isRunning = call.status === WorkflowAgentCallStatus.Running;
          return (
          <li
            key={call.ordinal}
            className={`workflow-run-view__call${isRunning ? ' is-running' : ''}`}
            data-running={isRunning ? 'true' : undefined}
            data-testid={`workflow-call-${call.ordinal}`}
          >
            <span className="workflow-run-view__call-ordinal">{call.ordinal}</span>
            <span className="workflow-run-view__call-label">{call.label || call.ordinal}</span>
            <span
              className={`workflow-run-view__pill workflow-run-view__pill--${call.status}`}
            >
              {call.status}
            </span>
            {call.resolved_model && (
              <span className="workflow-run-view__call-model">{call.resolved_model}</span>
            )}
            {call.error && (
              <span className="workflow-run-view__call-error">{call.error}</span>
            )}
          </li>
          );
        })}
      </ul>

      {run.status === WorkflowRunStatus.Failed && run.last_error && (
        <div className="workflow-run-view__last-error">
          <div className="workflow-run-view__section-title">Last error</div>
          <pre className="workflow-run-view__error-text">{run.last_error}</pre>
        </div>
      )}

      {run.status === WorkflowRunStatus.Completed && run.result_json && (
        <div className="workflow-run-view__result">
          <div className="workflow-run-view__section-title">Result</div>
          <pre className="workflow-run-view__result-text">{run.result_json}</pre>
        </div>
      )}
    </div>
  );
}
