import { test, expect } from '@playwright/test';

// The in-editor frontmatter card (notebook stage 4b). A note's leading `---…---` YAML
// renders as a compact card; clicking it reveals the raw YAML for editing; clicking
// away re-renders the card. Verified in a real browser because the card is a CodeMirror
// block widget and CM can't mount under happy-dom.

test.describe('FrontmatterCard', () => {
  test('renders frontmatter as a card and hides the raw YAML', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    const card = page.locator('.cm-md-frontmatter');
    await expect(card).toBeVisible();
    // The card surfaces the parsed fields...
    await expect(card.locator('.cm-md-fm-title')).toHaveText('Context rail');
    await expect(card.locator('.cm-md-fm-type')).toHaveText('area');
    await expect(card.locator('.cm-md-fm-tag')).toHaveCount(3);
    await expect(card.locator('.cm-md-fm-source')).toHaveText('/knowledge/areas/notebook.md');
    // ...and the raw fence/keys are not shown as text while the card is up.
    await expect(page.locator('.cm-content')).not.toContainText('title: Context rail');

    // The body below the card still renders as live-preview markdown.
    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
    await page.screenshot({ path: 'test-results/frontmatter-card.png' });
  });

  test('clicking the card reveals raw YAML, clicking the body restores the card', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-frontmatter');

    // Click the card → it hands off to raw YAML editing (cursor moves into the block).
    await page.locator('.cm-md-frontmatter').click();
    await expect(page.locator('.cm-md-frontmatter')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('title: Context rail');
    await page.screenshot({ path: 'test-results/frontmatter-revealed.png' });

    // Click into the body heading → the cursor leaves the block, the card returns.
    await page.locator('.cm-line', { hasText: 'Body heading' }).click();
    await expect(page.locator('.cm-md-frontmatter')).toBeVisible();
    await expect(page.locator('.cm-content')).not.toContainText('title: Context rail');
  });

  test('a frontmatter-only note stays raw (no body line to anchor the cursor)', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard&empty=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // The guard keeps the whole document from being swallowed by a block widget.
    await expect(page.locator('.cm-md-frontmatter')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('title: Just properties');
  });
});
