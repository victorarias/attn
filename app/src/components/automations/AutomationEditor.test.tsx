import { describe, it, expect, vi } from 'vitest';
import { act, render, screen, waitFor, userEvent } from '../../test/utils';
import { AutomationEditor } from './AutomationEditor';
import { AutomationDefinitionSummary } from '../../types/generated';
import { getAutomationEditorAutomationHandle } from './automationEditorAutomation';

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
      {
        value,
        onChange,
        ariaLabel,
        readOnly,
      }: { value: string; onChange: (value: string) => void; ariaLabel?: string; readOnly?: boolean },
      ref: React.Ref<{ applyExternalContent: (next: string) => void; focus: () => void; getDocText: () => string }>,
    ) {
      // Mirrors the real imperative handle: pushing external content reports
      // it through onChange, the way a CodeMirror dispatch would, so
      // AutomationEditor's controlled value tracks it — exactly what Reload
      // relies on. getDocText mirrors the controlled `value` prop directly:
      // unlike the real CodeMirror doc it can never lag behind it, since this
      // mock has no buffer of its own independent of `value`.
      //
      // The `next === value` guard is load-bearing, not tidiness: the real
      // applyExternalContent computes a MINIMAL edit and dispatches nothing
      // when the text is unchanged, so no onChange fires. A mock that always
      // called onChange would paper over exactly the case AutomationEditor's
      // setText calls handleChange for, and any test of that case would pass
      // against a component that had dropped the call.
      useImperativeHandle(ref, () => ({
        applyExternalContent: (next: string) => {
          if (next === value) return;
          onChange(next);
        },
        focus: () => {},
        getDocText: () => value,
      }), [onChange, value]);
      // Mirrors AutomationYamlEditor's `readOnly` prop with the native
      // textarea attribute of the same name: user-event's isEditable() check
      // respects `readOnly` on a textarea (like the real CodeMirror content
      // DOM's contenteditable=false under `editable={false}`) and refuses to
      // type into it, so a Defect D test driving this mock through
      // user.type() exercises the same "typing during save is blocked"
      // behavior the real editor provides. Load-bearing, not decoration: a
      // mock that ignored `readOnly` would let a Defect D regression test
      // pass whether or not AutomationEditor actually threads the prop.
      return (
        <textarea
          aria-label={ariaLabel ?? 'Automation definition'}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          readOnly={readOnly}
        />
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

  // Defect C: handleValidate has no staleness guard, so a Validate resolving
  // after the buffer already changed underneath it can re-assert a verdict
  // about text the daemon never saw. The previous test proves the DISPLAYED
  // result is cleared the instant the user types; this one proves the
  // in-flight request's own resolution can't undo that clear when it finally
  // arrives — the actual race the bug report describes.
  it('a stale Validate resolution does not re-assert a verdict after the buffer changed underneath it', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    let resolveValidate: (() => void) | undefined;
    props.validateDefinition.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveValidate = resolve;
        }),
    );
    render(<AutomationEditor {...props} />);

    const textarea = await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-validate'));
    await waitFor(() => expect(props.validateDefinition).toHaveBeenCalledTimes(1));

    // Edit while the request is still outstanding — its eventual answer will
    // describe a buffer that no longer exists.
    await user.type(textarea, 'x');
    expect(screen.queryByTestId('automation-editor-validation-ok')).not.toBeInTheDocument();

    // The stale request finally resolves...
    act(() => resolveValidate?.());
    await waitFor(() => expect(screen.getByTestId('automation-editor-validate')).not.toBeDisabled());

    // ...but "Looks good." must never appear against the edited buffer.
    expect(screen.queryByTestId('automation-editor-validation-ok')).not.toBeInTheDocument();
  });

  // The second half of Defect C's fix: handleChange must not clear a
  // 'checking' state just because the buffer changed, or the button's
  // disabled gate re-opens mid-flight and a second click can stack a second
  // request on top of the first — the two could then resolve out of order.
  it('Validate stays disabled while a request is outstanding, even after the buffer is edited', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    let resolveValidate: (() => void) | undefined;
    props.validateDefinition.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveValidate = resolve;
        }),
    );
    render(<AutomationEditor {...props} />);

    const textarea = await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-validate'));
    await waitFor(() => expect(props.validateDefinition).toHaveBeenCalledTimes(1));

    await user.type(textarea, 'x');
    expect(screen.getByTestId('automation-editor-validate')).toBeDisabled();

    // A click on a disabled button fires no click event — browsers don't
    // dispatch one, and neither does user-event — so this cannot stack a
    // second validateDefinition call.
    await user.click(screen.getByTestId('automation-editor-validate'));
    expect(props.validateDefinition).toHaveBeenCalledTimes(1);

    act(() => resolveValidate?.());
    await waitFor(() => expect(screen.getByTestId('automation-editor-validate')).not.toBeDisabled());
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

  // Defect D: handleSave captures `value` at click time but nothing froze the
  // buffer for the request's duration, so characters typed while Save was
  // outstanding were written into `value` and then silently discarded when
  // onSaved unmounted the editor. Locking the buffer (readOnly while saving)
  // is the fix; this proves both halves — the visible cue and the actual
  // block on further edits, not just the label on the Save button.
  it('locks the buffer against edits while Save is in flight, so typed characters are not silently discarded', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    let resolveApply:
      | ((result: { definition: AutomationDefinitionSummary; specYaml: string; revision: number }) => void)
      | undefined;
    props.applyDefinition.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveApply = resolve;
        }),
    );
    render(<AutomationEditor {...props} />);

    const textarea = await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    await user.click(screen.getByTestId('automation-editor-save'));
    await waitFor(() => expect(props.applyDefinition).toHaveBeenCalledTimes(1));

    expect(screen.getByTestId('automation-editor-locked-hint')).toBeInTheDocument();

    // Typing while locked must not change the buffer — this is the actual
    // guarantee, not just the visible hint above. Without readOnly threaded
    // through to the buffer, this keystroke would silently succeed and the
    // character would vanish, unsent, when the editor unmounts on success.
    await user.type(textarea, 'x');
    expect(textarea).toHaveValue('id: new-automation\n');

    act(() =>
      resolveApply?.({
        definition: makeDefinition({ id: 'new-automation', revision: 1 }),
        specYaml: 'id: new-automation\n',
        revision: 1,
      }),
    );
    await waitFor(() => expect(props.onSaved).toHaveBeenCalled());
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

  // While creating, Reload would re-fetch the starter template (getDefinition
  // is called with '') and overwrite the draft being typed, with nothing to
  // recover — the D5 stale-revision refusal it exists for cannot happen before
  // the definition is persisted. So it must not be reachable in that state.
  it('does not offer Reload while creating, so a draft cannot be discarded', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    expect(screen.queryByTestId('automation-editor-reload')).not.toBeInTheDocument();

    // Editing an existing definition still gets it.
    const editProps = baseProps();
    editProps.definitionId = 'd1';
    editProps.getDefinition.mockResolvedValueOnce({ specYaml: 'id: d1\n', revision: 1 });
    render(<AutomationEditor {...editProps} />);

    await screen.findByDisplayValue('id: d1\n', EXACT_VALUE);
    expect(screen.getAllByTestId('automation-editor-reload')).toHaveLength(1);

    // Guard against the assertion above passing vacuously if the create-mode
    // editor stopped rendering entirely.
    expect(screen.getAllByTestId('automation-editor')).toHaveLength(2);
    await user.click(screen.getAllByTestId('automation-editor-close')[0]);
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });
});

