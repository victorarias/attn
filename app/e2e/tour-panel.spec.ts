import { expect, test } from '@playwright/test';

test('keeps the visible Tour diff position stable while typing feedback', async ({ page }) => {
  await page.goto('/test-harness/?component=TourPanel');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await expect(page.locator('diffs-container')).toBeVisible();

  const main = page.locator('.tour-panel__main');
  const diffScroller = page.locator('.diff-view-scroller');
  await expect.poll(
    () => diffScroller.evaluate((element) => element.scrollHeight - element.clientHeight),
  ).toBeGreaterThan(500);
  expect(await main.evaluate((element) => element.scrollHeight - element.clientHeight)).toBeLessThanOrEqual(1);

  await diffScroller.evaluate((element) => {
    element.scrollTop = Math.min(900, element.scrollHeight - element.clientHeight);
    element.dispatchEvent(new Event('scroll'));
  });
  const anchoredScrollTop = await diffScroller.evaluate((element) => element.scrollTop);
  expect(anchoredScrollTop).toBeGreaterThan(500);

  await page.getByRole('button', { name: 'Conversation' }).click();
  const textarea = page.getByPlaceholder('Feedback on this file');
  await textarea.pressSequentially('Typing here must not move the diff.', { delay: 10 });

  await expect.poll(
    () => diffScroller.evaluate((element) => element.scrollTop),
  ).toBeCloseTo(anchoredScrollTop, 0);
});

test('keeps the visible Tour diff position stable while typing an inline comment', async ({ page }) => {
  await page.goto('/test-harness/?component=TourPanel');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await expect(page.locator('diffs-container')).toBeVisible();

  const main = page.locator('.tour-panel__main');
  const diffScroller = page.locator('.diff-view-scroller');
  await expect.poll(
    () => diffScroller.evaluate((element) => element.scrollHeight - element.clientHeight),
  ).toBeGreaterThan(500);
  expect(await main.evaluate((element) => element.scrollHeight - element.clientHeight)).toBeLessThanOrEqual(1);
  await diffScroller.evaluate((element) => {
    element.scrollTop = Math.min(900, element.scrollHeight - element.clientHeight);
    element.dispatchEvent(new Event('scroll'));
  });
  await expect.poll(() => diffScroller.evaluate((element) => element.scrollTop)).toBeGreaterThan(500);

  const lineNumbers = page.locator('diffs-container [data-line-index][data-column-number]');
  const visibleLineIndex = await lineNumbers.evaluateAll((elements) => {
    const viewport = document.querySelector('.diff-view-scroller')?.getBoundingClientRect();
    if (!viewport) return -1;
    return elements.findIndex((element) => {
      const rect = element.getBoundingClientRect();
      return rect.top > viewport.top + 80 && rect.bottom < viewport.bottom - 160;
    });
  });
  expect(visibleLineIndex).toBeGreaterThanOrEqual(0);

  await lineNumbers.nth(visibleLineIndex).hover();
  await page.locator('diffs-container [data-utility-button]:visible').click();

  const textarea = page.locator('.diff-comment-form textarea');
  await expect(textarea).toBeVisible();
  const anchoredScrollTop = await diffScroller.evaluate((element) => element.scrollTop);
  expect(anchoredScrollTop).toBeGreaterThan(500);

  await textarea.pressSequentially('Typing here ', { delay: 10 });
  await page.evaluate(() => {
    const api = window.__HARNESS__ as unknown as { refreshTourSnapshot: () => void };
    api.refreshTourSnapshot();
  });
  await textarea.pressSequentially('must not move the diff.', { delay: 10 });

  await expect.poll(
    () => diffScroller.evaluate((element) => element.scrollTop),
  ).toBeCloseTo(anchoredScrollTop, 0);
});

test('keeps the diff usable with long guidance, Mermaid, a hotspot, and large UI scale', async ({ page }) => {
  await page.goto('/test-harness/?component=TourPanel&guidance=long&scale=1.5');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await expect(page.locator('.tour-panel__mermaid')).toBeVisible();

  const main = page.locator('.tour-panel__main');
  const lensContent = page.locator('.tour-panel__lens-content');
  const hotspot = page.locator('.tour-panel__hotspot');
  const diffScroller = page.locator('.diff-view-scroller');

  expect(await main.evaluate((element) => element.scrollHeight - element.clientHeight)).toBeLessThanOrEqual(1);
  expect(await lensContent.evaluate((element) => element.scrollHeight - element.clientHeight)).toBeGreaterThan(0);
  expect(await hotspot.evaluate((element) => element.scrollHeight - element.clientHeight)).toBeGreaterThan(0);
  expect(await diffScroller.evaluate((element) => element.clientHeight)).toBeGreaterThan(120);
  expect(await diffScroller.evaluate((element) => element.scrollHeight - element.clientHeight)).toBeGreaterThan(500);
});

test('renders Markdown files as documents and can return to their changes', async ({ page }) => {
  await page.goto('/test-harness/?component=TourPanel&file=markdown');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const displayMode = page.getByRole('group', { name: 'Markdown display mode' });
  const rendered = displayMode.getByRole('button', { name: 'Rendered' });
  const changes = displayMode.getByRole('button', { name: 'Changes' });

  await expect(page.getByRole('heading', { name: 'Rendered Tour document' })).toBeVisible();
  await expect(page.locator('.tour-panel__markdown-preview strong')).toHaveText('Markdown as a document');
  await expect(page.locator('diffs-container')).toHaveCount(0);
  await expect(rendered).toHaveAttribute('aria-pressed', 'true');

  await changes.click();
  await expect(page.locator('diffs-container')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Rendered Tour document' })).toHaveCount(0);
  await expect(changes).toHaveAttribute('aria-pressed', 'true');

  await rendered.click();
  await expect(page.getByRole('heading', { name: 'Rendered Tour document' })).toBeVisible();
});
