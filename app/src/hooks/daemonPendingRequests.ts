/**
 * Request/result correlation for daemon commands: a command's promise parks
 * under `<kind>:<requestId>` until the matching `<command>_result` event lands.
 * See "WebSocket and state" in AGENTS.md.
 */

/**
 * The parked half of a command's promise, keyed by `pendingRequestKey`. One map
 * holds every command's resolver, so `resolve` cannot be typed here; go through
 * `settlePendingRequest` rather than reaching into the map.
 */
export interface PendingRequest {
  resolve: (result: any) => void;
  reject: (error: Error) => void;
}

export type PendingRequests = Map<string, PendingRequest>;

export interface PendingKeyedRequest extends PendingRequest {
  requestId: string;
}

export type PendingKeyedRequests = Map<string, PendingKeyedRequest>;

/** `<kind>:<requestId>` — the correlation key both sides of a command agree on. */
export function pendingRequestKey(kind: string, requestId: string): string {
  return `${kind}:${requestId}`;
}

/** Park one last-writer-wins request and reject the waiter it supersedes. */
export function sendKeyedRequest<T>(
  pending: PendingKeyedRequests,
  key: string,
  requestId: string,
  send: () => void,
  timeoutMessage: string,
  timeoutMs: number,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    pending.get(key)?.reject(new Error('Superseded by a newer request'));
    pending.set(key, {
      requestId,
      resolve: resolve as (value: unknown) => void,
      reject,
    });
    send();
    setTimeout(() => {
      if (pending.get(key)?.requestId === requestId) {
        pending.delete(key);
        reject(new Error(timeoutMessage));
      }
    }, timeoutMs);
  });
}

/** Take the waiter only when the result still answers its request id. */
export function takeKeyedRequest(
  pending: PendingKeyedRequests,
  key: string,
  requestId: unknown,
): PendingRequest | undefined {
  const waiter = pending.get(key);
  if (!waiter || waiter.requestId !== requestId) {
    return undefined;
  }
  pending.delete(key);
  return waiter;
}

/** The fields every `*_result` event carries, whatever else it adds. */
interface ResultEvent {
  request_id?: unknown;
  success?: boolean;
  error?: string;
}

/**
 * Settle the request a `*_result` event answers; returns whether a waiter was
 * found (a timed-out or other client's request is not an error). `extract`
 * returning `undefined` counts as failure, never `resolve(undefined)`.
 *
 * `failure` rejects with that error rather than a plain one built from the
 * event's text, for a domain whose refusals carry a code the caller branches on.
 */
export function settlePendingRequest<E extends ResultEvent, T>(
  pending: PendingRequests,
  kind: string,
  event: E,
  extract: (event: E) => T | undefined,
  failureMessage: string,
  failure?: Error,
): boolean {
  const requestId = event.request_id;
  if (typeof requestId !== 'string') {
    return false;
  }
  const key = pendingRequestKey(kind, requestId);
  const waiter = pending.get(key);
  if (!waiter) {
    return false;
  }
  pending.delete(key);
  const value = event.success ? extract(event) : undefined;
  if (value === undefined) {
    waiter.reject(failure ?? new Error(event.error || failureMessage));
  } else {
    waiter.resolve(value);
  }
  return true;
}
