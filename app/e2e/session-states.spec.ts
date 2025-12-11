import { test, expect } from './fixtures';

// Helper to inject a session via the window test API
async function injectSession(
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

// Helper to update session state via the window test API
async function updateSessionState(
  page: import('@playwright/test').Page,
  id: string,
  state: string
) {
  await page.evaluate(
    ({ id, state }) => {
      window.__TEST_UPDATE_SESSION_STATE?.(id, state as 'working' | 'waiting_input' | 'idle');
    },
    { id, state }
  );
}

test.describe('Session State Changes', () => {

  test('displays sessions grouped by state on dashboard', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');

    // Wait for app to be ready
    await page.waitForSelector('.dashboard');

    // Inject sessions via window API
    await injectSession(page, { id: 's1', label: 'Working Task', state: 'working' });
    await injectSession(page, { id: 's2', label: 'Needs Input', state: 'waiting_input' });
    await injectSession(page, { id: 's3', label: 'Finished', state: 'idle' });

    // Verify grouping headers
    await expect(page.locator('[data-testid="session-group-working"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-testid="session-group-waiting"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-group-idle"]')).toBeVisible();

    // Verify sessions in correct groups
    await expect(page.locator('[data-testid="session-s1"][data-state="working"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-s2"][data-state="waiting_input"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-s3"][data-state="idle"]')).toBeVisible();
  });

  test('state indicator colors match design spec', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Inject sessions
    await injectSession(page, { id: 's1', label: 'Working', state: 'working' });
    await injectSession(page, { id: 's2', label: 'Waiting', state: 'waiting_input' });
    await injectSession(page, { id: 's3', label: 'Idle', state: 'idle' });

    // Wait for sessions to appear
    await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

    // Verify colors (RGB equivalents)
    const workingDot = page.locator('[data-testid="session-s1"] [data-testid="state-indicator"]');
    const waitingDot = page.locator('[data-testid="session-s2"] [data-testid="state-indicator"]');
    const idleDot = page.locator('[data-testid="session-s3"] [data-testid="state-indicator"]');

    await expect(workingDot).toHaveCSS('background-color', 'rgb(34, 197, 94)');  // #22c55e green
    await expect(waitingDot).toHaveCSS('background-color', 'rgb(245, 158, 11)'); // #f59e0b yellow
    await expect(idleDot).toHaveCSS('background-color', 'rgb(107, 114, 128)');   // #6b7280 grey
  });

  test('UI updates in real-time when state changes', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Inject initial session
    await injectSession(page, { id: 's1', label: 'My Session', state: 'working' });

    // Initially working
    await expect(page.locator('[data-testid="session-s1"][data-state="working"]')).toBeVisible({ timeout: 5000 });

    // Change to waiting_input
    await updateSessionState(page, 's1', 'waiting_input');
    await expect(page.locator('[data-testid="session-s1"][data-state="waiting_input"]')).toBeVisible({ timeout: 2000 });

    // Change to idle
    await updateSessionState(page, 's1', 'idle');
    await expect(page.locator('[data-testid="session-s1"][data-state="idle"]')).toBeVisible({ timeout: 2000 });
  });

  test('attention drawer shows only waiting_input sessions', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Inject sessions with different states
    await injectSession(page, { id: 's1', label: 'Working', state: 'working' });
    await injectSession(page, { id: 's2', label: 'Waiting', state: 'waiting_input' });
    await injectSession(page, { id: 's3', label: 'Idle', state: 'idle' });

    // Wait for sessions to load
    await expect(page.locator('[data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

    // Open attention drawer (Cmd+K)
    await page.keyboard.press('Meta+k');

    // Wait for drawer to open
    await expect(page.locator('.attention-drawer.open')).toBeVisible({ timeout: 2000 });

    // Only waiting_input session should appear
    await expect(page.locator('[data-testid="attention-session-s2"]')).toBeVisible();
    await expect(page.locator('[data-testid="attention-session-s1"]')).not.toBeVisible();
    await expect(page.locator('[data-testid="attention-session-s3"]')).not.toBeVisible();
  });

  test('session moves between groups when state changes', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Inject session in working state
    await injectSession(page, { id: 's1', label: 'Test Session', state: 'working' });

    // Initially in working group
    await expect(page.locator('[data-testid="session-group-working"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-testid="session-group-working"] [data-testid="session-s1"]')).toBeVisible();

    // Change to waiting_input - should move to waiting group
    await updateSessionState(page, 's1', 'waiting_input');
    await expect(page.locator('[data-testid="session-group-waiting"] [data-testid="session-s1"]')).toBeVisible({ timeout: 2000 });

    // Working group should no longer exist (no sessions)
    await expect(page.locator('[data-testid="session-group-working"]')).not.toBeVisible();
  });

});
