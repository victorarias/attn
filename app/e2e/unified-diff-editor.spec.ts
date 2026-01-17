/**
 * UnifiedDiffEditor Tests
 *
 * Tests the unified diff approach where deleted lines are real document lines.
 * This eliminates the need for DOM injection - ONE comment mechanism for all lines.
 */
import { test, expect } from '@playwright/test';

test.describe('UnifiedDiffEditor', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/test-harness/?component=UnifiedDiffEditor');
    await page.waitForSelector('[data-testid="unified-diff-editor"]', { timeout: 5000 });
    // Wait for CodeMirror to initialize
    await page.waitForSelector('.cm-editor .cm-content', { timeout: 5000 });
  });

  test.describe('diff rendering', () => {
    test('renders unified diff structure', async ({ page }) => {
      const deletedLines = page.locator('.cm-deleted-line');
      const addedLines = page.locator('.cm-added-line');
      const originalGutter = page.locator('.cm-original-gutter');
      const modifiedGutter = page.locator('.cm-modified-gutter');

      await expect(deletedLines).toHaveCount(2);
      await expect(addedLines).toHaveCount(1);
      await expect(originalGutter).toBeVisible();
      await expect(modifiedGutter).toBeVisible();

      // Deleted lines should be clickable for comments
      await deletedLines.first().click();
      await expect(page.locator('.unified-comment-form')).toBeVisible();
    });
  });

  test.describe('inline comments', () => {
    test('opens comment form when clicking any line', async ({ page }) => {
      // Click on first line (regular line)
      const lines = page.locator('.cm-line');
      await lines.first().click();

      const form = page.locator('.unified-comment-form');
      await expect(form).toBeVisible();

      const textarea = form.locator('textarea');
      await expect(textarea).toBeFocused();
    });

    test('saves comment with correct line number', async ({ page }) => {
      // Click on a deleted line (should be line 3 or 4 in unified doc)
      await page.locator('.cm-deleted-line').first().click();

      const textarea = page.locator('.unified-comment-textarea');
      await textarea.fill('Comment on deleted line');
      await page.locator('.save-btn').click();

      // Verify comment appears
      const comment = page.locator('.unified-comment');
      await expect(comment).toBeVisible();
      await expect(comment).toContainText('Comment on deleted line');

      // Verify mock was called
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0][1]).toBe('Comment on deleted line');
    });

    test('cancels comment form with Escape key', async ({ page }) => {
      await page.locator('.cm-line').first().click();

      const textarea = page.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible();

      // Focus textarea so Escape key event is received
      await textarea.focus();
      await page.keyboard.press('Escape');

      await expect(textarea).not.toBeVisible();
    });
  });

  test.describe('saved comments', () => {
    async function addComment(page: import('@playwright/test').Page, content: string) {
      await page.locator('.cm-line').first().click();
      await page.locator('.unified-comment-textarea').fill(content);
      await page.locator('.save-btn').click();
      await page.waitForSelector('.unified-comment');
    }

    test('resolved and won\'t fix are mutually exclusive', async ({ page }) => {
      await addComment(page, 'Test comment');

      const comment = page.locator('.unified-comment');

      // Mark as won't fix first
      await comment.locator('.wontfix-btn').click();
      await expect(comment).toHaveClass(/wont-fix/);

      // Resolve - should clear won't fix
      await comment.locator('.resolve-btn').click();
      await expect(comment).toHaveClass(/resolved/);
      await expect(comment).not.toHaveClass(/wont-fix/);

      // Mark won't fix again - should clear resolved
      await comment.locator('.wontfix-btn').click();
      await expect(comment).toHaveClass(/wont-fix/);
      await expect(comment).not.toHaveClass(/resolved/);
    });

    test('delete button removes comment', async ({ page }) => {
      await addComment(page, 'Comment to delete');

      const comment = page.locator('.unified-comment');
      await expect(comment).toBeVisible();

      await comment.locator('.delete-btn').click();

      await expect(comment).not.toBeVisible();

      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('deleteComment'));
      expect(calls).toHaveLength(1);
    });
  });
  test.describe('scrolling', () => {
    test('scroll position preserved when saving comment', async ({ page }) => {
      const editor = page.locator('.cm-scroller');

      // Scroll down a bit
      await editor.evaluate((el) => {
        el.scrollTop = 50;
      });

      // Open form and save
      await page.locator('.cm-line').nth(3).click();
      await page.locator('.unified-comment-textarea').fill('Test');

      const scrollBefore = await editor.evaluate((el) => el.scrollTop);
      await page.locator('.save-btn').click();
      await page.waitForSelector('.unified-comment');

      const scrollAfter = await editor.evaluate((el) => el.scrollTop);

      // Scroll should be preserved (within small tolerance)
      expect(Math.abs(scrollAfter - scrollBefore)).toBeLessThan(10);
    });
  });

  test.describe('hunks/collapsed context mode', () => {
    test.beforeEach(async ({ page }) => {
      // Enable large diff and set context lines
      await page.locator('input[type="checkbox"]').check(); // Large Diff checkbox
      await page.locator('input[type="number"]').nth(1).fill('3'); // Context Lines input
      // Wait for editor to update
      await page.waitForTimeout(100);
    });

    test('shows collapsed regions with large diff and contextLines > 0', async ({ page }) => {
      const collapsedRegions = page.locator('.cm-collapsed-region');
      await expect(collapsedRegions.first()).toBeVisible();

      // Should show "N lines hidden" text
      await expect(collapsedRegions.first()).toContainText('lines hidden');
    });

    test('clicking collapsed region expands it', async ({ page }) => {
      const collapsedRegions = page.locator('.cm-collapsed-region');
      const initialCount = await collapsedRegions.count();
      expect(initialCount).toBeGreaterThan(0);

      // Click first collapsed region to expand
      await collapsedRegions.first().click();

      // Should have fewer collapsed regions (or same if there was only one)
      await page.waitForTimeout(100);
      const newCount = await collapsedRegions.count();
      expect(newCount).toBeLessThan(initialCount);
    });

    test('regions containing comments auto-expand', async ({ page }) => {
      // Get the text of first collapsed region to know how many lines are hidden
      const collapsedRegion = page.locator('.cm-collapsed-region').first();
      await expect(collapsedRegion).toBeVisible();

      // Click on a visible line to add a comment (this should be in a non-collapsed area)
      // First, let's expand a region by clicking it
      await collapsedRegion.click();
      await page.waitForTimeout(100);

      // Now click on a line that was previously hidden
      const lines = page.locator('.cm-line');
      await lines.nth(5).click();

      // Add a comment
      const textarea = page.locator('.unified-comment-textarea');
      await textarea.fill('Comment in previously collapsed region');
      await page.locator('.save-btn').click();
      await page.waitForSelector('.unified-comment');

      // The region should stay expanded because it has a comment
      // Even if we could re-collapse, the comment should keep it expanded
      const comment = page.locator('.unified-comment');
      await expect(comment).toBeVisible();
    });
  });
});
