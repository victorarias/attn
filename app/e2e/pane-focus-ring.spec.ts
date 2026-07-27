import { test, expect } from '@playwright/test';

// Regression witness for the SessionTerminalWorkspace focus-ring z-index bug:
// the active-pane `::after` ring must paint above any pane-local overlay
// (currently the bound-ticket overlay), on the real stylesheet, in a real
// browser — a stacking bug jsdom/happy-dom cannot see since neither applies
// the component's CSS file. See SessionTerminalWorkspace.css.
test('active-pane focus ring paints above the ticket overlay', async ({ page }) => {
  await page.goto('/test-harness/?component=PaneFocusRing');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const pane = page.locator('[data-testid="pane-active"]');
  const overlay = page.locator('[data-testid="ticket-overlay"]');
  await expect(overlay).toBeVisible();

  const [paneZ, overlayZ] = await Promise.all([
    pane.evaluate((el) => getComputedStyle(el, '::after').zIndex),
    overlay.evaluate((el) => getComputedStyle(el).zIndex),
  ]);
  expect(Number(paneZ)).toBeGreaterThan(Number(overlayZ));
});
