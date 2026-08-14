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
  /** The handler's return value, still JSON text, absent when it returned nothing. */
  payload?: unknown;
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
