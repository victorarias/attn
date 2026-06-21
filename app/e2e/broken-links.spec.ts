import { test, expect } from '@playwright/test';

// Broken-link flagging in the live editor, rendered in a real browser by its dedicated
// harness (CM cannot mount under happy-dom, and the headless unit tests cannot cover
// the async existence-check → StateEffect → repaint path). The harness stubs
// `existsFile` to report any path containing "missing" as absent.

test.describe('LiveMarkdownEditor broken links', () => {
  test('flags a link to a missing note, leaves real and external links alone', async ({ page }) => {
    await page.goto('/test-harness/?component=BrokenLinks');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // The missing-note link picks up the broken decoration once its async check
    // resolves and forces a repaint.
    const broken = page.locator('.cm-md-link-broken');
    await expect(broken).toHaveCount(1);
    await expect(broken).toContainText('ghost');

    // The link to a real note is a normal link, never flagged broken.
    const real = page.locator('.cm-md-link', { hasText: 'real' });
    await expect(real).toBeVisible();
    await expect(real).not.toHaveClass(/cm-md-link-broken/);

    // The broken flag is red, distinct from the accent a working link uses.
    const color = await broken.evaluate((el) => getComputedStyle(el).color);
    const realColor = await real.evaluate((el) => getComputedStyle(el).color);
    expect(color).not.toBe(realColor);

    // The existence check ran for the in-notebook links but NEVER for the external one.
    const checked = await page.evaluate(() => window.__HARNESS__.getCalls('existsFile').map((c) => c[0]));
    expect(checked).toContain('/knowledge/areas/missing.md');
    expect(checked).toContain('/knowledge/areas/real.md');
    expect(checked.some((p: string) => p.includes('example.com'))).toBe(false);

    await page.screenshot({ path: 'test-results/broken-links.png' });
  });

  test('checks and flags a newly-added broken link when the document changes', async ({ page }) => {
    await page.goto('/test-harness/?component=BrokenLinks');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');
    await expect(page.locator('.cm-md-link-broken')).toHaveCount(1);

    // Add a second link to a missing note: the docChanged rebuild discovers the new
    // path, checks it, and flags it once the check resolves.
    await page.getByTestId('add-missing-link').click();
    await expect(page.locator('.cm-md-link-broken')).toHaveCount(2);

    const checked = await page.evaluate(() => window.__HARNESS__.getCalls('existsFile').map((c) => c[0]));
    expect(checked).toContain('/knowledge/areas/missing-extra.md');
  });
});
