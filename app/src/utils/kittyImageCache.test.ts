// The blob cache: what gets asked for, what gets kept, and what stops being
// asked for. Every test here runs against its own cache instance rather than the
// module singleton, so one test's eviction cannot decide another's outcome.

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  KittyImageCache,
  kittyImageBlobFromResult,
  type KittyImageBlob,
} from './kittyImageCache';

function blob(overrides: Partial<KittyImageBlob> = {}): KittyImageBlob {
  const pixels = overrides.pixels ?? new Uint8Array(12);
  return {
    sessionId: 'sess',
    imageId: 1,
    generation: 10,
    width: 2,
    height: 2,
    format: 'rgb',
    ...overrides,
    pixels,
  };
}

/** A cache wired to a sender that records what it was asked for. */
function cacheWithSender(capacityBytes?: number) {
  const sent: Array<[string, number]> = [];
  const cache = new KittyImageCache(capacityBytes);
  cache.setSender((sessionId, imageId) => {
    sent.push([sessionId, imageId]);
    return true;
  });
  return { cache, sent };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('KittyImageCache requests', () => {
  it('asks once for a key it lacks', () => {
    const { cache, sent } = cacheWithSender();

    cache.ensure('sess', 7, 10);

    expect(sent).toEqual([['sess', 7]]);
    expect(cache.status('sess', 7, 10)).toBe('pending');
  });

  it('does not ask twice while an answer is in flight', () => {
    const { cache, sent } = cacheWithSender();

    cache.ensure('sess', 7, 10);
    cache.ensure('sess', 7, 10);

    expect(sent).toHaveLength(1);
  });

  it('does not ask again for pixels it already holds', () => {
    const { cache, sent } = cacheWithSender();
    cache.fill(blob({ imageId: 7, generation: 10 }));

    cache.ensure('sess', 7, 10);

    expect(sent).toHaveLength(0);
    expect(cache.status('sess', 7, 10)).toBe('present');
  });

  it('leaves a key absent when the socket cannot carry the request', () => {
    const cache = new KittyImageCache();
    cache.setSender(() => false);

    cache.ensure('sess', 7, 10);

    // Not pending: a request that never went out must be retried by the next
    // description, not waited on forever.
    expect(cache.status('sess', 7, 10)).toBe('absent');
  });

  it('asks again for the same image at a new generation', () => {
    const { cache, sent } = cacheWithSender();
    cache.fill(blob({ imageId: 7, generation: 10 }));

    cache.ensure('sess', 7, 11);

    expect(sent).toEqual([['sess', 7]]);
    expect(cache.get('sess', 7, 11)).toBeNull();
  });

  it('keeps sessions apart under the same image id', () => {
    const { cache, sent } = cacheWithSender();
    cache.fill(blob({ sessionId: 'a', imageId: 7 }));

    cache.ensure('b', 7, 10);

    expect(sent).toEqual([['b', 7]]);
  });
});

describe('KittyImageCache answers', () => {
  it('serves the pixels a fill carried', () => {
    const { cache } = cacheWithSender();
    const pixels = new Uint8Array([1, 2, 3, 4, 5, 6]);

    cache.fill(blob({ imageId: 7, pixels, width: 2, height: 1 }));

    expect(cache.get('sess', 7, 10)).toMatchObject({ width: 2, height: 1, format: 'rgb' });
    expect(Array.from(cache.get('sess', 7, 10)!.pixels)).toEqual([1, 2, 3, 4, 5, 6]);
  });

  it('wakes waiters so a pane that could not draw an image draws it now', () => {
    const { cache } = cacheWithSender();
    const seen: Array<[string, number]> = [];
    cache.subscribe((sessionId, imageId) => seen.push([sessionId, imageId]));

    cache.fill(blob({ imageId: 7 }));

    expect(seen).toEqual([['sess', 7]]);
  });

  it('stops asking for an image the daemon says it does not have', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { cache, sent } = cacheWithSender();
    cache.ensure('sess', 7, 10);

    // The failure names no generation — an evicted image has none — so it has to
    // land on whatever generation was waiting on that request.
    cache.markFailed('sess', 7, 'kitty image 7: not found');

    expect(cache.status('sess', 7, 10)).toBe('failed');
    cache.ensure('sess', 7, 10);
    expect(sent).toHaveLength(1);
  });

  it('marks every generation that was waiting on the failed request', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { cache } = cacheWithSender();
    cache.ensure('sess', 7, 10);
    cache.ensure('sess', 7, 11);

    cache.markFailed('sess', 7, 'gone');

    expect(cache.status('sess', 7, 10)).toBe('failed');
    expect(cache.status('sess', 7, 11)).toBe('failed');
  });

  it('asks again after a failure once the image is retransmitted', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { cache, sent } = cacheWithSender();
    cache.ensure('sess', 7, 10);
    cache.markFailed('sess', 7, 'gone');

    cache.ensure('sess', 7, 12);

    expect(sent).toHaveLength(2);
  });

  it('says loudly that an image has no pixels', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { cache } = cacheWithSender();

    cache.markFailed('sess', 7, 'kitty image 7: unknown id');

    expect(warn).toHaveBeenCalledWith(expect.stringContaining('kitty image 7: unknown id'));
  });
});

