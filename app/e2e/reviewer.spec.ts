/**
 * Reviewer E2E Tests
 *
 * Tests the AI reviewer flow from UI through daemon.
 * Uses ATTN_MOCK_REVIEWER=1 for predictable, fast tests.
 */
import { test, expect } from './fixtures';

// Helper to inject a session into the local UI store
async function injectLocalSession(
  page: import('@playwright/test').Page,
  session: { id: string; label: string; state: string; cwd: string }
) {
  await page.evaluate((s) => {
    window.__TEST_INJECT_SESSION?.({
      id: s.id,
      label: s.label,
      state: s.state as 'working' | 'waiting_input' | 'idle',
      cwd: s.cwd,
    });
  }, session);
}

// Helper to create a session in both local store AND daemon
async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (s: { id: string; label: string; state: string; directory?: string }) => Promise<void> },
  session: { id: string; label: string; state: string; cwd: string }
) {
  // 1. Create local session (for UI to show it)
  await injectLocalSession(page, session);

  // 2. Register with daemon (for state tracking via WebSocket)
  await daemon.injectSession({
    id: session.id,
    label: session.label,
    state: session.state,
    directory: session.cwd,
  });
}

test.describe('Reviewer Agent', () => {
  let testRepo: { repoPath: string; cleanup: () => void } | null = null;

  test.afterEach(async () => {
    // Clean up test repo
    if (testRepo) {
      testRepo.cleanup();
      testRepo = null;
    }
  });

  test('happy path: start review -> streaming output -> comments appear', async ({ page, daemon }) => {
    // Create test repo with git changes
    testRepo = await daemon.createTestRepo();

    // Start daemon
    await daemon.start();

    // Navigate to app first
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Create session in both local store and daemon
    await createSession(page, daemon, {
      id: 'test-session-1',
      label: 'test-review',
      state: 'idle',
      cwd: testRepo.repoPath,
    });

    // Wait for session to appear and click it
    const sessionCard = page.locator('[data-testid="session-test-session-1"]');
    await expect(sessionCard).toBeVisible({ timeout: 10000 });
    await sessionCard.click();

    // Wait for git status to load and Changes panel to show Review button
    // The Review button only appears when there are files to review
    const reviewBtn = page.locator('.changes-panel .review-btn');
    await expect(reviewBtn).toBeVisible({ timeout: 15000 });

    // Click Review button to open ReviewPanel
    await reviewBtn.click();

    // Wait for ReviewPanel to open
    const reviewPanel = page.locator('.review-panel');
    await expect(reviewPanel).toBeVisible({ timeout: 5000 });

    // Click the AI Review button in ReviewPanel to start the review
    const aiReviewBtn = page.locator('.review-agent-btn');
    await expect(aiReviewBtn).toBeVisible();
    await aiReviewBtn.click();

    // Wait for reviewer output panel to appear
    const reviewerOutput = page.locator('.reviewer-output-panel');
    await expect(reviewerOutput).toBeVisible({ timeout: 5000 });

    // Verify streaming content appears (from mock reviewer)
    // The mock reviewer sends: "Reviewing changes..." and "Found some issues in the code."
    await expect(page.locator('.reviewer-output-content')).toContainText('Reviewing changes', { timeout: 5000 });

    // Verify tool calls appear (muted style)
    const toolCall = page.locator('.reviewer-tool-call');
    await expect(toolCall.first()).toBeVisible({ timeout: 5000 });

    // Verify the tool name is visible
    await expect(page.locator('.tool-call-name').first()).toContainText('get_changed_files');

    // Wait for review to complete - the summary should appear
    await expect(page.locator('.reviewer-output-content')).toContainText('Summary', { timeout: 10000 });

    // Verify spinner is gone (review complete)
    const spinner = page.locator('.reviewer-spinner');
    await expect(spinner).not.toBeVisible({ timeout: 5000 });

    // === Additional Feature Tests ===

    // 1. Table rendering: verify markdown table is rendered as HTML table
    const table = page.locator('.reviewer-output-content table');
    await expect(table).toBeVisible({ timeout: 2000 });
    await expect(table.locator('th').first()).toContainText('File');

    // 2. Jump-to-file: verify add_comment tool call is clickable
    const addCommentToolCall = page.locator('.reviewer-tool-call.clickable');
    await expect(addCommentToolCall).toBeVisible({ timeout: 2000 });
    // The clickable tool call should have cursor: pointer style
    const cursor = await addCommentToolCall.evaluate(el => getComputedStyle(el).cursor);
    expect(cursor).toBe('pointer');

    // 3. Font size: verify Cmd+Plus increases font size
    const outputContent = page.locator('.reviewer-output-content');
    const initialFontSize = await outputContent.evaluate(el => getComputedStyle(el).fontSize);
    await page.keyboard.press('Meta+=');
    await page.waitForTimeout(100);
    const newFontSize = await outputContent.evaluate(el => getComputedStyle(el).fontSize);
    expect(parseInt(newFontSize)).toBe(parseInt(initialFontSize) + 1);
  });

  test('cancel review mid-stream', async ({ page, daemon }) => {
    // Create test repo
    testRepo = await daemon.createTestRepo();

    // Start daemon
    await daemon.start();

    // Navigate to app first
    await page.goto('/');
    await page.waitForSelector('.dashboard');

    // Create session in both local store and daemon
    await createSession(page, daemon, {
      id: 'test-session-2',
      label: 'test-cancel',
      state: 'idle',
      cwd: testRepo.repoPath,
    });

    // Wait for session to appear and click it
    const sessionCard = page.locator('[data-testid="session-test-session-2"]');
    await expect(sessionCard).toBeVisible({ timeout: 10000 });
    await sessionCard.click();

    const reviewBtn = page.locator('.changes-panel .review-btn');
    await expect(reviewBtn).toBeVisible({ timeout: 15000 });
    await reviewBtn.click();

    const reviewPanel = page.locator('.review-panel');
    await expect(reviewPanel).toBeVisible({ timeout: 5000 });

    // Start review
    const aiReviewBtn = page.locator('.review-agent-btn');
    await aiReviewBtn.click();

    // Wait for review to start (spinner appears)
    const spinner = page.locator('.reviewer-spinner');
    await expect(spinner).toBeVisible({ timeout: 5000 });

    // Wait a moment for some output, then cancel
    await expect(page.locator('.reviewer-output-content')).toContainText('Reviewing', { timeout: 5000 });

    // Click cancel button (same button changes to Cancel when running)
    const cancelBtn = page.locator('.review-agent-btn.running');
    await expect(cancelBtn).toBeVisible();
    await expect(cancelBtn).toContainText('Cancel');
    await cancelBtn.click();

    // Verify review stops (spinner disappears)
    await expect(spinner).not.toBeVisible({ timeout: 5000 });

    // Button should be back to "Review" state
    await expect(page.locator('.review-agent-btn')).toContainText('Review');
  });
});
