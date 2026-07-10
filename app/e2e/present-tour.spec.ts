import { test, expect, type Page, type Locator } from '@playwright/test';

/**
 * PresentTour (the multi-file `@pierre/diffs` CodeView tour reader) component
 * harness coverage. Mirrors e2e/diff-view-comment-interactions.spec.ts's
 * conventions (goto, wait for `__HARNESS__.ready`, wait for real rendered
 * content before interacting — Shiki highlighting is async, and CodeView
 * mounts each file's `<diffs-container>` lazily as it scrolls into the
 * virtualizer's window).
 *
 * The harness (test-harness/harnesses/PresentTourHarness.tsx) seeds a fixed
 * 3-file manifest (src/alpha.ts, src/beta.ts, src/gamma.ts), each with several
 * separate hunks so the tour has real scroll range even with
 * `expandUnchanged: false` collapsing unchanged context.
 */

const UNSEEDED = '/test-harness/?component=PresentTour&seed=0';

async function openHarness(page: Page, url: string) {
  await page.goto(url);
  await page.waitForFunction(() => window.__HARNESS__?.ready === true);
  await page.waitForSelector('diffs-container');
  await page.locator('diffs-container [data-line-number-content]').first().waitFor();
}

/** All currently-mounted per-file `<diffs-container>` roots, in DOM order. */
function fileContainers(page: Page): Locator {
  return page.locator('diffs-container');
}

/** The file path shown in a mounted `<diffs-container>`'s header title. */
async function titleOf(container: Locator): Promise<string | null> {
  const title = container.locator('[data-title]');
  if ((await title.count()) === 0) return null;
  return title.first().textContent();
}

function calls(page: Page, name: string) {
  return page.evaluate((n) => window.__HARNESS__.getCalls(n), name);
}

/**
 * Scrolls the tour via a REAL wheel event, not a programmatic scroll.
 * PresentTour pins `.present-tour-scroller` to the top until real user input
 * arrives (ported from DiffView's cold-window defense) — a JS-driven scroll
 * here would be silently snapped back to 0 before any of these tests got to
 * assert anything, since it doesn't count as "the user took over".
 */
async function scrollDown(page: Page, amount: number) {
  const box = await page.locator('.present-tour-scroller').boundingBox();
  if (box) {
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  }
  await page.mouse.wheel(0, amount);
  // Let the virtualizer's rAF-driven render loop settle before reading scrollTop.
  await page.waitForTimeout(200);
}

async function scrollerScrollTop(page: Page): Promise<number> {
  return page.evaluate(() => document.querySelector('.present-tour-scroller')?.scrollTop ?? -1);
}

/**
 * `scrollToFile` requests a `behavior: 'smooth'` scroll, so the virtualizer
 * keeps mounting/unmounting `<diffs-container>`s while the animation runs.
 * Interacting with an element found mid-animation (e.g. hovering a line to
 * reveal the gutter "+") races the next virtualization pass and detaches —
 * wait for scrollTop to stop moving across two checks before locating
 * anything by index/title.
 */
async function waitForScrollSettle(page: Page) {
  let previous = await scrollerScrollTop(page);
  for (let i = 0; i < 20; i++) {
    await page.waitForTimeout(100);
    const current = await scrollerScrollTop(page);
    if (current === previous) return;
    previous = current;
  }
}

/** Locates the currently-mounted `<diffs-container>` for a given file path, if any. */
async function findContainerByTitle(page: Page, path: string): Promise<Locator | null> {
  const count = await fileContainers(page).count();
  for (let i = 0; i < count; i++) {
    const candidate = fileContainers(page).nth(i);
    if ((await titleOf(candidate)) === path) return candidate;
  }
  return null;
}

/**
 * Opens a draft on the given line via the gutter "+" (hover then click),
 * scoped to one file's container.
 *
 * The diff rows re-render while hover state, draft mounts, and the
 * virtualizer churn, so the "+" can detach between resolving and clicking
 * (observed as `element was detached from the DOM, retrying` until the test
 * times out). Retry the whole hover→click gesture until a new draft form
 * actually appears, skipping the click if a prior attempt already landed.
 */
async function openDraftViaGutter(container: Locator, line: Locator) {
  const page = container.page();
  const forms = page.getByTestId('diff-comment-form');
  const before = await forms.count();
  await expect(async () => {
    if ((await forms.count()) > before) return; // a prior attempt's click landed
    await line.hover();
    await container.locator('[data-utility-button]').click({ timeout: 2000 });
    await expect(forms).toHaveCount(before + 1, { timeout: 1000 });
  }).toPass({ timeout: 15_000 });
}

/**
 * Collapses the summary card up front so its scroll-triggered fold (an
 * ~0.18s height animation that shifts the whole scroller and churns the
 * virtualizer) can't fire mid-gesture in specs whose subject is draft/form
 * behavior, not the fold. The fold itself is covered by its own spec.
 */
