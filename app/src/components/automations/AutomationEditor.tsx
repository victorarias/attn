// Self-service automation YAML editor: load (create or edit share one path via
// getDefinition('') → starter template, D7 in the design), Validate, Save, Cancel.
//
// D6 (no-stomp): this component's buffer state is populated ONCE on mount from
// getDefinition, and thereafter only by the user's own typing or an explicit
// click of Reload. It does not subscribe to useAutomationsStore (the panel's
// automations_changed-driven list refetch lives entirely in AutomationsPanel),
// so there is no code path by which a background broadcast can replace text the
// user is mid-typing. AutomationsPanel is responsible for not remounting this
// component except when the user explicitly opens a different target (New /
// Edit), which is exactly "explicit load" — see AutomationsPanel.tsx.
import { useCallback, useEffect, useRef, useState } from 'react';
import { AutomationDefinitionSummary } from '../../types/generated';
import { AutomationYamlEditor, type AutomationYamlEditorHandle } from './AutomationYamlEditor';
import { setAutomationEditorAutomationHandle } from './automationEditorAutomation';
import './AutomationEditor.css';

export interface AutomationEditorProps {
  // null → create (getDefinition is called with '', which returns the starter
  // template at revision 0). Non-null → edit that definition.
  definitionId: string | null;
  getDefinition: (definitionId: string) => Promise<{ specYaml: string; revision: number }>;
  validateDefinition: (definitionYaml: string) => Promise<void>;
  applyDefinition: (
    definitionYaml: string,
    expectedId: string,
    expectedRevision: number,
  ) => Promise<{ definition: AutomationDefinitionSummary; specYaml: string; revision: number }>;
  onCancel: () => void;
  // Called once Save succeeds. The caller (AutomationsPanel) closes the editor
  // back to the list — canonical state (including the new/updated row) arrives
  // there via the daemon's automations_changed broadcast the apply also
  // triggers, same as every other mutation on this panel.
  onSaved: (definition: AutomationDefinitionSummary) => void;
}

type LoadStatus = 'loading' | 'ready' | 'load-error';
type ValidationState = { state: 'idle' | 'checking' | 'ok' | 'error'; message?: string };

