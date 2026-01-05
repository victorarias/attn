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

    // 2. Jump-to-file with scroll: verify clicking different add_comment tool calls scrolls to correct lines
    // Mock sends two add_comment calls: line 10 and line 40
    const addCommentToolCalls = page.locator('.reviewer-tool-call.clickable');
    await expect(addCommentToolCalls).toHaveCount(2, { timeout: 2000 });

    // Verify file list has example.go and it's already selected (auto-selection)
    const fileItem = page.locator('.file-item', { hasText: 'example.go' });
    await expect(fileItem).toBeVisible({ timeout: 2000 });
    await expect(fileItem).toHaveClass(/selected/);

    // Click first tool call (line 10) - should scroll to show "doSomething()"
    await addCommentToolCalls.nth(0).click();
    await page.waitForTimeout(300);
    await expect(page.locator('.cm-content')).toContainText('doSomething', { timeout: 2000 });

    // Click second tool call (line 40) - should scroll to show "MODIFIED for review"
    await addCommentToolCalls.nth(1).click();
    await page.waitForTimeout(300);
    await expect(page.locator('.cm-content')).toContainText('MODIFIED for review', { timeout: 2000 });

    // File should still be selected after all clicks
    await expect(fileItem).toHaveClass(/selected/);

    // 3. Font size: verify Cmd+Plus increases font size
    const outputContent = page.locator('.reviewer-output-content');
    const initialFontSize = await outputContent.evaluate(el => getComputedStyle(el).fontSize);
    await page.keyboard.press('Meta+=');
    await page.waitForTimeout(100);
    const newFontSize = await outputContent.evaluate(el => getComputedStyle(el).fontSize);
    expect(parseInt(newFontSize)).toBe(parseInt(initialFontSize) + 1);

    // 4. Clickable filenames: verify clicking filenames navigates to file
    // Test both table cells AND paragraph text

    // Get all clickable file references
    const clickableRefs = page.locator('.reviewer-output-content .file-reference.clickable');
    const clickableCount = await clickableRefs.count();
    console.log('[Test] Number of clickable file references:', clickableCount);

    // Log the content of each clickable reference for debugging
    for (let i = 0; i < Math.min(clickableCount, 5); i++) {
      const text = await clickableRefs.nth(i).textContent();
      console.log(`[Test] Clickable ref ${i}: "${text}"`);
    }

    // We expect at least:
    // - 2 from table (example.go rows)
    // - 1 from paragraph "See example.go for more details"
    // - 1 from paragraph "test-file.md:25"
    expect(clickableCount).toBeGreaterThanOrEqual(3);

    // Test clicking a table filename
    const tableFilename = page.locator('.reviewer-output-content table .file-reference.clickable').first();
    await expect(tableFilename).toBeVisible({ timeout: 2000 });
    await tableFilename.click();
    await page.waitForTimeout(200);
    await expect(fileItem).toHaveClass(/selected/);

    // Test clicking a paragraph filename (the one with :25 line reference)
    const paragraphRefs = page.locator('.reviewer-output-content p .file-reference.clickable');
    const paragraphCount = await paragraphRefs.count();
    console.log('[Test] Number of paragraph file references:', paragraphCount);

    if (paragraphCount > 0) {
      // Should have "example.go" and "test-file.md:25" in paragraph
      for (let i = 0; i < paragraphCount; i++) {
        const text = await paragraphRefs.nth(i).textContent();
        console.log(`[Test] Paragraph ref ${i}: "${text}"`);
      }
    } else {
      // If no paragraph refs, log the paragraph HTML for debugging
      const paragraphHtml = await page.locator('.reviewer-output-content p').last().evaluate(el => el.innerHTML);
      console.log('[Test] Paragraph HTML (no clickable refs found):', paragraphHtml);
      throw new Error('Expected clickable file references in paragraph text');
    }

    // 5. Test plain-text filenames (not in markdown table)
    // This reproduces the user's actual issue - filenames like "m2-client-walking.md" in plain text
    const allRefs = await clickableRefs.allTextContents();
    console.log('[Test] All clickable refs:', allRefs);

    // Should include filenames from plain text section
    const hasM2File = allRefs.some(ref => ref.includes('m2-client-walking.md'));
    const hasGoMod = allRefs.some(ref => ref.includes('go.mod'));
    const hasGoSum = allRefs.some(ref => ref.includes('go.sum'));
    console.log('[Test] Plain text files found:', { hasM2File, hasGoMod, hasGoSum });

    if (!hasM2File || !hasGoMod || !hasGoSum) {
      // Dump entire output for debugging
      const outputHtml = await page.locator('.reviewer-output-content').evaluate(el => el.innerHTML);
      console.log('[Test] Full output HTML:', outputHtml.substring(0, 2000));
      throw new Error(`Missing plain-text file refs. Found: ${allRefs.join(', ')}`);
    }
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
