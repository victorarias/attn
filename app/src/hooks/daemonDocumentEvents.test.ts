import { describe, expect, it, vi } from 'vitest';
import {
  DocumentSubscriptions,
  documentSubscribePayload,
  type DocumentSubscriber,
} from './daemonDocumentEvents';

// The registry's whole job: route each delivery to the right subscriber, forget
// one the daemon has ended, and remember the rest across a reconnect so they can
// be re-sent — carrying what the subscriber holds at that moment, not what it
// held when it first subscribed.

function subscriber(overrides: Partial<DocumentSubscriber> = {}): DocumentSubscriber {
  return {
    request: { namespace: 'app/approval-gate', collection: 'requests' },
    have: () => [],
    onDelivery: vi.fn(),
    onEnded: vi.fn(),
    onLive: vi.fn(),
    ...overrides,
  };
}

describe('DocumentSubscriptions', () => {
  it('routes a delivery to the subscription that asked for it', () => {
    const registry = new DocumentSubscriptions();
    const first = subscriber();
    const second = subscriber();
    const firstId = registry.add(first);
    registry.add(second);

    const handled = registry.handleEvent({
      event: 'doc_subscription_delivery',
      subscription_id: firstId,
      delivery: 1,
      as_of_seq: 42,
      order: ['a'],
      upsert: [{ id: 'a', body: '{}', rev: 1, created_at: '', updated_at: '' }],
    });

    expect(handled).toBe(true);
    expect(first.onDelivery).toHaveBeenCalledWith({
      delivery: 1,
      asOfSeq: 42,
      order: ['a'],
      upsert: [{ id: 'a', body: '{}', rev: 1, created_at: '', updated_at: '' }],
    });
    expect(second.onDelivery).not.toHaveBeenCalled();
  });

  it('leaves events it does not own to the rest of the dispatch', () => {
    const registry = new DocumentSubscriptions();
    expect(registry.handleEvent({ event: 'fs_changed' })).toBe(false);
  });

  it('ignores a delivery for a subscription this client has already dropped', () => {
    const registry = new DocumentSubscriptions();
    const sub = subscriber();
    const id = registry.add(sub);
    registry.remove(id);

    expect(registry.handleEvent({
      event: 'doc_subscription_delivery',
      subscription_id: id,
      delivery: 1,
      as_of_seq: 1,
      order: [],
      upsert: [],
    })).toBe(true);
    expect(sub.onDelivery).not.toHaveBeenCalled();
  });

  it('forgets an ended subscription before telling the subscriber', () => {
    const registry = new DocumentSubscriptions();
    // A subscriber that resubscribes in response must get a fresh id, not the
    // one the daemon has just dropped.
    let idAtEnding = '';
    const sub = subscriber({
      onEnded: () => {
        idAtEnding = registry.add(subscriber());
      },
    });
    const id = registry.add(sub);

    registry.handleEvent({
      event: 'doc_subscription_ended',
      subscription_id: id,
      code: 'collection_undefined',
      error: 'gone',
    });

    expect(idAtEnding).not.toBe(id);
    expect(registry.size).toBe(1);
  });

  it('re-sends every wanted subscription on a new socket, with what it holds now', () => {
    const registry = new DocumentSubscriptions();
    let held = [{ id: 'a', rev: 1 }];
    const sub = subscriber({ have: () => held });
    const id = registry.add(sub);

    held = [{ id: 'a', rev: 7 }];
    const sent: Array<Record<string, unknown>> = [];
    registry.resubscribeAll((payload) => sent.push(payload));

    expect(sent).toHaveLength(1);
    expect(sent[0].cmd).toBe('doc_subscribe');
    expect(sent[0].subscription_id).toBe(id);
    expect(sent[0].have).toEqual([{ id: 'a', rev: 7 }]);
    expect(sub.onLive).toHaveBeenCalledWith(true);
  });

  it('tells every subscriber it is no longer being served, and keeps it', () => {
    const registry = new DocumentSubscriptions();
    const sub = subscriber();
    registry.add(sub);

    registry.markDisconnected();

    expect(sub.onLive).toHaveBeenCalledWith(false);
    expect(registry.size).toBe(1);
  });
});

describe('documentSubscribePayload', () => {
  it('sends a filter bound as JSON text, which is what the daemon type-checks', () => {
    const payload = documentSubscribePayload('sub-1', subscriber({
      request: {
        namespace: 'app/approval-gate',
        collection: 'requests',
        filters: [{ field: 'status', op: 'eq', value: 'pending' }],
        sort: { field: 'updated_at', desc: true },
        limit: 20,
      },
    }));

    expect(payload.query).toEqual({
      namespace: 'app/approval-gate',
      collection: 'requests',
      filters: [{ field: 'status', op: 'eq', value_json: '"pending"' }],
      sort: { field: 'updated_at', desc: true },
      limit: 20,
    });
  });

  it('omits have entirely when the subscriber holds nothing', () => {
    const payload = documentSubscribePayload('sub-1', subscriber());
    expect(payload.have).toBeUndefined();
  });
});
