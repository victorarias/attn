import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { DelegationSettings } from './DelegationSettings';
import { useDelegationPreferences } from '../hooks/useDelegationPreferences';
import { createMockDaemon } from '../test/mocks/daemon';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';
import type { DelegationSettingsState, DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { DelegationPreferences } from '../types/generated';

function setup(enabled = false) {
  const role = { id: 'build', name: 'Build', icon: 'code', enabled: true, description: 'Implement a change', instructions: 'Run relevant tests', stopping_point: 'Return for review', default_choice_id: 'default', choices: [{ id: 'default', name: 'Everyday', when: '', selection: { harness: '', provider: '', model: '', effort: '' } }] };
  let state: DelegationSettingsState = { preferences: { enabled, revision: 0, roles: enabled ? [role] : [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } }, templates: [role], harnesses: [{ id: 'codex', name: 'Codex', available: true, model_pin: true, effort_pin: true, discovery: true }] };
  const daemon = createMockDaemon();
  daemon.setResponse('load', () => structuredClone(state));
  daemon.setResponse('save', (args: unknown[]) => {
    state = { ...state, preferences: { ...structuredClone(args[0] as DelegationPreferences), revision: state.preferences.revision + 1 } };
    return structuredClone(state);
  });
  daemon.setResponse('models', { models: [{ harness: 'codex', provider: '', id: 'model-a', name: 'Everyday model', description: '', effort_support: 'supported', effort_levels: ['medium', 'high'], access: 'unknown', detail: '' }], detail: 'Reported by Codex' });
  const load = daemon.createRequest<DelegationSettingsState>('load');
  const save = daemon.createRequest<DelegationSettingsState>('save');
  const models = daemon.createRequest<DelegationModelCatalog>('models');
  function Harness() { const policy = useDelegationPreferences(true, load, save); return <DelegationSettings policy={policy} loadModels={models} />; }
  render(<Harness />);
  return { daemon, getState: () => state, externalChange: () => { state = { ...state, preferences: { ...state.preferences, revision: state.preferences.revision + 1 } }; useDelegationPreferencesPush.getState().push(state.preferences.revision); } };
}

afterEach(() => { cleanup(); useDelegationPreferencesPush.getState().clear(); });

it('starts off, opts in to starter roles, discovers on request and preserves edits when disabled', async () => {
  const { daemon, getState } = setup();
  await screen.findByTestId('delegation-settings');
  expect(daemon.getCalls('load')).toHaveLength(1);
  expect(daemon.getCalls('models')).toHaveLength(0);
  fireEvent.click(screen.getByRole('checkbox', { name: 'Off' }));
  await screen.findByRole('button', { name: 'Edit Build' });
  fireEvent.change(screen.getByLabelText('Harness'), { target: { value: 'codex' } });
  fireEvent.click(screen.getByRole('button', { name: 'Discover models' }));
  await screen.findByText('Reported by Codex');
  expect(daemon.getCalls('models').map(c => c.args)).toEqual([['codex']]);
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: JSON.stringify(['', 'model-a']) } });
  fireEvent.change(screen.getByLabelText('Effort'), { target: { value: 'medium' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use for unconfigured choices' }));
  fireEvent.click(screen.getByRole('button', { name: 'Edit Build' }));
  expect(screen.getByText('Instructions and stopping point').closest('details')).toHaveAttribute('open');
  fireEvent.change(screen.getByLabelText('Instructions for the delegated agent'), { target: { value: 'Verify the migration' } });
  fireEvent.click(screen.getByRole('button', { name: '+ Add alternative' }));
  fireEvent.change(screen.getByLabelText('Choice name'), { target: { value: 'Hard verification' } });
  fireEvent.change(screen.getByLabelText('Use when'), { target: { value: 'Verification is difficult' } });
  fireEvent.change(screen.getByLabelText('Effort'), { target: { value: 'high' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save preferences' }));
  await waitFor(() => expect(daemon.getCalls('save')).toHaveLength(2));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save preferences' })).toBeDisabled());
  const saved = getState().preferences;
  expect(saved.roles[0].choices.map(c => c.selection.effort)).toEqual(['medium', 'high']);
  expect(saved.roles[0].instructions).toBe('Verify the migration');
  fireEvent.click(screen.getByRole('checkbox', { name: 'On' }));
  await screen.findByRole('checkbox', { name: 'Off' });
  expect(getState().preferences.roles).toEqual(saved.roles);
  expect(daemon.getCalls('models')).toHaveLength(1);
});

it('preserves a dirty draft when another client saves and offers an explicit reload', async () => {
  const { externalChange, daemon } = setup(true);
  fireEvent.click(await screen.findByRole('button', { name: 'Edit Build' }));
  fireEvent.change(screen.getByLabelText('Role name'), { target: { value: 'My builder' } });
  await act(async () => externalChange());
  await screen.findByRole('alert');
  expect(screen.getByLabelText('Role name')).toHaveValue('My builder');
  expect(screen.getByRole('button', { name: 'Save preferences' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Reload saved settings' }));
  await waitFor(() => expect(screen.getByLabelText('Role name')).toHaveValue('Build'));
  expect(daemon.getCalls('load')).toHaveLength(3);
  expect(daemon.getCalls('save')).toHaveLength(0);
});
