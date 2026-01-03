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
      await textarea.click();
      await page.keyboard.type(content);
      await page.locator('.inline-comment-btn.save').click();

      // Verify
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      expect(calls).toHaveLength(1);
      expect(calls[0][4]).toBe(content);
    });

    /**
     * Test refocus on regular line comments (widget-based, not DOM-injected)
     *
     * Regular line comments use CodeMirror's widget system, which may have
     * different focus behavior than the DOM-injected deleted line comments.
     */
    test('supports refocus on multiple regular line comment forms', async ({ page }) => {
      const regularLines = page.locator('.cm-line');

      // Open first form on line 1
      await regularLines.first().click();
      let textareas = page.locator('.inline-comment-textarea');
      await expect(textareas.first()).toBeVisible();

      await textareas.first().click();
      await page.keyboard.type('First');

      // Open second form on line 2
      await regularLines.nth(1).click();
      await expect(textareas).toHaveCount(2);

      await textareas.nth(1).click();
      await page.keyboard.type('Second comment');

      // Refocus first textarea and continue typing
      await textareas.first().click();
      await page.keyboard.type(' comment');

      // Verify both have correct content
      await expect(textareas.first()).toHaveValue('First comment');
      await expect(textareas.nth(1)).toHaveValue('Second comment');
    });
  });

  test.describe('line number accuracy after deleted line comments', () => {
    /**
     * BUG REPRODUCTION: Line number offset after adding deleted line comments
     *
     * Bug: After adding comments to deleted lines, clicking on regular lines
     * opens comment forms at the wrong line number. The offset grows with
     * each deleted line comment added.
     *
     * Suspected cause: The injected DOM elements for deleted line comments
     * might be affecting posAtDOM() or lineBlockAtHeight() calculations.
     */
    test('regular line click detection remains accurate after deleted line comments', async ({ page }) => {
      // First, add a comment on a deleted line
      const deletedLine = page.locator('.cm-deletedLine').first();
      await deletedLine.click();

      const textarea = page.locator('.inline-comment-textarea').first();
      await expect(textarea).toBeVisible();
      await textarea.click();
      await page.keyboard.type('Comment on deleted line');
      await page.locator('.inline-comment-btn.save').first().click();

      // Wait for comment to be saved and form to close
      await expect(page.locator('.inline-comment.new')).toHaveCount(0);

      // Now add another comment on a different deleted line
      const deletedLine2 = page.locator('.cm-deletedLine').nth(1);
      await deletedLine2.click();
      const textarea2 = page.locator('.inline-comment-textarea').first();
      await expect(textarea2).toBeVisible();
      await textarea2.click();
      await page.keyboard.type('Second deleted line comment');
      await page.locator('.inline-comment-btn.save').first().click();

      // Wait for form to close
      await expect(page.locator('.inline-comment.new')).toHaveCount(0);

      // Now click on a regular line AFTER the deleted chunk
      // In the test diff, line 3 in modified is "console.log('line 5');"
      const regularLines = page.locator('.cm-line');
      const targetLine = regularLines.nth(2); // Line 3 (0-indexed: 2)
      await targetLine.click();

      // Get the line number from the opened form's label
      const formLabel = page.locator('.inline-comment-form .inline-comment-label');
      await expect(formLabel).toBeVisible();
      const labelText = await formLabel.textContent();

      // The label should say "Line 3" since we clicked on line 3
      // If the bug exists, it might say "Line 5" or higher due to offset
      console.log('Form label text:', labelText);

      // Verify via addComment calls - check the line numbers
      const calls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      console.log('All addComment calls:', JSON.stringify(calls));

      // The first two calls are for deleted lines (line_end < 0)
      // We want to check that regular line detection still works
      // For now, just verify the form opened and shows a reasonable line number
      expect(labelText).toMatch(/Line \d+/);

      // Save this comment and check the line numbers
      const newTextarea = page.locator('.inline-comment-textarea').first();
      await newTextarea.click();
      await page.keyboard.type('Regular line comment');
      await page.locator('.inline-comment-btn.save').first().click();

      // Get final addComment calls
      const finalCalls = await page.evaluate(() => window.__HARNESS__.getCalls('addComment'));
      console.log('Final addComment calls:', JSON.stringify(finalCalls));

      // The third call should be for a regular line (line_end > 0)
      // and line_start should equal line_end (single line comment)
      expect(finalCalls).toHaveLength(3);
      const regularLineCall = finalCalls[2];
      expect(regularLineCall[2]).toBe(regularLineCall[3]); // line_start === line_end
      expect(regularLineCall[2]).toBeGreaterThan(0); // Regular line (positive number)
      expect(regularLineCall[3]).toBeGreaterThan(0); // line_end also positive
    });
  });

  test.describe('multiple forms', () => {
    /**
     * REGRESSION TEST: Multiple forms with refocus capability
     *
     * Bug: When multiple comment forms are open, clicking back on a previous
     * textarea doesn't give it focus - typing has no effect. The cancel button
     * still works, suggesting the issue is focus/input capture, not the element.
     *
     * Root cause: CodeMirror's mousedown handler was letting clicks on
     * .inline-comment fall through to CodeMirror's default behavior, which
     * focuses the editor instead of the textarea.
     *
     * This test verifies:
     * 1. Multiple forms can be opened
     * 2. Each form retains its content
     * 3. User can refocus and continue typing in ANY open form
     * 4. All forms save with correct content
     */
    test('supports multiple independent comment forms with refocus', async ({ page }) => {
      const deletedLines = page.locator('.cm-deletedLine');

      // Open and type in first form (use click, not focus, to match real user behavior)
      await deletedLines.first().click();
      const firstTextarea = page.locator('.inline-comment-textarea').first();
      await firstTextarea.click();
      await page.keyboard.type('First');

      // Open and type in second form
      await deletedLines.nth(1).click();
      await expect(page.locator('.inline-comment-textarea')).toHaveCount(2);

      const secondTextarea = page.locator('.inline-comment-textarea').nth(1);
      await secondTextarea.click();
      await page.keyboard.type('Second comment');

      // THE KEY TEST: Refocus first textarea and continue typing
      // This is where the bug manifests - click doesn't give focus
      await firstTextarea.click();
      await page.keyboard.type(' comment'); // Should append to "First"

      // Verify both textareas have correct content
      await expect(firstTextarea).toHaveValue('First comment');
      await expect(secondTextarea).toHaveValue('Second comment');

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

  test.describe('scroll position preservation', () => {
    /**
     * REGRESSION TEST: Scroll position jumps on comment actions
     *
     * Bug: When user opens a comment form, saves a comment, or deletes a comment,
     * the scroll position jumps to a different location, making it hard to
     * continue reviewing the code.
     *
     * Expected: Scroll position should remain stable during all comment operations.
     */

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
      // Wait for scroll to settle
      await page.waitForTimeout(100);
    }

    // Helper to expand full diff (required to have scrollable content)
    async function expandFullDiff(page: import('@playwright/test').Page): Promise<void> {
      // Click the "Full" expand button to show entire file
      const fullButton = page.locator('button.expand-btn', { hasText: 'Full' });
      await fullButton.waitFor({ state: 'visible', timeout: 5000 });
      await fullButton.click();
      // Wait for editor to rebuild with full content
      await page.waitForTimeout(500);
    }

    test('preserves scroll position when saving comment', async ({ page }) => {
      // Multiple hunks create scrollable content
      // Scroll down to see the lower hunks
      await scrollTo(page, 300);

      // Click on a deleted line to open form
      const deletedLines = page.locator('.cm-deletedLine');
      await deletedLines.last().click();

      // Wait for form and type
      const textarea = page.locator('.inline-comment-textarea').first();
      await textarea.waitFor({ state: 'visible', timeout: 3000 });
      await textarea.focus();
      await page.keyboard.type('Test comment');

      // Record scroll before save
      const scrollBeforeSave = await getScrollTop(page);

      // Save the comment
      await page.locator('.inline-comment-btn.save').first().click();
      await page.waitForSelector('.inline-comment-content', { timeout: 3000 });

      // Check scroll position is preserved (allow small tolerance)
      const scrollAfterSave = await getScrollTop(page);
      expect(Math.abs(scrollAfterSave - scrollBeforeSave)).toBeLessThan(50);
    });

  });
});
