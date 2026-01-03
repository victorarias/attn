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

    // Wait for CodeMirror to fully initialize
    await page.waitForSelector('.cm-editor .cm-content', { timeout: 10000 });
    // Wait for diff lines to render (UnifiedDiffEditor uses .cm-line, .cm-deleted-line, etc.)
    await page.waitForSelector('.cm-line', { timeout: 5000 });
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
  });
});