describe('KittyImageCache eviction', () => {
  it('evicts the oldest blob at the byte cap, naming the limit and the ask', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { cache } = cacheWithSender(1000);
    cache.fill(blob({ imageId: 1, pixels: new Uint8Array(400) }));
    cache.fill(blob({ imageId: 2, pixels: new Uint8Array(400) }));

    cache.fill(blob({ imageId: 3, pixels: new Uint8Array(400) }));

    expect(cache.status('sess', 1, 10)).toBe('absent');
    expect(cache.status('sess', 2, 10)).toBe('present');
    expect(cache.status('sess', 3, 10)).toBe('present');
    expect(cache.bytes()).toBe(800);
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('limit 1000 bytes'));
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('asked for 400'));
  });

  it('evicts by last use, not by arrival', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { cache } = cacheWithSender(1000);
    cache.fill(blob({ imageId: 1, pixels: new Uint8Array(400) }));
    cache.fill(blob({ imageId: 2, pixels: new Uint8Array(400) }));

    // Drawing image 1 makes image 2 the stale one.
    cache.get('sess', 1, 10);
    cache.fill(blob({ imageId: 3, pixels: new Uint8Array(400) }));

    expect(cache.status('sess', 1, 10)).toBe('present');
    expect(cache.status('sess', 2, 10)).toBe('absent');
  });

  it('keeps an image bigger than the whole cache, and says the limit needs remeasuring', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { cache } = cacheWithSender(1000);

    cache.fill(blob({ imageId: 1, pixels: new Uint8Array(4000) }));

    expect(cache.status('sess', 1, 10)).toBe('present');
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('needs remeasuring'));
  });

  it('does not double-count a key refilled by both transports', () => {
    const { cache } = cacheWithSender();
    cache.fill(blob({ pixels: new Uint8Array(400) }));

    cache.fill(blob({ pixels: new Uint8Array(400) }));

    expect(cache.bytes()).toBe(400);
  });
});

describe('kittyImageBlobFromResult', () => {
  // The JSON answer and the binary frame must land the same blob: a relayed
  // remote session only ever gets the JSON one, and it has to draw the same.
  const success = {
    id: 'sess',
    image_id: 7,
    success: true,
    generation: 10,
    width: 2,
    height: 1,
    format: 'rgb',
    data_b64: btoa(String.fromCharCode(1, 2, 3, 4, 5, 6)),
  };

  it('decodes a base64 answer into the same blob a binary frame carries', () => {
    const result = kittyImageBlobFromResult(success);

    expect(result).toMatchObject({
      sessionId: 'sess', imageId: 7, generation: 10, width: 2, height: 1, format: 'rgb',
    });
    expect(Array.from(result!.pixels)).toEqual([1, 2, 3, 4, 5, 6]);
  });

  it('lands in the cache indistinguishably from a binary fill', () => {
    const { cache } = cacheWithSender();
    cache.fill(kittyImageBlobFromResult(success)!);

    const fromJson = cache.get('sess', 7, 10)!;
    cache.fill(blob({
      imageId: 7, width: 2, height: 1, pixels: new Uint8Array([1, 2, 3, 4, 5, 6]),
    }));

    const fromFrame = cache.get('sess', 7, 10)!;
    expect(Array.from(fromJson.pixels)).toEqual(Array.from(fromFrame.pixels));
    expect(fromJson.format).toBe(fromFrame.format);
  });

  it('refuses a failure, an unknown layout, and a missing field alike', () => {
    expect(kittyImageBlobFromResult({ id: 'sess', image_id: 7, success: false })).toBeNull();
    expect(kittyImageBlobFromResult({ ...success, format: 'jpeg' })).toBeNull();
    expect(kittyImageBlobFromResult({ ...success, width: undefined })).toBeNull();
    expect(kittyImageBlobFromResult({ ...success, data_b64: undefined })).toBeNull();
    expect(kittyImageBlobFromResult({ ...success, generation: undefined })).toBeNull();
  });
});
