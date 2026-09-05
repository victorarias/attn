import { useEffect, useRef, useState, useId } from 'react';
import type { DelegationChoice, DelegationPreferences, DelegationRole, DelegationSelection, DelegationHarness } from '../types/generated';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { DelegationPreferencesPolicy } from '../hooks/useDelegationPreferences';
import { DELEGATION_ICONS, DelegationRoleIcon } from './DelegationRoleIcon';
import './DelegationSettings.css';

const emptySelection = (): DelegationSelection => ({ harness: '', provider: '', model: '', effort: '' });
const id = (prefix: string) => `${prefix}-${crypto.randomUUID()}`;
const route = (s: DelegationSelection) => !s.harness ? 'Choose a harness and model' : [s.harness, s.provider, s.model || 'Harness default', s.effort].filter(Boolean).join(' / ');

function SelectionPicker({ value, onChange, harnesses, catalog, loading, error, discover }: {
  value: DelegationSelection;
  onChange: (s: DelegationSelection) => void;
  harnesses: DelegationHarness[];
  catalog?: DelegationModelCatalog;
  loading: boolean;
  error?: string;
  discover: (harness: string) => void;
}) {
  const prefix = useId();
  const [manual, setManual] = useState(false);
  const harness = harnesses.find(h => h.id === value.harness);
  const model = catalog?.models.find(m => m.id === value.model && m.provider === value.provider);
  const modelKey = JSON.stringify([value.provider, value.model]);
  const unknown = value.model && !model;
  const levels = model?.effort_levels ?? [];
  return <div className="delegation-picker">
    <div className="delegation-fields">
      <label>Harness<span className="delegation-select"><select value={value.harness} onChange={e => { setManual(false); onChange({ ...emptySelection(), harness: e.target.value }); }}>
        <option value="">Choose a harness</option>
        {harnesses.map(h => <option value={h.id} key={h.id}>{h.name}{h.available ? '' : ' (unavailable)'}</option>)}
        {value.harness && !harness && <option>{value.harness}</option>}
      </select></span></label>
      <label>Model<span className="delegation-select"><select value={manual ? '__custom' : modelKey} disabled={!value.harness || harness?.model_pin === false} onChange={e => {
        if (e.target.value === '__custom') { setManual(true); return; }
        const [provider, model] = JSON.parse(e.target.value) as [string, string];
        onChange({ ...value, provider, model, effort: '' });
      }}>
        <option value={JSON.stringify(['', ''])}>Harness default</option>
        {catalog?.models.map(m => <option key={`${m.provider}/${m.id}`} value={JSON.stringify([m.provider, m.id])} disabled={m.access === 'unsupported'}>{m.provider ? `${m.provider} / ` : ''}{m.name || m.id}</option>)}
        {unknown && <option value={modelKey}>{value.provider ? `${value.provider} / ` : ''}{value.model} (custom)</option>}
        <option value="__custom">Enter a model ID…</option>
      </select></span></label>
      <label>Effort<input list={`${prefix}-efforts`} value={value.effort} placeholder="Harness default" disabled={!value.harness || harness?.effort_pin === false || model?.effort_support === 'unsupported'} onChange={e => onChange({ ...value, effort: e.target.value })} />
        <datalist id={`${prefix}-efforts`}>{levels.map(level => <option key={level} value={level} />)}</datalist>
      </label>
    </div>
    {manual && <div className="delegation-fields custom-model">
      <label>Exact model ID<input value={value.model} onChange={e => onChange({ ...value, model: e.target.value, effort: '' })} placeholder="Model ID from your harness" /></label>
      {!['claude', 'codex', 'copilot'].includes(value.harness) && <label>Provider<input value={value.provider} onChange={e => onChange({ ...value, provider: e.target.value })} /></label>}
      <button className="settings-action" onClick={() => setManual(false)}>Done</button>
    </div>}
    <div className="delegation-row">
      <p className="settings-hint">{harness?.model_pin === false ? 'This harness uses its own selected model.' : model?.effort_support === 'unsupported' ? 'This model does not support an effort override.' : levels.length ? `Supported effort: ${levels.join(', ')}.` : 'Leave effort blank for the harness default. Enter a level only if your harness supports it.'}</p>
      <button className="settings-action" disabled={!value.harness || loading} onClick={() => discover(value.harness)}>{loading ? 'Discovering…' : catalog ? 'Refresh models' : 'Discover models'}</button>
    </div>
    {error && <p className="settings-warning" role="alert">{error}</p>}
    {catalog?.detail && <p className="settings-hint">{catalog.detail}</p>}
    {harness && !harness.available && <p className="settings-warning">This harness is unavailable on this daemon. Check Agents and models.</p>}
  </div>;
}

