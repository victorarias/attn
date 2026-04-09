import { afterEach, describe, expect, it, vi } from 'vitest';
import { waitForPaneTextChange } from './scenarioAssertions.mjs';

describe('waitForPaneTextChange', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('resolves once pane text differs from the previous snapshot', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn()
        .mockResolvedValueOnce({ text: 'same' })
        .mockResolvedValueOnce({ text: 'same' })
        .mockResolvedValueOnce({ text: 'changed' }),
    };

    const pending = waitForPaneTextChange(client, 'session-1', 'pane-1', 'same', 'text change', 2_000);
    await vi.advanceTimersByTimeAsync(500);

    await expect(pending).resolves.toEqual({ text: 'changed' });
    expect(client.request).toHaveBeenCalledTimes(3);
  });

  it('times out when pane text never changes', async () => {
    vi.useFakeTimers();
    const client = {
      request: vi.fn().mockResolvedValue({ text: 'same' }),
    };

    const pending = waitForPaneTextChange(client, 'session-1', 'pane-1', 'same', 'text change', 600);
    const assertion = expect(pending).rejects.toThrow(/Timed out waiting for text change/);
    await vi.advanceTimersByTimeAsync(1_000);
    await assertion;
  });
});
