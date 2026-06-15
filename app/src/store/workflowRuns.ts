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
