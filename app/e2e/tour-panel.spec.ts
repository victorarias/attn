import { expect, test } from '@playwright/test';

test('keeps the visible Tour diff position stable while typing a Tour-wide question', async ({ page }) => {
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
  const textarea = page.getByPlaceholder('Ask about this Tour');
  await textarea.pressSequentially('Typing here must not move the diff.', { delay: 10 });

  await expect.poll(
    () => diffScroller.evaluate((element) => element.scrollTop),
  ).toBeCloseTo(anchoredScrollTop, 0);
});

test('keeps Tour shortcuts out of a background terminal target', async ({ page }) => {
  await page.goto('/test-harness/?component=TourPanel');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  await page.getByTestId('tour-background-terminal').evaluate((element) => {
    (element as HTMLInputElement).focus();
  });
  await page.keyboard.press('a');

  await expect(page.getByLabel('Tour conversation')).toBeVisible();
  const state = await page.evaluate(() => ({
    activeInTour: Boolean((document.activeElement as HTMLElement | null)?.closest('.tour-panel')),
    backgroundKeys: (window.__HARNESS__ as unknown as { backgroundKeys: string[] }).backgroundKeys,
  }));
  expect(state.activeInTour).toBe(true);
  expect(state.backgroundKeys).toEqual([]);
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
  const findVisibleLineIndex = () => lineNumbers.evaluateAll((elements) => {
    const viewport = document.querySelector('.diff-view-scroller')?.getBoundingClientRect();
    if (!viewport) return -1;
    return elements.findIndex((element) => {
      const rect = element.getBoundingClientRect();
      return rect.top > viewport.top + 80 && rect.bottom < viewport.bottom - 160;
    });
  });
  await expect.poll(findVisibleLineIndex).toBeGreaterThanOrEqual(0);
  const visibleLineIndex = await findVisibleLineIndex();
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

test('reveals authored notes beside unchanged code without expanding the whole file', async ({ page }) => {
  await page.goto('/test-harness/?component=TourPanel&file=outside-annotation');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const annotation = page.locator('.diff-comment').filter({
    hasText: 'This note needs local context even though the line did not change.',
  });
  await expect(annotation).toBeVisible();
  await expect(annotation).toContainText(
    'This note needs local context even though the line did not change.',
  );
  await expect(page.locator('.diff-stale-comments')).toHaveCount(0);

  const contextBlock = page.locator('.diff-comment-context-block');
  const renderedLineNumbers = page.locator('diffs-container [data-line-number-content]');
  const targetLine = contextBlock.locator('[data-line-number-content]').filter({ hasText: /^150$/ });
  await expect(targetLine).toBeVisible();
  expect(await renderedLineNumbers.count()).toBeLessThan(80);

  await targetLine.hover();
  await contextBlock.locator('[data-utility-button]:visible').click();
  await contextBlock.locator('.diff-comment-form textarea').fill('Reviewer note on unchanged context.');
  await contextBlock.locator('.diff-comment-form .save-btn').click();
  await expect(contextBlock.locator('.diff-comment')).toContainText([
    'This note needs local context',
    'Reviewer note on unchanged context.',
  ]);
});

test('renders Markdown files as documents and uses the diff for commentable source', async ({ page }) => {
  await page.goto('/test-harness/?component=TourPanel&file=markdown');
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);

  const displayMode = page.getByRole('group', { name: 'Markdown display mode' });
  const rendered = displayMode.getByRole('button', { name: 'Rendered' });
  const source = displayMode.getByRole('button', { name: 'Source' });

  await expect(page.getByRole('heading', { name: 'Rendered Tour document' })).toBeVisible();
  await expect(page.locator('.tour-panel__markdown-preview strong')).toHaveText('Markdown as a document');
  await expect(page.locator('diffs-container')).toHaveCount(0);
  await expect(rendered).toHaveAttribute('aria-pressed', 'true');

  await source.click();
  await expect(page.locator('diffs-container')).toBeVisible();
  await expect(page.locator('diffs-container [data-line-number-content]').first()).toBeVisible();
  await expect(page.locator('diffs-container > [slot^="annotation-"]')).toHaveCount(1);
  await expect(page.locator('.diff-comment').first()).toContainText('Verify the rendered structure');
  await expect(page.locator('.diff-comment').first().locator('.diff-comment-actions')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Rendered Tour document' })).toHaveCount(0);
  await expect(source).toHaveAttribute('aria-pressed', 'true');

  const lineNumbers = page.locator('diffs-container [data-line-index][data-column-number]');
  await lineNumbers.nth(3).hover();
  await page.locator('diffs-container [data-utility-button]:visible').click();
  const comment = page.locator('.diff-comment-form textarea');
  await comment.fill('Reviewer annotation on the Markdown source.');
  await page.locator('.diff-comment-form .save-btn').click();
  await expect(page.locator('.diff-comment')).toContainText(['Verify the rendered structure', 'Reviewer annotation']);

  await rendered.click();
  await expect(page.getByRole('heading', { name: 'Rendered Tour document' })).toBeVisible();
});
