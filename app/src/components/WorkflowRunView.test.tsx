import { describe, it, expect } from 'vitest';
import { render, screen } from '../test/utils';
import { WorkflowRunView } from './WorkflowRunView';
import { WorkflowRun, Call } from '../types/generated';

function makeCall(overrides: Partial<Call> & { ordinal: string }): Call {
  return {
    status: 'running',
    run_id: 'wr1',
    ...overrides,
  } as Call;
}

function makeRun(overrides: Partial<WorkflowRun>): WorkflowRun {
  return {
    run_id: 'wr1',
    status: 'running',
    script_path: '/x/build/wf.js',
    script_hash: 'abc',
    created_at: '2026-06-15T00:00:00Z',
    updated_at: '2026-06-15T00:00:00Z',
    resumable: false,
    ...overrides,
  } as WorkflowRun;
}

describe('WorkflowRunView', () => {
  it('renders empty-state when run is null', () => {
    render(<WorkflowRunView run={null} />);
    expect(screen.getByText('No workflow run selected.')).toBeInTheDocument();
  });

  it('renders a running run with phase, status, an agent-call row, and progress', () => {
    const run = makeRun({
      status: 'running' as WorkflowRun['status'],
      phase: 'plan',
      agent_calls: [
        makeCall({ ordinal: '1', label: 'design step', status: 'ok' as Call['status'] }),
        makeCall({ ordinal: '2', label: 'build step', status: 'running' as Call['status'] }),
      ],
    });
    render(<WorkflowRunView run={run} />);

    expect(screen.getByText('Running')).toBeInTheDocument();
    expect(screen.getByText('phase: plan')).toBeInTheDocument();
    // 1 of 2 calls terminal (ok) -> "1/2 calls"
    expect(screen.getByText('1/2 calls')).toBeInTheDocument();

    const callRow = screen.getByTestId('workflow-call-1');
    expect(callRow).toHaveTextContent('design step');
    expect(callRow).toHaveTextContent('1');
  });

  it('shows the last_error text for a failed run', () => {
    const run = makeRun({
      status: 'failed' as WorkflowRun['status'],
      last_error: 'boom: step 2 exploded',
    });
    render(<WorkflowRunView run={run} />);
    expect(screen.getByText('boom: step 2 exploded')).toBeInTheDocument();
  });

  it('shows the result_json text for a completed run', () => {
    const run = makeRun({
      status: 'completed' as WorkflowRun['status'],
      result_json: '{"ok":true,"summary":"all green"}',
    });
    render(<WorkflowRunView run={run} />);
    expect(screen.getByText('{"ok":true,"summary":"all green"}')).toBeInTheDocument();
  });

  it('is strictly read-only: no mutating controls and no input roles', () => {
    const run = makeRun({
      status: 'running' as WorkflowRun['status'],
      agent_calls: [makeCall({ ordinal: '1', label: 'step', status: 'running' as Call['status'] })],
    });
    render(<WorkflowRunView run={run} />);

    const mutating = screen
      .queryAllByRole('button')
      .filter((el) => /stop|cancel|start|restart|answer|send/i.test(el.textContent || ''));
    expect(mutating).toHaveLength(0);

    expect(screen.queryByRole('textbox')).toBeNull();
    expect(screen.queryByRole('spinbutton')).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('renders no buttons at all when onClose is omitted', () => {
    const run = makeRun({ status: 'running' as WorkflowRun['status'] });
    render(<WorkflowRunView run={run} />);
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });

  it('renders only the local Hide button when onClose is provided', () => {
    const run = makeRun({ status: 'running' as WorkflowRun['status'] });
    render(<WorkflowRunView run={run} onClose={() => {}} />);
    const buttons = screen.queryAllByRole('button');
    expect(buttons).toHaveLength(1);
    expect(buttons[0]).toHaveTextContent('Hide');
  });
});
