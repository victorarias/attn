import { test, expect, type Page, type Locator } from '@playwright/test';

async function openReviewPanelHarness(page: Page, query: string) {
  await page.goto(`/test-harness/?component=ReviewPanel&${query}`);
  await page.waitForFunction(() => Boolean(window.__HARNESS__?.ready), null, { timeout: 5000 });
  await expect(page.locator('.review-panel')).toBeVisible();
}

function fileItem(page: Page, filePath: string): Locator {
  return page.locator('.file-item').filter({
    has: page.locator(`.file-name[title="${filePath}"]`),
  });
}

test.describe('ReviewPanel Harness', () => {
  test('shows local refs immediately while remotes sync in background', async ({ page }) => {
    await openReviewPanelHarness(
      page,
      'fetchDelayMs=1500&localFiles=src/local.ts&refreshedFiles=src/local.ts,src/remote.ts'
    );

    await expect(fileItem(page, 'src/local.ts')).toBeVisible();
    await expect(page.locator('.review-sync-status.syncing')).toContainText('Syncing with origin...');

    await expect(fileItem(page, 'src/remote.ts')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('.review-sync-status.syncing')).toHaveCount(0);
  });

  test('keeps panel usable when remotes sync fails', async ({ page }) => {
    await openReviewPanelHarness(
      page,
      'fetchFail=1&localFiles=src/local.ts'
    );

    await expect(fileItem(page, 'src/local.ts')).toBeVisible();
    await expect(page.locator('.review-sync-status.warning')).toContainText(
      'Could not refresh remotes; showing local refs'
    );
  });

  test('preserves selected file across background refresh', async ({ page }) => {
    await openReviewPanelHarness(
      page,
      'fetchDelayMs=1500&localFiles=src/a.ts,src/b.ts&refreshedFiles=src/b.ts,src/c.ts'
    );

    await expect(fileItem(page, 'src/a.ts')).toBeVisible();
    await expect(fileItem(page, 'src/b.ts')).toBeVisible();

    await fileItem(page, 'src/b.ts').click();
    await expect(fileItem(page, 'src/b.ts')).toHaveClass(/selected/);

    await expect(fileItem(page, 'src/c.ts')).toBeVisible({ timeout: 5000 });
    await expect(fileItem(page, 'src/b.ts')).toHaveClass(/selected/);
  });
});
