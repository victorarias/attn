import { test, expect } from './fixtures';

async function injectLocalSession(
  page: import('@playwright/test').Page,
  session: { id: string; label: string; state: string; cwd?: string }
) {
  await page.evaluate((s) => {
    window.__TEST_INJECT_SESSION?.({
      id: s.id,
      label: s.label,
      state: s.state as 'working' | 'waiting_input' | 'idle',
      cwd: s.cwd || '/tmp/test',
    });
  }, session);
}

async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string }) => Promise<void> },
  session: { id: string; label: string; state: string; cwd?: string }
) {
  const cwd = session.cwd || '/tmp/test';
  await injectLocalSession(page, { ...session, cwd });
  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: cwd,
  });
}

async function sendUserPromptInput(page: import('@playwright/test').Page, wsUrl: string, sessionId: string) {
  await page.evaluate(
    ({ wsUrl, sessionId }) =>
      new Promise<void>((resolve, reject) => {
        const ws = new WebSocket(wsUrl);
        const timer = window.setTimeout(() => {
          ws.close();
          reject(new Error('pty_input timeout'));
        }, 2000);

        ws.onerror = () => {
          window.clearTimeout(timer);
          reject(new Error('pty_input websocket error'));
        };

        ws.onopen = () => {
          ws.send(JSON.stringify({
            cmd: 'pty_input',
            id: sessionId,
            data: 'manual takeover',
            source: 'user',
          }));
          window.setTimeout(() => {
            window.clearTimeout(timer);
            ws.close();
            resolve();
          }, 50);
        };
      }),
    { wsUrl, sessionId }
  );
}

test.describe('Review Loop', () => {
  test('manual user prompt submission stops the loop and updates UI badges', async ({ page, daemon }) => {
    const daemonInfo = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    await createSession(page, daemon, {
      id: 'loop-s1',
      label: 'Loop Session',
      state: 'waiting_input',
      cwd: '/tmp/test/loop-s1',
    });

    await daemon.injectReviewLoopState({
      sessionId: 'loop-s1',
      status: 'waiting_for_agent_advance',
      iterationLimit: 2,
      iterationCount: 0,
      customPrompt: 'Do a full review',
      resolvedPrompt: 'Do a full review',
      advanceToken: 'test-token',
    });

    await expect(page.locator('[data-testid="session-loop-s1"]')).toBeVisible({ timeout: 5000 });
    await page.locator('[data-testid="session-loop-s1"]').click();

    const loopBar = page.locator('[data-testid="review-loop-bar-loop-s1"]');
    await expect(loopBar).toBeVisible({ timeout: 5000 });
    await expect(loopBar).toContainText('Running');
    await expect(page.locator('[data-testid="sidebar-session-loop-s1"]')).toContainText('loop');

    await sendUserPromptInput(page, daemonInfo.wsUrl, 'loop-s1');
    await daemon.updateSessionState('loop-s1', 'working');

    await expect(loopBar).toContainText('Stopped');
    await expect(loopBar).toContainText('manual user input');
    await expect(page.locator('[data-testid="sidebar-session-loop-s1"]')).toContainText('stopped');
  });
});
