import { test, expect } from './fixtures';

test.describe('Settings', () => {
  test('settings modal opens and closes', async ({ page, startDaemonWithPRs }) => {
    // Start daemon (no PRs needed)
    await startDaemonWithPRs();
    await page.goto('/');

    // Wait for dashboard to load
    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    // Click settings button
    const settingsBtn = page.locator('.settings-btn');
    await settingsBtn.click();

    // Settings modal should open
    const modal = page.locator('.settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });

    // Verify modal has expected sections
    await expect(modal.locator('h3', { hasText: 'Projects Directory' })).toBeVisible();
    await expect(modal.locator('h3', { hasText: 'Muted Repositories' })).toBeVisible();

    // Close modal via X button
    const closeBtn = modal.locator('.settings-close');
    await closeBtn.click();

    // Modal should be hidden
    await expect(modal).not.toBeVisible();
  });

  test('settings modal closes on overlay click', async ({ page, startDaemonWithPRs }) => {
    await startDaemonWithPRs();
    await page.goto('/');

    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    // Open settings
    await page.locator('.settings-btn').click();
    const modal = page.locator('.settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });

    // Click overlay (outside modal)
    await page.locator('.settings-overlay').click({ position: { x: 10, y: 10 } });

    // Modal should be hidden
    await expect(modal).not.toBeVisible();
  });

  test('projects directory can be typed manually', async ({ page, startDaemonWithPRs }) => {
    await startDaemonWithPRs();
    await page.goto('/');

    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    // Open settings
    await page.locator('.settings-btn').click();
    const modal = page.locator('.settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });

    // Type a projects directory
    const input = modal.locator('.settings-input');
    await input.fill('/Users/test/projects');
    await input.blur();

    // Close and reopen to verify persistence
    await modal.locator('.settings-close').click();
    await expect(modal).not.toBeVisible();

    // Reopen settings
    await page.locator('.settings-btn').click();
    await expect(modal).toBeVisible({ timeout: 2000 });

    // Value should be preserved
    await expect(input).toHaveValue('/Users/test/projects');
  });

  test('muted repos appear in settings modal', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // Add a PR
    mockGitHub.addPR({
      repo: 'test/settings-repo',
      number: 100,
      title: 'Settings Test PR',
      role: 'reviewer',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    // Wait for PR to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Settings Test PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // Mute the repo
    const repoHeader = page.locator('.repo-header').filter({ hasText: 'settings-repo' });
    await repoHeader.locator('.repo-mute-btn').click();

    // PR should disappear
    await expect(prCard).not.toBeVisible({ timeout: 5000 });

    // Open settings
    await page.locator('.settings-btn').click();
    const modal = page.locator('.settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });

    // Muted repo should appear in list
    const mutedRepoItem = modal.locator('.muted-repo-item').filter({ hasText: 'test/settings-repo' });
    await expect(mutedRepoItem).toBeVisible();
  });

  test('unmute repo from settings restores PRs', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // Add a PR
    mockGitHub.addPR({
      repo: 'test/unmute-repo',
      number: 101,
      title: 'Unmute Test PR',
      role: 'reviewer',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    // Wait for PR to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Unmute Test PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // Mute the repo
    const repoHeader = page.locator('.repo-header').filter({ hasText: 'unmute-repo' });
    await repoHeader.locator('.repo-mute-btn').click();
    await expect(prCard).not.toBeVisible({ timeout: 5000 });

    // Open settings
    await page.locator('.settings-btn').click();
    const modal = page.locator('.settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });

    // Click unmute button
    const mutedRepoItem = modal.locator('.muted-repo-item').filter({ hasText: 'test/unmute-repo' });
    await mutedRepoItem.locator('.unmute-btn').click();

    // Close modal
    await modal.locator('.settings-close').click();
    await expect(modal).not.toBeVisible();

    // PR should reappear
    await expect(prCard).toBeVisible({ timeout: 5000 });
  });
});
