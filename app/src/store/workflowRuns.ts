import { create } from 'zustand';
import { WorkflowRun } from '../types/generated';

interface WorkflowRunsStore {
  // Workflow runs from the daemon, keyed by run_id.
  workflowRuns: Record<string, WorkflowRun>;

  // Immutable upsert keyed by run.run_id. A run with an empty run_id is ignored.
  upsertWorkflowRun: (run: WorkflowRun) => void;

  // Batch upsert; entries with an empty run_id are ignored.
  upsertWorkflowRuns: (runs: WorkflowRun[]) => void;

  // Immutable delete by run_id.
  removeWorkflowRun: (runId: string) => void;

  // Clears the map (test convenience).
  reset: () => void;
}

export const useWorkflowRunsStore = create<WorkflowRunsStore>((set) => ({
  workflowRuns: {},

  upsertWorkflowRun: (run) =>
    set((state) => {
      if (!run || !run.run_id) return state;
      return { workflowRuns: { ...state.workflowRuns, [run.run_id]: run } };
    }),

  upsertWorkflowRuns: (runs) =>
    set((state) => {
      if (!runs || runs.length === 0) return state;
      const next = { ...state.workflowRuns };
      for (const run of runs) {
        if (!run || !run.run_id) continue;
        next[run.run_id] = run;
      }
      return { workflowRuns: next };
    }),

  removeWorkflowRun: (runId) =>
    set((state) => {
      if (!runId || !(runId in state.workflowRuns)) return state;
      const next = { ...state.workflowRuns };
      delete next[runId];
      return { workflowRuns: next };
    }),

  reset: () => set({ workflowRuns: {} }),
}));

// selectLatestWorkflowRunForSession returns the most recently created run
// attached to sessionId, or null. created_at is an ISO-8601 string so a
// lexicographic max is a correct chronological max. Pure and unit-testable so
// the App.tsx dock panel can stay a thin selector over the slice.
export function selectLatestWorkflowRunForSession(
  runs: Record<string, WorkflowRun>,
  sessionId: string | null | undefined,
): WorkflowRun | null {
  if (!sessionId) return null;
  let latest: WorkflowRun | null = null;
  for (const run of Object.values(runs)) {
    if (!run || run.session_id !== sessionId) continue;
    if (!latest || run.created_at > latest.created_at) latest = run;
  }
  return latest;
}

// workflowRunIdNeedingHydration returns the run_id whose full journal the detail
// panel should fetch, or null when no fetch is needed. The run list backfill
// (workflow_run_list) omits each run's agent_calls because it is a summary
// surface, so a run that reaches the panel only via that backfill renders with no
// journal ("0/0 calls"). Live runs get their calls from workflow_run_updated
// broadcasts, but a completed run sees no further broadcasts and would stay
// call-less after a reload. We therefore hydrate exactly when the panel is open
// and the shown run has no calls; once getWorkflowRun upserts the hydrated run,
// agent_calls is non-empty and this returns null (no refetch loop). Pure and
// unit-testable so the App.tsx effect stays a thin trigger over this decision.
export function workflowRunIdNeedingHydration(
  panelOpen: boolean,
  run: WorkflowRun | null | undefined,
): string | null {
  if (!panelOpen || !run || !run.run_id) return null;
  if ((run.agent_calls?.length ?? 0) > 0) return null;
  return run.run_id;
}
