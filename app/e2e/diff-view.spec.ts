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

  test('escapes raw HTML in rendered comment markdown', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => window.__HARNESS__.seedHtmlComment());

    const thread = page.getByTestId('diff-comment-thread');
    await expect(thread).toContainText('<img src=x onerror=alert(1)>');
    await expect(thread.locator('img')).toHaveCount(0);
    await expect(thread.locator('strong')).toContainText('safe markdown');
  });

  test('preserves soft line breaks in rendered comment markdown', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => window.__HARNESS__.seedMultilineComment());

    const paragraph = page.locator('.diff-comment-content p');
    await expect(paragraph).toContainText('First line');
    await expect(paragraph).toContainText('Second line');
    await expect.poll(() => paragraph.evaluate((el) => getComputedStyle(el).whiteSpace)).toBe('pre-wrap');
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

  test('keeps draft text when saving a new comment fails', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(4);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await textarea.fill('Retry this after failure');
    await page.evaluate(() => window.__HARNESS__.failNextAddComment());
    await page.locator('.diff-comment-form .save-btn').click();

    await expect.poll(() => calls(page, 'addComment')).toHaveLength(1);
    await expect(textarea).toBeVisible();
    await expect(textarea).toHaveValue('Retry this after failure');
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

  test('restores each file scroll position after switching away and back', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => {
      window.__HARNESS__.setUseLargeDiff(true);
      window.__HARNESS__.setExpandUnchanged(true);
    });
    await expect(page.locator('diffs-container')).toContainText('section3 20');

    const scroller = page.locator('.diff-view-scroller');
    await scroller.evaluate((element) => {
      element.scrollTop = Math.min(480, element.scrollHeight - element.clientHeight);
      element.dispatchEvent(new Event('scroll'));
    });
    const fileAScrollTop = await scroller.evaluate((element) => element.scrollTop);
    expect(fileAScrollTop).toBeGreaterThan(100);

    await page.evaluate(() => window.__HARNESS__.switchFile('fileB.ts'));
    await expect(page.locator('diffs-container')).toContainText('class Calculator');
    await expect.poll(() => scroller.evaluate((element) => element.scrollTop)).toBe(0);

    await page.evaluate(() => window.__HARNESS__.switchFile('fileA.ts'));
    await expect(page.locator('diffs-container')).toContainText('section3 20');
    await expect.poll(
      () => scroller.evaluate((element) => element.scrollTop),
    ).toBeCloseTo(fileAScrollTop, 0);
  });

  test('keeps an in-progress comment when switching away from a file and back', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(4);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Draft before switching files');

    await page.evaluate(() => window.__HARNESS__.switchFile('fileB.ts'));
    await expect(page.locator('diffs-container')).toContainText('class Calculator');
    await expect(page.locator('.diff-comment-form textarea')).toHaveCount(0);

    await page.evaluate(() => window.__HARNESS__.switchFile('fileA.ts'));
    await expect(page.locator('diffs-container')).toContainText('function example');
    await expect(page.locator('.diff-comment-form textarea')).toHaveValue('Draft before switching files');
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

  test('keeps an in-progress comment (text + focus) and existing comments when a background change re-renders the diff', async ({ page }) => {
    await openHarness(page, SEEDED);
    // The seeded comment is already on screen.
    await expect(page.getByTestId('diff-comment-thread')).toContainText('Seeded comment on an added line');

    // Start typing a brand-new comment via the gutter "+".
    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(6);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Half-written thought');
    await expect(textarea).toBeFocused();

    // A background change lands: a comment arrives on another line. This
    // re-renders the diff (lineAnnotations change) without touching the file.
    await page.evaluate(() => window.__HARNESS__.addBackgroundComment());
    await expect(page.locator('.diff-comment[data-comment-id^="bg-"]')).toBeVisible();

    // The half-written comment, its caret/focus, and the existing comment all survive.
    await expect(textarea).toHaveValue('Half-written thought');
    await expect(textarea).toBeFocused();
    await expect(page.getByText('Seeded comment on an added line')).toBeVisible();
  });

  test('keeps the diff viewport steady while typing and receiving comment updates', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => {
      window.__HARNESS__.setUseLargeDiff(true);
      window.__HARNESS__.setExpandUnchanged(true);
    });
    await expect(page.locator('diffs-container')).toContainText('section3 20');

    const scroller = page.locator('.diff-view-scroller');
    await scroller.evaluate((element) => {
      element.scrollTop = Math.min(480, element.scrollHeight - element.clientHeight);
      element.dispatchEvent(new Event('scroll'));
    });
    await expect.poll(() => scroller.evaluate((element) => element.scrollTop)).toBeGreaterThan(100);

    const lineNumbers = page.locator('diffs-container [data-line-index][data-column-number]');
    const visibleLineIndex = await lineNumbers.evaluateAll((elements) => {
      const viewport = document.querySelector('.diff-view-scroller')?.getBoundingClientRect();
      if (!viewport) return -1;
      return elements.findIndex((element) => {
        const rect = element.getBoundingClientRect();
        return rect.top > viewport.top + 60 && rect.bottom < viewport.bottom - 140;
      });
    });
    expect(visibleLineIndex).toBeGreaterThanOrEqual(0);

    await lineNumbers.nth(visibleLineIndex).hover();
    await page.locator('diffs-container [data-utility-button]:visible').click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    const anchoredScrollTop = await scroller.evaluate((element) => element.scrollTop);

    await textarea.pressSequentially('The viewport should stay anchored.', { delay: 10 });
    await expect.poll(
      () => scroller.evaluate((element) => element.scrollTop),
    ).toBeCloseTo(anchoredScrollTop, 0);

    await page.evaluate(() => window.__HARNESS__.addBackgroundComment());
    await expect(page.locator('.diff-comment[data-comment-id^="bg-"]')).toBeVisible();
    await expect.poll(
      () => scroller.evaluate((element) => element.scrollTop),
    ).toBeCloseTo(anchoredScrollTop, 0);
    await expect(textarea).toHaveValue('The viewport should stay anchored.');
  });

  test('keeps an in-progress edit (text + focus) when a background change re-renders the diff', async ({ page }) => {
    await openHarness(page, SEEDED);
    await page.locator('.diff-comment .edit-btn').click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Edited but not yet saved');
    await expect(textarea).toBeFocused();

    await page.evaluate(() => window.__HARNESS__.addBackgroundComment());
    await expect(page.locator('.diff-comment[data-comment-id^="bg-"]')).toBeVisible();

    await expect(textarea).toHaveValue('Edited but not yet saved');
    await expect(textarea).toBeFocused();
  });

  test('keeps an in-progress comment (text + focus) when the current file changes underneath', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // Start typing a brand-new comment.
    const line = page.locator('diffs-container [data-line-index][data-column-number]').nth(6);
    await line.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Mid-sentence when the file changed');
    await expect(textarea).toBeFocused();

    // The file under review changes in the background (the agent edits it).
    await page.evaluate(() => window.__HARNESS__.refreshContent());

    // The in-progress comment and its focus must survive the file change.
    await expect(textarea).toHaveValue('Mid-sentence when the file changed');
    await expect(textarea).toBeFocused();
  });

  test('keeps an in-progress edit (text + focus) when the current file changes underneath', async ({ page }) => {
    await openHarness(page, SEEDED);
    await page.locator('.diff-comment .edit-btn').click();

    const textarea = page.locator('.diff-comment-form textarea');
    await expect(textarea).toBeVisible();
    await textarea.fill('Editing while the file changes');
    await expect(textarea).toBeFocused();

    await page.evaluate(() => window.__HARNESS__.refreshContent());

    await expect(textarea).toHaveValue('Editing while the file changes');
    await expect(textarea).toBeFocused();
  });

  test('collapses stale comments (anchor line gone) at the top, expandable on click', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    // A comment whose anchor line no longer exists in the file.
    await page.evaluate(() => window.__HARNESS__.seedStaleComment());

    const toggle = page.getByTestId('diff-stale-comments-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).toContainText('1 comment not visible');

    // Collapsed by default, and never slotted into the diff as a normal annotation.
    await expect(page.getByText('Stale: this code is gone')).toHaveCount(0);
    await expect(page.locator('diffs-container > [slot^="annotation-"]')).toHaveCount(0);

    // Click to expand → readable and actionable.
    await toggle.click();
    await expect(page.getByText('Stale: this code is gone')).toBeVisible();
    await page.locator('.diff-stale-comments-body .delete-btn').click();
    await expect.poll(() => calls(page, 'deleteComment')).toEqual([['stale-1']]);
  });

  test('moves a comment into the stale banner when the code shrinks past its line', async ({ page }) => {
    await openHarness(page, SEEDED);
    // Initially anchored inline; no stale banner.
    await expect(page.getByTestId('diff-comment-thread')).toContainText('Seeded comment on an added line');
    await expect(page.getByTestId('diff-stale-comments-toggle')).toHaveCount(0);

    // The file shrinks below the comment's line — the comment is not lost.
    await page.evaluate(() => window.__HARNESS__.shrinkContent());
    const toggle = page.getByTestId('diff-stale-comments-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).toContainText('1 comment not visible');

    await toggle.click();
    await expect(page.getByText('Seeded comment on an added line')).toBeVisible();
  });

  test('keeps comments on collapsed unchanged lines visible in the top banner', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    const container = page.locator('diffs-container');

    await page.evaluate(() => {
      window.__HARNESS__.setUseLargeDiff(true);
      window.__HARNESS__.setExpandUnchanged(true);
      window.__HARNESS__.seedCollapsedContextComment();
    });
    await expect(container).toContainText('section2 15');
    await expect(page.getByText('Collapsed context comment')).toBeVisible();
    await expect(page.getByTestId('diff-stale-comments-toggle')).toHaveCount(0);

    await page.evaluate(() => window.__HARNESS__.setExpandUnchanged(false));
    const toggle = page.getByTestId('diff-stale-comments-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).toContainText('1 comment not visible');
    await expect(page.getByText('Collapsed context comment')).toHaveCount(0);

    await toggle.click();
    await expect(page.getByText('Collapsed context comment')).toBeVisible();
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
