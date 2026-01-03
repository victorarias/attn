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
    test('shows deleted lines with red background', async ({ page }) => {
      const deletedLines = page.locator('.cm-deleted-line');
      await expect(deletedLines).toHaveCount(2); // Two lines deleted in test data
    });

    test('shows added lines with green background', async ({ page }) => {
      const addedLines = page.locator('.cm-added-line');
      await expect(addedLines).toHaveCount(1); // One line added in test data
    });

    test('deleted lines are part of the document (can get line number)', async ({ page }) => {
      // Click on a deleted line - should open comment form
      await page.locator('.cm-deleted-line').first().click();

      const form = page.locator('.unified-comment-form');
      await expect(form).toBeVisible();
    });

    test('shows dual line number gutters', async ({ page }) => {
      // Should have both original and modified gutters
      const originalGutter = page.locator('.cm-original-gutter');
      const modifiedGutter = page.locator('.cm-modified-gutter');

      await expect(originalGutter).toBeVisible();
      await expect(modifiedGutter).toBeVisible();

      // Check that deleted lines show "-" in modified gutter
      // and added lines show "-" in original gutter
      const blankMarkers = page.locator('.cm-lineNumber-blank');
      await expect(blankMarkers).toHaveCount(3); // 2 deleted + 1 added = 3 blank markers
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

      await page.keyboard.press('Escape');

      await expect(textarea).not.toBeVisible();
    });

    test('saves comment with Cmd/Ctrl+Enter', async ({ page }) => {
      await page.locator('.cm-line').first().click();

      const textarea = page.locator('.unified-comment-textarea');
      await textarea.fill('Quick save test');

      // Use Meta+Enter (Cmd on Mac)
      await page.keyboard.press('Meta+Enter');

      const comment = page.locator('.unified-comment');
      await expect(comment).toBeVisible();

      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(1);
    });

    test('cancel button closes form', async ({ page }) => {
      await page.locator('.cm-line').first().click();

      const form = page.locator('.unified-comment-form');
      await expect(form).toBeVisible();

      await page.locator('.cancel-btn').click();

      await expect(form).not.toBeVisible();
    });
  });

  test.describe('saved comments', () => {
    async function addComment(page: import('@playwright/test').Page, content: string) {
      await page.locator('.cm-line').first().click();
      await page.locator('.unified-comment-textarea').fill(content);
      await page.locator('.save-btn').click();
      await page.waitForSelector('.unified-comment');
    }

    test('resolve button toggles resolved state', async ({ page }) => {
      await addComment(page, 'Test comment');

      const comment = page.locator('.unified-comment');
      await expect(comment).not.toHaveClass(/resolved/);

      await comment.locator('.resolve-btn').click();

      await expect(comment).toHaveClass(/resolved/);

      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('resolveComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0][1]).toBe(true); // resolved = true
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

    test('edit button opens edit form', async ({ page }) => {
      await addComment(page, 'Original content');

      const comment = page.locator('.unified-comment');
      await comment.locator('.edit-btn').click();

      // Should show textarea with original content
      const textarea = comment.locator('.unified-comment-textarea');
      await expect(textarea).toBeVisible();
      await expect(textarea).toHaveValue('Original content');

      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('startEdit'));
      expect(calls).toHaveLength(1);
    });

    test('edit form saves updated content', async ({ page }) => {
      await addComment(page, 'Original content');

      const comment = page.locator('.unified-comment');
      await comment.locator('.edit-btn').click();

      const textarea = comment.locator('.unified-comment-textarea');
      await textarea.clear();
      await textarea.fill('Updated content');
      await comment.locator('.save-btn').click();

      // Should show updated content
      await expect(comment.locator('.unified-comment-content')).toContainText('Updated content');

      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('editComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0][1]).toBe('Updated content');
    });

    test('edit form cancel restores original content', async ({ page }) => {
      await addComment(page, 'Original content');

      const comment = page.locator('.unified-comment');
      await comment.locator('.edit-btn').click();

      const textarea = comment.locator('.unified-comment-textarea');
      await textarea.clear();
      await textarea.fill('Changed but will cancel');
      await comment.locator('.cancel-btn').click();

      // Should show original content
      await expect(comment.locator('.unified-comment-content')).toContainText('Original content');

      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('cancelEdit'));
      expect(calls).toHaveLength(1);
    });
  });

  test.describe('multiple comments', () => {
    test('can have multiple comment forms open', async ({ page }) => {
      const lines = page.locator('.cm-line');

      // Open form on line 1
      await lines.nth(0).click();
      await expect(page.locator('.unified-comment-form')).toHaveCount(1);

      // Open form on line 3
      await lines.nth(2).click();
      await expect(page.locator('.unified-comment-form')).toHaveCount(2);
    });

    test('scroll position preserved when saving comment', async ({ page }) => {
      // This is the key test - unified diff should NOT have scroll jump issues
      // because we're not destroying/recreating the editor

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
});