async function collapseSummaryFirst(page: Page) {
  const summary = page.locator('.present-tour-summary');
  if ((await summary.count()) === 0) return;
  await page.getByTestId('present-tour-summary-toggle').click();
  await expect(summary).toHaveClass(/collapsed/);
  // The class flips immediately, but the body's height transition
  // (max-height 0.18s) still runs after — settle it too so the scroller's
  // layout has fully stopped moving before any gesture below.
  await expect(page.getByTestId('present-tour-summary-body')).toHaveCSS('max-height', '0px');
}

test.describe('PresentTour rendering', () => {
  test('renders every manifest file as a card inside one scroller, in reading order', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // The tour is a single scroll surface — exactly one `.present-tour-scroller`,
    // hosting every file's `<diffs-container>` as a virtualized child.
    await expect(page.locator('.present-tour-scroller')).toHaveCount(1);

    // alpha.ts is first in the manifest and must be the first (and initially
    // only, before any scrolling) file mounted.
    const containers = fileContainers(page);
    await expect(containers.first()).toBeVisible();
    expect(await titleOf(containers.first())).toBe('src/alpha.ts');

    // Scrolling to the end reveals gamma.ts as the last file, confirming
    // reading order end-to-end (not just the first item).
    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));
    await expect.poll(async () => {
      const last = containers.last();
      return titleOf(last);
    }).toBe('src/gamma.ts');
  });

  test('renders the per-file note as a callout under that file only', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    await expect(page.locator('.present-tour-file-note')).toContainText('Beta needs a second look.');
  });
});

test.describe('PresentTour reviewed toggle', () => {
  // Real-browser coverage for the slice-1 "controlled items only re-render on
  // a version bump" trap (see the module doc), applied to the reviewed
  // indicator: it's rendered via `renderHeaderPrefix`, a callback-based slot
  // rather than a field baked into `items`, so this proves the indicator
  // updates live without relying on jsdom (which can't mount the real
  // `@pierre/diffs` CodeView at all — see PresentRoot.test.tsx's mock note).
  test('clicking the header Reviewed toggle flips state and calls back to the parent', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const alpha = await findContainerByTitle(page, 'src/alpha.ts');
    expect(alpha).not.toBeNull();
    const toggle = alpha!.locator('.present-tour-reviewed-toggle');
    await expect(toggle).toBeVisible();
    await expect(toggle).not.toHaveClass(/is-reviewed/);

    await toggle.click();

    await expect(toggle).toHaveClass(/is-reviewed/);
    expect(await page.evaluate(() => window.__HARNESS__.getReviewedPaths())).toEqual(['src/alpha.ts']);

    // Click again to un-mark, confirming the version-bump path handles both
    // directions, not just the initial flip.
    await toggle.click();
    await expect(toggle).not.toHaveClass(/is-reviewed/);
    expect(await page.evaluate(() => window.__HARNESS__.getReviewedPaths())).toEqual([]);
  });

  test('reviewed state is independent per file', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    const alpha = await findContainerByTitle(page, 'src/alpha.ts');
    await alpha!.locator('.present-tour-reviewed-toggle').click();

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    const beta = await findContainerByTitle(page, 'src/beta.ts');
    await expect(beta!.locator('.present-tour-reviewed-toggle')).not.toHaveClass(/is-reviewed/);

    expect(await page.evaluate(() => window.__HARNESS__.getReviewedPaths())).toEqual(['src/alpha.ts']);
  });
});

test.describe('PresentTour scroll pin', () => {
  test('a programmatic scroll before any user input snaps back to the top', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await page.evaluate(() => {
      const scroller = document.querySelector('.present-tour-scroller') as HTMLElement | null;
      if (scroller) scroller.scrollTop = 500;
    });

    await expect.poll(() => scrollerScrollTop(page)).toBe(0);
  });

  test('a real wheel scroll arms takeover, and later programmatic scroll changes are no longer pinned', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await scrollDown(page, 300);
    const afterWheel = await scrollerScrollTop(page);
    expect(afterWheel, 'the real wheel scroll itself must not be pinned').toBeGreaterThan(0);

    await page.evaluate(() => {
      const scroller = document.querySelector('.present-tour-scroller') as HTMLElement | null;
      if (scroller) scroller.scrollTop = 500;
    });
    await page.waitForTimeout(150);

    const after = await scrollerScrollTop(page);
    expect(after, 'takeover must stick for later scroll changes too').toBe(500);
  });
});

