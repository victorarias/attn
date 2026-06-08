import { test, expect } from '@playwright/test';

// End-to-end membership flow against the real grid (WebGL renderer + ghostty,
// mock PTY) in the component harness — no daemon, no dev app. Verifies the
// hover-to-reveal remove (×) button, that removing reflows the grid and surfaces
// a restore control, and that restoring puts the session back.

test.describe('Grid membership (remove / restore)', () => {
  test('hover reveals the remove button; remove then restore round-trips', async ({ page }) => {
    await page.goto('/test-harness/?component=GridView');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    // Let ghostty load + the first paint settle.
    await page.waitForTimeout(900);

    const viewport = page.viewportSize()!;
    const third = viewport.width / 3;

    // No remove button until a tile is hovered.
    await expect(page.locator('.grid-tile-remove')).toHaveCount(0);

    // Hover the left tile (1×3 layout): its remove button appears at top-right.
    await page.mouse.move(third / 2, viewport.height / 2);
    await expect(page.locator('.grid-tile-remove')).toBeVisible();
    await page.screenshot({ path: 'test-results/grid-hover-remove.png' });

    // Remove it → the handler fires, the grid reflows to 2 tiles, and a
    // "1 hidden" restore control appears.
    await page.locator('.grid-tile-remove').click();
    const removeCalls = await page.evaluate(() => window.__HARNESS__.getCalls('onRemove'));
    expect(removeCalls).toEqual([['s1']]);
    await expect(page.locator('.grid-hidden-toggle')).toHaveText('1 hidden');
    await page.screenshot({ path: 'test-results/grid-after-remove.png' });

    // Open the restore list and put the session back.
    await page.locator('.grid-hidden-toggle').click();
    await expect(page.getByText('api server')).toBeVisible();
    await page.screenshot({ path: 'test-results/grid-restore-list.png' });
    await page.getByTitle('Restore api server').click();

    const restoreCalls = await page.evaluate(() => window.__HARNESS__.getCalls('onRestore'));
    expect(restoreCalls).toEqual([['s1']]);
    // Everything restored → the hidden control disappears.
    await expect(page.locator('.grid-hidden-toggle')).toHaveCount(0);
  });
});
