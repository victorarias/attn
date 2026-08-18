// App views acting: the one result an `app_command` waits on.
//
// Its own module rather than a case in the hook's switch, for the same reason
// the bus and notebook domains have one — the switch is already long and this
// domain is self-contained. Reached from the switch's `default` chain.

import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

interface AppDaemonEvent {
  event?: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  /** A stable name for the refusal, when the daemon had one. */
  error_code?: unknown;
  /** Present with error_code `reconcile_owed`: the rebuild the app owes. */
  reconcile?: unknown;
  /** The handler's return value, still JSON text, absent when it returned nothing. */
  payload?: unknown;
}

/**
 * A refusal a view can branch on. The message is the surface a person reads; the
 * code is what tells "retry once the rebuild finishes" apart from "this app's
 * handler is broken", which no two prose strings can be relied on to do.
 */
export class AppCommandError extends Error {
  readonly code: string;
  readonly reconcile: unknown;

  constructor(message: string, code: string, reconcile: unknown) {
    super(message);
    this.name = 'AppCommandError';
    this.code = code;
    this.reconcile = reconcile;
  }
}

/**
 * What a settled command carries. The value is wrapped rather than returned bare
 * because a command that succeeds and returns nothing is normal, and
 * `settlePendingRequest` reads a bare `undefined` as failure.
 */
export interface AppCommandResult {
  value: unknown;
}

/**
 * Settles an app-command result, or returns false for an event this module does
 * not own so the caller can keep looking.
 */
export function handleAppDaemonEvent(event: AppDaemonEvent, pending: PendingRequests): boolean {
  if (event.event !== 'app_command_result') return false;
  if (event.success === false && typeof event.error_code === 'string' && event.error_code !== '') {
    settlePendingRequest(
      pending,
      'app_command',
      { ...event, success: false },
      () => undefined,
      'The command was refused',
      new AppCommandError(
        event.error || 'The command was refused',
        event.error_code,
        event.reconcile,
      ),
    );
    return true;
  }
  settlePendingRequest(
    pending,
    'app_command',
    event,
    (settled): AppCommandResult | undefined => {
      if (typeof settled.payload !== 'string') return { value: undefined };
      try {
        return { value: JSON.parse(settled.payload) };
      } catch {
        // The daemon forwards what the handler returned without reading it, so a
        // body that will not parse is the app's bug — and rejecting says so
        // where the view can show it, instead of throwing in the socket's own
        // message handler and leaving the caller to time out.
        return undefined;
      }
    },
    'The command answered with something that is not JSON',
  );
  return true;
}
