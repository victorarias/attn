import { test, expect } from './fixtures';

test.describe('Settings', () => {
  test('settings modal opens and closes', async ({ page, startDaemonWithPRs }) => {
    // Start daemon (no PRs needed)
    await startDaemonWithPRs();
    await page.goto('/');

    // Wait for dashboard to load
    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    // Click settings button
    const settingsBtn = page.getByTestId('settings-button');
    await settingsBtn.click();

    // Settings modal should open
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });
    const searchInput = modal.getByRole('searchbox', { name: 'Search settings' });
    await expect(searchInput).toHaveAttribute('autocorrect', 'off');
    await expect(searchInput).toHaveAttribute('autocapitalize', 'none');
    await expect(searchInput).toHaveAttribute('spellcheck', 'false');

    // Verify workbench navigation exposes separated settings areas
    await expect(modal.locator('h3', { hasText: 'Mobile Web Client' })).toBeVisible();
    await modal.getByTestId('settings-nav-general').click();
    await expect(modal.locator('h3', { hasText: 'Projects Directory' })).toBeVisible();
    await modal.getByTestId('settings-nav-hygiene').click();
    await expect(modal.locator('h3', { hasText: 'Muted Repositories' })).toBeVisible();

    // Close modal via X button
    const closeBtn = modal.getByTestId('settings-close');
    await closeBtn.click();

    // Modal should be hidden
    await expect(modal).not.toBeVisible();
  });

  test('settings modal closes on overlay click', async ({ page, startDaemonWithPRs }) => {
    await startDaemonWithPRs();
    await page.goto('/');

    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    // Open settings
    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });

    // Click overlay (outside modal)
    await page.getByTestId('settings-overlay').click({ position: { x: 10, y: 10 } });

    // Modal should be hidden
    await expect(modal).not.toBeVisible();
  });

  test('projects directory can be typed manually', async ({ page, startDaemonWithPRs }) => {
    await startDaemonWithPRs();
    await page.goto('/');

    await expect(page.locator('.dashboard')).toBeVisible({ timeout: 5000 });

    // Open settings
    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });
    await modal.getByTestId('settings-nav-general').click();

    // Type a projects directory
    const projectsDir = '/tmp/attn-e2e-projects-manual';
    const input = modal.getByTestId('settings-projects-directory-input');
    await input.fill(projectsDir);
    await input.blur();

    // Close and reopen to verify persistence
    await modal.getByTestId('settings-close').click();
    await expect(modal).not.toBeVisible();

    // Reopen settings
    await page.getByTestId('settings-button').click();
    await expect(modal).toBeVisible({ timeout: 2000 });
    await modal.getByTestId('settings-nav-general').click();

    // Value should be preserved
    await expect(input).toHaveValue(projectsDir);
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
    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });
    await modal.getByTestId('settings-nav-hygiene').click();

    // Muted repo should appear in list
    const mutedRepoItem = modal.getByTestId('settings-muted-repository-item').filter({ hasText: 'test/settings-repo' });
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
    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });
    await modal.getByTestId('settings-nav-hygiene').click();

    // Click unmute button
    const mutedRepoItem = modal.getByTestId('settings-muted-repository-item').filter({ hasText: 'test/unmute-repo' });
    await mutedRepoItem.getByTestId('settings-unmute-repository-button').click();

    // Close modal
    await modal.getByTestId('settings-close').click();
    await expect(modal).not.toBeVisible();

    // PR should reappear
    await expect(prCard).toBeVisible({ timeout: 5000 });
  });

  test('mute author hides PR and shows in settings', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // Add a PR with a specific author
    mockGitHub.addPR({
      repo: 'test/author-repo',
      number: 200,
      title: 'Dependabot PR',
      role: 'reviewer',
      author: 'dependabot',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    // Wait for PR to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Dependabot PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // Hover over PR row to reveal the mute author button
    const prRow = page.locator('.pr-row').filter({ hasText: 'Dependabot PR' });
    await prRow.hover();

    // Click the mute author button
    const muteAuthorBtn = prRow.locator('[data-testid="mute-author-button"]');
    await expect(muteAuthorBtn).toBeVisible();
    await muteAuthorBtn.click();

    // PR should disappear
    await expect(prCard).not.toBeVisible({ timeout: 5000 });

    // Open settings
    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });
    await modal.getByTestId('settings-nav-hygiene').click();

    // Verify Muted Authors section exists
    await expect(modal.locator('h3', { hasText: 'Muted Authors' })).toBeVisible();

    // Muted author should appear in list
    const mutedAuthorItem = modal.getByTestId('settings-muted-author-item').filter({ hasText: 'dependabot' });
    await expect(mutedAuthorItem).toBeVisible();
  });

  test('unmute author from settings restores PRs', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // Add a PR with a specific author
    mockGitHub.addPR({
      repo: 'test/author-repo',
      number: 201,
      title: 'Renovate PR',
      role: 'reviewer',
      author: 'renovate',
    });

    await startDaemonWithPRs();
    await page.goto('/');

    // Wait for PR to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Renovate PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // Mute the author
    const prRow = page.locator('.pr-row').filter({ hasText: 'Renovate PR' });
    await prRow.hover();
    await prRow.locator('[data-testid="mute-author-button"]').click();
    await expect(prCard).not.toBeVisible({ timeout: 5000 });

    // Open settings
    await page.getByTestId('settings-button').click();
    const modal = page.getByTestId('settings-modal');
    await expect(modal).toBeVisible({ timeout: 2000 });
    await modal.getByTestId('settings-nav-hygiene').click();

    // Click unmute button for the author
    const mutedAuthorItem = modal.getByTestId('settings-muted-author-item').filter({ hasText: 'renovate' });
    await mutedAuthorItem.getByTestId('settings-unmute-author-button').click();

    // Close modal
    await modal.getByTestId('settings-close').click();
    await expect(modal).not.toBeVisible();

    // PR should reappear
    await expect(prCard).toBeVisible({ timeout: 5000 });
  });
});
