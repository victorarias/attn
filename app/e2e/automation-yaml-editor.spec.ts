import { test, expect, type Page } from '@playwright/test';

// Editing controls the AutomationYamlEditor harness exposes for the scroll
// tests below — same shape as LiveMarkdownEditorHarness's.
declare global {
  interface Window {
    __EDITOR_HARNESS__?: {
      applyExternal: (next: string) => void;
      swapValue: (next: string) => void;
    };
  }
}

// Scroll well into the long automation buffer and wait for CodeMirror to
// ACKNOWLEDGE the scroll (a deep, virtualized line attaches to the DOM) before
// returning the settled scrollTop — same rationale as
// live-markdown-editor.spec.ts's scrollIntoLongNote, which this mirrors.
async function scrollIntoLongBuffer(page: Page): Promise<number> {
  const scroller = page.locator('.cm-scroller');
  await scroller.evaluate((el) => { el.scrollTop = 600; });
  await expect(
    page.locator('.cm-line', { hasText: 'comment line number 25 of the long automation' }),
  ).toBeAttached();
  return scroller.evaluate((el) => el.scrollTop);
}

// The automation editor's single YAML buffer (CodeMirror), rendered in
// isolation by the component harness in a real browser — CM cannot mount
// under happy-dom (see AutomationEditor.test.tsx / AutomationsPanel.test.tsx,
// which mock this leaf component out for exactly that reason). This is where
// its three load-bearing, hard-to-get-right behaviors are actually verified:
// syntax-highlighting theme (classHighlighter, not CM's built-in white-in-dark
// theme), the Cmd-rewritten search keymap, and minimal-edit scroll
// preservation for Reload.
test.describe('AutomationYamlEditor', () => {
  test('renders YAML syntax highlighting via classHighlighter tok- classes, over the dark app theme', async ({ page }) => {
    await page.goto('/test-harness/?component=AutomationYamlEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // classHighlighter (not react-codemirror's built-in theme) is driving
    // highlighting: YAML keys and the boolean/string values pick up tok-*
    // classes.
    await expect(page.locator('.tok-propertyName, .tok-atom, .tok-string, .tok-bool').first()).toBeVisible();

    // The editor sits on the app's dark background, not a default white box —
    // the bug this design deliberately avoids (see editorTheme's doc comment
    // in AutomationYamlEditor.tsx: react-codemirror's built-in theme paints
    // white in dark mode).
    const bg = await page.locator('.cm-editor').evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(bg).not.toBe('rgb(255, 255, 255)');

    await page.screenshot({ path: 'test-results/automation-yaml-editor.png' });
  });

  test('typing edits the buffer and reports changes', async ({ page }) => {
    await page.goto('/test-harness/?component=AutomationYamlEditor&empty=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-content').click();
    await page.keyboard.type('id: fresh-automation', { delay: 8 });

    await page.waitForFunction(() => window.__HARNESS__.getCalls('change').length > 0);
    const last = await page.evaluate(() => {
      const calls = window.__HARNESS__.getCalls('change');
      return calls[calls.length - 1][0] as string;
    });
    expect(last).toBe('id: fresh-automation');
  });

  test('Cmd-F opens the search panel (macSearchKeymap survives a non-Mac-reporting browser)', async ({ page }) => {
    // The bug this rewrite avoids: searchKeymap binds "Mod-f", which
    // CodeMirror resolves to Ctrl (not Cmd) on any browser reporting a
    // non-Mac platform — exactly what headless Chromium on Linux CI runners
    // does. macSearchKeymap rewrites every "Mod-" prefix to an explicit
    // "Cmd-" so Cmd+F opens search here regardless of what the runner reports.
    await page.goto('/test-harness/?component=AutomationYamlEditor');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.locator('.cm-content').click();

    await expect(page.locator('.cm-panel.cm-search')).not.toBeAttached();
    await page.keyboard.press('Meta+f');
    await expect(page.locator('.cm-panel.cm-search')).toBeVisible();
  });

  test('keeps the buffer scrolled in place when Reload applies new content (minimal edit)', async ({ page }) => {
    // Mirrors live-markdown-editor.spec.ts's equivalent test: AutomationEditor's
    // Reload button drives applyExternalContent, which must not yank the
    // viewport back to the top when it re-confirms mostly-unchanged text.
    await page.goto('/test-harness/?component=AutomationYamlEditor&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const scroller = page.locator('.cm-scroller');
    const before = await scrollIntoLongBuffer(page);
    expect(before).toBeGreaterThan(400);

    // The daemon's reload response changes only the last comment line; the
    // rest of the document — including everything above the fold — is
    // unchanged.
    await page.evaluate(() => {
      const lines = Array.from({ length: 80 }, (_, i) => `comment line number ${i + 1} of the long automation`);
      lines[79] = 'comment line number 80 of the long automation — RELOADED';
      window.__EDITOR_HARNESS__!.applyExternal(`id: long-automation\nname: Long automation\n# ${lines.join('\n# ')}\n`);
    });

    await page.waitForFunction(() => {
      const calls = window.__HARNESS__.getCalls('change');
      const last = calls[calls.length - 1]?.[0] as string | undefined;
      return !!last && last.includes('RELOADED');
    });
    await expect
      .poll(() => scroller.evaluate((el, b) => Math.abs(el.scrollTop - b), before))
      .toBeLessThanOrEqual(4);
  });

  test('contrast: a full value swap snaps the scroller to the top (the bug the fix avoids)', async ({ page }) => {
    await page.goto('/test-harness/?component=AutomationYamlEditor&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const scroller = page.locator('.cm-scroller');
    expect(await scrollIntoLongBuffer(page)).toBeGreaterThan(400);

    await page.evaluate(() => {
      const lines = Array.from({ length: 80 }, (_, i) => `comment line number ${i + 1} of the long automation`);
      lines[79] = 'comment line number 80 of the long automation — RELOADED';
      window.__EDITOR_HARNESS__!.swapValue(`id: long-automation\nname: Long automation\n# ${lines.join('\n# ')}\n`);
    });

    await expect.poll(() => scroller.evaluate((el) => el.scrollTop)).toBeLessThan(50);
  });
});
