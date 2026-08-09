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

/** `<kind>:<requestId>` — the correlation key both sides of a command agree on. */
export function pendingRequestKey(kind: string, requestId: string): string {
  return `${kind}:${requestId}`;
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
 */
export function settlePendingRequest<E extends ResultEvent, T>(
  pending: PendingRequests,
  kind: string,
  event: E,
  extract: (event: E) => T | undefined,
  failureMessage: string,
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
    waiter.reject(new Error(event.error || failureMessage));
  } else {
    waiter.resolve(value);
  }
  return true;
}
