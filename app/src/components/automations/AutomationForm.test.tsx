import { describe, it, expect, vi } from 'vitest';
import { act, render, screen, waitFor, userEvent } from '../../test/utils';
import { AutomationForm } from './AutomationForm';
import { AutomationDefinitionSummary } from '../../types/generated';
import { AutomationFormValues, formValuesToSpec, specJSONString } from './automationFormModel';
import { LAUNCH_CATALOG } from './launchCatalog';

function makeDefinition(overrides: Partial<AutomationDefinitionSummary> & { id: string }): AutomationDefinitionSummary {
  return {
    name: 'Reviewer',
    enabled: true,
    revision: 1,
    trigger_type: 'manual',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as AutomationDefinitionSummary;
}

function manualValues(overrides: Partial<AutomationFormValues> = {}): AutomationFormValues {
  const firstModel = LAUNCH_CATALOG.codex.models[0];
  return {
    name: 'Original',
    id: 'original',
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
}

function manualSpecJson(overrides: Partial<AutomationFormValues> = {}): string {
  return specJSONString(manualValues(overrides));
}

function githubValues(): AutomationFormValues {
  return {
    name: 'Reviewer',
    id: 'reviewer',
    idCustomized: true,
    trigger: 'github_review_requested',
    scheduleCron: '',
    continuity: 'fresh',
    catchUp: '',
    repositoriesInclude: ['github.com/acme/widgets'],
    repositoriesExclude: [],
    agent: 'claude',
    model: 'sonnet',
    effort: 'medium',
    executable: '',
    directoryPath: '',
    repositoryOverrides: [{ repository: 'github.com/acme/widgets', path: '/home/user/widgets' }],
    prompt: 'Review the PR',
  };
}

function githubSpecJson(): string {
  return specJSONString(githubValues());
}

function baseProps() {
  return {
    definitionId: null as string | null,
    getDefinition: vi.fn().mockResolvedValue({ specJson: manualSpecJson() }),
    applyDefinition: vi.fn().mockResolvedValue({ definition: makeDefinition({ id: 'new-automation', revision: 1 }) }),
    deleteDefinition: vi.fn().mockResolvedValue(undefined),
    setEnabled: vi.fn().mockResolvedValue(undefined),
    onCancel: vi.fn(),
    onSaved: vi.fn(),
    onDeleted: vi.fn(),
  };
}

describe('AutomationForm', () => {
  it('create: name auto-derives the id until customized', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationForm {...props} />);

    const nameInput = screen.getByTestId('automation-form-name');
    const idInput = screen.getByTestId('automation-form-id') as HTMLInputElement;

    await user.type(nameInput, 'Nightly Sync');
    expect(idInput.value).toBe('nightly-sync');

    await user.click(screen.getByTestId('automation-form-id-customize'));
    await user.clear(idInput);
    await user.type(idInput, 'custom-id');

    await user.type(nameInput, ' more');
    expect(idInput.value).toBe('custom-id');
  });

  it('create + save: fills valid manual values, save calls applyDefinition with the expected spec', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationForm {...props} />);

    await user.type(screen.getByTestId('automation-form-name'), 'My automation');
    await user.type(screen.getByTestId('automation-form-directory-path'), '/tmp/work');
    await user.type(screen.getByTestId('automation-form-prompt'), 'Do the work');

    await user.click(screen.getByTestId('automation-form-save'));

    await waitFor(() => expect(props.applyDefinition).toHaveBeenCalledTimes(1));
    const [specJson, expectedId, expectedRevision] = props.applyDefinition.mock.calls[0];
    expect(expectedId).toBe('');
    expect(expectedRevision).toBe(0);

    const firstModel = LAUNCH_CATALOG.codex.models[0];
    const expectedValues: AutomationFormValues = {
      name: 'My automation',
      id: 'my-automation',
      idCustomized: false,
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
    };
    expect(JSON.parse(specJson)).toEqual(formValuesToSpec(expectedValues));
  });

  it('edit: loads a github spec into fields and saves with the loaded id/revision', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValue({
      specJson: githubSpecJson(),
      definition: makeDefinition({ id: 'd1', revision: 7 }),
    });
    render(<AutomationForm {...props} />);

    await waitFor(() => expect(props.getDefinition).toHaveBeenCalledWith('d1'));
    expect(await screen.findByTestId('automation-form-repositories-include-chip-0')).toHaveTextContent(
      'github.com/acme/widgets',
    );
    expect(screen.getByTestId('automation-form-model')).toHaveValue('sonnet');

    await user.click(screen.getByTestId('automation-form-save'));
    await waitFor(() => expect(props.applyDefinition).toHaveBeenCalledTimes(1));
    const [, expectedId, expectedRevision] = props.applyDefinition.mock.calls[0];
    expect(expectedId).toBe('d1');
    expect(expectedRevision).toBe(7);
  });

  it('blur validation: the required-prompt error appears only after blur and clears on retype', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationForm {...props} />);

    const promptField = screen.getByTestId('automation-form-prompt');
    expect(screen.queryByTestId('automation-form-error-prompt')).not.toBeInTheDocument();

    await user.click(promptField);
    await user.tab();

    expect(await screen.findByTestId('automation-form-error-prompt')).toHaveTextContent('A prompt is required.');

    await user.type(promptField, 'Review it');
    await waitFor(() => expect(screen.queryByTestId('automation-form-error-prompt')).not.toBeInTheDocument());
  });

  it('revision_conflict shows a stale banner with Reload; Reload re-fetches and repopulates', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValueOnce({
      specJson: manualSpecJson({ name: 'Original' }),
      definition: makeDefinition({ id: 'd1', revision: 3 }),
    });
    props.applyDefinition.mockRejectedValueOnce(
      Object.assign(new Error('automation definition changed elsewhere — reload before saving'), {
        code: 'revision_conflict',
      }),
    );
    render(<AutomationForm {...props} />);

    await waitFor(() =>
      expect((screen.getByTestId('automation-form-name') as HTMLInputElement).value).toBe('Original'),
    );

    await user.click(screen.getByTestId('automation-form-save'));
    expect(await screen.findByTestId('automation-form-stale-banner')).toBeInTheDocument();
    expect(screen.getByTestId('automation-form-reload')).toBeInTheDocument();

    props.getDefinition.mockResolvedValueOnce({
      specJson: manualSpecJson({ name: 'Changed elsewhere' }),
      definition: makeDefinition({ id: 'd1', revision: 4 }),
    });
    await user.click(screen.getByTestId('automation-form-reload'));

    await waitFor(() =>
      expect((screen.getByTestId('automation-form-name') as HTMLInputElement).value).toBe('Changed elsewhere'),
    );
    expect(screen.queryByTestId('automation-form-stale-banner')).not.toBeInTheDocument();
  });

  it('id_collision renders the error on the id field, not a stale banner', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.applyDefinition.mockRejectedValueOnce(Object.assign(new Error('id already exists'), { code: 'id_collision' }));
    render(<AutomationForm {...props} />);

    await user.type(screen.getByTestId('automation-form-name'), 'Dup');
    await user.type(screen.getByTestId('automation-form-directory-path'), '/tmp/x');
    await user.type(screen.getByTestId('automation-form-prompt'), 'Do it');

    await user.click(screen.getByTestId('automation-form-save'));

    expect(await screen.findByTestId('automation-form-error-id')).toHaveTextContent('id already exists');
    expect(screen.queryByTestId('automation-form-stale-banner')).not.toBeInTheDocument();
  });

  it('switching to scheduled reveals cron/catch-up controls; saving without a catch-up choice is blocked', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationForm {...props} />);

    await user.click(screen.getByTestId('automation-form-trigger-scheduled'));
    expect(screen.getByTestId('automation-form-cron')).toBeInTheDocument();
    expect(screen.getByTestId('automation-form-catchup-skip')).toBeInTheDocument();

    await user.type(screen.getByTestId('automation-form-name'), 'Nightly');
    await user.type(screen.getByTestId('automation-form-cron'), '0 9 * * *');
    await user.type(screen.getByTestId('automation-form-directory-path'), '/tmp/x');
    await user.type(screen.getByTestId('automation-form-prompt'), 'Do it');

    await user.click(screen.getByTestId('automation-form-save'));

    expect(await screen.findByTestId('automation-form-error-catchUp')).toHaveTextContent(
      'Choose what happens to missed runs.',
    );
    expect(props.applyDefinition).not.toHaveBeenCalled();
  });

  it('delete arms on first click, deletes on second, and disarms on an outside click', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValue({ specJson: manualSpecJson(), definition: makeDefinition({ id: 'd1', revision: 1 }) });
    render(<AutomationForm {...props} />);

    await screen.findByTestId('automation-form-name');

    await user.click(screen.getByTestId('automation-form-delete'));
    expect(screen.getByText('Confirm delete')).toBeInTheDocument();
    expect(props.deleteDefinition).not.toHaveBeenCalled();

    await user.click(screen.getByTestId('automation-form-cancel'));
    expect(screen.queryByText('Confirm delete')).not.toBeInTheDocument();
    expect(props.deleteDefinition).not.toHaveBeenCalled();

    await user.click(screen.getByTestId('automation-form-delete'));
    await user.click(screen.getByTestId('automation-form-delete'));
    await waitFor(() => expect(props.deleteDefinition).toHaveBeenCalledTimes(1));
    expect(props.deleteDefinition).toHaveBeenCalledWith('d1');
    await waitFor(() => expect(props.onDeleted).toHaveBeenCalledTimes(1));
  });

  it('enabled toggle reflects state, flips on click, and is absent while creating', async () => {
    const user = userEvent.setup();
    const createProps = baseProps();
    render(<AutomationForm {...createProps} />);
    expect(screen.queryByTestId('automation-form-enabled')).not.toBeInTheDocument();

    const editProps = baseProps();
    editProps.definitionId = 'd1';
    editProps.getDefinition.mockResolvedValue({
      specJson: manualSpecJson(),
      definition: makeDefinition({ id: 'd1', revision: 1, enabled: true }),
    });
    render(<AutomationForm {...editProps} />);

    const toggle = await screen.findByTestId('automation-form-enabled');
    expect(toggle).toHaveAttribute('aria-checked', 'true');

    await user.click(toggle);
    await waitFor(() => expect(editProps.setEnabled).toHaveBeenCalledWith('d1', false));
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'));
  });

  it('compiled sentence reflects the github fixture and updates on trigger switch', async () => {
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValue({
      specJson: githubSpecJson(),
      definition: makeDefinition({ id: 'd1', revision: 1 }),
    });
    render(<AutomationForm {...props} />);

    const sentence = await screen.findByTestId('automation-form-sentence');
    await waitFor(() => expect(sentence).toHaveTextContent('fresh worktree at the PR head'));
    expect(sentence).toHaveTextContent('sonnet');

    const user = userEvent.setup();
    await user.click(screen.getByTestId('automation-form-trigger-manual'));
    await waitFor(() => expect(sentence).toHaveTextContent('Run now'));
  });

  it('disables inputs while applyDefinition is pending and calls onSaved on resolve', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    let resolveApply: ((result: { definition: AutomationDefinitionSummary }) => void) | undefined;
    props.applyDefinition.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveApply = resolve;
        }),
    );
    render(<AutomationForm {...props} />);

    await user.type(screen.getByTestId('automation-form-name'), 'Nightly');
    await user.type(screen.getByTestId('automation-form-directory-path'), '/tmp/x');
    await user.type(screen.getByTestId('automation-form-prompt'), 'Do it');

    await user.click(screen.getByTestId('automation-form-save'));
    await waitFor(() => expect(props.applyDefinition).toHaveBeenCalledTimes(1));

    expect(screen.getByTestId('automation-form-name')).toBeDisabled();
    expect(screen.getByTestId('automation-form-save')).toHaveTextContent('Saving…');

    act(() => {
      resolveApply?.({ definition: makeDefinition({ id: 'new-automation', revision: 1 }) });
    });
    await waitFor(() => expect(props.onSaved).toHaveBeenCalled());
  });
});
