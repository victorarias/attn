import { create } from 'zustand';
import { AutomationDefinitionSummary, AutomationRunSummary } from '../types/generated';

interface AutomationsStore {
  // All automation definitions for the profile, as last fetched via
  // automation_definitions_get. Empty until the panel's first fetch resolves.
  definitions: AutomationDefinitionSummary[];

  // Runs per definition, keyed by definition_id, as last fetched via
  // automation_runs_get. A definition with no entry has not been fetched yet
  // (distinct from an entry of []: no runs exist).
  runsByDefinition: Record<string, AutomationRunSummary[]>;

  // Bumped by the automations_changed broadcast handler. Views watch this to
  // know when to re-fetch; it carries no data of its own.
  changedTick: number;

  setDefinitions: (definitions: AutomationDefinitionSummary[]) => void;
  setRuns: (definitionId: string, runs: AutomationRunSummary[]) => void;
  bumpChanged: () => void;

  // Clears the store (test convenience).
  reset: () => void;
}

export const useAutomationsStore = create<AutomationsStore>((set) => ({
  definitions: [],
  runsByDefinition: {},
  changedTick: 0,

  setDefinitions: (definitions) => set({ definitions: definitions ?? [] }),

  setRuns: (definitionId, runs) =>
    set((state) => {
      if (!definitionId) return state;
      return { runsByDefinition: { ...state.runsByDefinition, [definitionId]: runs ?? [] } };
    }),

  bumpChanged: () => set((state) => ({ changedTick: state.changedTick + 1 })),

  reset: () => set({ definitions: [], runsByDefinition: {}, changedTick: 0 }),
}));

// selectDefinitionById returns the definition with the given id, or null.
// Pure and unit-testable so AutomationsPanel stays a thin selector over the
// slice.
export function selectDefinitionById(
  definitions: AutomationDefinitionSummary[],
  definitionId: string | null | undefined,
): AutomationDefinitionSummary | null {
  if (!definitionId) return null;
  return definitions.find((definition) => definition.id === definitionId) ?? null;
}

// selectLatestRunForDefinition returns the most recently created run in
// `runs`, or null when there are none. created_at is an ISO-8601 string so a
// lexicographic max is a correct chronological max. Used for the definitions
// list's failure badge, which reflects each definition's latest run without
// requiring the user to expand it.
export function selectLatestRunForDefinition(
  runs: AutomationRunSummary[] | undefined,
): AutomationRunSummary | null {
  if (!runs || runs.length === 0) return null;
  let latest: AutomationRunSummary | null = null;
  for (const run of runs) {
    if (!latest || run.created_at > latest.created_at) latest = run;
  }
  return latest;
}
