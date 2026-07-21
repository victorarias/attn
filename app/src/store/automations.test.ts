import { describe, it, expect, beforeEach } from 'vitest';
import { useAutomationsStore, selectDefinitionById } from './automations';
import { AutomationDefinitionSummary, AutomationRunSummary } from '../types/generated';

function makeDefinition(
  overrides: Partial<AutomationDefinitionSummary> & { id: string },
): AutomationDefinitionSummary {
  return {
    name: 'PR reviewer',
    enabled: true,
    revision: 1,
    trigger_type: 'manual',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as AutomationDefinitionSummary;
}

function makeRun(
  overrides: Partial<AutomationRunSummary> & { id: string },
): AutomationRunSummary {
  return {
    definition_id: 'd1',
    definition_revision: 1,
    state: 'pending',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as AutomationRunSummary;
}

describe('useAutomationsStore', () => {
  beforeEach(() => {
    useAutomationsStore.getState().reset();
  });

  it('setDefinitions replaces the definitions list', () => {
    useAutomationsStore.getState().setDefinitions([makeDefinition({ id: 'd1' }), makeDefinition({ id: 'd2' })]);
    expect(useAutomationsStore.getState().definitions.map((d) => d.id)).toEqual(['d1', 'd2']);
  });

  it('setDefinitions with an empty/undefined list clears to []', () => {
    useAutomationsStore.getState().setDefinitions([makeDefinition({ id: 'd1' })]);
    useAutomationsStore.getState().setDefinitions(undefined as unknown as AutomationDefinitionSummary[]);
    expect(useAutomationsStore.getState().definitions).toEqual([]);
  });

  it('setRuns stores runs keyed by definition_id', () => {
    useAutomationsStore.getState().setRuns('d1', [makeRun({ id: 'r1' }), makeRun({ id: 'r2' })]);
    const runs = useAutomationsStore.getState().runsByDefinition;
    expect(runs['d1']?.map((r) => r.id)).toEqual(['r1', 'r2']);
  });

  it('setRuns ignores an empty definitionId', () => {
    useAutomationsStore.getState().setRuns('', [makeRun({ id: 'r1' })]);
    expect(useAutomationsStore.getState().runsByDefinition).toEqual({});
  });

  it('setRuns for one definition leaves another definition untouched', () => {
    const store = useAutomationsStore.getState();
    store.setRuns('d1', [makeRun({ id: 'r1' })]);
    store.setRuns('d2', [makeRun({ id: 'r2' })]);
    const runs = useAutomationsStore.getState().runsByDefinition;
    expect(runs['d1']?.map((r) => r.id)).toEqual(['r1']);
    expect(runs['d2']?.map((r) => r.id)).toEqual(['r2']);
  });

  it('re-setting runs for a definition replaces its prior entry', () => {
    const store = useAutomationsStore.getState();
    store.setRuns('d1', [makeRun({ id: 'r1' })]);
    store.setRuns('d1', [makeRun({ id: 'r2' })]);
    expect(useAutomationsStore.getState().runsByDefinition['d1']?.map((r) => r.id)).toEqual(['r2']);
  });

  it('bumpChanged increments changedTick', () => {
    expect(useAutomationsStore.getState().changedTick).toBe(0);
    useAutomationsStore.getState().bumpChanged();
    useAutomationsStore.getState().bumpChanged();
    expect(useAutomationsStore.getState().changedTick).toBe(2);
  });

  it('reset clears definitions, runs, and changedTick', () => {
    const store = useAutomationsStore.getState();
    store.setDefinitions([makeDefinition({ id: 'd1' })]);
    store.setRuns('d1', [makeRun({ id: 'r1' })]);
    store.bumpChanged();
    store.reset();
    const state = useAutomationsStore.getState();
    expect(state.definitions).toEqual([]);
    expect(state.runsByDefinition).toEqual({});
    expect(state.changedTick).toBe(0);
  });

  it('ensureRunRequest returns a stable id for a definition until cleared', () => {
    const store = useAutomationsStore.getState();
    const first = store.ensureRunRequest('d1');
    const second = store.ensureRunRequest('d1');
    expect(second).toBe(first);
    expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBe(first);

    store.clearRunRequest('d1');
    expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBeUndefined();

    const third = store.ensureRunRequest('d1');
    expect(third).not.toBe(first);
  });

  it('ensureRunRequest tracks separate ids per definition', () => {
    const store = useAutomationsStore.getState();
    const d1 = store.ensureRunRequest('d1');
    const d2 = store.ensureRunRequest('d2');
    expect(d1).not.toBe(d2);
    expect(useAutomationsStore.getState().pendingRunRequests).toEqual({ d1, d2 });
  });

  it('clearRunRequest is a no-op when there is no pending request', () => {
    const store = useAutomationsStore.getState();
    store.clearRunRequest('does-not-exist');
    expect(useAutomationsStore.getState().pendingRunRequests).toEqual({});
  });

  it('reset clears pendingRunRequests', () => {
    const store = useAutomationsStore.getState();
    store.ensureRunRequest('d1');
    store.reset();
    expect(useAutomationsStore.getState().pendingRunRequests).toEqual({});
  });

  it('adoptRunRequest stores the key when none is present for the definition', () => {
    const store = useAutomationsStore.getState();
    store.adoptRunRequest('d1', 'restart-key-1');
    expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBe('restart-key-1');
  });

  it('adoptRunRequest never overwrites an already-stored key', () => {
    const store = useAutomationsStore.getState();
    const existing = store.ensureRunRequest('d1');
    store.adoptRunRequest('d1', 'some-other-key');
    expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBe(existing);
  });

  it('adoptRunRequest is cleared by reset', () => {
    const store = useAutomationsStore.getState();
    store.adoptRunRequest('d1', 'restart-key-1');
    store.reset();
    expect(useAutomationsStore.getState().pendingRunRequests).toEqual({});
  });
});

describe('selectDefinitionById', () => {
  it('returns null when definitionId is empty/undefined', () => {
    const definitions = [makeDefinition({ id: 'd1' })];
    expect(selectDefinitionById(definitions, null)).toBeNull();
    expect(selectDefinitionById(definitions, undefined)).toBeNull();
    expect(selectDefinitionById(definitions, '')).toBeNull();
  });

  it('returns null when no definition matches', () => {
    expect(selectDefinitionById([makeDefinition({ id: 'd1' })], 'd2')).toBeNull();
  });

  it('returns the matching definition', () => {
    const definitions = [makeDefinition({ id: 'd1' }), makeDefinition({ id: 'd2', name: 'Second' })];
    expect(selectDefinitionById(definitions, 'd2')?.name).toBe('Second');
  });
});
