import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor, userEvent } from '../../test/utils';
import { AutomationEditor } from './AutomationEditor';
import { AutomationDefinitionSummary } from '../../types/generated';

// getByDisplayValue/findByDisplayValue apply the default TL normalizer (trim
// + collapse whitespace) to the DOM value being matched but NOT to the
// matcher string itself, so a multi-line YAML value with a trailing newline
// can never match a literal expected string — the actual value gets
// silently collapsed/trimmed before comparison while the expectation
// doesn't. Passing an identity normalizer restores exact literal matching,
// which is what these tests actually want against a YAML buffer.
const EXACT_VALUE = { normalizer: (text: string) => text };

// AutomationYamlEditor is CodeMirror-backed, which cannot mount under
// happy-dom (see AutomationsPanel.test.tsx / NotebookBrowser.test.tsx for the
// same constraint on the other two editors in this app). The real rendering
// and typing experience is covered by the Playwright harness; here the leaf
// component is mocked to a controlled textarea so this file can exercise
// AutomationEditor's load/validate/save/reload orchestration directly.
vi.mock('./AutomationYamlEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react');
  return {
    AutomationYamlEditor: forwardRef(function MockAutomationYamlEditor(
      { value, onChange, ariaLabel }: { value: string; onChange: (value: string) => void; ariaLabel?: string },
      ref: React.Ref<{ applyExternalContent: (next: string) => void; focus: () => void }>,
    ) {
      // Mirrors the real imperative handle: pushing external content reports
      // it through onChange, the way a CodeMirror dispatch would, so
      // AutomationEditor's controlled value tracks it — exactly what Reload
      // relies on.
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

function makeDefinition(overrides: Partial<AutomationDefinitionSummary> & { id: string }): AutomationDefinitionSummary {
  return {
    name: 'PR reviewer',
    enabled: true,
    revision: 1,
    trigger_type: 'manual',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as AutomationDefinitionSummary;
}

function baseProps() {
  return {
    definitionId: null as string | null,
    getDefinition: vi.fn().mockResolvedValue({ specYaml: 'id: new-automation\n', revision: 0 }),
    validateDefinition: vi.fn().mockResolvedValue(undefined),
    applyDefinition: vi.fn().mockResolvedValue({
      definition: makeDefinition({ id: 'new-automation', revision: 1 }),
      specYaml: 'id: new-automation\n',
      revision: 1,
    }),
    onCancel: vi.fn(),
    onSaved: vi.fn(),
  };
}

describe('AutomationEditor', () => {
  it('loads the starter template when definitionId is null', async () => {
    const props = baseProps();
    render(<AutomationEditor {...props} />);

    await waitFor(() => expect(props.getDefinition).toHaveBeenCalledWith(''));
    expect(await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE)).toBeInTheDocument();
    expect(screen.getByText('New automation')).toBeInTheDocument();
  });

  it('loads the definition_yaml for an existing id', async () => {
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValue({ specYaml: 'id: d1\nname: PR reviewer\n', revision: 5 });
    render(<AutomationEditor {...props} />);

    await waitFor(() => expect(props.getDefinition).toHaveBeenCalledWith('d1'));
    expect(await screen.findByDisplayValue('id: d1\nname: PR reviewer\n', EXACT_VALUE)).toBeInTheDocument();
    expect(screen.getByText('Edit automation')).toBeInTheDocument();
  });

  it('shows a load error instead of the buffer when getDefinition rejects', async () => {
    const props = baseProps();
    props.getDefinition.mockRejectedValue(new Error('daemon unreachable'));
    render(<AutomationEditor {...props} />);

    await waitFor(() =>
      expect(screen.getByTestId('automation-editor-load-error')).toHaveTextContent('daemon unreachable'),
    );
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('Validate calls validateDefinition with the current buffer and shows success', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-validate'));

    await waitFor(() => expect(props.validateDefinition).toHaveBeenCalledWith('id: new-automation\n'));
    expect(await screen.findByTestId('automation-editor-validation-ok')).toBeInTheDocument();
  });

  it('Validate shows the flat error message on rejection, verbatim, with no inline positioning attempted', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.validateDefinition.mockRejectedValue(new Error('trigger.schedule.cron: invalid cron expression'));
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-validate'));

    await waitFor(() =>
      expect(screen.getByTestId('automation-editor-validation-error')).toHaveTextContent(
        'trigger.schedule.cron: invalid cron expression',
      ),
    );
  });

  it('editing the buffer after a Validate result clears the stale result', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);

    const textarea = await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-validate'));
    await screen.findByTestId('automation-editor-validation-ok');

    await user.type(textarea, 'x');

    expect(screen.queryByTestId('automation-editor-validation-ok')).not.toBeInTheDocument();
  });

  it('Save applies with expected_id "" and expected_revision 0 when creating', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-save'));

    await waitFor(() =>
      expect(props.applyDefinition).toHaveBeenCalledWith('id: new-automation\n', '', 0),
    );
    await waitFor(() => expect(props.onSaved).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'new-automation', revision: 1 }),
    ));
  });

  it('Save applies with the loaded id/revision when editing', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValue({ specYaml: 'id: d1\n', revision: 7 });
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: d1\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-save'));

    await waitFor(() => expect(props.applyDefinition).toHaveBeenCalledWith('id: d1\n', 'd1', 7));
  });

  it('a Save rejection surfaces the error and does not call onSaved', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.applyDefinition.mockRejectedValue(new Error('id mismatch: would fork this definition'));
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-save'));

    await waitFor(() =>
      expect(screen.getByTestId('automation-editor-save-error')).toHaveTextContent(
        'id mismatch: would fork this definition',
      ),
    );
    expect(props.onSaved).not.toHaveBeenCalled();
  });

  it('Cancel calls onCancel without saving', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-cancel'));

    expect(props.onCancel).toHaveBeenCalledTimes(1);
    expect(props.applyDefinition).not.toHaveBeenCalled();
  });

  it('the header close button also calls onCancel', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-close'));

    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  // Reload is the recovery path after a stale-revision Save refusal (D5): it
  // re-fetches the definition and pushes it into the ALREADY-MOUNTED buffer
  // via the minimal-edit handle (applyExternalContent), rather than
  // unmounting/remounting the editor — which is what actually preserves
  // CodeMirror's scroll position and selection in the real component. This
  // test proves the orchestration calls getDefinition again and the new
  // content lands, without asserting on CodeMirror internals the mock
  // doesn't have.
  it('Reload re-fetches the definition and replaces the buffer without unmounting the editor', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValueOnce({ specYaml: 'id: d1\nname: v1\n', revision: 1 });
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: d1\nname: v1\n', EXACT_VALUE);

    props.getDefinition.mockResolvedValueOnce({ specYaml: 'id: d1\nname: v2\n', revision: 2 });
    await user.click(screen.getByTestId('automation-editor-reload'));

    await waitFor(() => expect(props.getDefinition).toHaveBeenCalledTimes(2));
    expect(await screen.findByDisplayValue('id: d1\nname: v2\n', EXACT_VALUE)).toBeInTheDocument();

    // The refreshed revision is what the next Save carries as expected_revision.
    await user.click(screen.getByTestId('automation-editor-save'));
    await waitFor(() => expect(props.applyDefinition).toHaveBeenCalledWith('id: d1\nname: v2\n', 'd1', 2));
  });

  it('a Reload failure surfaces its own error without clearing the buffer', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    props.definitionId = 'd1';
    props.getDefinition.mockResolvedValueOnce({ specYaml: 'id: d1\n', revision: 1 });
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: d1\n', EXACT_VALUE);

    props.getDefinition.mockRejectedValueOnce(new Error('daemon unreachable'));
    await user.click(screen.getByTestId('automation-editor-reload'));

    await waitFor(() =>
      expect(screen.getByTestId('automation-editor-reload-error')).toHaveTextContent('daemon unreachable'),
    );
    expect(screen.getByDisplayValue('id: d1\n', EXACT_VALUE)).toBeInTheDocument();
  });
});
