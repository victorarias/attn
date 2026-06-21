import { test, expect } from '@playwright/test';

// The notebook tile mounts the same NotebookSurface as the fullscreen modal, in a
// `tile` variant that folds its side panes responsively as the tile narrows. This
// exercises the render branch (context → NotebookTile → NotebookSurface) and the
// width-driven auto-fold (useTileAutoFold) end to end, in a real browser, through
// the actual ResizeObserver — neither of which the unit tests can cover together.
test.describe('NotebookTile (workspace tile)', () => {
  test('renders the live surface and folds its rail then tree as it narrows', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookTile');

    // The tile seeds from initialPath, so the live editor mounts with the note.
    const editor = page.locator('.cm-editor');
    await expect(editor).toBeVisible();
    await expect(page.locator('.notebook-browser-list')).toBeVisible();

    const body = page.locator('.notebook-browser-body');
    // Wide (1100px): markdown note → three columns, nothing auto-folded.
    await expect(body).toHaveClass(/has-rail/);
    await expect(body).not.toHaveClass(/tree-folded/);
    await expect(body).not.toHaveClass(/rail-folded/);

    // Medium (760px, < 900): the context rail auto-folds; the tree stays.
    await page.evaluate(() => window.__setTileWidth?.(760));
    await expect(body).toHaveClass(/rail-folded/);
    await expect(body).not.toHaveClass(/tree-folded/);

    // Narrow (520px, < 620): the file tree auto-folds too — document only.
    await page.evaluate(() => window.__setTileWidth?.(520));
    await expect(body).toHaveClass(/tree-folded/);
    await expect(body).toHaveClass(/rail-folded/);
    // The folded tree collapses to zero width but stays mounted (state survives).
    // Poll past the 180ms fold transition for the settled width.
    await expect(page.locator('.notebook-browser-list')).toHaveCount(1);
    await expect.poll(
      () => page.locator('.notebook-browser-list').evaluate((el) => el.getBoundingClientRect().width),
      { timeout: 2000 },
    ).toBeLessThan(2);

    // Widening again unfolds both panes (auto follows width; no manual override set).
    await page.evaluate(() => window.__setTileWidth?.(1100));
    await expect(body).not.toHaveClass(/tree-folded/);
    await expect(body).not.toHaveClass(/rail-folded/);
  });
});

declare global {
  interface Window {
    __setTileWidth?: (px: number) => void;
  }
}
