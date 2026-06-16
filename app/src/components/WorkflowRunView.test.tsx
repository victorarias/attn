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

  it('surfaces the in-flight call as a Current step callout with label, phase, and elapsed', () => {
    const startedAt = new Date(Date.now() - 65_000).toISOString();
    const run = makeRun({
      status: 'running' as WorkflowRun['status'],
      phase: 'review',
      agent_calls: [
        makeCall({ ordinal: '1', label: 'plan', status: 'ok' as Call['status'] }),
        makeCall({
          ordinal: '2',
          label: 'review changes',
          status: 'running' as Call['status'],
          phase: 'review',
          resolved_model: 'gpt-5-codex',
          started_at: startedAt,
        }),
      ],
    });
    render(<WorkflowRunView run={run} />);

    const callout = screen.getByTestId('workflow-current-step');
    expect(callout).toHaveTextContent('review changes');
    expect(callout).toHaveTextContent('gpt-5-codex');
    // started ~65s ago -> elapsed renders as 1:0X (m:ss), proving the live clock.
    expect(callout.textContent).toMatch(/1:0\d/);

    // The running row is emphasized for the eye.
    expect(screen.getByTestId('workflow-call-2')).toHaveAttribute('data-running', 'true');
    // done still excludes the running call: 1 of 2 terminal.
    expect(screen.getByText('1/2 calls')).toBeInTheDocument();
  });

  it('shows no Current step callout when nothing is running', () => {
    const run = makeRun({
      status: 'completed' as WorkflowRun['status'],
      agent_calls: [makeCall({ ordinal: '1', label: 'plan', status: 'ok' as Call['status'] })],
    });
    render(<WorkflowRunView run={run} />);
    expect(screen.queryByTestId('workflow-current-step')).toBeNull();
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
