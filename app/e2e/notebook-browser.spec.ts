import { test, expect } from '@playwright/test';

// The full notebook modal (lazy filesystem tree sidebar + single live-editor document
// pane), rendered by the component harness with mocked daemon functions in a real
// browser. Verifies the fs-backed layout end-to-end and that the always-live editor
// autosaves — no view/edit toggle. Screenshots capture the assembled UI.

test.describe('NotebookBrowser (fs surface)', () => {
  test('opens the preferred note into a live editor with no view/edit toggle and autosaves edits', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    // The sidebar lists the root as a lazy tree; the preferred note opens into the
    // live editor (heading is the file's basename).
    await expect(page.getByRole('treeitem', { name: 'knowledge' })).toBeVisible();
    await expect(page.getByRole('heading', { level: 2, name: 'index' })).toBeVisible();
    await page.waitForSelector('.cm-content');
    // Rendered inline (heading sized), not a textarea of raw markdown.
    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
    // There is no mode toggle — the surface is always editable.
    await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Save' })).toHaveCount(0);
    // Backlinks render in their strip (markdown only).
    await expect(page.getByRole('heading', { name: /Linked from/ })).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-browser-open.png' });

    // Type at the end of the document; the debounced autosave persists via hash-CAS.
    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type(' Extra words.', { delay: 8 });

    await page.waitForFunction(() => window.__HARNESS__.getCalls('writeFile').length > 0, null, {
      timeout: 4000,
    });
    const writes = await page.evaluate(() => window.__HARNESS__.getCalls('writeFile'));
    const last = writes[writes.length - 1] as [string, string, string | undefined];
    expect(last[0]).toBe('knowledge/index.md');
    expect(last[1]).toContain('Extra words.');
    expect(last[2]).toBe('h1'); // saved against the loaded hash
    // The live save indicator reflects the persisted state.
    await expect(page.getByText('Saved')).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-browser-saved.png' });
  });

  test('opens a text file from the tree in a plain editor (no markdown affordances)', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    // Click a non-markdown text file directly in the root tree.
    await page.getByRole('treeitem', { name: 'notes.txt' }).click();

    await expect(page.getByRole('heading', { level: 2, name: 'notes.txt' })).toBeVisible();
    // A plain textarea, not the CodeMirror markdown surface; no backlinks strip.
    await expect(page.getByRole('textbox', { name: 'File contents' })).toBeVisible();
    await expect(page.getByRole('heading', { name: /Linked from/ })).toHaveCount(0);
  });

  test('shows a read-only placeholder for a binary file', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'cover.png' }).click();

    await expect(page.getByRole('heading', { level: 2, name: 'Preview not available' })).toBeVisible();
    await expect(page.getByRole('textbox')).toHaveCount(0);
  });
});
