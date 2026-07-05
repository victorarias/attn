import { test, expect, type Page, type Locator } from '@playwright/test';

/**
 * Known-RED reproductions for two real DiffView (`@pierre/diffs` wrapper) bugs.
 * These are NOT fixes — they pin the observed mechanism so a follow-up PR can
 * fix `src/components/DiffView.tsx` with a concrete repro to check against.
 *
 * Bug 1: clicking to add a comment scrolls the diff back to the top, losing
 * the reader's place in a long diff.
 * Bug 2: after adding one comment, the gutter hover "+" stops following the
 * mouse and stays pinned to the line where the last comment was added.
 *
 * Harness conventions mirror e2e/diff-view.spec.ts (goto, wait for
 * `__HARNESS__.ready`, wait for real rendered `[data-line]` content before
 * interacting — Shiki highlighting is async).
 */

const UNSEEDED = '/test-harness/?component=DiffView&seed=0';

async function openHarness(page: Page, url: string) {
  await page.goto(url);
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await page.waitForSelector('diffs-container');
  await page.locator('diffs-container [data-line-number-content]').first().waitFor();
}

async function openLargeDiff(page: Page) {
  await openHarness(page, UNSEEDED);
  await page.evaluate(() => {
    window.__HARNESS__.setUseLargeDiff(true);
    // Expand unchanged lines too: with hunks collapsed the large diff barely
    // scrolls at all (~30px total), which isn't a meaningful "scrolled well
    // below the fold" position. Full-file expansion gives real scroll range.
    window.__HARNESS__.setExpandUnchanged(true);
  });
  // The large diff has 50+ lines; wait for a generous number of rendered rows
  // so we know the tall content (not just the first hunk) is up.
  await expect
    .poll(async () => page.locator('diffs-container [data-line]').count())
    .toBeGreaterThan(20);
}

/**
 * Candidate scroll containers, in the order the library's source suggests:
 * `.diff-view-scroller` is the `Virtualizer` root DiffView renders into
 * (`overflow: auto`); `diffs-container`'s shadow root may also manage its own
 * inner scroll region for `CodeView`'s paged virtualization. We log every
 * candidate's scrollTop so a reviewer can see which one is real, rather than
 * assuming the source comment is the full story.
 */
async function scrollTopReport(page: Page): Promise<Record<string, number | null>> {
  return page.evaluate(() => {
    const report: Record<string, number | null> = {};
    const scroller = document.querySelector('.diff-view-scroller');
    report['.diff-view-scroller'] = scroller ? scroller.scrollTop : null;

    const container = document.querySelector('diffs-container');
    report['diffs-container (light DOM el)'] = container ? (container as HTMLElement).scrollTop : null;

    const shadow = container?.shadowRoot;
    if (shadow) {
      const shadowScrollers = shadow.querySelectorAll('*');
      let maxScrollTop = 0;
      let maxSelector = '';
      shadowScrollers.forEach((el) => {
        const st = (el as HTMLElement).scrollTop;
        if (st > maxScrollTop) {
          maxScrollTop = st;
          maxSelector = el.tagName + (el.className ? `.${String(el.className).replace(/\s+/g, '.')}` : '');
        }
      });
      report['diffs-container shadowRoot (max scrollTop element)'] = maxScrollTop;
      report['diffs-container shadowRoot (max scrollTop selector)'] = maxSelector as unknown as number;
    }
    return report;
  });
}

/** Reads scrollTop off the element we've determined is the real scroll container. */
async function realScrollTop(page: Page): Promise<number> {
  return page.evaluate(() => {
    const scroller = document.querySelector('.diff-view-scroller') as HTMLElement | null;
    return scroller?.scrollTop ?? -1;
  });
}

async function scrollDown(page: Page, amount: number) {
  await page.evaluate((amt) => {
    const scroller = document.querySelector('.diff-view-scroller') as HTMLElement | null;
    scroller?.scrollBy(0, amt);
  }, amount);
  // Let the virtualizer's rAF-driven render loop settle before reading scrollTop.
  await page.waitForTimeout(150);
}

