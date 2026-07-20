import { useEffect, useState } from 'react';
import { AutomationDefinitionSummary, AutomationRunSummary } from '../types/generated';
import { useAutomationsStore, selectDefinitionById, selectLatestRunForDefinition } from '../store/automations';
import './AutomationsPanel.css';

export interface AutomationsPanelProps {
  isOpen: boolean;
  onClose: () => void;
  fetchDefinitions: () => Promise<AutomationDefinitionSummary[]>;
  fetchRuns: (definitionId: string) => Promise<AutomationRunSummary[]>;
  setEnabled: (definitionId: string, enabled: boolean) => Promise<void>;
  runNow: (definitionId: string) => Promise<{ runId?: string; ticketId?: string; sessionId?: string }>;
  onOpenTicket: (ticketId: string) => void;
  onSelectSession: (sessionId: string) => void;
  onFocusPane: (sessionId: string, paneId: string) => void;
}

// Where a run row navigates. The wire nit applies here: ticket_id/session_id/
// pane_id are always present on AutomationRunSummary but "" means absent, so
// a plain truthiness check is the correct emptiness test. Exported for direct
// unit coverage of the ticket-over-session precedence.
export type RunNavigationTarget =
  | { kind: 'ticket'; ticketId: string }
  | { kind: 'session'; sessionId: string; paneId: string | null }
  | null;

export function runNavigationTarget(run: AutomationRunSummary): RunNavigationTarget {
  if (run.ticket_id) return { kind: 'ticket', ticketId: run.ticket_id };
  if (run.session_id) return { kind: 'session', sessionId: run.session_id, paneId: run.pane_id || null };
  return null;
}

function triggerLabel(definition: AutomationDefinitionSummary): string {
  switch (definition.trigger_type) {
    case 'manual':
      return 'Manual';
    case 'github_review_requested':
      return 'GitHub';
    case 'scheduled': {
      if (!definition.schedule_cron) return 'Scheduled';
      return definition.schedule_time_zone
        ? `Scheduled — ${definition.schedule_cron} (${definition.schedule_time_zone})`
        : `Scheduled — ${definition.schedule_cron}`;
    }
    default:
      return definition.trigger_type;
  }
}

