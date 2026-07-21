import { useEffect, useState } from 'react';
import { AutomationDefinitionSummary, AutomationRunSummary } from '../types/generated';
import { useAutomationsStore, selectDefinitionById, selectLatestRunForDefinition } from '../store/automations';
import { AutomationActionTimeoutError } from '../hooks/useDaemonSocket';
import { AutomationEditor, automationEditorKey } from './automations/AutomationEditor';
import './AutomationsPanel.css';

export interface AutomationsPanelProps {
  isOpen: boolean;
  onClose: () => void;
  fetchDefinitions: () => Promise<AutomationDefinitionSummary[]>;
  fetchRuns: (definitionId: string) => Promise<AutomationRunSummary[]>;
  setEnabled: (definitionId: string, enabled: boolean) => Promise<void>;
  runNow: (
    definitionId: string,
    requestId: string,
  ) => Promise<{ runId?: string; ticketId?: string; sessionId?: string }>;
  getDefinition: (definitionId: string) => Promise<{ specYaml: string; revision: number }>;
  validateDefinition: (definitionYaml: string) => Promise<void>;
  applyDefinition: (
    definitionYaml: string,
    expectedId: string,
    expectedRevision: number,
  ) => Promise<{ definition: AutomationDefinitionSummary; specYaml: string; revision: number }>;
  onOpenTicket: (ticketId: string) => void;
  onSelectSession: (sessionId: string) => void;
  onFocusPane: (sessionId: string, paneId: string) => void;
}

// What the editor overlay is showing: closed, a fresh template (New
// automation), or an existing definition (Edit). Kept as a single value
// (rather than two booleans) so there is exactly one source of truth for
// "which target is open" — AutomationEditor is keyed off it, so switching
// targets always remounts a fresh editor instance (see automationEditorKey).
type EditorTarget = { definitionId: string | null } | null;

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

// reconcilePendingRunRequest clears a definition's in-flight run-now request
// key once a freshly-fetched run proves it reached a terminal state
// (delivered or failed). The key's occurrence_key is always "manual:<key>"
// (see internal/daemon/automations.go's automationRun); a run still pending
// keeps the key so an impatient re-click reuses the same request_id instead
// of minting a new one and claiming a duplicate run.
//
// When this session's store has no key for definitionId — most notably right
// after an app relaunch, since pendingRunRequests is in-memory only — a
// still-pending durable run on the daemon IS the retry identity. Adopt the
// newest pending manual occurrence from run history so the next click reuses
// it instead of minting a fresh id and claiming a second run.
function reconcilePendingRunRequest(definitionId: string, runs: AutomationRunSummary[]) {
  const key = useAutomationsStore.getState().pendingRunRequests[definitionId];
  if (key) {
    const occurrenceKey = `manual:${key}`;
    const match = runs.find((run) => run.occurrence_key === occurrenceKey);
    if (match && (match.state === 'delivered' || match.state === 'failed')) {
      useAutomationsStore.getState().clearRunRequest(definitionId);
    }
    return;
  }
  // Runs arrive newest-first, so the first match is the newest pending
  // manual run.
  const adoptable = runs.find((run) => run.state === 'pending' && run.occurrence_key?.startsWith('manual:'));
  if (adoptable?.occurrence_key) {
    useAutomationsStore
      .getState()
      .adoptRunRequest(definitionId, adoptable.occurrence_key.slice('manual:'.length));
  }
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
  getDefinition,
  validateDefinition,
  applyDefinition,
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
  // D6: this is the ONLY state that opens/closes/targets the editor. The list
  // refetch effect below never touches it, so an automations_changed broadcast
  // arriving while it's non-null cannot close the editor or swap its target —
  // see AutomationEditor.tsx's doc comment for the other half of the guarantee
  // (its buffer is loaded once per mount, not derived from the store).
  const [editorTarget, setEditorTarget] = useState<EditorTarget>(null);

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
              if (cancelled) return;
              useAutomationsStore.getState().setRuns(definition.id, runs);
              reconcilePendingRunRequest(definition.id, runs);
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
        reconcilePendingRunRequest(selectedDefinitionId, runs);
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
    const requestId = useAutomationsStore.getState().ensureRunRequest(definitionId);
    setRunInFlight((prev) => ({ ...prev, [definitionId]: true }));
    setRunErrors((prev) => {
      if (!(definitionId in prev)) return prev;
      const next = { ...prev };
      delete next[definitionId];
      return next;
    });
    runNow(definitionId, requestId)
      // Success: the daemon's automations_changed broadcast drives the
      // refetch above. No synthetic run row is injected here.
      .then(() => {
        useAutomationsStore.getState().clearRunRequest(definitionId);
      })
      .catch((error) => {
        if (error instanceof AutomationActionTimeoutError) {
          // No definitive outcome yet: the daemon may still deliver this run
          // after our wait window closed. Keep the request_id so a re-click
          // reuses it (dedups onto the same run) instead of claiming a
          // duplicate.
          setRunErrors((prev) => ({
            ...prev,
            [definitionId]:
              'Run request is still in flight — it will appear in run history; clicking again retries the same run.',
          }));
          return;
        }
        useAutomationsStore.getState().clearRunRequest(definitionId);
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

  // The editor is a full replacement of the panel body, not a sub-view of the
  // list — see EditorTarget's doc comment for why closing it is the only way
  // canonical list state (definitions/runsByDefinition) can affect it again.
  // Keyed on the target so switching from one Edit to another (or New) always
  // remounts AutomationEditor, which is what makes its mount-only load correct.
  if (editorTarget) {
    return (
      <div className="automations-panel" data-testid="automations-panel">
        <AutomationEditor
          key={automationEditorKey(editorTarget.definitionId)}
          definitionId={editorTarget.definitionId}
          getDefinition={getDefinition}
          validateDefinition={validateDefinition}
          applyDefinition={applyDefinition}
          onCancel={() => setEditorTarget(null)}
          onSaved={() => setEditorTarget(null)}
        />
      </div>
    );
  }

  return (
    <div className="automations-panel" data-testid="automations-panel">
      <div className="automations-panel__header">
        <span className="automations-panel__kicker">Automations</span>
        <div className="automations-panel__header-actions">
          <button
            type="button"
            className="automations-panel__new"
            onClick={() => setEditorTarget({ definitionId: null })}
            data-testid="automation-new"
          >
            New automation
          </button>
          <button
            type="button"
            className="automations-panel__close"
            onClick={onClose}
            aria-label="Close automations"
          >
            ✕
          </button>
        </div>
      </div>

      {definitionsError && (
        <p className="automations-panel__error" data-testid="automations-panel-error">
          {definitionsError}
        </p>
      )}

      {showEmpty ? (
        <div className="automations-panel__empty" data-testid="automations-panel-empty">
          <p>No automations yet.</p>
          <button
            type="button"
            className="automations-panel__new"
            onClick={() => setEditorTarget({ definitionId: null })}
            data-testid="automation-new-empty"
          >
            New automation
          </button>
        </div>
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

                <button
                  type="button"
                  className="automations-panel__edit"
                  onClick={() => setEditorTarget({ definitionId: definition.id })}
                  data-testid={`automation-edit-${definition.id}`}
                >
                  Edit
                </button>

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
