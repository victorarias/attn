/**
 * Live document queries: the `doc_subscription_delivery` and
 * `doc_subscription_ended` events, and the registry that says which subscriber
 * each one belongs to.
 *
 * Deliberately not `daemonPendingRequests`: that module correlates one command
 * with one `*_result`, and a subscription is a stream — many deliveries under
 * one id, ended by either side.
 *
 * The registry is also what survives a reconnect. A subscription lives on the
 * daemon's connection, so a dropped socket ends every one of them there; here
 * they are still wanted, and the hook resubscribes each on the next open. Each
 * subscriber is asked for its `have()` at that moment, so the resume carries
 * only what changed while the socket was down.
 */

import type { StoredDocument } from '../types/generated';

export interface DocumentRevision {
  id: string;
  rev: number;
}

/** A query, as the wire takes it. `after` is absent by construction: a live query is a window. */
export interface DocumentQueryRequest {
  namespace: string;
  collection: string;
  filters?: Array<{ field: string; op: string; value: unknown }>;
  sort?: { field: string; desc?: boolean };
  limit?: number;
}

/**
 * One delivery. The client rule is the whole contract: render `order`, take each
 * body from `upsert` if it is there else from your cache, forget every cached
 * document not named in `order`.
 */
export interface DocumentDelivery {
  delivery: number;
  asOfSeq: number;
  order: string[];
  upsert: StoredDocument[];
}

export interface DocumentSubscriber {
  request: DocumentQueryRequest;
  /** What this subscriber holds, read at every (re)subscribe. */
  have: () => DocumentRevision[];
  onDelivery: (delivery: DocumentDelivery) => void;
  /** The daemon will not answer this query again as written. Terminal. */
  onEnded: (code: string, message: string) => void;
  /** Whether the daemon is serving this subscription right now. */
  onLive: (live: boolean) => void;
}

/** The event shapes this module reads, loosely typed off the wire union. */
type DocumentEvent = {
  event: string;
  subscription_id?: unknown;
  delivery?: unknown;
  as_of_seq?: unknown;
  order?: unknown;
  upsert?: unknown;
  code?: unknown;
  error?: unknown;
};

/** What the registry needs to put a command on the wire, when there is one. */
export type DocumentCommandSender = (payload: Record<string, unknown>) => void;

export function documentSubscribePayload(id: string, sub: DocumentSubscriber): Record<string, unknown> {
  const have = sub.have();
  return {
    cmd: 'doc_subscribe',
    subscription_id: id,
    query: {
      namespace: sub.request.namespace,
      collection: sub.request.collection,
      filters: (sub.request.filters ?? []).map((f) => ({
        field: f.field,
        op: f.op,
        // The one polymorphic leaf on the wire: a filter's bound travels as JSON
        // text so the daemon can type-check it against the declared field.
        value_json: JSON.stringify(f.value ?? null),
      })),
      sort: sub.request.sort ? { field: sub.request.sort.field, desc: !!sub.request.sort.desc } : undefined,
      limit: sub.request.limit,
    },
    have: have.length > 0 ? have : undefined,
  };
}

/**
 * The live subscriptions this client holds, by the id it minted for each. Ids
 * are minted here and never reused, so a delivery from a subscription the client
 * has already dropped names nothing and is discarded.
 */
export class DocumentSubscriptions {
  private subs = new Map<string, DocumentSubscriber>();
  private nextId = 0;

  /** Registers a subscriber and returns the id its deliveries will carry. */
  add(sub: DocumentSubscriber): string {
    this.nextId += 1;
    const id = `docsub-${this.nextId}`;
    this.subs.set(id, sub);
    return id;
  }

  remove(id: string): boolean {
    return this.subs.delete(id);
  }

  entries(): Array<[string, DocumentSubscriber]> {
    return Array.from(this.subs.entries());
  }

  get size(): number {
    return this.subs.size;
  }

  /** Re-sends every wanted subscription over a freshly opened socket. */
  resubscribeAll(send: DocumentCommandSender): void {
    for (const [id, sub] of this.subs) {
      send(documentSubscribePayload(id, sub));
      sub.onLive(true);
    }
  }

  /** The socket went away, so nothing is being served. The subscriptions stay wanted. */
  markDisconnected(): void {
    for (const sub of this.subs.values()) {
      sub.onLive(false);
    }
  }

  /**
   * Handle one document event. Returns false when the event is not one of ours,
   * so the caller can keep its own dispatch exhaustive.
   */
  handleEvent(event: DocumentEvent): boolean {
    switch (event.event) {
      case 'doc_subscription_delivery': {
        const sub = this.subs.get(String(event.subscription_id ?? ''));
        if (!sub) return true;
        sub.onDelivery({
          delivery: typeof event.delivery === 'number' ? event.delivery : 0,
          asOfSeq: typeof event.as_of_seq === 'number' ? event.as_of_seq : 0,
          order: Array.isArray(event.order) ? (event.order as string[]) : [],
          upsert: Array.isArray(event.upsert) ? (event.upsert as StoredDocument[]) : [],
        });
        return true;
      }

      case 'doc_subscription_ended': {
        const id = String(event.subscription_id ?? '');
        const sub = this.subs.get(id);
        if (!sub) return true;
        // The daemon has already dropped it, so there is nothing to unsubscribe
        // from: forget it here before telling the subscriber, so a resubscribe it
        // starts in response mints a fresh id rather than colliding with this one.
        this.subs.delete(id);
        sub.onEnded(
          typeof event.code === 'string' ? event.code : '',
          typeof event.error === 'string' ? event.error : 'The subscription ended.',
        );
        return true;
      }

      default:
        return false;
    }
  }
}
