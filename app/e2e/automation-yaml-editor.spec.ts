import { test, expect, type Page } from '@playwright/test';

// Editing controls the AutomationYamlEditor harness exposes for the scroll
// tests below — same shape as LiveMarkdownEditorHarness's.
declare global {
  interface Window {
    // Declaration-merged with live-markdown-editor.spec.ts's identical block,
    // so the shape must stay in step with it: it describes whichever harness
    // page is loaded, and LiveMarkdownEditorHarness still exposes swapValue.
    // AutomationYamlEditorHarness does not — this spec never calls it.
    __EDITOR_HARNESS__?: {
      applyExternal: (next: string) => void;
      swapValue: (next: string) => void;
    };
  }
}

// Scroll well into the long automation buffer and wait for CodeMirror to
// ACKNOWLEDGE the scroll (a deep, virtualized line attaches to the DOM) before
// returning the settled scrollTop — same rationale as
// live-markdown-editor.spec.ts's scrollIntoLongNote, which this mirrors. The
// selection test needs this because the line it anchors on is virtualized: it
// has to be attached to the DOM before it can be clicked.
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
// theme), the Cmd-rewritten search keymap, and minimal-edit selection
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

  test('Reload leaves a selection above the edit exactly where it was (minimal edit)', async ({ page }) => {
    // This is what pins the minimal-edit path for Reload, and it is
    // deliberately an assertion about change MAPPING, not about scroll
    // position.
    //
    // There used to be a scroll-position pair here, mirroring
    // live-markdown-editor.spec.ts: one test asserting Reload holds the
    // viewport, and a contrast test asserting a full-document value swap
    // snaps it to the top. Both are gone because neither could fail for THIS
    // buffer. The contrast test went red in CI while passing locally, and
    // mutation testing then showed the positive one survived every mutation
    // aimed at it — a forced full-document replace AND scrollIntoView:true,
    // with the edit placed off screen. Scroll position after a doc-wide
    // replace is a measurement side effect of CodeMirror's height map, so it
    // varies with line heights, wrapping, and font metrics; the YAML buffer's
    // short comment lines simply do not reproduce what the markdown buffer's
    // wrapped prose does. A test that cannot fail is worse than no test.
    //
    // Change mapping is specified rather than measured: an edit confined to
    // line 80 is entirely after this selection, so mapping must leave the
    // selection byte-identical. A full replace (from 0 to doc.length, which is
    // what a plain controlled-value swap dispatches) deletes the selected text
    // out from under it and cannot preserve it. Verified red under exactly
    // that mutation.
    await page.goto('/test-harness/?component=AutomationYamlEditor&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await scrollIntoLongBuffer(page);

    // Select a whole line well above the edit site.
    const anchorLine = page.locator('.cm-line', { hasText: 'comment line number 25 of the long automation' });
    await anchorLine.click();
    await page.keyboard.press('Home');
    await page.keyboard.press('Shift+End');
    const selectedBefore = await page.evaluate(() => window.getSelection()?.toString() ?? '');
    expect(selectedBefore).toContain('comment line number 25 of the long automation');

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

    expect(await page.evaluate(() => window.getSelection()?.toString() ?? '')).toBe(selectedBefore);
  });
});
