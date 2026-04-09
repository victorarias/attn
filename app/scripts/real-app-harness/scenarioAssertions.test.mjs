import { afterEach, describe, expect, it, vi } from 'vitest';
import { assertPaneVisibleContentPreserved, waitForPaneTextChange } from './scenarioAssertions.mjs';

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

describe('assertPaneVisibleContentPreserved', () => {
  it('allows healthy widen recovery when anchors fully match but wrapped line count drops', async () => {
    const baselineVisibleContent = {
      lines: [
        '╭─────────────────────────────────────',
        '───────╮',
        '│ >_ OpenAI Codex (v0.118.0)',
        '│ model:     gpt-5.4 high   /model to',
        'change │',
        '│ directory: ~',
        'extra wrapped line 1',
        'extra wrapped line 2',
      ],
      summary: {
        nonEmptyLineCount: 36,
        charCount: 845,
      },
    };

    const currentVisibleContent = {
      lines: [
        '',
        '╭────────────────────────────────────────────╮',
        '│ >_ OpenAI Codex (v0.118.0)                 │',
        '│                                            │',
        '│ model:     gpt-5.4 high   /model to change │',
        '│ directory: ~                               │',
        '╰────────────────────────────────────────────╯',
        '',
        'Tip: New 2x rate limits until April',
        'TR205 anchor payload line 1',
      ],
      summary: {
        nonEmptyLineCount: 24,
        charCount: 1008,
      },
    };

    const client = {
      request: vi.fn().mockResolvedValue({
        pane: {
          visibleContent: currentVisibleContent,
        },
      }),
    };

    await expect(assertPaneVisibleContentPreserved(
      client,
      'session-1',
      'main',
      baselineVisibleContent,
      {
        minNonEmptyLineRatio: 0.7,
        minCharCountRatio: 0.55,
        minAnchorMatches: 2,
        timeoutMs: 100,
        description: 'widen recovery',
      },
    )).resolves.toMatchObject({
      matches: expect.arrayContaining([
        '│ >_ OpenAI Codex (v0.118.0)',
        '│ model:     gpt-5.4 high   /model to',
      ]),
    });
  });
});
