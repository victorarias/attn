import { test, expect } from './fixtures';

async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (session: { id: string; label: string; state: string; directory?: string }) => Promise<void> },
  id: string,
) {
  await page.evaluate((sessionId) => {
    window.__TEST_INJECT_SESSION?.({
      id: sessionId,
      label: 'Terminal Links',
      state: 'working',
      cwd: '/tmp/test/terminal-links',
    });
  }, id);
  await daemon.injectSession({
    id,
    label: 'Terminal Links',
    state: 'working',
    directory: '/tmp/test/terminal-links',
  });
}

async function writeTerminalOutput(
  page: import('@playwright/test').Page,
  sessionId: string,
  output: string,
) {
  await page.evaluate(({ id, data }) => {
    window.__TEST_EMIT_PTY_DATA?.(id, data);
  }, { id: sessionId, data: output });
}

async function installOpenerProbe(page: import('@playwright/test').Page) {
  await page.addInitScript(() => {
    (window as Window & { __OPENED_TERMINAL_URLS?: string[] }).__OPENED_TERMINAL_URLS = [];
    (window as Window & {
      __TAURI_INTERNALS__?: { invoke: (command: string, args: { url?: string }) => Promise<void> };
    }).__TAURI_INTERNALS__ = {
      invoke: async (command, args) => {
        if (command === 'plugin:opener|open_url' && args.url) {
          (window as Window & { __OPENED_TERMINAL_URLS?: string[] }).__OPENED_TERMINAL_URLS?.push(args.url);
        }
      },
    };
  });
}

async function expectOpenedUrl(page: import('@playwright/test').Page, url: string) {
  await expect
    .poll(
      async () => page.evaluate(
        () => (window as Window & { __OPENED_TERMINAL_URLS?: string[] }).__OPENED_TERMINAL_URLS ?? [],
      ),
      { timeout: 3000 },
    )
    .toContain(url);
}

async function openTerminalSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (session: { id: string; label: string; state: string; directory?: string }) => Promise<void> },
  sessionId: string,
) {
  await daemon.start();
  await page.goto('/');
  await page.waitForSelector('.dashboard');
  await createSession(page, daemon, sessionId);
  await page.locator(`[data-testid="session-${sessionId}"]`).click();
  const terminal = page.locator(`[data-pane-session-id="${sessionId}"][data-pane-id="main"] .terminal-container`);
  await expect(terminal).toBeVisible({ timeout: 5000 });
  return terminal;
}

test.describe('Ghostty terminal interactions', () => {
  test('opens a visible URL when clicked', async ({ page, daemon }) => {
    await installOpenerProbe(page);
    const terminal = await openTerminalSession(page, daemon, 's-link');
    const url = 'https://example.test/terminal-link';
    await writeTerminalOutput(page, 's-link', `\u001b[2J\u001b[H${url}`);

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_MAIN_TERMINAL_TEXT?.('s-link') ?? ''),
        { timeout: 5000 },
      )
      .toContain(url);

    await terminal.click({ position: { x: 100, y: 8 } });

    await expectOpenedUrl(page, url);
  });

  test('cmd+click opens a visible URL while terminal mouse tracking is active', async ({ page, daemon }) => {
    await installOpenerProbe(page);
    const terminal = await openTerminalSession(page, daemon, 's-link-tracked');
    const url = 'https://example.test/tracked-link';
    await writeTerminalOutput(page, 's-link-tracked', `\u001b[2J\u001b[H\u001b[?1000h${url}`);

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_MAIN_TERMINAL_TEXT?.('s-link-tracked') ?? ''),
        { timeout: 5000 },
      )
      .toContain(url);

    await terminal.click({ modifiers: ['Meta'], position: { x: 100, y: 8 } });

    await expectOpenedUrl(page, url);
  });

  test('copies selected terminal text without opening a dragged URL', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await installOpenerProbe(page);
    const terminal = await openTerminalSession(page, daemon, 's-copy');
    const url = 'https://example.test/copied-link';
    await writeTerminalOutput(page, 's-copy', `\u001b[2J\u001b[H${url}`);

    await terminal.hover({ position: { x: 2, y: 8 } });
    await page.mouse.down();
    await terminal.hover({ position: { x: 500, y: 8 } });
    await page.mouse.up();

    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toContain(url);
    await expect
      .poll(
        async () => page.evaluate(
          () => (window as Window & { __OPENED_TERMINAL_URLS?: string[] }).__OPENED_TERMINAL_URLS ?? [],
        ),
        { timeout: 500 },
      )
      .toEqual([]);
  });

  test('keeps copied selection attached to its text while scrolling', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-selection-scroll');
    const anchor = 'SELECTED_ANCHOR_LINE';
    const lines = Array.from({ length: 80 }, (_, index) => (
      index === 79 ? anchor : `SCROLL_LINE_${String(index).padStart(3, '0')}`
    )).join('\r\n');
    await writeTerminalOutput(page, 's-selection-scroll', `\u001b[2J\u001b[H${lines}`);

    const visibleText = await page.evaluate(() => window.__TEST_GET_MAIN_TERMINAL_TEXT?.('s-selection-scroll') ?? '');
    expect(visibleText).toContain(anchor);

    const terminalBounds = await terminal.boundingBox();
    expect(terminalBounds).not.toBeNull();
    const rowY = terminalBounds!.height - 8;
    await terminal.hover({ position: { x: 2, y: rowY } });
    await page.mouse.down();
    await terminal.hover({ position: { x: 220, y: rowY } });
    await page.mouse.up();
    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toContain(anchor);

    await terminal.hover({ position: { x: 20, y: 8 } });
    await page.mouse.wheel(0, -500);
    await page.keyboard.press('Meta+Shift+c');

    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toContain(anchor);
  });
});
