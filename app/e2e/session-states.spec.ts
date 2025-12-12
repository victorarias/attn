import { test, expect } from './fixtures';

// Helper to inject a session into the local UI store (represents UI-created session)
// This is needed because sessions must be created via UI - daemon-only sessions won't appear in Dashboard
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

// Helper to create a session in both local store AND daemon
// This sets up the full E2E flow: local session + daemon tracking + WebSocket updates
async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string }) => Promise<void> },
  session: { id: string; label: string; state: string; cwd?: string }
) {
  const cwd = session.cwd || '/tmp/test';

  // 1. Create local session (for UI to show it)
  await injectLocalSession(page, { ...session, cwd });

  // 2. Register with daemon (for state tracking via WebSocket)
  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: cwd,
  });
}

test.describe('Session State Changes', () => {

  test('displays sessions grouped by state on dashboard', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');

    // Wait for app to be ready
    await page.waitForSelector('.dashboard');

    // Create sessions in both local store and daemon (true E2E setup)
    // Each session has a unique cwd so daemon can track them independently
    await createSession(page, daemon, { id: 's1', label: 'Working Task', state: 'working', cwd: '/tmp/test/s1' });
    await createSession(page, daemon, { id: 's2', label: 'Needs Input', state: 'waiting_input', cwd: '/tmp/test/s2' });
    await createSession(page, daemon, { id: 's3', label: 'Finished', state: 'idle', cwd: '/tmp/test/s3' });

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

    // Create sessions in both local store and daemon
    await createSession(page, daemon, { id: 's1', label: 'Working', state: 'working', cwd: '/tmp/test/s1' });
    await createSession(page, daemon, { id: 's2', label: 'Waiting', state: 'waiting_input', cwd: '/tmp/test/s2' });
    await createSession(page, daemon, { id: 's3', label: 'Idle', state: 'idle', cwd: '/tmp/test/s3' });

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

  test('UI updates in real-time when state changes via daemon', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Create session in both local store and daemon
    await createSession(page, daemon, { id: 's1', label: 'My Session', state: 'working', cwd: '/tmp/test/s1' });

    // Initially working
    await expect(page.locator('[data-testid="session-s1"][data-state="working"]')).toBeVisible({ timeout: 5000 });

    // Change to waiting_input VIA DAEMON (tests daemon→WebSocket→UI flow)
    await daemon.updateSessionState('s1', 'waiting_input');
    // Wait for WebSocket to propagate the change to UI
    await expect(page.locator('[data-testid="session-s1"][data-state="waiting_input"]')).toBeVisible({ timeout: 5000 });

    // Change to idle VIA DAEMON
    await daemon.updateSessionState('s1', 'idle');
    await expect(page.locator('[data-testid="session-s1"][data-state="idle"]')).toBeVisible({ timeout: 5000 });

    // Change back to working VIA DAEMON
    await daemon.updateSessionState('s1', 'working');
    await expect(page.locator('[data-testid="session-s1"][data-state="working"]')).toBeVisible({ timeout: 5000 });
  });

  test('attention drawer shows only waiting_input sessions', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Create sessions with different states
    await createSession(page, daemon, { id: 's1', label: 'Working', state: 'working', cwd: '/tmp/test/s1' });
    await createSession(page, daemon, { id: 's2', label: 'Waiting', state: 'waiting_input', cwd: '/tmp/test/s2' });
    await createSession(page, daemon, { id: 's3', label: 'Idle', state: 'idle', cwd: '/tmp/test/s3' });

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

  test('session moves between groups when state changes via daemon', async ({ page, daemon }) => {
    await daemon.start();
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Create session in working state
    await createSession(page, daemon, { id: 's1', label: 'Test Session', state: 'working', cwd: '/tmp/test/s1' });

    // Initially in working group
    await expect(page.locator('[data-testid="session-group-working"]')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('[data-testid="session-group-working"] [data-testid="session-s1"]')).toBeVisible();

    // Change to waiting_input VIA DAEMON - should move to waiting group
    await daemon.updateSessionState('s1', 'waiting_input');
    await expect(page.locator('[data-testid="session-group-waiting"] [data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

    // Working group should no longer exist (no sessions)
    await expect(page.locator('[data-testid="session-group-working"]')).not.toBeVisible();

    // Change to idle VIA DAEMON - should move to idle group
    await daemon.updateSessionState('s1', 'idle');
    await expect(page.locator('[data-testid="session-group-idle"] [data-testid="session-s1"]')).toBeVisible({ timeout: 5000 });

    // Waiting group should no longer exist
    await expect(page.locator('[data-testid="session-group-waiting"]')).not.toBeVisible();
  });

});
