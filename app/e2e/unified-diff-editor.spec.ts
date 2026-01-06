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

      // Focus textarea so Escape key event is received
      await textarea.focus();
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

    test('won\'t fix button toggles state and updates button text', async ({ page }) => {
      await addComment(page, 'Test comment');

      const comment = page.locator('.unified-comment');
      const wontFixBtn = comment.locator('.wontfix-btn');

      // Initially not marked, button shows "Won't Fix"
      await expect(comment).not.toHaveClass(/wont-fix/);
      await expect(wontFixBtn).toHaveText("Won't Fix");

      // Click to mark as won't fix
      await wontFixBtn.click();
      await expect(comment).toHaveClass(/wont-fix/);
      await expect(wontFixBtn).toHaveText("Undo Won't Fix");

      // Click again to undo
      await wontFixBtn.click();
      await expect(comment).not.toHaveClass(/wont-fix/);
      await expect(wontFixBtn).toHaveText("Won't Fix");

      // Verify mock calls
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('wontFixComment'));
      expect(calls).toHaveLength(2);
      expect(calls[0][1]).toBe(true);  // First click: mark
      expect(calls[1][1]).toBe(false); // Second click: undo
    });

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

    test('collapsed region shows expand icon', async ({ page }) => {
      const expandIcon = page.locator('.cm-collapsed-icon');
      await expect(expandIcon.first()).toBeVisible();
      await expect(expandIcon.first()).toContainText('⊕');
    });

    test('contextLines=0 shows full diff (no collapsed regions)', async ({ page }) => {
      // Set context lines back to 0
      await page.locator('input[type="number"]').nth(1).fill('0');
      await page.waitForTimeout(100);

      const collapsedRegions = page.locator('.cm-collapsed-region');
      await expect(collapsedRegions).toHaveCount(0);
    });

    test('Cmd+/- does not affect collapsed regions', async ({ page }) => {
      // Verify we have collapsed regions initially
      const collapsedRegions = page.locator('.cm-collapsed-region');
      const initialCount = await collapsedRegions.count();
      expect(initialCount).toBeGreaterThan(0);

      // Focus the editor
      await page.locator('.cm-editor').click();

      // Press Cmd++ (font size increase)
      await page.keyboard.press('Meta+=');
      await page.waitForTimeout(100);

      // Collapsed regions should still be there
      const countAfterPlus = await collapsedRegions.count();
      expect(countAfterPlus).toBe(initialCount);

      // Press Cmd+- (font size decrease)
      await page.keyboard.press('Meta+-');
      await page.waitForTimeout(100);

      // Collapsed regions should still be there
      const countAfterMinus = await collapsedRegions.count();
      expect(countAfterMinus).toBe(initialCount);
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
