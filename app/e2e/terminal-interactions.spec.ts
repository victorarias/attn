import { test, expect } from './fixtures';

async function createSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (session: { id: string; label: string; state: string; directory?: string; workspace_id?: string }) => Promise<void> },
  id: string,
) {
  const workspaceId = `workspace-${id}`;
  await page.evaluate(({ sessionId, workspaceId }) => {
    window.__TEST_INJECT_SESSION?.({
      id: sessionId,
      label: 'Terminal Links',
      state: 'working',
      cwd: '/tmp/test/terminal-links',
      workspaceId,
    });
  }, { sessionId: id, workspaceId });
  await daemon.injectSession({
    id,
    label: 'Terminal Links',
    state: 'working',
    directory: '/tmp/test/terminal-links',
    workspace_id: workspaceId,
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

async function expectTerminalInputCount(
  page: import('@playwright/test').Page,
  sessionId: string,
  data: string,
  count: number,
) {
  await expect
    .poll(
      async () => page.evaluate(
        ({ id, expectedData }) => (
          window.__TEST_GET_SESSION_INPUT_EVENTS?.(id) ?? []
        ).filter((event) => event.event === 'send_to_pty' && event.data === expectedData).length,
        { id: sessionId, expectedData: data },
      ),
      { timeout: 3000 },
    )
    .toBe(count);
}

async function openTerminalSession(
  page: import('@playwright/test').Page,
  daemon: { injectSession: (session: { id: string; label: string; state: string; directory?: string; workspace_id?: string }) => Promise<void> },
  sessionId: string,
) {
  await daemon.start();
  await page.goto('/');
  await page.waitForSelector('.dashboard');
  await createSession(page, daemon, sessionId);
  await page.locator(`[data-testid="session-${sessionId}"]`).click();
  const terminal = page.locator(`[data-pane-session-id="${sessionId}"][data-pane-kind="agent"] .terminal-container`);
  await expect(terminal).toBeVisible({ timeout: 5000 });
  return terminal;
}

test.describe('Ghostty terminal interactions', () => {
  test('opens a visible URL only when cmd+clicked', async ({ page, daemon }) => {
    await installOpenerProbe(page);
    const terminal = await openTerminalSession(page, daemon, 's-link');
    const url = 'https://example.test/terminal-link';
    await writeTerminalOutput(page, 's-link', `\u001b[2J\u001b[H${url}`);

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-link') ?? ''),
        { timeout: 5000 },
      )
      .toContain(url);

    await terminal.hover({ position: { x: 100, y: 8 } });
    await expect(terminal).toHaveCSS('cursor', 'text');
    await terminal.click({ position: { x: 100, y: 8 } });
    await expect
      .poll(
        async () => page.evaluate(
          () => (window as Window & { __OPENED_TERMINAL_URLS?: string[] }).__OPENED_TERMINAL_URLS ?? [],
        ),
        { timeout: 500 },
      )
      .toEqual([]);
    await page.keyboard.down('Meta');
    await expect(terminal).toHaveCSS('cursor', 'pointer');
    await terminal.click({ position: { x: 100, y: 8 } });
    await page.keyboard.up('Meta');

    await expectOpenedUrl(page, url);
  });

  test('hit-tests URL clicks against the rendered canvas when it is vertically offset', async ({ page, daemon }) => {
    await installOpenerProbe(page);
    const terminal = await openTerminalSession(page, daemon, 's-link-offset');
    await page.addStyleTag({
      content: '.terminal-container canvas { margin-top: 18px !important; }',
    });
    const url = 'https://example.test/offset-link';
    await writeTerminalOutput(page, 's-link-offset', `\u001b[2J\u001b[Hnot-a-link\u001b[2;1H${url}`);

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-link-offset') ?? ''),
        { timeout: 5000 },
      )
      .toContain(url);

    const rowTargets = await terminal.evaluate((element, sessionId) => {
      const canvas = element.querySelector('canvas');
      if (!canvas) {
        throw new Error('Terminal canvas not found');
      }
      const paneSize = (window as Window & {
        __TEST_GET_SESSION_PANE_SIZE?: (id: string) => { rows: number } | null;
      }).__TEST_GET_SESSION_PANE_SIZE?.(sessionId);
      if (!paneSize?.rows) {
        throw new Error('Terminal pane size not available');
      }
      const terminalRect = element.getBoundingClientRect();
      const canvasRect = canvas.getBoundingClientRect();
      const rowHeight = canvasRect.height / paneSize.rows;
      const canvasTop = canvasRect.top - terminalRect.top;
      return {
        firstRowY: canvasTop + rowHeight * 0.5,
        secondRowY: canvasTop + rowHeight * 1.5,
      };
    }, 's-link-offset');

    await page.keyboard.down('Meta');
    await terminal.click({ position: { x: 100, y: rowTargets.firstRowY } });
    await expect
      .poll(
        async () => page.evaluate(
          () => (window as Window & { __OPENED_TERMINAL_URLS?: string[] }).__OPENED_TERMINAL_URLS ?? [],
        ),
        { timeout: 500 },
      )
      .toEqual([]);

    await terminal.click({ position: { x: 100, y: rowTargets.secondRowY } });
    await page.keyboard.up('Meta');

    await expectOpenedUrl(page, url);
  });

  test('cmd+click opens a visible URL while terminal mouse tracking is active', async ({ page, daemon }) => {
    await installOpenerProbe(page);
    const terminal = await openTerminalSession(page, daemon, 's-link-tracked');
    const url = 'https://example.test/tracked-link';
    await writeTerminalOutput(page, 's-link-tracked', `\u001b[2J\u001b[H\u001b[?1000h${url} `);

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-link-tracked') ?? ''),
        { timeout: 5000 },
      )
      .toContain(url);

    await terminal.hover({ position: { x: 100, y: 8 } });
    await expect(terminal).toHaveCSS('cursor', 'text');
    await page.keyboard.down('Meta');
    await expect(terminal).toHaveCSS('cursor', 'pointer');
    await terminal.click({ position: { x: 100, y: 8 } });
    await page.keyboard.up('Meta');

    await expectOpenedUrl(page, url);
  });

  test('forwards screenshot paste triggers from ctrl+v and command paste', async ({ page, daemon }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true });
      Object.defineProperty(navigator, 'userAgent', { value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X)', configurable: true });
    });
    const terminal = await openTerminalSession(page, daemon, 's-image-paste');
    await writeTerminalOutput(page, 's-image-paste', '\u001b[2J\u001b[Hready');
    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-image-paste') ?? ''),
        { timeout: 5000 },
      )
      .toContain('ready');

    await terminal.focus();
    await page.keyboard.press('Control+v');
    await expectTerminalInputCount(page, 's-image-paste', '\u0016', 1);

    await terminal.evaluate((element) => {
      const data = new DataTransfer();
      data.items.add(new File(['image'], 'screenshot.png', { type: 'image/png' }));
      element.dispatchEvent(new ClipboardEvent('paste', {
        bubbles: true,
        cancelable: true,
        clipboardData: data,
      }));
    });
    await expectTerminalInputCount(page, 's-image-paste', '\u0016', 2);

    await terminal.evaluate((element) => {
      const data = new DataTransfer();
      data.setData('text/plain', 'pasted text');
      element.dispatchEvent(new ClipboardEvent('paste', {
        bubbles: true,
        cancelable: true,
        clipboardData: data,
      }));
    });
    await expectTerminalInputCount(page, 's-image-paste', 'pasted text', 1);
  });

  test('leaves ctrl+v text paste available on non-mac platforms', async ({ page, daemon }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'platform', { value: 'Linux x86_64', configurable: true });
      Object.defineProperty(navigator, 'userAgent', { value: 'Mozilla/5.0 (X11; Linux x86_64)', configurable: true });
    });
    const terminal = await openTerminalSession(page, daemon, 's-text-paste-linux');
    await terminal.focus();

    await page.keyboard.press('Control+v');
    await expectTerminalInputCount(page, 's-text-paste-linux', '\u0016', 0);

    await terminal.evaluate((element) => {
      const data = new DataTransfer();
      data.setData('text/plain', 'linux pasted text');
      element.dispatchEvent(new ClipboardEvent('paste', {
        bubbles: true,
        cancelable: true,
        clipboardData: data,
      }));
    });
    await expectTerminalInputCount(page, 's-text-paste-linux', 'linux pasted text', 1);
  });

  test('double-click selects and copies a terminal word', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-double-click');
    await writeTerminalOutput(page, 's-double-click', '\u001b[2J\u001b[Hselectable-word suffix');

    await expect
      .poll(
        async () => page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-double-click') ?? ''),
        { timeout: 5000 },
      )
      .toContain('selectable-word');

    await terminal.dblclick({ position: { x: 55, y: 8 } });

    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('selectable-word');
  });

  test('does not create a selection from click-sized pointer jitter', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-click-jitter');
    await writeTerminalOutput(page, 's-click-jitter', '\u001b[2J\u001b[Hclick-jitter-target');
    await page.evaluate(() => navigator.clipboard.writeText('clipboard-sentinel'));

    const bounds = await terminal.boundingBox();
    expect(bounds).not.toBeNull();
    await page.mouse.move(bounds!.x + 55, bounds!.y + 8);
    await page.mouse.down();
    await page.mouse.move(bounds!.x + 56, bounds!.y + 8);
    await page.mouse.up();

    await expect.poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toBe('clipboard-sentinel');
  });

  test('option-drag selects text while terminal mouse tracking is active', async ({ page, context, daemon }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const terminal = await openTerminalSession(page, daemon, 's-option-selection');
    const text = 'select while tracked';
    await writeTerminalOutput(page, 's-option-selection', `\u001b[2J\u001b[H\u001b[?1000h${text}`);

    await terminal.hover({ position: { x: 2, y: 8 } });
    await page.keyboard.down('Alt');
    await page.mouse.down();
    await terminal.hover({ position: { x: 200, y: 8 } });
    await page.mouse.up();
    await page.keyboard.up('Alt');

    await expect
      .poll(async () => page.evaluate(() => navigator.clipboard.readText()), { timeout: 3000 })
      .toContain(text);
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

    const visibleText = await page.evaluate(() => window.__TEST_GET_SESSION_PANE_TEXT?.('s-selection-scroll') ?? '');
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
