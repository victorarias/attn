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

    // Verify progress line is gone (review complete)
    const progressLine = page.locator('.reviewer-progress-line');
    await expect(progressLine).not.toBeVisible({ timeout: 5000 });

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
    // Only files that exist in the changed files should be clickable

    // Get all clickable file references - should only be example.go (the changed file)
    const clickableRefs = page.locator('.reviewer-output-content .file-reference.clickable');
    const clickableCount = await clickableRefs.count();
    console.log('[Test] Number of clickable file references:', clickableCount);

    // Log the content of each clickable reference for debugging
    for (let i = 0; i < Math.min(clickableCount, 5); i++) {
      const text = await clickableRefs.nth(i).textContent();
      console.log(`[Test] Clickable ref ${i}: "${text}"`);
    }

    // We expect exactly the example.go references to be clickable:
    // - 2 from table (example.go rows)
    // - 1 from paragraph "See example.go for more details"
    // Files that don't exist in changed files (m2-client-walking.md, go.mod, go.sum, test-file.md)
    // should NOT be clickable
    expect(clickableCount).toBe(3);

    // All clickable refs should be example.go
    const allRefs = await clickableRefs.allTextContents();
    console.log('[Test] All clickable refs:', allRefs);
    expect(allRefs.every(ref => ref === 'example.go')).toBe(true);

    // Verify non-existent files are NOT clickable (rendered as plain text)
    // The mock output mentions these files but they're not in the changed files
    const outputHtml = await page.locator('.reviewer-output-content').evaluate(el => el.innerHTML);
    // These should appear as plain text (no clickable class)
    expect(outputHtml).toContain('m2-client-walking.md');
    expect(outputHtml).toContain('server/go.mod');
    expect(outputHtml).toContain('test-file.md:25');
    // But they should NOT have the clickable class
    expect(outputHtml).not.toMatch(/file-reference clickable[^>]*>m2-client-walking\.md/);
    expect(outputHtml).not.toMatch(/file-reference clickable[^>]*>server\/go\.mod/);

    // Test clicking a table filename - verify it navigates and shows diff
    const tableFilename = page.locator('.reviewer-output-content table .file-reference.clickable').first();
    await expect(tableFilename).toBeVisible({ timeout: 2000 });
    const clickedFileName = await tableFilename.textContent();
    console.log('[Test] Clicking file reference:', clickedFileName);

    // Log what files are in the list for debugging
    const fileListItems = await page.locator('.file-item').allTextContents();
    console.log('[Test] Files in list:', fileListItems);

    await tableFilename.click();
    await page.waitForTimeout(300);

    // BUG CHECK: After clicking, the diff should NOT disappear
    // If we see "Select a file to view diff", the path matching is broken
    const placeholder = page.locator('.diff-placeholder');
    const placeholderVisible = await placeholder.isVisible();
    if (placeholderVisible) {
      const placeholderText = await placeholder.textContent();
      console.log('[Test] BUG: Diff placeholder visible:', placeholderText);
      throw new Error(`BUG: Clicking "${clickedFileName}" caused diff to disappear. Path matching is broken.`);
    }

    // Verify the file is selected AND diff content is shown
    await expect(fileItem).toHaveClass(/selected/);
    // Verify the diff viewer is showing content (CodeMirror content)
    const diffContent = page.locator('.cm-content');
    await expect(diffContent).toBeVisible({ timeout: 2000 });
    const diffText = await diffContent.textContent();
    console.log('[Test] Diff viewer shows content:', diffText?.substring(0, 100));
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

    // Wait for review to start (progress line appears)
    const progressLine = page.locator('.reviewer-progress-line');
    await expect(progressLine).toBeVisible({ timeout: 5000 });

    // Wait a moment for some output, then cancel
    await expect(page.locator('.reviewer-output-content')).toContainText('Reviewing', { timeout: 5000 });

    // Click cancel button (same button changes to Cancel when running)
    const cancelBtn = page.locator('.review-agent-btn.running');
    await expect(cancelBtn).toBeVisible();
    await expect(cancelBtn).toContainText('Cancel');
    await cancelBtn.click();

    // Verify review stops (progress line disappears)
    await expect(progressLine).not.toBeVisible({ timeout: 5000 });

    // Button should be back to "Review" state
    await expect(page.locator('.review-agent-btn')).toContainText('Review');
  });
});
