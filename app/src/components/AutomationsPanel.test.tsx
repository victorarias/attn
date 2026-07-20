import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, userEvent } from '../test/utils';
import { AutomationsPanel } from './AutomationsPanel';
import { useAutomationsStore } from '../store/automations';
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
    state: 'delivered',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as AutomationRunSummary;
}

function baseProps() {
  return {
    isOpen: true,
    onClose: vi.fn(),
    fetchDefinitions: vi.fn().mockResolvedValue([]),
    fetchRuns: vi.fn().mockResolvedValue([]),
    setEnabled: vi.fn().mockResolvedValue(undefined),
    runNow: vi.fn().mockResolvedValue({}),
    onOpenTicket: vi.fn(),
    onSelectSession: vi.fn(),
    onFocusPane: vi.fn(),
  };
}

describe('AutomationsPanel', () => {
  beforeEach(() => {
    useAutomationsStore.getState().reset();
  });

  it('renders null when closed', () => {
    const props = baseProps();
    const { container } = render(<AutomationsPanel {...props} isOpen={false} />);
    expect(container).toBeEmptyDOMElement();
    expect(props.fetchDefinitions).not.toHaveBeenCalled();
  });

  it('renders the empty state when the daemon returns no definitions', async () => {
    render(<AutomationsPanel {...baseProps()} />);
    await waitFor(() => expect(screen.getByTestId('automations-panel-empty')).toBeInTheDocument());
    expect(screen.getByText(/create one with/i)).toBeInTheDocument();
  });

  it('renders definitions with name, trigger label, and manual-only Run now', async () => {
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([
      makeDefinition({ id: 'd1', name: 'PR reviewer', trigger_type: 'manual' }),
      makeDefinition({
        id: 'd2',
        name: 'Nightly digest',
        trigger_type: 'scheduled',
        schedule_cron: '0 9 * * *',
        schedule_time_zone: 'UTC',
      }),
      makeDefinition({ id: 'd3', name: 'Recurring reviewer', trigger_type: 'github_review_requested' }),
    ]);
    render(<AutomationsPanel {...props} />);

    await waitFor(() => expect(screen.getAllByTestId('automation-definition-row')).toHaveLength(3));
    expect(screen.getByText('PR reviewer')).toBeInTheDocument();
    expect(screen.getByText('Nightly digest')).toBeInTheDocument();
    expect(screen.getByText('Scheduled — 0 9 * * * (UTC)')).toBeInTheDocument();
    expect(screen.getByText('Recurring reviewer')).toBeInTheDocument();
    expect(screen.getByText('GitHub')).toBeInTheDocument();

    expect(screen.getByTestId('automation-run-now-d1')).toBeInTheDocument();
    expect(screen.queryByTestId('automation-run-now-d2')).not.toBeInTheDocument();
  });

  it('shows a failure badge and last_error when a definition latest run failed', async () => {
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
    props.fetchRuns.mockResolvedValue([
      makeRun({ id: 'r1', state: 'failed', created_at: '2026-01-02T00:00:00Z', last_error: 'boom' }),
      makeRun({ id: 'r2', state: 'delivered', created_at: '2026-01-01T00:00:00Z' }),
    ]);
    render(<AutomationsPanel {...props} />);

    await waitFor(() => expect(screen.getByTestId('automation-failure-badge-d1')).toBeInTheDocument());
    expect(screen.getByText('boom')).toBeInTheDocument();
  });

  it('does not flip the toggle optimistically and shows an inline error on rejection', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', enabled: true })]);
    props.setEnabled.mockRejectedValue(new Error('daemon refused'));
    render(<AutomationsPanel {...props} />);

    const toggle = await screen.findByTestId('automation-toggle-d1');
    expect(toggle).toBeChecked();

    await user.click(toggle);

    await waitFor(() => expect(screen.getByTestId('automation-toggle-error-d1')).toHaveTextContent('daemon refused'));
    expect(props.setEnabled).toHaveBeenCalledWith('d1', false);
    // Store state was never locally mutated; the checkbox reflects the
    // (unchanged) definition.enabled from the store, not an optimistic flip.
    expect(toggle).toBeChecked();
  });

  it('shows an inline error on Run now rejection without disabling the button afterward', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', trigger_type: 'manual' })]);
    props.runNow.mockRejectedValue(new Error('busy'));
    render(<AutomationsPanel {...props} />);

    const button = await screen.findByTestId('automation-run-now-d1');
    await user.click(button);

    await waitFor(() => expect(screen.getByTestId('automation-run-error-d1')).toHaveTextContent('busy'));
    expect(props.runNow).toHaveBeenCalledWith('d1');
    expect(button).not.toBeDisabled();
  });

  it('refetches definitions when changedTick bumps', async () => {
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
    render(<AutomationsPanel {...props} />);

    await waitFor(() => expect(props.fetchDefinitions).toHaveBeenCalledTimes(1));

    useAutomationsStore.getState().bumpChanged();

    await waitFor(() => expect(props.fetchDefinitions).toHaveBeenCalledTimes(2));
  });

  it('navigates to the ticket when a run has a ticket_id', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
    props.fetchRuns.mockResolvedValue([makeRun({ id: 'r1', ticket_id: 't1', session_id: 's1' })]);
    render(<AutomationsPanel {...props} />);

    await user.click(await screen.findByText('PR reviewer'));
    const runOpen = await screen.findByTestId('automation-run-open-r1');
    await user.click(runOpen);

    expect(props.onOpenTicket).toHaveBeenCalledWith('t1');
    expect(props.onSelectSession).not.toHaveBeenCalled();
  });

  it('navigates to the session and focuses the pane when a run has session_id/pane_id but no ticket_id', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
    props.fetchRuns.mockResolvedValue([makeRun({ id: 'r1', session_id: 's1', pane_id: 'p1' })]);
    render(<AutomationsPanel {...props} />);

    await user.click(await screen.findByText('PR reviewer'));
    const runOpen = await screen.findByTestId('automation-run-open-r1');
    await user.click(runOpen);

    expect(props.onSelectSession).toHaveBeenCalledWith('s1');
    expect(props.onFocusPane).toHaveBeenCalledWith('s1', 'p1');
  });

  it('renders a run with no ticket_id/session_id as non-navigable', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
    props.fetchRuns.mockResolvedValue([makeRun({ id: 'r1' })]);
    render(<AutomationsPanel {...props} />);

    await user.click(await screen.findByText('PR reviewer'));
    await screen.findByTestId('automation-run-row');
    expect(screen.queryByTestId('automation-run-open-r1')).not.toBeInTheDocument();
    expect(props.onOpenTicket).not.toHaveBeenCalled();
    expect(props.onSelectSession).not.toHaveBeenCalled();
  });
});