test.describe('DiffView scroll preservation', () => {
  /**
   * BUG REPRO (known RED): opening a comment draft via the gutter "+" on a
   * line that is currently visible mid-scroll should NOT move the viewport.
   * Against current code this is expected to jump back to (near) the top.
   */
  test('gutter "+" on a scrolled-down visible line does not reset scroll position', async ({ page }) => {
    await openLargeDiff(page);

    await scrollDown(page, 800);
    const before = await realScrollTop(page);
    const report = await scrollTopReport(page);
    console.log('[scroll-jump/gutter] candidate scrollTops after scrolling down 800px:', report);
    console.log('[scroll-jump/gutter] before =', before);
    expect(before, 'sanity: scrolling down must actually move .diff-view-scroller').toBeGreaterThan(0);

    // Find a line row that is currently within the visible viewport of the
    // scroller (not just present in the DOM — virtualized rows outside the
    // viewport can still be mounted).
    const scrollerBox = await page.locator('.diff-view-scroller').boundingBox();
    expect(scrollerBox).not.toBeNull();
    const lines = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lines.count();
    let visibleLine: Locator | null = null;
    for (let i = 0; i < count; i++) {
      const candidate = lines.nth(i);
      const box = await candidate.boundingBox();
      if (!box || !scrollerBox) continue;
      const midY = box.y + box.height / 2;
      if (midY > scrollerBox.y + 20 && midY < scrollerBox.y + scrollerBox.height - 20) {
        visibleLine = candidate;
        break;
      }
    }
    expect(visibleLine, 'must find a line row visible within the scrolled viewport').not.toBeNull();

    await visibleLine!.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    const afterOpen = await realScrollTop(page);
    console.log('[scroll-jump/gutter] after opening draft, scrollTop =', afterOpen, '(before was', before, ')');

    // Also carry the trigger through to SAVE, not just open — saving commits
    // the comment (onAddComment -> setComments in the harness), which is a
    // second, distinct re-render from opening the draft. Log both so we learn
    // whether the jump (if any) happens on open or specifically on save.
    await form.locator('textarea').click();
    await page.keyboard.type('Comment while scrolled', { delay: 10 });
    await form.locator('.save-btn').click();
    await expect(form).toHaveCount(0);
    const afterSave = await realScrollTop(page);
    console.log('[scroll-jump/gutter] after SAVING the comment, scrollTop =', afterSave, '(before was', before, ')');

    // EXPECTED TO FAIL against current code: opening the draft (or saving the
    // comment) jumps the scroller back toward the top instead of preserving
    // position.
    expect(Math.abs(afterOpen - before), 'scroll preserved on draft OPEN').toBeLessThanOrEqual(20);
    expect(Math.abs(afterSave - before), 'scroll preserved on comment SAVE').toBeLessThanOrEqual(20);
  });

  /**
   * BUG REPRO (known RED), second trigger: same scenario but via the plain
   * line-click -> action popup -> "Add comment" path, to learn whether the
   * jump is specific to the gutter-utility trigger or common to both.
   */
  test('line-click popup "Add comment" on a scrolled-down visible line does not reset scroll position', async ({ page }) => {
    await openLargeDiff(page);

    await scrollDown(page, 800);
    const before = await realScrollTop(page);
    console.log('[scroll-jump/popup] before =', before);
    expect(before, 'sanity: scrolling down must actually move .diff-view-scroller').toBeGreaterThan(0);

    const scrollerBox = await page.locator('.diff-view-scroller').boundingBox();
    const lines = page.locator('diffs-container [data-line]');
    const count = await lines.count();
    let visibleLine: Locator | null = null;
    for (let i = 0; i < count; i++) {
      const candidate = lines.nth(i);
      const box = await candidate.boundingBox();
      if (!box || !scrollerBox) continue;
      const midY = box.y + box.height / 2;
      if (midY > scrollerBox.y + 20 && midY < scrollerBox.y + scrollerBox.height - 20) {
        visibleLine = candidate;
        break;
      }
    }
    expect(visibleLine, 'must find a line row visible within the scrolled viewport').not.toBeNull();

    await visibleLine!.click();
    const popup = page.locator('.diff-selection-popup');
    await expect(popup).toBeVisible();
    await popup.locator('.diff-selection-popup-btn.comment').click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    const after = await realScrollTop(page);
    console.log('[scroll-jump/popup] after opening draft via popup, scrollTop =', after, '(before was', before, ')');

    expect(Math.abs(after - before)).toBeLessThanOrEqual(20);
  });

  /**
   * Isolates whether the jump requires a background CONTENT change while the
   * draft is open (the freeze/remount path via `refreshContent`), as opposed
   * to happening purely on draft-open. If this one passes while the two above
   * fail, the jump is caused by opening the draft itself, not by remounting
   * on content change.
   */
  test('refreshing content while a draft is open on a scrolled-down line does not reset scroll position', async ({ page }) => {
    await openLargeDiff(page);

    await scrollDown(page, 800);
    const scrolledBefore = await realScrollTop(page);
    console.log('[scroll-jump/refresh] scrollTop after initial scroll =', scrolledBefore);
    expect(scrolledBefore).toBeGreaterThan(0);

    const scrollerBox = await page.locator('.diff-view-scroller').boundingBox();
    const lines = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lines.count();
    let visibleLine: Locator | null = null;
    for (let i = 0; i < count; i++) {
      const candidate = lines.nth(i);
      const box = await candidate.boundingBox();
      if (!box || !scrollerBox) continue;
      const midY = box.y + box.height / 2;
      if (midY > scrollerBox.y + 20 && midY < scrollerBox.y + scrollerBox.height - 20) {
        visibleLine = candidate;
        break;
      }
    }
    expect(visibleLine).not.toBeNull();

    await visibleLine!.hover();
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    const before = await realScrollTop(page);
    console.log('[scroll-jump/refresh] scrollTop with draft open (pre-refresh) =', before);

    await page.evaluate(() => window.__HARNESS__.refreshContent());
    await page.waitForTimeout(200);

    const after = await realScrollTop(page);
    console.log('[scroll-jump/refresh] scrollTop after refreshContent() with draft open =', after, '(before was', before, ')');

    expect(Math.abs(after - before)).toBeLessThanOrEqual(20);
  });
});

