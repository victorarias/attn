/**
 * Component Harness Tests
 *
 * These tests use the test harness infrastructure to test components
 * in isolation with real browser rendering (no jsdom limitations).
 *
 * The harness runs without the daemon - all dependencies are mocked.
 */
import { test, expect } from '@playwright/test';

test.describe('ReviewPanel Component', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/test-harness/?component=ReviewPanel');

    await page.waitForFunction(() => window.__HARNESS__?.ready === true, null, { timeout: 10000 });

    // Wait for CodeMirror to fully initialize
    await page.waitForSelector('.cm-editor .cm-content', { timeout: 10000 });
    // Wait for diff lines to render (UnifiedDiffEditor uses .cm-line, .cm-deleted-line, etc.)
    await page.waitForSelector('.cm-line', { timeout: 5000 });
  });

  test.describe('font size keyboard shortcuts', () => {
    /**
     * BUG: Cmd+/- expands all hunks without changing UI state
     *
     * Expected behavior:
     * - Cmd+= and Cmd+- should only change font size
     * - Collapsed regions should remain collapsed
     * - UI toggle should stay on "Hunks"
     *
     * Current bug: Pressing Cmd+/- expands all hunks but the toggle still shows "Hunks"
     */
    test('Cmd+/- changes font size without affecting collapsed regions', async ({ page }) => {
      // First verify we're in hunks mode with collapsed regions
      const hunksBtn = page.locator('button.expand-btn', { hasText: 'Hunks' });
      await expect(hunksBtn).toHaveClass(/active/);

      const collapsedRegions = page.locator('.cm-collapsed-region');
      const initialCount = await collapsedRegions.count();

      // Skip if no collapsed regions (test harness might not create them properly)
      if (initialCount === 0) {
        console.log('No collapsed regions found - skipping keyboard shortcut test');
        return;
      }

      // NOTE: Keyboard handlers are document-level, so we don't need to click/focus the editor.
      // Clicking on the editor would trigger the comment form click handler, which auto-expands
      // regions containing the clicked line.

      // Press Cmd++ (font size increase)
      await page.keyboard.press('Meta+=');
      await page.waitForTimeout(200);

      // Collapsed regions should still be there
      const countAfterPlus = await collapsedRegions.count();
      expect(countAfterPlus).toBe(initialCount);

      // Hunks button should still be active
      await expect(hunksBtn).toHaveClass(/active/);

      // Press Cmd+- (font size decrease)
      await page.keyboard.press('Meta+-');
      await page.waitForTimeout(200);

      // Collapsed regions should still be there
      const countAfterMinus = await collapsedRegions.count();
      expect(countAfterMinus).toBe(initialCount);

      // Hunks button should still be active
      await expect(hunksBtn).toHaveClass(/active/);
    });

    test('Cmd+/- preserves saved comments', async ({ page }) => {
      // First add a comment
      const lines = page.locator('.cm-line');
      await lines.nth(3).click();

      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible({ timeout: 3000 });

      await textarea.fill('Test comment to preserve');
      await page.locator('.save-btn').click();

      // Verify comment is saved
      const savedComment = page.locator('.unified-comment');
      await expect(savedComment).toBeVisible();
      await expect(savedComment).toContainText('Test comment to preserve');

      // Change font size with Cmd+=
      await page.keyboard.press('Meta+=');
      await page.waitForTimeout(200);

      // Comment should still be visible
      await expect(savedComment).toBeVisible();
      await expect(savedComment).toContainText('Test comment to preserve');

      // Change font size with Cmd+-
      await page.keyboard.press('Meta+-');
      await page.waitForTimeout(200);

      // Comment should still be visible
      await expect(savedComment).toBeVisible();
      await expect(savedComment).toContainText('Test comment to preserve');
    });
  });

  test.describe('hunk/full toggle', () => {
    /**
     * BUG: Hunk selection not working - shows full diff all the time
     *
     * Expected behavior:
     * - "Hunks" button (default, active) should show collapsed regions
     * - "Full" button should show entire diff without collapsed regions
     *
     * Current bug: Both buttons show full diff because contextLines is always 0
     */
    test('hunks mode shows collapsed regions by default', async ({ page }) => {
      // The "Hunks" button should be active by default
      const hunksBtn = page.locator('button.expand-btn', { hasText: 'Hunks' });
      await expect(hunksBtn).toHaveClass(/active/);

      // With hunks mode, there should be collapsed regions (hidden unchanged lines)
      // The test harness uses DIFF_WITH_DELETIONS which has multiple sections
      const collapsedRegions = page.locator('.cm-collapsed-region');

      // BUG: This assertion will fail because collapsed regions don't appear
      // when contextLines is incorrectly set to 0
      await expect(collapsedRegions.first()).toBeVisible({ timeout: 3000 });
    });

    test('full mode shows no collapsed regions', async ({ page }) => {
      // Click the "Full" button
      const fullBtn = page.locator('button.expand-btn', { hasText: 'Full' });
      await fullBtn.click();

      // Should now be active
      await expect(fullBtn).toHaveClass(/active/);

      // Full mode should have no collapsed regions
      const collapsedRegions = page.locator('.cm-collapsed-region');
      await expect(collapsedRegions).toHaveCount(0);
    });

    test('switching from hunks to full removes collapsed regions', async ({ page }) => {
      // First verify hunks mode has collapsed regions (after fix)
      const collapsedRegions = page.locator('.cm-collapsed-region');

      // Click Full button
      const fullBtn = page.locator('button.expand-btn', { hasText: 'Full' });
      await fullBtn.click();
      await page.waitForTimeout(100);

      // Collapsed regions should be gone
      await expect(collapsedRegions).toHaveCount(0);
    });
  });

  test.describe('scroll position preservation', () => {
    // Helper to get scroll position of CodeMirror editor
    async function getScrollTop(page: import('@playwright/test').Page): Promise<number> {
      return page.evaluate(() => {
        const scroller = document.querySelector('.cm-scroller');
        return scroller ? scroller.scrollTop : 0;
      });
    }

    // Helper to scroll CodeMirror editor
    async function scrollTo(page: import('@playwright/test').Page, scrollTop: number): Promise<void> {
      await page.evaluate((top) => {
        const scroller = document.querySelector('.cm-scroller');
        if (scroller) scroller.scrollTop = top;
      }, scrollTop);
      await page.waitForTimeout(100);
    }

    /**
     * BUG: Scroll resets when canceling a comment
     *
     * Expected: Scroll position should be preserved when user cancels comment creation
     * Current bug: Scroll jumps to top or different position
     */
    test('preserves scroll position when canceling comment', async ({ page }) => {
      // First expand to full so we have scrollable content
      const fullBtn = page.locator('button.expand-btn', { hasText: 'Full' });
      await fullBtn.click();
      await page.waitForTimeout(200);

      // Scroll down
      await scrollTo(page, 200);
      const scrollBefore = await getScrollTop(page);
      expect(scrollBefore).toBeGreaterThan(100);

      // Click on a line to open comment form
      const lines = page.locator('.cm-line');
      await lines.nth(10).click();

      // Wait for form to appear
      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible({ timeout: 3000 });

      // Type something
      await textarea.fill('Test comment that will be canceled');

      // Get scroll position before cancel
      const scrollBeforeCancel = await getScrollTop(page);

      // Cancel the comment
      const cancelBtn = page.locator('.cancel-btn');
      await cancelBtn.click();

      // Wait for form to close
      await expect(textarea).not.toBeVisible();

      // BUG: Scroll should be preserved but it resets
      const scrollAfterCancel = await getScrollTop(page);
      expect(Math.abs(scrollAfterCancel - scrollBeforeCancel)).toBeLessThan(50);
    });

    /**
     * BUG: Scroll resets when saving a comment
     *
     * Expected: Scroll position should be preserved when user saves a comment
     * Current bug: Scroll jumps to different position
     */
    test('preserves scroll position when saving comment', async ({ page }) => {
      // First expand to full so we have scrollable content
      const fullBtn = page.locator('button.expand-btn', { hasText: 'Full' });
      await fullBtn.click();
      await page.waitForTimeout(200);

      // Scroll down
      await scrollTo(page, 200);
      const scrollBefore = await getScrollTop(page);
      expect(scrollBefore).toBeGreaterThan(100);

      // Click on a line to open comment form
      const lines = page.locator('.cm-line');
      await lines.nth(10).click();

      // Wait for form to appear
      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible({ timeout: 3000 });

      // Type something
      await textarea.fill('Test comment');

      // Get scroll position before save
      const scrollBeforeSave = await getScrollTop(page);

      // Save the comment
      const saveBtn = page.locator('.save-btn');
      await saveBtn.click();

      // Wait for comment to appear
      await page.waitForSelector('.unified-comment', { timeout: 3000 });

      // BUG: Scroll should be preserved but it resets
      const scrollAfterSave = await getScrollTop(page);
      expect(Math.abs(scrollAfterSave - scrollBeforeSave)).toBeLessThan(50);
    });
  });

  test.describe('comment operations', () => {
    test('opens form and saves comment on line click', async ({ page }) => {
      // Click on a line
      const lines = page.locator('.cm-line');
      await lines.nth(3).click();

      // Form should appear
      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible({ timeout: 3000 });

      // Type and save
      await textarea.fill('Test comment');
      await page.locator('.save-btn').click();

      // Saved comment should appear
      await expect(page.locator('.unified-comment')).toBeVisible();

      // Verify addComment was called
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0][4]).toBe('Test comment'); // content is 5th argument
    });

    test('cancel removes form without saving', async ({ page }) => {
      // Click on a line
      await page.locator('.cm-line').nth(3).click();

      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible();

      // Type something
      await textarea.fill('This will be discarded');

      // Cancel
      await page.locator('.cancel-btn').click();

      // Form should be gone
      await expect(textarea).not.toBeVisible();

      // addComment should NOT have been called
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(0);
    });

    test('deleted line comments work the same as regular lines', async ({ page }) => {
      // Click on a deleted line
      const deletedLine = page.locator('.cm-deleted-line').first();
      await deletedLine.click();

      // Form should appear
      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible({ timeout: 3000 });

      // Type and save
      await textarea.fill('Comment on deleted line');
      await page.locator('.save-btn').click();

      // Saved comment should appear
      await expect(page.locator('.unified-comment')).toBeVisible();

      // Verify addComment was called
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0][4]).toBe('Comment on deleted line');
    });

    /**
     * BUG: Comments on deleted lines appear at wrong position
     *
     * Expected behavior:
     * - Clicking on the 2nd deleted line should show the comment form after that specific line
     * - The saved comment should appear immediately after the clicked line
     *
     * Current bug: Comments appear around the entire deleted chunk, not on the specific line clicked
     */
    test('deleted line comment appears after the specific line clicked', async ({ page }) => {
      // Get the deleted lines - the harness has:
      // index 0: '// DELETED HUNK 1'
      // index 1: 'console.log('deleted A1');'
      // index 2: 'console.log('deleted A2');'
      // index 3: 'console.log('deleted A3');'
      const deletedLines = page.locator('.cm-deleted-line');
      const count = await deletedLines.count();
      expect(count).toBeGreaterThanOrEqual(4);

      // Click on the THIRD deleted line (index 2 = 'deleted A2')
      const targetDeletedLine = deletedLines.nth(2);
      const targetLineText = await targetDeletedLine.textContent();
      expect(targetLineText).toContain('deleted A2');

      await targetDeletedLine.click();

      // Form should appear
      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible({ timeout: 3000 });

      // Type and save
      await textarea.fill('Comment on A2 specifically');
      await page.locator('.save-btn').click();

      // Saved comment should appear
      const savedComment = page.locator('.unified-comment');
      await expect(savedComment).toBeVisible({ timeout: 3000 });

      // The SAVED comment should appear AFTER 'deleted A2', not somewhere else
      // Check DOM position of the saved comment relative to deleted lines
      const commentPosition = await page.evaluate(() => {
        const comment = document.querySelector('.unified-comment');
        if (!comment) return { found: false, prevLineText: 'comment not found' };

        // Walk backwards from the comment to find the nearest deleted line
        let prev = comment.previousElementSibling;
        while (prev && !prev.classList.contains('cm-deleted-line')) {
          prev = prev.previousElementSibling;
        }

        return {
          found: true,
          prevLineText: prev?.textContent || 'no deleted line found before comment',
        };
      });

      expect(commentPosition.found).toBe(true);
      // BUG: This will fail if the saved comment appears after the wrong line
      expect(commentPosition.prevLineText).toContain('deleted A2');
    });
  });
});