// The UI automation bridge's automation_editor_* verbs read this handle
// instead of scraping the DOM — see automationEditorAutomation.ts's doc
// comment for why (CodeMirror virtualizes long documents, and this seam is
// the evidence source for a save/reload comment-preservation proof).
describe('AutomationEditor automation bridge handle', () => {
  it('registers the handle while mounted and clears it on unmount', async () => {
    const props = baseProps();
    const { unmount } = render(<AutomationEditor {...props} />);

    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);
    expect(getAutomationEditorAutomationHandle()).not.toBeNull();

    unmount();
    expect(getAutomationEditorAutomationHandle()).toBeNull();
  });

  it('getState reflects create vs edit mode, reloadOffered, and a validation error message', async () => {
    const user = userEvent.setup();
    const createProps = baseProps();
    const { unmount: unmountCreate } = render(<AutomationEditor {...createProps} />);
    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);

    expect(getAutomationEditorAutomationHandle()?.getState()).toMatchObject({
      present: true,
      mode: 'create',
      definitionId: null,
      reloadOffered: false,
    });
    unmountCreate();

    const editProps = baseProps();
    editProps.definitionId = 'd1';
    editProps.validateDefinition.mockRejectedValue(
      new Error('trigger.schedule.cron: invalid cron expression'),
    );
    editProps.getDefinition.mockResolvedValueOnce({ specYaml: 'id: d1\n', revision: 7 });
    render(<AutomationEditor {...editProps} />);
    await screen.findByDisplayValue('id: d1\n', EXACT_VALUE);

    expect(getAutomationEditorAutomationHandle()?.getState()).toMatchObject({
      present: true,
      mode: 'edit',
      definitionId: 'd1',
      revision: 7,
      reloadOffered: true,
    });

    await user.click(screen.getByTestId('automation-editor-validate'));
    await screen.findByTestId('automation-editor-validation-error');

    expect(getAutomationEditorAutomationHandle()?.getState().validation).toEqual({
      state: 'error',
      message: 'trigger.schedule.cron: invalid cron expression',
    });
  });

  // The invariant setText must uphold: it's equivalent to the user replacing
  // the buffer by typing, so it goes through the same path as onChange — a
  // stale "Looks good." result is gone, and a subsequent Save sends exactly
  // what was set, not what was loaded or previously typed.
  it('setText clears a stale validation result and Save carries the newly set text', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);
    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);

    await user.click(screen.getByTestId('automation-editor-validate'));
    await screen.findByTestId('automation-editor-validation-ok');

    const handle = getAutomationEditorAutomationHandle();
    expect(handle).not.toBeNull();
    act(() => {
      handle?.setText('id: new-automation\nname: replaced via bridge\n');
    });

    expect(screen.queryByTestId('automation-editor-validation-ok')).not.toBeInTheDocument();
    await screen.findByDisplayValue('id: new-automation\nname: replaced via bridge\n', EXACT_VALUE);
    // Re-read through the module getter, not the captured handle: the
    // registration effect re-runs (and re-registers) on this state change,
    // same gotcha as SettingsModal's selectSection (see SettingsModal.test.tsx).
    expect(getAutomationEditorAutomationHandle()?.getState().text).toBe(
      'id: new-automation\nname: replaced via bridge\n',
    );

    await user.click(screen.getByTestId('automation-editor-save'));
    await waitFor(() =>
      expect(props.applyDefinition).toHaveBeenCalledWith(
        'id: new-automation\nname: replaced via bridge\n',
        '',
        0,
      ),
    );
  });

  // The case the test above cannot reach, and the only reason setText calls
  // handleChange in addition to pushing the text into CodeMirror: when the
  // new text equals the current buffer, applyExternalContent computes an
  // empty edit and dispatches nothing, so no onChange fires. Without the
  // explicit handleChange, a "Looks good." from a previous Validate would
  // survive a setText — telling the harness (and a user) that text was
  // validated when the validation it is showing predates the write.
  it('setText clears a stale validation result even when the text is unchanged', async () => {
    const user = userEvent.setup();
    const props = baseProps();
    render(<AutomationEditor {...props} />);
    await screen.findByDisplayValue('id: new-automation\n', EXACT_VALUE);

    await user.click(screen.getByTestId('automation-editor-validate'));
    await screen.findByTestId('automation-editor-validation-ok');

    act(() => {
      // Byte-identical to what is already in the buffer.
      getAutomationEditorAutomationHandle()?.setText('id: new-automation\n');
    });

    expect(screen.queryByTestId('automation-editor-validation-ok')).not.toBeInTheDocument();
    expect(getAutomationEditorAutomationHandle()?.getState().validation.state).toBe('idle');
  });
});