export function DelegationSettings({ policy, loadModels }: { policy: DelegationPreferencesPolicy; loadModels: (harness: string) => Promise<DelegationModelCatalog> }) {
  const { state, draft: config, setDraft, busy, dirty, error, changedElsewhere, persist, reload } = policy;
  const [screen, setScreen] = useState<'roles' | 'fallback'>('roles');
  const [roleID, setRoleID] = useState('');
  const [choiceID, setChoiceID] = useState('');
  const [iconsOpen, setIconsOpen] = useState(false);
  const [starter, setStarter] = useState<DelegationSelection>(emptySelection);
  const [undo, setUndo] = useState<DelegationPreferences | null>(null);
  const [catalogs, setCatalogs] = useState<Record<string, DelegationModelCatalog>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [modelErrors, setModelErrors] = useState<Record<string, string>>({});
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const discover = async (harness: string) => {
    if (!config?.enabled || loading[harness]) return;
    setLoading(s => ({ ...s, [harness]: true })); setModelErrors(s => ({ ...s, [harness]: '' }));
    try { const result = await loadModels(harness); if (mounted.current) setCatalogs(s => ({ ...s, [harness]: result })); }
    catch (e) { if (mounted.current) setModelErrors(s => ({ ...s, [harness]: e instanceof Error ? e.message : String(e) })); }
    finally { if (mounted.current) setLoading(s => ({ ...s, [harness]: false })); }
  };
  if (!config || !state) return <div role="status">{error || 'Loading delegation preferences…'}{error && <button className="settings-action" onClick={() => void reload()}>Retry</button>}</div>;
  const update = (next: DelegationPreferences) => setDraft(next);
  const role = config.roles.find(r => r.id === roleID);
  const updateRole = (next: DelegationRole) => update({ ...config, roles: config.roles.map(r => r.id === next.id ? next : r) });
  const picker = (selection: DelegationSelection, onChange: (s: DelegationSelection) => void) => <SelectionPicker value={selection} onChange={onChange} harnesses={state.harnesses} catalog={catalogs[selection.harness]} loading={!!loading[selection.harness]} error={modelErrors[selection.harness]} discover={h => void discover(h)} />;
  const addTemplates = () => update({ ...config, roles: [...config.roles, ...structuredClone(state.templates.filter(t => !config.roles.some(r => r.id === t.id)))] });
  const addRole = () => {
    const next: DelegationRole = { id: id('role'), name: 'New role', icon: '', enabled: true, description: '', instructions: '', stopping_point: '', default_choice_id: 'default', choices: [{ id: 'default', name: 'Default', when: '', selection: emptySelection() }] };
    update({ ...config, roles: [...config.roles, next] }); setRoleID(next.id); setChoiceID('default');
  };
  const removeRole = () => { setUndo(structuredClone(config)); update({ ...config, roles: config.roles.filter(r => r.id !== roleID) }); setRoleID(''); };
  const addChoice = () => {
    if (!role) return;
    const choice: DelegationChoice = { id: id('choice'), name: 'Alternative', when: '', selection: structuredClone(role.choices.find(c => c.id === role.default_choice_id)?.selection ?? emptySelection()) };
    updateRole({ ...role, choices: [...role.choices, choice] }); setChoiceID(choice.id);
  };
  const missing = config.roles.some(r => !r.choices.find(c => c.id === r.default_choice_id)?.selection.harness) || !config.fallback.selection.harness;
  return <div className="delegation-settings" data-testid="delegation-settings">
    <div className="delegation-enable">
      <div><h3>Use attn delegation preferences</h3><p className="settings-description">Apply these settings after an agent has decided to delegate.</p></div>
      <label className="delegation-switch"><input type="checkbox" checked={config.enabled} disabled={busy} onChange={e => {
        const next = { ...config, enabled: e.target.checked };
        if (next.enabled && next.revision === 0 && !next.roles.length) next.roles = structuredClone(state.templates);
        void persist(next);
      }} />{config.enabled ? 'On' : 'Off'}</label>
    </div>
    {error && <p role="alert" className="settings-warning">{error}</p>}
    {changedElsewhere && <p role="alert" className="settings-warning">Preferences changed elsewhere. Your draft is preserved. Reload saved settings before making a new edit.</p>}
    {!config.enabled ? <div className="delegation-off"><h3>Your existing setup stays yours.</h3><p className="settings-description">Off by default. Your custom skills, prompts, and direct delegation commands keep their current behavior. Turning it off preserves these settings.</p></div> : <fieldset disabled={busy} className="delegation-content">
      <div className="delegation-tabs" role="group" aria-label="Delegation settings">
        <button className={`settings-action ${screen === 'roles' ? 'active' : ''}`} onClick={() => { setScreen('roles'); setRoleID(''); }}>Roles</button>
        <button className={`settings-action ${screen === 'fallback' ? 'active' : ''}`} onClick={() => { setScreen('fallback'); setRoleID(''); }}>Fallback</button>
      </div>
      {screen === 'fallback' ? <>
        <h3>When no role fits</h3><p className="settings-description">Use this fallback only when the task does not match an enabled role.</p>
        {picker(config.fallback.selection, selection => update({ ...config, fallback: { ...config.fallback, selection } }))}
        <label className="delegation-field">Instructions for unmatched work (optional)<textarea value={config.fallback.instructions} onChange={e => update({ ...config, fallback: { ...config.fallback, instructions: e.target.value } })} /></label>
        <p className="settings-hint">These instructions apply only to unmatched work. Role instructions stay independent.</p>
      </> : role ? <>
        <div className="delegation-row between"><button className="settings-action" onClick={() => { setRoleID(''); setIconsOpen(false); }}>← All roles</button><button className="settings-action danger" onClick={removeRole}>Delete role</button></div>
        <div className="delegation-role-heading"><button className="delegation-icon" aria-label="Choose role icon" aria-expanded={iconsOpen} onClick={() => setIconsOpen(v => !v)}><DelegationRoleIcon icon={role.icon} name={role.name} /></button>
          <label className="delegation-field">Role name<input value={role.name} onChange={e => updateRole({ ...role, name: e.target.value })} /></label></div>
        {iconsOpen && <div className="delegation-icons" role="group" aria-label="Role icons"><button className="settings-action" onClick={() => { updateRole({ ...role, icon: '' }); setIconsOpen(false); }}>Initial</button>{DELEGATION_ICONS.map(icon => <button key={icon} className="delegation-icon" aria-label={`${icon} icon`} aria-pressed={role.icon === icon} onClick={() => { updateRole({ ...role, icon }); setIconsOpen(false); }}><DelegationRoleIcon icon={icon} name={role.name} /></button>)}</div>}
        <label className="delegation-field">When to choose this role<input value={role.description} onChange={e => updateRole({ ...role, description: e.target.value })} /><span className="settings-hint">Helps the deciding agent match this role to the task.</span></label>
        <details className="delegation-behavior" open key={role.id}><summary>Instructions and stopping point</summary><p className="settings-hint">Tells the delegated agent how to do the work.</p>
          <label className="delegation-field">Instructions for the delegated agent<textarea value={role.instructions} onChange={e => updateRole({ ...role, instructions: e.target.value })} /></label>
          <label className="delegation-field">Stopping point<textarea value={role.stopping_point} onChange={e => updateRole({ ...role, stopping_point: e.target.value })} /></label>
          <p className="settings-hint">These apply to every model choice in this role.</p>
        </details>
        <div className="delegation-row between"><h3>Model choices</h3><button className="settings-action" data-testid="delegation-add-choice" onClick={addChoice}>+ Add alternative</button></div>
        <p className="settings-hint">Use a faster model for clear steps and easy verification. Add a stronger model or more effort for ambiguity and difficult verification.</p>
        {[...role.choices].sort((a, b) => Number(b.id === role.default_choice_id) - Number(a.id === role.default_choice_id)).map(choice => {
          const isDefault = role.default_choice_id === choice.id;
          const setChoice = (next: DelegationChoice) => updateRole({ ...role, choices: role.choices.map(c => c.id === next.id ? next : c) });
          return <section className="delegation-choice" key={choice.id}>
            <div className="delegation-row between"><div><h4>{choice.name} <span className="settings-pill">{isDefault ? 'Default' : 'Alternative'}</span></h4><p className="settings-hint">{isDefault ? 'Use unless another choice fits the task.' : choice.when || 'Add a condition for this alternative.'}</p><p className="delegation-route">{route(choice.selection)}</p></div><button className="settings-action" aria-expanded={choiceID === choice.id} onClick={() => setChoiceID(choiceID === choice.id ? '' : choice.id)}>{choiceID === choice.id ? 'Close' : 'Edit'}</button></div>
            {choiceID === choice.id && <div className="delegation-choice-body">
              <label className="delegation-field">Choice name<input value={choice.name} onChange={e => setChoice({ ...choice, name: e.target.value })} /></label>
              {!isDefault && <label className="delegation-field">Use when<textarea value={choice.when} onChange={e => setChoice({ ...choice, when: e.target.value })} placeholder="For example: requirements are ambiguous or verification is difficult." /></label>}
              {picker(choice.selection, selection => setChoice({ ...choice, selection }))}
              <div className="delegation-row">
                {!isDefault && <button className="settings-action" onClick={() => { setUndo(structuredClone(config)); updateRole({ ...role, default_choice_id: choice.id }); }}>Make default</button>}
                <button className="settings-action" onClick={() => { const copy = { ...structuredClone(choice), id: id('choice'), name: `${choice.name} copy` }; updateRole({ ...role, choices: [...role.choices, copy] }); setChoiceID(copy.id); }}>Duplicate</button>
                {!isDefault && <button className="settings-action danger" onClick={() => { setUndo(structuredClone(config)); updateRole({ ...role, choices: role.choices.filter(c => c.id !== choice.id) }); }}>Remove alternative</button>}
              </div>
            </div>}
          </section>;
        })}
        <p className="settings-hint">Explicit model and effort requests apply to that request only. Saved defaults stay unchanged.</p>
      </> : <>
        {missing && <section className="delegation-starter"><h3>Start with one model</h3><p className="settings-description">Fill unconfigured defaults, then customize each role.</p>{picker(starter, setStarter)}<button className="settings-action" data-testid="delegation-apply-starter" disabled={!starter.harness} onClick={() => update({ ...config, roles: config.roles.map(r => ({ ...r, choices: r.choices.map(c => c.id === r.default_choice_id && !c.selection.harness ? { ...c, selection: structuredClone(starter) } : c) })), fallback: config.fallback.selection.harness ? config.fallback : { ...config.fallback, selection: structuredClone(starter) } })}>Use for unconfigured choices</button></section>}
        <div className="delegation-row between"><h3>Roles</h3><button className="settings-action" onClick={addRole}>+ New role</button></div>
        {config.roles.map(r => <div className={`delegation-role-row ${r.enabled ? '' : 'disabled'}`} key={r.id}><span className="delegation-icon"><DelegationRoleIcon icon={r.icon} name={r.name} /></span><div className="delegation-role-summary"><h4>{r.name}</h4><p className="settings-description">{r.description}</p><p className="delegation-route">{route(r.choices.find(c => c.id === r.default_choice_id)?.selection ?? emptySelection())}{r.choices.length > 1 ? ` · ${r.choices.length - 1} alternatives` : ''}</p></div><button className="settings-action" aria-label={`Edit ${r.name}`} onClick={() => { setRoleID(r.id); setChoiceID(r.default_choice_id); setIconsOpen(false); }}>Edit</button><input type="checkbox" aria-label={`Enable ${r.name}`} checked={r.enabled} onChange={e => updateRole({ ...r, enabled: e.target.checked })} /></div>)}
        <button className="settings-action" disabled={state.templates.every(t => config.roles.some(r => r.id === t.id))} onClick={addTemplates}>Add starter roles</button>
        <p className="settings-hint">Scout, Design, Build, Ship, and Review are starting points. Edit, disable, or replace any of them.</p>
      </>}
    </fieldset>}
    {undo && <div role="status" className="delegation-row"><span>Change ready to undo.</span><button className="settings-action" onClick={() => { update({ ...undo, revision: config.revision }); setUndo(null); }}>Undo</button></div>}
    <div className="delegation-save"><span role="status">{busy ? 'Saving…' : dirty ? 'Unsaved changes' : 'Saved'}</span><button className="settings-action" disabled={busy || (!dirty && !changedElsewhere)} onClick={() => { setUndo(null); void reload(true); }}>Reload saved settings</button><button className="settings-action primary" data-testid="delegation-save" disabled={busy || !dirty || changedElsewhere} onClick={() => void persist(config)}>Save preferences</button></div>
  </div>;
}