test.describe('PresentTour rail-driven scroll', () => {
  test('a scroll-to-path request (rail click / j-k) scrolls the tour to that file', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    // Sanity: alpha is showing first, beta is not yet mounted.
    expect(await titleOf(fileContainers(page).first())).toBe('src/alpha.ts');

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));

    // Scrolling all the way to beta (a later file) must actually move the
    // scroller off zero and mount beta's container.
    await expect.poll(() => scrollerScrollTop(page)).toBeGreaterThan(0);
    await expect.poll(async () => {
      const count = await fileContainers(page).count();
      for (let i = 0; i < count; i++) {
        if ((await titleOf(fileContainers(page).nth(i))) === 'src/beta.ts') return true;
      }
      return false;
    }).toBe(true);
  });

  test('re-requesting the same path still scrolls (nonce forces a re-scroll)', async ({ page }) => {
    await openHarness(page, UNSEEDED);

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));
    await expect.poll(() => scrollerScrollTop(page)).toBeGreaterThan(0);

    // Scroll back to the top via a real wheel (can't scroll programmatically —
    // the pin is already disarmed by the prior imperative scroll's own
    // internal mechanics, but a wheel is the realistic way a user would do
    // this) then re-request gamma; the tour must scroll back down again.
    await scrollDown(page, -100000);
    await page.waitForTimeout(200);
    const afterUp = await scrollerScrollTop(page);

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));
    await expect.poll(() => scrollerScrollTop(page)).toBeGreaterThan(afterUp);
  });
});

test.describe('PresentTour multiple simultaneous drafts across files', () => {
  test('opening a draft on one file and one on another keeps both open independently', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await collapseSummaryFirst(page);

    const alphaContainer = fileContainers(page).first();
    const alphaLine = alphaContainer.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(alphaContainer, alphaLine);

    const forms = page.getByTestId('diff-comment-form');
    await expect(forms).toHaveCount(1);
    await forms.nth(0).locator('textarea').fill('Comment on alpha');

    // Scroll to beta and open a second, independent draft there.
    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    // The scroll is smooth-animated (see waitForScrollSettle's doc comment);
    // let the virtualizer stop churning before interacting by index. Opening
    // alpha's draft above also grows alpha's rendered height (the comment
    // form), which can shift beta in/out of the virtualizer's mounted window
    // right as the animation settles — re-poll after settling rather than a
    // single lookup.
    await waitForScrollSettle(page);
    let betaContainer: Locator | null = null;
    await expect.poll(async () => {
      betaContainer = await findContainerByTitle(page, 'src/beta.ts');
      return betaContainer !== null;
    }).toBe(true);
    expect(betaContainer).not.toBeNull();
    const betaLine = betaContainer!.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(betaContainer!, betaLine);

    await expect(forms).toHaveCount(2);
    // Both boxes coexist with independently-typed text.
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Comment on alpha');
    await forms.nth(1).locator('textarea').fill('Comment on beta');
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Comment on alpha');
    await expect(forms.nth(1).locator('textarea')).toHaveValue('Comment on beta');

    // Saving each independently records the right filepath.
    await forms.nth(1).locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(1);
    await forms.nth(0).locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(2);

    const added = (await calls(page, 'addComment')) as Array<[string, number, number, string]>;
    const byContent = Object.fromEntries(added.map(([filepath, start, , content]) => [content, filepath]));
    expect(byContent['Comment on beta']).toBe('src/beta.ts');
    expect(byContent['Comment on alpha']).toBe('src/alpha.ts');
  });

  test('Escape closes the most-recently-opened draft first, across files', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await collapseSummaryFirst(page);

    const alphaContainer = fileContainers(page).first();
    const alphaLine = alphaContainer.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(alphaContainer, alphaLine);
    const forms = page.getByTestId('diff-comment-form');
    await forms.nth(0).locator('textarea').fill('Opened first (alpha)');

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/beta.ts'));
    // See the sibling test's comment: alpha's open draft grows its rendered
    // height, which can shift beta in/out of the virtualizer's mounted
    // window right as the smooth-scroll animation settles.
    await waitForScrollSettle(page);
    let betaContainer: Locator | null = null;
    await expect.poll(async () => {
      betaContainer = await findContainerByTitle(page, 'src/beta.ts');
      return betaContainer !== null;
    }).toBe(true);
    expect(betaContainer).not.toBeNull();
    const betaLine = betaContainer!.locator('[data-line-index][data-column-number]').nth(4);
    await openDraftViaGutter(betaContainer!, betaLine);
    await forms.nth(1).locator('textarea').fill('Opened second (beta)');
    await expect(forms).toHaveCount(2);

    await page.keyboard.press('Escape');
    await expect(forms).toHaveCount(1);
    await expect(forms.nth(0).locator('textarea')).toHaveValue('Opened first (alpha)');

    await page.keyboard.press('Escape');
    await expect(forms).toHaveCount(0);
    expect(await calls(page, 'addComment')).toHaveLength(0);
  });
});

