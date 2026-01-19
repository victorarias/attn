import { test, expect } from '@playwright/test';

test.describe('Dashboard PRs Harness', () => {
  test('fetches PR details when head branch is missing', async ({ page }) => {
    await page.goto('/test-harness/?component=DashboardPRs');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const prCard = page.getByTestId('pr-card').filter({
      has: page.getByText('Missing head branch', { exact: true }),
    });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    await prCard.locator('[data-testid="open-button"]').click();

    await expect(page.locator('[data-testid="open-status"]')).toHaveText('success');

    const detailCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendFetchPRDetails'));
    expect(detailCalls.length).toBe(1);

    const worktreeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendCreateWorktreeFromBranch'));
    expect(worktreeCalls.length).toBe(1);
    expect(worktreeCalls[0][1]).toContain('origin/feature/missing-head');
  });

  test('shows missing head branch error when details fetch does not include branch', async ({ page }) => {
    await page.goto('/test-harness/?component=DashboardPRs');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Still missing head branch' });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    await prCard.locator('[data-testid="open-button"]').click();

    await expect(page.locator('[data-testid="open-status"]')).toHaveText('error');
    await expect(page.locator('[data-testid="open-error"]')).toHaveText('missing_head_branch');

    const detailCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendFetchPRDetails'));
    expect(detailCalls.length).toBe(1);
    expect(detailCalls[0][0]).toBe('test/missing');

    const fetchRemotesCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendFetchRemotes'));
    expect(fetchRemotesCalls.length).toBe(0);

    const worktreeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendCreateWorktreeFromBranch'));
    expect(worktreeCalls.length).toBe(0);
  });

  test('surfaces fetch details failure', async ({ page }) => {
    await page.goto('/test-harness/?component=DashboardPRs&scenario=fetch-details-failed');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const prCard = page.getByTestId('pr-card').filter({
      has: page.getByText('Fetch details failed', { exact: true }),
    });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    await prCard.locator('[data-testid="open-button"]').click();

    await expect(page.locator('[data-testid="open-status"]')).toHaveText('error');
    await expect(page.locator('[data-testid="open-error"]')).toHaveText('fetch_pr_details_failed');

    const detailCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendFetchPRDetails'));
    expect(detailCalls.length).toBe(1);
    expect(detailCalls[0][0]).toBe('test/fetchfail');

    const fetchRemotesCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendFetchRemotes'));
    expect(fetchRemotesCalls.length).toBe(0);
  });

  test('surfaces missing projects directory', async ({ page }) => {
    await page.goto('/test-harness/?component=DashboardPRs&scenario=missing-projects-directory');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const prCard = page.getByTestId('pr-card').filter({
      has: page.getByText('Missing projects directory', { exact: true }),
    });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    await prCard.locator('[data-testid="open-button"]').click();

    await expect(page.locator('[data-testid="open-status"]')).toHaveText('error');
    await expect(page.locator('[data-testid="open-error"]')).toHaveText('missing_projects_directory');

    const detailCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendFetchPRDetails'));
    expect(detailCalls.length).toBe(0);
  });

  test('surfaces fetch remotes failure', async ({ page }) => {
    await page.goto('/test-harness/?component=DashboardPRs&scenario=fetch-remotes-failed');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const prCard = page.getByTestId('pr-card').filter({
      has: page.getByText('Fetch remotes failed', { exact: true }),
    });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    await prCard.locator('[data-testid="open-button"]').click();

    await expect(page.locator('[data-testid="open-status"]')).toHaveText('error');
    await expect(page.locator('[data-testid="open-error"]')).toHaveText('fetch_remotes_failed');

    const fetchRemotesCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendFetchRemotes'));
    expect(fetchRemotesCalls.length).toBe(1);

    const worktreeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendCreateWorktreeFromBranch'));
    expect(worktreeCalls.length).toBe(0);
  });

  test('surfaces worktree creation failure', async ({ page }) => {
    await page.goto('/test-harness/?component=DashboardPRs&scenario=worktree-failed');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const prCard = page.getByTestId('pr-card').filter({
      has: page.getByText('Worktree failed', { exact: true }),
    });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    await prCard.locator('[data-testid="open-button"]').click();

    await expect(page.locator('[data-testid="open-status"]')).toHaveText('error');
    await expect(page.locator('[data-testid="open-error"]')).toHaveText('create_worktree_failed');

    const worktreeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendCreateWorktreeFromBranch'));
    expect(worktreeCalls.length).toBe(1);
  });

  test('wires PR action buttons', async ({ page }) => {
    await page.goto('/test-harness/?component=DashboardPRs&scenario=actions');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const prCard = page.getByTestId('pr-card').filter({
      has: page.getByText('Action buttons', { exact: true }),
    });
    await expect(prCard).toBeVisible();

    await prCard.hover();
    await prCard.locator('[data-testid="approve-button"]').click();

    await prCard.locator('[data-testid="merge-button"]').click();
    await page.locator('.merge-confirm-modal .modal-btn-primary').click();

    await prCard.locator('[data-testid="mute-button"]').click({ force: true });

    const actionCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendPRAction'));
    expect(actionCalls.length).toBe(2);
    expect(actionCalls[0][0]).toBe('approve');
    expect(actionCalls[1][0]).toBe('merge');

    const muteCalls = await page.evaluate(() => window.__HARNESS__.getCalls('sendMutePR'));
    expect(muteCalls.length).toBe(1);
  });
});
