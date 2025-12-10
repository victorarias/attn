import { test, expect } from './fixtures';

test.describe('PR Actions', () => {
  test('approve PR via UI', async ({ page, mockGitHub, daemonInfo }) => {
    // 1. Setup: add PR to mock GitHub BEFORE starting daemon
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 42,
      title: 'Test PR',
      role: 'reviewer',
    });

    // 2. Access daemonInfo to trigger daemon startup (now that PRs are set up)
    // The daemon will poll for PRs on startup and get our PR
    console.log('Daemon is running at', daemonInfo.socketPath);

    // 3. Wait for page to load and connect to daemon
    await page.goto('/');

    // Wait for PRs to load - need to wait for both WS connection and PR poll
    // The daemon polls 200ms after we access daemonInfo, then broadcasts via WS
    await page.waitForTimeout(1000); // Give time for WS connection and broadcast

    // Wait for PR card to appear
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Test PR' });
    await expect(prCard).toBeVisible({ timeout: 10000 });

    // 4. Click the approve button
    const approveButton = prCard.locator('[data-testid="approve-button"]');
    await approveButton.click();

    // 5. Wait for approval to complete (button changes to success state)
    await expect(approveButton).toHaveAttribute('data-success', 'true', { timeout: 5000 });

    // 6. Assert mock server received the approve request
    expect(mockGitHub.hasApproveRequest('test/repo', 42)).toBe(true);
  });

  test('merge PR via UI', async ({ page, mockGitHub, daemonInfo }) => {
    // 1. Setup: add PR to mock GitHub
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 43,
      title: 'Merge Test PR',
      role: 'author',
    });

    // 2. Trigger daemon startup
    console.log('Daemon is running at', daemonInfo.socketPath);

    // 3. Wait for page to load
    await page.goto('/');
    await page.waitForSelector('[data-testid="pr-card"]', { timeout: 15000 });

    // 3. Find the PR card
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Merge Test PR' });
    await expect(prCard).toBeVisible();

    // 4. Click the merge button
    const mergeButton = prCard.locator('[data-testid="merge-button"]');
    await mergeButton.click();

    // 5. Handle merge confirmation modal
    const confirmButton = page.locator('.modal-btn-primary', { hasText: 'Merge' });
    await expect(confirmButton).toBeVisible({ timeout: 2000 });
    await confirmButton.click();

    // 6. Wait for merge to complete
    await expect(mergeButton).toHaveAttribute('data-success', 'true', { timeout: 5000 });

    // 7. Assert mock server received the merge request
    expect(mockGitHub.hasMergeRequest('test/repo', 43)).toBe(true);
  });

  test('mute PR via UI', async ({ page, mockGitHub, daemonInfo }) => {
    // 1. Setup: add PR to mock GitHub
    mockGitHub.addPR({
      repo: 'test/repo',
      number: 44,
      title: 'Mute Test PR',
      role: 'reviewer',
    });

    // 2. Trigger daemon startup
    console.log('Daemon is running at', daemonInfo.socketPath);

    // 3. Wait for page to load
    await page.goto('/');
    await page.waitForSelector('[data-testid="pr-card"]', { timeout: 15000 });

    // 3. Find the PR card
    const prCard = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Mute Test PR' });
    await expect(prCard).toBeVisible();

    // 4. Click the mute button
    const muteButton = prCard.locator('[data-testid="mute-button"]');
    await muteButton.click();

    // 5. PR card should disappear from the list
    await expect(prCard).not.toBeVisible({ timeout: 5000 });
  });

  test('multiple PRs from same repo', async ({ page, mockGitHub, daemonInfo }) => {
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

    // 2. Trigger daemon startup
    console.log('Daemon is running at', daemonInfo.socketPath);

    // 3. Wait for page to load
    await page.goto('/');
    await page.waitForSelector('[data-testid="pr-card"]', { timeout: 15000 });

    // 3. Verify both PRs are visible
    const firstPR = page.locator('[data-testid="pr-card"]').filter({ hasText: 'First PR' });
    const secondPR = page.locator('[data-testid="pr-card"]').filter({ hasText: 'Second PR' });

    await expect(firstPR).toBeVisible();
    await expect(secondPR).toBeVisible();

    // 4. Approve the first PR
    const approveButton = firstPR.locator('[data-testid="approve-button"]');
    await approveButton.click();
    await expect(approveButton).toHaveAttribute('data-success', 'true', { timeout: 5000 });

    // 5. Verify the second PR is still visible and unaffected
    await expect(secondPR).toBeVisible();

    // 6. Verify mock server received only the approve request for PR 50
    expect(mockGitHub.hasApproveRequest('test/repo', 50)).toBe(true);
    expect(mockGitHub.hasApproveRequest('test/repo', 51)).toBe(false);
  });
});
