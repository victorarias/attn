import { test, expect } from '@playwright/test';

// The in-tile fuzzy finder is a Cmd+P-style overlay scoped to one notebook tile.
// These exercise the full path the unit tests can't: the real keydown reaching the
// tile container, the live index → ranked list, and picking a note (which opens it
// and persists the path via onOpenFile). The harness mocks the daemon; initialPath
// is URL-controlled so we can test a seeded tile and a fresh (auto-open) tile.
test.describe('NotebookTile finder', () => {
  test('Cmd+P opens the finder; typing filters; Enter opens the note', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookTile');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    // Seeded tile: the editor mounts on the seed note, no finder yet.
    await expect(page.locator('.cm-editor')).toBeVisible();
    await expect(page.locator('.notebook-finder')).toHaveCount(0);

    // Focus inside the tile, then re-summon the finder with Cmd+P.
    await page.locator('.notebook-surface-tile').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();
    await expect(page.locator('.notebook-finder-input')).toBeFocused();

    // The empty query lists the whole (mocked) vault.
    await expect(page.locator('.notebook-finder-option')).toHaveCount(2);

    // Typing narrows to the journal note (matched by path/title).
    await page.locator('.notebook-finder-input').fill('journal');
    await expect(page.locator('.notebook-finder-option')).toHaveCount(1);
    await expect(page.locator('.notebook-finder-option-path')).toHaveText('journal/2026-06-20.md');

    // Enter opens the highlighted note: the finder closes and the path is persisted.
    await page.keyboard.press('Enter');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    const opened = await page.evaluate(() => window.__HARNESS__.getCalls('openFile').map((c) => c[0]));
    expect(opened).toContain('journal/2026-06-20.md');
  });

  test('Escape dismisses the finder back to the note', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookTile');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await expect(page.locator('.cm-editor')).toBeVisible();

    await page.locator('.notebook-surface-tile').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    // The note is still there — Escape closed the finder, nothing else.
    await expect(page.locator('.cm-editor')).toBeVisible();
  });

  test('a fresh tile (no seed) auto-opens the finder on its empty screen', async ({ page }) => {
    // Empty initialPath → no seed → the no-selection screen with the finder open.
    await page.goto('/test-harness/?component=NotebookTile&initialPath=');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    await expect(page.locator('.notebook-finder')).toBeVisible();
    await expect(page.locator('.notebook-finder-input')).toBeFocused();

    // Dismiss it and the empty state offers to reopen it.
    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await expect(page.getByText('Nothing selected')).toBeVisible();
    const reopen = page.locator('.notebook-finder-open-button');
    await expect(reopen).toBeVisible();

    // The "Find a note" button brings it back.
    await reopen.click();
    await expect(page.locator('.notebook-finder')).toBeVisible();
  });
});
