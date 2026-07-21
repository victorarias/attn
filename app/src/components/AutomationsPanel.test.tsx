import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, userEvent } from '../test/utils';
import { AutomationsPanel } from './AutomationsPanel';
import { useAutomationsStore } from '../store/automations';
import { AutomationActionTimeoutError } from '../hooks/useDaemonSocket';
import { AutomationDefinitionSummary, AutomationRunSummary } from '../types/generated';

// getByDisplayValue/findByDisplayValue apply the default TL normalizer (trim
// + collapse whitespace) to the DOM value being matched but NOT to the
// matcher string itself, so a multi-line YAML value with a trailing newline
// can never match a literal expected string. Passing an identity normalizer
// restores exact literal matching against the YAML buffer.
const EXACT_VALUE = { normalizer: (text: string) => text };

// AutomationEditor is CodeMirror-backed (via AutomationYamlEditor), which
// cannot mount under happy-dom — same constraint as LiveMarkdownEditor (see
// NotebookBrowser.test.tsx). The real editing surface is covered by the
// Playwright harness; here the leaf CodeMirror component is mocked to a
// controlled textarea so these tests can drive the panel/editor orchestration
// (open targets, D6's no-stomp rule, save/cancel wiring) without a browser.
vi.mock('./automations/AutomationYamlEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react');
  return {
    AutomationYamlEditor: forwardRef(function MockAutomationYamlEditor(
      { value, onChange, ariaLabel }: { value: string; onChange: (value: string) => void; ariaLabel?: string },
      ref: React.Ref<{ applyExternalContent: (next: string) => void; focus: () => void }>,
    ) {
      useImperativeHandle(ref, () => ({
        applyExternalContent: (next: string) => onChange(next),
        focus: () => {},
      }), [onChange]);
      return (
        <textarea aria-label={ariaLabel ?? 'Automation definition'} value={value} onChange={(event) => onChange(event.target.value)} />
      );
    }),
  };
});

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

function baseProps() {
  return {
    isOpen: true,
    onClose: vi.fn(),
    fetchDefinitions: vi.fn().mockResolvedValue([]),
    fetchRuns: vi.fn().mockResolvedValue([]),
    setEnabled: vi.fn().mockResolvedValue(undefined),
    runNow: vi.fn().mockResolvedValue(undefined),
    getDefinition: vi.fn().mockResolvedValue({ specYaml: 'id: new-automation\nname: New automation\n' }),
    validateDefinition: vi.fn().mockResolvedValue(undefined),
    applyDefinition: vi.fn().mockResolvedValue({
      definition: makeDefinition({ id: 'd1', revision: 2 }),
      specYaml: 'id: d1\n',
    }),
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

  describe('editor', () => {
    it('opens a fresh template via "New automation" in the header, requesting definition id ""', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new'));

      await waitFor(() => expect(props.getDefinition).toHaveBeenCalledWith(''));
      expect(await screen.findByTestId('automation-editor')).toBeInTheDocument();
      expect(screen.getByText('New automation')).toBeInTheDocument();
      // The list is gone while the editor is open.
      expect(screen.queryByTestId('automations-panel-list')).not.toBeInTheDocument();
    });

    it('opens the empty-state "New automation" affordance the same way', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new-empty'));
      await waitFor(() => expect(props.getDefinition).toHaveBeenCalledWith(''));
      expect(await screen.findByTestId('automation-editor')).toBeInTheDocument();
    });

    it('opens an existing definition via its row\'s Edit button, requesting that id', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1', name: 'PR reviewer' })]);
      props.getDefinition.mockResolvedValue({ specYaml: 'id: d1\nname: PR reviewer\n', definition: makeDefinition({ id: 'd1', revision: 3 }) });
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-edit-d1'));

      await waitFor(() => expect(props.getDefinition).toHaveBeenCalledWith('d1'));
      expect(await screen.findByTestId('automation-editor')).toBeInTheDocument();
      expect(screen.getByText('Edit automation')).toBeInTheDocument();
      expect(await screen.findByDisplayValue('id: d1\nname: PR reviewer\n', EXACT_VALUE)).toBeInTheDocument();
    });

    it('Cancel returns to the list without saving', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new'));
      await screen.findByTestId('automation-editor');
      await user.click(screen.getByTestId('automation-editor-cancel'));

      expect(screen.queryByTestId('automation-editor')).not.toBeInTheDocument();
      expect(await screen.findByTestId('automations-panel-list')).toBeInTheDocument();
      expect(props.applyDefinition).not.toHaveBeenCalled();
    });

    it('Save applies the buffer with expected_id/expected_revision from the loaded definition, then returns to the list', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
      props.getDefinition.mockResolvedValue({ specYaml: 'id: d1\nname: PR reviewer\n', definition: makeDefinition({ id: 'd1', revision: 3 }) });
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-edit-d1'));
      const textarea = await screen.findByDisplayValue('id: d1\nname: PR reviewer\n', EXACT_VALUE);
      await user.clear(textarea);
      await user.type(textarea, 'id: d1\nname: PR reviewer v2\n');

      await user.click(screen.getByTestId('automation-editor-save'));

      await waitFor(() =>
        expect(props.applyDefinition).toHaveBeenCalledWith('id: d1\nname: PR reviewer v2\n', 'd1', 3),
      );
      await waitFor(() => expect(screen.queryByTestId('automation-editor')).not.toBeInTheDocument());
    });

    it('a Save rejection surfaces the error prominently and keeps the editor open with the buffer intact', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.applyDefinition.mockRejectedValue(new Error('changed elsewhere — reload'));
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new'));
      await screen.findByTestId('automation-editor');
      await user.click(screen.getByTestId('automation-editor-save'));

      await waitFor(() =>
        expect(screen.getByTestId('automation-editor-save-error')).toHaveTextContent('changed elsewhere — reload'),
      );
      expect(screen.getByTestId('automation-editor')).toBeInTheDocument();
    });

    // D6: the panel refetches definitions on the automations_changed broadcast
    // (changedTick), which can fire while the user is mid-edit. That refetch
    // must update the list only — an open editor buffer is replaced ONLY on
    // explicit load (opening the editor) or explicit Reload, never by the
    // broadcast. This is the regression the design doc calls out by name.
    it('D6: a changedTick bump while the editor is open refetches the list but does not touch the open buffer', async () => {
      const user = userEvent.setup();
      const props = baseProps();
      props.fetchDefinitions.mockResolvedValue([makeDefinition({ id: 'd1' })]);
      render(<AutomationsPanel {...props} />);

      await user.click(await screen.findByTestId('automation-new'));
      const textarea = await screen.findByDisplayValue('id: new-automation\nname: New automation\n', EXACT_VALUE);
      await user.clear(textarea);
      await user.type(textarea, 'id: unsaved-work\nname: still typing\n');
      expect(props.getDefinition).toHaveBeenCalledTimes(1);

      const fetchCallsBefore = props.fetchDefinitions.mock.calls.length;
      useAutomationsStore.getState().bumpChanged();

      // The list-driving fetch re-runs...
      await waitFor(() => expect(props.fetchDefinitions.mock.calls.length).toBeGreaterThan(fetchCallsBefore));
      // ...but the editor was never reloaded and the typed text survives.
      expect(props.getDefinition).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId('automation-editor')).toBeInTheDocument();
      expect(screen.getByDisplayValue('id: unsaved-work\nname: still typing\n', EXACT_VALUE)).toBeInTheDocument();
    });
  });
});
