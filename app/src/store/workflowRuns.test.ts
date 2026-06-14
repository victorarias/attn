import { describe, it, expect, beforeEach } from 'vitest';
import { useWorkflowRunsStore } from './workflowRuns';
import { WorkflowRun } from '../types/generated';

function makeRun(overrides: Partial<WorkflowRun> & { run_id: string }): WorkflowRun {
  return {
    status: 'running',
    script_path: '/x/wf.js',
    ...overrides,
  } as WorkflowRun;
}

describe('useWorkflowRunsStore', () => {
  beforeEach(() => {
    useWorkflowRunsStore.getState().reset();
  });

  it('upsertWorkflowRun stores keyed by run_id', () => {
    useWorkflowRunsStore.getState().upsertWorkflowRun(makeRun({ run_id: 'wr1' }));
    const runs = useWorkflowRunsStore.getState().workflowRuns;
    expect(runs['wr1']).toBeDefined();
    expect(runs['wr1'].run_id).toBe('wr1');
    expect(runs['wr1'].status).toBe('running');
  });

  it('re-upserting the same run_id replaces the existing run', () => {
    const store = useWorkflowRunsStore.getState();
    store.upsertWorkflowRun(makeRun({ run_id: 'wr1', status: 'running' as WorkflowRun['status'] }));
    store.upsertWorkflowRun(makeRun({ run_id: 'wr1', status: 'completed' as WorkflowRun['status'] }));
    const runs = useWorkflowRunsStore.getState().workflowRuns;
    expect(Object.keys(runs)).toHaveLength(1);
    expect(runs['wr1'].status).toBe('completed');
  });

  it('upsertWorkflowRun ignores a run with empty run_id', () => {
    useWorkflowRunsStore.getState().upsertWorkflowRun(makeRun({ run_id: '' }));
    expect(useWorkflowRunsStore.getState().workflowRuns).toEqual({});
  });

  it('upsertWorkflowRuns batch inserts multiple runs', () => {
    useWorkflowRunsStore.getState().upsertWorkflowRuns([
      makeRun({ run_id: 'wr1' }),
      makeRun({ run_id: 'wr2' }),
      makeRun({ run_id: '' }), // ignored
    ]);
    const runs = useWorkflowRunsStore.getState().workflowRuns;
    expect(Object.keys(runs).sort()).toEqual(['wr1', 'wr2']);
  });

  it('removeWorkflowRun deletes one and leaves others', () => {
    const store = useWorkflowRunsStore.getState();
    store.upsertWorkflowRuns([makeRun({ run_id: 'wr1' }), makeRun({ run_id: 'wr2' })]);
    store.removeWorkflowRun('wr1');
    const runs = useWorkflowRunsStore.getState().workflowRuns;
    expect(runs['wr1']).toBeUndefined();
    expect(runs['wr2']).toBeDefined();
  });
});