test.describe('DiffView gutter hover follows pointer', () => {
  /**
   * Returns the line number of the row currently hosting the gutter utility
   * "+" (the `[data-gutter-utility-slot]` container the library appends into
   * a line's number cell), or null if the "+" isn't shown anywhere.
   *
   * `[data-gutter-utility-slot]` lives inside `diffs-container`'s OPEN shadow
   * root (appended by InteractionManager.showUtilityOnLine into the hovered
   * line's number element). Playwright's locator engine pierces open shadow
   * roots to FIND the node, but a plain `page.evaluate(() =>
   * document.querySelector(...))` does not — that returned null. Locating via
   * `page.locator` first, then running `.evaluate()` on the resolved handle
   * (which operates on the real DOM node and can freely walk `parentElement`
   * regardless of shadow boundaries), is the fix.
   */
  async function gutterUtilityLineNumber(page: Page): Promise<number | null> {
    const slot = page.locator('diffs-container [data-gutter-utility-slot]');
    if ((await slot.count()) === 0) return null;
    return slot.first().evaluate((el) => {
      const numberCell = el.parentElement;
      const raw =
        numberCell?.getAttribute('data-column-number') ??
        numberCell?.closest('[data-column-number]')?.getAttribute('data-column-number');
      return raw != null ? Number(raw) : null;
    });
  }

  /**
   * BUG REPRO (known RED): after adding a comment via the gutter "+" on line
   * A, hovering a different visible line B should move the "+" to B. Against
   * current code the "+" stays pinned to A (the library's InteractionManager
   * keeps preferring a committed `selectedRange` over the live hovered line —
   * see `placeUtility()` / `placeUtilityFromSelection()` in
   * @pierre/diffs/dist/managers/InteractionManager.js).
   */
  test('hover "+" moves to a newly hovered line after a comment was added on a previous line', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const lineLocators = page.locator('diffs-container [data-line-index][data-column-number]');
    const count = await lineLocators.count();
    expect(count).toBeGreaterThan(6);

    const lineA = lineLocators.nth(3);
    const lineB = lineLocators.nth(6);

    const lineNumberOf = async (loc: Locator) =>
      Number(await loc.getAttribute('data-column-number'));
    const numberA = await lineNumberOf(lineA);
    const numberB = await lineNumberOf(lineB);
    console.log('[hover-death] line A number =', numberA, ' line B number =', numberB);

    // Hover A: the "+" should land on A.
    await lineA.hover();
    await expect
      .poll(() => gutterUtilityLineNumber(page))
      .toBe(numberA);

    // Add a comment on A via the gutter "+".
    const plus = page.locator('diffs-container [data-utility-button]');
    await plus.waitFor({ state: 'visible' });
    await plus.click();

    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();
    const textarea = form.locator('textarea');
    await textarea.click();
    await page.keyboard.type('Comment on line A', { delay: 10 });
    await form.locator('.save-btn').click();

    await expect.poll(() => page.evaluate(() => window.__HARNESS__.getCalls('addComment'))).toHaveLength(1);
    await expect(form).toHaveCount(0);

    // Now hover a different visible line B.
    await lineB.hover();
    // Give the InteractionManager's pointer handlers a beat to react.
    await page.waitForTimeout(150);

    const pinnedLine = await gutterUtilityLineNumber(page);
    console.log('[hover-death] after hovering line B, "+" is attached to line number =', pinnedLine, '(expected', numberB, ')');

    // EXPECTED TO FAIL against current code: the "+" stays pinned to line A
    // (or wherever the committed selection landed) instead of following the
    // pointer to line B.
    expect(pinnedLine).toBe(numberB);
  });
});
