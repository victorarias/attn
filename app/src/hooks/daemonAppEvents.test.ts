import { describe, expect, it } from 'vitest';
import { AppCommandError, handleAppDaemonEvent } from './daemonAppEvents';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';

// A refused command has to reach the view as something it can branch on. A
// rebuild in progress is worth retrying; a handler that threw is not, and the
// two arrive as the same shape of failure with different codes.

function parked(pending: PendingRequests, requestId: string): Promise<unknown> {
  return new Promise((resolve, reject) => {
    pending.set(pendingRequestKey('app_command', requestId), { resolve, reject });
  });
}

describe('handleAppDaemonEvent', () => {
  it('rejects a refused command with the daemon code and reason', async () => {
    const pending: PendingRequests = new Map();
    const settled = parked(pending, 'req-1');

    const handled = handleAppDaemonEvent(
      {
        event: 'app_command_result',
        request_id: 'req-1',
        success: false,
        error: 'approval-gate is rebuilding its collections',
        error_code: 'reconcile_owed',
        reconcile: { reason: 'version_changed', through_seq: 42 },
      },
      pending,
    );

    expect(handled).toBe(true);
    const error = await settled.then(
      () => new Error('the command resolved instead of rejecting'),
      (err: unknown) => err,
    );
    expect(error).toBeInstanceOf(AppCommandError);
    expect((error as AppCommandError).code).toBe('reconcile_owed');
    expect((error as AppCommandError).message).toBe('approval-gate is rebuilding its collections');
    expect((error as AppCommandError).reconcile).toEqual({
      reason: 'version_changed',
      through_seq: 42,
    });
  });

  it('rejects a failure with no code as a plain error', async () => {
    const pending: PendingRequests = new Map();
    const settled = parked(pending, 'req-2');

    handleAppDaemonEvent(
      {
        event: 'app_command_result',
        request_id: 'req-2',
        success: false,
        error: 'approval-gate threw running command "forget"',
      },
      pending,
    );

    const error = await settled.then(
      () => new Error('the command resolved instead of rejecting'),
      (err: unknown) => err,
    );
    expect(error).not.toBeInstanceOf(AppCommandError);
    expect((error as Error).message).toBe('approval-gate threw running command "forget"');
  });

  it('resolves a successful command with its payload', async () => {
    const pending: PendingRequests = new Map();
    const settled = parked(pending, 'req-3');

    handleAppDaemonEvent(
      {
        event: 'app_command_result',
        request_id: 'req-3',
        success: true,
        payload: '{"forgotten":2}',
      },
      pending,
    );

    expect(await settled).toEqual({ value: { forgotten: 2 } });
  });
});
