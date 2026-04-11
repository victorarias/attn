import { afterEach, describe, expect, it, vi } from 'vitest';
import { UiAutomationClient } from './uiAutomationClient.mjs';

afterEach(() => {
  vi.useRealTimers();
});

describe('UiAutomationClient.request', () => {
  it('retries transient session absence for session-scoped actions', async () => {
    vi.useFakeTimers();
    const client = new UiAutomationClient();
    client.requestOnce = vi.fn()
      .mockRejectedValueOnce(new Error('Automation request failed: split_pane: Session not found'))
      .mockResolvedValueOnce({ paneId: 'pane-2' });

    const pending = client.request('split_pane', { sessionId: 'session-1' }, { timeoutMs: 2_000 });
    await vi.advanceTimersByTimeAsync(250);

    await expect(pending).resolves.toEqual({ paneId: 'pane-2' });
    expect(client.requestOnce).toHaveBeenCalledTimes(2);
  });

  it('does not retry transient session absence for non-session actions by default', async () => {
    const client = new UiAutomationClient();
    client.requestOnce = vi.fn().mockRejectedValue(new Error('Automation request failed: list_sessions: Session not found'));

    await expect(client.request('list_sessions', {}, { timeoutMs: 2_000 })).rejects.toThrow(
      'Automation request failed: list_sessions: Session not found',
    );
    expect(client.requestOnce).toHaveBeenCalledTimes(1);
  });
});

describe('UiAutomationClient.waitForFrontendResponsive', () => {
  it('fails fast when get_state reports a daemon version mismatch', async () => {
    const client = new UiAutomationClient();
    client.ensureBuildMatchesCurrentSource = vi.fn().mockResolvedValue(undefined);
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
    client.ensureBuildMatchesCurrentSource = vi.fn().mockResolvedValue(undefined);
    client.request = vi.fn()
      .mockResolvedValueOnce({
        daemonReady: true,
        connectionError: null,
        appBuild: {
          sourceFingerprint: 'git:abc',
        },
      })
      .mockResolvedValueOnce({
        sessions: [],
      });

    await expect(client.waitForFrontendResponsive(500, 'list_sessions')).resolves.toEqual({ sessions: [] });
    expect(client.request.mock.calls).toEqual([
      ['get_state', {}, { timeoutMs: 500 }],
      ['list_sessions', {}, { timeoutMs: 500 }],
    ]);
    expect(client.ensureBuildMatchesCurrentSource).toHaveBeenCalledTimes(1);
  });
});

describe('UiAutomationClient.ensureBuildMatchesCurrentSource', () => {
  it('rejects a packaged app built from a different source fingerprint', async () => {
    const client = new UiAutomationClient();
    client.getCurrentSourceIdentity = vi.fn().mockResolvedValue({ fingerprint: 'tree:current' });

    await expect(client.ensureBuildMatchesCurrentSource({
      appBuild: {
        sourceFingerprint: 'git:old',
      },
    })).rejects.toThrow(
      'packaged app source mismatch: app reports git:old, current source is tree:current; rebuild and reinstall attn.app',
    );
  });

  it('rejects a resolved daemon binary built from a different source fingerprint', async () => {
    const client = new UiAutomationClient();
    client.getCurrentSourceIdentity = vi.fn().mockResolvedValue({ fingerprint: 'tree:current' });
    client.resolveDaemonBinaryPath = vi.fn().mockReturnValue('/tmp/attn');
    client.readBinaryBuildInfo = vi.fn().mockResolvedValue({
      sourceFingerprint: 'git:stale-daemon',
    });

    await expect(client.ensureBuildMatchesCurrentSource({
      appBuild: {
        sourceFingerprint: 'tree:current',
      },
    })).rejects.toThrow(
      'daemon source mismatch: /tmp/attn reports git:stale-daemon, current source is tree:current; rebuild the resolved daemon binary before running real-app scenarios',
    );
  });
});
