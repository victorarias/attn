import { test, expect, type Page } from '@playwright/test';

/**
 * DiffView is a thin wrapper around @pierre/diffs. The library renders the diff
 * into a `diffs-container` custom element (shadow DOM + Shiki highlighting),
 * which jsdom/happy-dom cannot exercise — so these behaviors live here, not in a
 * vitest unit test.
 *
 * What we verify against the real rendered DOM:
 *  - the diff renders (line-number gutter present),
 *  - a seeded review comment is slotted into the library's native annotation
 *    framework (light-DOM `slot="annotation-<side>-<line>"`),
 *  - the thread's actions (resolve / edit / send / delete) fan out to our
 *    callbacks,
 *  - the line-selection -> popup -> draft -> save path creates a new comment,
 *  - the unified/split layout toggle re-renders the library.
 */

const SEEDED = '/test-harness/?component=DiffView';
const UNSEEDED = '/test-harness/?component=DiffView&seed=0';

async function openHarness(page: Page, url: string) {
  await page.goto(url);
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await page.waitForSelector('diffs-container');
  // Give Shiki one tick to tokenize and the annotations to slot.
  await page.locator('diffs-container [data-line-number-content]').first().waitFor();
}

function calls(page: Page, name: string) {
  return page.evaluate((n) => window.__HARNESS__.getCalls(n), name);
}

