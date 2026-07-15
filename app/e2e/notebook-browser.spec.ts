import { test, expect } from '@playwright/test';

// Drives an external file change through the harness, the way the daemon's fs_changed
// broadcast would (see NotebookBrowserHarness).
declare global {
  interface Window {
    __NB_HARNESS__?: {
      fsChanged: (path?: string, content?: string, hash?: string) => void;
      getContent: (path: string) => string;
    };
  }
}

// The full notebook modal (lazy filesystem tree sidebar + single live-editor document
// pane), rendered by the component harness with mocked daemon functions in a real
// browser. Verifies the fs-backed layout end-to-end and that the always-live editor
// autosaves — no view/edit toggle. Screenshots capture the assembled UI.

test.describe('NotebookBrowser (fs surface)', () => {
  test('opens the preferred note into a live editor with no view/edit toggle and autosaves edits', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);

    // The sidebar lists the root as a lazy tree; the preferred note opens into the
    // live editor (heading is the file's basename).
    await expect(page.getByRole('treeitem', { name: 'knowledge' })).toBeVisible();
    await expect(page.getByRole('heading', { level: 2, name: 'index' })).toBeVisible();
    await page.waitForSelector('.cm-content');
    // Rendered inline (heading sized), not a textarea of raw markdown.
    await expect(page.locator('.cm-md-h1').first()).toBeVisible();
    // There is no mode toggle — the surface is always editable.
    await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Save' })).toHaveCount(0);
    // Backlinks render in the right context rail as a card (markdown only).
    const rail = page.locator('.notebook-browser-rail');
    await expect(rail.getByRole('button', { name: '2026-06-20' })).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-browser-open.png' });

    // Type at the end of the document; the debounced autosave persists via hash-CAS.
    await page.locator('.cm-content').click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type(' Extra words.', { delay: 8 });

    await page.waitForFunction(() => window.__HARNESS__.getCalls('writeFile').length > 0, null, {
      timeout: 4000,
    });
    const writes = await page.evaluate(() => window.__HARNESS__.getCalls('writeFile'));
    const last = writes[writes.length - 1] as [string, string, string | undefined];
    expect(last[0]).toBe('knowledge/index.md');
    expect(last[1]).toContain('Extra words.');
    expect(last[2]).toBe('h1'); // saved against the loaded hash
    // The live save indicator reflects the persisted state.
    await expect(page.getByText('Saved')).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-browser-saved.png' });
  });

  test('opens a text file from the tree in a plain editor (no markdown affordances)', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    // Click a non-markdown text file directly in the root tree.
    await page.getByRole('treeitem', { name: 'notes.txt' }).click();

    await expect(page.getByRole('heading', { level: 2, name: 'notes.txt' })).toBeVisible();
    // A plain textarea, not the CodeMirror markdown surface; the context rail (outline
    // + backlinks) is a markdown affordance, so a text file shows no rail.
    await expect(page.getByRole('textbox', { name: 'File contents' })).toBeVisible();
    await expect(page.locator('.notebook-browser-rail')).toHaveCount(0);
  });

  test('lists the note outline in the context rail and scrolls the editor to a heading on click', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();
    await page.waitForSelector('.cm-content');

    // The rail's Outline lists the note's ATX headings, indented by level.
    const rail = page.locator('.notebook-browser-rail');
    await expect(rail.getByRole('button', { name: 'Knowledge index' })).toBeVisible();
    await expect(rail.getByRole('button', { name: 'Sections' })).toBeVisible();
    await expect(rail.getByRole('button', { name: 'Subsection detail' })).toBeVisible();

    // The editor starts at the top; clicking a lower heading scrolls it into view.
    const scrollTop = () =>
      page.locator('.cm-scroller').evaluate((el) => (el as HTMLElement).scrollTop);
    expect(await scrollTop()).toBeLessThan(40);
    await rail.getByRole('button', { name: 'Subsection detail' }).click();
    await expect.poll(scrollTop, { timeout: 2000 }).toBeGreaterThan(150);
  });

  test('keeps the reader scrolled in place when the open note changes on disk (and ignores unrelated changes)', async ({ page }) => {
    // The reported bug, end-to-end through the real NotebookSurface: reading a note
    // while files change scrolled it back to the top. Verified against the actual
    // component + CodeMirror (the packaged app runs this same code).
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();
    await page.waitForSelector('.cm-content');

    const scrollTop = () => page.locator('.cm-scroller').evaluate((el) => (el as HTMLElement).scrollTop);
    // Scroll down into the note.
    await page.locator('.cm-scroller').evaluate((el) => { (el as HTMLElement).scrollTop = 300; });
    const parked = await scrollTop();
    expect(parked).toBeGreaterThan(150);

    // 1) An UNRELATED file changes: fs_changed fires, the open note is re-read but its
    //    bytes are identical, so nothing is applied and the reader does not move.
    await page.evaluate(() => window.__NB_HARNESS__!.fsChanged('journal/2026-06-20.md', '# touched\n', 'h-x'));
    await page.waitForTimeout(150);
    expect(Math.abs((await scrollTop()) - parked)).toBeLessThanOrEqual(4);

    // 2) The OPEN note itself changes on disk (an agent appends a line below the fold).
    //    The new content is applied as a minimal edit, so the reader stays parked.
    await page.evaluate(() => {
      const current = window.__NB_HARNESS__!.getContent('knowledge/index.md');
      window.__NB_HARNESS__!.fsChanged('knowledge/index.md', `${current}\nAppended by an agent while you were reading.\n`, 'h-appended');
    });
    // The append landed in the document (scroll to the bottom to prove it's there) ...
    await expect.poll(async () => {
      // Scroll to the very bottom to prove the appended line is in the document.
      await page.locator('.cm-scroller').evaluate((el) => { (el as HTMLElement).scrollTop = (el as HTMLElement).scrollHeight; });
      return (await page.locator('.cm-content').textContent()) ?? '';
    }, { timeout: 2000 }).toContain('Appended by an agent');

    // ... and crucially, the apply itself did NOT jump the viewport: re-park and confirm
    // a fresh genuine change leaves scrollTop where the reader left it.
    await page.locator('.cm-scroller').evaluate((el) => { (el as HTMLElement).scrollTop = 300; });
    const reparked = await scrollTop();
    expect(reparked).toBeGreaterThan(150);
    await page.evaluate(() => {
      const current = window.__NB_HARNESS__!.getContent('knowledge/index.md');
      window.__NB_HARNESS__!.fsChanged('knowledge/index.md', `${current}\nA second agent edit.\n`, 'h-appended-2');
    });
    await page.waitForTimeout(200);
    expect(Math.abs((await scrollTop()) - reparked)).toBeLessThanOrEqual(4);
  });

  test('shows a read-only placeholder for a binary file', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'cover.png' }).click();

    await expect(page.getByRole('heading', { level: 2, name: 'Preview not available' })).toBeVisible();
    await expect(page.getByRole('textbox')).toHaveCount(0);
  });

  test('Cmd+P summons the fuzzy finder over the modal; typing filters; Enter opens the note', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // No finder until summoned — the modal navigates via the tree by default.
    await expect(page.locator('.notebook-finder')).toHaveCount(0);

    // Cmd+P from inside the modal (focus the editor first, as a user would) opens it.
    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();
    await expect(page.locator('.notebook-finder-input')).toBeFocused();

    // The empty query lists the whole (mocked) vault; typing narrows by path/title.
    await expect(page.locator('.notebook-finder-option')).toHaveCount(3);
    await page.locator('.notebook-finder-input').fill('journal');
    await expect(page.locator('.notebook-finder-option')).toHaveCount(1);
    await expect(page.locator('.notebook-finder-option-path')).toHaveText('journal/2026-06-20.md');

    // Enter opens the highlighted note in the modal's editor and closes the finder.
    await page.keyboard.press('Enter');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await expect(page.getByRole('heading', { level: 2, name: '2026-06-20' })).toBeVisible();
  });

  test('Esc closes the finder before the modal, and Cmd+P re-summons it', async ({ page }) => {
    // The modal's Esc is a capture-phase escape-stack entry; the finder must register a
    // higher-priority entry so the first Esc closes the finder, not the whole modal.
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();

    // First Esc closes only the finder — the modal stays open (no onClose), and focus
    // is restored into the dialog so Cmd+P re-summons without a re-click.
    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toBeVisible();
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBe(0);

    await page.keyboard.press('Meta+p');
    await expect(page.locator('.notebook-finder')).toBeVisible();

    // Close the finder, then a second Esc (no finder open) closes the modal itself.
    await page.keyboard.press('Escape');
    await expect(page.locator('.notebook-finder')).toHaveCount(0);
    await page.keyboard.press('Escape');
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBeGreaterThan(0);
  });

  test('Cmd+F opens the in-editor search panel; Esc closes it before the modal', async ({ page }) => {
    // The search panel gets its own higher-priority escape-stack entry, pushed only
    // while it's open — the first Esc must close just the panel, not the modal.
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    await page.locator('.cm-content').click();
    await page.keyboard.press('Meta+f');
    await expect(page.locator('.cm-panel.cm-search')).toBeVisible();
    await expect(page.locator('.cm-search input[name="search"]')).toBeFocused();

    // Search for a word that repeats throughout the fixture note; matches highlight.
    await page.keyboard.type('distilled');
    await expect(page.locator('.cm-searchMatch').first()).toBeVisible();
    expect(await page.locator('.cm-searchMatch').count()).toBeGreaterThan(0);

    // First Esc closes only the search panel — the modal stays open (no onClose).
    await page.keyboard.press('Escape');
    await expect(page.locator('.cm-panel.cm-search')).toHaveCount(0);
    await expect(page.locator('.notebook-browser')).toBeVisible();
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBe(0);

    // Second Esc (no panel open) closes the modal itself.
    await page.keyboard.press('Escape');
    expect(await page.evaluate(() => window.__HARNESS__.getCalls('close').length)).toBeGreaterThan(0);
  });

  test('renders stage-5 chrome and folds the tree to zero width via the edge handle', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.waitForSelector('.cm-content');

    // Header chrome (folded into the existing header): the chief pulse and a kind badge.
    await expect(page.locator('.notebook-browser-chief-pulse')).toContainText('chief: active');
    await expect(page.locator('.notebook-browser-kind-badge')).toBeVisible();
    await page.screenshot({ path: 'test-results/notebook-stage5-chrome.png' });

    // The tree column has real width, then folds to 0 — the pane stays in the DOM.
    const tree = page.locator('.notebook-browser-list');
    expect(await tree.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(100);
    await page.getByRole('button', { name: 'Hide file tree' }).click();
    await expect(page.locator('.notebook-browser-body')).toHaveClass(/tree-folded/);
    await expect.poll(() => tree.evaluate((el) => el.getBoundingClientRect().width)).toBeLessThan(2);
    await expect(tree).toBeAttached(); // folded, not unmounted

    // The handle now reopens it.
    await page.getByRole('button', { name: 'Show file tree' }).click();
    await expect.poll(() => tree.evaluate((el) => el.getBoundingClientRect().width)).toBeGreaterThan(100);
  });

  test('highlights a code fence, and renders a blockquote and a horizontal rule', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'fences.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'fences' })).toBeVisible();

    // The JS language parser lazy-loads on demand — give it generous room to arrive.
    await expect(page.locator('.cm-md-codeblock .tok-keyword')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('.cm-md-blockquote')).toHaveCount(1);
    await expect(page.locator('.cm-md-hr')).toHaveCount(1);
  });

  test('renders a GFM table as a widget, revealing raw source when clicked', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'fences.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'fences' })).toBeVisible();

    const table = page.locator('.cm-md-table');
    await expect(table).toBeVisible();
    await expect(table.locator('th', { hasText: 'col a' })).toBeVisible();
    await expect(table.locator('td', { hasText: 'two' })).toBeVisible();
    await expect(page.getByText('| one', { exact: false })).not.toBeVisible();

    await table.locator('tbody tr').first().click();
    await expect(table).not.toBeVisible();
    await expect(page.locator('.cm-content')).toContainText('| one');
  });

  test('renders an inline image as a widget wired to readAsset, shows a broken placeholder for a missing asset, and reveals raw source on click', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'images.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'images' })).toBeVisible();

    // The resolvable image renders as a real <img>, sourced from readAsset's bytes as
    // a data: URI (never a bare notebook-relative path — the editor has no fs perms).
    const img = page.locator('.cm-md-image img');
    await expect(img).toBeVisible();
    await expect(img).toHaveAttribute('src', /^data:image\/png;base64,/);
    const checked = await page.evaluate(() => window.__HARNESS__.getCalls('readAsset').map((c) => c[0]));
    expect(checked).toContain('assets/tiny.png');

    // The missing asset shows the broken placeholder, not a blank/broken <img>.
    const broken = page.locator('.cm-md-image-broken');
    await expect(broken).toBeVisible();
    await expect(broken).toContainText('gone');
    await expect(broken).toContainText('image not found');

    // Clicking the rendered widget's line reveals its raw markdown — the
    // regression-prone part: if the selection-intersection reveal rule breaks, the
    // widget never yields back to raw text.
    await page.locator('.cm-md-image').click();
    await expect(page.locator('.cm-md-image')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('![tiny](assets/tiny.png)');

    // Regression: the widget's eq() is deliberately position-blind (alt/src only) so
    // an edit ABOVE it doesn't recreate its DOM (which would flicker/reload the image).
    // But that means the click handler must read its position from the view at click
    // time, not from a value captured when the DOM was built — otherwise editing above
    // the image (shifting it down) leaves a click landing on the stale, pre-edit offset.
    await page.locator('.cm-content').getByText('Images', { exact: true }).click();
    await page.keyboard.press('Home');
    await page.keyboard.type('\n\n');
    await expect(page.locator('.cm-md-image')).toBeVisible();

    await page.locator('.cm-md-image').click();
    await expect(page.locator('.cm-md-image')).toHaveCount(0);
    await expect(page.locator('.cm-content')).toContainText('![tiny](assets/tiny.png)');
  });

  // CodeMirror renders each line as one text node, so a text-content locator can't
  // isolate a single word for dblclick; find the word's on-screen rect via a DOM
  // Range instead and double-click its center.
  async function dblclickWord(page: import('@playwright/test').Page, word: string) {
    const wordRect = await page.evaluate((needle) => {
      const walker = document.createTreeWalker(document.querySelector('.cm-content')!, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        const idx = node.textContent?.indexOf(needle) ?? -1;
        if (idx === -1) continue;
        const range = document.createRange();
        range.setStart(node, idx);
        range.setEnd(node, idx + needle.length);
        const rect = range.getBoundingClientRect();
        return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
      }
      return null;
    }, word);
    if (!wordRect) throw new Error(`could not locate "${word}" in the editor`);
    await page.mouse.dblclick(wordRect.x, wordRect.y);
  }

  test('Cmd+B/I/E toggle bold, italic, and inline code on a double-clicked word', async ({ page }) => {
    await page.goto('/test-harness/?component=NotebookBrowser');
    await page.waitForFunction(() => window.__HARNESS__?.ready === true);
    await page.getByRole('heading', { level: 2, name: 'index' }).waitFor();

    await page.getByRole('treeitem', { name: 'fences.md' }).click();
    await expect(page.getByRole('heading', { level: 2, name: 'fences' })).toBeVisible();

    const content = page.locator('.cm-content');

    await dblclickWord(page, 'fenced');
    await page.keyboard.press('Meta+b');
    await expect(content).toContainText('**fenced**');
    await page.keyboard.press('Meta+b');
    await expect(content).not.toContainText('**fenced**');
    await expect(content).toContainText('A fenced code block');

    // Regression coverage for a real bug: basicSetup's defaultKeymap binds Mod-i to
    // selectParentSyntax with preventDefault, which shadows Cmd-i at default keymap
    // precedence unless formattingKeymap() is raised via Prec.high. Only a real keydown
    // through the full extension stack (not a headless-state unit test) can catch this.
    await dblclickWord(page, 'blockquote');
    await page.keyboard.press('Meta+i');
    await expect(page.locator('.cm-md-em')).toBeVisible();
    await page.keyboard.press('Meta+i');
    await expect(page.locator('.cm-md-em')).toHaveCount(0);
    await expect(content).toContainText('a blockquote');

    await dblclickWord(page, 'horizontal');
    await page.keyboard.press('Meta+e');
    await expect(page.locator('.cm-md-code')).toBeVisible();
    await page.keyboard.press('Meta+e');
    await expect(page.locator('.cm-md-code')).toHaveCount(0);
    await expect(content).toContainText('a horizontal rule');
  });
});