export function AutomationEditor({
  definitionId,
  getDefinition,
  validateDefinition,
  applyDefinition,
  onCancel,
  onSaved,
}: AutomationEditorProps) {
  const editorRef = useRef<AutomationYamlEditorHandle>(null);

  const [value, setValue] = useState('');
  // The id used for expected_id on Save. Starts as the prop (null when
  // creating); a successful create's response tells us the id the daemon
  // actually persisted, so subsequent Saves in this same buffer become edits
  // instead of re-creating (D4 in the design).
  const [loadedId, setLoadedId] = useState<string | null>(definitionId);
  const [revision, setRevision] = useState(0);

  const [status, setStatus] = useState<LoadStatus>('loading');
  const [loadError, setLoadError] = useState<string | null>(null);

  const [reloading, setReloading] = useState(false);
  const [reloadError, setReloadError] = useState<string | null>(null);

  const [validation, setValidation] = useState<ValidationState>({ state: 'idle' });
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Mount-only initial load. Deliberately NOT re-run on definitionId churn —
  // AutomationsPanel remounts this component (fresh key) whenever the user
  // opens a different target, so "mount" already means "explicit load" (D6).
  useEffect(() => {
    let cancelled = false;
    getDefinition(definitionId ?? '')
      .then((result) => {
        if (cancelled) return;
        setValue(result.specYaml);
        setRevision(result.revision);
        setStatus('ready');
      })
      .catch((error) => {
        if (cancelled) return;
        setLoadError(error instanceof Error ? error.message : 'Failed to load automation definition');
        setStatus('load-error');
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Defect C guard: a dispatched Validate request has no cancel — it's a WS
  // request/result pair (handleAutomationValidateWS), not fetch, so
  // AbortController doesn't apply, and the daemon-side git shell-outs it
  // triggers keep running regardless of what the buffer does afterward.
  // Bumping this token on every dispatch AND on every edit gives the resolve
  // handlers below a generation identity to check against — same shape as
  // the daemon's classifierObservation convention (capture the observation
  // identity before the async call, reject stale results).
  const validateTokenRef = useRef(0);

  const handleChange = useCallback((next: string) => {
    setValue(next);
    // A prior Validate/Save result no longer describes the current text.
    // Bump the token so an in-flight Validate's eventual answer is
    // recognized as stale once it arrives (Defect C): typing invalidates it
    // because the verdict is about text the daemon never saw, and
    // re-asserting it here would be actively misleading either direction —
    // "Looks good." against a buffer that would now be rejected, or a
    // rejection pointing at a line the user already fixed. Deliberately do
    // NOT clear a 'checking' state here: the request itself is still
    // outstanding, and the Validate button's disabled gate must track that,
    // not the buffer — otherwise a second click here could stack a second
    // request on top of the first and the two could resolve out of order.
    // handleValidate's resolve handlers below are what move validation out
    // of 'checking', once the one outstanding request actually settles.
    validateTokenRef.current += 1;
    setValidation((prev) => (prev.state === 'idle' || prev.state === 'checking' ? prev : { state: 'idle' }));
    setSaveError(null);
  }, []);

  const handleValidate = useCallback(() => {
    const token = ++validateTokenRef.current;
    setValidation({ state: 'checking' });
    validateDefinition(value)
      .then(() => {
        // Superseded by an edit since dispatch: the text this describes is
        // gone, so fall back to idle rather than assert a verdict about a
        // buffer nobody can see anymore.
        setValidation(validateTokenRef.current === token ? { state: 'ok' } : { state: 'idle' });
      })
      .catch((error) => {
        setValidation(
          validateTokenRef.current === token
            ? { state: 'error', message: error instanceof Error ? error.message : 'Validation failed' }
            : { state: 'idle' },
        );
      });
  }, [validateDefinition, value]);

  // Defect D: handleSave captures `value` at click time, but nothing else in
  // the tree freezes the buffer for the request's duration —
  // handleAutomationApplyWS waits on the daemon's automationMu, held across a
  // full delivery (clone/fetch, agent spawn), bounded at 25s. AutomationYamlEditor
  // stays mounted and focused while `saving` is true, so a user who keeps
  // typing writes characters into `value` that were never part of what was
  // sent; on success onSaved unmounts the editor (AutomationsPanel's
  // setEditorTarget(null)) and those characters vanish with no warning. Fixed
  // by locking the buffer (readOnly, below) for the duration rather than
  // re-checking/warning on resolve: locking keeps "what was submitted" and
  // "what's on screen" identical throughout, so there's nothing to reconcile
  // or warn about after the fact.
  const handleSave = useCallback(() => {
    setSaving(true);
    setSaveError(null);
    applyDefinition(value, loadedId ?? '', revision)
      .then((result) => {
        setSaving(false);
        // Keep loadedId/revision in step with what the daemon actually
        // persisted (D4/D5): today the parent always closes the editor right
        // after onSaved, but updating here keeps a second Save in the same
        // buffer correct (an edit with the fresh revision, not a stale-id
        // create) if that ever changes.
        setLoadedId(result.definition.id);
        setRevision(result.definition.revision);
        onSaved(result.definition);
      })
      .catch((error) => {
        setSaving(false);
        setSaveError(error instanceof Error ? error.message : 'Save failed');
      });
  }, [applyDefinition, value, loadedId, revision, onSaved]);

  // Explicit reload (D6's other half of "only on explicit load or reload"):
  // re-fetch the definition and push it into the already-mounted buffer via
  // the minimal-edit handle, so an unaffected scroll position/selection
  // survives — see AutomationYamlEditor's applyExternalContent doc comment.
  // This is the recovery path after a stale-revision Save refusal ("changed
  // elsewhere — reload", D5).
  const handleReload = useCallback(() => {
    setReloading(true);
    setReloadError(null);
    getDefinition(loadedId ?? '')
      .then((result) => {
        setRevision(result.revision);
        editorRef.current?.applyExternalContent(result.specYaml);
        setReloading(false);
      })
      .catch((error) => {
        setReloadError(error instanceof Error ? error.message : 'Reload failed');
        setReloading(false);
      });
  }, [getDefinition, loadedId]);

  // Publish a read/write handle for the UI automation bridge (testing only —
  // see automationEditorAutomation.ts's doc comment). Re-registered whenever
  // the state getState() closes over changes, rather than GridView's
  // single-registration-reading-through-refs pattern: nothing here mutates
  // outside React's render cycle, so a plain effect dependency array keeps it
  // correct with far less machinery.
  useEffect(() => {
    setAutomationEditorAutomationHandle({
      getState: () => ({
        present: true,
        mode: definitionId ? 'edit' : 'create',
        definitionId: loadedId,
        revision,
        status,
        loadError: loadError ?? '',
        // The live CodeMirror doc, not the `value` state mirror — see
        // getDocText's doc comment for why the mirror can lag mid-typing.
        text: editorRef.current?.getDocText() ?? value,
        validation: { state: validation.state, message: validation.message ?? '' },
        saving,
        saveError: saveError ?? '',
        reloading,
        reloadError: reloadError ?? '',
        // Mirrors the JSX gate below: Reload presupposes a persisted definition.
        reloadOffered: loadedId !== null,
      }),
      // Equivalent to the user replacing the buffer by typing: go through the
      // same handleChange the real onChange path uses (so a stale validation
      // result and stale saveError are cleared even if `next` happens to
      // equal the current doc, when applyExternalContent's dispatch would be
      // a no-op), then push the text into CodeMirror so a subsequent Save —
      // and getDocText() — see exactly what was set.
      setText: (next: string) => {
        editorRef.current?.applyExternalContent(next);
        handleChange(next);
      },
    });
    return () => setAutomationEditorAutomationHandle(null);
  }, [
    definitionId,
    loadedId,
    revision,
    status,
    loadError,
    value,
    validation,
    saving,
    saveError,
    reloading,
    reloadError,
    handleChange,
  ]);

  return (
    <div className="automation-editor" data-testid="automation-editor">
      <div className="automation-editor__header">
        <h3 className="automation-editor__title">{definitionId ? 'Edit automation' : 'New automation'}</h3>
        <button
          type="button"
          className="automation-editor__close"
          onClick={onCancel}
          aria-label="Close editor"
          data-testid="automation-editor-close"
        >
          ✕
        </button>
      </div>

      {status === 'loading' && <p className="automation-editor__status">Loading…</p>}

      {status === 'load-error' && (
        <p className="automation-editor__error" data-testid="automation-editor-load-error">
          {loadError}
        </p>
      )}

      {status === 'ready' && (
        <>
          {definitionId && (
            <p className="automation-editor__hint">
              Comments in this file are preserved as written. Automations created before this editor existed may
              show comment-free YAML the first time they&apos;re opened here.
            </p>
          )}

          {saving && (
            <p className="automation-editor__hint" data-testid="automation-editor-locked-hint">
              Buffer locked while saving…
            </p>
          )}

          <div className="automation-editor__buffer">
            <AutomationYamlEditor
              ref={editorRef}
              value={value}
              onChange={handleChange}
              ariaLabel="Automation definition"
              autoFocus
              readOnly={saving}
            />
          </div>

          {reloadError && (
            <p className="automation-editor__error" data-testid="automation-editor-reload-error">
              {reloadError}
            </p>
          )}

          {validation.state === 'error' && (
            <p className="automation-editor__validation automation-editor__validation--error" data-testid="automation-editor-validation-error">
              {validation.message}
            </p>
          )}
          {validation.state === 'ok' && (
            <p className="automation-editor__validation automation-editor__validation--ok" data-testid="automation-editor-validation-ok">
              Looks good.
            </p>
          )}
          {saveError && (
            <p className="automation-editor__error" data-testid="automation-editor-save-error">
              {saveError}
            </p>
          )}

          <div className="automation-editor__actions">
            {/* Reload exists only as the recovery path after a stale-revision
                Save refusal (D5), which presupposes a persisted definition.
                While creating, loadedId is null and Reload would re-fetch the
                starter template — silently discarding the draft being typed
                for no reachable benefit. So it is not offered until the buffer
                is backed by a saved definition. */}
            {loadedId !== null ? (
              <button
                type="button"
                className="automation-editor__reload"
                onClick={handleReload}
                disabled={reloading}
                data-testid="automation-editor-reload"
              >
                {reloading ? 'Reloading…' : 'Reload'}
              </button>
            ) : (
              <span />
            )}
            <div className="automation-editor__actions-primary">
              <button
                type="button"
                className="automation-editor__validate"
                onClick={handleValidate}
                disabled={validation.state === 'checking'}
                data-testid="automation-editor-validate"
              >
                {validation.state === 'checking' ? 'Validating…' : 'Validate'}
              </button>
              <button
                type="button"
                className="automation-editor__cancel"
                onClick={onCancel}
                data-testid="automation-editor-cancel"
              >
                Cancel
              </button>
              <button
                type="button"
                className="automation-editor__save"
                onClick={handleSave}
                disabled={saving}
                data-testid="automation-editor-save"
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

// Exported for AutomationsPanel to size/key the editor's mount identity.
export function automationEditorKey(definitionId: string | null): string {
  return definitionId ?? '__new__';
}
