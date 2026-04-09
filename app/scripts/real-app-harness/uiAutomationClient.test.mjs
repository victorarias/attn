import { describe, expect, it, vi } from 'vitest';
import { UiAutomationClient } from './uiAutomationClient.mjs';

describe('UiAutomationClient.waitForFrontendResponsive', () => {
  it('fails fast when get_state reports a daemon version mismatch', async () => {
    const client = new UiAutomationClient();
    client.request = vi.fn().mockResolvedValue({
      daemonReady: false,
      connectionError: 'Version mismatch: daemon v50, app v49. Restart/reinstall required.',
    });

    await expect(client.waitForFrontendResponsive(200, 'list_sessions')).rejects.toThrow(
      'daemon not ready: Version mismatch: daemon v50, app v49. Restart/reinstall required.',
    );
    expect(client.request).toHaveBeenCalledWith('get_state', {}, { timeoutMs: 200 });
    expect(client.request).not.toHaveBeenCalledWith('list_sessions', {}, { timeoutMs: 200 });
  });

  it('checks daemon readiness before issuing the requested action', async () => {
    const client = new UiAutomationClient();
    client.request = vi.fn()
      .mockResolvedValueOnce({
        daemonReady: true,
        connectionError: null,
      })
      .mockResolvedValueOnce({
        sessions: [],
      });

    await expect(client.waitForFrontendResponsive(500, 'list_sessions')).resolves.toEqual({ sessions: [] });
    expect(client.request.mock.calls).toEqual([
      ['get_state', {}, { timeoutMs: 500 }],
      ['list_sessions', {}, { timeoutMs: 500 }],
    ]);
  });
});
