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

  const handleChange = useCallback((next: string) => {
    setValue(next);
    // A prior Validate/Save result no longer describes the current text.
    setValidation((prev) => (prev.state === 'idle' ? prev : { state: 'idle' }));
    setSaveError(null);
  }, []);

  const handleValidate = useCallback(() => {
    setValidation({ state: 'checking' });
    validateDefinition(value)
      .then(() => setValidation({ state: 'ok' }))
      .catch((error) => {
        setValidation({ state: 'error', message: error instanceof Error ? error.message : 'Validation failed' });
      });
  }, [validateDefinition, value]);

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

          <div className="automation-editor__buffer">
            <AutomationYamlEditor
              ref={editorRef}
              value={value}
              onChange={handleChange}
              ariaLabel="Automation definition"
              autoFocus
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
