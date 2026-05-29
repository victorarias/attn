import { describe, expect, it, vi } from 'vitest';
import { ensureCodexMainPromptReady } from './scenarioAgents.mjs';

describe('ensureCodexMainPromptReady', () => {
  it('accepts the current Codex directory trust dialog before returning ready', async () => {
    let acceptedTrust = false;
    const client = {
      request: vi.fn(async (action) => {
        switch (action) {
          case 'get_pane_state':
            return {
              pane: { bounds: { width: 640, height: 480 }, inputFocused: true },
              inputFocused: true,
              renderHealth: { flags: { terminalVisible: true } },
            };
          case 'read_pane_text':
            return {
              text: acceptedTrust
                ? '>_ OpenAI Codex\n100% left'
                : [
                    'Do you trust the contents of this directory?',
                    '1. Yes, continue',
                    '2. No, quit',
                    'Press enter to continue',
                  ].join('\n'),
            };
          case 'write_pane':
            acceptedTrust = true;
            return {};
          default:
            return {};
        }
      }),
    };

    await expect(ensureCodexMainPromptReady(client, 'session-1', 2_000)).resolves.toMatchObject({
      trustHandled: true,
      text: expect.stringContaining('OpenAI Codex'),
    });
    expect(client.request).toHaveBeenCalledWith(
      'type_pane_via_ui',
      { sessionId: 'session-1', paneId: 'main', text: '1' },
    );
    expect(client.request).toHaveBeenCalledWith(
      'write_pane',
      { sessionId: 'session-1', paneId: 'main', text: '\r', submit: false },
    );
  });
});
