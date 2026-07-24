import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, userEvent } from '../test/utils';
import { AutomationsPanel } from './AutomationsPanel';
import { useAutomationsStore } from '../store/automations';
import { AutomationActionTimeoutError } from '../hooks/useDaemonSocket';
import { AutomationDefinitionSummary, AutomationRunSummary } from '../types/generated';
import { AutomationFormValues, specJSONString } from './automations/automationFormModel';
import { LAUNCH_CATALOG } from './automations/launchCatalog';

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
    state: 'delivered',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as AutomationRunSummary;
}

// A valid v1alpha1 manual spec, so AutomationForm's edit-mode load
// (specToFormValues) succeeds without a parse error banner.
function manualSpecJson(overrides: Partial<AutomationFormValues> = {}): string {
  const firstModel = LAUNCH_CATALOG.codex.models[0];
  const values: AutomationFormValues = {
    name: 'PR reviewer',
    id: 'd1',
    idCustomized: true,
    trigger: 'manual',
    scheduleCron: '',
    continuity: 'fresh',
    catchUp: '',
    repositoriesInclude: [],
    repositoriesExclude: [],
    agent: 'codex',
    model: firstModel.id,
    effort: firstModel.defaultEffort,
    executable: '',
    directoryPath: '/tmp/work',
    repositoryOverrides: [],
    prompt: 'Do the work',
    ...overrides,
  };
  return specJSONString(values);
}

