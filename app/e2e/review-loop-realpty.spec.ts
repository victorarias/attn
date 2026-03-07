import { test, expect } from './fixtures';

const realPtyEnabled = process.env.VITE_FORCE_REAL_PTY === '1';

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

async function sendWSCommand(
  page: import('@playwright/test').Page,
  wsUrl: string,
  payload: object,
  predicate: (data: any) => boolean
) {
  return page.evaluate(
    ({ wsUrl, payload, predicateSource }) =>
      new Promise<any>((resolve, reject) => {
        const matches = new Function('data', `return (${predicateSource})(data);`) as (data: any) => boolean;
        const ws = new WebSocket(wsUrl);
        const timer = window.setTimeout(() => {
          ws.close();
          reject(new Error('websocket command timeout'));
        }, 4000);

        ws.onerror = () => {
          window.clearTimeout(timer);
          reject(new Error('websocket command error'));
        };

        ws.onmessage = (event) => {
          const data = JSON.parse(event.data);
          if (!matches(data)) {
            return;
          }
          window.clearTimeout(timer);
          ws.close();
          resolve(data);
        };

        ws.onopen = () => {
          ws.send(JSON.stringify(payload));
        };
      }),
    { wsUrl, payload, predicateSource: predicate.toString() }
  );
}

test.describe('Review Loop Real PTY', () => {
  test('advance injects the next loop pass into a real daemon PTY', async ({ page, daemon }) => {
    test.skip(!realPtyEnabled, 'Requires real PTY');

    const daemonInfo = await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    const sessionId = 'loop-real-1';
    const cwd = '/tmp';

    await injectLocalSession(page, {
      id: sessionId,
      label: 'Loop Real',
      state: 'waiting_input',
      cwd,
    });
    await daemon.injectSession({
      id: sessionId,
      label: 'Loop Real',
      state: 'waiting_input',
      directory: cwd,
    });

    const spawnResult = await sendWSCommand(
      page,
      daemonInfo.wsUrl,
      {
        cmd: 'spawn_session',
        id: sessionId,
        cwd,
        agent: 'shell',
        cols: 80,
        rows: 24,
        label: 'Loop Real',
      },
      (data) => data.event === 'spawn_result' && data.id === 'loop-real-1'
    );
    expect(spawnResult.success, JSON.stringify(spawnResult)).toBe(true);

    await page.locator(`[data-testid="session-${sessionId}"]`).click();
    await expect(page.locator('.terminal-wrapper.active')).toBeVisible({ timeout: 5000 });

    await page.waitForFunction((id) => {
      const events = (window as any).__TEST_PTY_EVENTS as Array<{ event: string; id?: string; data?: string }> | undefined;
      if (!events) return false;
      return events.some((evt) => evt.event === 'data' && evt.id === id);
    }, sessionId, { timeout: 5000 });

    const startResult = await sendWSCommand(
      page,
      daemonInfo.wsUrl,
      {
        cmd: 'start_review_loop',
        session_id: sessionId,
        preset_id: 'full-review-fix',
        prompt: 'Do a full review and when done run attn review-loop advance --session {{session_id}} --token {{advance_token}}',
        iteration_limit: 2,
      },
      (data) => data.event === 'review_loop_result' && data.action === 'start' && data.session_id === 'loop-real-1'
    );
    expect(startResult.success).toBe(true);

    await expect(page.locator(`[data-testid="review-loop-bar-${sessionId}"]`)).toContainText('Running');

    const firstPromptCount = await page.evaluate((id) => {
      const events = (window as any).__TEST_PTY_EVENTS as Array<{ event: string; id?: string; data?: string }> | undefined;
      if (!events) return 0;
      return events.filter((evt) => evt.event === 'data' && evt.id === id).length;
    }, sessionId);

    const loopStateResult = await sendWSCommand(
      page,
      daemonInfo.wsUrl,
      { cmd: 'get_review_loop_state', session_id: sessionId },
      (data) => data.event === 'review_loop_result' && data.action === 'get' && data.session_id === 'loop-real-1'
    );
    expect(loopStateResult.success).toBe(true);
    const token = loopStateResult.review_loop_state?.advance_token;
    expect(token).toBeTruthy();

    const advanceResult = await sendWSCommand(
      page,
      daemonInfo.wsUrl,
      { cmd: 'advance_review_loop', session_id: sessionId, token },
      (data) => data.event === 'review_loop_result' && data.action === 'advance' && data.session_id === 'loop-real-1'
    );
    expect(advanceResult.success).toBe(true);

    await page.waitForFunction(
      ({ id, minCount }) => {
        const events = (window as any).__TEST_PTY_EVENTS as Array<{ event: string; id?: string; data?: string }> | undefined;
        if (!events) return false;
        return events.filter((evt) => evt.event === 'data' && evt.id === id).length > minCount;
      },
      { id: sessionId, minCount: firstPromptCount },
      { timeout: 5000 }
    );

    await expect(page.locator(`[data-testid="review-loop-bar-${sessionId}"]`)).toContainText('Passes 1/2');
  });
});