test.describe('PresentTour draft and comment gutter interactions', () => {
  test('gutter "+" on a specific line opens a draft form anchored to that line', async ({ page }) => {
    await openHarness(page, UNSEEDED);
    await collapseSummaryFirst(page);

    const alphaContainer = fileContainers(page).first();
    const line = alphaContainer.locator('[data-line-index][data-column-number]').nth(3);
    const lineNumber = Number(await line.getAttribute('data-column-number'));

    await openDraftViaGutter(alphaContainer, line);
    const form = page.getByTestId('diff-comment-form');
    await expect(form).toBeVisible();

    await form.locator('textarea').fill('On the right line');
    await form.locator('.save-btn').click();
    await expect.poll(() => calls(page, 'addComment')).toHaveLength(1);

    const [[filepath, lineStart, lineEnd, content]] = (await calls(page, 'addComment')) as Array<
      [string, number, number, string]
    >;
    expect(filepath).toBe('src/alpha.ts');
    expect(content).toBe('On the right line');
    expect(Math.abs(lineStart)).toBe(lineNumber);
    expect(Math.abs(lineEnd)).toBe(lineNumber);
  });

  test('a seeded comment can be edited and deleted', async ({ page }) => {
    // seed=1 (default): harness seeds one comment on src/alpha.ts.
    await openHarness(page, '/test-harness/?component=PresentTour');
    await collapseSummaryFirst(page);

    const thread = page.getByTestId('diff-comment-thread');
    await expect(thread).toContainText('Seeded comment on alpha');

    await thread.locator('.edit-btn').click();
    const editForm = page.getByTestId('diff-comment-form');
    await expect(editForm).toBeVisible();
    await editForm.locator('textarea').fill('Edited comment content');
    await editForm.locator('.save-btn').click();
    await expect.poll(() => calls(page, 'editComment')).toHaveLength(1);
    await expect(thread).toContainText('Edited comment content');

    await thread.locator('.delete-btn').click();
    await expect.poll(() => calls(page, 'deleteComment')).toHaveLength(1);
    await expect(page.getByTestId('diff-comment-thread')).toHaveCount(0);
  });
});

test.describe('PresentTour summary fold', () => {
  // Browser-level regression for the listener-never-attached bug: the harness's
  // `deferred=1` + `settleDiffs()` mode reproduces the live app's loading ->
  // settled transition (diffs arrive async over the daemon WS), which is
  // exactly the case unit tests and the default harness mode (diffs
  // pre-loaded) never exercised.
  test('wheel-scrolling the diff folds the summary card; the toggle re-expands it', async ({ page }) => {
    await page.goto('/test-harness/?component=PresentTour&seed=0&deferred=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    const summary = page.locator('.present-tour-summary');
    await expect(summary).toBeVisible();
    await expect(summary).not.toHaveClass(/collapsed/);

    await page.evaluate(() => (window.__HARNESS__ as unknown as { settleDiffs: () => void }).settleDiffs());
    await page.waitForSelector('diffs-container');
    await page.locator('diffs-container [data-line-number-content]').first().waitFor();

    await expect(async () => {
      const box = await page.locator('.present-tour-scroller').boundingBox();
      if (!box) throw new Error('scroller not laid out yet');
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await page.mouse.wheel(0, 400);
      await expect(summary).toHaveClass(/collapsed/, { timeout: 1000 });
    }).toPass({ timeout: 15_000 });

    await page.getByTestId('present-tour-summary-toggle').click();
    await expect(summary).not.toHaveClass(/collapsed/);
  });
});

test.describe('PresentTour deferred-load scroll replay', () => {
  test('a scroll request issued while still loading is replayed once files settle', async ({ page }) => {
    // `&deferred=1` starts every file `{loading: true}` (no `<diffs-container>`
    // mounts at all yet) so this exercises the case the rail/j-k effect used
    // to drop: a scroll request that arrives before CodeView exists.
    await page.goto('/test-harness/?component=PresentTour&seed=0&deferred=1');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await expect(page.locator('.present-tour-loading')).toBeVisible();
    expect(await fileContainers(page).count()).toBe(0);

    await page.evaluate(() => window.__HARNESS__.scrollToFile('src/gamma.ts'));

    await page.evaluate(() => (window.__HARNESS__ as unknown as { settleDiffs: () => void }).settleDiffs());
    await page.waitForSelector('diffs-container');
    await page.locator('diffs-container [data-line-number-content]').first().waitFor();
    await waitForScrollSettle(page);

    let gammaContainer: Locator | null = null;
    await expect.poll(async () => {
      gammaContainer = await findContainerByTitle(page, 'src/gamma.ts');
      return gammaContainer !== null;
    }).toBe(true);
    expect(gammaContainer).not.toBeNull();

    // A scroller pinned to the top would leave gamma (the last of 3 files)
    // out of the virtualizer's mounted window; scrolled-to means it's visible.
    await expect(gammaContainer!).toBeInViewport();
  });
});
