import { test, expect } from './fixtures';

test.describe('Session restore and reconnect harness', () => {
  test('restores daemon sessions after app reload', async ({ page, daemon }) => {
    await daemon.start();

    await daemon.injectSession({
      id: 'restore-s1',
      label: 'Restore Working',
      agent: 'codex',
      state: 'working',
      directory: '/tmp/restore/s1',
    });
    await daemon.injectSession({
      id: 'restore-s2',
      label: 'Restore Waiting',
      agent: 'claude',
      state: 'waiting_input',
      directory: '/tmp/restore/s2',
    });
    await daemon.injectSession({
      id: 'restore-s3',
      label: 'Restore Idle',
      state: 'idle',
      directory: '/tmp/restore/s3',
    });

    await page.goto('/');
    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 10000 });

    await expect(page.locator('[data-testid="session-restore-s1"][data-state="working"]')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="session-restore-s2"][data-state="waiting_input"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-restore-s3"][data-state="idle"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-restore-s1"]')).toHaveCount(1);
    await expect(page.locator('[data-testid="session-restore-s2"]')).toHaveCount(1);
    await expect(page.locator('[data-testid="session-restore-s3"]')).toHaveCount(1);

    await page.locator('[data-testid="session-restore-s1"]').click();
    await expect(page.locator('[data-testid="sidebar-session-restore-s1"][data-state="working"]')).toHaveClass(/selected/, { timeout: 10000 });
    await expect(page.locator('[data-testid="sidebar-session-restore-s2"][data-state="waiting_input"]')).not.toHaveClass(/selected/);

    await page.reload();

    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="session-restore-s1"][data-state="working"]')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="session-restore-s2"][data-state="waiting_input"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-restore-s3"][data-state="idle"]')).toBeVisible();
    await expect(page.locator('[data-testid="session-restore-s1"]')).toHaveCount(1);
    await expect(page.locator('[data-testid="session-restore-s2"]')).toHaveCount(1);
    await expect(page.locator('[data-testid="session-restore-s3"]')).toHaveCount(1);

    await page.locator('[data-testid="session-restore-s1"]').click();
    await expect(page.locator('[data-testid="sidebar-session-restore-s1"][data-state="working"]')).toHaveClass(/selected/, { timeout: 10000 });
    await expect(page.locator('[data-testid="sidebar-session-restore-s2"][data-state="waiting_input"]')).not.toHaveClass(/selected/);
  });

  test('reconnects after daemon restart and marks stale running sessions idle when worker is missing', async ({ page, daemon }) => {
    await daemon.start();

    await daemon.injectSession({
      id: 'reconnect-s1',
      label: 'Reconnect Session',
      agent: 'claude',
      state: 'working',
      directory: '/tmp/reconnect/s1',
    });

    await page.goto('/');
    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="session-reconnect-s1"][data-state="working"]')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="session-reconnect-s1"]')).toHaveCount(1);
    await page.locator('[data-testid="session-reconnect-s1"]').click();
    await expect(page.locator('[data-testid="sidebar-session-reconnect-s1"][data-state="working"]')).toHaveClass(/selected/);
    await expect(page.locator('[data-testid="session-reconnect-s1"]')).toHaveCount(1);

    await daemon.restart();

    // Session remains tracked, but should be downgraded from running to idle.
    await expect(page.locator('[data-testid="session-reconnect-s1"]')).toHaveCount(1, { timeout: 15000 });
    await expect(page.locator('[data-testid="session-reconnect-s1"][data-state="idle"]')).toBeVisible({ timeout: 15000 });
    await expect(page.locator('.warning-banner')).toContainText('they were marked idle', { timeout: 10000 });

    await daemon.injectSession({
      id: 'reconnect-s2',
      label: 'New After Restart',
      state: 'idle',
      directory: '/tmp/reconnect/s2',
    });
    await expect(page.locator('[data-testid="session-reconnect-s2"][data-state="idle"]')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('[data-testid="session-reconnect-s2"]')).toHaveCount(1);
  });
});
