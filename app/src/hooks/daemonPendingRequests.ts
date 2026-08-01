/**
 * Request/result correlation for daemon commands.
 *
 * A fallible daemon command carries a `request_id`; the daemon answers with a
 * `<command>_result` event carrying the same id. The frontend parks the
 * promise's resolve/reject under `<kind>:<requestId>` until that event lands.
 * That key format is the whole protocol, and until this module existed it was
 * spelled out inline at every one of ~115 call sites with no name to search for.
 *
 * See "WebSocket and state" in AGENTS.md.
 */

/**
 * The parked half of a command's promise, keyed by `pendingRequestKey`.
 *
 * One map holds resolvers for every command, so `resolve` is unavoidably
 * untyped here — TypeScript has no existential type to say "some T, and the
 * waiter and the settler agree on it". `settlePendingRequest` is the typed
 * entry point; prefer it over reaching into the map.
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
 * Settle the request a `*_result` event answers.
 *
 * Resolves with `extract(event)` when the daemon reports success, rejects with
 * the daemon's error otherwise, and reports whether a waiter was actually found
 * — an event whose request already timed out, or that belongs to another client,
 * is not an error.
 *
 * `extract` returning `undefined` counts as failure. Several results are only
 * meaningful with a payload (`success && data.result`), and a silent
 * `resolve(undefined)` would hand the caller a shape it never checks for.
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
