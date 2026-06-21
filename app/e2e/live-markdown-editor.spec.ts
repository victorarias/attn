import { test, expect } from '@playwright/test';

// Editing controls the LiveMarkdownEditor harness exposes for the scroll tests below.
declare global {
  interface Window {
    __EDITOR_HARNESS__?: {
      applyExternal: (next: string) => void;
      swapValue: (next: string) => void;
    };
  }
}

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

  test('keeps the reader scrolled in place when an on-disk change is applied (minimal edit)', async ({ page }) => {
    // The bug: a note that changes on disk while you read it (an agent edit, or an
    // unrelated fs event that re-read it) was pushed in via a full document swap, which
    // snaps CodeMirror's scroller back to the top. applyExternalContent applies it as a
    // minimal edit so the viewport stays anchored. This is a real-browser behavior
    // (CM can't mount under happy-dom), so it's verified here.
    await page.goto('/test-harness/?component=LiveMarkdownEditor&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const scroller = page.locator('.cm-scroller');
    // Scroll well down into the long note.
    await scroller.evaluate((el) => { el.scrollTop = 600; });
    const before = await scroller.evaluate((el) => el.scrollTop);
    expect(before).toBeGreaterThan(400);

    // An agent rewrites a line near the END of the note (below the fold). Applied as a
    // minimal edit, the reader's scroll position is preserved exactly.
    await page.evaluate(() => {
      // Replace the last paragraph's text; everything above the change is untouched.
      const doc = Array.from({ length: 80 }, (_, i) => `Paragraph line number ${i + 1} of the long note.`);
      doc[79] = 'Paragraph line number 80 of the long note — EDITED BY AGENT.';
      window.__EDITOR_HARNESS__!.applyExternal(`# Long note\n\n${doc.join('\n\n')}\n`);
    });

    // The edit landed (the document text changed) ...
    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('change');
      const last = calls[calls.length - 1]?.[0] as string | undefined;
      return !!last && last.includes('EDITED BY AGENT');
    });
    // ... and the scroller did NOT jump (allow a couple px for re-measure rounding).
    const after = await scroller.evaluate((el) => el.scrollTop);
    expect(Math.abs(after - before)).toBeLessThanOrEqual(4);
  });

  test('contrast: a full value swap snaps the scroller to the top (the bug the fix avoids)', async ({ page }) => {
    // Proves the harness actually detects a scroll reset, and documents WHY the minimal
    // edit is necessary: replacing the whole controlled value — react-codemirror's
    // default reconciliation — resets CodeMirror's scroll to the top.
    await page.goto('/test-harness/?component=LiveMarkdownEditor&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const scroller = page.locator('.cm-scroller');
    await scroller.evaluate((el) => { el.scrollTop = 600; });
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(400);

    await page.evaluate(() => {
      const doc = Array.from({ length: 80 }, (_, i) => `Paragraph line number ${i + 1} of the long note.`);
      doc[79] = 'Paragraph line number 80 of the long note — EDITED BY AGENT.';
      window.__EDITOR_HARNESS__!.swapValue(`# Long note\n\n${doc.join('\n\n')}\n`);
    });

    // react-codemirror reconciles the changed controlled value as a full-document
    // replace, which snaps the viewport back to the top. (That replace is tagged as an
    // external change, so it deliberately does NOT re-fire onChange — hence we observe
    // the scroller directly rather than waiting on a change call.)
    await expect.poll(() => scroller.evaluate((el) => el.scrollTop)).toBeLessThan(50);
  });

  test('renders list bullets, task checkboxes, and a fenced code block, and toggles a checkbox on click', async ({ page }) => {
    await page.goto('/test-harness/?component=LiveMarkdownEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // Bullet markers render as bullets; the two `- [ ]/[x]` items render as checkboxes
    // (one checked), and the fenced block renders as a panel with a dimmed fence +
    // language tag.
    await expect(page.locator('.cm-md-bullet').first()).toBeVisible();
    await expect(page.locator('.cm-md-checkbox')).toHaveCount(2);
    await expect(page.locator('.cm-md-checkbox.is-checked')).toHaveCount(1);
    await expect(page.locator('.cm-md-codeblock').first()).toBeVisible();
    await expect(page.locator('.cm-md-codefence').first()).toBeVisible();
    await expect(page.locator('.cm-md-codeinfo').first()).toBeVisible();
    await page.screenshot({ path: 'test-results/live-editor-polish.png' });

    // Clicking the open checkbox toggles its source `[ ]` → `[x]`.
    await page.locator('.cm-md-checkbox:not(.is-checked)').first().click();
    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('change');
      const last = calls[calls.length - 1]?.[0] as string | undefined;
      return !!last && last.includes('- [x] an open task');
    });
    await expect(page.locator('.cm-md-checkbox.is-checked')).toHaveCount(2);
  });
});