function baseProps() {
  return {
    isOpen: true,
    onClose: vi.fn(),
    fetchDefinitions: vi.fn().mockResolvedValue([]),
    fetchRuns: vi.fn().mockResolvedValue([]),
    setEnabled: vi.fn().mockResolvedValue(undefined),
    runNow: vi.fn().mockResolvedValue(undefined),
    getDefinition: vi.fn().mockResolvedValue({ specYaml: '', specJson: manualSpecJson() }),
    applyDefinition: vi.fn().mockResolvedValue({
      definition: makeDefinition({ id: 'd1', revision: 2 }),
      specYaml: '',
    }),
    deleteDefinition: vi.fn().mockResolvedValue(undefined),
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

  it('renders the empty state with a New automation affordance instead of a CLI instruction', async () => {
    render(<AutomationsPanel {...baseProps()} />);
    await waitFor(() => expect(screen.getByTestId('automations-panel-empty')).toBeInTheDocument());
    expect(screen.queryByText(/attn automation apply/i)).not.toBeInTheDocument();
    expect(screen.getByTestId('automation-new-empty')).toBeInTheDocument();
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

  it('shows a failure badge and last_error when a definition\'s embedded last_run failed', async () => {
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([
      makeDefinition({
        id: 'd1',
        last_run: makeRun({ id: 'r1', state: 'failed', created_at: '2026-01-02T00:00:00Z', last_error: 'boom' }),
      }),
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
    expect(props.runNow).toHaveBeenCalledWith('d1', expect.any(String));
    expect(button).not.toBeDisabled();
  });

  it('a definitive Run now rejection clears the pending request id', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', trigger_type: 'manual' })]);
    props.runNow.mockRejectedValue(new Error('automation is disabled'));
    render(<AutomationsPanel {...props} />);

    const button = await screen.findByTestId('automation-run-now-d1');
    await user.click(button);

    await waitFor(() => expect(screen.getByTestId('automation-run-error-d1')).toHaveTextContent('automation is disabled'));
    expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBeUndefined();

    await user.click(button);
    const [firstCall, secondCall] = props.runNow.mock.calls;
    expect(secondCall[1]).not.toBe(firstCall[1]);
  });

  it('a Run now timeout keeps the pending request id so a retry reuses it', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', trigger_type: 'manual' })]);
    props.runNow.mockRejectedValue(new AutomationActionTimeoutError('Run automation timed out'));
    render(<AutomationsPanel {...props} />);

    const button = await screen.findByTestId('automation-run-now-d1');
    await user.click(button);

    await waitFor(() =>
      expect(screen.getByTestId('automation-run-error-d1')).toHaveTextContent(/still in flight/i),
    );
    const pendingId = useAutomationsStore.getState().pendingRunRequests['d1'];
    expect(pendingId).toBeTruthy();

    await user.click(button);
    const [firstCall, secondCall] = props.runNow.mock.calls;
    expect(firstCall[1]).toBe(pendingId);
    expect(secondCall[1]).toBe(pendingId);
  });

  it('reconciles a pending run request once a definition\'s embedded last_run for its request id reaches delivered', async () => {
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', trigger_type: 'manual' })]);
    useAutomationsStore.getState().reset();
    render(<AutomationsPanel {...props} />);
    await screen.findByTestId('automation-run-now-d1');

    const pendingId = useAutomationsStore.getState().ensureRunRequest('d1');

    // Still pending: the key survives.
    props.fetchDefinitions.mockResolvedValue([
      makeDefinition({
        id: 'd1',
        trigger_type: 'manual',
        last_run: makeRun({ id: 'r1', occurrence_key: `manual:${pendingId}`, state: 'pending' }),
      }),
    ]);
    useAutomationsStore.getState().bumpChanged();
    await waitFor(() => expect(props.fetchDefinitions).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBe(pendingId));

    // Delivered: the key clears, so the next click mints a fresh id.
    props.fetchDefinitions.mockResolvedValue([
      makeDefinition({
        id: 'd1',
        trigger_type: 'manual',
        last_run: makeRun({ id: 'r1', occurrence_key: `manual:${pendingId}`, state: 'delivered' }),
      }),
    ]);
    useAutomationsStore.getState().bumpChanged();
    await waitFor(() => expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBeUndefined());
  });

  it('adopts a pending manual run id from a definition\'s embedded last_run after a simulated relaunch, so Run now retries it instead of minting a fresh id', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([
      makeDefinition({
        id: 'd1',
        trigger_type: 'manual',
        last_run: makeRun({ id: 'r1', occurrence_key: 'manual:restart-key-1', state: 'pending' }),
      }),
    ]);
    // Simulate relaunch: the store starts empty regardless of what the
    // daemon still has pending.
    useAutomationsStore.getState().reset();
    render(<AutomationsPanel {...props} />);

    const button = await screen.findByTestId('automation-run-now-d1');
    await waitFor(() => expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBe('restart-key-1'));

    await user.click(button);
    expect(props.runNow).toHaveBeenCalledWith('d1', 'restart-key-1');
  });

  it('does not adopt a pending run whose occurrence_key is not manual-prefixed', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([
      makeDefinition({
        id: 'd1',
        trigger_type: 'manual',
        last_run: makeRun({ id: 'r1', occurrence_key: 'sched:2026-01-01T00:00:00Z', state: 'pending' }),
      }),
    ]);
    useAutomationsStore.getState().reset();
    render(<AutomationsPanel {...props} />);

    const button = await screen.findByTestId('automation-run-now-d1');
    await waitFor(() => expect(props.fetchDefinitions).toHaveBeenCalled());
    expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBeUndefined();

    await user.click(button);
    const [requestId] = props.runNow.mock.calls[0].slice(1);
    expect(requestId).not.toBe('sched:2026-01-01T00:00:00Z');
    expect(typeof requestId).toBe('string');
  });

  it('adoption never overwrites a key already stored for this session', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    useAutomationsStore.getState().reset();
    const storedKey = useAutomationsStore.getState().ensureRunRequest('d1');
    props.fetchDefinitions.mockResolvedValue([
      makeDefinition({
        id: 'd1',
        trigger_type: 'manual',
        last_run: makeRun({ id: 'r1', occurrence_key: 'manual:some-other-pending-run', state: 'pending' }),
      }),
    ]);
    render(<AutomationsPanel {...props} />);

    const button = await screen.findByTestId('automation-run-now-d1');
    await waitFor(() => expect(props.fetchDefinitions).toHaveBeenCalled());
    expect(useAutomationsStore.getState().pendingRunRequests['d1']).toBe(storedKey);

    await user.click(button);
    expect(props.runNow).toHaveBeenCalledWith('d1', storedKey);
  });

  it('refetches definitions when changedTick bumps', async () => {
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
    render(<AutomationsPanel {...props} />);

    await waitFor(() => expect(props.fetchDefinitions).toHaveBeenCalledTimes(1));

    useAutomationsStore.getState().bumpChanged();

    await waitFor(() => expect(props.fetchDefinitions).toHaveBeenCalledTimes(2));
  });

  it('refetches the selected definition\'s runs when changedTick bumps, so a newly delivered run appears without re-selecting', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
    props.fetchRuns.mockResolvedValueOnce([makeRun({ id: 'r1', state: 'pending' })]);
    render(<AutomationsPanel {...props} />);

    await user.click(await screen.findByText('PR reviewer'));
    await screen.findByTestId('automation-run-row');
    await waitFor(() => expect(props.fetchRuns).toHaveBeenCalledTimes(1));

    props.fetchRuns.mockResolvedValueOnce([
      makeRun({ id: 'r1', state: 'delivered' }),
      makeRun({ id: 'r2', state: 'delivered' }),
    ]);
    useAutomationsStore.getState().bumpChanged();

    await waitFor(() => expect(props.fetchRuns).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getAllByTestId('automation-run-row')).toHaveLength(2));
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

  // Form-level coverage (validation, error routing, delete flow) belongs to
  // AutomationForm.test.tsx. These only prove the overlay opens with the
  // right target, props are wired through, and closing restores the list —
  // see EditorTarget's doc comment in AutomationsPanel.tsx.
  describe('form overlay', () => {
    it('opens a fresh form via "New automation" in the header, in create mode', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new'));

      expect(await screen.findByTestId('automation-form')).toBeInTheDocument();
      expect(props.getDefinition).not.toHaveBeenCalled();
      // The list is gone while the form is open.
      expect(screen.queryByTestId('automations-panel-list')).not.toBeInTheDocument();
    });

    it('opens the empty-state "New automation" affordance the same way', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new-empty'));
      expect(await screen.findByTestId('automation-form')).toBeInTheDocument();
    });

    it('opens an existing definition via its row\'s Edit button, loading and showing that definition', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', name: 'PR reviewer' })]);
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-edit-d1'));

      await waitFor(() => expect(props.getDefinition).toHaveBeenCalledWith('d1'));
      expect(await screen.findByTestId('automation-form-name')).toHaveValue('PR reviewer');
    });

    it('Cancel returns to the list without saving', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new'));
      await screen.findByTestId('automation-form');
      await user.click(screen.getByTestId('automation-form-cancel'));

      expect(screen.queryByTestId('automation-form')).not.toBeInTheDocument();
      expect(await screen.findByTestId('automations-panel-list')).toBeInTheDocument();
      expect(props.applyDefinition).not.toHaveBeenCalled();
    });

    it('Save applies the loaded definition\'s expected_id/expected_revision, then returns to the list', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', name: 'PR reviewer' })]);
      props.getDefinition.mockResolvedValue({
        specYaml: '',
        specJson: manualSpecJson(),
        definition: makeDefinition({ id: 'd1', revision: 3 }),
      });
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-edit-d1'));
      await waitFor(() => expect(screen.getByTestId('automation-form-name')).toHaveValue('PR reviewer'));

      await user.click(screen.getByTestId('automation-form-save'));

      await waitFor(() => expect(props.applyDefinition).toHaveBeenCalledTimes(1));
      const [, expectedId, expectedRevision] = props.applyDefinition.mock.calls[0];
      expect(expectedId).toBe('d1');
      expect(expectedRevision).toBe(3);

      await waitFor(() => expect(screen.queryByTestId('automation-form')).not.toBeInTheDocument());
      expect(await screen.findByTestId('automations-panel-list')).toBeInTheDocument();
    });

    // D6: the panel refetches definitions on the automations_changed broadcast
    // (changedTick), which can fire while the user has the form open. That
    // refetch must update the list only — the open form is never remounted
    // or reset by the broadcast, only by explicit navigation (a fresh key).
    it('D6: a changedTick bump while the form is open refetches the list but does not remount the open form', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', name: 'PR reviewer' })]);
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-edit-d1'));
      await waitFor(() => expect(props.getDefinition).toHaveBeenCalledTimes(1));
      const nameField = await screen.findByTestId('automation-form-name');
      await user.clear(nameField);
      await user.type(nameField, 'still typing');

      const fetchCallsBefore = props.fetchDefinitions.mock.calls.length;
      useAutomationsStore.getState().bumpChanged();

      // The list-driving fetch re-runs...
      await waitFor(() => expect(props.fetchDefinitions.mock.calls.length).toBeGreaterThan(fetchCallsBefore));
      // ...but the form was never reloaded and the typed text survives.
      expect(props.getDefinition).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId('automation-form')).toBeInTheDocument();
      expect(screen.getByTestId('automation-form-name')).toHaveValue('still typing');
    });
  });
});
