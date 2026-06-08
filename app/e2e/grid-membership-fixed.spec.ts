import { test, expect } from '@playwright/test';

// Regression for "only every other hide actually hides": with a FIXED 2×2 shape,
// removing one of four tiles leaves the resolved shape at 2×2 (the grid dims do
// not change). The compositor's render-on-demand loop used to skip the repaint in
// that case, so the removed tile's stale frame lingered until the next dirtying
// event. This drives the real WebGL renderer and confirms the removed tile is
// actually gone after a single remove.

test.describe('Grid membership — fixed shape removal repaints immediately', () => {
  test('removing a tile under an unchanged 2×2 shape repaints right away', async ({ page }) => {
    await page.goto('/test-harness/?component=GridView&layout=fixed');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    // Let ghostty load, the first paint settle, and the seeded content land.
    await page.waitForTimeout(1100);

    const viewport = page.viewportSize()!;
    await page.screenshot({ path: 'test-results/grid-fixed-before.png' });

    // Top-left tile of the 2×2 is the first tile ('api server'). Hover it to
    // reveal its remove (×), then remove it.
    await page.mouse.move(viewport.width / 4, viewport.height / 4);
    await expect(page.locator('.grid-tile-remove')).toBeVisible();
    await page.locator('.grid-tile-remove').click();

    const removeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('onRemove'));
    expect(removeCalls).toEqual([['s1']]);
    await expect(page.locator('.grid-hidden-toggle')).toHaveText('1 hidden');

    // Give the reflow a few frames; without the fix nothing repaints here.
    await page.waitForTimeout(500);
    await page.screenshot({ path: 'test-results/grid-fixed-after.png' });
  });
});
