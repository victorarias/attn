import { test, expect } from '@playwright/test';

// The notebook's single read-and-type surface (CodeMirror live preview), rendered
// in isolation by the component harness in a real browser — CM cannot mount under
// happy-dom, so this is where its rendering and interactions are verified.
// Screenshots are captured so the live preview can be eyeballed.

test.describe('LiveMarkdownEditor (live preview)', () => {
  test('renders markdown inline, hides syntax off the cursor line, and reveals it on it', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // Headings are sized via the live-preview decorations.
    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
    await expect(page.locator('.cm-md-h2').first()).toBeVisible();
    // Inline styling renders (bold / italic / code / link).
    await expect(page.locator('.cm-md-strong').first()).toBeVisible();
    await expect(page.locator('.cm-md-em').first()).toBeVisible();
    await expect(page.locator('.cm-md-code').first()).toBeVisible();
    const link = page.locator('.cm-md-link').first();
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute('data-href', '/knowledge/areas/foo.md');

    // The heading's leading "# " is hidden while the cursor is elsewhere: the first
    // rendered line reads "Notebook heading", not "# Notebook heading".
    const firstLine = page.locator('.cm-line').first();
    await expect(firstLine).toContainText('Notebook heading');
    await expect(firstLine).not.toContainText('#');
    await page.screenshot({ path: 'test-results/live-editor-preview.png' });

    // Click into the heading line — its raw "# " marker is revealed for editing
    // (Obsidian's active-line behavior).
    await firstLine.click();
    await expect(firstLine).toContainText('#');
    await page.screenshot({ path: 'test-results/live-editor-active-line.png' });
  });

  test('renders a visible caret in the app theme (not CodeMirror\'s default black)', async ({ page }) => {
    // Regression: basicSetup's drawSelection hides the native caret and draws its own
    // .cm-cursor, whose default border is solid black — invisible on the dark pane.
    // Our editorTheme must paint it the app's text color (a CM theme without {dark}
    // otherwise inherits the light base default).
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.locator('.cm-content').click(); // focus → the cursor layer renders

    const cursor = page.locator('.cm-cursor').first();
    await cursor.waitFor();
    const borderColor = await cursor.evaluate((el) => getComputedStyle(el).borderLeftColor);
    // The bug was a black caret; ours uses --color-text-primary (dark theme #e8e8e8).
    expect(borderColor).not.toBe('rgb(0, 0, 0)');
    expect(borderColor).toBe('rgb(232, 232, 232)');
  });

  test('typing edits the document and reports changes', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor&empty=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-content').click();
    await page.keyboard.type('# Fresh note', { delay: 8 });

    // The change is reported with the new document text, and renders as a heading.
    await page.waitForFunction(() => window.__HARNESS__.getCalls('change').length > 0);
    const last = await page.evaluate(() => {
      const calls = window.__HARNESS__.getCalls('change');
      return calls[calls.length - 1][0] as string;
    });
    expect(last).toBe('# Fresh note');
    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
  });

  test('mod-click on a wiki link reports the href to follow', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-link');

    const modifier = process.platform === 'darwin' ? 'Meta' : 'Control';
    await page.locator('.cm-md-link').first().click({ modifiers: [modifier] });

    const calls = await page.evaluate(() => window.__HARNESS__.getCalls('followLink'));
    expect(calls).toHaveLength(1);
    expect(calls[0][0]).toBe('/knowledge/areas/foo.md');
  });

  test('selecting text reports a non-empty selection', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // Select the whole first line (the heading text).
    await page.locator('.cm-line').first().click();
    await page.keyboard.press('Home');
    await page.keyboard.press('Shift+End');

    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('selectionChange');
      const last = calls[calls.length - 1]?.[0] as { text?: string } | null;
      return !!last && typeof last.text === 'string' && last.text.length > 0;
    });
    const last = await page.evaluate(() => {
      const calls = window.__HARNESS__.getCalls('selectionChange');
      return calls[calls.length - 1][0] as { text: string; top: number; left: number };
    });
    expect(last.text).toContain('Notebook heading');
  });
});
