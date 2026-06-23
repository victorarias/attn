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
    // The card surfaces PROPERTIES — type, tags, sources, dates — but never a title.
    await expect(card.locator('.cm-md-fm-type')).toHaveText('area');
    await expect(card.locator('.cm-md-fm-tag')).toHaveCount(3);
    await expect(card.locator('.cm-md-fm-source')).toHaveText('/knowledge/areas/notebook.md');
    await expect(card.locator('.cm-md-fm-dates')).toContainText('created');
    await expect(card.locator('.cm-md-fm-title')).toHaveCount(0);
    // The title is the `# H1` rendered below the card.
    await expect(page.locator('.cm-md-h1').first()).toHaveText('Context rail');
    // The raw fence/keys are not shown as text while the card is up.
    await expect(page.locator('.cm-content')).not.toContainText('type: area');
    await page.screenshot({ path: 'test-results/frontmatter-card.png' });
  });

  test('clicking the card reveals raw YAML, clicking the body restores the card', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-frontmatter');

    // Click the card → it hands off to raw YAML editing (cursor moves into the block).
    await page.locator('.cm-md-frontmatter').click();
    await expect(page.locator('.cm-md-frontmatter')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('type: area');
    await page.screenshot({ path: 'test-results/frontmatter-revealed.png' });

    // Click into the body heading → the cursor leaves the block, the card returns.
    await page.locator('.cm-line', { hasText: 'Context rail' }).click();
    await expect(page.locator('.cm-md-frontmatter')).toBeVisible();
    await expect(page.locator('.cm-content')).not.toContainText('type: area');
  });

  test('focusing the body never expands frontmatter under the pointer click', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-frontmatter');

    // Record even a transient raw-YAML mount. The old focus-driven gate expanded the
    // block before CodeMirror placed the body selection, changing geometry mid-click.
    await page.evaluate(() => {
      const content = document.querySelector('.cm-content')!;
      (window as typeof window & { __rawFrontmatterSeen?: boolean }).__rawFrontmatterSeen = false;
      const observer = new MutationObserver(() => {
        if (content.textContent?.includes('type: area')) {
          (window as typeof window & { __rawFrontmatterSeen?: boolean }).__rawFrontmatterSeen = true;
        }
      });
      observer.observe(content, { childList: true, subtree: true, characterData: true });
    });

    await page.locator('.cm-line', { hasText: 'Context rail' }).click();
    await page.waitForTimeout(50);

    await expect(page.locator('.cm-md-frontmatter')).toBeVisible();
    expect(await page.evaluate(
      () => (window as typeof window & { __rawFrontmatterSeen?: boolean }).__rawFrontmatterSeen,
    )).toBe(false);
  });

  test('keeps long markdown rendered and ArrowUp local beyond character 3,000', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard&long=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-md-frontmatter');

    const scroller = page.locator('.cm-scroller');
    const stage2 = page.locator('.cm-line', { hasText: 'Stage 2: Rendered after' });
    // CodeMirror virtualizes offscreen lines, so scroll the editor before locating
    // Stage 2 in the DOM rather than asking Playwright to scroll a nonexistent node.
    // Acknowledge an intermediate scroll first. CM's virtual scroller can otherwise
    // restore its still-top internal anchor when a raw jump to the end races the first
    // measure (the same constraint covered by scrollIntoLongNote in the editor specs).
    await scroller.evaluate((el) => { el.scrollTop = 1800; });
    await expect(page.locator('.cm-line', { hasText: 'Supporting paragraph 40 ' })).toBeAttached();
    await expect(stage2).toBeAttached();
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(500);

    // Off the active line, the heading marker is hidden even though this heading is
    // beyond CM's initial 3,000-character parse window.
    await expect(stage2).not.toContainText('###');
    await stage2.click();

    // Chromium resolves this deep decorated-line click onto the following blank line.
    // ArrowUp must move locally onto Stage 2, not jump to the frontmatter atom.
    await page.keyboard.press('ArrowUp');
    await expect(stage2).toContainText('###');
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(500);

    await page.keyboard.press('ArrowUp');
    await expect(stage2).not.toContainText('###');
    expect(await scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(500);

    // A cursor transaction must not discard decorations for the remaining suffix.
    const stage3 = page.locator('.cm-line', { hasText: 'Stage 3: Rendering remains stable' });
    await scroller.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    await expect(stage3).toBeAttached();
    await expect(stage3).not.toContainText('###');
    await expect(page.locator('.cm-line', { hasText: 'Stage complete when:' })).not.toContainText('**');
  });

  test('a frontmatter-only note stays raw (no body line to anchor the cursor)', async ({ page }) => {
    await page.goto('/test-harness/?component=FrontmatterCard&empty=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // The guard keeps the whole document from being swallowed by a block widget.
    await expect(page.locator('.cm-md-frontmatter')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('type: area');
  });
});
