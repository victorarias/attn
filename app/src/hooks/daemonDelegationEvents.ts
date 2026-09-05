import type { DelegationPreferences, DelegationRole, DelegationHarness, DelegationModel } from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';
import { useDelegationPreferencesPush } from '../store/delegationPreferences';

export interface DelegationSettingsState {
  preferences: DelegationPreferences;
  templates: DelegationRole[];
  harnesses: DelegationHarness[];
}
export interface DelegationModelCatalog { models: DelegationModel[]; detail: string }
interface DelegationEvent {
  event?: string;
  success?: boolean;
  error?: string;
  request_id?: string;
  preferences?: DelegationPreferences;
  templates?: DelegationRole[];
  harnesses?: DelegationHarness[];
  models?: DelegationModel[];
  detail?: string;
  revision?: number;
}

export function handleDelegationDaemonEvent(event: DelegationEvent, pending: PendingRequests): boolean {
  if (event.event === 'delegation_preferences_result') {
    const extract = (value: DelegationEvent): DelegationSettingsState | undefined => value.preferences ? {
      preferences: value.preferences, templates: value.templates ?? [], harnesses: value.harnesses ?? [],
    } : undefined;
    if (!settlePendingRequest(pending, 'delegation_preferences_get', event, extract, 'Reading delegation preferences failed')) {
      settlePendingRequest(pending, 'delegation_preferences_save', event, extract, 'Saving delegation preferences failed');
    }
    return true;
  }
  if (event.event === 'delegation_models_result') {
    settlePendingRequest(pending, 'delegation_models', event,
      value => value.models ? { models: value.models, detail: value.detail ?? '' } : undefined,
      'Discovering models failed');
    return true;
  }
  if (event.event === 'delegation_preferences_changed') {
    if (typeof event.revision === 'number') useDelegationPreferencesPush.getState().push(event.revision);
    return true;
  }
  return false;
}
