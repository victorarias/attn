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

  // The run-now idempotency key currently in flight per definition, keyed by
  // definition_id. The daemon persists this as ClaimManualAutomationRun's
  // dedup key (occurrence_key "manual:<key>"), so a client-side retry of the
  // same click must reuse it rather than minting a fresh crypto.randomUUID()
  // — otherwise a run-now that times out client-side but still delivers on
  // the daemon can be re-triggered as a duplicate run by an impatient second
  // click. Populated by ensureRunRequest, cleared by clearRunRequest once the
  // run reaches a terminal state (delivered/failed) or a definitive daemon
  // rejection makes retrying pointless.
  //
  // This map is in-memory only and does not survive an app relaunch. The key
  // still has to be recoverable afterward: an unsettled durable run persists
  // in the daemon's SQLite state regardless of what this store remembers, so
  // canonical run history (not a local persistence layer) is the recovery
  // source — see adoptRunRequest.
  pendingRunRequests: Record<string, string>;

  setDefinitions: (definitions: AutomationDefinitionSummary[]) => void;
  setRuns: (definitionId: string, runs: AutomationRunSummary[]) => void;
  bumpChanged: () => void;
  ensureRunRequest: (definitionId: string) => string;
  clearRunRequest: (definitionId: string) => void;

  // adoptRunRequest recovers a pending run's request id when this session's
  // store has none for definitionId (e.g. right after an app relaunch). It
  // never overwrites an already-stored key: a key already in flight for this
  // session is more current than anything derived from a later fetch.
  adoptRunRequest: (definitionId: string, requestId: string) => void;

  // Clears the store (test convenience).
  reset: () => void;
}

export const useAutomationsStore = create<AutomationsStore>((set, get) => ({
  definitions: [],
  runsByDefinition: {},
  changedTick: 0,
  pendingRunRequests: {},

  setDefinitions: (definitions) => set({ definitions: definitions ?? [] }),

  setRuns: (definitionId, runs) =>
    set((state) => {
      if (!definitionId) return state;
      return { runsByDefinition: { ...state.runsByDefinition, [definitionId]: runs ?? [] } };
    }),

  bumpChanged: () => set((state) => ({ changedTick: state.changedTick + 1 })),

  ensureRunRequest: (definitionId) => {
    const existing = get().pendingRunRequests[definitionId];
    if (existing) return existing;
    const requestId = crypto.randomUUID();
    set((state) => ({ pendingRunRequests: { ...state.pendingRunRequests, [definitionId]: requestId } }));
    return requestId;
  },

  clearRunRequest: (definitionId) =>
    set((state) => {
      if (!(definitionId in state.pendingRunRequests)) return state;
      const next = { ...state.pendingRunRequests };
      delete next[definitionId];
      return { pendingRunRequests: next };
    }),

  adoptRunRequest: (definitionId, requestId) =>
    set((state) => {
      if (state.pendingRunRequests[definitionId]) return state;
      return { pendingRunRequests: { ...state.pendingRunRequests, [definitionId]: requestId } };
    }),

  reset: () => set({ definitions: [], runsByDefinition: {}, changedTick: 0, pendingRunRequests: {} }),
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