test.describe('DiffView (@pierre/diffs)', () => {
  test('renders the diff with a line-number gutter', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await expect(page.locator('diffs-container')).toBeVisible();
    // Unified small diff renders several numbered lines.
    const numbers = page.locator('diffs-container [data-line-number-content]');
    expect(await numbers.count()).toBeGreaterThan(0);
  });

  test('slots a seeded comment into the native annotation framework', async ({ page }) => {
    await openHarness(page, SEEDED);

    const thread = page.getByTestId('diff-comment-thread');
    await expect(thread).toBeVisible();
    await expect(thread).toContainText('Seeded comment on an added line');
    await expect(thread).toContainText('You');

    // The library slots the thread at the comment's (side, line) anchor.
    const wrapper = page.locator('diffs-container > [slot^="annotation-"]');
    await expect(wrapper).toHaveAttribute('slot', 'annotation-additions-4');
  });

  test('resolves and unresolves a comment', async ({ page }) => {
    await openHarness(page, SEEDED);
    const comment = page.locator('.diff-comment');

    await comment.locator('.resolve-btn').click();
    await expect.poll(() => calls(page, 'resolveComment')).toEqual([['seeded-1', true]]);
    await expect(comment).toHaveClass(/resolved/);
    await expect(comment.locator('.resolve-btn')).toHaveText('Unresolve');

    await comment.locator('.resolve-btn').click();
    await expect.poll(() => calls(page, 'resolveComment')).toEqual([
      ['seeded-1', true],
      ['seeded-1', false],
    ]);
    await expect(comment).not.toHaveClass(/resolved/);
  });

  test('edits a comment through the inline form', async ({ page }) => {
    await openHarness(page, SEEDED);
    const comment = page.locator('.diff-comment');

    await comment.locator('.edit-btn').click();
    await expect.poll(() => calls(page, 'startEdit')).toEqual([['seeded-1']]);

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    await form.locator('textarea').fill('Edited comment body');
    await form.locator('.save-btn').click();

    await expect.poll(() => calls(page, 'editComment')).toEqual([['seeded-1', 'Edited comment body']]);
    await expect(page.getByTestId('diff-comment-thread')).toContainText('Edited comment body');
  });

  test('sends a comment to Claude Code', async ({ page }) => {
    await openHarness(page, SEEDED);
    await page.locator('.diff-comment .send-btn').click();

    const sent = (await calls(page, 'sendToClaude')) as string[][];
    expect(sent).toHaveLength(1);
    expect(sent[0][0]).toContain('@fileA.ts:L4');
    expect(sent[0][0]).toContain('Seeded comment on an added line');
  });

  test('deletes a comment, removing its thread', async ({ page }) => {
    await openHarness(page, SEEDED);
    await expect(page.getByTestId('diff-comment-thread')).toBeVisible();

    await page.locator('.diff-comment .delete-btn').click();
    await expect.poll(() => calls(page, 'deleteComment')).toEqual([['seeded-1']]);
    await expect(page.getByTestId('diff-comment-thread')).toHaveCount(0);
  });

  test('adds a comment via line selection -> popup -> draft', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // Clicking a line-number cell starts a single-line selection, which surfaces
    // our action popup.
    await page.locator('diffs-container [data-line-index][data-column-number]').nth(4).click();
    const popup = page.locator('.diff-selection-popup');
    await expect(popup).toBeVisible();

    await popup.locator('.diff-selection-popup-btn.comment').click();
    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    await form.locator('textarea').fill('A brand new comment');
    await form.locator('.save-btn').click();

    const added = (await calls(page, 'addComment')) as Array<[number, number, string]>;
    expect(added).toHaveLength(1);
    expect(added[0][2]).toBe('A brand new comment');
    // Single-line selection on the additions side: start === end, positive end.
    expect(added[0][0]).toBe(added[0][1]);
    expect(added[0][1]).toBeGreaterThan(0);
  });

  test('opens the action popup when clicking the code area of a line', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // Clicking anywhere on a line — here the code cell, not the number gutter —
    // surfaces the action popup on that single line.
    await page.locator('diffs-container [data-line]').nth(4).click();
    const popup = page.locator('.diff-selection-popup');
    await expect(popup).toBeVisible();

    await popup.locator('.diff-selection-popup-btn.comment').click();
    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    await form.locator('textarea').fill('Comment from clicking the code');
    await form.locator('.save-btn').click();

    const added = (await calls(page, 'addComment')) as Array<[number, number, string]>;
    expect(added).toHaveLength(1);
    expect(added[0][2]).toBe('Comment from clicking the code');
  });

  test('the gutter "+" opens the draft directly, without the action popup', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // Hover a line to reveal the library's gutter "+" (enableGutterUtility),
    // then click it to open a draft directly on that line.
    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(4);
    await line.hover();

    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    // The "+" goes straight to the comment box — the action popup must NOT open.
    await expect(page.locator('.diff-selection-popup')).toHaveCount(0);

    await form.locator('textarea').fill('Comment from the gutter plus');
    await form.locator('.save-btn').click();

    const added = (await calls(page, 'addComment')) as Array<[number, number, string]>;
    expect(added).toHaveLength(1);
    expect(added[0][2]).toBe('Comment from the gutter plus');
    // The "+" anchors a single line on the additions side.
    expect(added[0][0]).toBe(added[0][1]);
    expect(added[0][1]).toBeGreaterThan(0);
  });

  test('switches back to a file already rendered earlier', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    const container = page.locator('diffs-container');

    // File A (function example) is shown first.
    await expect(container).toContainText('function example');

    // Switch to file B.
    await page.evaluate(() => window.__HARNESS__.switchFile('fileB.ts'));
    await expect(container).toContainText('class Calculator');
    await expect(container).not.toContainText('function example');

    // Switch back to file A — the previously rendered file must reappear.
    await page.evaluate(() => window.__HARNESS__.switchFile('fileA.ts'));
    await expect(container).toContainText('function example');
    await expect(container).not.toContainText('class Calculator');
  });

  test("re-renders when the same file's content changes in place", async ({ page }) => {
    await openHarness(page, UNSEEDED);
    const container = page.locator('diffs-container');
    // Full file so the appended marker is rendered regardless of hunk collapsing.
    await page.evaluate(() => window.__HARNESS__.setExpandUnchanged(true));
    await expect(container).toContainText('function example');
    await expect(container).not.toContainText('refresh-1');

    // Same file path, new content (e.g. the file changed on disk while viewing).
    await page.evaluate(() => window.__HARNESS__.refreshContent());
    await expect(container).toContainText('refresh-1');
  });

  test('toggles between unified and split layout', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // Unified is the default: the code column carries data-unified.
    await expect(page.locator('diffs-container [data-code][data-unified]')).toHaveCount(1);

    await page.evaluate(() => window.__HARNESS__.setDiffStyle('split'));
    await expect(page.locator('diffs-container [data-code][data-unified]')).toHaveCount(0);
    await expect(page.locator('diffs-container [data-code]')).not.toHaveCount(0);
  });
});
