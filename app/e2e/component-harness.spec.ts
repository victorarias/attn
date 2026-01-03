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

    // Wait for CodeMirror to fully initialize (not just harness ready signal)
    await page.waitForSelector('.cm-editor .cm-content', { timeout: 10000 });
    // Wait for deleted chunks to render (our test diff has deletions)
    await page.waitForSelector('.cm-deletedChunk', { timeout: 5000 });
  });

  test.describe('deleted line comments', () => {
    /**
     * REGRESSION TEST: Content preservation on editor effect re-run
     *
     * Bug: When user types in a deleted line comment form, and then opens
     * another form (which triggers React state change → editor effect re-runs
     * → editor recreated), the typed content was being lost.
     *
     * Fix: Store draft content in a ref (survives effect re-runs) instead of
     * state (which was causing the re-runs in the first place).
     *
     * This test replicates the exact bug scenario:
     * 1. Open form on deleted line, type content
     * 2. Open ANOTHER form (this triggers the state change that re-runs the effect)
     * 3. Verify first form's content is preserved
     */
    test('preserves typed content when opening additional comment forms', async ({ page }) => {
      const deletedLines = page.locator('.cm-deletedLine');

      // Step 1: Click first deleted line to open comment form
      await deletedLines.first().click();
      const firstTextarea = page.locator('.inline-comment-textarea').first();
      await expect(firstTextarea).toBeVisible();

      // Step 2: Type content using real keystrokes (not .fill())
      const testContent = 'This content must survive the effect re-run';
      await firstTextarea.focus();
      await page.keyboard.type(testContent, { delay: 10 });
      await expect(firstTextarea).toHaveValue(testContent);

      // Step 3: Open second form - THIS triggers the bug scenario
      // Opening a new form changes newDeletedLineComments state → effect re-runs
      await deletedLines.nth(1).click();

      // Wait for second form to appear
      const allTextareas = page.locator('.inline-comment-textarea');
      await expect(allTextareas).toHaveCount(2);

      // Step 4: THE KEY ASSERTION - first form's content must be preserved
      await expect(allTextareas.first()).toHaveValue(testContent);

      // Step 5: Save first comment to verify full flow
      const saveButtons = page.locator('.inline-comment-btn.save');
      await saveButtons.first().click();

      // Verify the saved content matches what we typed
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls.length).toBe(1);
      expect(calls[0][4]).toBe(testContent);
    });

    test('saves comment with correct content after typing', async ({ page }) => {
      // Click deleted line
      await page.locator('.cm-deletedLine').first().click();

      const textarea = page.locator('.inline-comment-textarea');
      await expect(textarea).toBeVisible();

      // Type with real keystrokes
      const content = 'Review comment on deleted code';
      await textarea.focus();
      await page.keyboard.type(content);

      // Save
      await page.locator('.inline-comment-btn.save').click();

      // Verify addComment received correct arguments
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0]).toEqual([
        'test-review-123',      // reviewId
        'src/example.ts',       // filepath
        expect.any(Number),     // lineStart
        expect.any(Number),     // lineEnd (negative for deleted lines)
        content,                // content
      ]);
    });

    test('cancel removes form without saving', async ({ page }) => {
      await page.locator('.cm-deletedLine').first().click();

      const textarea = page.locator('.inline-comment-textarea');
      await expect(textarea).toBeVisible();

      // Type something
      await textarea.focus();
      await page.keyboard.type('This will be discarded');

      // Cancel
      await page.locator('.inline-comment-btn.cancel-btn').click();

      // Form should be gone
      await expect(textarea).not.toBeVisible();

      // addComment should NOT have been called
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(0);
    });
  });

  test.describe('regular line comments', () => {
    test('opens form and saves comment on regular line', async ({ page }) => {
      // Click on a regular line (not deleted)
      const regularLine = page.locator('.cm-line').first();
      await regularLine.click();

      const textarea = page.locator('.inline-comment-textarea');
      await expect(textarea).toBeVisible();

      // Type and save
      const content = 'Comment on regular line';
      await textarea.focus();
      await page.keyboard.type(content);
      await page.locator('.inline-comment-btn.save').click();

      // Verify
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0][4]).toBe(content);
    });
  });

  test.describe('multiple forms', () => {
    test('supports multiple independent comment forms simultaneously', async ({ page }) => {
      const deletedLines = page.locator('.cm-deletedLine');

      // Open and type in first form
      await deletedLines.first().click();
      const firstTextarea = page.locator('.inline-comment-textarea').first();
      await firstTextarea.focus();
      await page.keyboard.type('First comment');

      // Open and type in second form
      await deletedLines.nth(1).click();
      await expect(page.locator('.inline-comment-textarea')).toHaveCount(2);

      const secondTextarea = page.locator('.inline-comment-textarea').nth(1);
      await secondTextarea.focus();
      await page.keyboard.type('Second comment');

      // Both should have their content
      await expect(page.locator('.inline-comment-textarea').first()).toHaveValue('First comment');
      await expect(page.locator('.inline-comment-textarea').nth(1)).toHaveValue('Second comment');

      // Save both - each should have correct content
      const saveButtons = page.locator('.inline-comment-btn.save');
      await saveButtons.first().click();
      await saveButtons.first().click(); // After first save, remaining button is now first

      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(2);
      expect(calls[0][4]).toBe('First comment');
      expect(calls[1][4]).toBe('Second comment');
    });
  });
});
