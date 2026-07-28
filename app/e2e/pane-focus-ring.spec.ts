import { test, expect } from '@playwright/test';

// Regression witness for the SessionTerminalWorkspace selection-marker z-index:
// the edge rail and spotlight corner marks must paint above pane-local overlays.
// This needs a real browser because jsdom/happy-dom do not apply the stylesheet.
test('active-pane selection markers paint above the ticket overlay', async ({ page }) => {
  await page.goto('/test-harness/?component=PaneFocusRing');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const workspace = page.locator('[data-testid="workspace"]');
  const pane = page.locator('[data-testid="pane-active"]');
  const inactiveTile = page.locator('[data-testid="tile-inactive"]');
  const overlay = page.locator('[data-testid="ticket-overlay"]');
  await expect(overlay).toBeVisible();

  const overlayZ = Number(await overlay.evaluate((el) => getComputedStyle(el).zIndex));

  for (const style of ['rail', 'spotlight']) {
    await workspace.evaluate((el, selectionStyle) => {
      el.classList.remove('workspace-selection--dim', 'workspace-selection--rail', 'workspace-selection--spotlight');
      el.classList.add(`workspace-selection--${selectionStyle}`);
    }, style);

    const markerZ = Number(
      await pane.evaluate((el) => getComputedStyle(el, '::after').zIndex),
    );
    expect(markerZ, `${style} marker z-index`).toBeGreaterThan(overlayZ);
    await expect(inactiveTile, `${style} inactive tile opacity`).toHaveCSS('opacity', '0.58');
    await expect(pane, `${style} active pane opacity`).toHaveCSS('opacity', '1');
  }

  await workspace.evaluate((el) => {
    el.classList.remove('workspace-selection--rail', 'workspace-selection--spotlight');
    el.classList.add('workspace-selection--dim');
  });

  expect(await pane.evaluate((el) => getComputedStyle(el, '::before').content)).toBe('none');
  expect(await pane.evaluate((el) => getComputedStyle(el, '::after').content)).toBe('none');
  await expect(pane).toHaveCSS('box-shadow', 'none');
  await expect(inactiveTile).toHaveCSS('opacity', '0.58');
  await expect(pane).toHaveCSS('opacity', '1');
});
