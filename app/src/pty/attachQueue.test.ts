import { describe, expect, it } from 'vitest';
import { enqueuePerKey } from './attachQueue';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('enqueuePerKey', () => {
  it('does not start the next task for a key until the prior one settles', async () => {
    const chains = new Map<string, Promise<unknown>>();
    const first = deferred<string>();
    const order: string[] = [];

    const a = enqueuePerKey(chains, 's1', () => {
      order.push('a:start');
      return first.promise;
    });
    const b = enqueuePerKey(chains, 's1', async () => {
      order.push('b:start');
      return 'b';
    });

    // The chain wrapper defers task start by a couple of microtasks.
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    expect(order).toEqual(['a:start']);

    first.resolve('a');
    await expect(a).resolves.toBe('a');
    await expect(b).resolves.toBe('b');
    expect(order).toEqual(['a:start', 'b:start']);
  });

  it('runs the next task even when the prior one rejects, without leaking the rejection', async () => {
    const chains = new Map<string, Promise<unknown>>();
    const a = enqueuePerKey(chains, 's1', () => Promise.reject(new Error('attach timed out')));
    const b = enqueuePerKey(chains, 's1', async () => 'recovered');

    await expect(a).rejects.toThrow('attach timed out');
    await expect(b).resolves.toBe('recovered');
  });

  it('keeps different keys independent', async () => {
    const chains = new Map<string, Promise<unknown>>();
    const blocked = deferred<string>();
    const order: string[] = [];

    void enqueuePerKey(chains, 's1', () => blocked.promise);
    const other = enqueuePerKey(chains, 's2', async () => {
      order.push('s2');
      return 's2';
    });

    await expect(other).resolves.toBe('s2');
    expect(order).toEqual(['s2']);
    blocked.resolve('s1');
  });

  it('cleans up the chain entry once the last task settles', async () => {
    const chains = new Map<string, Promise<unknown>>();
    await enqueuePerKey(chains, 's1', async () => 'done');
    // finally() cleanup runs a microtask after settlement.
    await Promise.resolve();
    await Promise.resolve();
    expect(chains.size).toBe(0);
  });
});
