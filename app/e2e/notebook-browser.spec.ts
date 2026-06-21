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
    // Backlinks render in the right context rail as a card (markdown only).
    const rail = page.locator('.notebook-browser-rail');
    await expect(rail.getByRole('button', { name: '2026-06-20' })).toBeVisible();
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
    // A plain textarea, not the CodeMirror markdown surface; the context rail (outline
    // + backlinks) is a markdown affordance, so a text file shows no rail.
    await expect(page.getByRole('textbox', { name: 'File contents' })).toBeVisible();
    await expect(page.locator('.notebook-browser-rail')).toHaveCount(0);
  });

  test('lists the note outline in the context rail and scrolls the editor to a heading on click', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();
    await page.waitForSelector('.cm-content');

    // The rail's Outline lists the note's ATX headings, indented by level.
    const rail = page.locator('.notebook-browser-rail');
    await expect(rail.getByRole('button', { name: 'Knowledge index' })).toBeVisible();
    await expect(rail.getByRole('button', { name: 'Sections' })).toBeVisible();
    await expect(rail.getByRole('button', { name: 'Subsection detail' })).toBeVisible();

    // The editor starts at the top; clicking a lower heading scrolls it into view.
    const scrollTop = () =>
      page.locator('.cm-scroller').evaluate((el) => (el as HTMLElement).scrollTop);
    expect(await scrollTop()).toBeLessThan(40);
    await rail.getByRole('button', { name: 'Subsection detail' }).click();
    await expect.poll(scrollTop, { timeout: 2000 }).toBeGreaterThan(150);
  });

  test('shows a read-only placeholder for a binary file', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'cover.png' }).click();

    await expect(page.getByRole('heading', { level: 2, name: 'Preview not available' })).toBeVisible();
    await expect(page.getByRole('textbox')).toHaveCount(0);
  });

  test('renders stage-5 chrome and folds the tree to zero width via the edge handle', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // Header chrome (folded into the existing header): the chief pulse and a kind badge.
    await expect(page.locator('.notebook-browser-chief-pulse')).toContainText('chief: active');
    await expect(page.locator('.notebook-browser-kind-badge')).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-stage5-chrome.png' });

    // The tree column has real width, then folds to 0 — the pane stays in the DOM.
    const tree = page.locator('.notebook-browser-list');
    expect(await tree.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(100);
    await page.getByRole('button', { name: 'Hide file tree' }).click();
    await expect(page.locator('.notebook-browser-body')).toHaveClass(/tree-folded/);
    await expect.poll(() => tree.evaluate((el) => el.getBoundingClientRect().width)).toBeLessThan(2);
    await expect(tree).toBeAttached(); // folded, not unmounted

    // The handle now reopens it.
    await page.getByRole('button', { name: 'Show file tree' }).click();
    await expect.poll(() => tree.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(100);
  });
});