export function AutomationsPanel({
  isOpen,
  onClose,
  fetchDefinitions,
  fetchRuns,
  setEnabled,
  runNow,
  onOpenTicket,
  onSelectSession,
  onFocusPane,
}: AutomationsPanelProps) {
  const definitions = useAutomationsStore((state) => state.definitions);
  const runsByDefinition = useAutomationsStore((state) => state.runsByDefinition);
  const changedTick = useAutomationsStore((state) => state.changedTick);

  const [selectedDefinitionId, setSelectedDefinitionId] = useState<string | null>(null);
  const [definitionsLoaded, setDefinitionsLoaded] = useState(false);
  const [definitionsError, setDefinitionsError] = useState<string | null>(null);
  const [runsError, setRunsError] = useState<string | null>(null);
  const [toggleErrors, setToggleErrors] = useState<Record<string, string>>({});
  const [toggleInFlight, setToggleInFlight] = useState<Record<string, boolean>>({});
  const [runErrors, setRunErrors] = useState<Record<string, string>>({});
  const [runInFlight, setRunInFlight] = useState<Record<string, boolean>>({});

  // Refetch on open and on every automations_changed tick. Also eagerly
  // fetches every definition's runs (not just the selected one) — the
  // definitions list shows a failure badge per row, so the badge data has to
  // exist before the user expands anything.
  useEffect(() => {
    if (!isOpen) return;
    let cancelled = false;
    fetchDefinitions()
      .then((fetched) => {
        if (cancelled) return;
        useAutomationsStore.getState().setDefinitions(fetched);
        setDefinitionsError(null);
        setDefinitionsLoaded(true);
        fetched.forEach((definition) => {
          fetchRuns(definition.id)
            .then((runs) => {
              if (!cancelled) useAutomationsStore.getState().setRuns(definition.id, runs);
            })
            .catch(() => {
              // Best-effort enrichment for the list badge; the definition row
              // still renders without it.
            });
        });
      })
      .catch((error) => {
        if (cancelled) return;
        setDefinitionsError(error instanceof Error ? error.message : 'Failed to load automations');
        setDefinitionsLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, changedTick, fetchDefinitions, fetchRuns]);

  // Refetch the selected definition's runs the moment it is selected, so the
  // expanded history is not stuck on whatever the eager fetch above last saw.
  useEffect(() => {
    if (!isOpen || !selectedDefinitionId) return;
    let cancelled = false;
    fetchRuns(selectedDefinitionId)
      .then((runs) => {
        if (cancelled) return;
        useAutomationsStore.getState().setRuns(selectedDefinitionId, runs);
        setRunsError(null);
      })
      .catch((error) => {
        if (!cancelled) setRunsError(error instanceof Error ? error.message : 'Failed to load runs');
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, selectedDefinitionId, fetchRuns]);

  if (!isOpen) return null;

  const selectedDefinition = selectDefinitionById(definitions, selectedDefinitionId);

  function handleToggle(definitionId: string, nextEnabled: boolean) {
    setToggleInFlight((prev) => ({ ...prev, [definitionId]: true }));
    setToggleErrors((prev) => {
      if (!(definitionId in prev)) return prev;
      const next = { ...prev };
      delete next[definitionId];
      return next;
    });
    setEnabled(definitionId, nextEnabled)
      .catch((error) => {
        setToggleErrors((prev) => ({
          ...prev,
          [definitionId]: error instanceof Error ? error.message : 'Failed to update',
        }));
      })
      .finally(() => {
        setToggleInFlight((prev) => ({ ...prev, [definitionId]: false }));
      });
  }

  function handleRunNow(definitionId: string) {
    setRunInFlight((prev) => ({ ...prev, [definitionId]: true }));
    setRunErrors((prev) => {
      if (!(definitionId in prev)) return prev;
      const next = { ...prev };
      delete next[definitionId];
      return next;
    });
    runNow(definitionId)
      // Success: the daemon's automations_changed broadcast drives the
      // refetch above. No synthetic run row is injected here.
      .catch((error) => {
        setRunErrors((prev) => ({
          ...prev,
          [definitionId]: error instanceof Error ? error.message : 'Run failed',
        }));
      })
      .finally(() => {
        setRunInFlight((prev) => ({ ...prev, [definitionId]: false }));
      });
  }

  function handleRunRowClick(run: AutomationRunSummary) {
    const target = runNavigationTarget(run);
    if (!target) return;
    if (target.kind === 'ticket') {
      onOpenTicket(target.ticketId);
      return;
    }
    onSelectSession(target.sessionId);
    if (target.paneId) onFocusPane(target.sessionId, target.paneId);
  }

  const showEmpty = definitionsLoaded && !definitionsError && definitions.length === 0;

  return (
    <div className="automations-panel" data-testid="automations-panel">
      <div className="automations-panel__header">
        <span className="automations-panel__kicker">Automations</span>
        <button
          type="button"
          className="automations-panel__close"
          onClick={onClose}
          aria-label="Close automations"
        >
          ✕
        </button>
      </div>

      {definitionsError && (
        <p className="automations-panel__error" data-testid="automations-panel-error">
          {definitionsError}
        </p>
      )}

      {showEmpty ? (
        <p className="automations-panel__empty" data-testid="automations-panel-empty">
          No automations defined — create one with <code>attn automation apply</code>.
        </p>
      ) : (
        <ul className="automations-panel__list" data-testid="automations-panel-list">
          {definitions.map((definition) => {
            const latestRun = selectLatestRunForDefinition(runsByDefinition[definition.id]);
            const failed = latestRun?.state === 'failed';
            const selected = definition.id === selectedDefinitionId;
            return (
              <li
                key={definition.id}
                className={`automations-panel__row${selected ? ' is-selected' : ''}`}
                data-testid="automation-definition-row"
                data-definition-id={definition.id}
              >
                <button
                  type="button"
                  className="automations-panel__row-main"
                  aria-pressed={selected}
                  onClick={() => setSelectedDefinitionId(selected ? null : definition.id)}
                  data-testid={`automation-definition-select-${definition.id}`}
                >
                  <span className="automations-panel__name">{definition.name}</span>
                  <span className="automations-panel__trigger">{triggerLabel(definition)}</span>
                  {failed && (
                    <span
                      className="automations-panel__badge automations-panel__badge--failed"
                      data-testid={`automation-failure-badge-${definition.id}`}
                    >
                      Failed
                    </span>
                  )}
                </button>

                <label className="automations-panel__toggle">
                  <input
                    type="checkbox"
                    checked={definition.enabled}
                    disabled={Boolean(toggleInFlight[definition.id])}
                    onChange={(event) => handleToggle(definition.id, event.target.checked)}
                    data-testid={`automation-toggle-${definition.id}`}
                  />
                  <span>{definition.enabled ? 'Enabled' : 'Disabled'}</span>
                </label>

                {definition.trigger_type === 'manual' && (
                  <button
                    type="button"
                    className="automations-panel__run-now"
                    disabled={Boolean(runInFlight[definition.id])}
                    onClick={() => handleRunNow(definition.id)}
                    data-testid={`automation-run-now-${definition.id}`}
                  >
                    Run now
                  </button>
                )}

                {toggleErrors[definition.id] && (
                  <p
                    className="automations-panel__error"
                    data-testid={`automation-toggle-error-${definition.id}`}
                  >
                    {toggleErrors[definition.id]}
                  </p>
                )}
                {runErrors[definition.id] && (
                  <p
                    className="automations-panel__error"
                    data-testid={`automation-run-error-${definition.id}`}
                  >
                    {runErrors[definition.id]}
                  </p>
                )}
                {failed && latestRun?.last_error && (
                  <p className="automations-panel__last-error">{latestRun.last_error}</p>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {selectedDefinition && (
        <div className="automations-panel__runs" data-testid="automations-panel-runs">
          <div className="automations-panel__runs-header">
            <h3 className="automations-panel__runs-title">{selectedDefinition.name} — runs</h3>
            <button
              type="button"
              className="automations-panel__runs-close"
              onClick={() => setSelectedDefinitionId(null)}
            >
              Close
            </button>
          </div>
          {runsError && <p className="automations-panel__error">{runsError}</p>}
          <ul className="automations-panel__run-list">
            {[...(runsByDefinition[selectedDefinition.id] ?? [])]
              .sort((a, b) => (a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0))
              .map((run) => {
                const target = runNavigationTarget(run);
                const content = (
                  <>
                    <span className={`automations-panel__run-state automations-panel__run-state--${run.state}`}>
                      {run.state}
                    </span>
                    <span className="automations-panel__run-created">
                      {new Date(run.created_at).toLocaleString()}
                    </span>
                    {run.delivered_at && (
                      <span className="automations-panel__run-delivered">
                        delivered {new Date(run.delivered_at).toLocaleString()}
                      </span>
                    )}
                    {run.occurrence_key && (
                      <span className="automations-panel__run-occurrence">{run.occurrence_key}</span>
                    )}
                  </>
                );
                return (
                  <li
                    key={run.id}
                    className="automations-panel__run-row"
                    data-testid="automation-run-row"
                    data-run-id={run.id}
                    data-state={run.state}
                  >
                    {target ? (
                      <button
                        type="button"
                        className="automations-panel__run-row-main"
                        onClick={() => handleRunRowClick(run)}
                        data-testid={`automation-run-open-${run.id}`}
                      >
                        {content}
                      </button>
                    ) : (
                      <div className="automations-panel__run-row-main">{content}</div>
                    )}
                    {run.last_error && <p className="automations-panel__run-error">{run.last_error}</p>}
                  </li>
                );
              })}
            {(runsByDefinition[selectedDefinition.id] ?? []).length === 0 && (
              <li className="automations-panel__run-empty">No runs yet.</li>
            )}
          </ul>
        </div>
      )}
    </div>
  );
}
