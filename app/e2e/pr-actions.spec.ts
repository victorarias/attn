import { test, expect } from './fixtures';

test.describe('PR Actions', () => {
  test('approve PR via UI', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // 1. Setup: add PR to mock GitHub BEFORE starting daemon
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 42,
      title: 'Test PR',
      role: 'reviewer',
    });

    // 2. Now start daemon (it will poll and get our PR)
    const daemonInfo = await startDaemonWithPRs();
    console.log('Daemon started with WS at', daemonInfo.wsUrl);

    // 3. Wait for page to load and connect to daemon
    await page.goto('/');

    // Wait for PR card to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Test PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // 4. Click the approve button
    const approveButton = prCard.locator('[data-testid="approve-button"]');
    await approveButton.click();

    // 5. Wait for approval to complete
    await page.waitForTimeout(1000);

    // 6. Assert mock server received the approve request
    expect(mockGitHub.hasApproveRequest('test/repo', 42)).toBe(true);
  });

  test('merge PR via UI', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // 1. Setup: add PR to mock GitHub
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 43,
      title: 'Merge Test PR',
      role: 'author',
    });

    // 2. Start daemon
    const daemonInfo = await startDaemonWithPRs();
    console.log('Daemon started with WS at', daemonInfo.wsUrl);

    // 3. Wait for page to load
    await page.goto('/');

    // Wait for PR card to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Merge Test PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // 4. Click the merge button
    const mergeButton = prCard.locator('[data-testid="merge-button"]');
    await mergeButton.click();

    // 5. Handle merge confirmation modal
    const confirmButton = page.locator('.modal-btn-primary', { hasText: 'Merge' });
    await expect(confirmButton).toBeVisible({ timeout: 2000 });
    await confirmButton.click();

    // 6. Wait for merge to complete
    await page.waitForTimeout(1000);

    // 7. Assert mock server received the merge request
    expect(mockGitHub.hasMergeRequest('test/repo', 43)).toBe(true);
  });

  test('mute PR via UI', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // 1. Setup: add PR to mock GitHub
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 44,
      title: 'Mute Test PR',
      role: 'reviewer',
    });

    // 2. Start daemon
    await startDaemonWithPRs();

    // 3. Wait for page to load
    await page.goto('/');

    // Wait for PR card to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Mute Test PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // 4. Click the mute button
    const muteButton = prCard.locator('[data-testid="mute-button"]');
    await muteButton.click();

    // 5. PR card should disappear from the list
    await expect(prCard).not.toBeVisible({ timeout: 5000 });
  });

  test('multiple PRs from same repo', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // 1. Setup: add multiple PRs to the same repo
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 50,
      title: 'First PR',
      role: 'reviewer',
    });
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 51,
      title: 'Second PR',
      role: 'author',
    });

    // 2. Start daemon
    await startDaemonWithPRs();

    // 3. Wait for page to load
    await page.goto('/');

    // Wait for PR cards to appear
    await page.waitForSelector('[data-testid="pr-card"]', { timeout: 15000 });

    // 4. Verify both PRs are visible
    const firstPR = page.locator('[data-testid="pr-card"]').filter({ hasText: 'First PR' });
    const secondPR = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Second PR' });

    await expect(firstPR).toBeVisible();
    await expect(secondPR).toBeVisible();

    // 5. Approve the first PR
    const approveButton = firstPR.locator('[data-testid="approve-button"]');
    await approveButton.click();

    // 6. Wait for approval to complete
    await page.waitForTimeout(1000);

    // 7. Verify the second PR is still visible
    await expect(secondPR).toBeVisible();

    // 8. Verify mock server received only the approve request for PR 50
    expect(mockGitHub.hasApproveRequest('test/repo', 50)).toBe(true);
    expect(mockGitHub.hasApproveRequest('test/repo', 51)).toBe(false);
  });

  test('mute repo via UI hides all PRs from that repo', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // 1. Setup: add multiple PRs to the same repo
    mockGitHub.addPR({
      repo: 'test/mute-repo',
      number: 60,
      title: 'PR to be muted via repo',
      role: 'reviewer',
    });
    mockGitHub.addPR({
      repo: 'test/mute-repo',
      number: 61,
      title: 'Another PR to be muted',
      role: 'author',
    });
    // Add a PR from a different repo that should NOT be muted
    mockGitHub.addPR({
      repo: 'test/other-repo',
      number: 70,
      title: 'PR from other repo',
      role: 'reviewer',
    });

    // 2. Start daemon
    await startDaemonWithPRs();

    // 3. Wait for page to load
    await page.goto('/');

    // Wait for PR cards to appear
    await page.waitForSelector('[data-testid="pr-card"]', { timeout: 15000 });

    // 4. Verify all PRs are visible initially
    const pr60 = page.locator('[data-testid="pr-card"]').filter({ hasText: 'PR to be muted via repo' });
    const pr61 = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Another PR to be muted' });
    const pr70 = page.locator('[data-testid="pr-card"]').filter({ hasText: 'PR from other repo' });

    await expect(pr60).toBeVisible();
    await expect(pr61).toBeVisible();
    await expect(pr70).toBeVisible();

    // 5. Click the repo mute button for test/mute-repo
    // The mute button is in the repo header
    const repoHeader = page.locator('.repo-header').filter({ hasText: 'mute-repo' });
    const muteRepoButton = repoHeader.locator('.repo-mute-btn');
    await muteRepoButton.click();

    // 6. Both PRs from test/mute-repo should disappear
    await expect(pr60).not.toBeVisible({ timeout: 5000 });
    await expect(pr61).not.toBeVisible({ timeout: 5000 });

    // 7. PR from test/other-repo should still be visible
    await expect(pr70).toBeVisible();
  });

  test('undo mute PR restores the PR', async ({ page, mockGitHub, startDaemonWithPRs }) => {
    // 1. Setup: add PR to mock GitHub
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 80,
      title: 'Undo Test PR',
      role: 'reviewer',
    });

    // 2. Start daemon
    await startDaemonWithPRs();

    // 3. Wait for page to load
    await page.goto('/');

    // Wait for PR card to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Undo Test PR' });
    await expect(prCard).toBeVisible({ timeout: 15000 });

    // 4. Click the mute button
    const muteButton = prCard.locator('[data-testid="mute-button"]');
    await muteButton.click();

    // 5. PR card should disappear
    await expect(prCard).not.toBeVisible({ timeout: 5000 });

    // 6. Undo toast should appear
    const undoToast = page.locator('.undo-toast');
    await expect(undoToast).toBeVisible({ timeout: 2000 });

    // 7. Click the undo button
    const undoButton = undoToast.locator('.toast-undo-btn');
    await undoButton.click();

    // 8. PR card should reappear
    await expect(prCard).toBeVisible({ timeout: 5000 });
  });
});
