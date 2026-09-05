import { expect, it } from 'vitest';
import { handleDelegationDaemonEvent } from './daemonDelegationEvents';
import type { PendingRequests } from './daemonPendingRequests';

it('correlates save errors without consuming another client request', async () => {
  const pending: PendingRequests = new Map();
  const result = new Promise((resolve, reject) => pending.set('delegation_preferences_save:mine', { resolve, reject }));
  handleDelegationDaemonEvent({ event: 'delegation_preferences_result', request_id: 'other', success: false, error: 'stale' }, pending);
  expect(pending.size).toBe(1);
  handleDelegationDaemonEvent({ event: 'delegation_preferences_result', request_id: 'mine', success: false, error: 'Preferences changed' }, pending);
  await expect(result).rejects.toThrow('Preferences changed');
  expect(pending.size).toBe(0);
});

it('distinguishes a successful empty catalog from discovery failure', async () => {
  for (const success of [true, false]) {
    const pending: PendingRequests = new Map();
    const result = new Promise((resolve, reject) => pending.set('delegation_models:models', { resolve, reject }));
    handleDelegationDaemonEvent({ event: 'delegation_models_result', request_id: 'models', success, models: [], error: success ? undefined : 'Harness unavailable', detail: 'No discovery API' }, pending);
    if (success) await expect(result).resolves.toEqual({ models: [], detail: 'No discovery API' });
    else await expect(result).rejects.toThrow('Harness unavailable');
  }
});
