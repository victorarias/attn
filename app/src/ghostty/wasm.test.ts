import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  constructedWith: [] as WebAssembly.Instance[],
}));

vi.mock('ghostty-web', () => ({
  Ghostty: class {
    constructor(instance: WebAssembly.Instance) {
      mocks.constructedWith.push(instance);
    }
  },
}));

import { loadGhostty, resetGhosttyModuleCacheForTests } from './wasm';

describe('loadGhostty', () => {
  beforeEach(() => {
    resetGhosttyModuleCacheForTests();
    mocks.constructedWith.length = 0;
  });

  it('compiles once and creates a separate WASM instance per caller', async () => {
    const module = {} as WebAssembly.Module;
    const instances = [
      { exports: { memory: new WebAssembly.Memory({ initial: 1 }) } },
      { exports: { memory: new WebAssembly.Memory({ initial: 1 }) } },
    ] as WebAssembly.Instance[];
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      statusText: 'OK',
      arrayBuffer: async () => new ArrayBuffer(8),
    })));
    const compile = vi.spyOn(WebAssembly, 'compile').mockResolvedValue(module);
    const instantiate = vi.spyOn(WebAssembly, 'instantiate')
      .mockResolvedValueOnce(instances[0])
      .mockResolvedValueOnce(instances[1]);

    await Promise.all([loadGhostty(), loadGhostty()]);

    expect(fetch).toHaveBeenCalledTimes(1);
    expect(compile).toHaveBeenCalledTimes(1);
    expect(instantiate).toHaveBeenCalledTimes(2);
    expect(mocks.constructedWith).toEqual(instances);
  });
});
