// Structured automation editor (PR6's replacement for the YAML buffer in
// AutomationEditor.tsx). Create mode (definitionId null) seeds its own
// defaults and never talks to the daemon until Save; edit mode loads once on
// mount (D6 in AutomationEditor's design: the host remounts this component on
// a fresh key whenever the user opens a different target, so "mount" already
// means "explicit load" — there is no reason to re-fetch on definitionId
// churn within one mount).
import { useCallback, useEffect, useRef, useState } from 'react';
import { Resolver, useFieldArray, useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { AutomationDefinitionSummary } from '../../types/generated';
import {
  AutomationFormValues,
  AutomationTrigger,
  automationFormSchema,
  slugFromName,
  specJSONString,
  specToFormValues,
} from './automationFormModel';
import { AutomationAgent, LAUNCH_CATALOG, effortOptionsFor } from './launchCatalog';
import { compiledSentenceSegments, compiledSentenceText, cronPhrase } from './automationCompiledSentence';
import { setAutomationFormAutomationHandle } from './automationFormAutomation';
import './AutomationForm.css';

export interface AutomationFormProps {
  // null → create. Non-null → edit that definition.
  definitionId: string | null;
  getDefinition: (definitionId: string) => Promise<{ specJson: string; definition?: AutomationDefinitionSummary }>;
  applyDefinition: (
    specJson: string,
    expectedId: string,
    expectedRevision: number,
  ) => Promise<{ definition: AutomationDefinitionSummary }>;
  deleteDefinition: (definitionId: string) => Promise<void>;
  setEnabled: (definitionId: string, enabled: boolean) => Promise<void>;
  onCancel: () => void;
  onSaved: (definition: AutomationDefinitionSummary) => void;
  onDeleted: () => void;
}

type LoadStatus = 'loading' | 'ready' | 'load-error';
type ModelMode = 'preset' | 'custom';

// errorCode reads the apply/delete/setEnabled promise's optional error code
// without assuming its shape — a plain Error may or may not carry one.
function errorCode(err: unknown): string {
  const code = (err as { code?: unknown } | null | undefined)?.code;
  return typeof code === 'string' ? code : '';
}

function messageOf(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

function isPresetModel(agent: AutomationAgent, model: string): boolean {
  return LAUNCH_CATALOG[agent].models.some((candidate) => candidate.id === model);
}

// Create mode's own defaults — deliberately not the daemon's starter-YAML
// template (that's a YAML-editor affordance for a text buffer with nothing
// else to seed it from); this form has structured fields to seed directly.
function makeCreateDefaults(): AutomationFormValues {
  const firstModel = LAUNCH_CATALOG.codex.models[0];
  return {
    name: '',
    id: '',
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
    directoryPath: '',
    repositoryOverrides: [],
    prompt: '',
  };
}

// flattenFieldErrors turns RHF's nested FieldErrors tree into dot-joined
// paths → message, for the automation bridge's `errors` field. Handles both
// nested objects (repositoryOverrides.0.repository) and arrays (RHF
// represents array errors as objects with numeric-string keys).
function flattenFieldErrors(errors: Record<string, unknown>, prefix = ''): Record<string, string> {
  let out: Record<string, string> = {};
  for (const key of Object.keys(errors)) {
    const value = errors[key] as Record<string, unknown> | undefined;
    if (!value || typeof value !== 'object') continue;
    const path = prefix ? `${prefix}.${key}` : key;
    if (typeof value.message === 'string') {
      out[path] = value.message;
    }
    const nestedKeys = Object.keys(value).filter(
      (k) => k !== 'message' && k !== 'type' && k !== 'ref' && k !== 'types' && k !== 'root',
    );
    if (nestedKeys.length > 0) {
      const nested: Record<string, unknown> = {};
      for (const nestedKey of nestedKeys) nested[nestedKey] = value[nestedKey];
      out = { ...out, ...flattenFieldErrors(nested, path) };
    }
  }
  return out;
}

export function AutomationForm({
  definitionId,
  getDefinition,
  applyDefinition,
  deleteDefinition,
  setEnabled: setEnabledAction,
  onCancel,
  onSaved,
  onDeleted,
}: AutomationFormProps) {
  const mode: 'create' | 'edit' = definitionId ? 'edit' : 'create';

  const {
    register,
    handleSubmit,
    watch,
    getValues,
    setValue,
    setError,
    setFocus,
    trigger,
    reset,
    control,
    formState: { errors },
  } = useForm<AutomationFormValues>({
    // automationFormSchema is typed z.ZodType<AutomationFormValues>, which
    // leaves zod's input generic as `unknown` — @hookform/resolvers' zod v4
    // overloads require the input type to extend RHF's FieldValues, so the
    // resolver's inferred type doesn't line up with useForm<AutomationFormValues>
    // without this cast. The runtime behavior (parse AutomationFormValues,
    // report issues per field) is unaffected; this is purely a typing gap
    // between the two libraries' zod v4 support.
    resolver: zodResolver(automationFormSchema as never) as unknown as Resolver<AutomationFormValues>,
    mode: 'onBlur',
    reValidateMode: 'onChange',
    defaultValues: makeCreateDefaults(),
  });

  const { fields: overrideFields, append: appendOverride, remove: removeOverride } = useFieldArray({
    control,
    name: 'repositoryOverrides',
  });

  const [status, setStatus] = useState<LoadStatus>(mode === 'edit' ? 'loading' : 'ready');
  const [loadError, setLoadError] = useState('');
  const [loadedId, setLoadedId] = useState<string | null>(definitionId);
  const [revision, setRevision] = useState(0);
  const [enabled, setEnabledState] = useState<boolean | null>(null);
  const [modelMode, setModelMode] = useState<ModelMode>('preset');

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [saveErrorCode, setSaveErrorCode] = useState('');

  const [deleteArmed, setDeleteArmed] = useState(false);
  const deleteContainerRef = useRef<HTMLDivElement>(null);
  const [includeInput, setIncludeInput] = useState('');
  const [excludeInput, setExcludeInput] = useState('');

  // Mount-only initial load — see the file-level comment for why this is
  // correct without a definitionId dependency (D6 convention).
  useEffect(() => {
    if (mode !== 'edit' || !definitionId) return;
    let cancelled = false;
    getDefinition(definitionId)
      .then((result) => {
        if (cancelled) return;
        let parsed: AutomationFormValues;
        try {
          parsed = specToFormValues(result.specJson);
        } catch (error) {
          setLoadError(messageOf(error, 'Failed to parse automation definition'));
          setStatus('load-error');
          return;
        }
        reset(parsed);
        setModelMode(isPresetModel(parsed.agent, parsed.model) ? 'preset' : 'custom');
        setLoadedId(result.definition?.id ?? definitionId);
        setRevision(result.definition?.revision ?? 0);
        setEnabledState(result.definition?.enabled ?? null);
        setStatus('ready');
      })
      .catch((error) => {
        if (cancelled) return;
        setLoadError(messageOf(error, 'Failed to load automation definition'));
        setStatus('load-error');
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const doSave = useCallback(
    (values: AutomationFormValues) => {
      setSaving(true);
      setSaveError('');
      setSaveErrorCode('');
      applyDefinition(specJSONString(values), loadedId ?? '', revision)
        .then((result) => {
          setSaving(false);
          setLoadedId(result.definition.id);
          setRevision(result.definition.revision);
          setEnabledState(result.definition.enabled);
          onSaved(result.definition);
        })
        .catch((error: unknown) => {
          setSaving(false);
          const code = errorCode(error);
          const message = messageOf(error, 'Failed to save automation');
          if (code === 'id_collision') {
            setError('id', { message });
            return;
          }
          setSaveErrorCode(code);
          setSaveError(message);
        });
    },
    [applyDefinition, loadedId, revision, onSaved, setError],
  );

  const onSubmit = handleSubmit(doSave);

  const handleReload = useCallback(() => {
    if (loadedId === null) return;
    getDefinition(loadedId)
      .then((result) => {
        let parsed: AutomationFormValues;
        try {
          parsed = specToFormValues(result.specJson);
        } catch (error) {
          setSaveErrorCode('');
          setSaveError(messageOf(error, 'Failed to parse automation definition'));
          return;
        }
        reset(parsed);
        setModelMode(isPresetModel(parsed.agent, parsed.model) ? 'preset' : 'custom');
        setRevision(result.definition?.revision ?? revision);
        setEnabledState(result.definition?.enabled ?? enabled);
        setSaveError('');
        setSaveErrorCode('');
      })
      .catch((error) => {
        setSaveErrorCode('');
        setSaveError(messageOf(error, 'Failed to reload automation definition'));
      });
  }, [getDefinition, loadedId, reset, revision, enabled]);

  const performDelete = useCallback(() => {
    if (loadedId === null) return;
    deleteDefinition(loadedId)
      .then(() => onDeleted())
      .catch((error) => {
        setDeleteArmed(false);
        setSaveErrorCode('');
        setSaveError(messageOf(error, 'Failed to delete automation'));
      });
  }, [loadedId, deleteDefinition, onDeleted]);

  const handleDeleteClick = useCallback(() => {
    if (!deleteArmed) {
      setDeleteArmed(true);
      return;
    }
    performDelete();
  }, [deleteArmed, performDelete]);

  // Clicking anywhere outside the delete control disarms it.
  useEffect(() => {
    if (!deleteArmed) return;
    function handlePointerDown(event: MouseEvent) {
      if (deleteContainerRef.current && !deleteContainerRef.current.contains(event.target as Node)) {
        setDeleteArmed(false);
      }
    }
    document.addEventListener('mousedown', handlePointerDown);
    return () => document.removeEventListener('mousedown', handlePointerDown);
  }, [deleteArmed]);

  const handleToggleEnabled = useCallback(() => {
    if (loadedId === null || enabled === null) return;
    const next = !enabled;
    setEnabledAction(loadedId, next)
      .then(() => setEnabledState(next))
      .catch((error) => {
        setSaveErrorCode('');
        setSaveError(messageOf(error, 'Failed to update automation'));
      });
  }, [loadedId, enabled, setEnabledAction]);

  const handleTriggerChange = useCallback(
    (next: AutomationTrigger) => {
      setValue('trigger', next, { shouldDirty: true, shouldValidate: true });
      setSaveError('');
      setSaveErrorCode('');
    },
    [setValue],
  );

  // regField wraps register() so a field that is ALREADY showing an error
  // re-validates on every keystroke, not just the next blur. mode:'onBlur' +
  // reValidateMode:'onChange' looks like it should give this for free, but
  // reValidateMode only takes effect after the form's first handleSubmit
  // call (RHF's documented scope for it) — before that, an existing error
  // only clears on the field's next blur under mode:'onBlur' alone. Since
  // the pinned config keeps mode/reValidateMode as given, this closes that
  // gap explicitly rather than changing the pinned trigger strategy.
  const regField = useCallback(
    (field: keyof AutomationFormValues) => {
      const base = register(field);
      return {
        ...base,
        onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => {
          base.onChange(event);
          if (errors[field]) void trigger(field);
        },
      };
    },
    [register, errors, trigger],
  );

  const nameRegister = regField('name');
  const handleNameChange = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      nameRegister.onChange(event);
      if (mode === 'create' && !getValues('idCustomized')) {
        setValue('id', slugFromName(event.target.value), { shouldDirty: true, shouldValidate: true });
      }
    },
    [nameRegister, mode, getValues, setValue],
  );

  const handleCustomizeId = useCallback(() => {
    setValue('idCustomized', true, { shouldDirty: true, shouldValidate: true });
    setFocus('id');
  }, [setValue, setFocus]);

  const handleAgentChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const nextAgent = event.target.value as AutomationAgent;
      const catalog = LAUNCH_CATALOG[nextAgent];
      const firstModel = catalog.models[0];
      setValue('agent', nextAgent, { shouldDirty: true, shouldValidate: true });
      setValue('model', firstModel.id, { shouldDirty: true, shouldValidate: true });
      setValue('effort', firstModel.defaultEffort, { shouldDirty: true, shouldValidate: true });
      setModelMode('preset');
    },
    [setValue],
  );

  const handleModelSelectChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const next = event.target.value;
      const agent = getValues('agent');
      const catalog = LAUNCH_CATALOG[agent];
      if (next === '__custom__') {
        setModelMode('custom');
        setValue('model', '', { shouldDirty: true, shouldValidate: true });
        setValue('effort', catalog.customDefaultEffort, { shouldDirty: true, shouldValidate: true });
        return;
      }
      setModelMode('preset');
      const preset = catalog.models.find((candidate) => candidate.id === next);
      setValue('model', next, { shouldDirty: true, shouldValidate: true });
      const { efforts, defaultEffort } = effortOptionsFor(agent, next);
      if (!efforts.includes(getValues('effort'))) {
        setValue('effort', preset?.defaultEffort ?? defaultEffort, { shouldDirty: true, shouldValidate: true });
      }
    },
    [getValues, setValue],
  );

  function addRepository(field: 'repositoriesInclude' | 'repositoriesExclude', raw: string) {
    const canonical = raw.trim().toLowerCase();
    if (canonical === '') return;
    setValue(field, [...getValues(field), canonical], { shouldDirty: true, shouldValidate: true });
  }

  function removeRepository(field: 'repositoriesInclude' | 'repositoriesExclude', index: number) {
    setValue(
      field,
      getValues(field).filter((_, i) => i !== index),
      { shouldDirty: true, shouldValidate: true },
    );
  }

  // Publish the UI automation bridge handle (testing/packaged-app harness
  // only — see automationFormAutomation.ts). Re-registered whenever any
  // closed-over state changes, same as AutomationEditor's registration
  // effect, so the bridge always reads through current handlers/state.
  useEffect(() => {
    setAutomationFormAutomationHandle({
      getState: () => ({
        present: true,
        mode,
        definitionId: loadedId,
        revision,
        status,
        loadError,
        values: getValues(),
        errors: flattenFieldErrors(errors as unknown as Record<string, unknown>),
        saving,
        saveError,
        saveErrorCode,
        enabled,
        compiledSentence: compiledSentenceText(getValues()),
        deleteArmed,
      }),
      setValues: (partial) => {
        (Object.keys(partial) as (keyof AutomationFormValues)[]).forEach((key) => {
          setValue(key, partial[key] as never, { shouldDirty: true, shouldValidate: true });
        });
      },
      submit: () => {
        onSubmit();
      },
      reload: handleReload,
      armDelete: () => setDeleteArmed(true),
      confirmDelete: performDelete,
    });
    return () => setAutomationFormAutomationHandle(null);
  }, [
    mode,
    loadedId,
    revision,
    status,
    loadError,
    errors,
    saving,
    saveError,
    saveErrorCode,
    enabled,
    deleteArmed,
    getValues,
    setValue,
    onSubmit,
    handleReload,
    performDelete,
  ]);

  if (status === 'load-error') {
    return (
      <div className="automation-form" data-testid="automation-form">
        <div className="automation-form__header">
          <h3 className="automation-form__title">{mode === 'edit' ? 'Edit automation' : 'New automation'}</h3>
          <button
            type="button"
            className="automation-form__close"
            onClick={onCancel}
            aria-label="Close"
            data-testid="automation-form-close"
          >
            ✕
          </button>
        </div>
        <p className="automation-form__load-error" data-testid="automation-form-load-error">
          {loadError}
        </p>
      </div>
    );
  }

  if (status === 'loading') {
    return (
      <div className="automation-form" data-testid="automation-form">
        <div className="automation-form__header">
          <h3 className="automation-form__title">Edit automation</h3>
          <button
            type="button"
            className="automation-form__close"
            onClick={onCancel}
            aria-label="Close"
            data-testid="automation-form-close"
          >
            ✕
          </button>
        </div>
        <p className="automation-form__status">Loading…</p>
      </div>
    );
  }

  const values = watch();
  const idCustomized = values.idCustomized;
  const { efforts } = effortOptionsFor(values.agent, values.model);
  const catalog = LAUNCH_CATALOG[values.agent];
  const sentenceSegments = compiledSentenceSegments(values);

  function fieldError(field: keyof AutomationFormValues): string | undefined {
    const entry = errors[field] as { message?: string } | undefined;
    return entry?.message;
  }

  function renderDirectoryField() {
    return (
      <div className="automation-form__field">
        <label className="automation-form__label" htmlFor="automation-form-directory-path">
          Directory
        </label>
        <input
          id="automation-form-directory-path"
          className={fieldError('directoryPath') ? 'automation-form__input automation-form__input--invalid' : 'automation-form__input'}
          data-testid="automation-form-directory-path"
          placeholder="/absolute/path"
          {...regField('directoryPath')}
        />
        {fieldError('directoryPath') && (
          <p className="automation-form__field-error" data-testid="automation-form-error-directoryPath">
            {fieldError('directoryPath')}
          </p>
        )}
      </div>
    );
  }

  return (
    <form className="automation-form" data-testid="automation-form" onSubmit={onSubmit}>
      <div className="automation-form__header">
        <h3 className="automation-form__title">{mode === 'edit' ? 'Edit automation' : 'New automation'}</h3>
        {mode === 'edit' && enabled !== null && (
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            className={enabled ? 'automation-form__enabled automation-form__enabled--on' : 'automation-form__enabled'}
            onClick={handleToggleEnabled}
            data-testid="automation-form-enabled"
          >
            {enabled ? 'Enabled' : 'Disabled'}
          </button>
        )}
        <button
          type="button"
          className="automation-form__close"
          onClick={onCancel}
          aria-label="Close"
          data-testid="automation-form-close"
        >
          ✕
        </button>
      </div>

      <fieldset className="automation-form__body" disabled={saving}>
        {saving && (
          <p className="automation-form__hint" data-testid="automation-form-saving-hint">
            Saving…
          </p>
        )}

        <section className="automation-form__section">
          <span className="automation-form__section-label">Name</span>
          <input
            className={fieldError('name') ? 'automation-form__input automation-form__input--invalid' : 'automation-form__input'}
            data-testid="automation-form-name"
            placeholder="Automation name"
            name={nameRegister.name}
            ref={nameRegister.ref}
            onBlur={nameRegister.onBlur}
            onChange={handleNameChange}
          />
          {fieldError('name') && (
            <p className="automation-form__field-error" data-testid="automation-form-error-name">
              {fieldError('name')}
            </p>
          )}

          {mode === 'create' ? (
            <div className="automation-form__id-row">
              <input
                className={fieldError('id') ? 'automation-form__input automation-form__input--invalid' : 'automation-form__input'}
                data-testid="automation-form-id"
                readOnly={!idCustomized}
                {...regField('id')}
              />
              {!idCustomized && (
                <button
                  type="button"
                  className="automation-form__id-customize"
                  onClick={handleCustomizeId}
                  data-testid="automation-form-id-customize"
                >
                  Customize
                </button>
              )}
            </div>
          ) : (
            <p className="automation-form__id-static" data-testid="automation-form-id-static">
              ID: {values.id} · fixed after creation
            </p>
          )}
          {fieldError('id') && (
            <p className="automation-form__field-error" data-testid="automation-form-error-id">
              {fieldError('id')}
            </p>
          )}
        </section>

        <section className="automation-form__section">
          <span className="automation-form__section-label">Trigger</span>
          <div className="automation-form__trigger-cards">
            <button
              type="button"
              className={
                values.trigger === 'manual' ? 'automation-form__trigger-card automation-form__trigger-card--selected' : 'automation-form__trigger-card'
              }
              onClick={() => handleTriggerChange('manual')}
              data-testid="automation-form-trigger-manual"
            >
              Manual
            </button>
            <button
              type="button"
              className={
                values.trigger === 'scheduled'
                  ? 'automation-form__trigger-card automation-form__trigger-card--selected'
                  : 'automation-form__trigger-card'
              }
              onClick={() => handleTriggerChange('scheduled')}
              data-testid="automation-form-trigger-scheduled"
            >
              Scheduled
            </button>
            <button
              type="button"
              className={
                values.trigger === 'github_review_requested'
                  ? 'automation-form__trigger-card automation-form__trigger-card--selected'
                  : 'automation-form__trigger-card'
              }
              onClick={() => handleTriggerChange('github_review_requested')}
              data-testid="automation-form-trigger-github"
            >
              PR review requested
            </button>
          </div>

          {values.trigger === 'manual' && (
            <div className="automation-form__trigger-section">
              <span className="automation-form__fact-chip">Fresh worker each run</span>
              {renderDirectoryField()}
            </div>
          )}

          {values.trigger === 'scheduled' && (
            <div className="automation-form__trigger-section">
              <div className="automation-form__field">
                <label className="automation-form__label" htmlFor="automation-form-cron">
                  Schedule (cron)
                </label>
                <input
                  id="automation-form-cron"
                  className={
                    fieldError('scheduleCron') ? 'automation-form__input automation-form__input--invalid' : 'automation-form__input'
                  }
                  data-testid="automation-form-cron"
                  placeholder="0 9 * * *"
                  {...regField('scheduleCron')}
                />
                <p className="automation-form__cron-phrase" data-testid="automation-form-cron-phrase">
                  {cronPhrase(values.scheduleCron) ?? "not set yet"}
                </p>
                {fieldError('scheduleCron') && (
                  <p className="automation-form__field-error" data-testid="automation-form-error-scheduleCron">
                    {fieldError('scheduleCron')}
                  </p>
                )}
              </div>

              <div className="automation-form__field">
                <span className="automation-form__label">Worker</span>
                <div className="automation-form__segmented">
                  <button
                    type="button"
                    className={
                      values.continuity === 'fresh'
                        ? 'automation-form__segment automation-form__segment--selected'
                        : 'automation-form__segment'
                    }
                    onClick={() => setValue('continuity', 'fresh', { shouldDirty: true, shouldValidate: true })}
                    data-testid="automation-form-continuity-fresh"
                  >
                    Fresh
                  </button>
                  <button
                    type="button"
                    className={
                      values.continuity === 'singleton'
                        ? 'automation-form__segment automation-form__segment--selected'
                        : 'automation-form__segment'
                    }
                    onClick={() => setValue('continuity', 'singleton', { shouldDirty: true, shouldValidate: true })}
                    data-testid="automation-form-continuity-singleton"
                  >
                    Singleton
                  </button>
                </div>
              </div>

              <div className="automation-form__field">
                <span className="automation-form__label">Missed runs</span>
                <div className="automation-form__segmented">
                  <button
                    type="button"
                    className={
                      values.catchUp === 'skip' ? 'automation-form__segment automation-form__segment--selected' : 'automation-form__segment'
                    }
                    onClick={() => setValue('catchUp', 'skip', { shouldDirty: true, shouldValidate: true })}
                    data-testid="automation-form-catchup-skip"
                  >
                    Skip
                  </button>
                  <button
                    type="button"
                    className={
                      values.catchUp === 'latest' ? 'automation-form__segment automation-form__segment--selected' : 'automation-form__segment'
                    }
                    onClick={() => setValue('catchUp', 'latest', { shouldDirty: true, shouldValidate: true })}
                    data-testid="automation-form-catchup-latest"
                  >
                    Latest
                  </button>
                </div>
                {fieldError('catchUp') && (
                  <p className="automation-form__field-error" data-testid="automation-form-error-catchUp">
                    {fieldError('catchUp')}
                  </p>
                )}
              </div>

              {renderDirectoryField()}
            </div>
          )}

          {values.trigger === 'github_review_requested' && (
            <div className="automation-form__trigger-section">
              <div className="automation-form__field">
                <label className="automation-form__label" htmlFor="automation-form-repositories-include-input">
                  Include repositories
                </label>
                <div className="automation-form__chip-input" data-testid="automation-form-repositories-include">
                  {values.repositoriesInclude.map((entry, index) => (
                    <span className="automation-form__chip" key={`${entry}-${index}`} data-testid={`automation-form-repositories-include-chip-${index}`}>
                      {entry}
                      <button
                        type="button"
                        aria-label={`Remove ${entry}`}
                        onClick={() => removeRepository('repositoriesInclude', index)}
                        data-testid={`automation-form-repositories-include-remove-${index}`}
                      >
                        ✕
                      </button>
                    </span>
                  ))}
                  <input
                    id="automation-form-repositories-include-input"
                    className="automation-form__chip-input-field"
                    value={includeInput}
                    onChange={(event) => setIncludeInput(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        addRepository('repositoriesInclude', includeInput);
                        setIncludeInput('');
                      } else if (event.key === 'Backspace' && includeInput === '' && values.repositoriesInclude.length > 0) {
                        removeRepository('repositoriesInclude', values.repositoriesInclude.length - 1);
                      }
                    }}
                    placeholder="host/owner/repository"
                    data-testid="automation-form-repositories-include-input"
                  />
                </div>
              </div>

              <div className="automation-form__field">
                <label className="automation-form__label" htmlFor="automation-form-repositories-exclude-input">
                  Exclude repositories
                </label>
                <div className="automation-form__chip-input" data-testid="automation-form-repositories-exclude">
                  {values.repositoriesExclude.map((entry, index) => (
                    <span className="automation-form__chip" key={`${entry}-${index}`} data-testid={`automation-form-repositories-exclude-chip-${index}`}>
                      {entry}
                      <button
                        type="button"
                        aria-label={`Remove ${entry}`}
                        onClick={() => removeRepository('repositoriesExclude', index)}
                        data-testid={`automation-form-repositories-exclude-remove-${index}`}
                      >
                        ✕
                      </button>
                    </span>
                  ))}
                  <input
                    id="automation-form-repositories-exclude-input"
                    className="automation-form__chip-input-field"
                    value={excludeInput}
                    onChange={(event) => setExcludeInput(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        addRepository('repositoriesExclude', excludeInput);
                        setExcludeInput('');
                      } else if (event.key === 'Backspace' && excludeInput === '' && values.repositoriesExclude.length > 0) {
                        removeRepository('repositoriesExclude', values.repositoriesExclude.length - 1);
                      }
                    }}
                    placeholder="host/owner/repository"
                    data-testid="automation-form-repositories-exclude-input"
                  />
                </div>
              </div>

              <span className="automation-form__fact-chip">One reviewer per PR — later request cycles return to it</span>
              <span className="automation-form__fact-chip">Missed while attn was off: latest request still runs</span>
              <p className="automation-form__invariant">
                Reviews always run in a fresh worktree checked out at the PR&apos;s head commit — your existing clone
                is never touched.
              </p>

              <details className="automation-form__advanced">
                <summary>Advanced</summary>
                <div className="automation-form__overrides">
                  {overrideFields.map((field, index) => (
                    <div className="automation-form__override-row" key={field.id}>
                      <input
                        className="automation-form__input"
                        placeholder="host/owner/repository"
                        data-testid={`automation-form-override-repository-${index}`}
                        {...register(`repositoryOverrides.${index}.repository` as const)}
                      />
                      <input
                        className="automation-form__input"
                        placeholder="/absolute/path"
                        data-testid={`automation-form-override-path-${index}`}
                        {...register(`repositoryOverrides.${index}.path` as const)}
                      />
                      <button
                        type="button"
                        onClick={() => removeOverride(index)}
                        data-testid={`automation-form-overrides-remove-${index}`}
                      >
                        Remove
                      </button>
                    </div>
                  ))}
                  <button
                    type="button"
                    onClick={() => appendOverride({ repository: '', path: '' })}
                    data-testid="automation-form-overrides-add"
                  >
                    Add override
                  </button>
                </div>
              </details>
            </div>
          )}
        </section>

        <section className="automation-form__section">
          <span className="automation-form__section-label">Runs as</span>
          <div className="automation-form__field">
            <label className="automation-form__label" htmlFor="automation-form-agent">
              Agent
            </label>
            <select
              id="automation-form-agent"
              className="automation-form__input"
              value={values.agent}
              onChange={handleAgentChange}
              data-testid="automation-form-agent"
            >
              <option value="codex">Codex</option>
              <option value="claude">Claude</option>
            </select>
          </div>

          <div className="automation-form__field">
            <label className="automation-form__label" htmlFor="automation-form-model">
              Model
            </label>
            <select
              id="automation-form-model"
              className="automation-form__input"
              value={modelMode === 'custom' ? '__custom__' : values.model}
              onChange={handleModelSelectChange}
              data-testid="automation-form-model"
            >
              {catalog.models.map((option) => (
                <option key={option.id} value={option.id}>
                  {option.label}
                </option>
              ))}
              <option value="__custom__">Custom…</option>
            </select>
            {modelMode === 'custom' && (
              <input
                className="automation-form__input"
                placeholder="Model name"
                data-testid="automation-form-model-custom"
                {...regField('model')}
              />
            )}
            {fieldError('model') && (
              <p className="automation-form__field-error" data-testid="automation-form-error-model">
                {fieldError('model')}
              </p>
            )}
          </div>

          <div className="automation-form__field">
            <label className="automation-form__label" htmlFor="automation-form-effort">
              Effort
            </label>
            <select
              id="automation-form-effort"
              className="automation-form__input"
              value={values.effort}
              onChange={(event) => setValue('effort', event.target.value, { shouldDirty: true, shouldValidate: true })}
              data-testid="automation-form-effort"
            >
              {efforts.map((effort) => (
                <option key={effort} value={effort}>
                  {effort}
                </option>
              ))}
            </select>
            {fieldError('effort') && (
              <p className="automation-form__field-error" data-testid="automation-form-error-effort">
                {fieldError('effort')}
              </p>
            )}
          </div>

          <p className="automation-form__invariant">
            Automation sessions always run unattended with the agent&apos;s automatic approval mode.
          </p>

          <details className="automation-form__advanced">
            <summary>Advanced</summary>
            <div className="automation-form__field">
              <label className="automation-form__label" htmlFor="automation-form-executable">
                Executable override
              </label>
              <input
                id="automation-form-executable"
                className="automation-form__input"
                placeholder="Default from PATH"
                data-testid="automation-form-executable"
                {...regField('executable')}
              />
            </div>
          </details>
        </section>

        <section className="automation-form__section">
          <span className="automation-form__section-label">Prompt</span>
          <textarea
            className={fieldError('prompt') ? 'automation-form__textarea automation-form__textarea--invalid' : 'automation-form__textarea'}
            data-testid="automation-form-prompt"
            {...regField('prompt')}
          />
          <p className="automation-form__hint">
            This is the instruction the agent receives. Trigger details arrive separately as structured context —
            they can never rewrite this prompt.
          </p>
          {fieldError('prompt') && (
            <p className="automation-form__field-error" data-testid="automation-form-error-prompt">
              {fieldError('prompt')}
            </p>
          )}
        </section>
      </fieldset>

      <p className="automation-form__sentence" aria-live="polite" aria-label="This automation, in plain words" data-testid="automation-form-sentence">
        {sentenceSegments.map((segment, index) => {
          const className =
            segment.emphasis === 'accent'
              ? 'automation-form__sentence-accent'
              : segment.emphasis === 'strong'
                ? 'automation-form__sentence-strong'
                : segment.emphasis === 'mono'
                  ? 'automation-form__sentence-mono'
                  : undefined;
          return className ? (
            <span className={className} key={index}>
              {segment.text}
            </span>
          ) : (
            <span key={index}>{segment.text}</span>
          );
        })}
      </p>

      {saveErrorCode === 'revision_conflict' && (
        <div className="automation-form__banner automation-form__banner--stale" data-testid="automation-form-stale-banner">
          <p>
            This automation changed elsewhere while you were editing. Reload to pick up the latest revision — your
            unsaved edits will be replaced.
          </p>
          <button type="button" onClick={handleReload} data-testid="automation-form-reload">
            Reload
          </button>
        </div>
      )}

      {saveError !== '' && saveErrorCode !== 'revision_conflict' && (
        <p className="automation-form__banner automation-form__banner--error" data-testid="automation-form-save-error">
          {saveError}
        </p>
      )}

      <div className="automation-form__actions">
        {mode === 'edit' ? (
          <div className="automation-form__delete" ref={deleteContainerRef}>
            <button
              type="button"
              className={
                deleteArmed ? 'automation-form__delete-button automation-form__delete-button--armed' : 'automation-form__delete-button'
              }
              onClick={handleDeleteClick}
              data-testid="automation-form-delete"
            >
              {deleteArmed ? 'Confirm delete' : 'Delete'}
            </button>
            {deleteArmed && <span className="automation-form__delete-note">Existing tickets and run history are kept.</span>}
          </div>
        ) : (
          <span />
        )}

        <div className="automation-form__actions-primary">
          <button type="button" className="automation-form__cancel" onClick={onCancel} data-testid="automation-form-cancel">
            Cancel
          </button>
          <button type="submit" className="automation-form__save" disabled={saving} data-testid="automation-form-save">
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </form>
  );
}

// Exported for the future host (AutomationsPanel) to size/key this
// component's mount identity, mirroring automationEditorKey.
export function automationFormKey(definitionId: string | null): string {
  return definitionId ?? '__new__';
}
